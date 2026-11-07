package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// Notification is one human-attention event. The daemon keeps a short ring so
// a phone that was asleep can catch up, and so the same feed drives the local
// desktop notifier and the Android foreground service.
type Notification struct {
	ID    int64     `json:"id"`
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	Body  string    `json:"body,omitempty"`
	At    time.Time `json:"at"`
	// SessionID and WorktreeID let a client put an unread bell on the exact
	// session. A run event has only the worktree.
	SessionID  string `json:"sessionId,omitempty"`
	WorktreeID string `json:"worktreeId,omitempty"`
	// SessionTitle names the session or chat the event came from, so an
	// activity list can label its source without a second lookup. An OpenCode
	// chat is not a PTY session: OpenCodeSessionID names it and SessionID
	// stays empty, because the id spaces are unrelated.
	SessionTitle      string `json:"sessionTitle,omitempty"`
	OpenCodeSessionID string `json:"openCodeSessionId,omitempty"`
	// Alert says whether the owner's settings allow this event to make noise
	// (a desktop bubble or a phone notification). Every event still joins the
	// activity ring: the bell is a list, the alert is the interruption.
	Alert bool `json:"alert"`
}

const (
	notificationRing          = 100
	notificationStateEvery    = 2 * time.Second
	notificationOpenCodeEvery = 3 * time.Second
	notificationCooldown      = 30 * time.Second
	notificationBodyMax       = 500
)

type notificationHub struct {
	server *Server
	log    *slog.Logger
	// desktop is nil when local notifications are disabled or notify-send is
	// not installed. It never runs in tests unless a test installs a fake.
	desktop func(Notification)

	mu       sync.Mutex
	seq      int64
	events   []Notification
	sessions map[string]model.SessionStatus
	runs     map[string]model.RunState
	// open holds the prompts that are still waiting for the owner right now
	// (a permission or a question), keyed by kind and ids. A baseline request
	// returns them: a reload while an agent is blocked must not leave the bell
	// empty, and a prompt that disappears and comes back is a new event.
	open map[string]Notification
	// lastIdle remembers each chat's last finished turn, so a poll can tell an
	// answer from a chat that has been idle all along.
	lastIdle    map[string]int64
	primed      bool
	permsPrimed bool
	lastEmit    map[string]time.Time
	startOnce   sync.Once
	// settings is the owner's alert policy, cached so an event does not read
	// state.db on every poll. The settings handler updates it under mu.
	settings store.NotificationSettings
}

func newNotificationHub(s *Server) *notificationHub {
	h := &notificationHub{
		server:   s,
		log:      s.log,
		sessions: map[string]model.SessionStatus{},
		runs:     map[string]model.RunState{},
		open:     map[string]Notification{},
		lastIdle: map[string]int64{},
		lastEmit: map[string]time.Time{},
		settings: store.DefaultNotificationSettings(),
	}
	if s.store != nil {
		if settings, err := s.store.GetNotificationSettings(); err == nil {
			h.settings = settings
		} else {
			s.log.Warn("notifications: read alert settings", "error", err)
		}
	}
	if s.cfg.DesktopNotify {
		if path, err := exec.LookPath("notify-send"); err == nil {
			h.desktop = func(n Notification) { sendDesktopNotification(path, n) }
		} else {
			s.log.Info("desktop notifications disabled: notify-send not found")
		}
	}
	return h
}

// StartNotifications starts the state and OpenCode watchers. It is idempotent;
// the goroutines end with ctx. Tests call pollState/pollPrompts directly.
func (s *Server) StartNotifications(ctx context.Context) {
	if s.notify == nil || s.store == nil {
		return
	}
	s.notify.start(ctx)
}

func (h *notificationHub) start(ctx context.Context) {
	h.startOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(notificationStateEvery)
			defer ticker.Stop()
			h.pollState()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					h.pollState()
				}
			}
		}()
		go func() {
			ticker := time.NewTicker(notificationOpenCodeEvery)
			defer ticker.Stop()
			h.pollPrompts(ctx)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					h.pollPrompts(ctx)
				}
			}
		}()
	})
}

func (h *notificationHub) pollState() {
	if h.server == nil || h.server.store == nil {
		return
	}
	sessions, err := h.server.store.ListSessions()
	if err != nil {
		h.log.Warn("notifications: list sessions", "error", err)
		return
	}
	runs, err := h.server.store.ListRuns()
	if err != nil {
		h.log.Warn("notifications: list runs", "error", err)
		return
	}

	now := time.Now().UTC()
	var events []Notification

	h.mu.Lock()
	first := !h.primed
	nextSessions := make(map[string]model.SessionStatus, len(sessions))
	for _, item := range sessions {
		nextSessions[item.ID] = item.Status
		if first {
			continue
		}
		previous, known := h.sessions[item.ID]
		if item.Status != model.SessionExited && item.Status != model.SessionLost {
			continue
		}
		if known && previous == item.Status {
			continue
		}
		// A session that was running when it disappeared deserves an alert;
		// one that was already finished before this daemon started does not
		// (the baseline above handles that case).
		if known && previous != model.SessionRunning {
			continue
		}
		key := "session:" + item.ID + ":" + string(item.Status)
		if !h.reserve(key, now) {
			continue
		}
		events = append(events, sessionNotification(item))
	}
	nextRuns := make(map[string]model.RunState, len(runs))
	for _, run := range runs {
		nextRuns[run.ID] = run.State
		if first {
			continue
		}
		previous, known := h.runs[run.ID]
		if run.State != model.RunBlocked && run.State != model.RunWaiting {
			continue
		}
		if known && previous == run.State {
			continue
		}
		key := "run:" + run.ID + ":" + string(run.State)
		if !h.reserve(key, now) {
			continue
		}
		events = append(events, runNotification(run))
	}
	h.sessions = nextSessions
	h.runs = nextRuns
	h.primed = true
	for key, at := range h.lastEmit {
		if now.Sub(at) > 24*time.Hour {
			delete(h.lastEmit, key)
		}
	}
	h.mu.Unlock()

	h.emit(events)
}

func sessionNotification(item model.Session) Notification {
	name := strings.TrimSpace(item.Title)
	if name == "" {
		name = strings.TrimSpace(item.Agent)
	}
	if name == "" {
		name = shortID(item.ID)
	}
	if item.Status == model.SessionLost {
		return Notification{
			Kind:         "session.lost",
			Title:        "Session lost",
			Body:         limitNotification(name + " is no longer attached"),
			SessionID:    item.ID,
			WorktreeID:   item.WorktreeID,
			SessionTitle: name,
		}
	}
	body := name + " finished"
	if item.ExitCode != nil && *item.ExitCode >= 0 {
		body = fmt.Sprintf("%s exited (%d)", name, *item.ExitCode)
	}
	return Notification{
		Kind:         "session.exited",
		Title:        "Agent finished",
		Body:         limitNotification(body),
		SessionID:    item.ID,
		WorktreeID:   item.WorktreeID,
		SessionTitle: name,
	}
}

func runNotification(run model.Run) Notification {
	goal := strings.TrimSpace(run.Goal)
	if goal == "" {
		goal = shortID(run.ID)
	}
	if run.State == model.RunBlocked {
		return Notification{
			Kind:         "run.blocked",
			Title:        "Agent needs your input",
			Body:         limitNotification(goal),
			WorktreeID:   run.WorktreeID,
			SessionTitle: goal,
		}
	}
	return Notification{
		Kind:         "run.waiting",
		Title:        "Agent is waiting",
		Body:         limitNotification(goal),
		WorktreeID:   run.WorktreeID,
		SessionTitle: goal,
	}
}

// reserve applies a short per-subject cooldown, so a run that flaps between
// blocked and running cannot produce a notification every two seconds.
func (h *notificationHub) reserve(key string, now time.Time) bool {
	if last, ok := h.lastEmit[key]; ok && now.Sub(last) < notificationCooldown {
		return false
	}
	h.lastEmit[key] = now
	return true
}

// pollPrompts watches the OpenCode prompts that block an agent: permission
// requests and question forms. Both need the owner, so both reach the bell with
// the chat and the workspace they came from.
func (h *notificationHub) pollPrompts(ctx context.Context) {
	server := h.server
	if server == nil || !server.openCodeEnabled() {
		return
	}
	sessions, err := server.recentOpenCodeSessions(ctx)
	if err != nil {
		// OpenCode may be restarting; a notification is not worth a log storm.
		return
	}
	if !h.includeChildSessions() {
		parents := sessions[:0]
		for _, session := range sessions {
			if strings.TrimSpace(session.ParentID) == "" {
				parents = append(parents, session)
			}
		}
		sessions = parents
	}
	permissions, err := server.collectPermissions(ctx, sessions)
	if err != nil {
		permissions = nil
	}
	questions, err := server.collectQuestions(ctx, sessions)
	if err != nil {
		// A failed form poll must not hide the permissions beside it.
		questions = nil
	}

	type prompt struct {
		key   string
		event Notification
	}
	prompts := make([]prompt, 0, len(permissions)+len(questions))
	for _, item := range permissions {
		title := strings.TrimSpace(item.SessionTitle)
		action := strings.TrimSpace(item.Action)
		if action == "" {
			action = strings.TrimSpace(item.Message)
		}
		body := title
		if action != "" {
			if body != "" {
				body += ": "
			}
			body += action
		}
		if body == "" {
			body = "An agent is waiting for a permission decision."
		}
		prompts = append(prompts, prompt{
			key: "permission:" + item.SessionID + "|" + item.ID,
			event: Notification{
				Kind:              "opencode.permission",
				Title:             "Permission requested",
				Body:              limitNotification(body),
				WorktreeID:        server.worktreeIDForDirectory(item.Directory),
				SessionTitle:      title,
				OpenCodeSessionID: item.SessionID,
			},
		})
	}
	for _, item := range questions {
		body := strings.TrimSpace(item.Title)
		if body == "" {
			body = "An agent is waiting for your answer."
		}
		prompts = append(prompts, prompt{
			key: "question:" + item.SessionID + "|" + item.ID,
			event: Notification{
				Kind:              "opencode.question",
				Title:             "Question for you",
				Body:              limitNotification(body),
				WorktreeID:        server.worktreeIDForDirectory(item.Directory),
				SessionTitle:      strings.TrimSpace(item.SessionTitle),
				OpenCodeSessionID: item.SessionID,
			},
		})
	}

	now := time.Now().UTC()
	h.mu.Lock()
	first := !h.permsPrimed
	next := make(map[string]Notification, len(prompts))
	var fresh []Notification
	for _, p := range prompts {
		if prev, ok := h.open[p.key]; ok {
			// Keep the first id and time, so a waiting row keeps its place, but
			// refresh what the row links to: a workspace can be added, or a chat
			// renamed, while the prompt waits.
			prev.WorktreeID = p.event.WorktreeID
			prev.SessionTitle = p.event.SessionTitle
			prev.Body = p.event.Body
			next[p.key] = prev
			// The ring row is what an activity list shows, so it has to carry
			// the same fresh links as the open prompt. Otherwise a page reads a
			// row whose workspace was registered after the event was created.
			h.replaceLocked(prev)
			continue
		}
		event := h.assignLocked(p.event)
		next[p.key] = event
		// A prompt that was already waiting when this daemon started is not
		// news, so it is not alerted; it still joins the ring, because a page
		// that loads later has to see what is blocking an agent. reserve also
		// keeps a flapping prompt to one alert per cooldown.
		h.appendLocked(event)
		if first || !h.reserve(p.key, now) {
			continue
		}
		fresh = append(fresh, event)
	}
	h.open = next

	// A chat's idle timestamp moving forward means it finished a turn. That is
	// activity even when it asks nothing, which is the difference between "the
	// bell only rings when something is wrong" and an activity list.
	for _, session := range sessions {
		idle := session.Time.Idle
		if idle <= 0 {
			continue
		}
		previous, seen := h.lastIdle[session.ID]
		h.lastIdle[session.ID] = idle
		// A chat that appears after the watcher started has a finished turn the
		// owner has not seen, so it reports even though the timestamp is new to
		// us. Only the priming poll stays quiet, so a daemon start is not a
		// storm of every old chat.
		if first || (seen && idle <= previous) {
			continue
		}
		// The idle timestamp already deduplicates a turn, so no cooldown here:
		// two answers in a row are two events.
		title := strings.TrimSpace(session.Title)
		body := server.openCodeLastReply(ctx, session.ID)
		if body == "" {
			body = title
		}
		if body == "" {
			body = "A chat finished a turn."
		}
		if outcome := strings.TrimSpace(session.Outcome); outcome != "" && outcome != "succeeded" {
			body += " (" + outcome + ")"
		}
		event := h.assignLocked(Notification{
			Kind:              "opencode.replied",
			Title:             "OpenCode replied",
			Body:              limitNotification(body),
			WorktreeID:        server.worktreeIDForDirectory(session.workDir()),
			SessionTitle:      title,
			OpenCodeSessionID: session.ID,
		})
		h.appendLocked(event)
		fresh = append(fresh, event)
	}
	h.permsPrimed = true
	h.mu.Unlock()
	h.alert(fresh)
}

type pendingPermission struct {
	SessionID    string
	SessionTitle string
	Directory    string
	ID           string
	Action       string
	Message      string
}

type pendingQuestion struct {
	SessionID    string
	SessionTitle string
	Directory    string
	ID           string
	Title        string
}

type openCodeFormRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type openCodeSessionRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// ParentID is set on a child/subagent chat. Those are the bulk of a busy
	// parent's traffic and usually need no owner decision, so the alert policy
	// can leave them out.
	ParentID string `json:"parentID"`
	// location.directory is the chat's working directory; the daemon maps it
	// back to a workspace so a permission bell can open the chat it came from.
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Directory string `json:"directory"`
	Outcome   string `json:"outcome"`
	Time      struct {
		Updated int64 `json:"updated"`
		// Idle is when the chat last finished a turn. A change means the agent
		// answered something, which is activity even when it needs no decision.
		Idle int64 `json:"idle"`
	} `json:"time"`
}

func (row openCodeSessionRow) workDir() string {
	if dir := strings.TrimSpace(row.Location.Directory); dir != "" {
		return dir
	}
	return strings.TrimSpace(row.Directory)
}

type openCodePermissionRow struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

// recentOpenCodeSessions is the shared poll list: only recent chats are asked
// for prompts, because an old chat waiting on a stale request should not wake
// the phone every ten seconds.
func (s *Server) recentOpenCodeSessions(reqCtx context.Context) ([]openCodeSessionRow, error) {
	ep, err := s.openCode.endpoint()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ep.baseURL+"/api/session?limit=20&order=desc", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", ep.password)
	req.Header.Set("Accept", "application/json")
	resp, err := s.openCode.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode sessions: HTTP %d", resp.StatusCode)
	}
	var list struct {
		Data []openCodeSessionRow `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	recent := make([]openCodeSessionRow, 0, len(list.Data))
	for _, session := range list.Data {
		if session.Time.Updated > 0 && now-session.Time.Updated > int64(6*time.Hour/time.Millisecond) {
			continue
		}
		recent = append(recent, session)
	}
	return recent, nil
}

// collectPermissions asks each recent chat for pending permission requests.
func (s *Server) collectPermissions(ctx context.Context, sessions []openCodeSessionRow) ([]pendingPermission, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	var out []pendingPermission
	for _, session := range sessions {
		perms, err := s.openCodePermissions(reqCtx, session.ID)
		if err != nil {
			continue
		}
		for _, perm := range perms {
			out = append(out, pendingPermission{
				SessionID:    session.ID,
				SessionTitle: session.Title,
				Directory:    session.workDir(),
				ID:           perm.ID,
				Action:       perm.Action,
				Message:      perm.Message,
			})
		}
	}
	return out, nil
}

// collectQuestions asks each recent chat for question forms. An agent that asks
// a question blocks its turn until the form is answered, so it deserves the
// same bell as a permission request.
func (s *Server) collectQuestions(ctx context.Context, sessions []openCodeSessionRow) ([]pendingQuestion, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	var out []pendingQuestion
	for _, session := range sessions {
		forms, err := s.openCodeForms(reqCtx, session.ID)
		if err != nil {
			continue
		}
		for _, form := range forms {
			out = append(out, pendingQuestion{
				SessionID:    session.ID,
				SessionTitle: session.Title,
				Directory:    session.workDir(),
				ID:           form.ID,
				Title:        form.Title,
			})
		}
	}
	return out, nil
}

// openCodeLastReply returns the last assistant text a chat produced, so the
// bell can say what the agent answered instead of only naming the chat. It is
// called only when a turn finishes, and an empty answer falls back to the title.
func (s *Server) openCodeLastReply(ctx context.Context, sessionID string) string {
	ep, err := s.openCode.endpoint()
	if err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ep.baseURL+"/api/session/"+sessionID+"/message?limit=3&order=desc", nil)
	if err != nil {
		return ""
	}
	req.SetBasicAuth("opencode", ep.password)
	req.Header.Set("Accept", "application/json")
	resp, err := s.openCode.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		Data []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return ""
	}
	for _, message := range body.Data {
		if message.Type != "assistant" {
			continue
		}
		for i := len(message.Content) - 1; i >= 0; i-- {
			if message.Content[i].Type != "text" {
				continue
			}
			if text := strings.TrimSpace(message.Content[i].Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func (s *Server) openCodeForms(ctx context.Context, sessionID string) ([]openCodeFormRow, error) {
	ep, err := s.openCode.endpoint()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.baseURL+"/api/session/"+sessionID+"/form", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", ep.password)
	req.Header.Set("Accept", "application/json")
	resp, err := s.openCode.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode forms: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []openCodeFormRow `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

func (s *Server) openCodePermissions(ctx context.Context, sessionID string) ([]openCodePermissionRow, error) {
	ep, err := s.openCode.endpoint()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.baseURL+"/api/session/"+sessionID+"/permission", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", ep.password)
	req.Header.Set("Accept", "application/json")
	resp, err := s.openCode.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode permissions: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []openCodePermissionRow `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

// alertWantedLocked maps an event kind to the owner's alert switches. It is
// called while h.mu is held, from assignLocked.
func (h *notificationHub) alertWantedLocked(kind string) bool {
	switch kind {
	case "session.exited", "session.lost":
		return h.settings.FinishedSessions
	case "run.blocked", "run.waiting":
		return h.settings.BlockedRuns
	case "opencode.permission", "opencode.question":
		return h.settings.Prompts
	case "opencode.replied":
		return h.settings.Replies
	default:
		// An unknown kind is attention until proven otherwise; a future event
		// must not become silent just because this switch did not know it.
		return true
	}
}

// assignLocked gives an event its sequence id, timestamp and alert verdict. An
// event that is recorded without being appended to the ring still needs a
// unique id, because a baseline returns it and the client deduplicates by id.
func (h *notificationHub) assignLocked(event Notification) Notification {
	h.seq++
	event.ID = h.seq
	event.At = time.Now().UTC()
	event.Alert = h.alertWantedLocked(event.Kind)
	return event
}

// notificationSettings returns the cached policy. It never reads the database:
// the ring is polled every few seconds and a settings read must not sit in that
// path.
func (h *notificationHub) notificationSettings() store.NotificationSettings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings
}

// setNotificationSettings persists and applies a policy. Persistence comes
// first: a settings change that cannot survive a restart must not silently
// govern this process either.
func (h *notificationHub) setNotificationSettings(settings store.NotificationSettings) error {
	if h.server != nil && h.server.store != nil {
		if err := h.server.store.PutNotificationSettings(settings); err != nil {
			return err
		}
	}
	h.mu.Lock()
	h.settings = settings
	h.mu.Unlock()
	return nil
}

// includeChildSessions reports whether child/subagent OpenCode chats may raise
// events at all. The ring is an activity list, so an excluded child is skipped
// entirely instead of being shown and then muted.
func (h *notificationHub) includeChildSessions() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings.ChildSessions
}

func (s *Server) handleGetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if s.notify == nil {
		writeJSON(w, http.StatusOK, store.DefaultNotificationSettings())
		return
	}
	writeJSON(w, http.StatusOK, s.notify.notificationSettings())
}

func (s *Server) handleNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var body store.NotificationSettings
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	if s.notify == nil {
		s.refuse(w, r, codeInvalidRequest, "notifications are not running")
		return
	}
	if err := s.notify.setNotificationSettings(body); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *notificationHub) appendLocked(event Notification) {
	h.events = append(h.events, event)
	if len(h.events) > notificationRing {
		h.events = append([]Notification(nil), h.events[len(h.events)-notificationRing:]...)
	}
}

func (h *notificationHub) emit(events []Notification) {
	if len(events) == 0 {
		return
	}
	h.mu.Lock()
	for i := range events {
		events[i] = h.assignLocked(events[i])
		h.appendLocked(events[i])
	}
	h.mu.Unlock()
	h.alert(events)
}

func (h *notificationHub) alert(events []Notification) {
	if h.desktop == nil || len(events) == 0 {
		return
	}
	for _, event := range events {
		if !event.Alert {
			continue
		}
		go h.desktop(event)
	}
}

// replaceLocked swaps the ring copy of an event for an enriched one, keeping
// its id so a client that already merged it updates in place instead of
// duplicating the row.
func (h *notificationHub) replaceLocked(event Notification) {
	for i := range h.events {
		if h.events[i].ID == event.ID {
			h.events[i] = event
			return
		}
	}
}

// dismiss removes events from the ring. It returns how many were there, so the
// handler can answer 204 without inventing a count.
func (h *notificationHub) dismiss(ids []int64) int {
	drop := make(map[int64]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := make([]Notification, 0, len(h.events))
	removed := 0
	for _, event := range h.events {
		if drop[event.ID] {
			removed++
			continue
		}
		kept = append(kept, event)
	}
	h.events = kept
	return removed
}

// clear empties the ring. A pending prompt stays in the open set, so it is not
// raised again until a new one replaces it.
func (h *notificationHub) clear() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	removed := len(h.events)
	h.events = nil
	return removed
}

// since returns events after the cursor. A missing cursor is a baseline
// request: a new client gets the current sequence without replaying old
// alerts. An explicit after=0 is not a baseline; it asks for every event the
// ring still holds, which is what a client that has never stored a cursor needs
// once it has recorded the baseline.
func (h *notificationHub) since(after int64, baseline bool) ([]Notification, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	next := h.seq
	if baseline {
		// A baseline is a phone-style "start from here" read: no replay. The web
		// client does not use it, it asks from cursor 0 because the ring is the
		// activity list it shows.
		return []Notification{}, next
	}
	out := make([]Notification, 0, len(h.events))
	for _, event := range h.events {
		if event.ID > after {
			out = append(out, event)
		}
	}
	return out, next
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notify == nil {
		writeJSON(w, http.StatusOK, struct {
			Next   int64          `json:"next"`
			Events []Notification `json:"events"`
		}{Events: []Notification{}})
		return
	}
	values, present := r.URL.Query()["after"]
	raw := ""
	if present && len(values) > 0 {
		raw = strings.TrimSpace(values[0])
	}
	baseline := raw == ""
	after := int64(0)
	if !baseline {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			s.refuse(w, r, codeInvalidRequest, "after must be a non-negative integer")
			return
		}
		after = value
	}
	events, next := s.notify.since(after, baseline)
	writeJSON(w, http.StatusOK, struct {
		Next   int64          `json:"next"`
		Events []Notification `json:"events"`
	}{Next: next, Events: events})
}

func limitNotification(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value))
	if len(value) > notificationBodyMax {
		value = value[:notificationBodyMax] + "…"
	}
	return value
}

func sendDesktopNotification(binary string, n Notification) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	title := limitNotification(n.Title)
	if title == "" {
		title = "DarkFactory"
	}
	body := limitNotification(n.Body)
	if body == "" {
		body = "An agent needs your attention."
	}
	urgency := "normal"
	if n.Kind == "run.blocked" || n.Kind == "opencode.permission" {
		urgency = "critical"
	}
	// Arguments are passed directly, never through a shell. "--" keeps a title
	// that starts with "-" from being parsed as a notify-send option.
	cmd := exec.CommandContext(ctx, binary, "-a", "DarkFactory", "-u", urgency, "-t", "10000", "--", title, body)
	_ = cmd.Run()
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

// handleDismissNotifications removes the rows the owner closed. It is a
// mutation because a reload must not resurrect what was cleared, and the ring
// is shared by every client of this daemon.
func (s *Server) handleDismissNotifications(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 200 {
		s.refuse(w, r, codeInvalidRequest, "send between 1 and 200 event ids")
		return
	}
	if s.notify != nil {
		s.notify.dismiss(body.IDs)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleClearNotifications empties the activity ring.
func (s *Server) handleClearNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notify != nil {
		s.notify.clear()
	}
	w.WriteHeader(http.StatusNoContent)
}
