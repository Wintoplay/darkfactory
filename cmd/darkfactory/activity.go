package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// activityTracker is the on-demand listener's idle clock. A request in flight,
// including a terminal stream, keeps the daemon up; once the last handler
// returns the idle window starts again. It deliberately counts any request,
// not only authenticated ones: a closed tab must not keep a listener alive.
type activityTracker struct {
	active atomic.Int64
	last   atomic.Int64
}

func newActivityTracker() *activityTracker {
	tracker := &activityTracker{}
	tracker.touch()
	return tracker
}

func (a *activityTracker) touch() {
	a.last.Store(time.Now().UnixNano())
}

func (a *activityTracker) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.active.Add(1)
		a.touch()
		defer func() {
			a.active.Add(-1)
			a.touch()
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *activityTracker) idleFor(window time.Duration) bool {
	if window <= 0 || a.active.Load() > 0 {
		return false
	}
	last := a.last.Load()
	return last > 0 && time.Since(time.Unix(0, last)) >= window
}

// waitIdle closes the returned channel after the listener has been idle for
// window. A nil return means the feature is disabled. The goroutine exits when
// ctx is cancelled, so `serve` does not leak it during shutdown.
func (a *activityTracker) waitIdle(ctx context.Context, window time.Duration) <-chan struct{} {
	if window <= 0 {
		return nil
	}
	poll := window / 4
	if poll < 50*time.Millisecond {
		poll = 50 * time.Millisecond
	}
	if poll > time.Second {
		poll = time.Second
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.idleFor(window) {
					close(done)
					return
				}
			}
		}
	}()
	return done
}
