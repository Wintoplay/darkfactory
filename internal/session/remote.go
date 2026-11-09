//go:build !windows

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
)

// remoteLink is the daemon-side handle to a PTY owned by the session host.
// The host keeps the master fd and the ring; the daemon only mirrors frames
// into a local ring so the existing HTTP stream and xterm path stay unchanged.
type remoteLink struct {
	client *HostClient
	id     string

	mu       sync.Mutex
	detached bool
	stream   io.ReadCloser
}

func (l *remoteLink) setStream(stream io.ReadCloser) {
	l.mu.Lock()
	l.stream = stream
	detached := l.detached
	l.mu.Unlock()
	if detached && stream != nil {
		_ = stream.Close()
	}
}

func (l *remoteLink) detach() {
	l.mu.Lock()
	l.detached = true
	stream := l.stream
	l.stream = nil
	l.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

func (l *remoteLink) isDetached() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.detached
}

// UseHost attaches every session the host already owns. It must run before the
// HTTP server reconciles stored rows, otherwise a live host session looks
// missing and gets restarted under a duplicate PTY.
func (m *Manager) UseHost(client *HostClient) error {
	if client == nil {
		return errors.New("session host client is nil")
	}
	m.mu.Lock()
	m.host = client
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, err := client.List(ctx)
	if err != nil {
		return fmt.Errorf("list host sessions: %w", err)
	}
	for _, item := range items {
		if _, ok := m.Get(item.ID); ok {
			continue
		}
		snapshot, stream, err := client.Attach(ctx, item.ID)
		if err != nil {
			m.mu.Lock()
			m.sessions[item.ID] = m.newLostSession(item, "host_attach_failed")
			m.mu.Unlock()
			continue
		}
		s := m.newRemoteSession(item, snapshot, stream)
		m.mu.Lock()
		m.sessions[s.ID] = s
		m.mu.Unlock()
		go s.remoteLoop(stream)
	}
	return nil
}

func (m *Manager) startRemote(spec Spec) (*Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	item, err := m.host.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	snapshot, stream, err := m.host.Attach(ctx, item.ID)
	if err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = m.host.Stop(stopCtx, item.ID, "attach_failed")
		stopCancel()
		return nil, fmt.Errorf("attach host session: %w", err)
	}
	s := m.newRemoteSession(item, snapshot, stream)
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	go s.remoteLoop(stream)
	return s, nil
}

func (m *Manager) newRemoteSession(item model.Session, snapshot []byte, stream io.ReadCloser) *Session {
	ring := NewRing(m.ringSize)
	ring.Write(snapshot)
	s := &Session{
		ID:          item.ID,
		WorktreeID:  item.WorktreeID,
		Kind:        item.Kind,
		Agent:       item.Agent,
		Origin:      item.Origin,
		Command:     item.Command,
		Args:        append([]string(nil), item.Args...),
		Title:       item.Title,
		Cols:        item.Cols,
		Rows:        item.Rows,
		Containment: item.Containment,
		CreatedAt:   item.CreatedAt,
		ring:        ring,
		subs:        make(map[chan []byte]struct{}),
		done:        make(chan struct{}),
		status:      item.Status,
		exitCode:    item.ExitCode,
		pid:         item.PID,
		pgid:        item.PGID,
		startTicks:  item.StartTicks,
		incarnation: item.IncarnationID,
		reason:      item.Reason,
		endedAt:     item.EndedAt,
		manager:     m,
		remote:      &remoteLink{client: m.host, id: item.ID, stream: stream},
	}
	// The host appends the input modes it still tracks from the PTY to the end
	// of this snapshot, because the ring tail alone is not sufficient: an
	// enable the app emitted once at startup can have been evicted long before
	// this daemon attached. Seed the tracker from the whole payload so a daemon
	// restart does not lose it.
	s.modes.Write(snapshot)
	if item.Status != model.SessionRunning {
		s.closeDone()
	}
	return s
}

func (m *Manager) newLostSession(item model.Session, reason string) *Session {
	now := time.Now().UTC()
	s := &Session{
		ID:          item.ID,
		WorktreeID:  item.WorktreeID,
		Kind:        item.Kind,
		Agent:       item.Agent,
		Origin:      item.Origin,
		Command:     item.Command,
		Args:        append([]string(nil), item.Args...),
		Title:       item.Title,
		Cols:        item.Cols,
		Rows:        item.Rows,
		Containment: item.Containment,
		CreatedAt:   item.CreatedAt,
		ring:        NewRing(m.ringSize),
		subs:        make(map[chan []byte]struct{}),
		done:        make(chan struct{}),
		status:      model.SessionLost,
		reason:      reason,
		endedAt:     &now,
		manager:     m,
	}
	s.closeDone()
	return s
}

func (s *Session) remoteLoop(stream io.ReadCloser) {
	// Close whichever stream is current when this loop returns. The initial
	// stream is replaced after a mirror drop, so a plain defer would leak the
	// replacement.
	defer func() { _ = stream.Close() }()
	for {
		kind, payload, err := readFrame(stream)
		if err != nil {
			_ = stream.Close()
			next, ok := s.reattachRemote()
			if !ok {
				return
			}
			stream = next
			continue
		}
		switch kind {
		case frameData:
			if len(payload) == 0 {
				continue
			}
			s.writeChunk(payload)
		case frameExit:
			var item model.Session
			if err := json.Unmarshal(payload, &item); err != nil {
				s.markLost("host_protocol_error")
				return
			}
			// A host stream can close because this mirror fell behind its
			// subscriber buffer, not because the PTY exited. An exit frame
			// carrying a running model is that false exit: reattach and draw
			// the live screen again rather than declaring the session dead.
			if item.Status == model.SessionRunning {
				_ = stream.Close()
				next, ok := s.reattachRemote()
				if !ok {
					return
				}
				stream = next
				continue
			}
			s.end(model.SessionExited, item.ExitCode, item.Reason)
			return
		default:
			s.markLost("host_protocol_error")
			return
		}
	}
}

// reattachRemote replaces a mirror stream that ended while the session lives.
// The fresh snapshot is replayed to browser viewers, so a terminal that was
// connected through the dropped mirror sees the current screen instead of a
// frozen one. It reports false when the session is detached, gone or the host
// cannot be reached; the caller then ends or loses the session.
func (s *Session) reattachRemote() (io.ReadCloser, bool) {
	if s.remote == nil {
		return nil, false
	}
	if s.remote.isDetached() {
		return nil, false
	}
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, stream, err := s.remote.client.Attach(ctx, s.ID)
	if err != nil {
		s.markLost("host_lost")
		return nil, false
	}
	s.mu.Lock()
	s.ring.Reset(snapshot)
	s.modes.Write(snapshot)
	s.broadcastLocked(snapshot)
	s.mu.Unlock()
	s.remote.setStream(stream)
	return stream, true
}

func (s *Session) stopRemote(reason string) StopResult {
	s.mu.Lock()
	if s.status != model.SessionRunning {
		s.mu.Unlock()
		return StopAlreadyExited
	}
	if reason == "" {
		reason = "stopped"
	}
	s.reason = reason
	done := s.done
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	verdict, err := s.remote.client.Stop(ctx, s.ID, reason)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.markLost("host_missing")
			return StopAlreadyExited
		}
		return StopUnverifiable
	}
	if waitDone(done, 4*time.Second) {
		return verdict
	}
	s.end(model.SessionExited, nil, reason)
	return verdict
}

func (s *Session) markLost(reason string) {
	s.end(model.SessionLost, nil, reason)
}

func (s *Session) detach() {
	if s.remote != nil {
		s.remote.detach()
	}
	s.mu.Lock()
	subs := s.subs
	s.subs = nil
	s.mu.Unlock()
	for ch := range subs {
		close(ch)
	}
	s.closeDone()
}
