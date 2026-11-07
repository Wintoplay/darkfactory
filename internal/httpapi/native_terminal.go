package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
)

// handleNativeTerminal starts a local window only after the gateway has
// recorded the decision. The attach process receives the same credential as
// the caller over an anonymous descriptor, preserving its scope and policy.
func (s *Server) handleNativeTerminal(w http.ResponseWriter, r *http.Request) {
	if !isLocalUIRequest(r) {
		s.refuse(w, r, codeNativeTerminalRemote, "")
		return
	}
	sess, ok := s.sessions.Get(r.PathValue("id"))
	if !ok {
		s.refuse(w, r, codeSessionNotFound, "")
		return
	}
	if sess.Status() != model.SessionRunning {
		s.refuse(w, r, codeNativeTerminalUnavailable, "session is not running")
		return
	}
	if runtime.GOOS != "linux" || (os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "") {
		s.refuse(w, r, codeNativeTerminalUnavailable, "a Linux graphical session is required")
		return
	}
	ghostty, err := exec.LookPath("ghostty")
	if err != nil {
		s.refuse(w, r, codeNativeTerminalUnavailable, "Ghostty is not installed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if !supportedGhostty(ctx, ghostty) {
		s.refuse(w, r, codeNativeTerminalUnavailable, "this feature requires the tested Ghostty 1.3.1")
		return
	}
	credential := ""
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(header, "Bearer ") {
		credential = strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	if credential == "" {
		cookie, e := r.Cookie(cookieName)
		if e != nil {
			s.refuse(w, r, codeUnauthorized, "")
			return
		}
		credential = cookie.Value
	}
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(requestID) > 128 {
		s.refuse(w, r, codeInvalidRequest, "Idempotency-Key is longer than 128 bytes")
		return
	}
	key := nativeRequestKey{principal: principalFromRequest(r), session: sess.ID, request: requestID}
	if err := s.nativeOnce(r.Context(), key, func() error {
		return startNativeTerminal(r.Context(), ghostty, r.Host, sess.ID, credential)
	}); err != nil {
		s.log.Warn("native terminal could not start")
		s.refuse(w, r, codeNativeTerminalFailed, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The readiness wait is a variable so tests can shorten it. Ten seconds covers
// a cold Ghostty start on the machines this was exercised on.
var nativeReadyTimeout = 10 * time.Second

// A repeated launch intent must not open another window after a lost response.
type nativeRequestKey struct {
	principal principal
	session   string
	request   string
}

type nativeRequest struct {
	done      chan struct{}
	completed time.Time
}

func (s *Server) nativeOnce(ctx context.Context, key nativeRequestKey, launch func() error) error {
	if key.request == "" {
		return launch()
	}
	for {
		s.nativeMu.Lock()
		if s.nativeRequests == nil {
			s.nativeRequests = make(map[nativeRequestKey]*nativeRequest)
		}
		for k, entry := range s.nativeRequests {
			if !entry.completed.IsZero() && time.Since(entry.completed) > 10*time.Minute {
				delete(s.nativeRequests, k)
			}
		}
		if entry := s.nativeRequests[key]; entry != nil {
			if !entry.completed.IsZero() {
				s.nativeMu.Unlock()
				return nil
			}
			done := entry.done
			s.nativeMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if len(s.nativeRequests) >= 2048 {
			s.nativeMu.Unlock()
			return errors.New("native launch capacity reached")
		}
		entry := &nativeRequest{done: make(chan struct{})}
		s.nativeRequests[key] = entry
		s.nativeMu.Unlock()
		err := launch()
		s.nativeMu.Lock()
		if err != nil {
			delete(s.nativeRequests, key)
		} else {
			entry.completed = time.Now()
		}
		close(entry.done)
		s.nativeMu.Unlock()
		return err
	}
}

// nativeTerminalVersion is the only Ghostty version whose output controls and
// launch flags were exercised. Reject unknown versions rather than silently
// relying on new defaults.
const nativeTerminalVersion = "1.3.1"

func supportedGhostty(ctx context.Context, binary string) bool {
	version, ok := ghosttyVersion(ctx, binary)
	return ok && version == nativeTerminalVersion
}

func ghosttyVersion(ctx context.Context, binary string) (string, bool) {
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 || fields[0] != "Ghostty" {
		return "", false
	}
	return fields[1], true
}

// nativeTerminalView is the local UI's answer to "show the Ghostty button?".
// The browser terminal (xterm.js) and the desktop VTE pane do not depend on
// this. Hiding the action when the host cannot run it is better than offering
// a button that can only fail.
type nativeTerminalView struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const nativeTerminalCapTTL = 30 * time.Second

// nativeTerminalCapability probes the host at most once per TTL. The probe is
// deliberately about the daemon host, not the browser: the Ghostty window is a
// host-local launcher, so a remote browser never sees it regardless.
func (s *Server) nativeTerminalCapability() nativeTerminalView {
	s.nativeCapMu.Lock()
	defer s.nativeCapMu.Unlock()
	now := time.Now()
	if !s.nativeCapAt.IsZero() && now.Sub(s.nativeCapAt) < nativeTerminalCapTTL {
		return s.nativeCap
	}
	s.nativeCap = probeNativeTerminal()
	s.nativeCapAt = now
	return s.nativeCap
}

func probeNativeTerminal() nativeTerminalView {
	if runtime.GOOS != "linux" {
		return nativeTerminalView{Reason: "not_linux"}
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nativeTerminalView{Reason: "no_graphical_session"}
	}
	binary, err := exec.LookPath("ghostty")
	if err != nil {
		return nativeTerminalView{Reason: "not_installed"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	version, ok := ghosttyVersion(ctx, binary)
	if !ok || version != nativeTerminalVersion {
		return nativeTerminalView{Reason: "unsupported_version"}
	}
	return nativeTerminalView{Available: true, Version: version}
}

func startNativeTerminal(ctx context.Context, ghostty, listen, id, credential string) error {
	if credential == "" || len(credential) > 4096 || strings.ContainsAny(credential, "\r\n") {
		return errors.New("invalid credential")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	if _, err = io.WriteString(writer, credential); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyReader.Close()
	defer readyWriter.Close()
	cmd := exec.Command(ghostty,
		"--gtk-single-instance=false", "--clipboard-read=deny", "--clipboard-write=ask",
		"--link-url=false", "--image-storage-limit=0", "--right-click-action=context-menu",
		"--mouse-shift-capture=never", "--confirm-close-surface=false", "-e", exe, "native-attach",
		"--listen", listen, "--credential-fd", "3", "--ready-fd", "4", id)
	cmd.ExtraFiles = []*os.File{reader, readyWriter}
	if err = cmd.Start(); err != nil {
		return err
	}
	_ = readyWriter.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ready := make(chan error, 1)
	go func() {
		var b [1]byte
		_, e := io.ReadFull(readyReader, b[:])
		if e == nil && b[0] != 1 {
			e = errors.New("invalid readiness response")
		}
		ready <- e
	}()
	timer := time.NewTimer(nativeReadyTimeout)
	defer timer.Stop()
	select {
	case err = <-ready:
		if err == nil {
			return nil
		}
	case err = <-done:
		if err == nil {
			err = errors.New("native window exited before attaching")
		}
	case <-timer.C:
		err = errors.New("native window did not attach")
	case <-ctx.Done():
		err = ctx.Err()
	}
	_ = cmd.Process.Kill()
	return err
}
