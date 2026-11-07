package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// The ring is the activity list; Alert is what may interrupt. Turning replies
// off must not hide them from the bell, and a finished session must follow the
// same switch instead of being silently always-on.
func TestNotificationSettingsSeparateAlertsFromTheRing(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	s.notify.desktop = nil

	run, err := s.store.PutRun(modelRun("run_alert", "ship it"))
	if err != nil {
		t.Fatal(err)
	}
	s.notify.pollState() // baseline
	run.State = model.RunBlocked
	if _, err := s.store.PutRun(run); err != nil {
		t.Fatal(err)
	}
	s.notify.pollState()
	events := s.notify.testEvents()
	if len(events) != 1 || !events[0].Alert {
		t.Fatalf("blocked run = %#v, want one alerting event", events)
	}

	settings := store.DefaultNotificationSettings()
	settings.BlockedRuns = false
	settings.FinishedSessions = false
	if err := s.notify.setNotificationSettings(settings); err != nil {
		t.Fatal(err)
	}
	run.State = model.RunWaiting
	if _, err := s.store.PutRun(run); err != nil {
		t.Fatal(err)
	}
	s.notify.pollState()
	events = s.notify.testEvents()
	if len(events) != 2 || events[1].Alert {
		t.Fatalf("waiting run with alerts off = %#v, want a silent ring row", events)
	}

	// The policy survives a restart because it is in state.db, not only in the
	// running hub.
	saved, err := s.store.GetNotificationSettings()
	if err != nil {
		t.Fatal(err)
	}
	if saved.BlockedRuns || saved.FinishedSessions || saved.ChildSessions || saved.Replies {
		t.Fatalf("saved settings = %#v", saved)
	}
	if !saved.Prompts {
		t.Fatalf("prompts switch was lost: %#v", saved)
	}
}

// A busy parent produces a child chat per tool call. With the child switch off
// those chats must not even be polled for prompts, so a subagent request cannot
// ring the phone; turning the switch on brings them back.
func TestChildSessionsFollowTheAlertSwitch(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	s.notify.desktop = nil
	chatDir := t.TempDir()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/session":
			fmt.Fprintf(w, `{"data":[
				{"id":"ses_parent","title":"Parent","location":{"directory":%q},"time":{"updated":%d}},
				{"id":"ses_child","title":"Child","parentID":"ses_parent","location":{"directory":%q},"time":{"updated":%d}}
			]}`, chatDir, time.Now().UnixMilli(), chatDir, time.Now().UnixMilli())
		case "/api/session/ses_parent/permission":
			_, _ = w.Write([]byte(`{"data":[{"id":"per_parent","action":"bash"}]}`))
		case "/api/session/ses_child/permission":
			_, _ = w.Write([]byte(`{"data":[{"id":"per_child","action":"edit"}]}`))
		default:
			// Forms are polled beside permissions; any other path is empty.
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer fake.Close()
	pw := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(pw, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.openCode = newOpenCodeClient(config.Config{OpenCodeAPIURL: fake.URL, OpenCodePasswordFile: pw})

	s.notify.pollPrompts(t.Context())
	events := s.notify.testEvents()
	if len(events) != 1 || events[0].OpenCodeSessionID != "ses_parent" {
		t.Fatalf("parent-only priming = %#v", events)
	}

	settings := store.DefaultNotificationSettings()
	settings.ChildSessions = true
	if err := s.notify.setNotificationSettings(settings); err != nil {
		t.Fatal(err)
	}
	s.notify.pollPrompts(t.Context())
	events = s.notify.testEvents()
	if len(events) != 2 || events[1].OpenCodeSessionID != "ses_child" {
		t.Fatalf("child switch on = %#v, want the child prompt", events)
	}
}

func modelRun(id, goal string) (run model.Run) {
	run.ID = id
	run.WorktreeID = "wt_alert"
	run.Goal = goal
	run.State = model.RunRunning
	run.CreatedAt = time.Now().UTC()
	return run
}

func TestNotificationSettingsEndpointRoundTrip(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	want := store.NotificationSettings{
		FinishedSessions: true,
		Replies:          true,
		BlockedRuns:      false,
		Prompts:          true,
		ChildSessions:    true,
	}
	body, _ := json.Marshal(want)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/notifications/settings", bytes.NewReader(body))
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications/settings", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got store.NotificationSettings
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
}

// The newest registration wins only when it answers. A stale file left behind
// by a previous port must not hide the live ocd/opencode service.
func TestOpenCodeDiscoveryPrefersTheLiveRegistration(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var gotPassword string
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || r.URL.Path != "/api/session" {
			http.NotFound(w, r)
			return
		}
		gotPassword = password
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer live.Close()

	dir := filepath.Join(stateDir, "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The live row is older; the dead row gets a future mtime so sorting alone
	// would pick it. The probe must reject it and fall through.
	if err := os.WriteFile(filepath.Join(dir, "service-v2.json"),
		[]byte(`{"url":"`+live.URL+`","password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "service.json")
	if err := os.WriteFile(stale, []byte(`{"url":"http://127.0.0.1:1","password":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(stale, later, later); err != nil {
		t.Fatal(err)
	}
	credentialFile := filepath.Join(t.TempDir(), "service-local-install.json")
	if err := os.WriteFile(credentialFile, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.OpenCodePasswordFile = credentialFile
	client := newOpenCodeClient(cfg)
	if client == nil {
		t.Fatal("client is nil")
	}
	endpoint, err := client.endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.baseURL != live.URL || gotPassword != "secret" {
		t.Fatalf("endpoint = %#v password=%q", endpoint, gotPassword)
	}

	// A non-loopback registration is ignored even when it is the newest file:
	// the state file must never be able to receive the service password.
	remote := filepath.Join(dir, "service-local-install.json")
	if err := os.WriteFile(remote, []byte(`{"url":"http://203.0.113.9:9999","password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	later = time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(remote, later, later); err != nil {
		t.Fatal(err)
	}
	// Bypass the cache, as a restart would.
	client.resolved = time.Time{}
	endpoint, err = client.endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.baseURL != live.URL {
		t.Fatalf("remote registration won: %#v", endpoint)
	}
}

// An explicitly configured URL is the owner's choice and must not be replaced
// by whatever a state file happens to say. This also keeps tests hermetic.
func TestOpenCodeExplicitURLSkipsDiscovery(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	dir := filepath.Join(stateDir, "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.json"), []byte(`{"url":"http://127.0.0.1:1","password":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialFile := filepath.Join(t.TempDir(), "service.json")
	if err := os.WriteFile(credentialFile, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OpenCodeAPIURL = "http://127.0.0.1:43210"
	cfg.OpenCodePasswordFile = credentialFile
	client := newOpenCodeClient(cfg)
	endpoint, err := client.endpoint()
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.baseURL != "http://127.0.0.1:43210" || endpoint.password != "secret" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
}
