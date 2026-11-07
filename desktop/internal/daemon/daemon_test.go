package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEnsureReachableAlreadyRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("probe path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// A working daemon must not shell out at all.
	t.Setenv("DARKFACTORY_BIN", filepath.Join(t.TempDir(), "missing"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := EnsureReachable(ctx, target); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureReachableStartsDaemon(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	port := freePort(t)
	dir := t.TempDir()
	server := filepath.Join(dir, "server.py")
	pidfile := filepath.Join(dir, "pid")
	if err := os.WriteFile(server, []byte(`import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200 if self.path == "/healthz" else 404)
        self.end_headers()
    def log_message(self, *args):
        pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
`), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "darkfactory")
	script := fmt.Sprintf(`#!/bin/sh
python3 %q "$DF_TEST_PORT" >/dev/null 2>&1 &
echo $! > "$DF_TEST_PIDFILE"
exit 0
`, server)
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DARKFACTORY_BIN", fake)
	t.Setenv("DF_TEST_PORT", strconv.Itoa(port))
	t.Setenv("DF_TEST_PIDFILE", pidfile)
	restoreTimeouts(t, 5*time.Second, 50*time.Millisecond)
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidfile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	})

	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := EnsureReachable(ctx, target); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureReachableFailsWhenDaemonDoesNotStart(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "darkfactory")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DARKFACTORY_BIN", fake)
	restoreTimeouts(t, 250*time.Millisecond, 25*time.Millisecond)
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := EnsureReachable(ctx, target); err == nil {
		t.Fatal("EnsureReachable accepted a daemon that never started")
	}
}

func restoreTimeouts(t *testing.T, start, poll time.Duration) {
	t.Helper()
	oldStart, oldPoll := startTimeout, pollInterval
	startTimeout, pollInterval = start, poll
	t.Cleanup(func() {
		startTimeout, pollInterval = oldStart, oldPoll
	})
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
