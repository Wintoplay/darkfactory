package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func TestPairedDeviceTokenWorksAsBearer(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)

	codeReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair/code", strings.NewReader(`{"label":"Laptop","scope":"full"}`))
	codeReq.Header.Set("Authorization", "Bearer test-token")
	codeReq.Header.Set("Content-Type", "application/json")
	codeReq.Header.Set("X-DF-Request", "1")
	codeResp, err := http.DefaultClient.Do(codeReq)
	if err != nil {
		t.Fatal(err)
	}
	var issued pairCodeResponse
	if err := json.NewDecoder(codeResp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	codeResp.Body.Close()

	pairReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair", strings.NewReader(fmt.Sprintf(`{"code":%q}`, issued.Code)))
	pairReq.Header.Set("Content-Type", "application/json")
	pairReq.Header.Set("X-DF-Request", "1")
	pairResp, err := http.DefaultClient.Do(pairReq)
	if err != nil {
		t.Fatal(err)
	}
	var paired struct {
		Token  string `json:"token"`
		Scope  string `json:"scope"`
		HostID string `json:"hostId"`
	}
	if err := json.NewDecoder(pairResp.Body).Decode(&paired); err != nil {
		t.Fatal(err)
	}
	pairResp.Body.Close()
	if pairResp.StatusCode != http.StatusOK || paired.Token == "" || paired.Scope != scopeFull || paired.HostID == "" {
		t.Fatalf("pair = %d %#v", pairResp.StatusCode, paired)
	}

	stateReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	stateReq.Header.Set("Authorization", "Bearer "+paired.Token)
	stateResp, err := http.DefaultClient.Do(stateReq)
	if err != nil {
		t.Fatal(err)
	}
	var state stateResponse
	if err := json.NewDecoder(stateResp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	stateResp.Body.Close()
	if stateResp.StatusCode != http.StatusOK || state.Device == nil || state.Device.ID == "" ||
		state.HostID == "" || state.HostID != paired.HostID || state.Scope != scopeFull || !state.Terminal {
		t.Fatalf("bearer state = %d scope=%q terminal=%v device=%#v", stateResp.StatusCode, state.Scope, state.Terminal, state.Device)
	}

	revokeReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/devices/"+state.Device.ID, nil)
	revokeReq.Header.Set("Authorization", "Bearer test-token")
	revokeReq.Header.Set("X-DF-Request", "1")
	revokeResp, err := http.DefaultClient.Do(revokeReq)
	if err != nil {
		t.Fatal(err)
	}
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d", revokeResp.StatusCode)
	}

	stateReq, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	stateReq.Header.Set("Authorization", "Bearer "+paired.Token)
	stateResp, err = http.DefaultClient.Do(stateReq)
	if err != nil {
		t.Fatal(err)
	}
	stateResp.Body.Close()
	if stateResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked bearer token = %d, want 401", stateResp.StatusCode)
	}
}

func TestPairedDeviceScopeAndRevoke(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair/code", strings.NewReader(`{"label":"iPad","scope":"read","terminal":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var issued pairCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if issued.Label != "iPad" || issued.Scope != scopeRead {
		t.Fatalf("pair grant = %#v", issued)
	}

	client := newSessionClient()
	body, _ := json.Marshal(map[string]string{"code": issued.Code})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair status = %d", resp.StatusCode)
	}

	state := getState(t, client, srv.URL)
	if state.Scope != scopeRead || state.Device == nil || state.Device.Label != "iPad" {
		t.Fatalf("state scope=%q device=%#v", state.Scope, state.Device)
	}

	// A read-only cookie can read but not mint a pairing code or mutate.
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only mutation status = %d, want 403", resp.StatusCode)
	}

	// A read-only viewer may ask a TUI for a redraw, but never send input.
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ses_missing/redraw", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("read-only redraw was refused")
	}
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ses_missing/input", strings.NewReader(`{"data":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only input status = %d, want 403", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/devices", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var devices struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(devices.Devices) != 1 || !devices.Devices[0].Current || devices.Devices[0].Scope != scopeRead {
		t.Fatalf("devices = %#v", devices.Devices)
	}

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/devices/"+devices.Devices[0].ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only revoke status = %d, want 403", resp.StatusCode)
	}
}

func TestFullDeviceCanRevokeAnother(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)

	readClient := pairClient(t, srv.URL, "Read phone", scopeRead)
	fullClient := pairClient(t, srv.URL, "Desktop", scopeFull)

	readState := getState(t, readClient, srv.URL)
	if readState.Device == nil {
		t.Fatal("read device missing from state")
	}

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/devices/"+readState.Device.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err := fullClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = readClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked device state = %d, want 401", resp.StatusCode)
	}

	// The full device still works.
	if got := getState(t, fullClient, srv.URL); got.Device == nil || got.Device.Label != "Desktop" {
		t.Fatalf("full device state = %#v", got.Device)
	}
}

func TestRevokeAllOthersKeepsCurrent(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)

	readClient := pairClient(t, srv.URL, "Read phone", scopeRead)
	fullClient := pairClient(t, srv.URL, "Desktop", scopeFull)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/devices/revoke-all", strings.NewReader(`{"keepCurrent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := fullClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Revoked int `json:"revoked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || result.Revoked != 1 {
		t.Fatalf("revoke-all status=%d revoked=%d", resp.StatusCode, result.Revoked)
	}
	if got := getState(t, fullClient, srv.URL); got.Device == nil || got.Device.Label != "Desktop" {
		t.Fatalf("current device was revoked: %#v", got.Device)
	}
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = readClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other device state = %d, want 401", resp.StatusCode)
	}
}

func TestFullScopeIsWorkspaceAndReadScopeIsNot(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)

	// A full-scope device gets the workspace surface even if an older client
	// sent terminal:false. The pair-time terminal flag is no longer an
	// independent grant.
	phone := pairClientTerminal(t, srv.URL, "Phone", scopeFull, false)
	state := getState(t, phone, srv.URL)
	if state.Scope != scopeFull {
		t.Fatalf("scope = %q, want full", state.Scope)
	}
	if !state.Terminal {
		t.Fatal("a full-scope device reports no workspace access")
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/ses_missing/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := phone.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("full-scope stream = %d, want 404", resp.StatusCode)
	}

	// Stopping a session stays available to a full-scope device.
	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/sessions/ses_missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err = phone.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("a full-scope device cannot stop a session")
	}

	// Read-only wins over a stale terminal:true request. It can read state,
	// request a redraw, and open chats, but not attach, type, resize, or create
	// a session.
	tablet := pairClientTerminal(t, srv.URL, "Tablet", scopeRead, true)
	state = getState(t, tablet, srv.URL)
	if state.Terminal {
		t.Fatal("a read-only device reports workspace access")
	}
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/sessions"},
		{http.MethodGet, "/api/v1/sessions/ses_missing/stream"},
		{http.MethodPost, "/api/v1/sessions/ses_missing/input"},
		{http.MethodPost, "/api/v1/sessions/ses_missing/native"},
		{http.MethodPost, "/api/v1/sessions/ses_missing/resize"},
	} {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(`{"cols":10,"rows":10}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DF-Request", "1")
		resp, err := tablet.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ses_missing/redraw", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err = tablet.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("a read-only redraw was refused")
	}

	// Chat writes are workspace actions too. A read-only device may read the
	// panel but cannot prompt; a full-scope device is not stopped by the scope
	// gate (the missing upstream may still fail).
	for _, tc := range []struct {
		client *sessionClient
		want   int
	}{
		{tablet, http.StatusForbidden},
		{phone, 0},
	} {
		req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/opencode/sessions/ses_missing/prompt", strings.NewReader(`{"text":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DF-Request", "1")
		resp, err = tc.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if tc.want != 0 && resp.StatusCode != tc.want {
			t.Fatalf("OpenCode prompt status = %d, want %d", resp.StatusCode, tc.want)
		}
		if tc.want == 0 && resp.StatusCode == http.StatusForbidden {
			t.Fatal("a full-scope device was refused an OpenCode prompt")
		}
	}
}

func TestBrowserSessionIdleExpiryAndRenewal(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.InstanceSecret = "idle-secret"
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	server := New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	getState := func(client *sessionClient) int {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Past the idle window: refused and the row is gone.
	idle := mintBrowserCookie(t, srv, "test-token")
	key := server.hashSecret(idle.cookie.Value)
	server.openMu.Lock()
	device := server.browsers[key]
	device.LastSeenAt = time.Now().Add(-browserTTL - time.Minute)
	server.browsers[key] = device
	server.openMu.Unlock()
	if code := getState(idle); code != http.StatusUnauthorized {
		t.Fatalf("idle session = %d, want 401", code)
	}

	// Past the absolute cap, even with a fresh LastSeenAt.
	old := mintBrowserCookie(t, srv, "test-token")
	key = server.hashSecret(old.cookie.Value)
	server.openMu.Lock()
	device = server.browsers[key]
	device.CreatedAt = time.Now().Add(-browserMaxTTL - time.Minute)
	device.LastSeenAt = time.Now()
	server.browsers[key] = device
	server.openMu.Unlock()
	if code := getState(old); code != http.StatusUnauthorized {
		t.Fatalf("capped session = %d, want 401", code)
	}

	// A session used after the renewal threshold gets a fresh cookie.
	fresh := mintBrowserCookie(t, srv, "test-token")
	key = server.hashSecret(fresh.cookie.Value)
	server.openMu.Lock()
	device = server.browsers[key]
	device.LastSeenAt = time.Now().Add(-2 * time.Hour)
	server.browsers[key] = device
	server.openMu.Unlock()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fresh.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("active session = %d, want 200", resp.StatusCode)
	}
	renewed := resp.Cookies()
	if len(renewed) != 1 || renewed[0].Name != cookieName || renewed[0].MaxAge != int(browserTTL/time.Second) {
		t.Fatalf("renewal cookies = %#v", renewed)
	}
}

func pairClient(t *testing.T, baseURL, label, scope string) *sessionClient {
	t.Helper()
	return pairClientTerminal(t, baseURL, label, scope, false)
}

func pairClientTerminal(t *testing.T, baseURL, label, scope string, terminal bool) *sessionClient {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"label": label, "scope": scope, "terminal": terminal})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/pair/code", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var issued pairCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	client := newSessionClient()
	body, _ := json.Marshal(map[string]string{"code": issued.Code})
	req, err = http.NewRequest(http.MethodPost, baseURL+"/api/v1/pair", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair %q status = %d", label, resp.StatusCode)
	}
	return client
}

func getState(t *testing.T, client doer, baseURL string) stateResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d body=%q", resp.StatusCode, raw)
	}
	var state stateResponse
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
