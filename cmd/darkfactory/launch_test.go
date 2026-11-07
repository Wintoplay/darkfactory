package main

import (
	"net/http"
	"net/http/httptest"
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
