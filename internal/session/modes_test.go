package session

import (
	"bytes"
	"testing"
)

func restore(t *testing.T, writes ...string) string {
	t.Helper()
	var modes attachModes
	for _, w := range writes {
		modes.Write([]byte(w))
	}
	return string(modes.Restore())
}

func TestRestoreInputModes(t *testing.T) {
	got := restore(t,
		"\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[?1h\x1b[?1004h",
		"paint \x1b[2J",
	)
	want := "\x1b[?1h\x1b[?1006h\x1b[?1000h\x1b[?1004h\x1b[?2004h"
	if got != want {
		t.Fatalf("restore = %q, want %q", got, want)
	}
}

// A TUI that turns tracking on and then off again must not be re-enabled: the
// client would send clicks to an app that stopped listening for them.
func TestRestoreFollowsTheLastDecision(t *testing.T) {
	if got := restore(t, "\x1b[?1000h\x1b[?1006h\x1b[?1000l"); got != "\x1b[?1006h" {
		t.Fatalf("restore after tracking off = %q", got)
	}
	// Changing the protocol replaces the previous one instead of enabling both.
	if got := restore(t, "\x1b[?1002h\x1b[?1006h\x1b[?1015h"); got != "\x1b[?1015h\x1b[?1002h" {
		t.Fatalf("restore after encoding change = %q", got)
	}
}

// Enables arrive in grouped parameters and can straddle read boundaries.
func TestModeTrackingSurvivesSplitAndGroupedSequences(t *testing.T) {
	got := restore(t,
		"\x1b[?1000;10",
		"06;2004h",
		"\x1b[?1",
		"h",
	)
	want := "\x1b[?1h\x1b[?1006h\x1b[?1000h\x1b[?2004h"
	if got != want {
		t.Fatalf("restore = %q, want %q", got, want)
	}
}

// Output-mode sequences and plain text must not leak into the restore.
func TestModeTrackingIgnoresOutputModes(t *testing.T) {
	got := restore(t,
		"\x1b[?25l\x1b[?1049h\x1b[?7l\x1b[0m\x1b[2J\x1b[1;1Hhello\x1b[?25h",
		"\x1b]0;title\x07",
	)
	if got != "" {
		t.Fatalf("restore = %q, want none", got)
	}
}

func TestModeTrackingIgnoresControlStrings(t *testing.T) {
	var modes attachModes
	// A title or device reply can contain a byte run that looks like an enable.
	modes.Write([]byte("\x1b]0;build \x1b[?1000h done\x07"))
	modes.Write([]byte("\x1bP1+q\x1b[?2004h\x1b\\"))
	if got := modes.Restore(); len(got) != 0 {
		t.Fatalf("restore = %q, want none", got)
	}
	// A string split across reads is still skipped, and a real enable after it
	// is still seen.
	modes.Write([]byte("\x1b]8;;http://example.invalid"))
	modes.Write([]byte("\x1b\\\x1b[?2004h"))
	if got := string(modes.Restore()); got != "\x1b[?2004h" {
		t.Fatalf("restore = %q", got)
	}
}

func TestModeRestoreIsEmptyWithoutObservation(t *testing.T) {
	var modes attachModes
	if got := modes.Restore(); len(got) != 0 {
		t.Fatalf("restore = %q, want none", got)
	}
}

func TestModeCarryIsBounded(t *testing.T) {
	var modes attachModes
	modes.Write([]byte("\x1b[" + string(bytes.Repeat([]byte("9"), maxModeCarry+1))))
	if len(modes.carry) != 0 {
		t.Fatalf("carry = %d bytes, want dropped", len(modes.carry))
	}
	// The tracker still accepts a normal sequence afterwards.
	modes.Write([]byte("\x1b[?2004h"))
	if got := string(modes.Restore()); got != "\x1b[?2004h" {
		t.Fatalf("restore = %q", got)
	}
}
