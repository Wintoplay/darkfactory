//go:build !windows

package session

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Wintoplay/darkfactory/internal/model"
)

const (
	frameSnapshot byte = 'S'
	frameData     byte = 'D'
	frameExit     byte = 'X'
	maxHostFrame       = 64 << 20
	maxHostBody        = 64 << 20
)

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > maxHostFrame {
		return fmt.Errorf("host frame too large: %d", len(payload))
	}
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxHostFrame {
		return 0, nil, fmt.Errorf("host frame too large: %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

// NewHostHandler exposes a local Manager over a Unix socket. The handler is
// intentionally unauthenticated: the socket is 0600 and only the same user can
// connect. It must never be mounted on a TCP listener.
func NewHostHandler(manager *Manager, shutdown func()) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeHostJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		list := manager.List()
		out := make([]model.Session, 0, len(list))
		for _, sess := range list {
			out = append(out, sess.Model())
		}
		writeHostJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		var spec Spec
		dec := json.NewDecoder(io.LimitReader(r.Body, maxHostBody))
		if err := dec.Decode(&spec); err != nil {
			http.Error(w, "invalid session spec", http.StatusBadRequest)
			return
		}
		sess, err := manager.Start(spec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeHostJSON(w, http.StatusCreated, sess.Model())
	})
	mux.HandleFunc("DELETE /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		reason := strings.TrimSpace(r.URL.Query().Get("reason"))
		verdict, err := manager.RemoveWithResult(r.PathValue("id"), reason)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeHostJSON(w, http.StatusOK, map[string]any{"verdict": verdict})
	})
	mux.HandleFunc("POST /v1/sessions/{id}/input", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := manager.Get(r.PathValue("id"))
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}
		if err := sess.Write(body); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/sessions/{id}/resize", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := manager.Get(r.PathValue("id"))
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "invalid size", http.StatusBadRequest)
			return
		}
		if err := sess.Resize(body.Cols, body.Rows); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/sessions/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := manager.Get(r.PathValue("id"))
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		flusher, _ := w.(http.Flusher)
		meter := NewStreamMeter(r.PathValue("id"), "host")
		meter.Run(r.Context(), slog.Default())
		snapshot, chunks, cancel := sess.Subscribe()
		defer cancel()
		// The ring tail cannot carry an enable the app emitted once at startup
		// and then painted past. The host owns the PTY and still tracks those
		// modes, so attach carries them after the snapshot. No new frame kind:
		// an older daemon already seeds its tracker from these bytes, so this
		// stays wire-compatible.
		snapshot = append(snapshot, sess.modes.Restore()...)
		if err := writeFrame(w, frameSnapshot, snapshot); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-sess.Done():
				drainHostChunks(w, chunks, flusher)
				_ = writeFrame(w, frameExit, mustJSON(sess.Model()))
				return
			case chunk, ok := <-chunks:
				if !ok {
					// A closed channel is not proof the session ended. The
					// broadcast path closes a subscriber that falls too far
					// behind, and the session may still be running. Sending an
					// exit frame here would make the daemon mark a live PTY as
					// exited.
					if sess.Status() != model.SessionRunning {
						_ = writeFrame(w, frameExit, mustJSON(sess.Model()))
					}
					return
				}
				if len(chunk) == 0 {
					continue
				}
				chunk, closed := takeHostChunks(chunks, chunk)
				if len(chunk) > 0 {
					if err := writeFrame(w, frameData, chunk); err != nil {
						return
					}
					meter.Record(len(chunk))
					if flusher != nil {
						flusher.Flush()
					}
				}
				if closed {
					if sess.Status() != model.SessionRunning {
						_ = writeFrame(w, frameExit, mustJSON(sess.Model()))
					}
					return
				}
			}
		}
	})
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		manager.CloseAllWithReason("host_shutdown")
		if shutdown != nil {
			shutdown()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// hostBatchLimit bounds how much output one frame carries. Batching turns a
// fast PTY that writes line by line into a few larger frames instead of one
// HTTP chunk and flush per write. The limit keeps a burst from delaying the
// first bytes a client sees for too long.
const hostBatchLimit = 256 << 10

// takeHostChunks appends chunks that are already queued for this subscriber.
// The bool reports that the channel closed while draining, which may mean the
// session ended or that this subscriber was dropped for falling behind.
func takeHostChunks(chunks <-chan []byte, batch []byte) ([]byte, bool) {
	for len(batch) < hostBatchLimit {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return batch, true
			}
			batch = append(batch, chunk...)
		default:
			return batch, false
		}
	}
	return batch, false
}

func drainHostChunks(w io.Writer, chunks <-chan []byte, flusher http.Flusher) {
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return
			}
			if len(chunk) == 0 {
				continue
			}
			chunk, closed := takeHostChunks(chunks, chunk)
			if len(chunk) > 0 {
				if err := writeFrame(w, frameData, chunk); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if closed {
				return
			}
		default:
			return
		}
	}
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func writeHostJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
