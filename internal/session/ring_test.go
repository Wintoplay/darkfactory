package session

import "testing"

func TestRingKeepsNewestBytes(t *testing.T) {
	r := NewRing(5)
	r.Write([]byte("abc"))
	r.Write([]byte("def"))
	if got := string(r.Snapshot()); got != "bcdef" {
		t.Fatalf("snapshot = %q", got)
	}
	r.Write([]byte("0123456789"))
	if got := string(r.Snapshot()); got != "56789" {
		t.Fatalf("snapshot after overflow = %q", got)
	}
}
