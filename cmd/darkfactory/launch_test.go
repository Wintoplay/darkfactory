package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The desktop entry runs `darkfactory launch`, so the two facts it depends on
// are checked here: "is something ours listening" and "did it come up yet".

func TestDaemonHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("health check hit %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !daemonHealthy(srv.URL) {
		t.Fatal("a live daemon was not seen as healthy")
	}
	// The port is closed: nothing is listening, so launch must start one.
	if daemonHealthy("http://127.0.0.1:1") {
		t.Fatal("a dead port was reported healthy")
	}
}

func TestDaemonHealthyRejectsAnErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if daemonHealthy(srv.URL) {
		t.Fatal("a 503 was reported healthy")
	}
}

func TestWaitForDaemonGivesUpAndNamesTheLog(t *testing.T) {
	const logPath = "/somewhere/launch.log"
	start := time.Now()
	err := waitForDaemon("http://127.0.0.1:1", 300*time.Millisecond, logPath)
	if err == nil {
		t.Fatal("waitForDaemon returned successfully with nothing listening")
	}
	if !strings.Contains(err.Error(), logPath) {
		t.Fatalf("error does not point at the log: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("gave up after %s, before the timeout", elapsed)
	}
}

func TestWaitForDaemonSeesALateStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := waitForDaemon(srv.URL, 2*time.Second, "unused"); err != nil {
		t.Fatalf("a daemon that is already up was not accepted: %v", err)
	}
}

// `launch --desktop` may only hand off to a real program: a stray
// darkfactory-desktop file without an execute bit must not be run, and a
// package-only install has to fall back to the browser.
func TestExecutableFileNeedsARegularExecutable(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "darkfactory-desktop")
	if err := os.WriteFile(plain, []byte("not a program\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := executableFile(plain); ok {
		t.Fatalf("a non-executable file was accepted: %q", got)
	}
	if got, ok := executableFile(filepath.Join(dir, "missing")); ok {
		t.Fatalf("a missing file was accepted: %q", got)
	}
	prog := filepath.Join(dir, "runnable")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := executableFile(prog); !ok || got != prog {
		t.Fatalf("executableFile(%q) = %q, %v", prog, got, ok)
	}
}

func TestDesktopClientPrefersTheSiblingBinary(t *testing.T) {
	dir := t.TempDir()
	sibling := filepath.Join(dir, "darkfactory-desktop")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A different one on PATH must lose to the binary beside darkfactory.
	pathDir := t.TempDir()
	onPath := filepath.Join(pathDir, "darkfactory-desktop")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	got, ok := desktopClientFor(filepath.Join(dir, "darkfactory"))
	if !ok || got != sibling {
		t.Fatalf("desktopClientFor = %q, %v; want the sibling %q", got, ok, sibling)
	}

	// Without a sibling the PATH copy is the next choice.
	got, ok = desktopClientFor(filepath.Join(t.TempDir(), "darkfactory"))
	if !ok || got != onPath {
		t.Fatalf("desktopClientFor = %q, %v; want the PATH copy %q", got, ok, onPath)
	}

	// A non-executable sibling is skipped, not run.
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "darkfactory-desktop"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok = desktopClientFor(filepath.Join(broken, "darkfactory"))
	if !ok || got != onPath {
		t.Fatalf("desktopClientFor = %q, %v; want the PATH copy %q", got, ok, onPath)
	}

	t.Setenv("PATH", "")
	if got, ok := desktopClientFor(filepath.Join(broken, "darkfactory")); ok {
		t.Fatalf("a package-only install reported a desktop client: %q", got)
	}
}

func TestDesktopClientPassesTheLoopbackURLAndItsExitStatus(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	prog := filepath.Join(dir, "darkfactory-desktop")
	script := "#!/bin/sh\nprintf '%s' \"$*\" >" + out + "\nexit 7\n"
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runDesktopClient(prog, "http://127.0.0.1:8787")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("runDesktopClient error = %v, want exit status 7", err)
	}
	got, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "--url http://127.0.0.1:8787" {
		t.Fatalf("shell args = %q", got)
	}
}
