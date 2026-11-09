package httpapi

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func FuzzParseE2EEEnvelope(f *testing.F) {
	f.Add([]byte(`{"v":1,"deviceId":"d","id":"i","ts":1,"nonce":"AA==","ciphertext":"AA=="}`))
	f.Add([]byte(`{"v":3,"deviceId":"d","id":"i","ts":1}`))
	f.Add([]byte(`{} {}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := parseE2EEEnvelope(raw)
		if err != nil {
			return
		}
		if (got.V != e2eeVersion && got.V != e2eeVersionV2) || got.DeviceID == "" || got.ID == "" || got.TS == 0 {
			t.Fatalf("accepted envelope violates semantic constraints: %+v", got)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := parseE2EEEnvelope(encoded)
		if err != nil || !reflect.DeepEqual(got, roundTrip) {
			t.Fatalf("accepted envelope did not round-trip: got=%+v roundtrip=%+v err=%v", got, roundTrip, err)
		}
	})
}

func FuzzParsePairRequest(f *testing.F) {
	f.Add([]byte(`{"code":"AAAA"}`))
	f.Add([]byte(`{"v":2,"suite":"x25519-mlkem768-v1","epk":"","kem":"","sealedCode":""}`))
	f.Add([]byte(`{"code":"AAAA","unknown":true}`))
	f.Add([]byte(`{"v":2,"suite":"x25519-mlkem768-v1","epk":"AQ==","kem":"AQ==","sealedCode":"AQ=="}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := parsePairRequest(raw)
		if err != nil {
			return
		}
		if got.V != 0 && got.V != e2eeVersion && got.V != e2eeVersionV2 {
			t.Fatalf("accepted unsupported pair version: %d", got.V)
		}
		if got.V == e2eeVersionV2 && (got.Suite != e2eeSuiteV2 || got.Code != "") {
			t.Fatalf("accepted unsafe v2 pair request: %+v", got)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := parsePairRequest(encoded)
		if err != nil || !reflect.DeepEqual(got, roundTrip) {
			t.Fatalf("accepted pair request did not round-trip: got=%+v roundtrip=%+v err=%v", got, roundTrip, err)
		}
	})
}

func FuzzParseE2EEIdentityFile(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"v":2,"epoch":1,"p256":"AA==","x25519":"AA==","mlkem":"AA=="}`))
	f.Add([]byte("not-json"))
	p256, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	x25519, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	kem, err := mlkem.GenerateKey768()
	if err != nil {
		f.Fatal(err)
	}
	p256DER, err := x509.MarshalPKCS8PrivateKey(p256)
	if err != nil {
		f.Fatal(err)
	}
	validIdentity, err := json.Marshal(e2eeIdentityFile{
		V:      e2eeIdentityVersion,
		Epoch:  1,
		P256:   base64.StdEncoding.EncodeToString(p256DER),
		X25519: base64.StdEncoding.EncodeToString(x25519.Bytes()),
		MLKEM:  base64.StdEncoding.EncodeToString(kem.Bytes()),
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(validIdentity)
	f.Fuzz(func(t *testing.T, raw []byte) {
		identity, ok, err := parseE2EEIdentityFile(raw)
		if err != nil || !ok {
			return
		}
		if identity.p256 == nil || identity.x25519 == nil || identity.mlkem == nil || identity.epoch == 0 {
			t.Fatal("accepted identity is incomplete")
		}
	})
}

func FuzzE2EEBundleCanonical(f *testing.F) {
	valid := e2eeBundle{
		V:      e2eeVersionV2,
		Epoch:  1,
		Suite:  e2eeSuiteV2,
		P256:   base64.StdEncoding.EncodeToString([]byte{1}),
		X25519: base64.StdEncoding.EncodeToString(make([]byte, e2eeX25519Size)),
		MLKEM:  base64.StdEncoding.EncodeToString(make([]byte, e2eeMLKEMPublicSize)),
	}
	seed, _ := json.Marshal(valid)
	f.Add(seed)
	f.Add([]byte(`{"v":99,"suite":"legacy"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxBody {
			t.Skip()
		}
		var bundle e2eeBundle
		if err := decodeStrictJSON(raw, maxBody, &bundle); err != nil {
			return
		}
		canonical, err := bundle.canonical()
		if err != nil {
			return
		}
		if bundle.V != e2eeVersionV2 || bundle.Suite != e2eeSuiteV2 || bundle.Epoch == 0 {
			t.Fatalf("accepted unsupported bundle: %+v", bundle)
		}
		canonicalAgain, err := bundle.canonical()
		if err != nil || !reflect.DeepEqual(canonical, canonicalAgain) {
			t.Fatalf("bundle canonical form is unstable: %v", err)
		}
	})
}

func FuzzE2EEInvalidInputCannotMutate(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"v":2,"deviceId":"missing","id":"i","ts":1,"suite":"x25519-mlkem768-v1"}`))
	cfg := config.Default()
	cfg.Token = "fuzz-master"
	cfg.E2EEMinSuite = e2eeVersion
	sessions := session.NewManager(1024)
	server := New(cfg, nil, sessions, slog.New(slog.NewTextHandler(io.Discard, nil)))
	deviceToken := "fuzz-device-token"
	clientPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	clientDER, err := x509.MarshalPKIXPublicKey(clientPrivate.PublicKey())
	if err != nil {
		f.Fatal(err)
	}
	clientKey := base64.StdEncoding.EncodeToString(clientDER)
	clientPublic, _, err := parseClientE2EEKey(clientKey)
	if err != nil {
		f.Fatal(err)
	}
	c2s, _, err := deriveE2EEKeys(server.e2ee.private, clientPublic, server.e2ee.publicB64, clientKey)
	if err != nil {
		f.Fatal(err)
	}
	deviceID := "fuzz-device"
	deviceKey := server.hashSecret(deviceToken)
	server.browsers[deviceKey] = store.Browser{
		ID:            deviceID,
		Label:         "fuzz-device",
		Scope:         scopeFull,
		ExpiresAt:     time.Now().Add(time.Hour),
		KeyVersion:    browserKeyVersion,
		SecretTag:     server.secretTag(),
		E2EEPublicKey: clientKey,
	}
	handler := server.Handler()
	f.Cleanup(func() { sessions.CloseAll() })
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > e2eeMaxBody-2048 {
			t.Skip()
		}
		ts := time.Now().UnixMilli()
		plain, err := json.Marshal(e2eeRequest{Method: "GET", Path: "/api/v1/state", Token: deviceToken})
		if err != nil {
			t.Fatal(err)
		}
		nonce, ciphertext, err := sealE2EE(c2s, plain, e2eeAAD("req", deviceID, "fuzz-request", strconv.FormatInt(ts, 10)))
		if err != nil {
			t.Fatal(err)
		}
		tampered, err := base64.StdEncoding.DecodeString(ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		delta := byte(1)
		if len(raw) > 0 {
			delta = raw[0]%255 + 1
		}
		tampered[len(tampered)-1] ^= delta
		envelope, err := json.Marshal(e2eeEnvelope{
			V:          e2eeVersion,
			DeviceID:   deviceID,
			ID:         "fuzz-request",
			TS:         ts,
			Nonce:      nonce,
			Ciphertext: base64.StdEncoding.EncodeToString(tampered),
		})
		if err != nil {
			t.Fatal(err)
		}
		server.e2eeLimitMu.Lock()
		server.e2eeSources = make(map[string]pairWindow)
		server.e2eeGlobal = pairWindow{}
		server.e2eeLimitMu.Unlock()
		request := httptest.NewRequest("POST", "/api/v1/e2ee", strings.NewReader(string(envelope)))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "127.0.0.1:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var refusal refusalEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
			t.Fatalf("tampered authenticated envelope did not return refusal JSON: status=%d body=%s err=%v", response.Code, response.Body.String(), err)
		}
		if response.Code != 400 || refusal.Code != codeInvalidRequest {
			t.Fatalf("tampered ciphertext reached dispatch or wrong refusal: status=%d code=%q", response.Code, refusal.Code)
		}
		server.openMu.Lock()
		browserCount := len(server.browsers)
		server.openMu.Unlock()
		if browserCount != 1 {
			t.Fatalf("tampered envelope changed browser rows: count=%d", browserCount)
		}
		server.e2eeMu.Lock()
		replayCount := len(server.e2eeReplay)
		server.e2eeMu.Unlock()
		if replayCount != 0 {
			t.Fatalf("tampered envelope consumed %d replay slots", replayCount)
		}
	})
}

func TestStrictCryptoParsersRejectUnknownAndTrailingJSON(t *testing.T) {
	validEnvelope := []byte(`{"v":1,"deviceId":"d","id":"i","ts":1,"nonce":"AA==","ciphertext":"AA=="}`)
	for _, raw := range [][]byte{
		append(append([]byte{}, validEnvelope...), []byte(` {}`)...),
		[]byte(`{"v":1,"deviceId":"d","id":"i","ts":1,"unknown":true}`),
	} {
		if _, err := parseE2EEEnvelope(raw); err == nil {
			t.Fatalf("accepted malformed envelope JSON: %s", raw)
		}
	}
	for _, raw := range [][]byte{
		[]byte(`{"code":"AAAA"} {}`),
		[]byte(`{"code":"AAAA","unknown":true}`),
	} {
		if _, err := parsePairRequest(raw); err == nil {
			t.Fatalf("accepted malformed pair JSON: %s", raw)
		}
	}
}
