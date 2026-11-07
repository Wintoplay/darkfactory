package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func nativePaneSession(t *testing.T, srvURL, token string, st *store.Store) model.Session {
	t.Helper()
	resp := postJSON(t, srvURL+"/api/v1/repos", token, map[string]string{
		"path": t.TempDir(), "name": "pane",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()
	worktrees, err := st.ListWorktrees()
	if err != nil || len(worktrees) == 0 {
		t.Fatalf("worktrees = %#v, %v", worktrees, err)
	}
	resp = postJSON(t, srvURL+"/api/v1/sessions", token, map[string]any{
		"worktreeId": worktrees[0].ID, "agent": "shell", "kind": "shell",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create session status = %d: %s", resp.StatusCode, raw)
	}
	defer resp.Body.Close()
	var sess model.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestNativePaneCapabilityIsSessionScoped(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	sess := nativePaneSession(t, srv.URL, cfg.Token, st)

	resp := postJSON(t, srv.URL+"/api/v1/sessions/"+sess.ID+"/native-pane", cfg.Token, nil)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("native-pane status = %d: %s", resp.StatusCode, raw)
	}
	var minted struct {
		Pane string `json:"pane"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if minted.Pane == "" {
		t.Fatal("empty pane capability")
	}

	do := func(method, path, bearer string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		got, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	doJSON := func(method, path, bearer string, body any) *http.Response {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		got, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// A pane is not a general credential.
	state := do(http.MethodGet, "/api/v1/state", minted.Pane)
	if state.StatusCode != http.StatusForbidden {
		t.Fatalf("pane on /state = %d, want 403", state.StatusCode)
	}
	state.Body.Close()

	// Nor is it valid for another session.
	other := do(http.MethodPost, "/api/v1/sessions/ses_other/input", minted.Pane)
	if other.StatusCode != http.StatusForbidden {
		t.Fatalf("pane on another session = %d, want 403", other.StatusCode)
	}
	other.Body.Close()

	// It is valid for the four routes of its own session.
	resize := doJSON(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/resize", minted.Pane, map[string]uint16{"cols": 80, "rows": 24})
	if resize.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resize.Body)
		resize.Body.Close()
		t.Fatalf("pane resize = %d: %s", resize.StatusCode, raw)
	}
	resize.Body.Close()

	// One stream claims the capability.
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sessions/"+sess.ID+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+minted.Pane)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("pane stream = %d", stream.StatusCode)
	}
	if _, err := stream.Body.Read(make([]byte, 1)); err != nil {
		t.Fatalf("read stream: %v", err)
	}

	second := do(http.MethodGet, "/api/v1/sessions/"+sess.ID+"/stream", minted.Pane)
	if second.StatusCode != http.StatusForbidden {
		t.Fatalf("second stream = %d, want 403", second.StatusCode)
	}
	second.Body.Close()

	cancel()
	io.Copy(io.Discard, stream.Body)
	stream.Body.Close()

	// Closing the window drops the capability, so its input token stops too.
	// The stream handler revokes it when it notices the closed connection, so
	// poll briefly instead of assuming it ran before this request.
	deadline := time.Now().Add(2 * time.Second)
	for {
		input := do(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/input", minted.Pane)
		io.Copy(io.Discard, input.Body)
		input.Body.Close()
		if input.StatusCode == http.StatusUnauthorized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("input after close = %d, want 401", input.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativePaneRejectsForwardedRequests(t *testing.T) {
	srv, _, cfg := newAPITestServer(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ses_missing/native-pane", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forwarded mint = %d, want 403", resp.StatusCode)
	}
}
