//go:build !windows

package session

import (
	"bytes"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
)

func TestHostSurvivesDaemonDetach(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	hostMgr := NewManager(1 << 20)
	server := &http.Server{Handler: NewHostHandler(hostMgr, nil)}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	defer hostMgr.CloseAllWithReason("test_done")

	client := NewHostClient(socket)
	daemonMgr := NewManager(1 << 20)
	if err := daemonMgr.UseHost(client); err != nil {
		t.Fatal(err)
	}
	sess, err := daemonMgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "printf 'host-marker'; sleep 5"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !bytes.Contains(sess.Snapshot(), []byte("host-marker")) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Contains(sess.Snapshot(), []byte("host-marker")) {
		if hostSess, ok := hostMgr.Get(sess.ID); ok {
			t.Logf("host status=%s snapshot=%q", hostSess.Status(), hostSess.Snapshot())
		} else {
			t.Logf("host session %s missing", sess.ID)
		}
		t.Logf("daemon status=%s snapshot=%q", sess.Status(), sess.Snapshot())
		t.Fatalf("snapshot = %q", sess.Snapshot())
	}

	// A daemon shutdown detaches; it must not stop the host's PTY.
	daemonMgr.CloseAll()
	hostSess, ok := hostMgr.Get(sess.ID)
	if !ok {
		t.Fatal("host session disappeared on daemon detach")
	}
	if hostSess.Status() != "running" {
		t.Fatalf("host status = %q", hostSess.Status())
	}

	// A new daemon reattaches to the same PTY and scrollback.
	next := NewManager(1 << 20)
	if err := next.UseHost(client); err != nil {
		t.Fatal(err)
	}
	reattached, ok := next.Get(sess.ID)
	if !ok {
		t.Fatal("new daemon did not reattach")
	}
	if reattached.Status() != "running" {
		t.Fatalf("reattached status = %q", reattached.Status())
	}
	if !bytes.Contains(reattached.Snapshot(), []byte("host-marker")) {
		t.Fatalf("reattached snapshot = %q", reattached.Snapshot())
	}
	if _, err := next.RemoveWithResult(sess.ID, "test_done"); err != nil {
		t.Fatal(err)
	}
	if _, ok := hostMgr.Get(sess.ID); ok {
		t.Fatal("host session still exists after stop")
	}
}

// TestHostDroppedMirrorIsNotAnExit covers the case where the daemon's mirror
// falls behind a fast PTY. The host drops that subscriber; the stream must end
// without an exit frame while the PTY still runs, or the daemon marks a live
// session exited and the pane freezes.
func TestHostDroppedMirrorIsNotAnExit(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	hostMgr := NewManager(1 << 20)
	server := &http.Server{Handler: NewHostHandler(hostMgr, nil)}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	defer hostMgr.CloseAllWithReason("test_done")

	client := NewHostClient(socket)
	sess, err := client.Start(t.Context(), Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args: []string{"-c",
			`sleep 1; i=0; while [ $i -lt 30000 ]; do printf "%s\n" "0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789"; i=$((i+1)); done; sleep 30`},
		Cols: 80,
		Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, stream, err := client.Attach(t.Context(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	// Do not read while the burst runs. The host drops a subscriber that falls
	// behind; this is a mirror drop, not a PTY exit.
	time.Sleep(1500 * time.Millisecond)
	sawExit := false
	for {
		kind, payload, err := readFrame(stream)
		if err != nil {
			break
		}
		if kind == frameExit {
			sawExit = true
			t.Logf("exit frame = %s", payload)
		}
	}
	if sawExit {
		t.Fatal("dropped mirror produced an exit frame for a running session")
	}
	if hostSess, ok := hostMgr.Get(sess.ID); !ok || hostSess.Status() != "running" {
		t.Fatalf("host session after dropped mirror = %v, %v", hostSess, ok)
	}
}

// TestDroppedMirrorReattaches proves the daemon recovers when the host drops
// its mirror stream without ending the PTY. The session must stay running and
// output written after the drop must reach the daemon's ring.
func TestDroppedMirrorReattaches(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	hostMgr := NewManager(1 << 20)
	server := &http.Server{Handler: NewHostHandler(hostMgr, nil)}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	defer hostMgr.CloseAllWithReason("test_done")

	client := NewHostClient(socket)
	daemonMgr := NewManager(1 << 20)
	defer daemonMgr.CloseAllWithReason("test_done")
	if err := daemonMgr.UseHost(client); err != nil {
		t.Fatal(err)
	}
	sess, err := daemonMgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", "cat"},
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	hostSess, ok := hostMgr.Get(sess.ID)
	if !ok {
		t.Fatal("host session missing")
	}
	if err := hostSess.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, sess, "before")

	// Close only the daemon's mirror, not the host PTY. This is what the host
	// does when the mirror falls far enough behind that its subscriber is
	// dropped.
	sess.remote.mu.Lock()
	stream := sess.remote.stream
	sess.remote.mu.Unlock()
	if stream == nil {
		t.Fatal("daemon has no mirror stream")
	}
	_ = stream.Close()

	if err := hostSess.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, sess, "after")
	if sess.Status() != model.SessionRunning {
		t.Fatalf("session status after mirror drop = %q", sess.Status())
	}
}

func waitForSnapshot(t *testing.T, sess *Session, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains(sess.Snapshot(), []byte(want)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("snapshot never contained %q: %q", want, sess.Snapshot())
}

// TestHostReattachRestoresEvictedModes is the host reattach regression: a TUI
// enables mouse tracking once at startup and then writes more than the whole
// ring. A restarted daemon only sees a ring tail without that enable, so the
// host has to carry its own tracked modes on attach. Seeding the daemon's
// tracker from the tail alone restored nothing and clicks became selections.
func TestHostReattachRestoresEvictedModes(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	hostMgr := NewManager(1 << 20)
	server := &http.Server{Handler: NewHostHandler(hostMgr, nil)}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	defer hostMgr.CloseAllWithReason("test_done")

	client := NewHostClient(socket)
	daemonMgr := NewManager(1 << 20)
	if err := daemonMgr.UseHost(client); err != nil {
		t.Fatal(err)
	}
	sess, err := daemonMgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args: []string{"-c",
			`printf "\033[?1000h\033[?1006h"; ` +
				`i=0; while [ $i -lt 20000 ]; do printf "%s\n" "0123456789012345678901234567890123456789012345678901234"; i=$((i+1)); done; ` +
				`printf "\033[?25l\033[1;1Hscreen\033[?25h"; sleep 30`},
		Cols: 80,
		Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	hostSess, ok := hostMgr.Get(sess.ID)
	if !ok {
		t.Fatal("host session missing")
	}

	// The 20000-line paint is just over the 1 MiB host ring, so the startup
	// enables are evicted before the final screen marker arrives.
	deadline := time.Now().Add(10 * time.Second)
	for !bytes.Contains(hostSess.Snapshot(), []byte("screen")) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := hostSess.Snapshot()
	if !bytes.Contains(snapshot, []byte("screen")) {
		t.Fatalf("host snapshot never reached the final paint (%d bytes)", len(snapshot))
	}
	if bytes.Contains(snapshot, []byte("\x1b[?1000h")) {
		t.Fatalf("test setup is wrong: the host ring still holds the startup enable (%d bytes)", len(snapshot))
	}

	// A daemon restart detaches but must leave the host PTY and its tracker.
	daemonMgr.CloseAll()
	if hostSess.Status() != "running" {
		t.Fatalf("host status after daemon detach = %q", hostSess.Status())
	}

	next := NewManager(1 << 20)
	if err := next.UseHost(client); err != nil {
		t.Fatal(err)
	}
	reattached, ok := next.Get(sess.ID)
	if !ok {
		t.Fatal("new daemon did not reattach")
	}
	replay, redraw, _, cancel := reattached.SubscribeBrowser()
	cancel()
	if !redraw {
		t.Fatal("reattach must ask for a redraw, not replay the raw tail")
	}
	if bytes.Contains(replay, []byte("screen")) {
		t.Fatalf("reattach replayed the raw tail: %q", replay)
	}
	for _, want := range []string{"\x1b[?1000h", "\x1b[?1006h"} {
		if !bytes.Contains(replay, []byte(want)) {
			t.Fatalf("reattach does not restore %q: %q", want, replay)
		}
	}

	if _, err := next.RemoveWithResult(sess.ID, "test_done"); err != nil {
		t.Fatal(err)
	}
	if _, ok := hostMgr.Get(sess.ID); ok {
		t.Fatal("host session still exists after stop")
	}
}
