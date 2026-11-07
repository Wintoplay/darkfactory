//go:build linux

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestNativeTerminalCredentialUsesPrivateDescriptor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DF_NATIVE_TEST_DIR", dir)
	fake := filepath.Join(dir, "ghostty")
	script := "#!/bin/sh\n/bin/cat <&3 > \"$DF_NATIVE_TEST_DIR/credential\"\nprintf '%s\\n' \"$@\" > \"$DF_NATIVE_TEST_DIR/args\"\nprintf '\\001' >&4\n/bin/sleep 0.1\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "test-private-credential"
	if err := startNativeTerminal(context.Background(), fake, "127.0.0.1:8787", strings.Repeat("a", 32), secret); err != nil {
		t.Fatal(err)
	}
	var args []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		args, err = os.ReadFile(filepath.Join(dir, "args"))
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(args) == 0 {
		t.Fatal("native process did not receive its arguments")
	}
	got, err := os.ReadFile(filepath.Join(dir, "credential"))
	if err != nil || string(got) != secret {
		t.Fatal("native process did not receive caller credential on fd 3")
	}
	if strings.Contains(string(args), secret) {
		t.Fatal("credential appeared in arguments")
	}
	for _, required := range []string{"--clipboard-read=deny", "--clipboard-write=ask", "--link-url=false", "--image-storage-limit=0", "--right-click-action=context-menu", "--mouse-shift-capture=never", "--credential-fd"} {
		if !strings.Contains(string(args), required) {
			t.Fatalf("missing %s", required)
		}
	}
}

func TestNativeTerminalRejectsForwardedRequest(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/missing/native", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-DF-Request", "1")
	req.Header.Set("X-Forwarded-For", "192.0.2.10")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}

func TestNativeTerminalRecorderFailurePreventsLaunch(t *testing.T) {
	srv, _ := newRefusalServer(t, func(s *Server) { s.gateway.recorder = &testRecorder{err: errors.New("record failure")} })
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/missing/native", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", resp.StatusCode)
	}
}

func TestSupportedGhosttyVersion(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ghostty")
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"Ghostty 1.3.1\n", true},
		{"Ghostty 1.3.0\n", false},
		{"Ghostty 1.4.0\n", false},
		{"other 1.3.1\n", false},
	} {
		if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s' '"+tc.output+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := supportedGhostty(context.Background(), fake); got != tc.want {
			t.Fatalf("version %q: got %v", tc.output, got)
		}
	}
}

func TestProbeNativeTerminal(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ghostty")
	write := func(output string) {
		t.Helper()
		if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":99")
	t.Setenv("PATH", dir)

	write("Ghostty 1.3.1\n")
	if got := probeNativeTerminal(); !got.Available || got.Version != "1.3.1" {
		t.Fatalf("installed 1.3.1 = %#v, want available 1.3.1", got)
	}

	write("Ghostty 1.4.0\n")
	if got := probeNativeTerminal(); got.Available || got.Reason != "unsupported_version" {
		t.Fatalf("installed 1.4.0 = %#v, want unsupported_version", got)
	}

	t.Setenv("PATH", t.TempDir())
	if got := probeNativeTerminal(); got.Available || got.Reason != "not_installed" {
		t.Fatalf("missing binary = %#v, want not_installed", got)
	}

	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if got := probeNativeTerminal(); got.Available || got.Reason != "no_graphical_session" {
		t.Fatalf("headless host = %#v, want no_graphical_session", got)
	}
}

func TestStateReportsNativeTerminalCapability(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ghostty")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'Ghostty 1.3.1\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":99")

	srv, _ := newRefusalServer(t, nil)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d", resp.StatusCode)
	}
	var body stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.NativeTerminal.Available || body.NativeTerminal.Version != "1.3.1" {
		t.Fatalf("nativeTerminal = %#v, want available 1.3.1", body.NativeTerminal)
	}
}

func TestNativeLaunchKillsChildOnReadinessFailure(t *testing.T) {
	nativeReadyTimeout = 200 * time.Millisecond
	defer func() { nativeReadyTimeout = 10 * time.Second }()
	fake, pidFile := fakeNativeChild(t, "exec /bin/sleep 30\n")
	err := startNativeTerminal(context.Background(), fake, "127.0.0.1:8787", strings.Repeat("a", 32), "test-private-credential")
	if err == nil {
		t.Fatal("a child that never reports readiness was accepted")
	}
	waitForExit(t, readPid(t, pidFile))
}

func TestNativeLaunchCancellationKillsChild(t *testing.T) {
	fake, pidFile := fakeNativeChild(t, "exec /bin/sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	err := startNativeTerminal(ctx, fake, "127.0.0.1:8787", strings.Repeat("a", 32), "test-private-credential")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	waitForExit(t, readPid(t, pidFile))
}

// fakeNativeChild writes a fake Ghostty that records its pid and then runs the
// supplied shell body. The body must not write to fd 4 unless the test wants a
// successful readiness report.
func fakeNativeChild(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DF_NATIVE_TEST_DIR", dir)
	fake := filepath.Join(dir, "ghostty")
	script := "#!/bin/sh\nprintf '%s' $$ > \"$DF_NATIVE_TEST_DIR/pid\"\n" + body
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return fake, filepath.Join(dir, "pid")
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 500; i++ {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr != nil {
				t.Fatal(convErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake native child did not start")
	return 0
}

func waitForExit(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 500; i++ {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("native child %d survived", pid)
}

func TestNativeLaunchIntentIsIdempotent(t *testing.T) {
	s := &Server{}
	key := nativeRequestKey{principal: principal{Kind: principalHuman, ID: "device:one"}, session: "session", request: "one"}
	var calls atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := s.nativeOnce(context.Background(), key, func() error {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return nil
			}); err != nil {
				t.Errorf("launch: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("launched %d times, want 1", got)
	}
}

func TestFailedNativeLaunchCanRetry(t *testing.T) {
	s := &Server{}
	key := nativeRequestKey{principal: principal{Kind: principalHuman, ID: "token"}, session: "session", request: "one"}
	if err := s.nativeOnce(context.Background(), key, func() error { return errors.New("failed") }); err == nil {
		t.Fatal("failure was ignored")
	}
	called := false
	if err := s.nativeOnce(context.Background(), key, func() error { called = true; return nil }); err != nil || !called {
		t.Fatal("failed launch was not retried")
	}
}
