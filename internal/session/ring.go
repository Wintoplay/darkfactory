package session

import "sync"

type Ring struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func NewRing(max int) *Ring {
	if max <= 0 {
		max = 4 << 20
	}
	return &Ring{max: max}
}

func (r *Ring) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) >= r.max {
		r.buf = append(r.buf[:0], p[len(p)-r.max:]...)
		r.truncated = true
		return
	}
	if overflow := len(r.buf) + len(p) - r.max; overflow > 0 {
		copy(r.buf, r.buf[overflow:])
		r.buf = r.buf[:len(r.buf)-overflow]
		r.truncated = true
	}
	r.buf = append(r.buf, p...)
}

// Reset replaces the buffer with a fresh replay after a mirror reattaches.
func (r *Ring) Reset(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf[:0], p...)
	r.truncated = false
}

func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// SnapshotFromStart returns the buffered bytes and reports whether they still
// begin at the session's first byte. Eviction is what makes a replay start
// mid-sequence, so a caller that wants to replay a snapshot with no newline in
// it has to know.
func (r *Ring) SnapshotFromStart() ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out, !r.truncated
}

func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}
