package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
)

const (
	openCodeMaxResponse = 16 << 20
	openCodeMaxPrompt   = 64 << 10
	openCodeMaxTitle    = 200
	openCodeMaxAgent    = 80
	openCodeMaxSearch   = 200
	openCodeMaxCursor   = 256
	openCodeMaxReply    = 4 << 10
	openCodeMaxCommand  = 64
)

var openCodeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// openCodeClient is the daemon's private client for the local OpenCode service.
// The browser never sees the service password or the loopback API URL.
//
// Two engines speak this API: upstream `opencode` and the Rust rewrite `ocd`.
// Both register the URL and password they actually bound in a state file, and
// that file changes when the service restarts on a new port. The client
// therefore resolves the newest reachable registration instead of trusting a
// single frozen URL: a stale file used to make `/api/v1/state` report OpenCode
// as available while every chat request hit a dead port.
type openCodeClient struct {
	configuredURL          string
	configuredPasswordFile string
	// discover is true for the default configuration. An explicit
	// opencodeAPIURL is the owner's override and is used as given, which also
	// keeps tests hermetic.
	discover bool
	http     *http.Client
	stream   *http.Client
	probe    *http.Client

	resolveMu sync.Mutex
	active    openCodeEndpoint
	resolved  time.Time

	mu       sync.Mutex
	password string
	modTime  time.Time
	size     int64
}

// openCodeEndpoint is one resolved service: where to send the request and the
// password that Basic-authenticates it. A registration carries the password
// inline; the configured fallback reads it from a file.
type openCodeEndpoint struct {
	baseURL      string
	password     string
	passwordFile string
}

type openCodeCredentials struct {
	Password string `json:"password"`
}

// openCodeRegistration is the state-file row both engines write. Only url and
// password matter here; id/version/pid are informational.
type openCodeRegistration struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	PID      int    `json:"pid"`
	Version  string `json:"version"`
}

const openCodeDiscoverTTL = 10 * time.Second

func newOpenCodeClient(cfg config.Config) *openCodeClient {
	if strings.TrimSpace(cfg.OpenCodeAPIURL) == "" || strings.TrimSpace(cfg.OpenCodePasswordFile) == "" {
		return nil
	}
	return &openCodeClient{
		configuredURL:          strings.TrimRight(cfg.OpenCodeAPIURL, "/"),
		configuredPasswordFile: cfg.OpenCodePasswordFile,
		discover:               strings.TrimRight(cfg.OpenCodeAPIURL, "/") == config.Default().OpenCodeAPIURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// A redirect could move the request (and the Basic credential) to a
			// different origin. The OpenCode service is local and does not need
			// redirects.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		// The event stream is long-lived, so it must not inherit the 30 s
		// request timeout. Its context is the browser request's context.
		stream: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		// A registration is only useful if the port answers; bound the probe so
		// a dead file cannot stall the first request for 30 seconds.
		probe: &http.Client{
			Timeout: 700 * time.Millisecond,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// openCodeRegistrationFiles lists the state rows newest-first sources. Both
// engines use the same folder; the v1 local-install file is kept as a fallback
// for an older install that never wrote service.json.
func openCodeRegistrationFiles() []string {
	stateDir := os.Getenv("XDG_STATE_HOME")
	configDir := os.Getenv("XDG_CONFIG_HOME")
	home, _ := os.UserHomeDir()
	if stateDir == "" {
		stateDir = filepath.Join(home, ".local", "state")
	}
	if configDir == "" {
		configDir = filepath.Join(home, ".config")
	}
	candidates := []string{
		filepath.Join(stateDir, "opencode", "service.json"),
		filepath.Join(stateDir, "opencode", "service-v2.json"),
		filepath.Join(stateDir, "opencode", "service-local-install.json"),
		filepath.Join(configDir, "opencode", "service-local-install.json"),
	}
	seen := make(map[string]bool, len(candidates))
	files := candidates[:0]
	for _, path := range candidates {
		if seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	return files
}

// loopbackServiceURL is the security gate for a discovered registration. A
// state file is writable by the same user, but it must never be able to send
// the service password to another host.
func loopbackServiceURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *openCodeClient) probeRegistration(baseURL, password string) bool {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/session?limit=1", nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("opencode", password)
	req.Header.Set("Accept", "application/json")
	resp, err := c.probe.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	// Require the session-list shape, not any 200. A stale registration may
	// point at a port another local server now owns; its password must not be
	// treated as an OpenCode credential.
	var listed struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listed); err != nil {
		return false
	}
	return listed.Data != nil
}

// discoverEndpoint reads every registration and returns the newest one whose
// port answers with the password it advertises. Ties are broken by file mtime,
// so a restarted service wins over the file it replaced.
func (c *openCodeClient) discoverEndpoint() (openCodeEndpoint, error) {
	type candidate struct {
		path     string
		modTime  time.Time
		priority int
	}
	// The canonical current-service file wins over the legacy local-install
	// row even when the legacy file was touched more recently. Both engines
	// rewrite service.json; local-install is an older artifact that can easily
	// outlive the service it names.
	priorities := map[string]int{}
	for i, path := range openCodeRegistrationFiles() {
		priorities[path] = i
	}
	candidates := make([]candidate, 0, 4)
	probed := 0
	for _, path := range openCodeRegistrationFiles() {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{path: path, modTime: info.ModTime(), priority: priorities[path]})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	for _, item := range candidates {
		if probed >= 3 {
			break
		}
		raw, err := os.ReadFile(item.path)
		if err != nil {
			continue
		}
		var reg openCodeRegistration
		if err := json.Unmarshal(raw, &reg); err != nil {
			continue
		}
		password := strings.TrimSpace(reg.Password)
		baseURL := strings.TrimRight(strings.TrimSpace(reg.URL), "/")
		if password == "" || baseURL == "" || !loopbackServiceURL(baseURL) {
			continue
		}
		probed++
		if c.probeRegistration(baseURL, password) {
			return openCodeEndpoint{baseURL: baseURL, password: password}, nil
		}
	}
	return openCodeEndpoint{}, fmt.Errorf("no reachable OpenCode service registration")
}

// endpoint resolves the live service. A discovered endpoint is cached for a
// few seconds so a poll does not probe on every request; the configured
// fallback is used only when no registration answers.
func (c *openCodeClient) endpoint() (openCodeEndpoint, error) {
	if c == nil {
		return openCodeEndpoint{}, fmt.Errorf("OpenCode is not configured")
	}
	if !c.discover {
		return c.fileEndpoint(c.configuredURL, c.configuredPasswordFile)
	}
	c.resolveMu.Lock()
	defer c.resolveMu.Unlock()
	if c.active.baseURL != "" && time.Since(c.resolved) < openCodeDiscoverTTL {
		return c.active, nil
	}
	resolved, err := c.discoverEndpoint()
	if err != nil {
		// A default-config daemon may still be pointed at an explicit service
		// before any engine has registered. Try the configured fallback, but
		// only if it actually answers; otherwise report unavailable instead of
		// repeating the old "opencode: true on a dead port" lie.
		fallback, fallbackErr := c.fileEndpoint(c.configuredURL, c.configuredPasswordFile)
		if fallbackErr == nil && c.probeRegistration(fallback.baseURL, fallback.password) {
			resolved = fallback
		} else {
			c.active = openCodeEndpoint{}
			c.resolved = time.Now()
			return openCodeEndpoint{}, err
		}
	}
	c.active = resolved
	c.resolved = time.Now()
	return resolved, nil
}

// fileEndpoint resolves the configured URL and password file. The file is
// re-read only when its mtime or size changed.
func (c *openCodeClient) fileEndpoint(baseURL, passwordFile string) (openCodeEndpoint, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(passwordFile) == "" {
		return openCodeEndpoint{}, fmt.Errorf("OpenCode is not configured")
	}
	info, err := os.Stat(passwordFile)
	if err != nil {
		return openCodeEndpoint{}, fmt.Errorf("read OpenCode credentials: %w", err)
	}
	if info.IsDir() {
		return openCodeEndpoint{}, fmt.Errorf("OpenCode credentials path is a directory")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.password != "" && info.ModTime().Equal(c.modTime) && info.Size() == c.size {
		return openCodeEndpoint{baseURL: strings.TrimRight(baseURL, "/"), password: c.password, passwordFile: passwordFile}, nil
	}

	raw, err := os.ReadFile(passwordFile)
	if err != nil {
		return openCodeEndpoint{}, fmt.Errorf("read OpenCode credentials: %w", err)
	}
	var creds openCodeCredentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return openCodeEndpoint{}, fmt.Errorf("parse OpenCode credentials: %w", err)
	}
	password := strings.TrimSpace(creds.Password)
	if password == "" {
		return openCodeEndpoint{}, fmt.Errorf("OpenCode credentials have no password")
	}
	c.password = password
	c.modTime = info.ModTime()
	c.size = info.Size()
	return openCodeEndpoint{baseURL: strings.TrimRight(baseURL, "/"), password: password, passwordFile: passwordFile}, nil
}

// available reports whether a live service answers right now. The state poll
// calls this, so it must not claim a dead port is usable.
func (c *openCodeClient) available() bool {
	if c == nil {
		return false
	}
	_, err := c.endpoint()
	return err == nil
}

func (c *openCodeClient) request(r *http.Request, method, path string, body []byte, accept string) (*http.Request, error) {
	ep, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, ep.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", ep.password)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "darkfactory")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *openCodeClient) do(r *http.Request, method, path string, body []byte) (*http.Response, error) {
	req, err := c.request(r, method, path, body, "application/json")
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

func (c *openCodeClient) doStream(r *http.Request, path string) (*http.Response, error) {
	req, err := c.request(r, http.MethodGet, path, nil, "text/event-stream")
	if err != nil {
		return nil, err
	}
	return c.stream.Do(req)
}

func (s *Server) openCodeEnabled() bool {
	return s.openCode != nil && s.openCode.available()
}

func (s *Server) handleOpenCodeSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := openCodeLimit(query.Get("limit"), 50, 100)
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	order := strings.TrimSpace(query.Get("order"))
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		s.refuse(w, r, codeInvalidRequest, "order must be asc or desc")
		return
	}
	search := strings.TrimSpace(query.Get("search"))
	if len(search) > openCodeMaxSearch {
		s.refuse(w, r, codeInvalidRequest, "search is longer than 200 bytes")
		return
	}

	values := url.Values{}
	values.Set("limit", strconv.Itoa(limit))
	values.Set("order", order)
	if search != "" {
		values.Set("search", search)
	}
	s.proxyOpenCode(w, r, http.MethodGet, "/api/session?"+values.Encode(), nil)
}

func (s *Server) handleOpenCodeMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !openCodeID.MatchString(id) {
		s.refuse(w, r, codeInvalidID, "invalid session id")
		return
	}
	query := r.URL.Query()
	limit, err := openCodeLimit(query.Get("limit"), 100, 200)
	if err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	order := strings.TrimSpace(query.Get("order"))
	if order == "" {
		order = "asc"
	}
	if order != "asc" && order != "desc" {
		s.refuse(w, r, codeInvalidRequest, "order must be asc or desc")
		return
	}
	cursor := strings.TrimSpace(query.Get("cursor"))
	if len(cursor) > openCodeMaxCursor {
		s.refuse(w, r, codeInvalidRequest, "cursor is longer than 256 bytes")
		return
	}

	values := url.Values{}
	values.Set("limit", strconv.Itoa(limit))
	// OpenCode's cursor already carries the order. Sending order again makes
	// the service reject the request with InvalidCursorError.
	if cursor != "" {
		values.Set("cursor", cursor)
	} else {
		values.Set("order", order)
	}
	path := "/api/session/" + url.PathEscape(id) + "/message?" + values.Encode()
	s.proxyOpenCode(w, r, http.MethodGet, path, nil)
}

type openCodePromptRequest struct {
	Text string `json:"text"`
}

func (s *Server) handleOpenCodePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !openCodeID.MatchString(id) {
		s.refuse(w, r, codeInvalidID, "invalid session id")
		return
	}
	var body openCodePromptRequest
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		s.refuse(w, r, codeInvalidRequest, "text is required")
		return
	}
	if len(text) > openCodeMaxPrompt {
		s.refuse(w, r, codeRequestBodyTooLarge, "prompt is larger than 64 KiB")
		return
	}
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := "/api/session/" + url.PathEscape(id) + "/prompt"
	s.proxyOpenCode(w, r, http.MethodPost, path, payload)
}

func (s *Server) handleOpenCodeInterrupt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !openCodeID.MatchString(id) {
		s.refuse(w, r, codeInvalidID, "invalid session id")
		return
	}
	path := "/api/session/" + url.PathEscape(id) + "/interrupt"
	s.proxyOpenCode(w, r, http.MethodPost, path, nil)
}

func (s *Server) handleOpenCodePermissions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !openCodeID.MatchString(id) {
		s.refuse(w, r, codeInvalidID, "invalid session id")
		return
	}
	path := "/api/session/" + url.PathEscape(id) + "/permission"
	s.proxyOpenCode(w, r, http.MethodGet, path, nil)
}

type openCodePermissionReply struct {
	Decision string `json:"decision"`
	Message  string `json:"message"`
}

func (s *Server) handleOpenCodePermissionReply(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	requestID := r.PathValue("requestID")
	if !openCodeID.MatchString(id) || !openCodeID.MatchString(requestID) {
		s.refuse(w, r, codeInvalidID, "invalid permission id")
		return
	}
	var body openCodePermissionReply
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	decision := strings.TrimSpace(body.Decision)
	if decision != "once" && decision != "always" && decision != "reject" {
		s.refuse(w, r, codeInvalidRequest, "decision must be once, always, or reject")
		return
	}
	message := strings.TrimSpace(body.Message)
	if len(message) > openCodeMaxReply {
		s.refuse(w, r, codeRequestBodyTooLarge, "message is larger than 4 KiB")
		return
	}
	payload, err := json.Marshal(map[string]any{"decision": decision, "message": message})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := "/api/session/" + url.PathEscape(id) + "/permission/" + url.PathEscape(requestID) + "/reply"
	s.proxyOpenCode(w, r, http.MethodPost, path, payload)
}

// handleOpenCodeCommands lists the commands OpenCode has registered. Commands
// load from the location's config, so the location must be one of this daemon's
// worktrees: the browser can name a worktree, never an arbitrary directory.
func (s *Server) handleOpenCodeCommands(w http.ResponseWriter, r *http.Request) {
	path := "/api/command"
	if location := strings.TrimSpace(r.URL.Query().Get("location")); location != "" {
		known, err := s.knownWorktreePath(location)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !known {
			s.refuse(w, r, codeUnknownLocation, "")
			return
		}
		// OpenCode v2 parses the command location as an object query parameter;
		// a bare `location=<path>` is rejected with 400, which left the phone's
		// "/" completer empty and logged an error on every chat switch.
		path += "?location%5Bdirectory%5D=" + url.QueryEscape(location)
	}
	s.proxyOpenCode(w, r, http.MethodGet, path, nil)
}

func (s *Server) knownWorktreePath(path string) (bool, error) {
	clean := filepath.Clean(path)
	root := filepath.Clean(s.cfg.WorktreeRoot)
	if root != "" && root != "." && (clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))) {
		return true, nil
	}
	worktrees, err := s.store.ListWorktrees()
	if err != nil {
		return false, err
	}
	for _, wt := range worktrees {
		// OpenCode may hand back a cleaned path; compare the same way.
		if filepath.Clean(wt.Path) == clean {
			return true, nil
		}
	}
	return false, nil
}

// worktreeIDForDirectory maps a chat's working directory back to the workspace
// row that owns it. OpenCode reports the directory it was created in, which is
// a registered worktree; an empty or unknown directory returns "", and the
// caller shows the event without a workspace link rather than guessing.
func (s *Server) worktreeIDForDirectory(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || s.store == nil {
		return ""
	}
	clean := filepath.Clean(dir)
	worktrees, err := s.store.ListWorktrees()
	if err != nil {
		return ""
	}
	best, bestLen := "", 0
	for _, wt := range worktrees {
		path := filepath.Clean(wt.Path)
		if path == clean {
			return wt.ID
		}
		// A chat may have been created in a subdirectory of a workspace.
		if strings.HasPrefix(clean, path+string(filepath.Separator)) && len(path) > bestLen {
			best, bestLen = wt.ID, len(path)
		}
	}
	return best
}

type openCodeCommandRequest struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// handleOpenCodeCommand runs a registered slash command. OpenCode names the
// payload field "text", not "arguments"; files, agents, skills and delivery are
// optional and stay unset here.
func (s *Server) handleOpenCodeCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !openCodeID.MatchString(id) {
		s.refuse(w, r, codeInvalidID, "invalid session id")
		return
	}
	var body openCodeCommandRequest
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > openCodeMaxCommand || strings.ContainsAny(name, " \t\r\n/") {
		s.refuse(w, r, codeInvalidRequest, "invalid command name")
		return
	}
	if len(body.Text) > openCodeMaxPrompt {
		s.refuse(w, r, codeRequestBodyTooLarge, "command text is larger than 64 KiB")
		return
	}
	payload, err := json.Marshal(map[string]string{"name": name, "text": body.Text})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path := "/api/session/" + url.PathEscape(id) + "/command"
	s.proxyOpenCode(w, r, http.MethodPost, path, payload)
}

// handleOpenCodeEvents proxies OpenCode's volatile event stream. The browser
// uses it only as a low-latency hint and still has a slow fallback poll.
func (s *Server) handleOpenCodeEvents(w http.ResponseWriter, r *http.Request) {
	if s.openCode == nil {
		s.refuse(w, r, codeOpenCodeDisabled, "")
		return
	}
	resp, err := s.openCode.doStream(r, "/api/event")
	if err != nil {
		s.log.Error("opencode event stream", "error", err)
		s.refuse(w, r, codeOpenCodeUnavailable, "")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.Error("opencode rejected event stream", "status", resp.StatusCode)
		s.refuse(w, r, codeOpenCodeFailed, "")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.refuse(w, r, codeStreamingUnsupported, "")
		return
	}
	// The OpenCode event stream is long-lived; clear the listener deadlines.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	buffer := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			if readErr != io.EOF {
				s.log.Error("opencode event stream read", "error", readErr)
			}
			return
		}
	}
}

type openCodeCreateRequest struct {
	WorktreeID string `json:"worktreeId"`
	Title      string `json:"title"`
	Agent      string `json:"agent"`
}

func (s *Server) handleCreateOpenCodeSession(w http.ResponseWriter, r *http.Request) {
	var body openCodeCreateRequest
	if err := decodeJSON(r, &body); err != nil {
		s.refuse(w, r, codeInvalidRequest, err.Error())
		return
	}

	worktreeID := strings.TrimSpace(body.WorktreeID)
	if worktreeID == "" {
		s.refuse(w, r, codeWorktreeRequired, "")
		return
	}
	if s.store == nil {
		s.refuse(w, r, codeStoreUnavailable, "")
		return
	}
	wt, err := s.store.GetWorktree(worktreeID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	absolute, err := filepath.Abs(wt.Path)
	if err != nil {
		s.refuse(w, r, codeInvalidPath, "invalid worktree directory")
		return
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		s.refuse(w, r, codeNotADirectory, "worktree path is not a directory")
		return
	}

	title := strings.TrimSpace(body.Title)
	if len(title) > openCodeMaxTitle {
		s.refuse(w, r, codeInvalidRequest, "title is longer than 200 bytes")
		return
	}
	agent := strings.TrimSpace(body.Agent)
	if len(agent) > openCodeMaxAgent {
		s.refuse(w, r, codeInvalidRequest, "agent is longer than 80 bytes")
		return
	}

	payload := map[string]any{
		"location": map[string]string{"directory": absolute},
	}
	if title != "" {
		payload["title"] = title
	}
	if agent != "" {
		payload["agent"] = agent
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.proxyOpenCode(w, r, http.MethodPost, "/api/session", raw)
}

func (s *Server) proxyOpenCode(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	if s.openCode == nil {
		s.refuse(w, r, codeOpenCodeDisabled, "")
		return
	}
	resp, err := s.openCode.do(r, method, path, body)
	if err != nil {
		s.log.Error("opencode request", "method", method, "path", path, "error", err)
		s.refuse(w, r, codeOpenCodeUnavailable, "")
		return
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, openCodeMaxResponse+1))
	if err != nil {
		s.log.Error("opencode response", "method", method, "path", path, "error", err)
		s.refuse(w, r, codeOpenCodeUnavailable, "")
		return
	}
	if len(raw) > openCodeMaxResponse {
		s.log.Error("opencode response too large", "method", method, "path", path)
		s.refuse(w, r, codeOpenCodeTooLarge, "")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.log.Error("opencode rejected request", "method", method, "path", path, "status", resp.StatusCode)
		status := http.StatusBadGateway
		switch resp.StatusCode {
		case http.StatusBadRequest:
			status = http.StatusBadRequest
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusConflict:
			status = http.StatusConflict
		case http.StatusTooManyRequests:
			status = http.StatusTooManyRequests
		}
		s.refuse(w, r, codeOpenCodeStatus(status), "")
		return
	}
	if resp.StatusCode == http.StatusNoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}

// codeOpenCodeStatus keeps a stable refusal code per upstream class. The
// registry maps each to the same HTTP status the proxy used to pass through.
func codeOpenCodeStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return codeOpenCodeBadRequest
	case http.StatusNotFound:
		return codeOpenCodeNotFound
	case http.StatusConflict:
		return codeOpenCodeConflict
	case http.StatusTooManyRequests:
		return codeOpenCodeRateLimited
	default:
		return codeOpenCodeFailed
	}
}

func openCodeLimit(raw string, fallback, maximum int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("limit must be a number")
	}
	if value < 1 {
		value = 1
	}
	if value > maximum {
		value = maximum
	}
	return value, nil
}
