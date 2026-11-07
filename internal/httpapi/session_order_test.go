package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// seedWorkspaceSessions writes n sessions into one workspace, oldest first.
func seedWorkspaceSessions(t *testing.T, s *Server, worktreeID string, n int) []model.Session {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows := make([]model.Session, 0, n)
	for i := 0; i < n; i++ {
		sess, err := s.store.PutSession(model.Session{
			WorktreeID: worktreeID,
			Kind:       "shell",
			Command:    "/bin/sh",
			Title:      "s" + string(rune('a'+i)),
			Status:     model.SessionExited,
			CreatedAt:  base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, sess)
	}
	return rows
}

func stateSessionOrder(t *testing.T, srvURL, worktreeID string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state struct {
		Sessions []model.Session `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, sess := range state.Sessions {
		if sess.WorktreeID == worktreeID {
			order = append(order, sess.ID)
		}
	}
	return order
}

func postJSONWithBody(t *testing.T, srvURL, path string, body any) (*http.Response, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srvURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func TestReorderSessionsPersistsAndIsAudited(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	_, rows := seedFleet(t, s, 1)
	wt := rows[0]
	sessions := seedWorkspaceSessions(t, s, wt.ID, 3)

	order := []string{sessions[2].ID, sessions[0].ID, sessions[1].ID}
	resp, body := postJSONWithBody(t, srv.URL, "/api/v1/sessions/order", map[string]any{"worktreeId": wt.ID, "ids": order})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	got := stateSessionOrder(t, srv.URL, wt.ID)
	if len(got) != 3 || got[0] != order[0] || got[1] != order[1] || got[2] != order[2] {
		t.Fatalf("session order = %v, want %v", got, order)
	}

	// The drag is a mutation like any other: a decision row plus an outcome row
	// naming the workspace the client reordered.
	entries, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("ledger rows = %d, want a decision and an outcome", len(entries))
	}
	if entries[0].Verb != verbSessionOrder || entries[0].Decision != store.LedgerAllow {
		t.Fatalf("decision row = %#v", entries[0])
	}
	if entries[1].Outcome != store.LedgerApplied || entries[1].Target != wt.ID {
		t.Fatalf("outcome row = %#v, want applied against the workspace", entries[1])
	}
}

func TestReorderRefusesASessionFromAnotherWorkspace(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	_, rows := seedFleet(t, s, 2)
	mine := seedWorkspaceSessions(t, s, rows[0].ID, 1)
	other := seedWorkspaceSessions(t, s, rows[1].ID, 1)

	resp, body := postJSONWithBody(t, srv.URL, "/api/v1/sessions/order", map[string]any{
		"worktreeId": rows[0].ID,
		"ids":        []string{mine[0].ID, other[0].ID},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var envelope refusalEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != codeSessionOrderInvalid {
		t.Fatalf("code = %q, want %q", envelope.Code, codeSessionOrderInvalid)
	}
}

func TestPinSessionFloatsToTheTopAndIsAudited(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	_, rows := seedFleet(t, s, 1)
	sessions := seedWorkspaceSessions(t, s, rows[0].ID, 3)
	youngest := sessions[2]

	resp, body := postJSONWithBody(t, srv.URL, "/api/v1/sessions/"+youngest.ID+"/pin", map[string]any{"pinned": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pin status = %d: %s", resp.StatusCode, body)
	}
	var pinned model.Session
	if err := json.Unmarshal(body, &pinned); err != nil {
		t.Fatal(err)
	}
	if !pinned.Pinned {
		t.Fatalf("response did not mark the session pinned: %#v", pinned)
	}
	order := stateSessionOrder(t, srv.URL, rows[0].ID)
	if len(order) != 3 || order[0] != youngest.ID {
		t.Fatalf("pinned order = %v, want %q first", order, youngest.ID)
	}

	// Unpinning puts it back among the others, oldest first.
	if resp, body := postJSONWithBody(t, srv.URL, "/api/v1/sessions/"+youngest.ID+"/pin", map[string]any{"pinned": false}); resp.StatusCode != http.StatusOK {
		t.Fatalf("unpin status = %d: %s", resp.StatusCode, body)
	}
	order = stateSessionOrder(t, srv.URL, rows[0].ID)
	if len(order) != 3 || order[2] != youngest.ID {
		t.Fatalf("unpinned order = %v, want %q last", order, youngest.ID)
	}

	entries, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	var pins int
	for _, entry := range entries {
		if entry.Verb == verbSessionPin {
			pins++
		}
	}
	if pins != 4 {
		t.Fatalf("ledger pin rows = %d, want two decision/outcome pairs", pins)
	}
}

func TestPinSessionRequiresAnExplicitValue(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	_, rows := seedFleet(t, s, 1)
	sessions := seedWorkspaceSessions(t, s, rows[0].ID, 1)

	resp, body := postJSONWithBody(t, srv.URL, "/api/v1/sessions/"+sessions[0].ID+"/pin", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var envelope refusalEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != codeInvalidRequest {
		t.Fatalf("code = %q, want %q", envelope.Code, codeInvalidRequest)
	}
}

func TestRevealWorktreeUsesTheStoredDirectory(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	dir := t.TempDir()
	repo, err := s.store.PutRepo(model.Repo{Name: "reveal", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.store.PutWorktree(model.Worktree{RepoID: repo.ID, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	rows := []model.Worktree{row}

	// Do not start a real file manager from a test suite.
	var revealed string
	original := revealFolder
	revealFolder = func(path string) error {
		revealed = path
		return nil
	}
	t.Cleanup(func() { revealFolder = original })

	resp, body := postJSONWithBody(t, srv.URL, "/api/v1/worktrees/"+rows[0].ID+"/reveal", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if revealed != rows[0].Path {
		t.Fatalf("revealed %q, want the stored worktree path %q", revealed, rows[0].Path)
	}

	resp, body = postJSONWithBody(t, srv.URL, "/api/v1/worktrees/missing/reveal", map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown worktree status = %d: %s", resp.StatusCode, body)
	}

	revealFolder = func(string) error { return errNoFileManager }
	resp, body = postJSONWithBody(t, srv.URL, "/api/v1/worktrees/"+rows[0].ID+"/reveal", map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("no file manager status = %d: %s", resp.StatusCode, body)
	}
	var envelope refusalEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != codeRevealUnavailable {
		t.Fatalf("code = %q, want %q", envelope.Code, codeRevealUnavailable)
	}
}

func TestRevealRefusesAMissingDirectory(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	repo, err := s.store.PutRepo(model.Repo{Name: "gone", Path: "/tmp/gone"})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := s.store.PutWorktree(model.Worktree{RepoID: repo.ID, Path: "/nonexistent/darkfactory-reveal"})
	if err != nil {
		t.Fatal(err)
	}
	resp, body := postJSONWithBody(t, srv.URL, "/api/v1/worktrees/"+wt.ID+"/reveal", map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var envelope refusalEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != codeRevealUnavailable {
		t.Fatalf("code = %q, want %q", envelope.Code, codeRevealUnavailable)
	}
}
