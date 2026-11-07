package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wintoplay/darkfactory/internal/id"
	"github.com/Wintoplay/darkfactory/internal/model"
)

// A native pane capability lets the desktop shell run the existing attach
// client for one session without ever receiving the master token or a device
// bearer. The page, already authenticated, mints it; the shell redeems it as a
// bearer for the four session routes only. It is single-stream, expires while
// unused, and is dropped when the stream closes.
const (
	paneUnusedTTL = 2 * time.Minute
	paneOpenTTL   = 12 * time.Hour
	maxPanes      = 32
)

type paneGrant struct {
	principal principal
	session   string
	expires   time.Time
	opened    bool
}

func (s *Server) handleNativePane(w http.ResponseWriter, r *http.Request) {
	if !isLocalUIRequest(r) {
		s.refuse(w, r, codeNativePaneScope, "the native pane is minted on the daemon host")
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
	pane := id.New()
	key := s.hashSecret(pane)
	now := time.Now()
	s.paneMu.Lock()
	if s.panes == nil {
		s.panes = make(map[string]paneGrant)
	}
	for k, grant := range s.panes {
		if now.After(grant.expires) {
			delete(s.panes, k)
		}
	}
	if len(s.panes) >= maxPanes {
		s.paneMu.Unlock()
		s.refuse(w, r, codeNativeTerminalFailed, "")
		return
	}
	s.panes[key] = paneGrant{
		principal: principalFromRequest(r),
		session:   sess.ID,
		expires:   now.Add(paneUnusedTTL),
	}
	s.paneMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"pane": pane})
}

func (s *Server) paneFor(token string) (paneGrant, bool) {
	if token == "" {
		return paneGrant{}, false
	}
	key := s.hashSecret(token)
	now := time.Now()
	s.paneMu.Lock()
	defer s.paneMu.Unlock()
	grant, ok := s.panes[key]
	if !ok {
		return paneGrant{}, false
	}
	if now.After(grant.expires) {
		delete(s.panes, key)
		return paneGrant{}, false
	}
	return grant, true
}

// claimPaneStream binds the capability to one stream and extends it for the
// life of the attached window. A second stream with the same pane is refused.
func (s *Server) claimPaneStream(key string) bool {
	s.paneMu.Lock()
	defer s.paneMu.Unlock()
	grant, ok := s.panes[key]
	now := time.Now()
	if !ok || grant.opened || now.After(grant.expires) {
		return false
	}
	grant.opened = true
	grant.expires = now.Add(paneOpenTTL)
	s.panes[key] = grant
	return true
}

func (s *Server) dropPane(key string) {
	s.paneMu.Lock()
	delete(s.panes, key)
	s.paneMu.Unlock()
}

// dropPanesFor ends every pane minted by one principal. A capability is
// authority over a PTY, so logging out or revoking a device must take it away
// now, not when the window happens to close.
func (s *Server) dropPanesFor(p principal) {
	s.paneMu.Lock()
	defer s.paneMu.Unlock()
	for key, grant := range s.panes {
		if grant.principal == p {
			delete(s.panes, key)
		}
	}
}

// dropAllPanes ends every pane capability. A revoke-all has no principal left
// to compare against.
func (s *Server) dropAllPanes() {
	s.paneMu.Lock()
	s.panes = nil
	s.paneMu.Unlock()
}

// dropPanesExcept is the keepCurrent form of a revoke-all.
func (s *Server) dropPanesExcept(keep principal) {
	s.paneMu.Lock()
	defer s.paneMu.Unlock()
	for key, grant := range s.panes {
		if grant.principal != keep {
			delete(s.panes, key)
		}
	}
}

// dropPanesForSession ends every pane attached to a session that is going away.
func (s *Server) dropPanesForSession(sessionID string) {
	s.paneMu.Lock()
	defer s.paneMu.Unlock()
	for key, grant := range s.panes {
		if grant.session == sessionID {
			delete(s.panes, key)
		}
	}
}

// paneRoute reports whether a path is one of the four routes a pane capability
// may use, and which session it names.
func paneRoute(path string) (session, action string, ok bool) {
	rest, found := strings.CutPrefix(path, "/api/v1/sessions/")
	if !found {
		return "", "", false
	}
	session, action, found = strings.Cut(rest, "/")
	if !found || session == "" {
		return "", "", false
	}
	switch action {
	case "stream", "input", "resize", "redraw":
		return session, action, true
	}
	return "", "", false
}
