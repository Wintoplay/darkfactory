package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestActivityTrackerKeepsAnActiveRequestAlive(t *testing.T) {
	tracker := newActivityTracker()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := tracker.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(done)
	}()
	<-started
	if tracker.idleFor(10 * time.Millisecond) {
		t.Fatal("an in-flight request was reported idle")
	}
	close(release)
	<-done
	time.Sleep(20 * time.Millisecond)
	if !tracker.idleFor(10 * time.Millisecond) {
		t.Fatal("the tracker did not become idle after the request returned")
	}
}

func TestActivityTrackerIdleWaitAndCancel(t *testing.T) {
	tracker := newActivityTracker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := tracker.waitIdle(ctx, 30*time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle wait never fired")
	}

	cancelled, stop := context.WithCancel(context.Background())
	stopped := tracker.waitIdle(cancelled, time.Hour)
	stop()
	select {
	case <-stopped:
		// The returned channel is only closed on idle, not on cancellation.
		t.Fatal("cancelled wait reported idle")
	case <-time.After(20 * time.Millisecond):
	}
}
