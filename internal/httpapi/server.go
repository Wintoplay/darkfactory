package httpapi

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/gitwt"
	"github.com/Wintoplay/darkfactory/internal/id"
	"github.com/Wintoplay/darkfactory/internal/model"
	runpkg "github.com/Wintoplay/darkfactory/internal/run"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
	"github.com/Wintoplay/darkfactory/web"
)

const (
	maxBody       = 1 << 20
	maxDirEntries = 400
	cookieName    = "__Host-df_session"
	// Branches DarkFactory creates carry their own namespace. This was `orca/`,
	// a rename leftover: the branch name is user-visible and ends up in git
	// completion, so it has to say where it came from.
	worktreeBranchPrefix = "darkfactory/"
	openCodeTTL          = 60 * time.Second
	browserTTL           = 30 * 24 * time.Hour
	browserMaxTTL        = 90 * 24 * time.Hour
	browserRenewInterval = time.Hour

	deviceKeyTTL       = 180 * 24 * time.Hour
	deviceSessionTTL   = 12 * time.Hour
	deviceChallengeTTL = 2 * time.Minute
	deviceGlobalLimit  = 120
	deviceSourceLimit  = 20

	pairCodeTTL      = 120 * time.Second
	pairGlobalLimit  = 30
	pairSourceLimit  = 5
	pairRateInterval = time.Minute

	// The encrypted envelope is the phone's whole API: every keystroke is one
	// envelope, and a fast typist plus two three-second pollers exceeds a
	// 5/second budget. 20/second per source still bounds an unauthenticated
	// caller's parse, ECDH and AEAD work; the global ceiling is three busy
	// phones.
	e2eeGlobalLimit = 3600
	e2eeSourceLimit = 1200

	scopeFull = "full"
	scopeRead = "read"

	// browserKeyVersion marks rows keyed with the instance secret. Bump it
	// whenever the key derivation changes; loadBrowsers drops older rows.
	browserKeyVersion = 1
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Build is the daemon's version plus the commit the Makefile stamped. The UI
// shows it in the footer and /state returns it, so a browser can name the
// build it is actually talking to.
var Build = "unknown"

func validName(name string) bool {
	return safeName.MatchString(name) && name != "." && name != ".."
}

type Server struct {
	cfg      config.Config
	store    *store.Store
	sessions *session.Manager
	runs     *runpkg.Coordinator
	log      *slog.Logger
	openCode *openCodeClient

	// createMu serialises the idempotent create path: checking for a replayed
	// request id and starting the PTY must not interleave with a second retry.
	createMu sync.Mutex

	nativeMu       sync.Mutex
	nativeRequests map[nativeRequestKey]*nativeRequest
	// nativeCap caches the host-local Ghostty probe. /state is polled, and a
	// newly installed Ghostty should appear without a restart; a 30 s TTL is
	// the compromise between exec cost and staleness.
	nativeCapMu sync.Mutex
	nativeCap   nativeTerminalView
	nativeCapAt time.Time

	// paneMu guards the short-lived native pane capabilities the local UI mints
	// for the desktop shell. The map key is a hash of the capability.
	paneMu sync.Mutex
	panes  map[string]paneGrant

	openMu      sync.Mutex
	openCodes   map[string]time.Time
	browsers    map[string]store.Browser
	pairCodes   map[string]pairGrant
	pairGlobal  pairWindow
	pairSources map[string]pairWindow

	// sourceMu rate-limits the public-source warning. The check itself is
	// stateless; only the "we already logged this hour" bit lives here.
	sourceMu     sync.Mutex
	sourceWarnAt time.Time

	// deviceMu guards the public-key credential, its short-lived sessions, and
	// the one-use challenges. These are separate from browser cookies so a
	// WebView that rejects Secure cookies can still authenticate.
	deviceMu         sync.Mutex
	deviceKeys       map[string]store.DeviceKey
	deviceChallenges map[string]deviceChallenge
	deviceSessions   map[string]deviceSession
	deviceGlobal     pairWindow
	deviceSources    map[string]pairWindow

	gateway *gateway

	// notify feeds the local desktop notifier and the phone's foreground
	// service. It is empty until StartNotifications starts the watchers.
	notify *notificationHub

	// e2ee is the daemon's P-256 identity. e2eeReplay stops a captured
	// encrypted envelope from being applied twice.
	e2ee       *e2eeState
	e2eeMu     sync.Mutex
	e2eeReplay map[string]time.Time

	// e2eeLimitMu guards the envelope budget. It is separate from e2eeMu so a
	// rate-limited request does not serialise behind a replay write.
	e2eeLimitMu sync.Mutex
	e2eeGlobal  pairWindow
	e2eeSources map[string]pairWindow
}

type deviceChallenge struct {
	DeviceID  string
	ExpiresAt time.Time
}

type deviceSession struct {
	DeviceID  string
	Scope     string
	ExpiresAt time.Time
}

type pairGrant struct {
	ExpiresAt time.Time
	Label     string
	Scope     string
}

type pairWindow struct {
	Start time.Time
	Count int
}

func (w *pairWindow) allow(now time.Time, limit int) bool {
	if w.Start.IsZero() || now.Sub(w.Start) >= pairRateInterval {
		w.Start = now
		w.Count = 0
	}
	if w.Count >= limit {
		return false
	}
	w.Count++
	return true
}

func New(cfg config.Config, st *store.Store, sessions *session.Manager, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if strings.TrimSpace(cfg.InstanceSecret) == "" {
		// config.Load always sets one; this covers an embedder or a test that
		// builds Config by hand. A random secret still fails closed: rows from
		// the previous process will not match.
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			panic("darkfactory: no randomness for instance secret: " + err.Error())
		}
		cfg.InstanceSecret = hex.EncodeToString(raw[:])
		log.Warn("instance secret missing; browser sessions will not survive a restart")
	}
	s := &Server{
		cfg:              cfg,
		store:            st,
		sessions:         sessions,
		log:              log,
		openCode:         newOpenCodeClient(cfg),
		openCodes:        make(map[string]time.Time),
		browsers:         make(map[string]store.Browser),
		pairCodes:        make(map[string]pairGrant),
		pairSources:      make(map[string]pairWindow),
		deviceKeys:       make(map[string]store.DeviceKey),
		deviceChallenges: make(map[string]deviceChallenge),
		deviceSessions:   make(map[string]deviceSession),
		deviceSources:    make(map[string]pairWindow),
		e2eeReplay:       make(map[string]time.Time),
		e2eeSources:      make(map[string]pairWindow),
	}
	e2ee, err := loadOrCreateE2EE(st)
	if err != nil {
		// Failing closed is better than silently minting a new pin: an E2EE
		// request then refuses while ordinary local-browser pairing still works.
		log.Error("host e2ee identity unavailable", "error", err)
	} else {
		s.e2ee = e2ee
	}
	// The gateway always has a recorder, but only a real store can hold the
	// chain. An embedder without one keeps the seam and loses the audit trail.
	s.gateway = newGateway(nopRecorder{}, cfg.Policy, log)
	if st != nil {
		s.gateway = newGateway(ledgerRecorder{store: st}, cfg.Policy, log)
	}
	s.loadBrowsers()
	s.loadDeviceKeys()
	if st != nil && sessions != nil {
		s.runs = runpkg.New(cfg, st, sessions, log)
		sessions.SetPersist(func(item model.Session) {
			if err := st.UpdateSession(item); err != nil && !errors.Is(err, store.ErrNotFound) {
				log.Error("persist session", "session", item.ID, "error", err)
			}
		})
		sessions.SetTranscript(func(id string, data []byte) {
			if err := st.PutTranscript(id, data); err != nil {
				log.Error("persist transcript", "session", id, "error", err)
			}
		})
		s.reconcileWorktrees()
		s.reconcileWorkspaces()
		s.reconcileSessions()
	}
	s.notify = newNotificationHub(s)
	return s
}

// reconcileWorktrees fills the containment mode for rows created before the
// field existed, so the UI and the next session agree on the mode. A global
// "off" is an explicit compatibility reset: older rows were auto-promoted to
// jail, which breaks agents that need the user's real HOME and shared backend.
func (s *Server) reconcileWorktrees() {
	worktrees, err := s.store.ListWorktrees()
	if err != nil {
		s.log.Error("reconcile worktrees", "error", err)
		return
	}
	globalOff := strings.TrimSpace(s.cfg.Containment) == "off"
	for _, wt := range worktrees {
		mode := strings.TrimSpace(wt.Containment)
		if !globalOff && mode != "" {
			continue
		}
		wt.Containment = s.defaultContainment()
		if _, err := s.store.PutWorktree(wt); err != nil {
			s.log.Error("reconcile worktree", "worktree", wt.ID, "error", err)
		}
	}
}

// reconcileWorkspaces gives every repo its in-place workspace. Rows added
// before the invariant existed are repaired here so the fleet is always
// repo → workspace → sessions.
func (s *Server) reconcileWorkspaces() {
	repos, err := s.store.ListRepos()
	if err != nil {
		s.log.Error("reconcile workspaces: list repos", "error", err)
		return
	}
	worktrees, err := s.store.ListWorktrees()
	if err != nil {
		s.log.Error("reconcile workspaces: list worktrees", "error", err)
		return
	}
	have := make(map[string]bool, len(worktrees))
	for _, wt := range worktrees {
		if wt.Workspace {
			have[wt.RepoID] = true
		}
	}
	for _, repo := range repos {
		if have[repo.ID] {
			continue
		}
		if _, err := s.store.PutWorktree(model.Worktree{
			RepoID:      repo.ID,
			Path:        repo.Path,
			Workspace:   true,
			Containment: s.defaultContainment(),
		}); err != nil {
			s.log.Error("reconcile workspace", "repo", repo.ID, "error", err)
		}
	}
}

// reconcileSessions runs once at startup. A row that was live when the daemon
// stopped is restarted under the same ID, with its bounded scrollback, so the
// browser sees a reconnect rather than a tombstone. A process that exited on
// its own is left alone. A row that cannot be restarted is marked lost.
func (s *Server) reconcileSessions() {
	stored, err := s.store.ListSessions()
	if err != nil {
		s.log.Error("reconcile sessions", "error", err)
		return
	}
	live := make(map[string]bool)
	for _, sess := range s.sessions.List() {
		live[sess.ID] = true
	}
	now := time.Now().UTC()
	for _, item := range stored {
		if live[item.ID] {
			continue
		}
		if !restorable(item) {
			if item.Status == model.SessionRunning {
				markSessionLost(s, item, "daemon_restart", now)
			}
			continue
		}
		if err := s.restoreSession(item); err != nil {
			s.log.Warn("restore session", "session", item.ID, "error", err)
			markSessionLost(s, item, "restore_failed", now)
		}
	}
}

func restorable(item model.Session) bool {
	if item.Origin == "run" {
		return false
	}
	switch item.Status {
	case model.SessionRunning:
		return true
	case model.SessionExited:
		return item.Reason == "shutdown"
	case model.SessionLost:
		return item.Reason == "daemon_restart" || item.Reason == "host_lost" || item.Reason == "host_missing"
	default:
		return false
	}
}

func markSessionLost(s *Server, item model.Session, reason string, now time.Time) {
	item.Status = model.SessionLost
	item.Reason = reason
	if item.EndedAt == nil {
		item.EndedAt = &now
	}
	if err := s.store.UpdateSession(item); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("mark session lost", "session", item.ID, "error", err)
	}
}

func (s *Server) restoreSession(item model.Session) error {
	wt, err := s.store.GetWorktree(item.WorktreeID)
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		return fmt.Errorf("worktree path: %w", err)
	}
	kind := strings.TrimSpace(item.Kind)
	if kind == "" {
		kind = "agent"
	}
	agent := strings.TrimSpace(item.Agent)
	command, args := item.Command, append([]string(nil), item.Args...)
	var env map[string]string
	title := item.Title
	if kind == "shell" || agent == "shell" {
		kind = "shell"
		if command == "" {
			command = os.Getenv("SHELL")
		}
		if command == "" {
			command = "/bin/sh"
		}
		if len(args) == 0 {
			args = []string{"-l"}
		}
		if title == "" {
			title = "shell"
		}
	} else if preset, ok := s.cfg.Agents[agent]; ok {
		command, args, env = preset.Command, preset.Args, preset.Env
		if title == "" {
			title = agent
		}
	} else if command == "" {
		return fmt.Errorf("unknown agent %q", agent)
	}
	containment := strings.TrimSpace(item.Containment)
	if containment == "" {
		containment = s.containmentFor(wt)
	}
	if preset, ok := s.cfg.Agents[agent]; ok && strings.TrimSpace(preset.Containment) != "" {
		containment = preset.Containment
		if containment == "auto" {
			containment = s.defaultContainment()
		}
	}
	if containment == "jail" && !session.JailAvailable() && !s.cfg.JailScrubEnv {
		containment = "off"
	}
	home := ""
	if containment == "jail" {
		home = s.homeFor(wt.ID)
	}
	history, err := s.store.GetTranscript(item.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("transcript: %w", err)
	}
	hiddenPaths, err := s.hiddenPathsFor(containment, agent)
	if err != nil {
		return err
	}
	origin := item.Origin
	if origin == "" {
		origin = "user"
	}
	sess, err := s.sessions.Start(session.Spec{
		ID:          item.ID,
		CreatedAt:   item.CreatedAt,
		WorktreeID:  wt.ID,
		Dir:         wt.Path,
		Kind:        kind,
		Agent:       agent,
		Origin:      origin,
		Command:     command,
		Args:        args,
		Env:         env,
		Title:       title,
		Cols:        item.Cols,
		Rows:        item.Rows,
		Containment: containment,
		Home:        home,
		HiddenPaths: hiddenPaths,
		ScrubEnv:    containment == "jail" && s.cfg.JailScrubEnv,
		EnvAllow:    s.jailEnvAllow(agent),
		History:     history,
	})
	if err != nil {
		return err
	}
	if err := s.store.UpdateSession(sess.Model()); err != nil {
		sess.Close()
		return fmt.Errorf("update row: %w", err)
	}
	s.log.Info("restored session", "session", item.ID, "worktree", wt.ID, "agent", agent, "historyBytes", len(history))
	return nil
}

// route is one API endpoint. The table is the single wiring point: Handler
// registers it, and the refusal and gateway tests walk the same list, so a
// route cannot be added without a typed auth failure.
type route struct {
	method  string
	path    string
	handler http.HandlerFunc
	auth    bool
	// verb is the gateway verb for a mutation. Empty for reads and for the
	// code-gated bootstrap routes, which are called out in gateway.go.
	verb string
}

func (s *Server) routeTable() []route {
	return []route{
		{http.MethodPost, "/api/v1/open/code", s.handleOpenCode, true, verbOpenCode},
		{http.MethodPost, "/api/v1/open", s.handleOpen, false, ""},
		{http.MethodPost, "/api/v1/open/logout", s.handleLogout, true, verbLogout},
		{http.MethodPost, "/api/v1/pair/code", s.handlePairCode, true, verbPairCode},
		{http.MethodPost, "/api/v1/pair", s.handlePair, false, ""},
		{http.MethodGet, "/api/v1/e2ee/host-key", s.handleHostKey, false, ""},
		{http.MethodPost, "/api/v1/e2ee", s.handleE2EE, false, ""},
		{http.MethodGet, "/api/v1/devices", s.handleDevices, true, ""},
		{http.MethodPost, "/api/v1/devices/revoke-all", s.handleRevokeAllDevices, true, verbDevicesRevokeAll},
		{http.MethodDelete, "/api/v1/devices/{id}", s.handleDeleteDevice, true, verbDeviceRevoke},
		{http.MethodPost, "/api/v1/device-keys", s.handleCreateDeviceKey, true, verbDeviceKeyCreate},
		{http.MethodPost, "/api/v1/device-keys/challenge", s.handleDeviceChallenge, false, ""},
		{http.MethodPost, "/api/v1/device-keys/verify", s.handleDeviceVerify, false, ""},
		{http.MethodGet, "/api/v1/state", s.handleState, true, ""},
		{http.MethodGet, "/api/v1/notifications", s.handleNotifications, true, ""},
		{http.MethodGet, "/api/v1/notifications/settings", s.handleGetNotificationSettings, true, ""},
		{http.MethodPost, "/api/v1/notifications/settings", s.handleNotificationSettings, true, verbNotificationSettings},
		{http.MethodPost, "/api/v1/notifications/dismiss", s.handleDismissNotifications, true, verbNotificationDismiss},
		{http.MethodPost, "/api/v1/notifications/clear", s.handleClearNotifications, true, verbNotificationClear},
		{http.MethodGet, "/api/v1/audit", s.handleAudit, true, ""},
		{http.MethodGet, "/api/v1/opencode/sessions", s.handleOpenCodeSessions, true, ""},
		{http.MethodPost, "/api/v1/opencode/sessions", s.handleCreateOpenCodeSession, true, verbOpenCodeCreate},
		{http.MethodGet, "/api/v1/opencode/sessions/{id}/messages", s.handleOpenCodeMessages, true, ""},
		{http.MethodPost, "/api/v1/opencode/sessions/{id}/prompt", s.handleOpenCodePrompt, true, verbOpenCodePrompt},
		{http.MethodPost, "/api/v1/opencode/sessions/{id}/interrupt", s.handleOpenCodeInterrupt, true, verbOpenCodeInterrupt},
		{http.MethodGet, "/api/v1/opencode/sessions/{id}/permissions", s.handleOpenCodePermissions, true, ""},
		{http.MethodPost, "/api/v1/opencode/sessions/{id}/permissions/{requestID}/reply", s.handleOpenCodePermissionReply, true, verbOpenCodePermission},
		{http.MethodPost, "/api/v1/opencode/sessions/{id}/command", s.handleOpenCodeCommand, true, verbOpenCodeCommand},
		{http.MethodGet, "/api/v1/opencode/commands", s.handleOpenCodeCommands, true, ""},
		{http.MethodGet, "/api/v1/opencode/events", s.handleOpenCodeEvents, true, ""},
		{http.MethodGet, "/api/v1/fs/dirs", s.handleListDirs, true, ""},
		{http.MethodPost, "/api/v1/fs/pick", s.handlePickDir, true, verbPickDirectory},
		{http.MethodPost, "/api/v1/debug/client", s.handleClientDebug, true, verbDebugLog},
		{http.MethodPost, "/api/v1/repos", s.handleCreateRepo, true, verbRepoCreate},
		{http.MethodPost, "/api/v1/repos/order", s.handleReorderRepos, true, verbRepoOrder},
		{http.MethodDelete, "/api/v1/repos/{id}", s.handleDeleteRepo, true, verbRepoDelete},
		{http.MethodPost, "/api/v1/worktrees", s.handleCreateWorktree, true, verbWorktreeCreate},
		{http.MethodPost, "/api/v1/worktrees/order", s.handleReorderWorktrees, true, verbWorktreeOrder},
		{http.MethodPost, "/api/v1/worktrees/{id}/reveal", s.handleRevealWorktree, true, verbWorktreeReveal},
		{http.MethodDelete, "/api/v1/worktrees/{id}", s.handleDeleteWorktree, true, verbWorktreeDelete},
		{http.MethodGet, "/api/v1/runs", s.handleListRuns, true, ""},
		{http.MethodPost, "/api/v1/runs", s.handleCreateRun, true, verbRunCreate},
		{http.MethodGet, "/api/v1/runs/{id}", s.handleGetRun, true, ""},
		{http.MethodGet, "/api/v1/runs/{id}/timeline", s.handleGetRun, true, ""},
		{http.MethodPost, "/api/v1/runs/{id}/start", s.handleStartRun, true, verbRunStart},
		{http.MethodPost, "/api/v1/runs/{id}/cancel", s.handleCancelRun, true, verbRunCancel},
		{http.MethodPost, "/api/v1/runs/{id}/steps/{ordinal}/answer", s.handleAnswerStep, true, verbRunAnswer},
		{http.MethodPost, "/api/v1/sessions", s.handleCreateSession, true, verbSessionCreate},
		{http.MethodPost, "/api/v1/sessions/order", s.handleReorderSessions, true, verbSessionOrder},
		{http.MethodPost, "/api/v1/sessions/{id}/pin", s.handlePinSession, true, verbSessionPin},
		{http.MethodDelete, "/api/v1/sessions/{id}", s.handleDeleteSession, true, verbSessionDelete},
		{http.MethodGet, "/api/v1/sessions/{id}/stream", s.handleStream, true, ""},
		{http.MethodPost, "/api/v1/sessions/{id}/native", s.handleNativeTerminal, true, verbSessionNative},
		{http.MethodPost, "/api/v1/sessions/{id}/native-pane", s.handleNativePane, true, verbSessionNativePane},
		{http.MethodPost, "/api/v1/sessions/{id}/input", s.handleInput, true, verbSessionInput},
		{http.MethodPost, "/api/v1/sessions/{id}/resize", s.handleResize, true, verbSessionResize},
		{http.MethodPost, "/api/v1/sessions/{id}/redraw", s.handleRedraw, true, verbSessionRedraw},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	for _, rt := range s.routeTable() {
		handler := rt.handler
		if rt.verb != "" {
			handler = s.mutate(rt.verb, handler)
		}
		if rt.auth {
			handler = s.auth(handler)
		}
		mux.HandleFunc(rt.method+" "+rt.path, handler)
	}
	mux.Handle("GET /{$}", localUIOnly(http.HandlerFunc(s.handleIndex)))
	mux.Handle("GET /index.html", localUIOnly(http.HandlerFunc(s.handleIndex)))
	mux.Handle("/", localUIOnly(http.FileServer(http.FS(web.Assets))))
	return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A non-loopback listener refuses a direct public peer before routing,
		// so no route (not even /healthz or /pair) is an exception.
		if ref := s.sourceRefusal(r); ref != nil {
			s.writeRefusal(w, *ref)
			return
		}
		// A known path with the wrong method must answer with the same
		// envelope; the mux's default 405 is plain text.
		if allowed, wrong := s.wrongMethod(r); wrong {
			if len(allowed) > 0 {
				w.Header().Set("Allow", strings.Join(allowed, ", "))
			}
			s.refuse(w, r, codeMethodNotAllowed, r.Method+" is not allowed on this route")
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

// wrongMethod reports whether the path belongs to an API route that does not
// accept this method. HEAD is allowed wherever GET is.
func (s *Server) wrongMethod(r *http.Request) ([]string, bool) {
	var allowed []string
	matched := false
	for _, rt := range s.routeTable() {
		if !pathMatches(rt.path, r.URL.Path) {
			continue
		}
		matched = true
		if rt.method == r.Method || (r.Method == http.MethodHead && rt.method == http.MethodGet) {
			return nil, false
		}
		allowed = append(allowed, rt.method)
	}
	if !matched {
		return nil, false
	}
	sort.Strings(allowed)
	return allowed, true
}

func pathMatches(pattern, path string) bool {
	if strings.HasSuffix(pattern, "/") != strings.HasSuffix(path, "/") {
		return false
	}
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			continue
		}
		if part != pathParts[i] {
			return false
		}
	}
	return true
}

// handleIndex serves the shell with a per-response nonce. xterm.js creates
// <style> nodes at runtime; the client stamps this nonce onto them. xterm also
// writes dynamic truecolor values as style attributes, which a nonce cannot
// cover, so style-src-attr allows only attributes; style-src itself stays
// nonce-gated and inline scripts remain blocked by default-src.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	nonce, err := newNonce()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, err := web.Assets.ReadFile("index.html")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := strings.Replace(string(raw), "{{CSP_NONCE}}", nonce, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(
		"default-src 'self'; style-src 'self' 'nonce-%s'; style-src-attr 'unsafe-inline'; object-src 'none'; frame-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		nonce,
	))
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func newNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// securityHeaders are cheap and do not depend on auth mode. The index route
// adds a style nonce for xterm's injected styles; everything else stays strict.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		// The binary embeds the UI; never let a browser keep an older app.js.
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r) {
			s.refuse(w, r, codeOriginDenied, "host is not in the allowlist")
			return
		}
		if !sameSiteRequest(r) {
			s.refuse(w, r, codeOriginDenied, "cross-site requests are refused")
			return
		}
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			if !sameOrigin(origin, r) {
				s.refuse(w, r, codeOriginDenied, "origin does not match the request host")
				return
			}
		}
		info, ok := s.authenticate(r)
		if !ok {
			s.refuse(w, r, codeUnauthorized, "")
			return
		}
		if info.kind == authPane {
			sid, action, ok := paneRoute(r.URL.Path)
			if !ok || sid != info.paneSession {
				s.refuse(w, r, codeNativePaneScope, "")
				return
			}
			if action == "stream" && !s.claimPaneStream(info.paneKey) {
				s.refuse(w, r, codeNativePaneScope, "this pane is already attached")
				return
			}
		}
		if info.kind == authCookie && isMutation(r.Method) && r.Header.Get("X-DF-Request") != "1" {
			s.refuse(w, r, codeOriginDenied, "a cookie mutation must send X-DF-Request: 1")
			return
		}
		if (info.kind == authCookie || info.kind == authSession) && info.scope == scopeRead && isMutation(r.Method) && !readOnlyMutationAllowed(r.URL.Path) {
			s.refuse(w, r, codeReadOnly, "this device was paired without write scope")
			return
		}
		if (info.kind == authCookie || info.kind == authSession) && !info.terminal && isTerminalRoute(r) && !isRedrawRoute(r) {
			s.refuse(w, r, codeTerminalRequired, "this device needs workspace access for the terminal")
			return
		}
		if info.renewToken != "" {
			http.SetCookie(w, sessionCookie(info.renewToken, int(browserTTL/time.Second)))
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authInfoKey{}, info)))
	}
}

type authKind int

const (
	authNone authKind = iota
	authBearer
	authCookie
	authSession
	authPane
)

type authInfo struct {
	kind       authKind
	scope      string
	deviceID   string
	deviceKey  string
	sessionKey string
	// Terminal is derived workspace access for compatibility with the older
	// gate name. A full-scope device has it; a read-only device does not. The
	// master bearer token always has it.
	terminal bool
	// renewToken is the raw cookie to re-issue when the idle window is close to
	// lapsing. The middleware sets it before the handler runs, which is also
	// safe for a stream that writes its own headers.
	renewToken string
	// Pane fields identify a session-scoped native pane capability: the
	// originating principal, the one session it may use, and the hashed key to
	// revoke when its stream closes.
	paneKey       string
	paneSession   string
	panePrincipal principal
}

type authInfoKey struct{}

func authFrom(r *http.Request) authInfo {
	info, _ := r.Context().Value(authInfoKey{}).(authInfo)
	if info.scope == "" {
		// Fail closed: a request that reached a handler without the auth
		// middleware is treated as the narrowest grant, not as full access.
		info.scope = scopeRead
	}
	return info
}

func normalizeScope(scope string) string {
	if scope == scopeFull {
		return scopeFull
	}
	return scopeRead
}

// scopeHasWorkspace is the single capability behind the terminal and the
// OpenCode write surface. They are not separable in practice: an OpenCode turn
// can run workspace actions, so a device trusted to chat is trusted to type.
// The old terminal flag stays in persisted rows and JSON for compatibility, but
// it is derived from scope instead of being an independent grant.
func scopeHasWorkspace(scope string) bool {
	return normalizeScope(scope) == scopeFull
}

func (s *Server) authenticate(r *http.Request) (authInfo, bool) {
	// A paired-device token can arrive either as the HttpOnly cookie (browser)
	// or as a bearer token (native client). Both are the same 256-bit hashed
	// row, so scope, workspace access, expiry and revocation stay in one place.
	rawCredential := ""
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(header, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if secureEqual(token, s.cfg.Token) {
			return authInfo{kind: authBearer, scope: scopeFull, terminal: true}, true
		}
		if token != "" {
			if grant, ok := s.paneFor(token); ok {
				return authInfo{
					kind:          authPane,
					scope:         scopeFull,
					terminal:      true,
					paneKey:       s.hashSecret(token),
					paneSession:   grant.session,
					panePrincipal: grant.principal,
				}, true
			}
			key := s.hashSecret(token)
			now := time.Now()
			s.deviceMu.Lock()
			session, ok := s.deviceSessions[key]
			if ok && now.After(session.ExpiresAt) {
				delete(s.deviceSessions, key)
				ok = false
			}
			s.deviceMu.Unlock()
			if ok {
				return authInfo{
					kind:       authSession,
					scope:      normalizeScope(session.Scope),
					deviceID:   session.DeviceID,
					sessionKey: key,
					terminal:   scopeHasWorkspace(session.Scope),
				}, true
			}
			rawCredential = token
		}
	}
	if rawCredential == "" {
		cookie, err := r.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			return authInfo{}, false
		}
		rawCredential = cookie.Value
	}
	key := s.hashSecret(rawCredential)
	now := time.Now()
	var info authInfo
	var persist *store.Browser
	var expired bool
	s.openMu.Lock()
	device, ok := s.browsers[key]
	if ok && device.SecretTag != s.secretTag() {
		delete(s.browsers, key)
		expired = true
		ok = false
	}
	if ok {
		// Normalise old rows before the idle check: a zero timestamp must not be
		// a way to skip it.
		if device.Scope == "" {
			device.Scope = scopeFull
		} else if device.Scope != scopeFull && device.Scope != scopeRead {
			// Unknown scopes fail closed rather than becoming full control.
			device.Scope = scopeRead
		}
		if device.ID == "" {
			device.ID = id.New()
		}
		if device.Label == "" {
			device.Label = "Browser"
		}
		if device.CreatedAt.IsZero() {
			device.CreatedAt = now.UTC()
		}
		if device.LastSeenAt.IsZero() {
			device.LastSeenAt = now.UTC()
		}
		expired = now.After(device.ExpiresAt) ||
			now.Sub(device.LastSeenAt) > browserTTL ||
			now.Sub(device.CreatedAt) > browserMaxTTL
		if expired {
			delete(s.browsers, key)
			ok = false
		}
	}
	if ok {
		idle := now.Sub(device.LastSeenAt)
		renew := idle >= browserRenewInterval
		if idle > time.Minute {
			device.LastSeenAt = now.UTC()
			if renew {
				device.ExpiresAt = now.UTC().Add(browserTTL)
			}
			snapshot := device
			persist = &snapshot
		}
		s.browsers[key] = device
		info = authInfo{
			kind:      authCookie,
			scope:     normalizeScope(device.Scope),
			deviceID:  device.ID,
			deviceKey: key,
			terminal:  scopeHasWorkspace(device.Scope),
		}
		if renew {
			// Sliding idle window, hard-capped by CreatedAt. Issued by the
			// middleware so a handler can still write its own headers. A native
			// bearer client ignores the Set-Cookie; its token stays the same.
			info.renewToken = rawCredential
		}
	}
	s.openMu.Unlock()
	if expired && s.store != nil {
		if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("delete expired browser", "error", err)
		}
	}
	if persist != nil && s.store != nil {
		// Do not resurrect a row revoked between the auth lookup and this
		// write. The map is the authority; the store is only its mirror.
		s.openMu.Lock()
		current, still := s.browsers[key]
		s.openMu.Unlock()
		if still && current.ID == persist.ID && current.LastSeenAt.Equal(persist.LastSeenAt) {
			if err := s.store.PutBrowser(key, *persist); err != nil {
				s.log.Warn("touch browser session", "error", err)
			}
		}
	}
	if !ok {
		return authInfo{}, false
	}
	return info, true
}

// loadBrowsers restores hashed browser sessions after a daemon restart, so an
// open tab reconnects without another `darkfactory open`. Expired rows are
// removed once at startup; rows written before device metadata existed are
// upgraded to a full-scope device instead of being silently discarded.
func (s *Server) loadBrowsers() {
	if s.store == nil {
		return
	}
	saved, err := s.store.ListBrowsers()
	if err != nil {
		s.log.Error("load browser sessions", "error", err)
		return
	}
	now := time.Now().UTC()
	secretTag := s.secretTag()
	for key, device := range saved {
		if device.SecretTag != secretTag || device.KeyVersion != browserKeyVersion {
			// The key was derived differently, so this row can never be matched
			// again. Delete it instead of listing a phantom device.
			if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete browser session with an old key", "error", err)
			}
			continue
		}
		if now.After(device.ExpiresAt) {
			if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete expired browser", "error", err)
			}
			continue
		}
		upgraded := false
		if device.ID == "" {
			device.ID = id.New()
			upgraded = true
		}
		if device.Label == "" {
			device.Label = "Browser"
			upgraded = true
		}
		if device.Scope == "" {
			device.Scope = scopeFull
			upgraded = true
		} else if device.Scope != scopeFull && device.Scope != scopeRead {
			device.Scope = scopeRead
			upgraded = true
		}
		if device.CreatedAt.IsZero() {
			device.CreatedAt = now
			upgraded = true
		}
		if device.LastSeenAt.IsZero() {
			device.LastSeenAt = now
			upgraded = true
		}
		// Idle expiry and the absolute cap apply on load, not only on request:
		// a daemon that was off for longer than the window must not resurrect a
		// stale session the moment it starts.
		if now.Sub(device.LastSeenAt) > browserTTL || now.Sub(device.CreatedAt) > browserMaxTTL {
			if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete idle browser", "error", err)
			}
			continue
		}
		s.browsers[key] = device
		if upgraded {
			if err := s.store.PutBrowser(key, device); err != nil {
				s.log.Warn("upgrade browser session", "error", err)
			}
		}
	}
	if len(s.browsers) > 0 {
		s.log.Info("loaded browser sessions", "count", len(s.browsers))
	}
}

func (s *Server) loadDeviceKeys() {
	if s.store == nil {
		return
	}
	saved, err := s.store.ListDeviceKeys()
	if err != nil {
		s.log.Error("load device keys", "error", err)
		return
	}
	now := time.Now().UTC()
	secretTag := s.secretTag()
	for id, key := range saved {
		if key.SecretTag != secretTag || key.Algorithm != "ES256" || key.ExpiresAt.IsZero() || now.After(key.ExpiresAt) {
			if err := s.store.DeleteDeviceKey(id); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete unusable device key", "error", err)
			}
			continue
		}
		if key.Scope == "" {
			key.Scope = scopeFull
			if err := s.store.PutDeviceKey(key); err != nil {
				s.log.Warn("upgrade device key", "error", err)
			}
		} else if key.Scope != scopeFull && key.Scope != scopeRead {
			key.Scope = scopeRead
			if err := s.store.PutDeviceKey(key); err != nil {
				s.log.Warn("upgrade device key", "error", err)
			}
		}
		s.deviceKeys[id] = key
	}
	if len(s.deviceKeys) > 0 {
		s.log.Info("loaded device keys", "count", len(s.deviceKeys))
	}
}

func (s *Server) pruneBrowsers(now time.Time) {
	var expired []string
	s.openMu.Lock()
	secretTag := s.secretTag()
	for key, device := range s.browsers {
		if device.SecretTag != secretTag ||
			now.After(device.ExpiresAt) ||
			now.Sub(device.LastSeenAt) > browserTTL ||
			now.Sub(device.CreatedAt) > browserMaxTTL {
			delete(s.browsers, key)
			expired = append(expired, key)
		}
	}
	s.openMu.Unlock()
	if s.store == nil {
		return
	}
	for _, key := range expired {
		if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("delete expired browser", "error", err)
		}
	}
}

func (s *Server) hostAllowed(r *http.Request) bool {
	host := hostOnly(r.Host)
	if host == "" {
		return false
	}
	if isLoopbackHost(host) || strings.EqualFold(host, "localhost") {
		return true
	}
	if listenHost := hostOnly(s.cfg.Listen); listenHost != "" && strings.EqualFold(host, listenHost) {
		return true
	}
	if publicHost := publicURLHost(s.cfg.PublicURL); publicHost != "" && strings.EqualFold(host, publicHost) {
		return true
	}
	for _, allowed := range s.cfg.AllowedHosts {
		if strings.EqualFold(host, hostOnly(allowed)) {
			return true
		}
	}
	return false
}

func sameHost(a, b string) bool {
	return strings.EqualFold(hostOnly(a), hostOnly(b))
}

// sameOrigin is stricter than sameHost: it also compares the port when both
// sides name one. A page served from another loopback port is the same site but
// not the daemon's origin, and SameSite cookies do not separate them.
func sameOrigin(origin string, r *http.Request) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if !sameHost(parsed.Host, r.Host) {
		return false
	}
	originPort := parsed.Port()
	requestPort := ""
	if _, port, err := net.SplitHostPort(r.Host); err == nil {
		requestPort = port
	}
	return originPort == "" || requestPort == "" || originPort == requestPort
}

// sameSiteRequest uses browser fetch metadata to refuse a request the browser
// says came from another origin. Native clients send no Sec-Fetch-Site, so an
// absent header is allowed; only a present, non-same-origin value is refused.
// This is what separates two loopback ports, where SameSite=Strict does not.
func sameSiteRequest(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

func hostOnly(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func publicURLHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func listenIsLoopback(addr string) bool {
	host := hostOnly(addr)
	if host == "" {
		return false
	}
	return isLoopbackHost(host)
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return isLoopbackHost(strings.Trim(host, "[]"))
}

func isMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// A read-only device may log out and ask a full-screen PTY for a redraw; the
// redraw only nudges SIGWINCH, it does not write terminal input. Resize stays
// blocked because it would change the size of the one PTY another client owns.
func readOnlyMutationAllowed(path string) bool {
	return path == "/api/v1/open/logout" || strings.HasSuffix(path, "/redraw")
}

// isTerminalRoute is the PTY surface: creating a session, reading its bytes,
// typing into it, resizing it, or asking it to repaint. Control-plane verbs
// (state, runs, approvals, OpenCode, stop) stay available to a device that was
// paired without terminal access.
func isTerminalRoute(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/v1/sessions") {
		return false
	}
	if path == "/api/v1/sessions" {
		return r.Method == http.MethodPost
	}
	switch {
	case strings.HasSuffix(path, "/native"),
		strings.HasSuffix(path, "/stream"),
		strings.HasSuffix(path, "/input"),
		strings.HasSuffix(path, "/resize"),
		strings.HasSuffix(path, "/redraw"):
		return true
	}
	return false
}

// isRedrawRoute stays available to a read-only device: it only asks the PTY to
// repaint with its current size, while every byte-carrying route stays gated.
func isRedrawRoute(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, "/redraw")
}

func secretHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// hashSecret is the at-rest key for anything that grants access: an in-memory
// pair/open code or a persisted browser session. Mixing the instance secret in
// means a state.db restored without ~/.config/darkfactory/instance-secret
// cannot be turned back into working cookies (DP-2). The separator is safe
// because the secret is hex and the value is opaque.
func (s *Server) hashSecret(value string) string {
	return secretHash(s.cfg.InstanceSecret + "|" + value)
}

// secretTag identifies the instance secret without storing it. A row minted
// under a different tag cannot be authenticated, so it is dropped rather than
// listed.
func (s *Server) secretTag() string {
	return secretHash(s.cfg.InstanceSecret)[:16]
}

// hostID is a stable, non-secret identifier for this daemon. A native client
// uses it to keep worktree and session IDs host-scoped; it changes when the
// instance secret is rotated, which is the same event that revokes device rows.
func (s *Server) hostID() string {
	return s.hashSecret("host-id")[:32]
}

// sessionCookie is the only cookie shape the daemon sets. `__Host-` requires
// Secure, Path=/ and no Domain. Chromium accepts a Secure cookie from
// http://127.0.0.1 (a trustworthy origin); WebKitGTK did not in the 2026-11-01
// probe, so the optional desktop shell cannot rely on this cookie over plain
// http. Do not weaken the flags to make a WebView work.
func sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
		MaxAge:   maxAge,
	}
}

func secureEqual(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

type openCodeResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type pairCodeResponse struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
	Label     string    `json:"label"`
	Scope     string    `json:"scope"`
	Terminal  bool      `json:"terminal"`
}

func browserOrigin(r *http.Request) string {
	scheme := "http"
	if isSecureRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if hostOnly(host) == "0.0.0.0" || hostOnly(host) == "::" {
		_, port, err := net.SplitHostPort(host)
		if err != nil {
			port = "8787"
		}
		host = net.JoinHostPort("127.0.0.1", port)
	}
	return scheme + "://" + host
}

func (s *Server) pairOrigin(r *http.Request) string {
	if origin := strings.TrimRight(strings.TrimSpace(s.cfg.PublicURL), "/"); origin != "" {
		return origin
	}
	return browserOrigin(r)
}

func (s *Server) handleOpenCode(w http.ResponseWriter, r *http.Request) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		s.fail(w, r, err)
		return
	}
	code := hex.EncodeToString(raw[:])
	expiry := time.Now().UTC().Add(openCodeTTL)
	s.openMu.Lock()
	now := time.Now()
	for key, expires := range s.openCodes {
		if now.After(expires) {
			delete(s.openCodes, key)
		}
	}
	s.openCodes[s.hashSecret(code)] = expiry
	s.openMu.Unlock()

	writeJSON(w, http.StatusOK, openCodeResponse{
		URL:       browserOrigin(r) + "/#code=" + code,
		ExpiresAt: expiry,
	})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) || !s.hostAllowed(r) {
		s.refuse(w, r, codeOpenHostOnly, "")
		return
	}
	if !sameSiteRequest(r) {
		s.refuse(w, r, codeOriginDenied, "cross-site requests are refused")
		return
	}
	if r.Header.Get("X-DF-Request") != "1" {
		s.refuse(w, r, codeOriginDenied, "a mutation must send X-DF-Request: 1")
		return
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if !sameOrigin(origin, r) {
			s.refuse(w, r, codeOriginDenied, "origin does not match the request host")
			return
		}
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		s.refuse(w, r, codeOpenInvalid, "")
		return
	}
	key := s.hashSecret(code)
	s.openMu.Lock()
	expiry, ok := s.openCodes[key]
	if ok {
		delete(s.openCodes, key)
	}
	s.openMu.Unlock()
	if !ok || time.Now().After(expiry) {
		s.refuse(w, r, codeOpenInvalid, "")
		return
	}
	_, expiry, err := s.mintBrowserSession(w, r, "Local browser", scopeFull)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expiresAt": expiry})
}

// handlePairCode is host-initiated: an already authenticated browser asks for
// a short one-time code. The code is hashed in memory, never logged, and is
// carried to the phone in the URL fragment. The label and scope are chosen by
// the host browser and travel with the grant, not with the phone's request.
func (s *Server) handlePairCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label    string `json:"label"`
		Scope    string `json:"scope"`
		Terminal bool   `json:"terminal"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "Phone"
	}
	if len(label) > 80 {
		s.refuse(w, r, codeInvalidRequest, "label is longer than 80 bytes")
		return
	}
	scope := strings.TrimSpace(body.Scope)
	if scope == "" {
		scope = scopeFull
	}
	if scope != scopeFull && scope != scopeRead {
		s.refuse(w, r, codeInvalidScope, "")
		return
	}

	code, err := newPairCode()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	expiry := time.Now().UTC().Add(pairCodeTTL)
	s.openMu.Lock()
	now := time.Now()
	for key, grant := range s.pairCodes {
		if now.After(grant.ExpiresAt) {
			delete(s.pairCodes, key)
		}
	}
	s.pairCodes[s.hashSecret(normalizePairCode(code))] = pairGrant{ExpiresAt: expiry, Label: label, Scope: scope}
	s.openMu.Unlock()

	writeJSON(w, http.StatusOK, pairCodeResponse{
		Code:      code,
		URL:       s.pairOrigin(r) + "/#pair=" + url.QueryEscape(code),
		ExpiresAt: expiry,
		Label:     label,
		Scope:     scope,
		Terminal:  scopeHasWorkspace(scope),
	})
}

// handlePair is intentionally unauthenticated but code-gated. It is the only
// cookie-minting route allowed off loopback, and only over HTTPS or behind an
// HTTPS proxy; plain-HTTP remote pairing is refused even with --allow-remote.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r) {
		s.refuse(w, r, codeOriginDenied, "host is not in the allowlist")
		return
	}
	if !sameSiteRequest(r) {
		s.refuse(w, r, codeOriginDenied, "cross-site requests are refused")
		return
	}
	if !listenIsLoopback(s.cfg.Listen) && r.TLS == nil {
		s.refuse(w, r, codePairHTTPSOnly, "")
		return
	}
	if !isLoopbackRequest(r) && !isSecureRequest(r) {
		s.refuse(w, r, codePairHTTPSOnly, "")
		return
	}
	// A reverse proxy on loopback still represents a remote client. If it
	// forwarded the request but did not terminate TLS, refuse pairing.
	if strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "" && !isSecureRequest(r) {
		s.refuse(w, r, codePairHTTPSOnly, "a forwarded pairing request must arrive over HTTPS")
		return
	}
	if r.Header.Get("X-DF-Request") != "1" {
		s.refuse(w, r, codeOriginDenied, "a mutation must send X-DF-Request: 1")
		return
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if !sameOrigin(origin, r) {
			s.refuse(w, r, codeOriginDenied, "origin does not match the request host")
			return
		}
	}
	if !s.allowPair(r) {
		s.refuse(w, r, codePairRateLimited, "")
		return
	}
	var body struct {
		Code            string `json:"code"`
		ClientPublicKey string `json:"clientPublicKey"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	var clientPublic *ecdh.PublicKey
	clientPublicB64 := ""
	if strings.TrimSpace(body.ClientPublicKey) != "" {
		if s.e2ee == nil {
			s.refuse(w, r, codeE2EEUnavailable, "")
			return
		}
		parsed, canonical, err := parseClientE2EEKey(body.ClientPublicKey)
		if err != nil {
			s.refuse(w, r, codeInvalidRequest, err.Error())
			return
		}
		clientPublic = parsed
		clientPublicB64 = canonical
	}
	code := normalizePairCode(body.Code)
	if len(code) != 8 {
		s.refuse(w, r, codePairInvalid, "")
		return
	}
	key := s.hashSecret(code)
	s.openMu.Lock()
	grant, ok := s.pairCodes[key]
	if ok {
		delete(s.pairCodes, key)
	}
	s.openMu.Unlock()
	if !ok || time.Now().After(grant.ExpiresAt) {
		s.refuse(w, r, codePairInvalid, "")
		return
	}
	// A sealed native pairing must not leak the same token in a Set-Cookie
	// header; only the legacy browser/native response gets the cookie.
	token, deviceID, expiry, err := s.mintBrowserSessionWithKey(
		w, r, grant.Label, grant.Scope, clientPublicB64, clientPublic == nil,
	)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response := map[string]any{
		"ok":        true,
		"hostId":    s.hostID(),
		"deviceId":  deviceID,
		"expiresAt": expiry,
		"scope":     normalizeScope(grant.Scope),
		"terminal":  scopeHasWorkspace(grant.Scope),
	}
	if clientPublic != nil {
		_, s2c, err := deriveE2EEKeys(s.e2ee.private, clientPublic, s.e2ee.publicB64, clientPublicB64)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		sealed, err := sealE2EECombined(
			s2c,
			[]byte(token),
			e2eeAAD("pair", s.hostID(), deviceID, clientPublicB64),
		)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		response["e2ee"] = e2eeVersion
		response["hostPublicKey"] = s.e2ee.publicB64
		response["fingerprint"] = s.e2ee.fingerprint
		response["sealedToken"] = sealed
	} else {
		response["token"] = token
	}
	writeJSON(w, http.StatusOK, response)
}

func newPairCode() (string, error) {
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return code[:4] + "-" + code[4:], nil
}

func normalizePairCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func (s *Server) allowPair(r *http.Request) bool {
	now := time.Now()
	source := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		source = host
	}
	s.openMu.Lock()
	defer s.openMu.Unlock()
	window := s.pairSources[source]
	if !window.allow(now, pairSourceLimit) {
		return false
	}
	s.pairSources[source] = window
	// The global budget is spent only after the per-source check, so one
	// source cannot exhaust the shared allowance by itself.
	if !s.pairGlobal.allow(now, pairGlobalLimit) {
		return false
	}
	if len(s.pairSources) > 256 {
		for key, entry := range s.pairSources {
			if now.Sub(entry.Start) >= pairRateInterval {
				delete(s.pairSources, key)
			}
		}
	}
	return true
}

// allowE2EE bounds the unauthenticated encrypted-envelope route. The route is
// the phone's whole API, so the budget is sized for normal polling; it exists
// to stop a caller from forcing unbounded JSON, ECDH and AEAD work.
func (s *Server) allowE2EE(r *http.Request) bool {
	now := time.Now()
	source := r.RemoteAddr
	if host, _, err := net.SplitHostPort(source); err == nil {
		source = host
	}
	s.e2eeLimitMu.Lock()
	defer s.e2eeLimitMu.Unlock()
	if s.e2eeSources == nil {
		s.e2eeSources = make(map[string]pairWindow)
	}
	window := s.e2eeSources[source]
	if !window.allow(now, e2eeSourceLimit) {
		return false
	}
	s.e2eeSources[source] = window
	if !s.e2eeGlobal.allow(now, e2eeGlobalLimit) {
		return false
	}
	if len(s.e2eeSources) > 256 {
		for key, entry := range s.e2eeSources {
			if now.Sub(entry.Start) >= pairRateInterval {
				delete(s.e2eeSources, key)
			}
		}
	}
	return true
}

func (s *Server) mintBrowserSession(w http.ResponseWriter, r *http.Request, label, scope string) (string, time.Time, error) {
	token, _, expiry, err := s.mintBrowserSessionWithKey(w, r, label, scope, "", true)
	return token, expiry, err
}

func (s *Server) mintBrowserSessionWithKey(w http.ResponseWriter, r *http.Request, label, scope, e2eePublicKey string, setCookie bool) (string, string, time.Time, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", time.Time{}, err
	}
	if scope != scopeRead {
		scope = scopeFull
	}
	if strings.TrimSpace(label) == "" {
		label = "Browser"
	}
	now := time.Now().UTC()
	sessionToken := hex.EncodeToString(raw[:])
	sessionKey := s.hashSecret(sessionToken)
	sessionExpiry := now.Add(browserTTL)
	device := store.Browser{
		ID:            id.New(),
		Label:         label,
		Scope:         scope,
		CreatedAt:     now,
		LastSeenAt:    now,
		ExpiresAt:     sessionExpiry,
		KeyVersion:    browserKeyVersion,
		SecretTag:     s.secretTag(),
		Terminal:      scopeHasWorkspace(scope),
		E2EEPublicKey: e2eePublicKey,
	}
	s.pruneBrowsers(time.Now())
	if s.store != nil {
		if err := s.store.PutBrowser(sessionKey, device); err != nil {
			return "", "", time.Time{}, err
		}
	}
	s.openMu.Lock()
	s.browsers[sessionKey] = device
	s.openMu.Unlock()

	if setCookie {
		http.SetCookie(w, sessionCookie(sessionToken, int(browserTTL/time.Second)))
	}
	return sessionToken, device.ID, sessionExpiry, nil
}

func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	info := authFrom(r)
	// A browser cookie and a native bearer token resolve to the same hashed
	// Browser row. Logout ends that row, not just the cookie's transport.
	key := info.deviceKey
	if key == "" {
		if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
			key = s.hashSecret(cookie.Value)
		}
	}
	if key != "" {
		s.openMu.Lock()
		delete(s.browsers, key)
		s.openMu.Unlock()
		if s.store != nil {
			if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete browser", "error", err)
			}
		}
	}
	if info.kind == authSession && info.sessionKey != "" {
		// End this tab's session, not every tab using the same device key.
		s.deviceMu.Lock()
		delete(s.deviceSessions, info.sessionKey)
		s.deviceMu.Unlock()
	}
	// A native pane is authority over a PTY. Logout ends it with the session
	// that minted it instead of waiting for the window to close.
	s.dropPanesFor(principalFromRequest(r))
	http.SetCookie(w, sessionCookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateDeviceKey registers a public-key credential for a desktop or
// web client. The request authenticates with the master token, a paired cookie,
// a paired bearer token, or an existing device session; the new key inherits
// that principal's scope and workspace access. The private key never reaches
// the daemon.
func (s *Server) handleCreateDeviceKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label     string `json:"label"`
		PublicKey string `json:"publicKey"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "Desktop"
	}
	if len(label) > 80 {
		s.refuse(w, r, codeInvalidRequest, "label is longer than 80 bytes")
		return
	}
	encoded := strings.TrimSpace(body.PublicKey)
	if encoded == "" || len(encoded) > 2048 {
		s.refuse(w, r, codeInvalidRequest, "publicKey must be a base64 SPKI key")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, "publicKey must be base64 SPKI")
		return
	}
	if _, err := parseDevicePublicKey(raw); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}

	info := authFrom(r)
	now := time.Now().UTC()
	key := store.DeviceKey{
		ID:         id.New(),
		Label:      label,
		Scope:      normalizeScope(info.scope),
		Terminal:   scopeHasWorkspace(info.scope),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(deviceKeyTTL),
		PublicKey:  encoded,
		Algorithm:  "ES256",
		SecretTag:  s.secretTag(),
	}
	s.deviceMu.Lock()
	s.deviceKeys[key.ID] = key
	s.deviceMu.Unlock()
	if s.store != nil {
		if err := s.store.PutDeviceKey(key); err != nil {
			s.deviceMu.Lock()
			delete(s.deviceKeys, key.ID)
			s.deviceMu.Unlock()
			s.fail(w, r, fmt.Errorf("store device key: %w", err))
			return
		}
	}
	setAuditTarget(r, key.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"deviceId":  key.ID,
		"label":     key.Label,
		"scope":     key.Scope,
		"terminal":  key.Terminal,
		"expiresAt": key.ExpiresAt,
	})
}

// handleDeviceChallenge issues a one-use nonce for a registered key. It is
// unauthenticated by design: the signature that follows is the credential.
// A missing or expired device gets the same 401 as a bad signature.
func (s *Server) handleDeviceChallenge(w http.ResponseWriter, r *http.Request) {
	if !s.sameDeviceOrigin(r) || r.Header.Get("X-DF-Request") != "1" {
		s.refuse(w, r, codeOriginDenied, "use the daemon's own origin and send X-DF-Request: 1")
		return
	}
	if !s.allowDeviceAuth(r) {
		s.refuse(w, r, codeDeviceRateLimited, "")
		return
	}
	var body struct {
		DeviceID string `json:"deviceId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	deviceID := strings.TrimSpace(body.DeviceID)
	if !openCodeID.MatchString(deviceID) {
		s.refuse(w, r, codeInvalidID, "invalid device id")
		return
	}
	now := time.Now().UTC()
	s.deviceMu.Lock()
	key, ok := s.deviceKeys[deviceID]
	if !ok || key.Algorithm != "ES256" || key.SecretTag != s.secretTag() || now.After(key.ExpiresAt) {
		s.deviceMu.Unlock()
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	// Keep other outstanding nonces: two tabs can launch together and each
	// needs its own challenge. Only expired grants are removed here.
	for challenge, grant := range s.deviceChallenges {
		if now.After(grant.ExpiresAt) {
			delete(s.deviceChallenges, challenge)
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		s.deviceMu.Unlock()
		s.fail(w, r, err)
		return
	}
	challenge := hex.EncodeToString(raw[:])
	expiry := now.Add(deviceChallengeTTL)
	s.deviceChallenges[challenge] = deviceChallenge{DeviceID: deviceID, ExpiresAt: expiry}
	if len(s.deviceChallenges) > 1024 {
		for value, grant := range s.deviceChallenges {
			if now.After(grant.ExpiresAt) {
				delete(s.deviceChallenges, value)
			}
		}
	}
	s.deviceMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"challenge": challenge, "expiresAt": expiry})
}

// handleDeviceVerify consumes a challenge and checks an ES256 signature over
// the device id and nonce. A success returns a short-lived session token; the
// client keeps it in sessionStorage and re-signs on the next launch.
func (s *Server) handleDeviceVerify(w http.ResponseWriter, r *http.Request) {
	if !s.sameDeviceOrigin(r) || r.Header.Get("X-DF-Request") != "1" {
		s.refuse(w, r, codeOriginDenied, "use the daemon's own origin and send X-DF-Request: 1")
		return
	}
	if !s.allowDeviceAuth(r) {
		s.refuse(w, r, codeDeviceRateLimited, "")
		return
	}
	var body struct {
		DeviceID  string `json:"deviceId"`
		Challenge string `json:"challenge"`
		Signature string `json:"signature"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	deviceID := strings.TrimSpace(body.DeviceID)
	challenge := strings.TrimSpace(body.Challenge)
	if !openCodeID.MatchString(deviceID) || challenge == "" || len(challenge) > 128 {
		s.refuse(w, r, codeInvalidRequest, "deviceId, challenge and signature are required")
		return
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.Signature))
	if err != nil || len(signature) != 64 {
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	now := time.Now().UTC()
	s.deviceMu.Lock()
	grant, ok := s.deviceChallenges[challenge]
	if ok {
		// Consume even on a bad signature: one nonce, one attempt.
		delete(s.deviceChallenges, challenge)
	}
	key, keyOK := s.deviceKeys[deviceID]
	s.deviceMu.Unlock()
	if !ok || grant.DeviceID != deviceID || now.After(grant.ExpiresAt) || !keyOK ||
		key.Algorithm != "ES256" || key.SecretTag != s.secretTag() || now.After(key.ExpiresAt) {
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	pub, err := parseDevicePublicKeyFromBase64(key.PublicKey)
	if err != nil {
		s.log.Warn("device key is unreadable", "device", deviceID, "error", err)
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	if !verifyES256(pub, deviceAuthPayload(deviceID, challenge), signature) {
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	// A revoke can land while the signature is being checked. Re-read under the
	// lock and refuse if the key changed or disappeared, so an in-flight verify
	// cannot mint a session for a credential the user just deleted.
	s.deviceMu.Lock()
	current, still := s.deviceKeys[deviceID]
	if !still || current.PublicKey != key.PublicKey || current.Algorithm != "ES256" ||
		current.SecretTag != s.secretTag() || now.After(current.ExpiresAt) {
		s.deviceMu.Unlock()
		s.refuse(w, r, codeUnauthorized, "")
		return
	}
	current.LastSeenAt = now
	s.deviceKeys[deviceID] = current
	key = current
	s.deviceMu.Unlock()
	if s.store != nil {
		if err := s.store.PutDeviceKey(key); err != nil {
			s.log.Warn("update device key", "device", deviceID, "error", err)
		}
	}
	token, expiry, err := s.mintDeviceSession(deviceID, key.Scope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"scope":     normalizeScope(key.Scope),
		"terminal":  scopeHasWorkspace(key.Scope),
		"expiresAt": expiry,
	})
}

func (s *Server) mintDeviceSession(deviceID, scope string) (string, time.Time, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(raw[:])
	now := time.Now().UTC()
	expiry := now.Add(deviceSessionTTL)
	s.deviceMu.Lock()
	s.deviceSessions[s.hashSecret(token)] = deviceSession{
		DeviceID:  deviceID,
		Scope:     normalizeScope(scope),
		ExpiresAt: expiry,
	}
	if len(s.deviceSessions) > 1024 {
		for key, session := range s.deviceSessions {
			if now.After(session.ExpiresAt) {
				delete(s.deviceSessions, key)
			}
		}
	}
	s.deviceMu.Unlock()
	return token, expiry, nil
}

func (s *Server) revokeDeviceSessions(deviceID string) {
	s.deviceMu.Lock()
	for key, session := range s.deviceSessions {
		if session.DeviceID == deviceID {
			delete(s.deviceSessions, key)
		}
	}
	s.deviceMu.Unlock()
}

func (s *Server) sameDeviceOrigin(r *http.Request) bool {
	if !s.hostAllowed(r) {
		return false
	}
	if !sameSiteRequest(r) {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if !sameOrigin(origin, r) {
			return false
		}
	}
	return true
}

func (s *Server) allowDeviceAuth(r *http.Request) bool {
	now := time.Now()
	source := r.RemoteAddr
	if host, _, err := net.SplitHostPort(source); err == nil {
		source = host
	}
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	window := s.deviceSources[source]
	if !window.allow(now, deviceSourceLimit) {
		return false
	}
	s.deviceSources[source] = window
	if !s.deviceGlobal.allow(now, deviceGlobalLimit) {
		return false
	}
	if len(s.deviceSources) > 256 {
		for key, entry := range s.deviceSources {
			if now.Sub(entry.Start) >= pairRateInterval {
				delete(s.deviceSources, key)
			}
		}
	}
	return true
}

func deviceAuthPayload(deviceID, challenge string) []byte {
	return []byte("darkfactory-device-auth-v1\n" + deviceID + "\n" + challenge)
}

func parseDevicePublicKey(der []byte) (*ecdsa.PublicKey, error) {
	value, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("publicKey is not a valid SPKI key")
	}
	pub, ok := value.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("publicKey must be an ECDSA P-256 key")
	}
	return pub, nil
}

func parseDevicePublicKeyFromBase64(encoded string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return parseDevicePublicKey(der)
}

// verifyES256 accepts the raw r||s signature WebCrypto produces, not the
// ASN.1 form ecdsa.VerifyASN1 expects.
func verifyES256(pub *ecdsa.PublicKey, payload, signature []byte) bool {
	if len(signature) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if r.Sign() <= 0 || s.Sign() <= 0 {
		return false
	}
	digest := sha256.Sum256(payload)
	return ecdsa.Verify(pub, digest[:], r, s)
}

type deviceView struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind,omitempty"`
	Label      string    `json:"label"`
	Scope      string    `json:"scope"`
	Terminal   bool      `json:"terminal"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Current    bool      `json:"current"`
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	info := authFrom(r)
	now := time.Now()
	s.openMu.Lock()
	devices := make([]deviceView, 0, len(s.browsers))
	secretTag := s.secretTag()
	for _, device := range s.browsers {
		if device.SecretTag != secretTag || now.After(device.ExpiresAt) {
			continue
		}
		scope := normalizeScope(device.Scope)
		devices = append(devices, deviceView{
			ID:         device.ID,
			Label:      device.Label,
			Scope:      scope,
			Terminal:   scopeHasWorkspace(scope),
			CreatedAt:  device.CreatedAt,
			LastSeenAt: device.LastSeenAt,
			ExpiresAt:  device.ExpiresAt,
			Current:    device.ID != "" && device.ID == info.deviceID,
		})
	}
	s.openMu.Unlock()
	s.deviceMu.Lock()
	for id, key := range s.deviceKeys {
		if key.SecretTag != secretTag || now.After(key.ExpiresAt) {
			continue
		}
		devices = append(devices, deviceView{
			ID:         id,
			Kind:       "key",
			Label:      key.Label,
			Scope:      normalizeScope(key.Scope),
			Terminal:   scopeHasWorkspace(key.Scope),
			CreatedAt:  key.CreatedAt,
			LastSeenAt: key.LastSeenAt,
			ExpiresAt:  key.ExpiresAt,
			Current:    id == info.deviceID,
		})
	}
	s.deviceMu.Unlock()
	sort.Slice(devices, func(i, j int) bool { return devices[i].CreatedAt.Before(devices[j].CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleRevokeAllDevices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeepCurrent bool `json:"keepCurrent"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	info := authFrom(r)
	var keys []string
	var deviceIDs []string
	s.openMu.Lock()
	for key, device := range s.browsers {
		if body.KeepCurrent && device.ID != "" && device.ID == info.deviceID {
			continue
		}
		keys = append(keys, key)
		if device.ID != "" {
			deviceIDs = append(deviceIDs, device.ID)
		}
		delete(s.browsers, key)
	}
	s.openMu.Unlock()
	s.deviceMu.Lock()
	var keyIDs []string
	for id := range s.deviceKeys {
		if body.KeepCurrent && id == info.deviceID {
			continue
		}
		keyIDs = append(keyIDs, id)
		deviceIDs = append(deviceIDs, id)
		delete(s.deviceKeys, id)
	}
	s.deviceMu.Unlock()
	if s.store != nil {
		for _, key := range keys {
			if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete browser", "error", err)
			}
		}
		for _, id := range keyIDs {
			if err := s.store.DeleteDeviceKey(id); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete device key", "error", err)
			}
		}
	}
	for _, deviceID := range deviceIDs {
		s.revokeDeviceSessions(deviceID)
	}
	// Pane capabilities are PTY authority; a revoke-all takes them away with
	// the devices. KeepCurrent spares only the caller's own panes.
	if body.KeepCurrent {
		s.dropPanesExcept(principalFromRequest(r))
	} else {
		s.dropAllPanes()
	}
	if !body.KeepCurrent && info.kind == authCookie {
		http.SetCookie(w, sessionCookie("", -1))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": len(keys) + len(keyIDs)})
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.PathValue("id"))
	if !openCodeID.MatchString(deviceID) {
		s.refuse(w, r, codeInvalidID, "invalid device id")
		return
	}
	info := authFrom(r)
	var key string
	s.openMu.Lock()
	for candidate, device := range s.browsers {
		if device.ID == deviceID {
			key = candidate
			delete(s.browsers, candidate)
			break
		}
	}
	s.openMu.Unlock()
	if key == "" {
		s.deviceMu.Lock()
		_, keyFound := s.deviceKeys[deviceID]
		if keyFound {
			delete(s.deviceKeys, deviceID)
		}
		s.deviceMu.Unlock()
		if !keyFound {
			s.refuse(w, r, codeDeviceNotFound, "")
			return
		}
		if s.store != nil {
			if err := s.store.DeleteDeviceKey(deviceID); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("delete device key", "error", err)
			}
		}
		s.revokeDeviceSessions(deviceID)
		s.dropPanesFor(principal{Kind: principalHuman, ID: "device:" + deviceID})
		if info.kind == authCookie && info.deviceID == deviceID {
			http.SetCookie(w, sessionCookie("", -1))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.store != nil {
		if err := s.store.DeleteBrowser(key); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("delete browser", "error", err)
		}
	}
	if info.kind == authCookie && key == info.deviceKey {
		http.SetCookie(w, sessionCookie("", -1))
	}
	s.dropPanesFor(principal{Kind: principalHuman, ID: "device:" + deviceID})
	w.WriteHeader(http.StatusNoContent)
}

type sessionView struct {
	model.Session
	Live bool `json:"live"`
}

type agentView struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	EnvKeys []string `json:"envKeys,omitempty"`
}

type stateResponse struct {
	HostID string `json:"hostId"`
	Build  string `json:"build"`
	// UI is the browser shell embedded in this binary. A page compares it with
	// its own UI_VERSION and reloads once when a restart left it on the
	// previous generation.
	UI          string               `json:"ui,omitempty"`
	Repos       []model.Repo         `json:"repos"`
	Worktrees   []model.Worktree     `json:"worktrees"`
	Sessions    []sessionView        `json:"sessions"`
	Runs        []model.Run          `json:"runs"`
	Agents      map[string]agentView `json:"agents"`
	OpenCodeURL string               `json:"opencodeURL,omitempty"`
	OpenCode    bool                 `json:"opencode"`
	Scope       string               `json:"scope"`
	Terminal    bool                 `json:"terminal"`
	// NativeTerminal tells the local UI whether the optional Ghostty window can
	// work on this host. The browser terminal does not depend on it.
	NativeTerminal nativeTerminalView `json:"nativeTerminal"`
	Device         *deviceView        `json:"device,omitempty"`
}

// redactAgents exposes what a preset runs and which env keys it sets, never the
// values: a secret must not reach the state API, a log, or a device.
func redactAgents(presets map[string]config.AgentPreset) map[string]agentView {
	out := make(map[string]agentView, len(presets))
	for name, preset := range presets {
		keys := make([]string, 0, len(preset.Env))
		for key := range preset.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out[name] = agentView{Command: preset.Command, Args: preset.Args, EnvKeys: keys}
	}
	return out
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	repos, err := s.store.ListRepos()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	worktrees, err := s.store.ListWorktrees()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A repo can become a git repo after it was added, so derive this instead
	// of trusting the stored flag.
	for i := range repos {
		repos[i].Git = isGitDir(repos[i].Path)
	}
	// Encode empty collections as [] rather than null so a client can iterate
	// without a special case.
	if repos == nil {
		repos = []model.Repo{}
	}
	if worktrees == nil {
		worktrees = []model.Worktree{}
	}
	stored, err := s.store.ListSessions()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	live := make(map[string]*session.Session)
	for _, sess := range s.sessions.List() {
		live[sess.ID] = sess
	}
	views := make([]sessionView, 0, len(stored)+len(live))
	seen := make(map[string]bool, len(live))
	now := time.Now().UTC()
	for _, item := range stored {
		if sess, ok := live[item.ID]; ok {
			current := sess.Model()
			// The live session struct carries runtime state only. Pin, drag
			// order and the client's idempotency key live on the stored row, so
			// copying them back stops a status sync from erasing them.
			current.Pinned = item.Pinned
			current.Position = item.Position
			current.ClientRequestID = item.ClientRequestID
			if item.Status != current.Status || item.PID != current.PID || item.Reason != current.Reason {
				if _, err := s.store.PutSession(current); err != nil {
					s.log.Error("sync session row", "session", item.ID, "error", err)
				}
			}
			views = append(views, sessionView{Session: current, Live: current.Status == model.SessionRunning})
			seen[item.ID] = true
			continue
		}
		if item.Status == model.SessionRunning {
			item.Status = model.SessionLost
			item.Reason = "daemon_restart"
			if item.EndedAt == nil {
				item.EndedAt = &now
			}
			if _, err := s.store.PutSession(item); err != nil {
				s.log.Error("mark session lost", "session", item.ID, "error", err)
			}
		}
		views = append(views, sessionView{Session: item, Live: false})
	}
	for id, sess := range live {
		if seen[id] {
			continue
		}
		current := sess.Model()
		views = append(views, sessionView{Session: current, Live: current.Status == model.SessionRunning})
	}
	sort.SliceStable(views, func(i, j int) bool { return store.SessionLess(views[i].Session, views[j].Session) })
	runs, err := s.store.ListRuns()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if runs == nil {
		runs = []model.Run{}
	}
	info := authFrom(r)
	scope := info.scope
	var current *deviceView
	if info.kind == authCookie {
		s.openMu.Lock()
		if device, ok := s.browsers[info.deviceKey]; ok {
			deviceScope := normalizeScope(device.Scope)
			current = &deviceView{
				ID:         device.ID,
				Label:      device.Label,
				Scope:      deviceScope,
				Terminal:   scopeHasWorkspace(deviceScope),
				CreatedAt:  device.CreatedAt,
				LastSeenAt: device.LastSeenAt,
				ExpiresAt:  device.ExpiresAt,
				Current:    true,
			}
		}
		s.openMu.Unlock()
	}
	if info.kind == authSession && info.deviceID != "" {
		s.deviceMu.Lock()
		if key, ok := s.deviceKeys[info.deviceID]; ok {
			current = &deviceView{
				ID:         key.ID,
				Kind:       "key",
				Label:      key.Label,
				Scope:      normalizeScope(key.Scope),
				Terminal:   scopeHasWorkspace(key.Scope),
				CreatedAt:  key.CreatedAt,
				LastSeenAt: key.LastSeenAt,
				ExpiresAt:  key.ExpiresAt,
				Current:    true,
			}
		}
		s.deviceMu.Unlock()
	}
	if current != nil {
		scope = current.Scope
	}
	writeJSON(w, http.StatusOK, stateResponse{
		HostID:         s.hostID(),
		Build:          Build,
		UI:             web.Version,
		Repos:          repos,
		Worktrees:      worktrees,
		Sessions:       views,
		Runs:           runs,
		Agents:         redactAgents(s.cfg.Agents),
		OpenCodeURL:    s.cfg.OpenCodeURL,
		OpenCode:       s.openCodeEnabled(),
		Scope:          scope,
		Terminal:       info.terminal,
		NativeTerminal: s.nativeTerminalCapability(),
		Device:         current,
	})
}

type dirEntryView struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Git  bool   `json:"git"`
}

type dirsResponse struct {
	Path      string         `json:"path"`
	Parent    string         `json:"parent,omitempty"`
	Home      string         `json:"home"`
	Git       bool           `json:"git"`
	Entries   []dirEntryView `json:"entries"`
	Truncated bool           `json:"truncated"`
}

// handleListDirs backs the add-repo picker. It is read-only, bearer-gated and
// returns directories only: no file names, sizes or contents. The daemon runs
// with the user's uid, so this grants no authority the caller did not already
// have through POST /repos.
func (s *Server) handleListDirs(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		raw = home
		if raw == "" {
			raw = string(filepath.Separator)
		}
	}
	if home != "" {
		switch {
		case raw == "~":
			raw = home
		case strings.HasPrefix(raw, "~/"):
			raw = filepath.Join(home, raw[2:])
		}
	}
	if !filepath.IsAbs(raw) {
		s.refuse(w, r, codeInvalidPath, "path must be absolute")
		return
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		s.refuse(w, r, codeInvalidPath, err.Error())
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		s.refuse(w, r, codeNotADirectory, "")
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		s.refuse(w, r, codeInvalidPath, "cannot read directory")
		return
	}

	out := make([]dirEntryView, 0, len(entries))
	truncated := false
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		full := filepath.Join(path, name)
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			if target, statErr := os.Stat(full); statErr == nil && target.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		if len(out) == maxDirEntries {
			truncated = true
			break
		}
		out = append(out, dirEntryView{Name: name, Path: full, Git: isGitDir(full)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Git != out[j].Git {
			return out[i].Git
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	writeJSON(w, http.StatusOK, dirsResponse{
		Path:      path,
		Parent:    parent,
		Home:      home,
		Git:       isGitDir(path),
		Entries:   out,
		Truncated: truncated,
	})
}

func (s *Server) defaultContainment() string {
	mode := strings.TrimSpace(s.cfg.Containment)
	if mode == "" || mode == "auto" {
		if session.JailAvailable() || s.cfg.JailScrubEnv {
			return "jail"
		}
		return "off"
	}
	if mode == "jail" && !session.JailAvailable() && !s.cfg.JailScrubEnv {
		return "off"
	}
	return mode
}

func (s *Server) containmentFor(wt model.Worktree) string {
	if strings.TrimSpace(wt.Containment) != "" {
		mode := wt.Containment
		if mode == "jail" && !session.JailAvailable() && !s.cfg.JailScrubEnv {
			return "off"
		}
		return mode
	}
	return s.defaultContainment()
}

func (s *Server) homeFor(worktreeID string) string {
	if strings.TrimSpace(s.cfg.HomeRoot) == "" {
		return ""
	}
	return filepath.Join(s.cfg.HomeRoot, worktreeID)
}

// hiddenPathsFor is the jail's effective mask list. The daemon's own config and
// state are always hidden; JailHideCredentials adds credential/keyring/browser
// paths, and JailHiddenPaths adds operator-chosen paths. JailPathAllow removes
// exact paths from that list. An allow inside a masked parent is refused rather
// than silently left hidden, so a config mistake fails closed.
func (s *Server) hiddenPathsFor(containment, agent string) ([]string, error) {
	paths := []string{s.cfg.ConfigDir, s.cfg.StateDir}
	allowed := append([]string(nil), s.cfg.JailPathAllow...)
	if preset, ok := s.cfg.Agents[agent]; ok {
		allowed = append(allowed, preset.JailPathAllow...)
	}
	if containment == "jail" {
		if s.cfg.JailHideCredentials {
			paths = append(paths, session.CredentialHiddenPaths()...)
		}
		paths = append(paths, s.cfg.JailHiddenPaths...)
	}

	allow := make(map[string]struct{}, len(allowed))
	for _, path := range allowed {
		if strings.TrimSpace(path) != "" {
			allow[path] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, ok := allow[path]; ok {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, path := range allowed {
		for _, masked := range out {
			if path != masked && pathInside(path, masked) {
				return nil, fmt.Errorf("jailPathAllow %q is inside masked path %q; mask the exact path instead", path, masked)
			}
		}
	}
	return out, nil
}

func pathInside(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// jailEnvAllow is the union of the global and per-agent allow lists. It is only
// consumed by a scrubbed jail.
func (s *Server) jailEnvAllow(agent string) []string {
	names := append([]string(nil), s.cfg.JailEnvAllow...)
	if preset, ok := s.cfg.Agents[agent]; ok {
		names = append(names, preset.JailEnvAllow...)
	}
	seen := make(map[string]struct{}, len(names))
	out := names[:0]
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func isGitDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func (s *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	path, err := filepath.Abs(strings.TrimSpace(body.Path))
	if err != nil {
		s.refuse(w, r, codeInvalidPath, err.Error())
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		s.refuse(w, r, codeNotADirectory, "repo path is not a directory")
		return
	}
	// Store one repo row per resolved directory. Otherwise the same checkout
	// can appear twice in the fleet and produce duplicate worktree actions.
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		s.refuse(w, r, codeInvalidPath, err.Error())
		return
	}
	repos, err := s.store.ListRepos()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, existing := range repos {
		existingPath := existing.Path
		if resolved, err := filepath.EvalSymlinks(existingPath); err == nil {
			existingPath = resolved
		}
		if existingPath == path {
			s.refuseTarget(w, r, codeRepoExists, path, "this directory is already a repo")
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	if !validName(name) {
		s.refuse(w, r, codeInvalidRequest, "invalid repo name")
		return
	}
	gitRepo := isGitDir(path)
	repo, err := s.store.PutRepo(model.Repo{Name: name, Path: path, Git: gitRepo})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Every repo gets one in-place workspace at its own path. For a git repo
	// this is the checkout: sessions there share its files and agent history.
	// Isolated git worktrees are additional, optional rows.
	if _, err := s.store.PutWorktree(model.Worktree{
		RepoID:      repo.ID,
		Path:        path,
		Workspace:   true,
		Containment: s.defaultContainment(),
	}); err != nil {
		_ = s.store.DeleteRepo(repo.ID)
		s.fail(w, r, err)
		return
	}
	setAuditTarget(r, repo.ID)
	writeJSON(w, http.StatusCreated, repo)
}

func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	worktrees, err := s.store.ListWorktrees()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var linked []string
	for _, wt := range worktrees {
		if wt.RepoID == id && !wt.Workspace {
			linked = append(linked, wt.ID)
		}
	}
	if len(linked) > 0 {
		sort.Strings(linked)
		s.refuseTarget(w, r, codeRepoBusy, id, "repo has git worktrees: "+summarizeIDs(linked))
		return
	}
	// A workspace is the repo directory itself, so removing the repo removes
	// its workspace record and sessions. The directory and files stay.
	for _, wt := range worktrees {
		if wt.RepoID != id {
			continue
		}
		if err := s.cancelRunsForWorktree(wt.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.dropSessionsForWorktree(wt.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.store.DeleteWorktree(wt.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.store.DeleteRepo(id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReorderRepos sets the fleet order of repo groups. Like the worktree
// order, the ids a client sends are a preference: a repo it did not name keeps
// its age order and lands after the named ones, so a stale tab cannot lose a
// repo that was added mid-drag.
func (s *Server) handleReorderRepos(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 500 {
		s.refuseTarget(w, r, codeRepoOrderInvalid, "ids", "send between 1 and 500 repo ids")
		return
	}
	ordered, err := s.store.ReorderRepos(body.IDs)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRepoOrderInvalid):
			s.refuseTarget(w, r, codeRepoOrderInvalid, "ids",
				strings.TrimPrefix(err.Error(), store.ErrRepoOrderInvalid.Error()+": "))
		case errors.Is(err, store.ErrNotFound):
			s.refuseTarget(w, r, codeRepoNotFound, "fleet", "there are no repos to reorder")
		default:
			s.fail(w, r, err)
		}
		return
	}
	setAuditTarget(r, "fleet")
	writeJSON(w, http.StatusOK, struct {
		Repos []model.Repo `json:"repos"`
	}{Repos: ordered})
}

func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RepoID    string `json:"repoId"`
		Name      string `json:"name"`
		BaseRef   string `json:"baseRef"`
		Workspace bool   `json:"workspace"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	repo, err := s.store.GetRepo(body.RepoID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuseTarget(w, r, codeRepoNotFound, body.RepoID, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	// An in-place workspace is the repo's own checkout. For a git repo this is
	// how a session can reuse the checkout's agent history instead of starting
	// in a new linked worktree. There is no branch and no isolation.
	if body.Workspace {
		worktrees, err := s.store.ListWorktrees()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, wt := range worktrees {
			if wt.RepoID == repo.ID && wt.Workspace {
				s.refuseTarget(w, r, codeWorkspaceBusy, repo.ID, "workspace is already open")
				return
			}
		}
		wt, err := s.store.PutWorktree(model.Worktree{
			RepoID:      repo.ID,
			Path:        repo.Path,
			Workspace:   true,
			Containment: s.defaultContainment(),
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		setAuditTarget(r, wt.ID)
		writeJSON(w, http.StatusCreated, wt)
		return
	}
	if !isGitDir(repo.Path) {
		worktrees, err := s.store.ListWorktrees()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, wt := range worktrees {
			if wt.RepoID == repo.ID && wt.Workspace {
				s.refuseTarget(w, r, codeWorkspaceBusy, repo.ID, "this repo has no branches or extra worktrees")
				return
			}
		}
		wt, err := s.store.PutWorktree(model.Worktree{
			RepoID:      repo.ID,
			Path:        repo.Path,
			Workspace:   true,
			Containment: s.defaultContainment(),
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		setAuditTarget(r, wt.ID)
		writeJSON(w, http.StatusCreated, wt)
		return
	}
	name := strings.TrimSpace(body.Name)
	if !validName(name) {
		s.refuse(w, r, codeInvalidRequest, "invalid worktree name")
		return
	}
	baseRef := strings.TrimSpace(body.BaseRef)
	if baseRef == "" {
		baseRef = "HEAD"
	}
	// Keep the human-readable name, but add a short id so two repos with the
	// same display name cannot claim the same directory.
	if !validName(repo.Name) {
		s.refuseTarget(w, r, codeRepoNameUnsafe, repo.ID, "")
		return
	}
	wtID := id.New()
	path := filepath.Join(s.cfg.WorktreeRoot, repo.Name, name+"-"+wtID[:8])
	branch := worktreeBranchPrefix + name
	if err := gitwt.Create(r.Context(), repo.Path, path, branch, baseRef); err != nil {
		s.refuseTarget(w, r, codeWorktreeCreateFailed, repo.ID, err.Error())
		return
	}
	wt, err := s.store.PutWorktree(model.Worktree{
		ID: wtID, RepoID: repo.ID, Path: path, Branch: branch, BaseRef: baseRef,
		Containment: s.defaultContainment(),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	setAuditTarget(r, wt.ID)
	writeJSON(w, http.StatusCreated, wt)
}

// handleReorderWorktrees sets the fleet order of one repo's worktrees. The ids
// a client sends are a preference, not a census: a sibling it did not name keeps
// its age order and lands after the named ones, so a stale tab cannot lose a row
// and a worktree added mid-drag does not become unreachable.
func (s *Server) handleReorderWorktrees(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RepoID string   `json:"repoId"`
		IDs    []string `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	repoID := strings.TrimSpace(body.RepoID)
	if repoID == "" {
		s.refuseTarget(w, r, codeInvalidRequest, "repoId", "repoId is required")
		return
	}
	if _, err := s.store.GetRepo(repoID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuseTarget(w, r, codeRepoNotFound, repoID, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 500 {
		s.refuseTarget(w, r, codeWorktreeOrderInvalid, "ids", "send between 1 and 500 worktree ids")
		return
	}
	ordered, err := s.store.ReorderWorktrees(repoID, body.IDs)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.refuseTarget(w, r, codeRepoNotFound, repoID, "this repo has no worktrees")
		case errors.Is(err, store.ErrOrderInvalid):
			s.refuseTarget(w, r, codeWorktreeOrderInvalid, "ids",
				strings.TrimPrefix(err.Error(), store.ErrOrderInvalid.Error()+": "))
		default:
			s.fail(w, r, err)
		}
		return
	}
	setAuditTarget(r, repoID)
	writeJSON(w, http.StatusOK, struct {
		RepoID    string           `json:"repoId"`
		Worktrees []model.Worktree `json:"worktrees"`
	}{RepoID: repoID, Worktrees: ordered})
}

// handleReorderSessions sets the order of one workspace's sessions. Like the
// fleet order, the ids a client sends are a preference: a session it did not
// name keeps its display order and lands after the named ones, so a stale tab
// cannot lose a row and a session created mid-drag does not become unreachable.
func (s *Server) handleReorderSessions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorktreeID string   `json:"worktreeId"`
		IDs        []string `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	worktreeID := strings.TrimSpace(body.WorktreeID)
	if worktreeID == "" {
		s.refuseTarget(w, r, codeInvalidRequest, "worktreeId", "worktreeId is required")
		return
	}
	if _, err := s.store.GetWorktree(worktreeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuseTarget(w, r, codeWorktreeNotFound, worktreeID, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 500 {
		s.refuseTarget(w, r, codeSessionOrderInvalid, "ids", "send between 1 and 500 session ids")
		return
	}
	ordered, err := s.store.ReorderSessions(worktreeID, body.IDs)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.refuseTarget(w, r, codeWorktreeNotFound, worktreeID, "this workspace has no sessions")
		case errors.Is(err, store.ErrSessionOrderInvalid):
			s.refuseTarget(w, r, codeSessionOrderInvalid, "ids",
				strings.TrimPrefix(err.Error(), store.ErrSessionOrderInvalid.Error()+": "))
		default:
			s.fail(w, r, err)
		}
		return
	}
	setAuditTarget(r, worktreeID)
	writeJSON(w, http.StatusOK, struct {
		WorktreeID string          `json:"worktreeId"`
		Sessions   []model.Session `json:"sessions"`
	}{WorktreeID: worktreeID, Sessions: ordered})
}

// handlePinSession keeps a session at the top of its workspace. The body is
// explicit rather than a toggle so a retried request cannot flip the pin back.
func (s *Server) handlePinSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	if body.Pinned == nil {
		s.refuseTarget(w, r, codeInvalidRequest, "pinned", "send pinned as true or false")
		return
	}
	id := r.PathValue("id")
	session, err := s.store.SetSessionPinned(id, *body.Pinned)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuse(w, r, codeSessionNotFound, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	setAuditTarget(r, session.ID)
	writeJSON(w, http.StatusOK, session)
}

// handleRevealWorktree asks the desktop to show a worktree's directory. The
// path comes from the store, never from the request, so a client can only
// reveal a folder the daemon already knows; the caller must be on the daemon's
// machine, because the file manager opens there.
func (s *Server) handleRevealWorktree(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		s.refuse(w, r, codeRevealRemote, "")
		return
	}
	wt, err := s.store.GetWorktree(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuse(w, r, codeWorktreeNotFound, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	path := filepath.Clean(wt.Path)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		s.refuseTarget(w, r, codeRevealUnavailable, wt.ID, "the worktree directory is gone")
		return
	}
	if err := revealFolder(path); err != nil {
		if errors.Is(err, errNoFileManager) {
			s.refuse(w, r, codeRevealUnavailable, "")
			return
		}
		s.log.Error("reveal in file manager failed", "worktree", wt.ID, "error", err)
		s.refuse(w, r, codeRevealFailed, "")
		return
	}
	setAuditTarget(r, wt.ID)
	writeJSON(w, http.StatusOK, struct {
		Path string `json:"path"`
	}{Path: path})
}

func (s *Server) handleDeleteWorktree(w http.ResponseWriter, r *http.Request) {
	wt, err := s.store.GetWorktree(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuse(w, r, codeWorktreeNotFound, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	if wt.Workspace {
		s.refuseTarget(w, r, codeWorkspaceTied, wt.RepoID, "")
		return
	}
	repo, err := s.store.GetRepo(wt.RepoID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuseTarget(w, r, codeRepoNotFound, wt.RepoID, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	// Refuse before anything destructive happens: stopping sessions, cancelling
	// runs and dropping rows are all irreversible, and a refused delete must not
	// have already torn the session down. A worktree whose directory is gone has
	// nothing left to lose, and git refuses to forget one without --force, so
	// treat that case as forced.
	force := r.URL.Query().Get("force") == "1"
	if _, err := os.Stat(wt.Path); errors.Is(err, os.ErrNotExist) {
		force = true
	}
	if !force {
		if live := s.liveSessionIDs(wt.ID); len(live) > 0 {
			s.refuseTarget(w, r, codeWorktreeBusy, wt.ID, "worktree has live sessions ("+summarizeIDs(live)+")")
			return
		}
		dirty, err := gitwt.Dirty(r.Context(), wt.Path)
		if err != nil {
			s.refuseTarget(w, r, codeWorktreeRemoveFailed, wt.ID, "cannot verify the worktree is clean: "+err.Error())
			return
		}
		if len(dirty) > 0 {
			s.refuseTarget(w, r, codeWorktreeDirty, wt.ID, "worktree has uncommitted changes ("+summarizePaths(dirty)+")")
			return
		}
	}
	if err := s.cancelRunsForWorktree(wt.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	// Stop live sessions and drop their rows first: removing the worktree
	// would leave orphan PTYs in a deleted cwd.
	if err := s.dropSessionsForWorktree(wt.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := gitwt.Remove(r.Context(), repo.Path, wt.Path, force); err != nil {
		s.refuseTarget(w, r, codeWorktreeRemoveFailed, wt.ID, err.Error())
		return
	}
	if err := s.store.DeleteWorktree(wt.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// liveSessionIDs names the running PTYs in a worktree, so a refusal can say
// what it would have stopped.
func (s *Server) liveSessionIDs(worktreeID string) []string {
	var ids []string
	for _, sess := range s.sessions.List() {
		if sess.WorktreeID == worktreeID && sess.Status() == model.SessionRunning {
			ids = append(ids, sess.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// summarizeIDs and summarizePaths keep a refusal readable: the first few items
// say what is at stake, a count says how much more there is.
func summarizeIDs(ids []string) string {
	short := make([]string, 0, len(ids))
	for _, value := range ids {
		if len(value) > 8 {
			value = value[:8]
		}
		short = append(short, value)
	}
	return summarize(short)
}

func summarizePaths(paths []string) string {
	return summarize(paths)
}

func summarize(items []string) string {
	const limit = 3
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:limit], ", "), len(items)-limit)
}

func (s *Server) cancelRunsForWorktree(worktreeID string) error {
	runs, err := s.store.ListRuns()
	if err != nil {
		return err
	}
	for _, item := range runs {
		if item.WorktreeID != worktreeID || item.State.Terminal() {
			continue
		}
		if _, err := s.runs.Cancel(item.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) dropSessionsForWorktree(worktreeID string) error {
	for _, sess := range s.sessions.List() {
		if sess.WorktreeID != worktreeID {
			continue
		}
		verdict, err := s.sessions.RemoveWithResult(sess.ID, "worktree_removed")
		if err != nil && !errors.Is(err, session.ErrNotFound) {
			return err
		}
		if verdict == session.StopUnverifiable {
			s.log.Warn("worktree session stop unverifiable", "session", sess.ID)
		}
	}
	stored, err := s.store.ListSessions()
	if err != nil {
		return err
	}
	for _, item := range stored {
		if item.WorktreeID != worktreeID {
			continue
		}
		if item.Status == model.SessionRunning || item.Status == model.SessionLost {
			if verdict := session.StopStored(item.PID, item.StartTicks, 2*time.Second); verdict == session.StopUnverifiable {
				s.log.Warn("stored session stop unverifiable", "session", item.ID)
			}
		}
		if err := s.store.DeleteSession(item.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := s.store.DeleteTranscript(item.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("delete transcript", "session", item.ID, "error", err)
		}
	}
	return nil
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if runs == nil {
		runs = []model.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorktreeID string           `json:"worktreeId"`
		Goal       string           `json:"goal"`
		Steps      []model.PlanStep `json:"steps"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	run, err := s.runs.Create(body.WorktreeID, body.Goal, body.Steps)
	if err != nil {
		switch {
		case errors.Is(err, runpkg.ErrInvalidPlan):
			s.refuse(w, r, codeInvalidPlan, err.Error())
		case errors.Is(err, store.ErrNotFound):
			s.refuseTarget(w, r, codeWorktreeNotFound, body.WorktreeID, "")
		default:
			s.fail(w, r, err)
		}
		return
	}
	setAuditTarget(r, run.ID)
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRun(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuse(w, r, codeRunNotFound, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	plan, err := s.store.GetPlan(run.PlanHash)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	steps, err := s.store.ListSteps(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if steps == nil {
		steps = []model.Step{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "plan": plan, "steps": steps})
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.Start(r.PathValue("id"))
	if err != nil {
		s.runFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.Cancel(r.PathValue("id"))
	if err != nil {
		s.runFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleAnswerStep(w http.ResponseWriter, r *http.Request) {
	ordinal, err := strconv.Atoi(r.PathValue("ordinal"))
	if err != nil || ordinal < 1 {
		s.refuse(w, r, codeInvalidStep, "invalid step ordinal")
		return
	}
	var body struct {
		Result string `json:"result"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	run, err := s.runs.Answer(r.PathValue("id"), ordinal, strings.TrimSpace(body.Result))
	if err != nil {
		s.runFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) runFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.refuse(w, r, codeRunNotFound, "")
	case errors.Is(err, runpkg.ErrRunTerminal):
		s.refuse(w, r, codeRunTerminal, "")
	case errors.Is(err, runpkg.ErrStepNotWait):
		s.refuse(w, r, codeStepNotWaiting, "")
	case errors.Is(err, runpkg.ErrInvalidPlan):
		s.refuse(w, r, codeInvalidPlan, err.Error())
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorktreeID string `json:"worktreeId"`
		Agent      string `json:"agent"`
		Kind       string `json:"kind"`
		Title      string `json:"title"`
		Cols       uint16 `json:"cols"`
		Rows       uint16 `json:"rows"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	// A create costs a PTY, and a client that retries after a lost response
	// would otherwise leave a twin session behind. The key is optional so that
	// curl and the CLI stay simple; the browser always sends one.
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(requestID) > 128 {
		s.refuse(w, r, codeInvalidRequest, "Idempotency-Key is longer than 128 bytes")
		return
	}
	if requestID != "" {
		s.createMu.Lock()
		defer s.createMu.Unlock()
		if existing, ok := s.sessionByRequestID(requestID); ok {
			// The live model knows the PTY, not the client's key; keep the key
			// from the row so a replayed response looks like the original.
			stored := existing.ClientRequestID
			live := false
			if sess, ok := s.sessions.Get(existing.ID); ok {
				existing = sess.Model()
				existing.ClientRequestID = stored
				live = sess.Status() == model.SessionRunning
			}
			writeJSON(w, http.StatusOK, sessionView{Session: existing, Live: live})
			return
		}
	}
	wt, err := s.store.GetWorktree(body.WorktreeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.refuseTarget(w, r, codeWorktreeNotFound, body.WorktreeID, "")
			return
		}
		s.fail(w, r, err)
		return
	}
	kind := strings.TrimSpace(body.Kind)

	if kind == "" {
		kind = "agent"
	}
	agent := strings.TrimSpace(body.Agent)
	command, args, env, title := "", []string(nil), map[string]string(nil), body.Title
	if kind == "shell" || agent == "shell" {
		kind = "shell"
		command = os.Getenv("SHELL")
		if command == "" {
			command = "/bin/sh"
		}
		args = []string{"-l"}
		if title == "" {
			title = "shell"
		}
	} else {
		preset, ok := s.cfg.Agents[agent]
		if !ok {
			s.refuse(w, r, codeUnknownAgent, "unknown agent "+agent)
			return
		}
		command, args, env = preset.Command, preset.Args, preset.Env
		if title == "" {
			title = agent
		}
	}
	if title == "" {
		title = filepath.Base(command)
	}
	containment := s.containmentFor(wt)
	if preset, ok := s.cfg.Agents[agent]; ok && strings.TrimSpace(preset.Containment) != "" {
		containment = preset.Containment
		if containment == "auto" {
			containment = s.defaultContainment()
		}
		if containment == "jail" && !session.JailAvailable() && !s.cfg.JailScrubEnv {
			containment = "off"
		}
	}
	home := ""
	if containment == "jail" {
		home = s.homeFor(wt.ID)
	}
	hiddenPaths, err := s.hiddenPathsFor(containment, agent)
	if err != nil {
		s.refuse(w, r, codeSessionCreateFailed, err.Error())
		return
	}
	sess, err := s.sessions.Start(session.Spec{
		WorktreeID:  wt.ID,
		Dir:         wt.Path,
		Kind:        kind,
		Agent:       agent,
		Origin:      "user",
		Command:     command,
		Args:        args,
		Env:         env,
		Title:       title,
		Cols:        body.Cols,
		Rows:        body.Rows,
		Containment: containment,
		Home:        home,
		HiddenPaths: hiddenPaths,
		ScrubEnv:    containment == "jail" && s.cfg.JailScrubEnv,
		EnvAllow:    s.jailEnvAllow(agent),
	})
	if err != nil {
		s.refuse(w, r, codeSessionCreateFailed, err.Error())
		return
	}
	first := sess.Model()
	first.ClientRequestID = requestID
	if _, err := s.store.PutSession(first); err != nil {
		sess.Close()
		s.fail(w, r, err)
		return
	}
	// A short-lived command can exit before the row is written. Put the final
	// shape once more so the store never claims a dead PTY is running.
	if current := sess.Model(); current.Status != first.Status {
		current.ClientRequestID = requestID
		if _, err := s.store.PutSession(current); err != nil {
			s.log.Error("persist session exit", "session", sess.ID, "error", err)
		}
	}
	created := sess.Model()
	created.ClientRequestID = requestID
	setAuditTarget(r, created.ID)
	writeJSON(w, http.StatusCreated, sessionView{Session: created, Live: sess.Status() == model.SessionRunning})
}

// sessionByRequestID finds a session that was already created for this
// Idempotency-Key. Scanning is deliberate: a personal daemon holds tens of
// sessions, and a dedicated index would need a migration for a rare path.
func (s *Server) sessionByRequestID(key string) (model.Session, bool) {
	sessions, err := s.store.ListSessions()
	if err != nil {
		return model.Session{}, false
	}
	for _, sess := range sessions {
		if sess.ClientRequestID == key {
			return sess, true
		}
	}
	return model.Session{}, false
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	verdict := session.StopUnverifiable
	found := false
	if _, ok := s.sessions.Get(id); ok {
		result, err := s.sessions.RemoveWithResult(id, "stopped")
		if err != nil && !errors.Is(err, session.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		verdict = result
		found = true
	} else if item, err := s.store.GetSession(id); err == nil {
		found = true
		if item.Status == model.SessionRunning || item.Status == model.SessionLost {
			verdict = session.StopStored(item.PID, item.StartTicks, 2*time.Second)
		} else {
			verdict = session.StopAlreadyExited
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	if !found {
		s.refuse(w, r, codeSessionNotFound, "")
		return
	}
	if err := s.store.DeleteSession(id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	if err := s.store.DeleteTranscript(id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("delete transcript", "session", id, "error", err)
	}
	// A pane capability names this session; the session is gone, so is it.
	s.dropPanesForSession(id)
	if verdict == session.StopUnverifiable {
		s.log.Warn("session stop unverifiable", "session", id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "verdict": verdict})
}

// takeStreamChunks appends chunks already queued for this viewer. One flush
// per burst keeps a PTY that writes line by line from turning into one HTTP
// chunk, one native-attach write and one VTE render per line. The byte cap
// bounds how long the first byte of a burst can wait.
const streamBatchLimit = 256 << 10

func takeStreamChunks(chunks <-chan []byte, batch []byte) ([]byte, bool) {
	for len(batch) < streamBatchLimit {
		select {
		case chunk, open := <-chunks:
			if !open {
				return batch, true
			}
			batch = append(batch, chunk...)
		default:
			return batch, false
		}
	}
	return batch, false
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessions.Get(r.PathValue("id"))
	if !ok {
		s.refuse(w, r, codeSessionNotFound, "")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.refuse(w, r, codeStreamingUnsupported, "")
		return
	}
	// A terminal stream is long-lived; the listener's request/response
	// deadlines must not cut it. The envelope route clears the same deadlines
	// on the outer connection for its own streams.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	snapshot, redraw, chunks, cancel := sess.SubscribeBrowser()
	defer cancel()
	info, _ := r.Context().Value(authInfoKey{}).(authInfo)
	kind := "daemon"
	if info.paneKey != "" {
		// The capability dies with the window that redeemed it. A shell that
		// crashes still closes its stream, so this is the normal path.
		defer s.dropPane(info.paneKey)
		kind = "pane"
	}
	meter := session.NewStreamMeter(r.PathValue("id"), kind)
	meter.Run(r.Context(), s.log)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	if redraw {
		w.Header().Set("X-DF-Redraw", "1")
	}
	w.WriteHeader(http.StatusOK)
	// A browser fetch does not hand the response to the page until the first
	// body byte arrives. An attach that suppresses the replay for a full-screen
	// TUI sent nothing at all, so the page never saw X-DF-Redraw, never asked
	// for the SIGWINCH repaint, and stayed black until the app happened to write
	// something on its own. One SGR reset is a terminal no-op and makes the
	// headers readable immediately.
	if _, err := io.WriteString(w, "\x1b[0m"); err != nil {
		return
	}
	if len(snapshot) > 0 {
		if _, err := w.Write(snapshot); err != nil {
			return
		}
	}
	meter.Record(len(snapshot))
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sess.Done():
			return
		case chunk, open := <-chunks:
			if !open {
				return
			}
			chunk, closed := takeStreamChunks(chunks, chunk)
			if len(chunk) > 0 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				meter.Record(len(chunk))
				flusher.Flush()
			}
			if closed {
				// The subscriber channel closed while the stream did not:
				// a newer attach took over or the client fell behind. The
				// client saw a silent freeze, so this event is logged even
				// without DF_STREAM_DEBUG.
				b, d := meter.Totals()
				s.log.Warn("stream_ended_early", "session", r.PathValue("id"),
					"bytes", b, "elapsed_ms", d.Milliseconds())
				return
			}
		case <-ticker.C:
			flusher.Flush()
		}
	}
}

// handleClientDebug accepts the opt-in browser debug log so it lands in the
// same request log as the server. It is bearer-gated and line-capped; it never
// receives tokens or request bodies.
func (s *Server) handleClientDebug(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) > 200 {
		lines = lines[:200]
	}
	for _, line := range lines {
		line = sanitizeLogLine(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > 500 {
			line = string(runes[:500]) + "…"
		}
		s.log.Info("client-debug", "line", line)
	}
	w.WriteHeader(http.StatusNoContent)
}

// sanitizeLogLine removes control characters from client-supplied text before
// it reaches the log. A newline would let a client forge a second record, and
// an escape sequence would let it drive whatever renders the log.
func sanitizeLogLine(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	for _, r := range line {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Drop other control characters.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessions.Get(r.PathValue("id"))
	if !ok {
		s.refuse(w, r, codeSessionNotFound, "")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	if err := sess.Write(body); err != nil {
		s.refuse(w, r, codeTerminalWriteFailed, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessions.Get(r.PathValue("id"))
	if !ok {
		s.refuse(w, r, codeSessionNotFound, "")
		return
	}
	var body struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	if err := sess.Resize(body.Cols, body.Rows); err != nil {
		s.refuse(w, r, codeInvalidResize, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRedraw(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessions.Get(r.PathValue("id"))
	if !ok {
		s.refuse(w, r, codeSessionNotFound, "")
		return
	}
	if err := sess.Kick(); err != nil {
		s.refuse(w, r, codeTerminalWriteFailed, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

// decodeOptionalJSON accepts an empty body as an empty request. Handlers that
// already worked before a body was added use it so an old client is not broken.
func decodeOptionalJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
