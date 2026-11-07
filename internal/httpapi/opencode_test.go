package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func TestOpenCodeProxy(t *testing.T) {
	var gotPrompt string
	var gotCreate map[string]any
	var gotInterrupt bool
	var gotPermissionReply string
	var gotEvents bool
	var gotCommand string
	worktreePath := t.TempDir()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/session":
			if r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("order") != "desc" {
				t.Errorf("session query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, `{"data":[{"id":"ses_test","title":"Test","time":{"created":1,"updated":2},"location":{"directory":"/tmp"}}],"cursor":{"next":null,"previous":null}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/session/ses_test/message":
			query := r.URL.Query()
			if query.Get("cursor") == "cur1" {
				if query.Get("order") != "" {
					t.Errorf("cursor query must not carry order: %q", r.URL.RawQuery)
				}
				writeTestJSON(w, `{"data":[{"id":"msg_0","type":"user","text":"older","time":{"created":0}}],"cursor":{"next":null,"previous":null}}`)
				return
			}
			if query.Get("limit") != "100" || query.Get("order") != "asc" {
				t.Errorf("message query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, `{"data":[{"id":"msg_1","type":"user","text":"hello","time":{"created":1}},{"id":"msg_2","type":"assistant","agent":"build","model":{"id":"model","providerID":"provider"},"content":[{"type":"text","text":"hi"}],"time":{"created":2}}],"cursor":{"next":"cur1","previous":null}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/session/ses_test/prompt":
			raw, _ := io.ReadAll(r.Body)
			gotPrompt = string(raw)
			writeTestJSON(w, `{"data":{"id":"msg_3"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/session/ses_test/interrupt":
			gotInterrupt = true
			writeTestJSON(w, `{"data":{}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/session/ses_test/permission":
			writeTestJSON(w, `{"data":[{"id":"per_1","sessionID":"ses_test","action":"bash","resources":["go test ./..."],"message":"Run tests?"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/session/ses_test/permission/per_1/reply":
			raw, _ := io.ReadAll(r.Body)
			gotPermissionReply = string(raw)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/command":
			if got := r.URL.Query().Get("location[directory]"); got != worktreePath {
				t.Errorf("command location = %q (raw query %q)", got, r.URL.RawQuery)
			}
			writeTestJSON(w, `{"data":[{"name":"init","description":"Initialize the project"},{"name":"undo","description":null}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/session/ses_test/command":
			raw, _ := io.ReadAll(r.Body)
			gotCommand = string(raw)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/event":
			gotEvents = true
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message\ndata: {\"type\":\"session.updated\"}\n\n")
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &gotCreate); err != nil {
				t.Errorf("create body: %v", err)
			}
			writeTestJSON(w, `{"data":{"id":"ses_new","title":"New","time":{"created":1,"updated":2},"location":{"directory":"/tmp"}}}`)
		default:
			t.Errorf("unexpected upstream request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "service.json")
	if err := os.WriteFile(passwordFile, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.OpenCodeAPIURL = upstream.URL
	cfg.OpenCodePasswordFile = passwordFile

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.PutWorktree(model.Worktree{
		ID:        "wt_test",
		RepoID:    "repo_test",
		Path:      worktreePath,
		Branch:    "main",
		BaseRef:   "HEAD",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/opencode/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated opencode status = %d", resp.StatusCode)
	}

	do := func(method, path, body string) (int, string) {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	status, body := do(http.MethodGet, "/api/v1/opencode/sessions", "")
	if status != http.StatusOK || !strings.Contains(body, "ses_test") {
		t.Fatalf("sessions status=%d body=%q", status, body)
	}

	status, body = do(http.MethodGet, "/api/v1/opencode/sessions/ses_test/messages", "")
	if status != http.StatusOK || !strings.Contains(body, "msg_2") {
		t.Fatalf("messages status=%d body=%q", status, body)
	}

	status, body = do(http.MethodGet, "/api/v1/opencode/sessions/ses_test/messages?cursor=cur1", "")
	if status != http.StatusOK || !strings.Contains(body, "msg_0") {
		t.Fatalf("older messages status=%d body=%q", status, body)
	}

	status, body = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/prompt", `{"text":"hello"}`)
	if status != http.StatusOK {
		t.Fatalf("prompt status=%d body=%q", status, body)
	}
	if gotPrompt != `{"text":"hello"}` {
		t.Fatalf("prompt body = %q", gotPrompt)
	}

	status, body = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/interrupt", "")
	if status != http.StatusOK || !gotInterrupt {
		t.Fatalf("interrupt status=%d body=%q called=%v", status, body, gotInterrupt)
	}

	status, body = do(http.MethodGet, "/api/v1/opencode/sessions/ses_test/permissions", "")
	if status != http.StatusOK || !strings.Contains(body, "per_1") {
		t.Fatalf("permissions status=%d body=%q", status, body)
	}

	status, body = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/permissions/per_1/reply", `{"decision":"once"}`)
	if status != http.StatusNoContent {
		t.Fatalf("permission reply status=%d body=%q", status, body)
	}
	if gotPermissionReply != `{"decision":"once","message":""}` {
		t.Fatalf("permission reply body = %q", gotPermissionReply)
	}

	status, body = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/permissions/per_1/reply", `{"decision":"maybe"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid permission decision status=%d body=%q", status, body)
	}

	status, body = do(http.MethodGet, "/api/v1/opencode/commands?location="+url.QueryEscape(worktreePath), "")
	if status != http.StatusOK || !strings.Contains(body, "init") {
		t.Fatalf("commands status=%d body=%q", status, body)
	}

	status, _ = do(http.MethodGet, "/api/v1/opencode/commands?location="+url.QueryEscape("/etc"), "")
	if status != http.StatusForbidden {
		t.Fatalf("unknown command location status = %d", status)
	}

	status, body = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/command", `{"name":"init","text":"--force"}`)
	if status != http.StatusNoContent {
		t.Fatalf("command status=%d body=%q", status, body)
	}
	if gotCommand != `{"name":"init","text":"--force"}` {
		t.Fatalf("command body = %q", gotCommand)
	}

	status, _ = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/command", `{"name":"../etc"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid command name status = %d", status)
	}

	status, body = do(http.MethodGet, "/api/v1/opencode/events", "")
	if status != http.StatusOK || !gotEvents || !strings.Contains(body, "session.updated") {
		t.Fatalf("events status=%d body=%q called=%v", status, body, gotEvents)
	}

	status, body = do(http.MethodPost, "/api/v1/opencode/sessions", `{"worktreeId":"wt_test","title":"New","agent":"build"}`)
	if status != http.StatusOK {
		t.Fatalf("create status=%d body=%q", status, body)
	}
	location, ok := gotCreate["location"].(map[string]any)
	if !ok || location["directory"] != worktreePath {
		t.Fatalf("create location = %#v", gotCreate["location"])
	}
	if gotCreate["title"] != "New" || gotCreate["agent"] != "build" {
		t.Fatalf("create body = %#v", gotCreate)
	}

	status, _ = do(http.MethodGet, "/api/v1/opencode/sessions/ses_test/messages?limit=nope", "")
	if status != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", status)
	}

	status, _ = do(http.MethodPost, "/api/v1/opencode/sessions/ses_test/prompt", `{"text":""}`)
	if status != http.StatusBadRequest {
		t.Fatalf("empty prompt status = %d", status)
	}
}

func TestOpenCodeStateAndMissingCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.OpenCodeAPIURL = "http://127.0.0.1:1"
	cfg.OpenCodePasswordFile = filepath.Join(t.TempDir(), "missing.json")

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.OpenCode {
		t.Fatal("OpenCode must be disabled without a password file")
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/opencode/sessions", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("missing credentials status = %d", resp.StatusCode)
	}
}

func writeTestJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}
