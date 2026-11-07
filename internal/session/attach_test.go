package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// zshPrompt is what a fresh shell actually leaves in the ring: SGR colours, a
// CR + erase for the redraw, and no newline anywhere. It is the tail the Pixel
// emulator attached to as a black screen.
var zshPrompt = []byte("\r\x1b[K\r\x1b[A\x1b[K\r\x1b]0;user@workstation: /tmp\a" +
	"\x1b[01;32muser@workstation\x1b[00m:\x1b[01;34m/tmp\x1b[00m$ " +
	"\r\x1b[K\r\x1b[A\x1b[K\r\x1b]0;user@workstation: /tmp\a" +
	"\x1b[01;32muser@workstation\x1b[00m:\x1b[01;34m/tmp\x1b[00m$ ")

func TestBrowserSnapshotReplaysCompletePromptOnly(t *testing.T) {
	data, redraw := browserSnapshot(zshPrompt, true)
	if redraw {
		t.Fatal("a prompt that never ran a command should replay, not ask for a redraw")
	}
	if string(data) != string(zshPrompt) {
		t.Fatalf("snapshot = %q, want the whole prompt", data)
	}
}

func TestBrowserSnapshotTruncatedPromptRedraws(t *testing.T) {
	data, redraw := browserSnapshot(zshPrompt, false)
	if !redraw || len(data) != 0 {
		t.Fatalf("data=%q redraw=%v, want empty redraw", data, redraw)
	}
}

func TestBrowserSnapshotSuppressesPartialTUI(t *testing.T) {
	tui := []byte("\x1b[?25l\x1b[1;1H\x1b[38;2;238;238;238mhello")
	data, redraw := browserSnapshot(tui, true)
	if !redraw {
		t.Fatal("expected a redraw for a cursor-addressed tail")
	}
	if len(data) != 0 {
		t.Fatalf("snapshot = %q, want none", data)
	}
}

func TestBrowserSnapshotTrimsToNewline(t *testing.T) {
	data, redraw := browserSnapshot([]byte("2;40;40;40mjunk\nline one\nline two\n"), true)
	if redraw {
		t.Fatal("line-oriented output should replay")
	}
	if got, want := string(data), "line one\nline two\n"; got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

func TestBrowserSnapshotNoNewlineRedraws(t *testing.T) {
	data, redraw := browserSnapshot([]byte("\x1b[1;1Hstill painting"), true)
	if !redraw || len(data) != 0 {
		t.Fatalf("data=%q redraw=%v, want empty redraw", data, redraw)
	}
}

func TestBrowserSnapshotCapsTail(t *testing.T) {
	line := bytes.Repeat([]byte("x\n"), browserReplayLimit/2+64)
	data, redraw := browserSnapshot(line, true)
	if redraw {
		t.Fatal("line-oriented output should replay")
	}
	if len(data) > browserReplayLimit {
		t.Fatalf("snapshot length = %d, limit %d", len(data), browserReplayLimit)
	}
}

// TestBrowserAttachRestoresInputModes is the attach regression for C1. A real
// PTY enables mouse tracking, SGR encoding, bracketed paste and application
// cursor keys once, then repaints on SIGWINCH without repeating those emits.
// The browser attach suppresses the raw tail for such a session, so it has to
// carry the modes itself -- on the first attach and again after a reattach --
// while still never replaying screen bytes.
func TestBrowserAttachRestoresInputModes(t *testing.T) {
	mgr := NewManager(1 << 20)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args: []string{"-c",
			`printf '\033[?1000h\033[?1006h\033[?2004h\033[?1h'; ` +
				`printf '\033[?25l\033[2J\033[1;1Hmouse-tui ready\033[?25h'; ` +
				`trap 'printf "\033[?25l\033[2J\033[1;1Hrepaint\033[?25h"' WINCH; ` +
				`while :; do sleep 1; done`},
		Cols: 40,
		Rows: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor := func(want string) string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if snapshot := string(sess.Snapshot()); strings.Contains(snapshot, want) {
				return snapshot
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("session output never contained %q: %q", want, sess.Snapshot())
		return ""
	}

	if got := strings.Count(waitFor("mouse-tui ready"), "\x1b[?1000h"); got != 1 {
		t.Fatalf("mouse tracking was emitted %d times at startup, want once", got)
	}
	restores := []string{"\x1b[?1h", "\x1b[?1006h", "\x1b[?1000h", "\x1b[?2004h"}
	first, redraw, _, cancelFirst := sess.SubscribeBrowser()
	cancelFirst()
	if !redraw {
		t.Fatal("a cursor-addressed screen must ask for a redraw, not a replay")
	}
	if strings.Contains(string(first), "mouse-tui") {
		t.Fatalf("attach replayed the raw tail instead of only modes: %q", first)
	}
	for _, want := range restores {
		if !strings.Contains(string(first), want) {
			t.Fatalf("attach does not restore %q: %q", want, first)
		}
	}

	// The repaint deliberately omits the enables; the reattach still has to
	// restore them from tracked state.
	if err := sess.Kick(); err != nil {
		t.Fatal(err)
	}
	snapshot := waitFor("repaint")
	if got := strings.Count(snapshot, "\x1b[?1000h"); got != 1 {
		t.Fatalf("the SIGWINCH repaint re-emitted mouse tracking (%d total)", got)
	}
	second, redraw, _, cancelSecond := sess.SubscribeBrowser()
	cancelSecond()
	if !redraw {
		t.Fatal("reattach must ask for a redraw")
	}
	if string(second) != string(first) {
		t.Fatalf("reattach restore = %q, want the same modes %q", second, first)
	}
}

func TestRingReportsEviction(t *testing.T) {
	ring := NewRing(8)
	ring.Write([]byte("abc"))
	if _, complete := ring.SnapshotFromStart(); !complete {
		t.Fatal("a ring that never evicted should report a complete snapshot")
	}
	ring.Write([]byte("defghij"))
	if _, complete := ring.SnapshotFromStart(); complete {
		t.Fatal("an evicted ring should not claim to start at the first byte")
	}
}
