package nativeattach

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestFilterSplitControlStrings(t *testing.T) {
	in := "hello\x1b[31m\x1b[8;100;100t\x1b]0;spoof\a\x1b]8;;https://example.invalid\x1b\\link\x1b]8;;\x1b\\\x1b]52;c;SGk=\a\x1b]10;?\a\x1b]4;1;?\x1b\\\x1b]10;red\a\x1b_Gimage\x1b\\\x1bP$qm\x1b\\end"
	want := "hello\x1b[31mlink\x1b]52;c;SGk=\a\x1b]10;?\a\x1b]4;1;?\x1b\\\x1bP$qm\x1b\\end"
	for split := 1; split <= len(in); split++ {
		var out bytes.Buffer
		f := NewFilter(&out)
		for i := 0; i < len(in); i += split {
			end := min(i+split, len(in))
			if _, err := f.Write([]byte(in[i:end])); err != nil {
				t.Fatal(err)
			}
		}
		if out.String() != want {
			t.Fatalf("chunk %d: got %q", split, out.String())
		}
	}
}

func TestFilterBoundsUnterminatedControl(t *testing.T) {
	var out bytes.Buffer
	f := NewFilter(&out)
	_, _ = f.Write([]byte("start\x1b]0;" + strings.Repeat("x", maxControl*2)))
	_, _ = f.Write([]byte("\aend"))
	if out.String() != "startend" {
		t.Fatalf("got %q", out.String())
	}
}

// An unterminated string stays in its control state like a real terminal, so
// bytes after it are not printed. Both terminator forms must release the
// filter, and an oversized payload must not grow without bound.
func TestFilterBoundsEveryControlKind(t *testing.T) {
	for _, tc := range []struct {
		name, start, end string
	}{
		{"osc-bel", "\x1b]", "\a"},
		{"osc-st", "\x1b]", "\x1b\\"},
		{"dcs", "\x1bP", "\x1b\\"},
		{"apc", "\x1b_", "\x1b\\"},
		{"pm", "\x1b^", "\x1b\\"},
		{"sos", "\x1bX", "\x1b\\"},
	} {
		var out bytes.Buffer
		f := NewFilter(&out)
		if _, err := f.Write([]byte("a" + tc.start + strings.Repeat("x", maxControl*3))); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(tc.end + "b")); err != nil {
			t.Fatal(err)
		}
		if out.String() != "ab" {
			t.Fatalf("%s: got %q", tc.name, out.String())
		}
	}
}

// The filter is byte oriented. C1 introducers are only meaningful as raw
// bytes; in UTF-8 they also occur as continuation bytes (for example U+041B is
// D0 9B), so treating every 0x9B as CSI would corrupt text. Ghostty decodes
// UTF-8 and does not treat a bare 0x9B as CSI; the 2026-11-02 probe found raw
// and UTF-8 C1 OSC bytes did not change the default colour.
func TestFilterPreservesUTF8ContainingC1Bytes(t *testing.T) {
	for _, text := range []string{"Л", "œ", "日本語", "a\u009bb", "\u009d0;title\u009c"} {
		var out bytes.Buffer
		f := NewFilter(&out)
		if _, err := f.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if out.String() != text {
			t.Fatalf("got %q want %q", out.String(), text)
		}
	}
}

// Colour sets, resets, titles, links, images and window operations stay inert;
// only the queries a TUI needs and clipboard writes that Ghostty will ask the
// user to authorize reach the terminal. Malformed queries are inert too.
func TestFilterAllowsOnlyColourDECRQSSAndClipboardWrites(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		keep     bool
	}{
		{"osc10-query", "\x1b]10;?\a", true},
		{"osc11-query-st", "\x1b]11;?\x1b\\", true},
		{"osc12-query", "\x1b]12;?\a", true},
		{"osc4-query", "\x1b]4;1;?;2;?\x1b\\", true},
		{"osc4-mixed", "\x1b]4;1;?;rgb:00/00/00\a", false},
		{"osc4-odd", "\x1b]4;1;?;2\a", false},
		{"osc10-mixed", "\x1b]10;?;?\a", true},
		{"osc10-set", "\x1b]10;red\a", false},
		{"osc10-reset", "\x1b]110\a", false},
		{"osc104-reset", "\x1b]104\a", false},
		{"osc0-title", "\x1b]0;spoof\a", false},
		{"osc8-link", "\x1b]8;;https://example.invalid\x1b\\", false},
		{"osc52-write", "\x1b]52;c;SGk=\a", true},
		{"osc52-write-st", "\x1b]52;;SGk=\x1b\\", true},
		{"osc52-empty", "\x1b]52;c;\a", false},
		{"osc52-read", "\x1b]52;c;?\a", false},
		{"osc52-primary", "\x1b]52;p;SGk=\a", false},
		{"osc52-bad-base64", "\x1b]52;c;!!!!\a", false},
		{"osc52-short-base64", "\x1b]52;c;SGk\a", false},
		{"osc52-extra-fields", "\x1b]52;c;SGk=;extra\a", false},
		{"osc1337", "\x1b]1337;File=size=1\a", false},
		{"dcs-decrqss", "\x1bP$qm\x1b\\", true},
		{"dcs-sixel", "\x1bPq#0;2;0;0;0\x1b\\", false},
		{"dcs-unknown", "\x1bPzzz\x1b\\", false},
		{"apc-kitty", "\x1b_Gf=100;AAAA\x1b\\", false},
		{"csi-window", "\x1b[8;100;100t", false},
		{"csi-window-private", "\x1b[?22;0t", false},
		{"csi-sgr", "\x1b[31m", true},
		{"csi-private-mode", "\x1b[?1049h", true},
		{"csi-device-attributes", "\x1b[c", true},
	} {
		var out bytes.Buffer
		f := NewFilter(&out)
		if _, err := f.Write([]byte(tc.in)); err != nil {
			t.Fatal(err)
		}
		want := ""
		if tc.keep {
			want = tc.in
		}
		if out.String() != want {
			t.Fatalf("%s: got %q want %q", tc.name, out.String(), want)
		}
	}
}

// A clipboard write may exceed the small control-string bound, but it is still
// bounded, and a payload past the bound is dropped whole rather than truncated.
func TestFilterBoundsClipboardWrites(t *testing.T) {
	small := "\x1b]52;c;" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), 300<<10)) + "\a"
	var out bytes.Buffer
	f := NewFilter(&out)
	if _, err := f.Write([]byte(small + "after")); err != nil {
		t.Fatal(err)
	}
	if out.String() != small+"after" {
		t.Fatalf("300 KiB clipboard write was not preserved (%d bytes)", out.Len())
	}

	large := "\x1b]52;c;" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), 900<<10)) + "\a"
	var outLarge bytes.Buffer
	f = NewFilter(&outLarge)
	if _, err := f.Write([]byte(large + "after")); err != nil {
		t.Fatal(err)
	}
	if outLarge.String() != "after" {
		t.Fatalf("oversized clipboard write leaked %d bytes", outLarge.Len())
	}
}
