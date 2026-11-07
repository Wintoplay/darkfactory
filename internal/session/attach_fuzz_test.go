package session

import (
	"bytes"
	"testing"
)

// FuzzBrowserSnapshot pins the attach policy against arbitrary ring bytes.
//
// The heuristic is a guess, so the properties are structural, not semantic:
// whatever it decides, the bytes handed to a fresh terminal must be bytes the
// session actually wrote, in order, starting where the client can render them,
// and never larger than the replay budget. The three bugs behind this policy
// (a black pane on a fresh shell, a colourised prompt read as a repaint, a
// replay starting inside an escape sequence) were all found by hand. This is
// the net for the next one.
//
// Seeds run as ordinary tests in `make test`; `make fuzz` explores past them.
func FuzzBrowserSnapshot(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte("\n"),
		[]byte("hello"),
		zshPrompt,
		[]byte("\x1b[?25l\x1b[1;1H\x1b[38;2;238;238;238mhello"),
		[]byte("2;40;40;40mjunk\nline one\nline two\n"),
		[]byte("\x1b[1;1Hstill painting"),
		bytes.Repeat([]byte("x\n"), 64),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, snapshot []byte) {
		// complete is the caller's "the ring never evicted" bit, which is what
		// makes a newline-free snapshot coherent at all.
		for _, complete := range []bool{true, false} {
			data, redraw := browserSnapshot(snapshot, complete)

			if len(data) > browserReplayLimit {
				t.Fatalf("replay %d bytes, limit %d", len(data), browserReplayLimit)
			}
			// History beside a redraw is the bug the policy exists to prevent.
			if redraw && len(data) != 0 {
				t.Fatalf("redraw with %d bytes of history", len(data))
			}
			if len(data) == 0 {
				continue
			}
			// A replay is a contiguous tail of the ring, never synthesised bytes.
			if !bytes.HasSuffix(snapshot, data) {
				t.Fatalf("replay %q is not a suffix of the ring", data)
			}
			// The whole buffer is coherent by construction (nothing evicted).
			if bytes.Equal(data, snapshot) {
				continue
			}
			// Otherwise it must start on a line boundary, or the client paints
			// the second half of an escape sequence.
			if start := len(snapshot) - len(data); snapshot[start-1] != '\n' {
				t.Fatalf("replay starts at byte %d, inside a line", start)
			}
		}
	})
}
