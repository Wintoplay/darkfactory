package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionCapturesOutput(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "printf 'hello from pty'"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish")
	}
	// Done can close before the PTY reader has copied the final chunk into the
	// ring; wait for the bytes rather than sampling once.
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for {
		got = string(sess.Snapshot())
		if strings.Contains(got, "hello from pty") || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(got, "hello from pty") {
		t.Fatalf("snapshot = %q", got)
	}
	if sess.Status() != "exited" {
		t.Fatalf("status = %q", sess.Status())
	}
}

func TestSessionSavesTranscriptOnExit(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()
	saved := make(chan []byte, 1)
	mgr.SetTranscript(func(_ string, data []byte) {
		saved <- append([]byte(nil), data...)
	})
	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "printf 'persist me'; sleep 5"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(string(sess.Snapshot()), "persist me") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	sess.Stop("shutdown")
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish")
	}
	select {
	case got := <-saved:
		if !strings.Contains(string(got), "persist me") {
			t.Fatalf("transcript = %q", got)
		}
	default:
		t.Fatal("transcript was not saved")
	}
}

func TestSessionStopRecordsIdentity(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := sess.Model()
	if model.PID <= 1 || model.PGID != model.PID || model.StartTicks == 0 || model.IncarnationID == "" {
		t.Fatalf("session identity is incomplete: %#v", model)
	}
	if verdict := sess.Stop("test_stop"); verdict != StopStopped {
		t.Fatalf("stop verdict = %q, want stopped", verdict)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stopped session did not finish")
	}
	final := sess.Model()
	if final.Status != "exited" || final.Reason != "test_stop" || final.EndedAt == nil {
		t.Fatalf("final session = %#v", final)
	}
	if ProcessExists(model.PID) && ProcessMatches(model.PID, model.StartTicks) {
		t.Fatal("stopped process is still alive")
	}
}

func TestStopStoredRefusesReusedPID(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := sess.Model()
	if verdict := StopStored(model.PID, model.StartTicks+1, time.Second); verdict != StopUnverifiable {
		t.Fatalf("verdict = %q, want unverifiable", verdict)
	}
	if !ProcessExists(model.PID) {
		t.Fatal("unverifiable stop must not kill a process")
	}
	sess.Stop("test_cleanup")
}

func TestJailProjectsHomeAndMasksHiddenPath(t *testing.T) {
	if !JailAvailable() {
		t.Skip("bwrap is not installed or cannot create a namespace")
	}
	mgr := NewManager(4096)
	defer mgr.CloseAll()
	home := filepath.Join(t.TempDir(), "home")
	dir := t.TempDir()
	hidden := filepath.Join(t.TempDir(), "daemon")
	if err := os.MkdirAll(hidden, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "token"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.Start(Spec{
		Dir:         dir,
		Kind:        "shell",
		Command:     "/bin/sh",
		Args:        []string{"-c", `printf 'home=%s\n' "$HOME"; cat ` + filepath.Join(hidden, "token") + ` 2>/dev/null || printf 'hidden-ok\n'`},
		Containment: "jail",
		Home:        home,
		HiddenPaths: []string{hidden},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("jail session did not finish")
	}
	out := string(sess.Snapshot())
	if !strings.Contains(out, "home="+home) {
		t.Fatalf("HOME was not projected: %q", out)
	}
	if !strings.Contains(out, "hidden-ok") || strings.Contains(out, "secret") {
		t.Fatalf("hidden path was visible: %q", out)
	}
	if sess.Model().Containment != "jail" {
		t.Fatalf("containment = %q", sess.Model().Containment)
	}
}

func TestJailEnvironmentScrubKeepsOnlyAllowedValues(t *testing.T) {
	base := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/home/real",
		"LANG=en_US.UTF-8",
		"DISPLAY=:0",
		"WAYLAND_DISPLAY=wayland-0",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
		"SSH_AUTH_SOCK=/run/user/1000/ssh-agent",
		"OPENAI_API_KEY=sk-live-do-not-leak",
		"XDG_RUNTIME_DIR=/run/user/1000",
	}
	env := sessionEnvironment(base, Spec{
		Containment: "jail",
		ScrubEnv:    true,
		Home:        "/work/homes/wt1",
		EnvAllow:    []string{"SSH_AUTH_SOCK"},
		Env:         map[string]string{"AGENT_KEY": "present", "PATH": "/opt/bin"},
	}, "jail")

	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "OPENAI_API_KEY", "XDG_RUNTIME_DIR"} {
		if _, ok := testEnvValue(env, key); ok {
			t.Fatalf("%s leaked into a scrubbed jail: %#v", key, env)
		}
	}
	if got, _ := testEnvValue(env, "SSH_AUTH_SOCK"); got != "/run/user/1000/ssh-agent" {
		t.Fatalf("allowed SSH_AUTH_SOCK = %q", got)
	}
	if got, _ := testEnvValue(env, "HOME"); got != "/work/homes/wt1" {
		t.Fatalf("HOME = %q, want the projected home", got)
	}
	if got, _ := testEnvValue(env, "PATH"); got != "/opt/bin" {
		t.Fatalf("PATH = %q, want the preset override", got)
	}
	if got, _ := testEnvValue(env, "AGENT_KEY"); got != "present" {
		t.Fatalf("AGENT_KEY = %q", got)
	}
	if got, _ := testEnvValue(env, "LANG"); got != "en_US.UTF-8" {
		t.Fatalf("LANG = %q", got)
	}
	if got, _ := testEnvValue(env, "TERM"); got != "xterm-256color" {
		t.Fatalf("TERM = %q", got)
	}
}

func TestSessionEnvironmentInheritsWithoutScrub(t *testing.T) {
	env := sessionEnvironment([]string{"DISPLAY=:0", "PATH=/bin"}, Spec{Home: "/h"}, "jail")
	if _, ok := testEnvValue(env, "DISPLAY"); !ok {
		t.Fatal("inherit mode must preserve the daemon environment")
	}
}

func TestCredentialHiddenPathsCoverSecrets(t *testing.T) {
	t.Setenv("HOME", "/tmp/df-test-home")
	paths := CredentialHiddenPaths()
	want := map[string]bool{
		"/tmp/df-test-home/.ssh":                  true,
		"/tmp/df-test-home/.aws":                  true,
		"/tmp/df-test-home/.config/opencode":      true,
		"/tmp/df-test-home/.local/share/keyrings": true,
		"/tmp/df-test-home/.config/google-chrome": true,
	}
	got := make(map[string]bool, len(paths))
	for _, path := range paths {
		got[path] = true
	}
	for path := range want {
		if !got[path] {
			t.Fatalf("CredentialHiddenPaths missing %s: %#v", path, paths)
		}
	}
}

func TestMergeUniquePathsDropsRootAndDuplicates(t *testing.T) {
	got := mergeUniquePaths([]string{"/", "/tmp/a", "/tmp/a", "relative", " /tmp/b "})
	want := []string{"/tmp/a", "/tmp/b"}
	if len(got) != len(want) {
		t.Fatalf("mergeUniquePaths = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeUniquePaths = %#v, want %#v", got, want)
		}
	}
}

func testEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}

func TestScrubEnvRequiresJail(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()
	_, err := mgr.Start(Spec{
		Dir:         t.TempDir(),
		Kind:        "shell",
		Command:     "/bin/sh",
		Args:        []string{"-c", "true"},
		Containment: "off",
		ScrubEnv:    true,
	})
	if err == nil {
		t.Fatal("environment scrubbing without jail must be refused")
	}
	if !strings.Contains(err.Error(), "jail") {
		t.Fatalf("error = %v, want a jail requirement", err)
	}
}

func TestJailSessionScrubsEnvironment(t *testing.T) {
	if !JailAvailable() {
		t.Skip("bwrap is not installed or cannot create a namespace")
	}
	t.Setenv("DISPLAY", ":99")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/df-agent.sock")
	t.Setenv("OPENAI_API_KEY", "sk-live-do-not-leak")
	home := filepath.Join(t.TempDir(), "home")
	mgr := NewManager(4096)
	defer mgr.CloseAll()
	sess, err := mgr.Start(Spec{
		Dir:         t.TempDir(),
		Kind:        "shell",
		Command:     "/bin/sh",
		Args:        []string{"-c", `printf 'display=%s\nssh=%s\nkey=%s\nhome=%s\n' "$DISPLAY" "$SSH_AUTH_SOCK" "$OPENAI_API_KEY" "$HOME"`},
		Containment: "jail",
		Home:        home,
		ScrubEnv:    true,
		EnvAllow:    []string{"SSH_AUTH_SOCK"},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("scrubbed jail session did not finish")
	}
	out := strings.ReplaceAll(string(sess.Snapshot()), "\r\n", "\n")
	if !strings.Contains(out, "display=\n") {
		t.Fatalf("DISPLAY leaked: %q", out)
	}
	if !strings.Contains(out, "ssh=/tmp/df-agent.sock\n") {
		t.Fatalf("allowed SSH_AUTH_SOCK missing: %q", out)
	}
	if !strings.Contains(out, "key=\n") || strings.Contains(out, "sk-live-do-not-leak") {
		t.Fatalf("OPENAI_API_KEY leaked: %q", out)
	}
	if !strings.Contains(out, "home="+home+"\n") {
		t.Fatalf("HOME was not projected: %q", out)
	}
}

func TestJailUsableRejectsBrokenBwrap(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if jailUsable(broken) {
		t.Fatal("a bwrap that cannot create a namespace must not be reported usable")
	}
}

func TestKickSendsWinch(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", `trap 'printf winch' WINCH; printf ready; while :; do sleep 1; done`},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(string(sess.Snapshot()), "ready") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := sess.Kick(); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(string(sess.Snapshot()), "winch") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := string(sess.Snapshot()); !strings.Contains(got, "winch") {
		t.Fatalf("Kick did not deliver SIGWINCH: %q", got)
	}
}

func TestSubscribeBrowserDetachesPrevious(t *testing.T) {
	mgr := NewManager(1024)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, first, cancelFirst := sess.SubscribeBrowser()
	defer cancelFirst()
	_, _, second, cancelSecond := sess.SubscribeBrowser()
	defer cancelSecond()

	select {
	case got, ok := <-first:
		if !ok {
			t.Fatal("first browser stream closed without a detached notice")
		}
		if !strings.Contains(string(got), "detached") {
			t.Fatalf("first browser stream got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first browser stream was not detached")
	}
	select {
	case _, ok := <-first:
		if ok {
			t.Fatal("first browser stream is still open")
		}
	case <-time.After(time.Second):
		t.Fatal("first browser stream did not close")
	}

	sess.broadcast([]byte("still-live"))
	select {
	case got := <-second:
		if string(got) != "still-live" {
			t.Fatalf("second browser stream got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second browser stream did not receive output")
	}
}

func TestReaperStopsSessionWhenDirectoryDisappears(t *testing.T) {
	mgr := NewManager(1024)
	mgr.StartReaper(20 * time.Millisecond)
	defer mgr.CloseAll()

	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.Start(Spec{
		Dir:     dir,
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sess.Status() == "exited" {
			if sess.Model().Reason != "worktree_missing" {
				t.Fatalf("reason = %q", sess.Model().Reason)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("reaper did not stop a session in a missing directory")
}
