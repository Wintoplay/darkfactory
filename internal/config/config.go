package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wintoplay/darkfactory/internal/policy"
)

const (
	defaultListen         = "127.0.0.1:8787"
	defaultRingBytes      = 4 << 20
	defaultConfigName     = "config.json"
	defaultOpenCodeAPIURL = "http://127.0.0.1:26706"
	// defaultLedgerRetentionDays bounds state.db growth: an input row per
	// keystroke is useful for a month of history and worthless after that.
	defaultLedgerRetentionDays = 30

	// Listener deadlines. The read and write defaults are deliberately finite:
	// a stalled request or response should not pin a goroutine forever. The
	// stream handlers clear their own deadlines, so this does not truncate a
	// terminal or event stream.
	defaultReadHeaderTimeout = "10s"
	defaultReadTimeout       = "60s"
	defaultWriteTimeout      = "60s"
	defaultIdleTimeout       = "2m"
	defaultMaxHeaderBytes    = 1 << 20
)

type AgentPreset struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Containment string            `json:"containment,omitempty"`
	// JailEnvAllow and JailPathAllow add to the global jail allow lists for
	// this preset. They only matter when containment resolves to jail.
	JailEnvAllow  []string `json:"jailEnvAllow,omitempty"`
	JailPathAllow []string `json:"jailPathAllow,omitempty"`
}

type Config struct {
	Listen       string `json:"listen"`
	ConfigDir    string `json:"configDir"`
	StateDir     string `json:"stateDir"`
	WorktreeRoot string `json:"worktreeRoot"`
	HomeRoot     string `json:"homeRoot"`
	Containment  string `json:"containment"`
	// JailScrubEnv gives a jailed session a minimal environment instead of
	// inheriting the daemon's. It strips desktop, SSH-agent, keyring and cloud
	// credentials; per-preset Env values are still passed through. It only
	// applies when containment actually resolves to jail, and it is off by
	// default so an existing jail keeps its current behaviour.
	JailScrubEnv bool `json:"jailScrubEnv,omitempty"`
	// JailHideCredentials adds a built-in list of credential, browser and
	// keyring paths to the jail's tmpfs masks. It is off by default because a
	// jailed OpenCode currently reads the real service password file; enable it
	// only for agents that do not need that file, or give those agents a
	// per-worktree config first.
	JailHideCredentials bool `json:"jailHideCredentials,omitempty"`
	// JailHiddenPaths lists extra absolute paths (or ~/ paths) that a jailed
	// session must not see. Paths that do not exist are ignored, and duplicates
	// are removed. The daemon's config and state directories are always masked.
	JailHiddenPaths []string `json:"jailHiddenPaths,omitempty"`
	// JailEnvAllow copies named variables from the daemon environment into a
	// scrubbed jail. The names are visible in config and state; the values are
	// not written anywhere. HOME and TMPDIR are refused because the jail
	// projects its own.
	JailEnvAllow []string `json:"jailEnvAllow,omitempty"`
	// JailPathAllow exempts exact paths from the effective mask list, including
	// the built-in credential masks. It does not punch a hole through a masked
	// parent directory; mask the exact path instead.
	JailPathAllow []string `json:"jailPathAllow,omitempty"`
	RingBytes     int      `json:"ringBytes"`
	// DesktopNotify shows local freedesktop notifications for attention
	// events (a blocked run, a finished/lost session, a pending OpenCode
	// permission). It is local D-Bus only; it never calls a vendor push
	// service or a remote endpoint.
	DesktopNotify bool `json:"desktopNotify"`
	// LedgerRetentionDays bounds the decision ledger: rows older than this are
	// pruned at startup and every few hours, leaving a marker row. Zero keeps
	// everything, which is what a debugging session wants.
	LedgerRetentionDays int `json:"ledgerRetentionDays"`
	// HTTP timeouts for the listener. Each is a duration string ("30s", "2m")
	// or "never"/"0" to disable that deadline. ReadHeaderTimeout bounds a
	// client that stalls mid-headers; ReadTimeout bounds a stalled body;
	// WriteTimeout bounds a response that never finishes; IdleTimeout bounds a
	// kept-alive connection between requests. Long-lived streams clear the
	// read and write deadlines for their own request, so a nonzero default
	// does not cut a terminal or event stream.
	ReadHeaderTimeout string `json:"readHeaderTimeout,omitempty"`
	ReadTimeout       string `json:"readTimeout,omitempty"`
	WriteTimeout      string `json:"writeTimeout,omitempty"`
	IdleTimeout       string `json:"idleTimeout,omitempty"`
	// MaxHeaderBytes bounds one request head. Zero keeps the Go default.
	MaxHeaderBytes int                    `json:"maxHeaderBytes,omitempty"`
	Agents         map[string]AgentPreset `json:"agents"`
	// AllowedHosts extends the Host allowlist beyond loopback, the listen host
	// and publicURL. It is not a network-source policy: a public peer is still
	// refused on a non-loopback listener.
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	// AllowPublicSources accepts direct peers that are neither loopback, LAN
	// (RFC1918/ULA/link-local/on-link) nor Tailscale. It is off by default
	// because the daemon serves plaintext HTTP and has no TLS of its own: a
	// public bind belongs behind Tailscale Serve, an SSH tunnel or a TLS
	// gateway, not on the open internet.
	AllowPublicSources bool   `json:"allowPublicSources,omitempty"`
	PublicURL          string `json:"publicURL,omitempty"`
	OpenCodeURL        string `json:"opencodeURL,omitempty"`
	// OpenCodeAPIURL is the local OpenCode service the daemon proxies. It is
	// deliberately separate from OpenCodeURL: the link may be a Tailscale URL,
	// while the API credential must stay on loopback.
	OpenCodeAPIURL       string `json:"opencodeAPIURL,omitempty"`
	OpenCodePasswordFile string `json:"opencodePasswordFile,omitempty"`

	Token     string `json:"-"`
	TokenPath string `json:"-"`

	// PolicyPath is where the policy file lives; a missing file means the
	// built-in default. Policy is the parsed result: `serve` loads it so a
	// malformed file refuses startup instead of being ignored (GV-5).
	PolicyPath string         `json:"-"`
	Policy     *policy.Policy `json:"-"`

	// InstanceSecret salts the at-rest key for browser sessions, so a bbolt
	// file copied to another machine (or restored without this file) cannot be
	// turned back into working cookies. It is a credential: 0600, never in
	// config.json, never logged.
	InstanceSecret     string `json:"-"`
	InstanceSecretPath string `json:"-"`
}

func Default() Config {
	return Config{
		Listen:               defaultListen,
		RingBytes:            defaultRingBytes,
		DesktopNotify:        true,
		Containment:          "off",
		LedgerRetentionDays:  defaultLedgerRetentionDays,
		ReadHeaderTimeout:    defaultReadHeaderTimeout,
		ReadTimeout:          defaultReadTimeout,
		WriteTimeout:         defaultWriteTimeout,
		IdleTimeout:          defaultIdleTimeout,
		MaxHeaderBytes:       defaultMaxHeaderBytes,
		OpenCodeAPIURL:       defaultOpenCodeAPIURL,
		OpenCodePasswordFile: defaultOpenCodePasswordFile(),
		Agents: map[string]AgentPreset{
			"claude":   {Command: "claude"},
			"codex":    {Command: "codex"},
			"opencode": {Command: "opencode"},
			// The Rust rewrite of OpenCode 2. It speaks the same service API, so
			// the chat panel works with either engine; this preset only makes
			// launching its TUI a first-class choice.
			"ocd": {Command: "ocd"},
		},
	}
}

// HTTPTimeouts is the parsed listener deadline set. A zero duration means that
// deadline is disabled; MaxHeaderBytes zero means the Go default.
type HTTPTimeouts struct {
	ReadHeader     time.Duration
	Read           time.Duration
	Write          time.Duration
	Idle           time.Duration
	MaxHeaderBytes int
}

// Timeouts parses the configured duration strings. normalize() validates them
// at startup, so this cannot fail for a config that Load accepted; it returns
// the error anyway for a Config built by hand.
func (c Config) Timeouts() (HTTPTimeouts, error) {
	readHeader, err := parseTimeout(c.ReadHeaderTimeout, "readHeaderTimeout")
	if err != nil {
		return HTTPTimeouts{}, err
	}
	read, err := parseTimeout(c.ReadTimeout, "readTimeout")
	if err != nil {
		return HTTPTimeouts{}, err
	}
	write, err := parseTimeout(c.WriteTimeout, "writeTimeout")
	if err != nil {
		return HTTPTimeouts{}, err
	}
	idle, err := parseTimeout(c.IdleTimeout, "idleTimeout")
	if err != nil {
		return HTTPTimeouts{}, err
	}
	if c.MaxHeaderBytes < 0 {
		return HTTPTimeouts{}, fmt.Errorf("maxHeaderBytes cannot be negative")
	}
	return HTTPTimeouts{
		ReadHeader:     readHeader,
		Read:           read,
		Write:          write,
		Idle:           idle,
		MaxHeaderBytes: c.MaxHeaderBytes,
	}, nil
}

// parseTimeout accepts a Go duration or the words "never"/"off" (and "0") for
// no deadline. An empty value means the same thing, so a config written before
// the field existed keeps working.
func parseTimeout(value, field string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "never", "off", "0":
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration or \"never\"", field, value)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s must not be negative", field)
	}
	return duration, nil
}

func Load() (Config, error) {
	cfg := Default()
	cfg.ConfigDir = envPath("XDG_CONFIG_HOME", ".config", "darkfactory")
	cfg.StateDir = envPath("XDG_STATE_HOME", ".local/state", "darkfactory")
	cfg.WorktreeRoot = envPath("XDG_DATA_HOME", ".local/share", "darkfactory/worktrees")
	cfg.HomeRoot = envPath("XDG_DATA_HOME", ".local/share", "darkfactory/homes")
	cfg.TokenPath = filepath.Join(cfg.ConfigDir, "token")
	cfg.InstanceSecretPath = filepath.Join(cfg.ConfigDir, "instance-secret")
	cfg.PolicyPath = filepath.Join(cfg.ConfigDir, "policy.json")

	configPath := filepath.Join(cfg.ConfigDir, defaultConfigName)
	if raw, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", configPath, err)
	}

	if err := cfg.normalize(); err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(cfg.WorktreeRoot, 0o700); err != nil {
		return Config{}, fmt.Errorf("create worktree root: %w", err)
	}
	if err := os.MkdirAll(cfg.HomeRoot, 0o700); err != nil {
		return Config{}, fmt.Errorf("create home root: %w", err)
	}

	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := writeConfig(configPath, cfg); err != nil {
			return Config{}, err
		}
	}

	token, err := ensureToken(cfg.TokenPath)
	if err != nil {
		return Config{}, err
	}
	cfg.Token = token
	secret, err := ensureInstanceSecret(cfg.InstanceSecretPath)
	if err != nil {
		return Config{}, err
	}
	cfg.InstanceSecret = secret
	return cfg, nil
}

func (c *Config) normalize() error {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.RingBytes <= 0 {
		c.RingBytes = defaultRingBytes
	}
	if c.LedgerRetentionDays < 0 {
		return fmt.Errorf("ledgerRetentionDays cannot be negative; use 0 to keep the ledger forever")
	}
	for _, item := range []struct{ field, value string }{
		{"readHeaderTimeout", c.ReadHeaderTimeout},
		{"readTimeout", c.ReadTimeout},
		{"writeTimeout", c.WriteTimeout},
		{"idleTimeout", c.IdleTimeout},
	} {
		if _, err := parseTimeout(item.value, item.field); err != nil {
			return err
		}
	}
	if c.MaxHeaderBytes < 0 {
		return fmt.Errorf("maxHeaderBytes cannot be negative")
	}
	if c.MaxHeaderBytes == 0 {
		c.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if c.ConfigDir == "" {
		c.ConfigDir = envPath("XDG_CONFIG_HOME", ".config", "darkfactory")
	}
	if c.StateDir == "" {
		c.StateDir = envPath("XDG_STATE_HOME", ".local/state", "darkfactory")
	}
	if c.WorktreeRoot == "" {
		c.WorktreeRoot = envPath("XDG_DATA_HOME", ".local/share", "darkfactory/worktrees")
	}
	if c.HomeRoot == "" {
		c.HomeRoot = envPath("XDG_DATA_HOME", ".local/share", "darkfactory/homes")
	}
	c.Containment = strings.TrimSpace(c.Containment)
	switch c.Containment {
	case "":
		c.Containment = "off"
	case "auto", "off", "jail":
	default:
		return fmt.Errorf("containment must be auto, off or jail")
	}
	var err error
	if c.JailHiddenPaths, err = normalizePathList(c.JailHiddenPaths); err != nil {
		return fmt.Errorf("jailHiddenPaths: %w", err)
	}
	if c.JailPathAllow, err = normalizePathList(c.JailPathAllow); err != nil {
		return fmt.Errorf("jailPathAllow: %w", err)
	}
	if c.JailEnvAllow, err = normalizeEnvNames(c.JailEnvAllow); err != nil {
		return fmt.Errorf("jailEnvAllow: %w", err)
	}
	if c.TokenPath == "" {
		c.TokenPath = filepath.Join(c.ConfigDir, "token")
	}
	if c.InstanceSecretPath == "" {
		c.InstanceSecretPath = filepath.Join(c.ConfigDir, "instance-secret")
	}
	if c.Agents == nil {
		c.Agents = Default().Agents
	}
	hosts := c.AllowedHosts[:0]
	for _, host := range c.AllowedHosts {
		if host = strings.TrimSpace(host); host != "" {
			hosts = append(hosts, host)
		}
	}
	c.AllowedHosts = hosts
	if c.PublicURL != "" {
		publicURL, err := normalizePublicURL(c.PublicURL)
		if err != nil {
			return err
		}
		c.PublicURL = publicURL
	}
	if c.OpenCodeURL != "" {
		openCodeURL, err := normalizeOpenCodeURL(c.OpenCodeURL)
		if err != nil {
			return err
		}
		c.OpenCodeURL = openCodeURL
	}
	if c.OpenCodeAPIURL == "" {
		c.OpenCodeAPIURL = defaultOpenCodeAPIURL
	}
	openCodeAPIURL, err := normalizeOpenCodeAPIURL(c.OpenCodeAPIURL)
	if err != nil {
		return err
	}
	c.OpenCodeAPIURL = openCodeAPIURL
	if c.OpenCodePasswordFile == "" {
		c.OpenCodePasswordFile = defaultOpenCodePasswordFile()
	}
	passwordFile, err := normalizeFilePath(c.OpenCodePasswordFile)
	if err != nil {
		return fmt.Errorf("opencodePasswordFile: %w", err)
	}
	c.OpenCodePasswordFile = passwordFile
	for name, preset := range c.Agents {
		if strings.TrimSpace(preset.Command) == "" {
			return fmt.Errorf("agent %q has no command", name)
		}
		switch strings.TrimSpace(preset.Containment) {
		case "", "auto", "off", "jail":
		default:
			return fmt.Errorf("agent %q containment must be auto, off or jail", name)
		}
		if preset.JailEnvAllow, err = normalizeEnvNames(preset.JailEnvAllow); err != nil {
			return fmt.Errorf("agent %q jailEnvAllow: %w", name, err)
		}
		if preset.JailPathAllow, err = normalizePathList(preset.JailPathAllow); err != nil {
			return fmt.Errorf("agent %q jailPathAllow: %w", name, err)
		}
		c.Agents[name] = preset
	}
	return nil
}

func normalizePublicURL(raw string) (string, error) {
	return normalizeExternalURL(raw, "publicURL")
}

func normalizeOpenCodeURL(raw string) (string, error) {
	return normalizeExternalURL(raw, "opencodeURL")
}

// normalizeOpenCodeAPIURL is stricter than a user-facing link: the daemon sends
// the OpenCode service password with every request, so the target must stay on
// loopback. A remote HTTPS URL would leak a local credential.
func normalizeOpenCodeAPIURL(raw string) (string, error) {
	normalized, err := normalizeExternalURL(raw, "opencodeAPIURL")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("opencodeAPIURL must be an absolute URL")
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("opencodeAPIURL must point at loopback")
	}
	return normalized, nil
}

func defaultOpenCodePasswordFile() string {
	return envPath("XDG_CONFIG_HOME", ".config", filepath.Join("opencode", "service-local-install.json"))
}

func normalizeFilePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("must not be empty")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		if raw == "~" {
			raw = home
		} else {
			raw = filepath.Join(home, raw[2:])
		}
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("must be an absolute path")
	}
	return filepath.Clean(raw), nil
}

func normalizePathList(raw []string) ([]string, error) {
	out := raw[:0]
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		path, err := normalizeFilePath(entry)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out, nil
}

func normalizeEnvNames(raw []string) ([]string, error) {
	out := raw[:0]
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		name := strings.TrimSpace(entry)
		if name == "" {
			continue
		}
		if !validEnvName(name) {
			return nil, fmt.Errorf("%q is not a variable name", name)
		}
		switch name {
		case "HOME", "TMPDIR":
			return nil, fmt.Errorf("%s is projected by the jail and cannot be allowed", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func validEnvName(name string) bool {
	for i, r := range name {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return name != ""
}

// normalizeExternalURL is the shared rule for a user-facing link: absolute
// http(s), no credentials, no path/query/fragment, and no plaintext remote
// origin. A browser link is a trust boundary even when the daemon never fetches
// it itself.
func normalizeExternalURL(raw, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%s must be an absolute URL", field)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("%s must use http or https", field)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%s must not contain credentials", field)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("%s must not contain a path", field)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%s must not contain a query or fragment", field)
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("%s must use https for a non-loopback host", field)
	}
	return strings.TrimRight(raw, "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeConfig(path string, cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func ensureToken(path string) (string, error) {
	return ensureSecretFile(path, "token")
}

// ensureInstanceSecret creates the salt for browser-session hashing. It is
// deliberately a separate file from the token: rotating one must not silently
// rotate the other, and losing this file fails closed (every browser session is
// rejected and the user runs `darkfactory open` again).
func ensureInstanceSecret(path string) (string, error) {
	return ensureSecretFile(path, "instance secret")
}

// RotateToken replaces the master token. The caller decides whether to show it:
// a rotated token is a credential, and `darkfactory token` already prints the
// current one for scripts.
func RotateToken(path string) (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	if err := writeSecretFile(path, secret); err != nil {
		return "", err
	}
	return secret, nil
}

// RotateInstanceSecret replaces the salt behind every browser session. Rows
// keyed with the old salt stop matching, so every browser and paired device is
// logged out; that is the point of rotating it.
func RotateInstanceSecret(path string) (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", fmt.Errorf("generate instance secret: %w", err)
	}
	if err := writeSecretFile(path, secret); err != nil {
		return "", err
	}
	return secret, nil
}

func newSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// writeSecretFile is atomic: a reader either sees the old secret or the new
// one, never a half-written file. The mode is set on the temporary inode before
// the rename, so there is no window where the credential is group-readable.
func writeSecretFile(path, secret string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return fmt.Errorf("write secret: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure secret: %w", err)
	}
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write secret: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace secret: %w", err)
	}
	return nil
}

func ensureSecretFile(path, label string) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(raw))
		if secret == "" {
			return "", fmt.Errorf("%s file %s is empty", label, path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("secure %s file: %w", label, err)
		}
		return secret, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", label, err)
	}

	secret, err := newSecret()
	if err != nil {
		return "", fmt.Errorf("generate %s: %w", label, err)
	}
	if err := writeSecretFile(path, secret); err != nil {
		return "", err
	}
	return secret, nil
}

// ValidateListen refuses a non-loopback bind unless it was asked for, and a
// plaintext non-loopback bind unless it was asked for twice.
func ValidateListen(addr string, allowRemote, insecureRemote bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("listen address %q has no host; name 127.0.0.1 or a specific address", addr)
	}
	if isLoopback(host) {
		return nil
	}
	if !allowRemote {
		return fmt.Errorf("refusing non-loopback listen %q without --allow-remote", addr)
	}
	if !insecureRemote {
		return fmt.Errorf("refusing plaintext non-loopback listen %q without --insecure-remote", addr)
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envPath(xdgEnv, homeSuffix, leaf string) string {
	if base := strings.TrimSpace(os.Getenv(xdgEnv)); base != "" {
		return filepath.Join(base, leaf)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, homeSuffix, leaf)
}
