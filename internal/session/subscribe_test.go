//go:build !windows

package session

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSubscribeKeepsTheBytesAtTheSeam attaches while the child floods numbered
// tokens, then joins the snapshot with the chunk that follows it. The seam is
// where a subscriber used to lose output: the snapshot and the registration
// were separate steps, so a chunk written between them reached neither.
func TestSubscribeKeepsTheBytesAtTheSeam(t *testing.T) {
	mgr := NewManager(64 << 10)
	defer mgr.CloseAll()

	sess, err := mgr.Start(Spec{
		Dir:     t.TempDir(),
		Kind:    "shell",
		Command: "/bin/sh",
		Args:    []string{"-c", `i=0; while :; do i=$((i+1)); printf '%06d\n' "$i"; done`},
		Cols:    120,
		Rows:    32,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop("test_done")

	deadline := time.Now().Add(5 * time.Second)
	for len(sess.Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if len(sess.Snapshot()) == 0 {
		t.Fatal("the session produced no output")
	}

	for iteration := 0; iteration < 200; iteration++ {
		snapshot, chunks, cancel := sess.Subscribe()
		seam := append([]byte(nil), snapshot...)
		select {
		case chunk, ok := <-chunks:
			cancel()
			if !ok {
				t.Fatalf("iteration %d: the stream closed while the child was writing", iteration)
			}
			seam = append(seam, chunk...)
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatalf("iteration %d: no chunk arrived", iteration)
		}
		checkSeam(t, iteration, seam)
	}
}

// checkSeam requires the numbered tokens around the seam to be consecutive. A
// lost chunk leaves a jump, a duplicated chunk repeats a number, and a lost
// byte corrupts a token.
func checkSeam(t *testing.T, iteration int, data []byte) {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("iteration %d: the snapshot and the stream were both empty", iteration)
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	// The snapshot may start in the middle of a token; the stream may end in
	// one. Neither partial token says anything about the seam.
	lines = lines[1:]
	if !strings.HasSuffix(text, "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	previous, complete := -1, 0
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		value, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("iteration %d: token %q is corrupt, so bytes were lost or inserted", iteration, line)
		}
		if previous >= 0 && value != previous+1 {
			t.Fatalf("iteration %d: %d follows %d, so the seam lost or repeated output", iteration, value, previous)
		}
		previous = value
		complete++
	}
	if complete < 10 {
		t.Fatalf("iteration %d: only %d complete tokens around the seam", iteration, complete)
	}
}

// BenchmarkSubscribeAtFullRing measures the cost of attaching to a session
// whose scrollback ring is full: one copy of the ring plus a channel.
func BenchmarkSubscribeAtFullRing(b *testing.B) {
	sess := &Session{
		ring:   NewRing(4 << 20),
		subs:   make(map[chan []byte]struct{}),
		done:   make(chan struct{}),
		status: "running",
	}
	sess.ring.Write(make([]byte, 4<<20))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ch, cancel := sess.Subscribe()
		cancel()
		_ = ch
	}
}
