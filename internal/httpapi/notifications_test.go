package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/model"
)

func (h *notificationHub) testEvents() []Notification {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Notification(nil), h.events...)
}

func (h *notificationHub) testOpen() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.open)
}

func TestNotificationFeedBaselinesAndReportsTransitions(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	s.notify.desktop = nil

	// Existing rows are a baseline: starting the daemon must not replay them.
	if _, err := s.store.PutSession(model.Session{
		ID: "ses_a", WorktreeID: "wt_1", Title: "first", Status: model.SessionRunning,
		CreatedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := s.store.PutRun(model.Run{
		ID: "run_a", WorktreeID: "wt_1", Goal: "ship the thing", State: model.RunRunning,
		CreatedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.notify.pollState()
	if got := s.notify.testEvents(); len(got) != 0 {
		t.Fatalf("baseline = %v, want no events", got)
	}

	code := 0
	if _, err := s.store.PutSession(model.Session{
		ID: "ses_a", WorktreeID: "wt_1", Title: "first", Status: model.SessionExited, ExitCode: &code,
		CreatedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	run.State = model.RunBlocked
	if _, err := s.store.PutRun(run); err != nil {
		t.Fatal(err)
	}
	s.notify.pollState()
	events := s.notify.testEvents()
	if len(events) != 2 {
		t.Fatalf("events = %d, want session + run", len(events))
	}
	if events[0].Kind != "session.exited" || events[0].Title != "Agent finished" {
		t.Fatalf("session event = %#v", events[0])
	}
	if events[0].SessionID != "ses_a" || events[0].WorktreeID != "wt_1" {
		t.Fatalf("session event target = %#v", events[0])
	}
	if events[1].Kind != "run.blocked" || events[1].Title != "Agent needs your input" {
		t.Fatalf("run event = %#v", events[1])
	}
	if events[1].WorktreeID != "wt_1" || events[1].SessionID != "" {
		t.Fatalf("run event target = %#v", events[1])
	}

	// Re-poll: no duplicate alerts for the same transition.
	s.notify.pollState()
	if got := s.notify.testEvents(); len(got) != 2 {
		t.Fatalf("duplicate poll grew the ring to %d", len(got))
	}

	// A missing cursor is a baseline: report the current sequence, not a replay.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var page struct {
		Next   int64          `json:"next"`
		Events []Notification `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Next != 2 || len(page.Events) != 0 {
		t.Fatalf("baseline route = next=%d events=%d, want 2 and none", page.Next, len(page.Events))
	}

	// An explicit after=0 asks for the retained ring. A service that recorded
	// cursor 0 at baseline can still see the first event instead of losing it.
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	respZero, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer respZero.Body.Close()
	var pageZero struct {
		Next   int64          `json:"next"`
		Events []Notification `json:"events"`
	}
	if err := json.NewDecoder(respZero.Body).Decode(&pageZero); err != nil {
		t.Fatal(err)
	}
	if pageZero.Next != 2 || len(pageZero.Events) != 2 {
		t.Fatalf("after=0 = next=%d events=%d, want 2 and 2", pageZero.Next, len(pageZero.Events))
	}

	// After the baseline cursor, a later transition is delivered once.
	run.State = model.RunWaiting
	if _, err := s.store.PutRun(run); err != nil {
		t.Fatal(err)
	}
	s.notify.pollState()
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications?after=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var page2 struct {
		Next   int64          `json:"next"`
		Events []Notification `json:"events"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&page2); err != nil {
		t.Fatal(err)
	}
	if page2.Next != 3 || len(page2.Events) != 1 || page2.Events[0].Kind != "run.waiting" {
		t.Fatalf("after cursor = next=%d events=%#v", page2.Next, page2.Events)
	}
}

func TestDesktopNotificationPassesTitleAndBodyAsArguments(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "notify-send")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sendDesktopNotification(script, Notification{
		Kind:  "run.blocked",
		Title: "-needs input",
		Body:  "line one\nline two",
	})
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"-a\nDarkFactory\n", "-u\ncritical\n", "--\n-needs input\n", "line one line two\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notify-send args missing %q:\n%s", want, got)
		}
	}
}

func TestNotificationPermissionsAreEmittedOnce(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	s.notify.desktop = nil
	// A permission event must name the workspace it came from: an OpenCode chat
	// is not a PTY session, so the directory is the only link back.
	chatDir := t.TempDir()
	repo, err := s.store.PutRepo(model.Repo{Name: "chat-repo", Path: chatDir})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := s.store.PutWorktree(model.Worktree{RepoID: repo.ID, Path: chatDir})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	permissions := []openCodePermissionRow{{ID: "per_1", Action: "bash"}}
	var forms []openCodeFormRow
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session":
			fmt.Fprintf(w, `{"data":[{"id":"ses_a","title":"Chat A","location":{"directory":%q},"time":{"updated":%d}}]}`, chatDir, time.Now().UnixMilli())
		case "/api/session/ses_a/permission":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": permissions})
		case "/api/session/ses_a/form":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": forms})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	pw := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(pw, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.openCode = newOpenCodeClient(config.Config{OpenCodeAPIURL: fake.URL, OpenCodePasswordFile: pw})

	s.notify.pollPrompts(t.Context())
	// A prompt that was already waiting is not alerted, but it does join the
	// ring: the activity list a page loads later has to show what is blocking.
	primed := s.notify.testEvents()
	if len(primed) != 1 || primed[0].OpenCodeSessionID != "ses_a" {
		t.Fatalf("primed permission = %#v", primed)
	}
	mu.Lock()
	permissions = append(permissions, openCodePermissionRow{ID: "per_2", Action: "edit"})
	mu.Unlock()
	s.notify.pollPrompts(t.Context())
	events := s.notify.testEvents()
	if len(events) != 2 {
		t.Fatalf("permission events = %d, want 2", len(events))
	}
	events = events[1:]
	if events[0].Kind != "opencode.permission" || events[0].Title != "Permission requested" {
		t.Fatalf("permission event = %#v", events[0])
	}
	if events[0].Body != "Chat A: edit" {
		t.Fatalf("permission body = %q", events[0].Body)
	}
	if events[0].SessionTitle != "Chat A" || events[0].OpenCodeSessionID != "ses_a" {
		t.Fatalf("permission source = %q / %q", events[0].SessionTitle, events[0].OpenCodeSessionID)
	}
	if events[0].WorktreeID != wt.ID {
		t.Fatalf("permission worktree = %q, want %q", events[0].WorktreeID, wt.ID)
	}
	if events[0].SessionID != "" {
		t.Fatalf("permission sessionId = %q, want empty: an OpenCode chat is not a PTY session", events[0].SessionID)
	}

	s.notify.pollPrompts(t.Context())
	if got := s.notify.testEvents(); len(got) != 2 {
		t.Fatalf("duplicate permission poll = %d events, want 2", len(got))
	}
	if got := s.notify.testOpen(); got != 2 {
		t.Fatalf("open prompts = %d, want the two awaiting decisions", got)
	}
}

// A question the agent asks is the same kind of block as a permission: the turn
// does not continue until the owner answers, so the bell has to ring with the
// chat and the workspace, and a page that reloads still sees it.
func TestNotificationQuestionsReachTheBellWithTheirSource(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	s.notify.desktop = nil
	chatDir := t.TempDir()
	repo, err := s.store.PutRepo(model.Repo{Name: "ask-repo", Path: chatDir})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := s.store.PutWorktree(model.Worktree{RepoID: repo.ID, Path: chatDir})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	forms := []openCodeFormRow{{ID: "form_1", Title: "Which database should I use?"}}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session":
			fmt.Fprintf(w, `{"data":[{"id":"ses_q","title":"Chat Q","location":{"directory":%q},"time":{"updated":%d}}]}`, chatDir, time.Now().UnixMilli())
		case "/api/session/ses_q/permission":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []openCodePermissionRow{}})
		case "/api/session/ses_q/form":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": forms})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	pw := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(pw, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.openCode = newOpenCodeClient(config.Config{OpenCodeAPIURL: fake.URL, OpenCodePasswordFile: pw})

	// Priming does not alert, but the question still joins the ring.
	s.notify.pollPrompts(t.Context())
	primed := s.notify.testEvents()
	if len(primed) != 1 {
		t.Fatalf("primed questions = %d, want 1", len(primed))
	}
	event := primed[0]
	if event.Kind != "opencode.question" || event.Title != "Question for you" {
		t.Fatalf("question event = %#v", event)
	}
	if event.Body != "Which database should I use?" {
		t.Fatalf("question body = %q", event.Body)
	}
	if event.SessionTitle != "Chat Q" || event.OpenCodeSessionID != "ses_q" {
		t.Fatalf("question source = %q / %q", event.SessionTitle, event.OpenCodeSessionID)
	}
	if event.WorktreeID != wt.ID {
		t.Fatalf("question worktree = %q, want %q", event.WorktreeID, wt.ID)
	}
	if event.SessionID != "" {
		t.Fatalf("question sessionId = %q, want empty: an OpenCode chat is not a PTY session", event.SessionID)
	}

	// Answering the form drops the open prompt. The answered row stays in the
	// ring until the owner dismisses it, which is the manual-clear contract.
	mu.Lock()
	forms = nil
	mu.Unlock()
	s.notify.pollPrompts(t.Context())
	if got := s.notify.testOpen(); got != 0 {
		t.Fatalf("answered question is still open (%d)", got)
	}
	if got := s.notify.testEvents(); len(got) != 1 {
		t.Fatalf("answered question left the ring: %#v", got)
	}

	// A later question is a new event in the ring, not a repeat of the first.
	mu.Lock()
	forms = []openCodeFormRow{{ID: "form_2", Title: "And the schema?"}}
	mu.Unlock()
	s.notify.pollPrompts(t.Context())
	ring := s.notify.testEvents()
	if len(ring) != 2 || ring[1].Kind != "opencode.question" || ring[1].Body != "And the schema?" {
		t.Fatalf("ring = %#v", ring)
	}
}

// A chat created in a subdirectory still belongs to the workspace above it, and
// a chat outside every workspace belongs to none: the panel may show an event
// without a link, but it must never link the wrong workspace.
func TestWorktreeIDForDirectory(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	root := t.TempDir()
	repo, err := s.store.PutRepo(model.Repo{Name: "sub", Path: root})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := s.store.PutWorktree(model.Worktree{RepoID: repo.ID, Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.worktreeIDForDirectory(root + "/pkg/deep"); got != wt.ID {
		t.Fatalf("subdirectory = %q, want %q", got, wt.ID)
	}
	// A sibling with the same prefix is not inside the worktree.
	if got := s.worktreeIDForDirectory(root + "-other"); got != "" {
		t.Fatalf("sibling prefix = %q, want empty", got)
	}
	if got := s.worktreeIDForDirectory(""); got != "" {
		t.Fatalf("empty directory = %q, want empty", got)
	}
}

// An answer that asks nothing is still activity: the bell used to stay empty
// after a normal exchange, which read as broken. A chat's idle timestamp moving
// forward is the signal; an unchanged timestamp must not repeat the row.
func TestNotificationReplyRaisesActivity(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	s.notify.desktop = nil
	chatDir := t.TempDir()
	repo, err := s.store.PutRepo(model.Repo{Name: "reply-repo", Path: chatDir})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := s.store.PutWorktree(model.Worktree{RepoID: repo.ID, Path: chatDir})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	idle := int64(1_000)
	outcome := "succeeded"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session":
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprintf(w, `{"data":[{"id":"ses_r","title":"Chat R","outcome":%q,"location":{"directory":%q},"time":{"updated":%d,"idle":%d}}]}`,
				outcome, chatDir, time.Now().UnixMilli(), idle)
		case "/api/session/ses_r/permission":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []openCodePermissionRow{}})
		case "/api/session/ses_r/form":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []openCodeFormRow{}})
		case "/api/session/ses_r/message":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": "m1", "type": "assistant", "content": []map[string]any{
					{"type": "text", "text": "Morning! How can I help?"}}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	pw := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(pw, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.openCode = newOpenCodeClient(config.Config{OpenCodeAPIURL: fake.URL, OpenCodePasswordFile: pw})

	// The first poll primes: a chat that finished before the daemon started is
	// not news, and must not ring on every restart.
	s.notify.pollPrompts(t.Context())
	if got := s.notify.testEvents(); len(got) != 0 {
		t.Fatalf("reply baseline = %d events", len(got))
	}

	mu.Lock()
	idle = 2_000
	mu.Unlock()
	s.notify.pollPrompts(t.Context())
	events := s.notify.testEvents()
	if len(events) != 1 {
		t.Fatalf("reply events = %d, want 1", len(events))
	}
	if events[0].Kind != "opencode.replied" || events[0].Title != "OpenCode replied" {
		t.Fatalf("reply event = %#v", events[0])
	}
	if events[0].Body != "Morning! How can I help?" || events[0].SessionTitle != "Chat R" {
		t.Fatalf("reply body = %q / %q", events[0].Body, events[0].SessionTitle)
	}
	if events[0].OpenCodeSessionID != "ses_r" || events[0].WorktreeID != wt.ID {
		t.Fatalf("reply source = %q / %q", events[0].OpenCodeSessionID, events[0].WorktreeID)
	}

	// The same finished turn must not ring twice.
	s.notify.pollPrompts(t.Context())
	if got := s.notify.testEvents(); len(got) != 1 {
		t.Fatalf("duplicate reply poll = %d events, want 1", len(got))
	}

	// A failed turn still reports, and says so.
	mu.Lock()
	idle = 3_000
	outcome = "failed"
	mu.Unlock()
	s.notify.pollPrompts(t.Context())
	events = s.notify.testEvents()
	if len(events) != 2 || events[1].Body != "Morning! How can I help? (failed)" {
		t.Fatalf("failed reply = %#v", events)
	}
}

// Dismiss and Clear all change the ring, not just the page: a reload reads the
// ring, so a local removal alone would bring the row straight back.
func TestDismissAndClearNotificationsAreMutations(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	s.notify.desktop = nil
	s.notify.emit([]Notification{
		{Kind: "session.exited", Title: "A"},
		{Kind: "opencode.replied", Title: "B"},
	})

	read := func(query string) []Notification {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var page struct {
			Events []Notification `json:"events"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		return page.Events
	}
	if got := read("?after=0"); len(got) != 2 {
		t.Fatalf("ring = %d events, want 2", len(got))
	}

	resp := postJSON(t, srv.URL+"/api/v1/notifications/dismiss", "test-token", map[string]any{"ids": []int64{1}})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dismiss status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := read("?after=0"); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("after dismiss = %#v", got)
	}

	resp = postJSON(t, srv.URL+"/api/v1/notifications/clear", "test-token", map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := read("?after=0"); len(got) != 0 {
		t.Fatalf("after clear = %#v", got)
	}

	// Both are audited mutations like any other.
	entries, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	verbs := map[string]int{}
	for _, entry := range entries {
		if entry.Verb != "" {
			verbs[entry.Verb]++
		}
	}
	if verbs[verbNotificationDismiss] == 0 || verbs[verbNotificationClear] == 0 {
		t.Fatalf("ledger verbs = %#v", verbs)
	}
}
