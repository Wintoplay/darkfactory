package httpapi

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	cfg.E2EEMinSuite = 1 // This test exercises explicit legacy compatibility.
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

type testE2EEV2Client struct {
	base       string
	deviceID   string
	hostID     string
	token      string
	bundle     e2eeBundle
	bundleHash [32]byte
}

func fetchE2EEV2Bundle(t *testing.T, srvURL string) (e2eeBundle, string, [32]byte) {
	t.Helper()
	resp, err := http.Get(srvURL + "/api/v1/e2ee/host-key")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host key status = %d", resp.StatusCode)
	}
	var hostKey struct {
		HostID string     `json:"hostId"`
		Bundle e2eeBundle `json:"bundle"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hostKey); err != nil {
		t.Fatal(err)
	}
	if err := verifyE2EEBundle(hostKey.Bundle); err != nil {
		t.Fatalf("host bundle did not verify: %v", err)
	}
	payload, err := hostKey.Bundle.canonical()
	if err != nil {
		t.Fatal(err)
	}
	return hostKey.Bundle, hostKey.HostID, sha256.Sum256(payload)
}

func newTestE2EEV2Client(t *testing.T, srvURL, master string) *testE2EEV2Client {
	t.Helper()
	bundle, hostID, bundleHash := fetchE2EEV2Bundle(t, srvURL)

	codeResp := doJSON(t, http.MethodPost, srvURL+"/api/v1/pair/code", master, map[string]any{
		"label": "e2ee-v2-test",
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

	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ekRaw, err := base64.StdEncoding.DecodeString(bundle.MLKEM)
	if err != nil {
		t.Fatal(err)
	}
	ek, err := mlkem.NewEncapsulationKey768(ekRaw)
	if err != nil {
		t.Fatal(err)
	}
	ssKEM, kemCT := ek.Encapsulate()
	epkRaw := eph.PublicKey().Bytes()
	sealedCode := sealPairCodeV2ForTest(t, bundle, hostID, eph, kemCT, ssKEM, codeBody.Code)

	pairResp := doJSON(t, http.MethodPost, srvURL+"/api/v1/pair", "", map[string]any{
		"sealedCode": sealedCode,
		"v":          e2eeVersionV2,
		"suite":      e2eeSuiteV2,
		"epk":        base64.StdEncoding.EncodeToString(epkRaw),
		"kem":        base64.StdEncoding.EncodeToString(kemCT),
	})
	defer pairResp.Body.Close()
	if pairResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(pairResp.Body)
		t.Fatalf("v2 pair status = %d: %s", pairResp.StatusCode, raw)
	}
	var pair struct {
		DeviceID      string     `json:"deviceId"`
		HostID        string     `json:"hostId"`
		Suite         string     `json:"suite"`
		SealedPayload string     `json:"sealedPayload"`
		SealedToken   string     `json:"sealedToken"`
		Token         string     `json:"token"`
		Bundle        e2eeBundle `json:"bundle"`
	}
	if err := json.NewDecoder(pairResp.Body).Decode(&pair); err != nil {
		t.Fatal(err)
	}
	if pair.SealedPayload == "" || pair.SealedToken != "" || pair.Token != "" {
		t.Fatalf("v2 pair did not seal the payload: %+v", pair)
	}
	if cookies := pairResp.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("v2 pairing leaked a Set-Cookie: %v", cookies)
	}
	if pair.Suite != e2eeSuiteV2 || pair.HostID != hostID {
		t.Fatalf("v2 pair metadata = %+v", pair)
	}
	if err := verifyE2EEBundle(pair.Bundle); err != nil {
		t.Fatalf("pair bundle did not verify: %v", err)
	}

	hostPub, err := ecdh.X25519().NewPublicKey(mustBase64(t, bundle.X25519))
	if err != nil {
		t.Fatal(err)
	}
	ssDH, err := eph.ECDH(hostPub)
	if err != nil {
		t.Fatal(err)
	}
	ikm := append(append([]byte{}, ssDH...), ssKEM...)
	material := sha256.Sum256(append(append([]byte{}, epkRaw...), kemCT...))
	transcript := e2eeV2Transcript(e2eePairDomainV2, bundleHash[:], []byte(hostID), []byte(pair.DeviceID), material[:])
	derived, err := hkdf.Key(sha256.New, ikm, transcript, e2eeKeyInfoV2, 64)
	if err != nil {
		t.Fatal(err)
	}
	sealedRaw, err := base64.StdEncoding.DecodeString(pair.SealedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealedRaw) < 12+16 {
		t.Fatal("sealed payload is too short")
	}
	plain, err := openE2EE(
		derived[32:],
		base64.StdEncoding.EncodeToString(sealedRaw[:12]),
		base64.StdEncoding.EncodeToString(sealedRaw[12:]),
		transcript,
	)
	if err != nil {
		t.Fatalf("open sealed payload: %v", err)
	}
	var payload e2eeSealedPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.DeviceID != pair.DeviceID ||
		payload.HostID != hostID || payload.Suite != e2eeSuiteV2 {
		t.Fatalf("sealed payload = %+v", payload)
	}
	return &testE2EEV2Client{
		base:       srvURL,
		deviceID:   pair.DeviceID,
		hostID:     hostID,
		token:      payload.Token,
		bundle:     bundle,
		bundleHash: bundleHash,
	}
}

func sealPairCodeV2ForTest(t *testing.T, bundle e2eeBundle, hostID string, eph *ecdh.PrivateKey, kemCT, ssKEM []byte, code string) string {
	t.Helper()
	ssDH, err := eph.ECDH(mustX25519Public(t, bundle.X25519))
	if err != nil {
		t.Fatal(err)
	}
	material := sha256.Sum256(append(append([]byte{}, eph.PublicKey().Bytes()...), kemCT...))
	bundleBytes, err := bundle.canonical()
	if err != nil {
		t.Fatal(err)
	}
	bundleHash := sha256.Sum256(bundleBytes)
	transcript := e2eeV2Transcript("darkfactory-e2ee-pair-init-v2", bundleHash[:], []byte(hostID), material[:])
	ikm := append(append([]byte{}, ssDH...), ssKEM...)
	key, err := hkdf.Key(sha256.New, ikm, transcript, e2eeKeyInfoV2, 32)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(code), transcript)
	return base64.StdEncoding.EncodeToString(append(nonce, sealed...))
}

func mustX25519Public(t *testing.T, encoded string) *ecdh.PublicKey {
	t.Helper()
	key, err := ecdh.X25519().NewPublicKey(mustBase64(t, encoded))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustBase64(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// sealedV2 builds one envelope and returns it with the response key and id.
func (c *testE2EEV2Client) sealedV2(t *testing.T, method, path string, body []byte, ts time.Time) ([]byte, []byte, string) {
	t.Helper()
	idRaw := make([]byte, 16)
	if _, err := rand.Read(idRaw); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(idRaw)

	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ekRaw := mustBase64(t, c.bundle.MLKEM)
	ek, err := mlkem.NewEncapsulationKey768(ekRaw)
	if err != nil {
		t.Fatal(err)
	}
	ssKEM, kemCT := ek.Encapsulate()
	epkRaw := eph.PublicKey().Bytes()

	hostPub, err := ecdh.X25519().NewPublicKey(mustBase64(t, c.bundle.X25519))
	if err != nil {
		t.Fatal(err)
	}
	ssDH, err := eph.ECDH(hostPub)
	if err != nil {
		t.Fatal(err)
	}
	ikm := append(append([]byte{}, ssDH...), ssKEM...)
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ts.UnixMilli()))
	transcript := e2eeV2Transcript(e2eeTxDomainV2, c.bundleHash[:], []byte(c.deviceID), []byte(id), stamp[:], epkRaw, kemCT)
	derived, err := hkdf.Key(sha256.New, ikm, transcript, e2eeKeyInfoV2, 64)
	if err != nil {
		t.Fatal(err)
	}

	plain, err := json.Marshal(e2eeRequest{Method: method, Path: path, Body: body, Token: c.token})
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, err := sealE2EE(
		derived[:32],
		plain,
		e2eeAAD("req", "v2", c.deviceID, id, strconv.FormatInt(ts.UnixMilli(), 10)),
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(e2eeEnvelope{
		V:          e2eeVersionV2,
		DeviceID:   c.deviceID,
		ID:         id,
		TS:         ts.UnixMilli(),
		Suite:      e2eeSuiteV2,
		Epk:        base64.StdEncoding.EncodeToString(epkRaw),
		KEM:        base64.StdEncoding.EncodeToString(kemCT),
		Nonce:      nonce,
		Ciphertext: sealed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return envelope, derived[32:], id
}

func (c *testE2EEV2Client) request(t *testing.T, method, path string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	envelope, s2c, id := c.sealedV2(t, method, path, body, time.Now())
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
		t.Fatalf("e2ee v2 status = %d: %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, e2eeResponseType) ||
		!strings.HasSuffix(got, "v=2") {
		t.Fatalf("response type = %q", got)
	}
	var status int
	var headers http.Header
	var bodyOut []byte
	ended := false
	for frameIndex := 0; ; frameIndex++ {
		var length [4]byte
		if _, err := io.ReadFull(resp.Body, length[:]); err != nil {
			if err == io.EOF {
				if !ended {
					t.Fatalf("v2 response ended before authenticated terminator at frame %d", frameIndex)
				}
				break
			}
			t.Fatal(err)
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(resp.Body, frame); err != nil {
			t.Fatal(err)
		}
		aad := []byte("resp|v2|" + c.deviceID + "|" + id + "|" + strconv.Itoa(frameIndex))
		if ended {
			t.Fatal("v2 response contained data after authenticated terminator")
		}
		plain, err := openE2EE(
			s2c,
			base64.StdEncoding.EncodeToString(frame[:12]),
			base64.StdEncoding.EncodeToString(frame[12:]),
			aad,
		)
		if err != nil {
			endAAD := []byte(fmt.Sprintf("resp|v2|%s|%s|%d|end", c.deviceID, id, frameIndex))
			plain, err = openE2EE(
				s2c,
				base64.StdEncoding.EncodeToString(frame[:12]),
				base64.StdEncoding.EncodeToString(frame[12:]),
				endAAD,
			)
			if err != nil || string(plain) != "DFE2EE-END-v2" {
				t.Fatalf("frame %d was neither authenticated data nor terminator: %v", frameIndex, err)
			}
			ended = true
			continue
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
	if ended {
		var extra [1]byte
		if n, err := resp.Body.Read(extra[:]); n != 0 || err != io.EOF {
			t.Fatal("v2 response had trailing bytes after terminator")
		}
	}
	return status, headers, bodyOut
}

func postRawEnvelope(t *testing.T, base string, raw []byte) (int, string) {
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
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var refusal refusalEnvelope
	_ = json.Unmarshal(body, &refusal)
	return resp.StatusCode, refusal.Code
}

func TestE2EEV2PairAndState(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	client := newTestE2EEV2Client(t, srv.URL, "test-token")

	status, headers, body := client.request(t, http.MethodGet, "/api/v1/state", nil)
	if status != http.StatusOK {
		t.Fatalf("state status = %d: %s", status, body)
	}
	if headers.Get("Content-Type") != "application/json" {
		t.Fatalf("state content type = %q", headers.Get("Content-Type"))
	}
	if !bytes.Contains(body, []byte(`"hostId"`)) {
		t.Fatalf("state body did not look like state: %s", body)
	}
}

type failingE2EEOuterWriter struct {
	header http.Header
	writes int
}

func (w *failingE2EEOuterWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingE2EEOuterWriter) WriteHeader(int) {}

func (w *failingE2EEOuterWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestE2EEV2WriterDoesNotTerminateAfterWriteFailure(t *testing.T) {
	outer := &failingE2EEOuterWriter{}
	writer := &e2eeResponseWriter{
		outer:   outer,
		key:     make([]byte, 32),
		aad:     []byte("resp|v2|device|request"),
		version: e2eeVersionV2,
	}
	if _, err := writer.Write([]byte("payload")); err == nil {
		t.Fatal("write to failed outer stream succeeded")
	}
	writer.finish()
	if outer.writes != 1 {
		t.Fatalf("writer attempted %d frames after output failure, want only the failed head frame", outer.writes)
	}
}

func TestE2EEV2PairRejectsLowOrderBeforeMinting(t *testing.T) {
	srv, server := newRefusalServer(t, nil)
	bundle, hostID, _ := fetchE2EEV2Bundle(t, srv.URL)
	codeResp := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair/code", "test-token", map[string]any{"scope": "full"})
	var codeBody struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(codeResp.Body).Decode(&codeBody); err != nil {
		t.Fatal(err)
	}
	codeResp.Body.Close()

	ek, err := mlkem.NewEncapsulationKey768(mustBase64(t, bundle.MLKEM))
	if err != nil {
		t.Fatal(err)
	}
	_, kemCT := ek.Encapsulate()
	bad := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair", "", map[string]any{
		"v":          e2eeVersionV2,
		"suite":      e2eeSuiteV2,
		"epk":        base64.StdEncoding.EncodeToString(make([]byte, e2eeX25519Size)),
		"kem":        base64.StdEncoding.EncodeToString(kemCT),
		"sealedCode": base64.StdEncoding.EncodeToString(make([]byte, 48)),
	})
	var refusal refusalEnvelope
	_ = json.NewDecoder(bad.Body).Decode(&refusal)
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest || refusal.Code != codeInvalidRequest {
		t.Fatalf("low-order pair material = %d %q, want 400 %q", bad.StatusCode, refusal.Code, codeInvalidRequest)
	}
	server.openMu.Lock()
	grantCount := len(server.pairCodes)
	server.openMu.Unlock()
	if grantCount != 1 {
		t.Fatalf("invalid pair attempt consumed its grant: remaining=%d", grantCount)
	}
	server.openMu.Lock()
	browserCount := len(server.browsers)
	server.openMu.Unlock()
	if browserCount != 0 {
		t.Fatalf("invalid pair attempt minted %d browser rows", browserCount)
	}

	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ssKEM, validKEM := ek.Encapsulate()
	sealedCode := sealPairCodeV2ForTest(t, bundle, hostID, eph, validKEM, ssKEM, codeBody.Code)
	substituted, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	attack := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair", "", map[string]any{
		"v":          e2eeVersionV2,
		"suite":      e2eeSuiteV2,
		"epk":        base64.StdEncoding.EncodeToString(substituted.PublicKey().Bytes()),
		"kem":        base64.StdEncoding.EncodeToString(validKEM),
		"sealedCode": sealedCode,
	})
	var substitutedRefusal refusalEnvelope
	_ = json.NewDecoder(attack.Body).Decode(&substitutedRefusal)
	attack.Body.Close()
	if attack.StatusCode != http.StatusBadRequest || substitutedRefusal.Code != codeInvalidRequest {
		t.Fatalf("substituted pair key = %d %q", attack.StatusCode, substitutedRefusal.Code)
	}
	server.openMu.Lock()
	grantCount = len(server.pairCodes)
	browserCount = len(server.browsers)
	server.openMu.Unlock()
	if grantCount != 1 || browserCount != 0 {
		t.Fatalf("substitution consumed grant or minted row: grants=%d browsers=%d", grantCount, browserCount)
	}
	good := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair", "", map[string]any{
		"v":          e2eeVersionV2,
		"suite":      e2eeSuiteV2,
		"epk":        base64.StdEncoding.EncodeToString(eph.PublicKey().Bytes()),
		"kem":        base64.StdEncoding.EncodeToString(validKEM),
		"sealedCode": sealedCode,
	})
	defer good.Body.Close()
	if good.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(good.Body)
		t.Fatalf("valid retry after rejected pair = %d: %s", good.StatusCode, body)
	}
}

func TestE2EEV2RejectsV1Envelope(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	client := newTestE2EEV2Client(t, srv.URL, "test-token")

	envelope, _, _ := client.sealedV2(t, http.MethodGet, "/api/v1/state", nil, time.Now())
	var downgraded e2eeEnvelope
	if err := json.Unmarshal(envelope, &downgraded); err != nil {
		t.Fatal(err)
	}
	downgraded.V = e2eeVersion
	downgraded.Epk = ""
	downgraded.KEM = ""
	raw, _ := json.Marshal(downgraded)
	if status, code := postRawEnvelope(t, srv.URL, raw); status != http.StatusForbidden || code != codeE2EESuiteDowngrade {
		t.Fatalf("v1 envelope against a v2 row = %d %q", status, code)
	}
}

func TestE2EEV2VersionFlipFailsClosed(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)

	// A v2 row refuses v1.
	v2 := newTestE2EEV2Client(t, srv.URL, "test-token")
	envelope, _, _ := v2.sealedV2(t, http.MethodGet, "/api/v1/state", nil, time.Now())
	var flipped e2eeEnvelope
	_ = json.Unmarshal(envelope, &flipped)
	flipped.V = e2eeVersion
	raw, _ := json.Marshal(flipped)
	if status, code := postRawEnvelope(t, srv.URL, raw); status != http.StatusForbidden || code != codeE2EESuiteDowngrade {
		t.Fatalf("v2->v1 flip = %d %q", status, code)
	}

	// A v1 row refuses v2, so an attacker cannot claim an upgrade the row did
	// not negotiate.
	v1 := newTestE2EEClient(t, srv.URL, "test-token")
	v1Envelope, err := v1.rawEnvelope(t, http.MethodGet, "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	var upgrade e2eeEnvelope
	_ = json.Unmarshal(v1Envelope, &upgrade)
	upgrade.V = e2eeVersionV2
	upgrade.Epk = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, e2eeX25519Size))
	upgrade.KEM = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, e2eeMLKEMCiphertextSize))
	raw, _ = json.Marshal(upgrade)
	if status, code := postRawEnvelope(t, srv.URL, raw); status != http.StatusForbidden || code != codeE2EESuiteDowngrade {
		t.Fatalf("v1->v2 flip = %d %q", status, code)
	}
}

func TestE2EEUnknownSuiteFailsClosed(t *testing.T) {
	srv, server := newRefusalServer(t, nil)
	client := newTestE2EEClient(t, srv.URL, "test-token")
	envelope, err := client.rawEnvelope(t, http.MethodGet, "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	var unknownWireSuite e2eeEnvelope
	if err := json.Unmarshal(envelope, &unknownWireSuite); err != nil {
		t.Fatal(err)
	}
	unknownWireSuite.Suite = "future-suite"
	raw, _ := json.Marshal(unknownWireSuite)
	if status, code := postRawEnvelope(t, srv.URL, raw); status != http.StatusForbidden || code != codeE2EESuiteDowngrade {
		t.Fatalf("unknown wire suite = %d %q", status, code)
	}

	server.openMu.Lock()
	for key, browser := range server.browsers {
		if browser.ID == client.deviceID {
			browser.E2EESuite = "future-suite"
			server.browsers[key] = browser
		}
	}
	server.openMu.Unlock()
	envelope, err = client.rawEnvelope(t, http.MethodGet, "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	if status, code := postRawEnvelope(t, srv.URL, envelope); status != http.StatusForbidden || code != codeE2EESuiteDowngrade {
		t.Fatalf("unknown stored suite = %d %q", status, code)
	}
}

func TestE2EEV2PairRequiresBundle(t *testing.T) {
	srv, _ := newRefusalServer(t, func(s *Server) { s.cfg.E2EEMinSuite = 2 })

	mintCode := func() string {
		resp := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair/code", "test-token", map[string]any{"scope": "full"})
		defer resp.Body.Close()
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.Code
	}

	v1Key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v1DER, err := x509.MarshalPKIXPublicKey(v1Key.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	v1ClientKey := base64.StdEncoding.EncodeToString(v1DER)

	cases := []struct {
		name   string
		body   func(code string) map[string]any
		status int
		code   string
	}{
		{
			name:   "no bundle",
			body:   func(code string) map[string]any { return map[string]any{"code": code} },
			status: http.StatusForbidden,
			code:   codeE2EESuiteDowngrade,
		},
		{
			name: "v1 client key",
			body: func(code string) map[string]any {
				return map[string]any{"code": code, "clientPublicKey": v1ClientKey}
			},
			status: http.StatusForbidden,
			code:   codeE2EESuiteDowngrade,
		},
		{
			name: "unknown suite",
			body: func(code string) map[string]any {
				return map[string]any{"code": code, "v": 2, "suite": "mlkem-only"}
			},
			status: http.StatusBadRequest,
			code:   codeInvalidRequest,
		},
		{
			name: "missing material",
			body: func(code string) map[string]any {
				return map[string]any{"code": code, "v": 2, "suite": e2eeSuiteV2}
			},
			status: http.StatusBadRequest,
			code:   codeInvalidRequest,
		},
	}
	for _, tc := range cases {
		resp := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair", "", tc.body(mintCode()))
		var refusal refusalEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&refusal)
		resp.Body.Close()
		if resp.StatusCode != tc.status || refusal.Code != tc.code {
			t.Fatalf("%s: status = %d code = %q", tc.name, resp.StatusCode, refusal.Code)
		}
	}
}

func TestHybridMinimumRefusesPlainPairing(t *testing.T) {
	srv, server := newRefusalServer(t, func(s *Server) { s.cfg.E2EEMinSuite = e2eeVersionV2 })
	codeResp := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair/code", "test-token", map[string]any{"scope": "full"})
	var grant struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(codeResp.Body).Decode(&grant); err != nil {
		t.Fatal(err)
	}
	codeResp.Body.Close()
	plain := doJSON(t, http.MethodPost, srv.URL+"/api/v1/pair", "", map[string]any{"code": grant.Code})
	var refusal refusalEnvelope
	_ = json.NewDecoder(plain.Body).Decode(&refusal)
	plain.Body.Close()
	if plain.StatusCode != http.StatusForbidden || refusal.Code != codeE2EESuiteDowngrade {
		t.Fatalf("plaintext pair under hybrid minimum = %d %q", plain.StatusCode, refusal.Code)
	}
	server.openMu.Lock()
	browserCount := len(server.browsers)
	server.openMu.Unlock()
	if browserCount != 0 {
		t.Fatalf("plaintext pair minted %d browser rows under hybrid minimum", browserCount)
	}
}

func TestE2EEV2MinSuiteRefusesLegacyRow(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	legacy := newTestE2EEClient(t, srv.URL, "test-token")

	s.cfg.E2EEMinSuite = 2
	envelope, err := legacy.rawEnvelope(t, http.MethodGet, "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	if status, code := postRawEnvelope(t, srv.URL, envelope); status != http.StatusForbidden || code != codeE2EESuiteDowngrade {
		t.Fatalf("legacy row under min-suite 2 = %d %q", status, code)
	}
}

func TestE2EEV2ReplayAndTamper(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	client := newTestE2EEV2Client(t, srv.URL, "test-token")

	envelope, _, _ := client.sealedV2(t, http.MethodGet, "/api/v1/state", nil, time.Now())
	if status, code := postRawEnvelope(t, srv.URL, envelope); status != http.StatusOK {
		t.Fatalf("first envelope = %d %q", status, code)
	}
	if status, code := postRawEnvelope(t, srv.URL, envelope); status != http.StatusBadRequest {
		t.Fatalf("replay = %d %q", status, code)
	}

	var tampered e2eeEnvelope
	_ = json.Unmarshal(envelope, &tampered)
	raw, _ := base64.StdEncoding.DecodeString(tampered.Ciphertext)
	raw[0] ^= 1
	tampered.Ciphertext = base64.StdEncoding.EncodeToString(raw)
	rawEnvelope, _ := json.Marshal(tampered)
	if status, _ := postRawEnvelope(t, srv.URL, rawEnvelope); status != http.StatusBadRequest {
		t.Fatalf("tampered ciphertext = %d", status)
	}

	var badKEM e2eeEnvelope
	fresh, _, _ := client.sealedV2(t, http.MethodGet, "/api/v1/state", nil, time.Now())
	_ = json.Unmarshal(fresh, &badKEM)
	kem, _ := base64.StdEncoding.DecodeString(badKEM.KEM)
	kem[0] ^= 1
	badKEM.KEM = base64.StdEncoding.EncodeToString(kem)
	rawEnvelope, _ = json.Marshal(badKEM)
	if status, _ := postRawEnvelope(t, srv.URL, rawEnvelope); status != http.StatusBadRequest {
		t.Fatalf("tampered kem = %d", status)
	}
}

func TestE2EEV2BundleSignature(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	bundle, _, _ := fetchE2EEV2Bundle(t, srv.URL)
	if err := verifyE2EEBundle(bundle); err != nil {
		t.Fatalf("fresh bundle did not verify: %v", err)
	}

	tamper := func(name string, mutate func(*e2eeBundle)) {
		bad := bundle
		mutate(&bad)
		if err := verifyE2EEBundle(bad); err == nil {
			t.Fatalf("%s: tampered bundle verified", name)
		}
	}
	tamper("suite", func(b *e2eeBundle) { b.Suite = e2eeSuiteV2 + "-old" })
	tamper("epoch", func(b *e2eeBundle) { b.Epoch++ })
	tamper("x25519", func(b *e2eeBundle) {
		raw, _ := base64.StdEncoding.DecodeString(b.X25519)
		raw[0] ^= 1
		b.X25519 = base64.StdEncoding.EncodeToString(raw)
	})
	tamper("mlkem", func(b *e2eeBundle) {
		raw, _ := base64.StdEncoding.DecodeString(b.MLKEM)
		raw[0] ^= 1
		b.MLKEM = base64.StdEncoding.EncodeToString(raw)
	})
	tamper("signature", func(b *e2eeBundle) {
		raw, _ := base64.StdEncoding.DecodeString(b.Sig)
		raw[0] ^= 1
		b.Sig = base64.StdEncoding.EncodeToString(raw)
	})
}

func TestE2EEIdentityUpgradesLegacyKey(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	legacyPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	legacyDER, err := x509.MarshalPKCS8PrivateKey(legacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutE2EEHostKey(legacyDER); err != nil {
		t.Fatal(err)
	}

	first, err := loadOrCreateE2EE(st)
	if err != nil {
		t.Fatal(err)
	}
	legacyPubDER, err := x509.MarshalPKIXPublicKey(legacyPrivate.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if first.publicB64 != base64.StdEncoding.EncodeToString(legacyPubDER) {
		t.Fatal("upgrade changed the pinned P-256 key")
	}
	if err := verifyE2EEBundle(first.bundle); err != nil {
		t.Fatalf("upgraded bundle did not verify: %v", err)
	}

	second, err := loadOrCreateE2EE(st)
	if err != nil {
		t.Fatal(err)
	}
	if second.publicB64 != first.publicB64 || second.fingerprintV2 != first.fingerprintV2 ||
		second.bundle.Epoch != first.bundle.Epoch ||
		second.bundle.X25519 != first.bundle.X25519 || second.bundle.MLKEM != first.bundle.MLKEM {
		t.Fatal("identity changed across the second load")
	}
	if err := verifyE2EEBundle(second.bundle); err != nil {
		t.Fatalf("reloaded bundle did not verify: %v", err)
	}
}

func TestE2EEEmptyPersistedIdentityFailsClosed(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.PutE2EEHostKey([]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateE2EE(st); err == nil {
		t.Fatal("empty persisted identity silently minted a replacement key")
	}
	stored, err := st.GetE2EEHostKey()
	if err != nil {
		t.Fatalf("empty identity row was replaced or removed: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("empty identity row changed to %d bytes", len(stored))
	}
}
