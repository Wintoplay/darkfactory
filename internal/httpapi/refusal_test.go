package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

var refusalCodeShape = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// The golden set is the contract. A rename, a removed code or a changed status
// fails here, which is the point of "codes immutable" (GV-7).
const goldenRefusals = `capability_scope 403
device_not_found 404
device_rate_limited 429
e2ee_rate_limited 429
e2ee_unavailable 503
host_admin 403
internal_error 500
invalid_id 400
invalid_path 400
invalid_plan 400
invalid_request 400
invalid_resize 400
invalid_scope 400
invalid_step 400
method_not_allowed 405
native_pane_scope 403
native_terminal_failed 500
native_terminal_remote 403
native_terminal_unavailable 409
not_a_directory 400
not_a_principal 403
not_found 404
open_host_only 403
open_invalid 401
opencode_bad_request 400
opencode_conflict 409
opencode_disabled 503
opencode_failed 502
opencode_not_found 404
opencode_rate_limited 429
opencode_response_too_large 502
opencode_unavailable 502
origin_denied 403
pair_https_only 403
pair_invalid 401
pair_rate_limited 429
picker_failed 500
picker_remote 409
picker_unavailable 409
policy_denied 403
read_only 403
repo_busy 409
repo_exists 409
repo_name_unsafe 409
repo_not_found 404
repo_order_invalid 400
request_too_large 413
reveal_failed 500
reveal_remote 409
reveal_unavailable 409
run_not_found 404
run_terminal 409
session_create_failed 409
session_not_found 404
session_order_invalid 400
source_denied 403
step_not_waiting 409
store_unavailable 503
streaming_unsupported 500
terminal_required 403
terminal_write_failed 409
unauthorized 401
unknown_agent 400
unknown_location 403
workspace_busy 409
workspace_tied_to_repo 409
worktree_busy 409
worktree_create_failed 409
worktree_dirty 409
worktree_not_found 404
worktree_order_invalid 400
worktree_remove_failed 409
worktree_required 400`

func TestRefusalCodesAreStable(t *testing.T) {
	var lines []string
	for _, code := range refusalCodeList() {
		lines = append(lines, fmt.Sprintf("%s %d", code, refusalSpecs[code].status))
	}
	if got := strings.Join(lines, "\n"); got != goldenRefusals {
		t.Fatalf("refusal registry changed:\n--- got ---\n%s\n--- want ---\n%s", got, goldenRefusals)
	}
	for code, spec := range refusalSpecs {
		if !refusalCodeShape.MatchString(code) {
			t.Errorf("code %q is not lower_snake_case", code)
		}
		if spec.status < 400 || spec.status > 599 {
			t.Errorf("code %q maps to status %d, want a 4xx or 5xx", code, spec.status)
		}
		if spec.remediable && spec.hint == "" {
			t.Errorf("code %q is remediable but has no hint", code)
		}
	}
}

func TestRefusalEnvelopeShape(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)

	resp, err := http.Get(srv.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content type = %q, want JSON", got)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codeUnauthorized {
		t.Fatalf("code = %q, want %q", body.Code, codeUnauthorized)
	}
	if !body.Remediable {
		t.Fatal("unauthorized must be marked remediable")
	}
	if body.Hint == "" {
		t.Fatal("unauthorized must carry a hint")
	}
	if body.Detail != "" {
		t.Fatalf("unauthorized detail = %q, want empty", body.Detail)
	}
}

func TestInternalErrorDoesNotLeak(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	s.fail(rec, req, errors.New("store path /home/user/secret.db failed"))

	var body refusalEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError || body.Code != codeInternalError {
		t.Fatalf("status=%d code=%q, want 500 internal_error", rec.Code, body.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("internal detail leaked: %s", rec.Body.String())
	}
}

func TestUnknownRefusalCodeFallsBack(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()
	s.refuse(rec, req, "not_a_registered_code", "secret detail")

	var body refusalEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError || body.Code != codeInternalError {
		t.Fatalf("status=%d code=%q, want 500 internal_error", rec.Code, body.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "not_a_registered") {
		t.Fatalf("unknown code leaked: %s", rec.Body.String())
	}
}

// TestEveryRouteRefusesWithoutAuth walks the same route table Handler uses, so
// a new route cannot appear without the typed 401 path.
func TestEveryRouteRefusesWithoutAuth(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	replacer := strings.NewReplacer("{id}", "obj_missing", "{requestID}", "req_missing", "{ordinal}", "1")
	for _, rt := range s.routeTable() {
		if !rt.auth {
			continue
		}
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			path := replacer.Replace(rt.path)
			req := httptest.NewRequest(rt.method, path, nil)
			req.Host = "127.0.0.1:8787"
			rec := httptest.NewRecorder()
			srv.Config.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
			}
			var body refusalEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not a refusal: %v (%s)", err, rec.Body.String())
			}
			if body.Code != codeUnauthorized {
				t.Fatalf("code = %q, want %q", body.Code, codeUnauthorized)
			}
		})
	}
}

func TestBootstrapRoutesRefuseTyped(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	cases := []struct {
		name       string
		method     string
		path       string
		remoteAddr string
		headers    map[string]string
		body       string
		status     int
		code       string
	}{
		{
			name: "open off host", method: http.MethodPost, path: "/api/v1/open",
			remoteAddr: "203.0.113.9:4444", status: http.StatusForbidden, code: codeOpenHostOnly,
		},
		{
			name: "open without request header", method: http.MethodPost, path: "/api/v1/open",
			remoteAddr: "127.0.0.1:4444", status: http.StatusForbidden, code: codeOriginDenied,
		},
		{
			name: "open with a bad code", method: http.MethodPost, path: "/api/v1/open",
			remoteAddr: "127.0.0.1:4444", headers: map[string]string{"X-DF-Request": "1"},
			body: `{"code":"nope"}`, status: http.StatusUnauthorized, code: codeOpenInvalid,
		},
		{
			name: "pair without request header", method: http.MethodPost, path: "/api/v1/pair",
			remoteAddr: "127.0.0.1:4444", status: http.StatusForbidden, code: codeOriginDenied,
		},
		{
			name: "pair with a bad code", method: http.MethodPost, path: "/api/v1/pair",
			remoteAddr: "127.0.0.1:4444", headers: map[string]string{"X-DF-Request": "1"},
			body: `{"code":"AAAA-AAAA"}`, status: http.StatusUnauthorized, code: codePairInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader
			if tc.body != "" {
				reader = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, reader)
			req.Host = "127.0.0.1:8787"
			req.RemoteAddr = tc.remoteAddr
			for key, value := range tc.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			srv.Config.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			var body refusalEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not a refusal: %v (%s)", err, rec.Body.String())
			}
			if body.Code != tc.code {
				t.Fatalf("code = %q, want %q", body.Code, tc.code)
			}
		})
	}
}

// TestHandlerRefusals covers one representative failure per family, including
// every handler that used to answer with bare text.
func TestWrongMethodRefusesTyped(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	cases := []struct {
		method string
		path   string
		allow  string
	}{
		{http.MethodPut, "/api/v1/state", http.MethodGet},
		{http.MethodPost, "/api/v1/repos/obj_missing", http.MethodDelete},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test-token")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 405: %s", resp.StatusCode, raw)
			}
			if allow := resp.Header.Get("Allow"); !strings.Contains(allow, tc.allow) {
				t.Fatalf("Allow = %q, want %s", allow, tc.allow)
			}
			var body refusalEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != codeMethodNotAllowed {
				t.Fatalf("code = %q, want %q", body.Code, codeMethodNotAllowed)
			}
		})
	}
}

func TestHandlerRefusals(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{"repo body", http.MethodPost, "/api/v1/repos", `{`, http.StatusBadRequest, codeInvalidRequest},
		{"repo not a directory", http.MethodPost, "/api/v1/repos", `{"path":"/does/not/exist"}`, http.StatusBadRequest, codeNotADirectory},
		{"worktree missing repo", http.MethodPost, "/api/v1/worktrees", `{"repoId":"missing"}`, http.StatusNotFound, codeRepoNotFound},
		{"session missing worktree", http.MethodPost, "/api/v1/sessions", `{"worktreeId":"missing","agent":"shell"}`, http.StatusNotFound, codeWorktreeNotFound},
		{"run missing worktree", http.MethodPost, "/api/v1/runs", `{"worktreeId":"missing","goal":"g","steps":[{"kind":"command","command":["true"]}]}`, http.StatusNotFound, codeWorktreeNotFound},
		{"run missing", http.MethodGet, "/api/v1/runs/missing", "", http.StatusNotFound, codeRunNotFound},
		{"run start missing", http.MethodPost, "/api/v1/runs/missing/start", "", http.StatusNotFound, codeRunNotFound},
		{"step ordinal", http.MethodPost, "/api/v1/runs/missing/steps/zero/answer", `{"result":"ok"}`, http.StatusBadRequest, codeInvalidStep},
		{"step missing run", http.MethodPost, "/api/v1/runs/missing/steps/1/answer", `{"result":"ok"}`, http.StatusNotFound, codeRunNotFound},
		{"device missing", http.MethodDelete, "/api/v1/devices/missing", "", http.StatusNotFound, codeDeviceNotFound},
		{"session stream", http.MethodGet, "/api/v1/sessions/missing/stream", "", http.StatusNotFound, codeSessionNotFound},
		{"session input", http.MethodPost, "/api/v1/sessions/missing/input", "x", http.StatusNotFound, codeSessionNotFound},
		{"session resize", http.MethodPost, "/api/v1/sessions/missing/resize", `{"cols":80,"rows":24}`, http.StatusNotFound, codeSessionNotFound},
		{"session redraw", http.MethodPost, "/api/v1/sessions/missing/redraw", "", http.StatusNotFound, codeSessionNotFound},
		{"session delete", http.MethodDelete, "/api/v1/sessions/missing", "", http.StatusNotFound, codeSessionNotFound},
		{"opencode body", http.MethodPost, "/api/v1/opencode/sessions", `{`, http.StatusBadRequest, codeInvalidRequest},
		{"opencode no worktree", http.MethodPost, "/api/v1/opencode/sessions", `{"worktreeId":""}`, http.StatusBadRequest, codeWorktreeRequired},
		{"opencode unknown location", http.MethodGet, "/api/v1/opencode/commands?location=/etc", "", http.StatusForbidden, codeUnknownLocation},
		{"dirs relative", http.MethodGet, "/api/v1/fs/dirs?path=relative", "", http.StatusBadRequest, codeInvalidPath},
		{"dirs missing", http.MethodGet, "/api/v1/fs/dirs?path=/does/not/exist", "", http.StatusBadRequest, codeNotADirectory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader
			if tc.body != "" {
				reader = strings.NewReader(tc.body)
			}
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, reader)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			if tc.method != http.MethodGet {
				req.Header.Set("X-DF-Request", "1")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.status, raw)
			}
			var body refusalEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("body is not a refusal: %v", err)
			}
			if body.Code != tc.code {
				t.Fatalf("code = %q, want %q", body.Code, tc.code)
			}
		})
	}
}

func TestUnknownAgentRefusal(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	worktreeID := createRepoWorkspace(t, srv)

	resp := postJSON(t, srv.URL+"/api/v1/sessions", "test-token", map[string]string{
		"worktreeId": worktreeID,
		"agent":      "nope",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, raw)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codeUnknownAgent {
		t.Fatalf("code = %q, want %q", body.Code, codeUnknownAgent)
	}
}

func TestWorkspaceDeleteRefusal(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	worktreeID := createRepoWorkspace(t, srv)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/worktrees/"+worktreeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409: %s", resp.StatusCode, raw)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codeWorkspaceTied {
		t.Fatalf("code = %q, want %q", body.Code, codeWorkspaceTied)
	}
}

// createRepoWorkspace adds a temp directory as a repo and returns the id of
// the in-place workspace every repo gets.
func createRepoWorkspace(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	resp := postJSON(t, srv.URL+"/api/v1/repos", "test-token", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create repo = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	for _, wt := range state.Worktrees {
		if wt.Workspace {
			return wt.ID
		}
	}
	t.Fatal("repo has no workspace")
	return ""
}

func newRefusalServer(t *testing.T, mutate func(*Server)) (*httptest.Server, *Server) {
	t.Helper()
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	s := New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if mutate != nil {
		mutate(s)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		srv.Close()
		mgr.CloseAll()
		st.Close()
	})
	return srv, s
}
