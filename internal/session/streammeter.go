package session

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// StreamMeter aggregates per-connection terminal stream traffic for the
// terminal-lag investigation. It is a development aid: NewStreamMeter returns
// nil unless DF_STREAM_DEBUG is set, and a nil meter short-circuits to no-ops,
// so the hot path is unchanged by default. One meter per client stream logs a
// two-second rate line and a lifetime total when the stream context ends.
type StreamMeter struct {
	session string
	kind    string
	start   time.Time
	bytes   atomic.Int64
	writes  atomic.Int64
	once    sync.Once
}

// NewStreamMeter returns a meter for one connection, or nil when
// DF_STREAM_DEBUG is unset.
func NewStreamMeter(session, kind string) *StreamMeter {
	if os.Getenv("DF_STREAM_DEBUG") == "" {
		return nil
	}
	return &StreamMeter{session: session, kind: kind, start: time.Now()}
}

// Record counts one flush of n bytes.
func (m *StreamMeter) Record(n int) {
	if m == nil {
		return
	}
	m.bytes.Add(int64(n))
	m.writes.Add(1)
}

// Totals returns the lifetime counters, zero for a nil meter.
func (m *StreamMeter) Totals() (int64, time.Duration) {
	if m == nil {
		return 0, 0
	}
	return m.bytes.Load(), time.Since(m.start)
}

// Run logs the rate every two seconds and the lifetime total at exit. It takes
// the request context, so a stream that outlives its log target is safe.
func (m *StreamMeter) Run(ctx context.Context, log *slog.Logger) {
	if m == nil {
		return
	}
	m.once.Do(func() {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			var lastBytes, lastWrites int64
			for {
				select {
				case <-ctx.Done():
					log.Info("stream_end", "kind", m.kind, "session", m.session,
						"bytes", m.bytes.Load(), "writes", m.writes.Load(),
						"ms", time.Since(m.start).Milliseconds())
					return
				case <-ticker.C:
					b, w := m.bytes.Load(), m.writes.Load()
					log.Info("stream_rate", "kind", m.kind, "session", m.session,
						"kib_per_s", (b-lastBytes)/2/1024,
						"writes_per_2s", w-lastWrites,
						"bytes_per_2s", b-lastBytes)
					lastBytes, lastWrites = b, w
				}
			}
		}()
	})
}
