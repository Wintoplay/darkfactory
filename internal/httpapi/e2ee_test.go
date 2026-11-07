package httpapi

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

type testE2EEClient struct {
	base        string
	deviceID    string
	token       string
	clientB64   string
	hostB64     string
	clientToSrv []byte
	srvToClient []byte
}

func newTestE2EEClient(t *testing.T, srvURL, master string) *testE2EEClient {
	t.Helper()

	hostResp, err := http.Get(srvURL + "/api/v1/e2ee/host-key")
	if err != nil {
		t.Fatal(err)
	}
	defer hostResp.Body.Close()
	if hostResp.StatusCode != http.StatusOK {
		t.Fatalf("host key status = %d", hostResp.StatusCode)
	}
	var hostKey struct {
		PublicKey string `json:"publicKey"`
		HostID    string `json:"hostId"`
	}
	if err := json.NewDecoder(hostResp.Body).Decode(&hostKey); err != nil {
		t.Fatal(err)
	}

	clientPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientDER, err := x509.MarshalPKIXPublicKey(clientPrivate.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	clientB64 := base64.StdEncoding.EncodeToString(clientDER)

	codeResp := doJSON(t, http.MethodPost, srvURL+"/api/v1/pair/code", master, map[string]any{
		"label": "e2ee-test",
		"scope": "full",
	})
	defer codeResp.Body.Close()
	var codeBody struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(codeResp.Body).Decode(&codeBody); err != nil {
		t.Fatal(err)
	}
	if codeBody.Code == "" {
		t.Fatalf("pair code = %d", codeResp.StatusCode)
	}

	pairResp := doJSON(t, http.MethodPost, srvURL+"/api/v1/pair", "", map[string]any{
		"code":            codeBody.Code,
		"clientPublicKey": clientB64,
	})
	defer pairResp.Body.Close()
	if pairResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(pairResp.Body)
		t.Fatalf("pair status = %d: %s", pairResp.StatusCode, raw)
	}
	var pair struct {
		DeviceID      string `json:"deviceId"`
		HostID        string `json:"hostId"`
		HostPublicKey string `json:"hostPublicKey"`
		SealedToken   string `json:"sealedToken"`
		Token         string `json:"token"`
	}
	if err := json.NewDecoder(pairResp.Body).Decode(&pair); err != nil {
		t.Fatal(err)
	}
	if pair.SealedToken == "" || pair.Token != "" {
		t.Fatalf("pair did not use sealed token: %+v", pair)
	}
	if cookies := pairResp.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("sealed pairing leaked a Set-Cookie: %v", cookies)
	}
	if pair.HostID != hostKey.HostID || pair.HostPublicKey != hostKey.PublicKey {
		t.Fatal("pair host key does not match host-key endpoint")
	}

	hostDER, err := base64.StdEncoding.DecodeString(pair.HostPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParsePKIXPublicKey(hostDER)
	if err != nil {
		t.Fatal(err)
	}
	var hostPublic *ecdh.PublicKey
	switch key := parsed.(type) {
	case *ecdh.PublicKey:
		hostPublic = key
	case *ecdsa.PublicKey:
		hostPublic, err = ecdh.P256().NewPublicKey(elliptic.Marshal(elliptic.P256(), key.X, key.Y))
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("host key is not a public key")
	}
	c2s, s2c := clientDerive(t, clientPrivate, hostPublic, pair.HostPublicKey, clientB64)

	sealed, err := base64.StdEncoding.DecodeString(pair.SealedToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) < 12 {
		t.Fatal("sealed token is too short")
	}
	tokenPlain, err := openE2EE(s2c, base64.StdEncoding.EncodeToString(sealed[:12]), base64.StdEncoding.EncodeToString(sealed[12:]), e2eeAAD("pair", pair.HostID, pair.DeviceID, clientB64))
	if err != nil {
		t.Fatal(err)
	}
	return &testE2EEClient{
		base:        srvURL,
		deviceID:    pair.DeviceID,
		token:       string(tokenPlain),
		clientB64:   clientB64,
		hostB64:     pair.HostPublicKey,
		clientToSrv: c2s,
		srvToClient: s2c,
	}
}

func clientDerive(t *testing.T, client *ecdh.PrivateKey, host *ecdh.PublicKey, hostB64, clientB64 string) ([]byte, []byte) {
	t.Helper()
	shared, err := client.ECDH(host)
	if err != nil {
		t.Fatal(err)
	}
	salt := sha256.Sum256([]byte(hostB64 + "|" + clientB64))
	derived, err := hkdf.Key(sha256.New, shared, salt[:], e2eeKeyInfo, 64)
	if err != nil {
		t.Fatal(err)
	}
	return derived[:32], derived[32:]
}

func (c *testE2EEClient) request(t *testing.T, method, path string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	requestID := make([]byte, 16)
	if _, err := rand.Read(requestID); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(requestID)
	ts := time.Now().UnixMilli()
	plain, err := json.Marshal(e2eeRequest{
		Method: method,
		Path:   path,
		Body:   body,
		Token:  c.token,
	})
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, err := sealE2EE(c.clientToSrv, plain, e2eeAAD("req", c.deviceID, id, strconv.FormatInt(ts, 10)))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(e2eeEnvelope{
		V:          e2eeVersion,
		DeviceID:   c.deviceID,
		ID:         id,
		TS:         ts,
		Nonce:      nonce,
		Ciphertext: sealed,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/v1/e2ee", bytes.NewReader(envelope))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("e2ee status = %d: %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, e2eeResponseType) {
		t.Fatalf("response type = %q", got)
	}

	var status int
	var headers http.Header
	var bodyOut []byte
	for frameIndex := 0; ; frameIndex++ {
		var length [4]byte
		if _, err := io.ReadFull(resp.Body, length[:]); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(resp.Body, frame); err != nil {
			t.Fatal(err)
		}
		if len(frame) < 13 {
			t.Fatalf("frame %d is too short", frameIndex)
		}
		plain, err := openE2EE(
			c.srvToClient,
			base64.StdEncoding.EncodeToString(frame[:12]),
			base64.StdEncoding.EncodeToString(frame[12:]),
			[]byte("resp|"+c.deviceID+"|"+id+"|"+strconv.Itoa(frameIndex)),
		)
		if err != nil {
			t.Fatalf("frame %d: %v", frameIndex, err)
		}
		if frameIndex == 0 {
			var head e2eeResponseHead
			if err := json.Unmarshal(plain, &head); err != nil {
				t.Fatal(err)
			}
			status = head.Status
			headers = make(http.Header)
			for name, value := range head.Headers {
				headers.Set(name, value)
			}
			continue
		}
		bodyOut = append(bodyOut, plain...)
	}
	return status, headers, bodyOut
}

func TestE2EEPairAndState(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	client := newTestE2EEClient(t, srv.URL, "test-token")

	status, headers, body := client.request(t, http.MethodGet, "/api/v1/state", nil)
	if status != http.StatusOK {
		t.Fatalf("state status = %d: %s", status, body)
	}
	if headers.Get("Content-Type") != "application/json" {
		t.Fatalf("state content type = %q", headers.Get("Content-Type"))
	}
	if !bytes.Contains(body, []byte(`"hostId"`)) || !bytes.Contains(body, []byte(`"build"`)) {
		t.Fatalf("state body did not look like state: %s", body)
	}

	// A bad inner token is refused before the encrypted stream starts.
	bad := *client
	bad.token = "not-the-token"
	body, err := bad.rawEnvelope(t, http.MethodGet, "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/e2ee", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", resp.StatusCode)
	}
}

func TestE2EERejectsReplayAndTamper(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	client := newTestE2EEClient(t, srv.URL, "test-token")

	requestID := make([]byte, 16)
	if _, err := rand.Read(requestID); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(requestID)
	ts := time.Now().UnixMilli()
	plain, _ := json.Marshal(e2eeRequest{Method: http.MethodGet, Path: "/api/v1/state", Token: client.token})
	nonce, sealed, err := sealE2EE(client.clientToSrv, plain, e2eeAAD("req", client.deviceID, id, strconv.FormatInt(ts, 10)))
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(e2eeEnvelope{V: 1, DeviceID: client.deviceID, ID: id, TS: ts, Nonce: nonce, Ciphertext: sealed})

	post := func(raw []byte) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/e2ee", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := post(envelope); status != http.StatusOK {
		t.Fatalf("first envelope = %d", status)
	}
	if status := post(envelope); status != http.StatusBadRequest {
		t.Fatalf("replay = %d, want 400", status)
	}

	// Corrupt one ciphertext byte; the AEAD must reject it before any inner
	// handler sees the request.
	var tampered e2eeEnvelope
	_ = json.Unmarshal(envelope, &tampered)
	raw, _ := base64.StdEncoding.DecodeString(tampered.Ciphertext)
	raw[0] ^= 1
	tampered.Ciphertext = base64.StdEncoding.EncodeToString(raw)
	rawEnvelope, _ := json.Marshal(tampered)
	if status := post(rawEnvelope); status != http.StatusBadRequest {
		t.Fatalf("tampered = %d, want 400", status)
	}
}

func TestE2EEHostKeySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	first, err := loadOrCreateE2EE(st)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateE2EE(st)
	if err != nil {
		t.Fatal(err)
	}
	if first.publicB64 != second.publicB64 {
		t.Fatal("host key changed across load")
	}
}

func TestE2EEReplayStorageFailureRefusesConsumption(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	now := time.Now()
	accepted, err := s.consumeE2EEReplay("device", "request", now, now.Add(e2eeClockSkew))
	if err == nil || accepted {
		t.Fatalf("closed store accepted request: accepted=%v err=%v", accepted, err)
	}
}

// TestE2EEReplayRefusedAcrossDaemonRestart is the end-to-end counterpart to the
// store test: the request ID must survive a real server restart, not only a
// database reopen. It also covers a future timestamp inside the skew window,
// whose record has to outlive its own request by the full window.
func TestE2EEReplayRefusedAcrossDaemonRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.InstanceSecret = "e2ee-restart-test-secret"
	cfg.WorktreeRoot = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	type daemon struct {
		srv *httptest.Server
		st  *store.Store
		mgr *session.Manager
	}
	start := func() daemon {
		st, err := store.Open(dir + "/state.db")
		if err != nil {
			t.Fatal(err)
		}
		mgr := session.NewManager(1024)
		srv := httptest.NewServer(New(cfg, st, mgr, log).Handler())
		return daemon{srv: srv, st: st, mgr: mgr}
	}
	stop := func(d daemon) {
		d.srv.Close()
		d.mgr.CloseAll()
		if err := d.st.Close(); err != nil {
			t.Fatal(err)
		}
	}

	first := start()
	client := newTestE2EEClient(t, first.srv.URL, cfg.Token)
	now := time.Now()
	present := client.rawEnvelopeAt(t, http.MethodGet, "/api/v1/state", now)
	future := client.rawEnvelopeAt(t, http.MethodGet, "/api/v1/state", now.Add(4*time.Minute))
	if status := postE2EEEnvelope(t, first.srv.URL, present); status != http.StatusOK {
		t.Fatalf("present envelope = %d", status)
	}
	if status := postE2EEEnvelope(t, first.srv.URL, future); status != http.StatusOK {
		t.Fatalf("future envelope = %d", status)
	}
	stop(first)

	second := start()
	defer stop(second)
	if status := postE2EEEnvelope(t, second.srv.URL, present); status != http.StatusBadRequest {
		t.Fatalf("replay after restart = %d, want 400", status)
	}
	if status := postE2EEEnvelope(t, second.srv.URL, future); status != http.StatusBadRequest {
		t.Fatalf("future replay after restart = %d, want 400", status)
	}
	fresh := client.rawEnvelopeAt(t, http.MethodGet, "/api/v1/state", time.Now())
	if status := postE2EEEnvelope(t, second.srv.URL, fresh); status != http.StatusOK {
		t.Fatalf("fresh envelope after restart = %d, want 200", status)
	}
}

// TestE2EERefusesEncodedNestedEnvelope pins the anti-recursion guard to the
// decoded path. "%652ee" is "e2ee" to both ParseRequestURI and ServeMux, so a
// raw-string check would dispatch a second envelope through this handler.
func TestE2EERefusesEncodedNestedEnvelope(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	client := newTestE2EEClient(t, srv.URL, "test-token")
	now := time.Now()

	nested := client.rawEnvelopeAt(t, http.MethodGet, "/api/v1/state", now)
	for _, path := range []string{"/api/v1/%652ee", "/api/v1/%65%32%65%65", "/api/v1/e2ee", "/api/v1/e2ee/state"} {
		outer, err := client.sealedEnvelope(http.MethodPost, path, nested, now)
		if err != nil {
			t.Fatal(err)
		}
		if status := postE2EEEnvelope(t, srv.URL, outer); status != http.StatusBadRequest {
			t.Fatalf("%s: outer status = %d, want 400", path, status)
		}
	}
	// None of the refused outers dispatched the inner request, so its id is
	// still unused and it must succeed when sent directly.
	if status := postE2EEEnvelope(t, srv.URL, nested); status != http.StatusOK {
		t.Fatalf("nested request was dispatched (status %d), want 200", status)
	}
}

func TestE2EEEnvelopeBodyLimit(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	oversized := bytes.Repeat([]byte("x"), e2eeMaxBody+1)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/e2ee", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized envelope = %d, want 413", resp.StatusCode)
	}
}

func postE2EEEnvelope(t *testing.T, base string, raw []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/e2ee", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (c *testE2EEClient) rawEnvelope(t *testing.T, method, path string) ([]byte, error) {
	t.Helper()
	return c.envelopeAt(method, path, time.Now())
}

func (c *testE2EEClient) rawEnvelopeAt(t *testing.T, method, path string, ts time.Time) []byte {
	t.Helper()
	raw, err := c.envelopeAt(method, path, ts)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (c *testE2EEClient) envelopeAt(method, path string, ts time.Time) ([]byte, error) {
	return c.sealedEnvelope(method, path, nil, ts)
}

func (c *testE2EEClient) sealedEnvelope(method, path string, body []byte, ts time.Time) ([]byte, error) {
	requestID := make([]byte, 16)
	if _, err := rand.Read(requestID); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(requestID)
	stamp := strconv.FormatInt(ts.UnixMilli(), 10)
	plain, err := json.Marshal(e2eeRequest{Method: method, Path: path, Body: body, Token: c.token})
	if err != nil {
		return nil, err
	}
	nonce, sealed, err := sealE2EE(c.clientToSrv, plain, e2eeAAD("req", c.deviceID, id, stamp))
	if err != nil {
		return nil, err
	}
	return json.Marshal(e2eeEnvelope{V: e2eeVersion, DeviceID: c.deviceID, ID: id, TS: ts.UnixMilli(), Nonce: nonce, Ciphertext: sealed})
}

func doJSON(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
