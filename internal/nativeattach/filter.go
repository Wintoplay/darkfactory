package nativeattach

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
)

// Filter contains output-driven terminal features which must not control the
// desktop. It buffers a control string across reads and caps its size.
type Filter struct {
	out     io.Writer
	state   byte
	kind    byte
	seq     []byte
	discard bool
}

const (
	maxControl = 4096
	// A clipboard write carries a base64 payload, so it needs a larger bound
	// than the other control strings. Ghostty still asks the user to authorize
	// the write and shows the decoded content.
	maxClipboard = 1 << 20
)

func NewFilter(out io.Writer) *Filter { return &Filter{out: out} }

func (f *Filter) Write(p []byte) (int, error) {
	var out bytes.Buffer
	for _, b := range p {
		switch f.state {
		case 0:
			if b == 0x1b {
				f.state = 1
			} else {
				out.WriteByte(b)
			}
		case 1:
			switch b {
			case ']', 'P', '_', '^', 'X':
				f.kind, f.state, f.discard = b, 2, false
				f.seq = append(f.seq[:0], 0x1b, b)
			case '[':
				f.kind, f.state, f.discard = b, 4, false
				f.seq = append(f.seq[:0], 0x1b, b)
			default:
				out.WriteByte(0x1b)
				out.WriteByte(b)
				f.state = 0
			}
		case 2:
			f.add(b)
			if b == 0x07 && f.kind == ']' {
				f.finish(&out)
			} else if b == 0x1b {
				f.state = 3
			}
		case 3:
			f.add(b)
			if b == '\\' {
				f.finish(&out)
			} else if b != 0x1b {
				f.state = 2
			}
		case 4:
			if b == 0x1b {
				f.seq = f.seq[:0]
				f.state = 1
				break
			}
			f.add(b)
			if b >= 0x40 && b <= 0x7e {
				if !f.discard && b != 't' {
					out.Write(f.seq)
				}
				f.seq = f.seq[:0]
				f.state, f.discard = 0, false
			}
		}
	}
	if out.Len() > 0 {
		if _, err := io.Copy(f.out, &out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (f *Filter) add(b byte) {
	if f.discard {
		return
	}
	limit := maxControl
	if f.kind == ']' && bytes.HasPrefix(f.seq, []byte("\x1b]52;")) {
		limit = maxClipboard
	}
	if len(f.seq) >= limit {
		f.discard = true
		f.seq = f.seq[:0]
		return
	}
	f.seq = append(f.seq, b)
}

func (f *Filter) finish(out *bytes.Buffer) {
	if !f.discard && f.allowed() {
		out.Write(f.seq)
	}
	f.seq = f.seq[:0]
	f.state = 0
	f.discard = false
}

// Allow colour queries and DECRQSS; the terminal handles their replies. All
// other OSC and device-control strings are inert, including images and links.
// OSC 52 writes are forwarded only when Ghostty will ask the user to authorize
// them; reads (an empty or `?` payload) stay inert, as do PRIMARY/cut-buffer
// targets.
func (f *Filter) allowed() bool {
	if f.kind == 'P' {
		return bytes.HasPrefix(f.seq, []byte("\x1bP$q"))
	}
	if f.kind != ']' {
		return false
	}
	payload := string(f.seq[2:])
	payload = strings.TrimSuffix(payload, "\a")
	payload = strings.TrimSuffix(payload, "\x1b\\")
	parts := strings.Split(payload, ";")
	if len(parts) < 2 {
		return false
	}
	switch parts[0] {
	case "10", "11", "12":
		for _, spec := range parts[1:] {
			if spec != "?" {
				return false
			}
		}
		return true
	case "4":
		if len(parts) < 3 || len(parts)%2 == 0 {
			return false
		}
		for i := 2; i < len(parts); i += 2 {
			if parts[i] != "?" {
				return false
			}
		}
		return true
	case "52":
		if len(parts) != 3 || (parts[1] != "" && parts[1] != "c") {
			return false
		}
		if parts[2] == "" || parts[2] == "?" || len(parts[2])%4 != 0 {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(parts[2])
		return err == nil
	}
	return false
}
