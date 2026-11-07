package session

import (
	"strconv"
	"sync"
)

// attachModes is the part of a full-screen app's terminal state that decides
// how the browser *encodes user input*. A fresh xterm starts with every one of
// these modes off, and a TUI that enabled mouse tracking at startup does not
// repeat the enable when it repaints on SIGWINCH. Suppressing the ring tail for
// a TUI therefore used to leave clicks and pastes unencodable until the app
// happened to restart: the screen repainted, the pointer did nothing.
//
// The tracker follows the PTY output, not the ring, so it survives ring
// eviction, and it remembers the last decision per mode rather than every byte.
// Only modes whose state changes the bytes xterm sends are tracked; output
// modes such as cursor visibility and the alternate screen are left alone
// because replaying a guessed value would corrupt the paint.
type attachModes struct {
	mu sync.Mutex

	appCursor      bool // DECCKM (?1): arrows become SS3 sequences
	bracketedPaste bool // ?2004
	focusEvents    bool // ?1004: report focus in/out
	mouseTracking  int  // last of ?1000/1002/1003 turned on, 0 when off
	mouseEncoding  int  // last of ?1005/1006/1015 turned on, 0 when off
	carry          []byte
}

// maxModeCarry bounds the incomplete escape sequence kept between reads. A
// private-mode sequence is far shorter; anything longer is not one.
const maxModeCarry = 64

// Write consumes PTY output. A sequence split across reads is completed from
// the carry, because a mode enable that happens to straddle a read boundary
// still has to be remembered.
func (m *attachModes) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	buf := p
	if len(m.carry) > 0 {
		buf = make([]byte, 0, len(m.carry)+len(p))
		buf = append(buf, m.carry...)
		buf = append(buf, p...)
		m.carry = nil
	}
	i := 0
	for i < len(buf) {
		if buf[i] != 0x1b {
			i++
			continue
		}
		if i+1 >= len(buf) {
			m.keep(buf[i:])
			return
		}
		if isControlString(buf[i+1]) {
			// OSC/DCS/APC/PM carry arbitrary text (a window title, a device
			// reply). A mode-looking byte run inside one is not a mode.
			end, ok := controlStringEnd(buf, i+2)
			if !ok {
				m.keep(buf[i:])
				return
			}
			i = end
			continue
		}
		if buf[i+1] != '[' {
			// Other two-byte escapes: not a private mode.
			i += 2
			continue
		}
		j := i + 2
		for j < len(buf) && !isCSIFinal(buf[j]) {
			j++
		}
		if j >= len(buf) {
			m.keep(buf[i:])
			return
		}
		m.apply(buf[i+2:j], buf[j])
		i = j + 1
	}
}

func (m *attachModes) keep(tail []byte) {
	if len(tail) > maxModeCarry {
		m.carry = nil
		return
	}
	m.carry = append(m.carry[:0], tail...)
}

// isCSIFinal matches the ECMA-48 final-byte range. '[' itself is excluded by
// starting the scan after it.
func isCSIFinal(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

func isControlString(b byte) bool {
	// OSC ']', DCS 'P', SOS 'X', PM '^', APC '_'.
	return b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_'
}

// controlStringEnd returns the index just past the string terminator (BEL or
// ST), or ok=false when the terminator has not arrived in this chunk.
func controlStringEnd(buf []byte, start int) (int, bool) {
	for j := start; j < len(buf); j++ {
		if buf[j] == 0x07 {
			return j + 1, true
		}
		if buf[j] == 0x1b && j+1 < len(buf) && buf[j+1] == '\\' {
			return j + 2, true
		}
	}
	return 0, false
}

// apply records one DEC private mode set/reset. Grouped parameters
// ("\x1b[?1000;1006h") are applied one by one, which is how xterm reports a
// TUI enabling tracking and SGR encoding in one write.
func (m *attachModes) apply(params []byte, final byte) {
	if final != 'h' && final != 'l' {
		return
	}
	if len(params) == 0 || params[0] != '?' {
		return
	}
	on := final == 'h'
	for _, field := range splitParams(params[1:]) {
		value, err := strconv.Atoi(string(field))
		if err != nil {
			continue
		}
		switch value {
		case 1:
			m.appCursor = on
		case 1000, 1002, 1003:
			if on {
				m.mouseTracking = value
			} else if m.mouseTracking == value {
				m.mouseTracking = 0
			}
		case 1005, 1006, 1015:
			if on {
				m.mouseEncoding = value
			} else if m.mouseEncoding == value {
				m.mouseEncoding = 0
			}
		case 1004:
			m.focusEvents = on
		case 2004:
			m.bracketedPaste = on
		}
	}
}

func splitParams(b []byte) [][]byte {
	out := make([][]byte, 0, 4)
	start := 0
	for i := 0; i <= len(b); i++ {
		if i == len(b) || b[i] == ';' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return out
}

// Restore is the sequence that puts a fresh xterm back into the modes the app
// believes it is in. It is empty when nothing has been observed, which is the
// honest answer for a session whose startup scrolled out before the tracker
// could see it.
func (m *attachModes) Restore() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []byte
	if m.appCursor {
		out = append(out, "\x1b[?1h"...)
	}
	// Encoding first: the tracking protocol decides whether reports are sent,
	// the encoding decides their shape, and setting both in either order works.
	if m.mouseEncoding != 0 {
		out = append(out, "\x1b[?"...)
		out = strconv.AppendInt(out, int64(m.mouseEncoding), 10)
		out = append(out, 'h')
	}
	if m.mouseTracking != 0 {
		out = append(out, "\x1b[?"...)
		out = strconv.AppendInt(out, int64(m.mouseTracking), 10)
		out = append(out, 'h')
	}
	if m.focusEvents {
		out = append(out, "\x1b[?1004h"...)
	}
	if m.bracketedPaste {
		out = append(out, "\x1b[?2004h"...)
	}
	return out
}
