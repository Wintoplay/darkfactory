//go:build !windows

package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wintoplay/darkfactory/internal/id"
	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/creack/pty"
)

var ErrNotFound = errors.New("session not found")

// StopResult is the honest answer to "did deleting this row stop the process?".
// A stored PID is only signalled when the process start time still matches the
// recorded incarnation; otherwise PID reuse makes the answer unverifiable.
type StopResult string

const (
	StopStopped       StopResult = "stopped"
	StopAlreadyExited StopResult = "already_exited"
	StopUnverifiable  StopResult = "unverifiable"
)

type Spec struct {
	ID          string
	CreatedAt   time.Time
	WorktreeID  string
	Dir         string
	Kind        string
	Agent       string
	Origin      string
	Command     string
	Args        []string
	Env         map[string]string
	Title       string
	Cols        uint16
	Rows        uint16
	Containment string
	Home        string
	HiddenPaths []string
	// ScrubEnv asks a jailed session for a minimal environment instead of the
	// daemon's full os.Environ(). It is only valid with jail containment and a
	// projected HOME; Start refuses it otherwise rather than silently dropping
	// the request.
	ScrubEnv bool
	// EnvAllow names variables copied from the daemon environment when ScrubEnv
	// is set. It never copies HOME or TMPDIR; those come from the projected
	// session HOME.
	EnvAllow []string
	History  []byte
}

type Session struct {
	ID          string
	WorktreeID  string
	Kind        string
	Agent       string
	Origin      string
	Command     string
	Args        []string
	Title       string
	Cols        uint16
	Rows        uint16
	Dir         string
	Containment string
	CreatedAt   time.Time

	mu          sync.RWMutex
	writeMu     sync.Mutex
	cmd         *exec.Cmd
	pty         *os.File
	ring        *Ring
	subs        map[chan []byte]struct{}
	modes       attachModes
	done        chan struct{}
	status      model.SessionStatus
	exitCode    *int
	pid         int
	pgid        int
	startTicks  uint64
	incarnation string
	reason      string
	endedAt     *time.Time
	manager     *Manager
	remote      *remoteLink
	doneOnce    sync.Once
}

type Manager struct {
	mu         sync.RWMutex
	sessions   map[string]*Session
	ringSize   int
	persist    func(model.Session)
	transcript func(string, []byte)
	host       *HostClient

	reaperOnce sync.Once
	reaperStop chan struct{}
}

func NewManager(ringBytes int) *Manager {
	return &Manager{
		sessions:   make(map[string]*Session),
		ringSize:   ringBytes,
		reaperStop: make(chan struct{}),
	}
}

// SetPersist installs the durable write path. It must be set before sessions
// start; the session manager never imports the store.
func (m *Manager) SetPersist(fn func(model.Session)) {
	m.mu.Lock()
	m.persist = fn
	m.mu.Unlock()
}

// SetTranscript installs the durable scrollback path. The manager never imports
// the store; the server wires this to a capped bucket.
func (m *Manager) SetTranscript(fn func(string, []byte)) {
	m.mu.Lock()
	m.transcript = fn
	m.mu.Unlock()
}

// StartReaper stops a PTY once its working directory disappears. It is the
// last line of defence for a worktree removed out from under a live shell.
func (m *Manager) StartReaper(interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	m.reaperOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-m.reaperStop:
					return
				case <-ticker.C:
					m.reap()
				}
			}
		}()
	})
}

func (m *Manager) reap() {
	for _, s := range m.List() {
		if s.Status() != model.SessionRunning {
			continue
		}
		if s.Dir == "" {
			continue
		}
		if _, err := os.Stat(s.Dir); err != nil {
			s.Stop("worktree_missing")
		}
	}
}

func (m *Manager) Start(spec Spec) (*Session, error) {
	if spec.Command == "" {
		return nil, errors.New("session command is empty")
	}
	if m.host != nil {
		return m.startRemote(spec)
	}
	if spec.Cols == 0 {
		spec.Cols = 120
	}
	if spec.Rows == 0 {
		spec.Rows = 32
	}
	sessionID := spec.ID
	if sessionID == "" {
		sessionID = id.New()
	}
	createdAt := spec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	containment := spec.Containment
	if containment == "jail" && !JailAvailable() {
		if spec.ScrubEnv {
			return nil, fmt.Errorf("jail environment scrubbing requires a usable bwrap")
		}
		containment = "off"
	}
	if spec.ScrubEnv {
		if containment != "jail" {
			return nil, fmt.Errorf("environment scrubbing requires jail containment")
		}
		if spec.Home == "" {
			return nil, fmt.Errorf("environment scrubbing requires a per-worktree HOME")
		}
	}
	if spec.Home != "" {
		if err := os.MkdirAll(spec.Home, 0o700); err != nil {
			return nil, fmt.Errorf("create session home: %w", err)
		}
	}
	env := sessionEnvironment(os.Environ(), spec, containment)
	command := spec.Command
	args := spec.Args
	if containment == "jail" {
		command, args = jailCommand(spec)
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = spec.Dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: spec.Cols, Rows: spec.Rows})
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Command, err)
	}
	pid, pgid := 0, 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
		pgid = pid
	}
	ring := NewRing(m.ringSize)
	if len(spec.History) > 0 {
		ring.Write(spec.History)
	}
	s := &Session{
		ID:          sessionID,
		WorktreeID:  spec.WorktreeID,
		Kind:        spec.Kind,
		Agent:       spec.Agent,
		Origin:      spec.Origin,
		Command:     spec.Command,
		Args:        spec.Args,
		Title:       spec.Title,
		Cols:        spec.Cols,
		Rows:        spec.Rows,
		Dir:         spec.Dir,
		Containment: containment,
		CreatedAt:   createdAt,
		cmd:         cmd,
		pty:         ptyFile,
		ring:        ring,
		subs:        make(map[chan []byte]struct{}),
		done:        make(chan struct{}),
		status:      model.SessionRunning,
		pid:         pid,
		pgid:        pgid,
		startTicks:  ReadStartTicks(pid),
		incarnation: id.New(),
		manager:     m,
	}
	if len(spec.History) > 0 {
		s.modes.Write(spec.History)
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()

	go s.readLoop(ptyFile)
	go s.waitLoop()
	return s, nil
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Remove stops a live PTY, removes it from the in-memory map, and returns the
// verdict. The caller owns the durable row and can delete it either way.
func (m *Manager) Remove(id string) error {
	_, err := m.RemoveWithResult(id, "stopped")
	return err
}

func (m *Manager) RemoveWithResult(id, reason string) (StopResult, error) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return StopUnverifiable, ErrNotFound
	}
	verdict := s.Stop(reason)
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return verdict, nil
}

// CloseAll stops local PTYs and detaches remote ones. The daemon must not stop
// the session host on shutdown; the host owns the PTYs and survives it.
func (m *Manager) CloseAll() {
	m.CloseAllWithReason("shutdown")
}

func (m *Manager) CloseAllWithReason(reason string) {
	m.reaperOnce.Do(func() { close(m.reaperStop) })
	for _, s := range m.List() {
		if s.remote != nil {
			s.detach()
			continue
		}
		s.Stop(reason)
	}
}

func (s *Session) readLoop(ptyFile *os.File) {
	buf := make([]byte, 32*1024)
	for {
		n, err := ptyFile.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.ring.Write(chunk)
			s.modes.Write(chunk)
			s.broadcast(chunk)
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) waitLoop() {
	err := s.cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	s.finish(code)
}

func (s *Session) finish(code int) {
	s.end(model.SessionExited, &code, "exited")
}

func (s *Session) end(status model.SessionStatus, code *int, reason string) {
	s.mu.Lock()
	if s.status != model.SessionRunning {
		s.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	s.status = status
	if code != nil {
		copy := *code
		s.exitCode = &copy
	}
	s.endedAt = &now
	if s.reason == "" && reason != "" {
		s.reason = reason
	}
	if s.reason == "" {
		s.reason = "exited"
	}
	subs := s.subs
	finalReason := s.reason
	s.subs = nil
	s.mu.Unlock()
	if finalReason == "shutdown" {
		s.saveTranscript()
	}
	for ch := range subs {
		close(ch)
	}
	s.notify()
	s.closeDone()
}

func (s *Session) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *Session) broadcast(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != model.SessionRunning {
		return
	}
	for ch := range s.subs {
		select {
		case ch <- chunk:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (s *Session) Write(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if s.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.remote.client.Input(ctx, s.ID, p)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.pty == nil {
		return errors.New("session is closed")
	}
	_, err := s.pty.Write(p)
	return err
}

func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return errors.New("invalid terminal size")
	}
	if s.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.remote.client.Resize(ctx, s.ID, cols, rows); err != nil {
			return err
		}
		s.mu.Lock()
		s.Cols, s.Rows = cols, rows
		s.mu.Unlock()
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.pty == nil {
		return errors.New("session is closed")
	}
	if err := pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return err
	}
	s.mu.Lock()
	s.Cols, s.Rows = cols, rows
	s.mu.Unlock()
	return nil
}

// Kick forces a SIGWINCH repaint without changing the final terminal size.
// A same-size ioctl is a no-op on Linux, so nudge one row (or column) and put
// it back. This is what makes a refreshed TUI redraw instead of replaying a
// partial frame from the ring.
func (s *Session) Kick() error {
	if s.remote != nil {
		s.mu.RLock()
		cols, rows := s.Cols, s.Rows
		s.mu.RUnlock()
		if cols == 0 {
			cols = 120
		}
		if rows == 0 {
			rows = 32
		}
		nudge, ok := nudgeSize(cols, rows)
		if !ok {
			return errors.New("cannot nudge terminal size")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.remote.client.Resize(ctx, s.ID, nudge.Cols, nudge.Rows); err != nil {
			return err
		}
		return s.remote.client.Resize(ctx, s.ID, cols, rows)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.pty == nil {
		return errors.New("session is closed")
	}
	s.mu.RLock()
	cols, rows := s.Cols, s.Rows
	s.mu.RUnlock()
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	nudge, ok := nudgeSize(cols, rows)
	if !ok {
		return errors.New("cannot nudge terminal size")
	}
	if err := pty.Setsize(s.pty, &nudge); err != nil {
		return err
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

func nudgeSize(cols, rows uint16) (pty.Winsize, bool) {
	size := pty.Winsize{Cols: cols, Rows: rows}
	if rows > 2 {
		size.Rows = rows - 1
		return size, true
	}
	if cols < 65535 {
		size.Cols = cols + 1
		return size, true
	}
	return pty.Winsize{}, false
}

func (s *Session) Snapshot() []byte {
	return s.ring.Snapshot()
}

func (s *Session) saveTranscript() {
	if s.ring == nil {
		return
	}
	data := s.ring.Snapshot()
	if len(data) == 0 {
		return
	}
	m := s.manager
	if m == nil {
		return
	}
	m.mu.RLock()
	fn := m.transcript
	m.mu.RUnlock()
	if fn != nil {
		fn(s.ID, data)
	}
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) Subscribe() ([]byte, <-chan []byte, func()) {
	snapshot := s.ring.Snapshot()
	ch, cancel := s.subscribeChan()
	return snapshot, ch, cancel
}

// browserDetachedNotice is written to the old browser stream when a newer
// attach takes over. The PTY has one size; two clients with different viewport
// sizes cannot both own it without corrupting the other's screen.
const browserDetachedNotice = "\r\n[detached: another browser attached]\r\n"

// SubscribeBrowser is the browser-facing attach path. It may suppress the
// snapshot for a full-screen TUI and tell the caller to request a repaint.
// The replay always ends with the input modes the app has enabled: a fresh
// xterm has them all off, and a repaint on SIGWINCH does not repeat the
// enables, so without this the screen paints but clicks and pastes send
// nothing.
// Only one browser terminal may own a session at a time: a new attach detaches
// the previous stream instead of letting both resize the same PTY.
func (s *Session) SubscribeBrowser() ([]byte, bool, <-chan []byte, func()) {
	snapshot, complete := s.ring.SnapshotFromStart()
	replay, redraw := browserSnapshot(snapshot, complete)
	replay = append(replay, s.modes.Restore()...)
	ch := make(chan []byte, 256)
	s.mu.Lock()
	if s.status == model.SessionRunning && s.subs != nil {
		for old := range s.subs {
			delete(s.subs, old)
			select {
			case old <- []byte(browserDetachedNotice):
			default:
			}
			close(old)
		}
		s.subs[ch] = struct{}{}
	} else {
		close(ch)
	}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return replay, redraw, ch, cancel
}

func (s *Session) subscribeChan() (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	s.mu.Lock()
	if s.status == model.SessionRunning && s.subs != nil {
		s.subs[ch] = struct{}{}
	} else {
		close(ch)
	}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

// Stop escalates SIGTERM to SIGKILL for the whole process group, then waits.
// It is safe to call more than once and from the reaper, shutdown and API.
func (s *Session) Stop(reason string) StopResult {
	if s.remote != nil {
		return s.stopRemote(reason)
	}
	s.mu.Lock()
	if s.status != model.SessionRunning {
		s.mu.Unlock()
		return StopAlreadyExited
	}
	if reason == "" {
		reason = "stopped"
	}
	s.reason = reason
	pid, pgid, startTicks := s.pid, s.pgid, s.startTicks
	done := s.done
	s.mu.Unlock()

	wasAlive := ProcessExists(pid)
	if !wasAlive {
		return StopAlreadyExited
	}

	s.writeMu.Lock()
	if s.pty != nil {
		_ = s.pty.Close()
		s.pty = nil
	}
	s.writeMu.Unlock()

	if startTicks == 0 {
		// A live session created by this manager is trusted in memory even on
		// platforms without /proc. A stored row is not: StopProcess refuses it.
		if !signalGroup(pid, pgid, syscall.SIGTERM) {
			return StopUnverifiable
		}
	} else if !signalGroup(pid, pgid, syscall.SIGTERM) {
		// Closing the master above hangs up the terminal, and SIGHUP can kill
		// the process group before SIGTERM is sent. The process was alive when
		// this call started, so this is still a stop we caused rather than one
		// that had already happened. Report the wait, not ESRCH.
		if waitDone(done, 2*time.Second) {
			return StopStopped
		}
		return StopUnverifiable
	}
	if waitDone(done, 2*time.Second) {
		return StopStopped
	}
	if !signalGroup(pid, pgid, syscall.SIGKILL) {
		return StopUnverifiable
	}
	if waitDone(done, 2*time.Second) {
		return StopStopped
	}
	return StopUnverifiable
}

func (s *Session) Close() {
	s.Stop("stopped")
}

func (s *Session) Status() model.SessionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Session) ExitCode() *int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.exitCode == nil {
		return nil
	}
	code := *s.exitCode
	return &code
}

func (s *Session) Model() model.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modelLocked()
}

func (s *Session) modelLocked() model.Session {
	code := s.exitCode
	if code != nil {
		copy := *code
		code = &copy
	}
	ended := s.endedAt
	if ended != nil {
		copy := *ended
		ended = &copy
	}
	return model.Session{
		ID:            s.ID,
		WorktreeID:    s.WorktreeID,
		Kind:          s.Kind,
		Agent:         s.Agent,
		Origin:        s.Origin,
		Command:       s.Command,
		Args:          append([]string(nil), s.Args...),
		Title:         s.Title,
		Status:        s.status,
		Cols:          s.Cols,
		Rows:          s.Rows,
		ExitCode:      code,
		PID:           s.pid,
		PGID:          s.pgid,
		StartTicks:    s.startTicks,
		IncarnationID: s.incarnation,
		Containment:   s.Containment,
		EndedAt:       ended,
		Reason:        s.reason,
		CreatedAt:     s.CreatedAt,
	}
}

func (s *Session) notify() {
	// The manager owns the callback; sessions only carry the durable shape.
	s.mu.RLock()
	snapshot := s.modelLocked()
	s.mu.RUnlock()
	m := s.manager
	if m == nil {
		return
	}
	m.mu.RLock()
	fn := m.persist
	m.mu.RUnlock()
	if fn != nil {
		fn(snapshot)
	}
}

var jailProbe struct {
	once sync.Once
	ok   bool
}

// JailAvailable reports whether the Linux bubblewrap jail can be used. A
// missing bwrap, or one whose user namespace the kernel refuses, is an honest
// fallback to same-uid, not a silent claim.
func JailAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return false
	}
	jailProbe.once.Do(func() {
		jailProbe.ok = jailUsable(path)
	})
	return jailProbe.ok
}

// jailUsable runs the smallest jail that exercises the user namespace. The
// binary can exist while AppArmor or the kernel denies the uid map, which would
// otherwise start a session that dies with "setting up uid map: Permission
// denied" instead of falling back to same-uid.
func jailUsable(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path,
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "/bin/true",
	).Run() == nil
}

// sessionEnvironment builds the child environment. Inheriting the daemon's
// environment leaks SSH agent sockets, D-Bus, desktop session variables, cloud
// credentials and shell hooks into an agent. A scrubbed jail keeps only what a
// terminal and a non-interactive CLI need; per-preset Env values are applied
// last and still override the allowlist.
func sessionEnvironment(base []string, spec Spec, containment string) []string {
	if containment != "jail" || !spec.ScrubEnv {
		env := append([]string(nil), base...)
		env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
		if spec.Home != "" {
			env = append(env, "HOME="+spec.Home, "TMPDIR=/tmp")
		}
		keys := make([]string, 0, len(spec.Env))
		for key := range spec.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			env = append(env, key+"="+spec.Env[key])
		}
		return env
	}

	allowed := map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true,
		"TERM": true, "COLORTERM": true,
		"LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true,
		"LC_MESSAGES": true, "TZ": true,
		"SHELL": true, "USER": true, "LOGNAME": true,
	}
	copyAllow := make(map[string]bool, len(spec.EnvAllow))
	for _, key := range spec.EnvAllow {
		if key != "" && key != "HOME" && key != "TMPDIR" {
			copyAllow[key] = true
		}
	}
	values := make(map[string]string, len(base))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || (!allowed[key] && !copyAllow[key]) || key == "HOME" {
			continue
		}
		values[key] = value
	}
	if strings.TrimSpace(values["PATH"]) == "" {
		values["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	if spec.Home != "" {
		values["HOME"] = spec.Home
		values["TMPDIR"] = "/tmp"
	}
	values["TERM"] = "xterm-256color"
	values["COLORTERM"] = "truecolor"
	for key, value := range spec.Env {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// CredentialHiddenPaths is the built-in list of credential, keyring and browser
// profile paths masked by JailHideCredentials. It is deliberately about
// secrets, not application configuration an agent may legitimately need.
func CredentialHiddenPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	rel := []string{
		".ssh",
		".aws",
		".azure",
		".config/gcloud",
		".kube",
		".docker",
		".gnupg",
		".netrc",
		".git-credentials",
		".password-store",
		".config/gh",
		".config/opencode",
		".config/rclone",
		".local/share/keyrings",
		".mozilla",
		".config/google-chrome",
		".config/chromium",
		".config/BraveSoftware",
		".config/Code",
		".npmrc",
		".pypirc",
		".cargo/credentials.toml",
	}
	out := make([]string, 0, len(rel))
	for _, path := range rel {
		out = append(out, filepath.Join(home, path))
	}
	return out
}

func mergeUniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := paths[:0]
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" || path == "/" || !filepath.IsAbs(path) {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func jailCommand(spec Spec) (string, []string) {
	args := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--chdir", spec.Dir,
	}
	if spec.Home != "" {
		args = append(args, "--bind", spec.Home, spec.Home, "--setenv", "HOME", spec.Home)
	}
	for _, path := range mergeUniquePaths(spec.HiddenPaths) {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--tmpfs", path)
		}
	}
	args = append(args, "--bind", spec.Dir, spec.Dir, "--", spec.Command)
	args = append(args, spec.Args...)
	return "bwrap", args
}

func waitDone(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func signalGroup(pid, pgid int, sig syscall.Signal) bool {
	if pid <= 1 {
		return false
	}
	target := pid
	if pgid > 0 {
		target = -pgid
	}
	if err := syscall.Kill(target, sig); err == nil {
		return true
	} else if !errors.Is(err, syscall.ESRCH) {
		return false
	}
	if target != -pid {
		if err := syscall.Kill(pid, sig); err == nil {
			return true
		}
	}
	return false
}

// ProcessExists reports whether a PID exists. It deliberately does not prove
// identity; use ProcessMatches for that.
func ProcessExists(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ReadStartTicks returns the Linux /proc start time in clock ticks. A zero
// value means the platform cannot prove process identity.
func ReadStartTicks(pid int) uint64 {
	state, ticks, ok := readProcStat(pid)
	if !ok || state == "Z" {
		return 0
	}
	return ticks
}

// ProcessMatches checks the recorded start time, which defeats PID reuse.
func ProcessMatches(pid int, startTicks uint64) bool {
	if pid <= 1 || startTicks == 0 {
		return false
	}
	state, ticks, ok := readProcStat(pid)
	return ok && state != "Z" && ticks == startTicks
}

func readProcStat(pid int) (string, uint64, bool) {
	if pid <= 1 {
		return "", 0, false
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", 0, false
	}
	text := string(raw)
	end := strings.LastIndexByte(text, ')')
	if end < 0 || end+2 >= len(text) {
		return "", 0, false
	}
	fields := strings.Fields(text[end+1:])
	// After comm: field 3 is state; field 22 is starttime (index 19 here).
	if len(fields) < 20 {
		return "", 0, false
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return fields[0], ticks, true
}

// StopStored tries to stop a process from a persisted row. It refuses to
// signal anything it cannot identify, which is the only honest answer after a
// restart or PID reuse.
func StopStored(pid int, startTicks uint64, timeout time.Duration) StopResult {
	if pid <= 1 {
		return StopUnverifiable
	}
	if !ProcessExists(pid) {
		return StopAlreadyExited
	}
	if startTicks == 0 || !ProcessMatches(pid, startTicks) {
		return StopUnverifiable
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if !signalGroup(pid, pid, syscall.SIGTERM) {
		if !ProcessExists(pid) {
			return StopAlreadyExited
		}
		return StopUnverifiable
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !ProcessMatches(pid, startTicks) {
			return StopStopped
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ProcessMatches(pid, startTicks) {
		return StopStopped
	}
	if !signalGroup(pid, pid, syscall.SIGKILL) {
		return StopUnverifiable
	}
	deadline = time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !ProcessMatches(pid, startTicks) {
			return StopStopped
		}
		time.Sleep(50 * time.Millisecond)
	}
	return StopUnverifiable
}
