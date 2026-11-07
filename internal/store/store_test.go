package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
)

func TestStoreCRUD(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repo, err := st.PutRepo(model.Repo{Name: "demo", Path: "/tmp/demo"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.ID == "" {
		t.Fatal("repo id is empty")
	}
	if _, err := st.GetRepo(repo.ID); err != nil {
		t.Fatal(err)
	}

	wt, err := st.PutWorktree(model.Worktree{RepoID: repo.ID, Path: "/tmp/demo-wt", Branch: "orca/demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListWorktrees(); err != nil {
		t.Fatal(err)
	}

	session, err := st.PutSession(model.Session{WorktreeID: wt.ID, Command: "/bin/sh", Status: model.SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" {
		t.Fatal("session id is empty")
	}

	if err := st.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSession(session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted session error = %v", err)
	}
}

func TestBrowserSessionRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	device := Browser{ID: "dev_test", Label: "Phone", Scope: "full", CreatedAt: expiry.Add(-time.Hour), LastSeenAt: expiry.Add(-time.Minute), ExpiresAt: expiry}
	if err := st.PutBrowser("hashed-token", device); err != nil {
		t.Fatal(err)
	}
	browsers, err := st.ListBrowsers()
	if err != nil {
		t.Fatal(err)
	}
	if got := browsers["hashed-token"]; !got.ExpiresAt.Equal(expiry) || got.ID != "dev_test" || got.Scope != "full" {
		t.Fatalf("device = %#v, want %#v", got, device)
	}
	if err := st.DeleteBrowser("hashed-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListBrowsers(); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceKeyRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	key := DeviceKey{
		ID:         "devkey_test",
		Label:      "Desktop",
		Scope:      "full",
		Terminal:   true,
		CreatedAt:  expiry.Add(-time.Hour),
		LastSeenAt: expiry.Add(-time.Minute),
		ExpiresAt:  expiry,
		PublicKey:  "spki-base64",
		Algorithm:  "ES256",
		SecretTag:  "tag",
	}
	if err := st.PutDeviceKey(key); err != nil {
		t.Fatal(err)
	}
	keys, err := st.ListDeviceKeys()
	if err != nil {
		t.Fatal(err)
	}
	if got := keys[key.ID]; got.PublicKey != key.PublicKey || got.Scope != key.Scope ||
		!got.Terminal || !got.ExpiresAt.Equal(expiry) || got.Algorithm != "ES256" {
		t.Fatalf("device key = %#v, want %#v", got, key)
	}
	if err := st.DeleteDeviceKey(key.ID); err != nil {
		t.Fatal(err)
	}
	keys, err = st.ListDeviceKeys()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys[key.ID]; ok {
		t.Fatalf("deleted key still listed: %#v", keys)
	}
}

// A second daemon must fail fast and say why; bbolt's bare "timeout" is what
// used to reach the user.
func TestOpenRefusesASecondWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open = %v, want ErrLocked", err)
	}
}
