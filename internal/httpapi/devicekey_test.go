package httpapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func testDeviceKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, base64.StdEncoding.EncodeToString(der)
}

func signDeviceChallenge(t *testing.T, key *ecdsa.PrivateKey, deviceID, challenge string) string {
	t.Helper()
	digest := sha256.Sum256(deviceAuthPayload(deviceID, challenge))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return base64.StdEncoding.EncodeToString(sig)
}

func devicePost(t *testing.T, url, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDeviceKeyRegisterChallengeAndVerify(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	key, publicKey := testDeviceKey(t)

	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "test-token", map[string]string{
		"label":     "Desktop",
		"publicKey": publicKey,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	var registered struct {
		DeviceID string `json:"deviceId"`
		Scope    string `json:"scope"`
		Terminal bool   `json:"terminal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.DeviceID == "" || registered.Scope != scopeFull || !registered.Terminal {
		t.Fatalf("registered = %#v", registered)
	}

	resp = devicePost(t, srv.URL+"/api/v1/device-keys/challenge", "", map[string]string{"deviceId": registered.DeviceID})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("challenge status = %d", resp.StatusCode)
	}
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	if challenge.Challenge == "" {
		t.Fatal("empty challenge")
	}

	signature := signDeviceChallenge(t, key, registered.DeviceID, challenge.Challenge)
	resp = devicePost(t, srv.URL+"/api/v1/device-keys/verify", "", map[string]string{
		"deviceId":  registered.DeviceID,
		"challenge": challenge.Challenge,
		"signature": signature,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status = %d", resp.StatusCode)
	}
	var verified struct {
		Token    string `json:"token"`
		Scope    string `json:"scope"`
		Terminal bool   `json:"terminal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verified); err != nil {
		t.Fatal(err)
	}
	if verified.Token == "" || verified.Scope != scopeFull || !verified.Terminal {
		t.Fatalf("verified = %#v", verified)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+verified.Token)
	stateResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stateResp.Body.Close()
	if stateResp.StatusCode != http.StatusOK {
		t.Fatalf("session state status = %d", stateResp.StatusCode)
	}
	var state stateResponse
	if err := json.NewDecoder(stateResp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Scope != scopeFull || state.Device == nil || state.Device.Kind != "key" ||
		state.Device.ID != registered.DeviceID || !state.Device.Current {
		t.Fatalf("state = %#v", state)
	}

	// The device session sees itself as the current key in the revoke UI.
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/devices", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+verified.Token)
	devicesResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer devicesResp.Body.Close()
	var devices struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.NewDecoder(devicesResp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	current := false
	for _, device := range devices.Devices {
		if device.ID == registered.DeviceID && device.Kind == "key" && device.Current {
			current = true
		}
	}
	if !current {
		t.Fatalf("device key not marked current: %#v", devices.Devices)
	}

	// The challenge is single-use even when the signature is valid.
	resp = devicePost(t, srv.URL+"/api/v1/device-keys/verify", "", map[string]string{
		"deviceId":  registered.DeviceID,
		"challenge": challenge.Challenge,
		"signature": signature,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed verify status = %d, want 401", resp.StatusCode)
	}
}

func TestDeviceKeySessionEnforcesStoredScope(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	key, publicKey := testDeviceKey(t)
	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "test-token", map[string]string{"publicKey": publicKey})
	defer resp.Body.Close()
	var registered struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}

	// A key registered by a read-only principal (or downgraded later) must not
	// gain write or terminal access when it authenticates.
	s.deviceMu.Lock()
	stored := s.deviceKeys[registered.DeviceID]
	stored.Scope = scopeRead
	stored.Terminal = false
	s.deviceKeys[registered.DeviceID] = stored
	s.deviceMu.Unlock()

	resp = devicePost(t, srv.URL+"/api/v1/device-keys/challenge", "", map[string]string{"deviceId": registered.DeviceID})
	defer resp.Body.Close()
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	signature := signDeviceChallenge(t, key, registered.DeviceID, challenge.Challenge)
	resp = devicePost(t, srv.URL+"/api/v1/device-keys/verify", "", map[string]string{
		"deviceId":  registered.DeviceID,
		"challenge": challenge.Challenge,
		"signature": signature,
	})
	defer resp.Body.Close()
	var session struct {
		Token    string `json:"token"`
		Scope    string `json:"scope"`
		Terminal bool   `json:"terminal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if session.Scope != scopeRead || session.Terminal {
		t.Fatalf("read-only session = %#v", session)
	}

	resp = devicePost(t, srv.URL+"/api/v1/repos", session.Token, map[string]string{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only mutation status = %d, want 403", resp.StatusCode)
	}
}

func TestDeviceKeyLogoutEndsOnlyCurrentSession(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	key, publicKey := testDeviceKey(t)
	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "test-token", map[string]string{"publicKey": publicKey})
	defer resp.Body.Close()
	var registered struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	issue := func() string {
		t.Helper()
		resp := devicePost(t, srv.URL+"/api/v1/device-keys/challenge", "", map[string]string{"deviceId": registered.DeviceID})
		defer resp.Body.Close()
		var challenge struct {
			Challenge string `json:"challenge"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
			t.Fatal(err)
		}
		signature := signDeviceChallenge(t, key, registered.DeviceID, challenge.Challenge)
		resp = devicePost(t, srv.URL+"/api/v1/device-keys/verify", "", map[string]string{
			"deviceId":  registered.DeviceID,
			"challenge": challenge.Challenge,
			"signature": signature,
		})
		defer resp.Body.Close()
		var session struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
			t.Fatal(err)
		}
		if session.Token == "" {
			t.Fatal("no session token")
		}
		return session.Token
	}
	first := issue()
	second := issue()

	resp = devicePost(t, srv.URL+"/api/v1/open/logout", first, map[string]string{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	for token, want := range map[string]int{first: http.StatusUnauthorized, second: http.StatusOK} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("state after logout = %d, want %d", resp.StatusCode, want)
		}
	}
}

func TestDeviceKeyRejectsBadSignatureAndUnknownDevice(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	key, publicKey := testDeviceKey(t)

	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "test-token", map[string]string{"publicKey": publicKey})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	var registered struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}

	resp = devicePost(t, srv.URL+"/api/v1/device-keys/challenge", "", map[string]string{"deviceId": registered.DeviceID})
	defer resp.Body.Close()
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
		t.Fatal(err)
	}
	bad := signDeviceChallenge(t, key, registered.DeviceID, "not-the-challenge")
	resp = devicePost(t, srv.URL+"/api/v1/device-keys/verify", "", map[string]string{
		"deviceId":  registered.DeviceID,
		"challenge": challenge.Challenge,
		"signature": bad,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want 401", resp.StatusCode)
	}

	resp = devicePost(t, srv.URL+"/api/v1/device-keys/challenge", "", map[string]string{"deviceId": "00000000000000000000000000000000"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown device status = %d, want 401", resp.StatusCode)
	}
}

func TestDeviceKeyAppearsInDevicesAndRevokes(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	_, publicKey := testDeviceKey(t)
	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "test-token", map[string]string{"label": "Desktop", "publicKey": publicKey})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	var registered struct {
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/devices", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	devicesResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer devicesResp.Body.Close()
	var devices struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.NewDecoder(devicesResp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, device := range devices.Devices {
		if device.ID == registered.DeviceID && device.Kind == "key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("device key missing from devices: %#v", devices.Devices)
	}

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/devices/"+registered.DeviceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	revokeResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d", revokeResp.StatusCode)
	}

	resp = devicePost(t, srv.URL+"/api/v1/device-keys/challenge", "", map[string]string{"deviceId": registered.DeviceID})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked challenge status = %d, want 401", resp.StatusCode)
	}
}

func TestDeviceKeyRejectsNonP256PublicKey(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "test-token", map[string]string{
		"publicKey": base64.StdEncoding.EncodeToString(der),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("P-384 status = %d, want 400", resp.StatusCode)
	}
}

func TestDeviceKeyRegistrationNeedsAuth(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	_, publicKey := testDeviceKey(t)
	resp := devicePost(t, srv.URL+"/api/v1/device-keys", "", map[string]string{"publicKey": publicKey})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated register status = %d, want 401", resp.StatusCode)
	}
	if strings.TrimSpace(publicKey) == "" {
		t.Fatal("empty test key")
	}
}
