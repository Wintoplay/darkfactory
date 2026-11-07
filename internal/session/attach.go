package session

import "bytes"

// browserReplayLimit bounds the shell history replayed into a fresh xterm.
// The host ring is deliberately larger (default 4 MiB), but a browser attach
// only needs recent scrollback. Sending the whole ring makes a refresh slow and
// almost guarantees that the first byte is in the middle of an escape sequence.
const browserReplayLimit = 256 << 10

// browserSnapshot decides what a fresh browser terminal should receive.
//
// A raw PTY ring is not a screen snapshot. For a full-screen TUI (OpenCode,
// vim, htop, less) the tail is a partial repaint: it starts inside an SGR or
// cursor sequence and has no newlines to cut at. Replaying it produces
// gibberish. Those sessions get no history; the client asks the PTY for a
// SIGWINCH repaint instead. A shell gets a bounded tail trimmed to a newline,
// so recent scrollback survives without starting mid-sequence.
//
// complete reports that the snapshot still starts at the session's first byte,
// which is what makes a snapshot with no newline replayable at all.
func browserSnapshot(snapshot []byte, complete bool) ([]byte, bool) {
	if len(snapshot) == 0 {
		return nil, false
	}
	if looksLikeTUI(snapshot) {
		return nil, true
	}
	data := snapshot
	if len(data) > browserReplayLimit {
		data = data[len(data)-browserReplayLimit:]
		complete = false
	}
	// A newline is the only cheap, protocol-independent boundary that is safe
	// for ordinary line-oriented output. If there is none, this is cursor-
	// addressed output and a replay cannot be made coherent -- unless nothing
	// was evicted, in which case the buffer *is* the session from its first
	// byte. A shell that has not run anything yet prints a prompt and no
	// newline, and this used to swallow it: a fresh shell, and every tab switch
	// back to it, attached to a black screen until the user typed.
	cut := bytes.IndexByte(data, '\n')
	if cut < 0 {
		if complete && !hasCursorAddress(data) {
			return data, false
		}
		return nil, true
	}
	return data[cut+1:], false
}

// looksLikeTUI is a conservative tail heuristic. It is intentionally biased
// toward redraw: a wrong "TUI" answer costs scrollback, while a wrong "shell"
// answer shows garbage.
func looksLikeTUI(snapshot []byte) bool {
	tail := snapshot
	if len(tail) > 128<<10 {
		tail = tail[len(tail)-128<<10:]
	}
	if bytes.Contains(tail, []byte("\x1b[?1049h")) || bytes.Contains(tail, []byte("\x1b[?47h")) {
		return true
	}
	// Repainters hide the cursor while painting and show it when settled.
	if bytes.Contains(tail, []byte("\x1b[?25l")) && bytes.Contains(tail, []byte("\x1b[?25h")) {
		return true
	}
	escapes := bytes.Count(tail, []byte("\x1b["))
	lines := bytes.Count(tail, []byte{'\n'})
	// Escape-heavy and newline-free on its own is not a repaint: a colourised
	// prompt that zsh redraws on every resize looks exactly the same. A screen
	// painter has to place the cursor, so require a row/column address.
	if lines == 0 && escapes > 20 && hasCursorAddress(tail) {
		return true
	}
	return escapes > 500 && lines < 10
}

// hasCursorAddress reports a CUP/HVP sequence (\x1b[<row>;<col>H). SGR colours,
// line erases and the relative moves a prompt uses are deliberately not counted:
// those replay onto one line exactly as they would live.
func hasCursorAddress(b []byte) bool {
	for i := 0; i+2 < len(b); i++ {
		if b[i] != 0x1b || b[i+1] != '[' {
			continue
		}
		j := i + 2
		digits := 0
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			j++
			digits++
		}
		if digits == 0 || j >= len(b) || b[j] != ';' {
			continue
		}
		j++
		digits = 0
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			j++
			digits++
		}
		if digits > 0 && j < len(b) && (b[j] == 'H' || b[j] == 'f') {
			return true
		}
	}
	return false
}
