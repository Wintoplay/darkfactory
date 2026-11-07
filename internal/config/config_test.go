package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateListen(t *testing.T) {
	cases := []struct {
		addr     string
		allow    bool
		insecure bool
		ok       bool
	}{
		{"127.0.0.1:8787", false, false, true},
		{"localhost:8787", false, false, true},
		{"[::1]:8787", false, false, true},
		{"0.0.0.0:8787", false, false, false},
		{"0.0.0.0:8787", true, false, false},
		{"0.0.0.0:8787", true, true, true},
		{":8787", true, true, false},
		{"not-an-address", false, false, false},
	}
	for _, tc := range cases {
		err := ValidateListen(tc.addr, tc.allow, tc.insecure)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateListen(%q, %v, %v) = %v, want ok=%v", tc.addr, tc.allow, tc.insecure, err, tc.ok)
		}
	}
}

func TestNormalizeJailHiddenPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.JailHiddenPaths = []string{" ", "~/secrets", "/tmp/secrets", "/tmp/secrets"}
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, "secrets"), "/tmp/secrets"}
	if len(cfg.JailHiddenPaths) != len(want) {
		t.Fatalf("jailHiddenPaths = %#v, want %#v", cfg.JailHiddenPaths, want)
	}
	for i := range want {
		if cfg.JailHiddenPaths[i] != want[i] {
			t.Fatalf("jailHiddenPaths = %#v, want %#v", cfg.JailHiddenPaths, want)
		}
	}

	cfg = Default()
	cfg.JailHiddenPaths = []string{"relative/path"}
	if err := cfg.normalize(); err == nil {
		t.Fatal("a relative jailHiddenPaths entry must be refused")
	}
}

func TestNormalizeJailAllowLists(t *testing.T) {
	cfg := Default()
	cfg.JailEnvAllow = []string{" SSH_AUTH_SOCK ", "SSH_AUTH_SOCK", "OPENAI_API_KEY"}
	cfg.JailPathAllow = []string{"/tmp/secrets", "/tmp/secrets"}
	cfg.Agents["codex"] = AgentPreset{
		Command:       "codex",
		JailEnvAllow:  []string{"GH_TOKEN"},
		JailPathAllow: []string{"/tmp/codex"},
	}
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.JailEnvAllow; len(got) != 2 || got[0] != "SSH_AUTH_SOCK" || got[1] != "OPENAI_API_KEY" {
		t.Fatalf("jailEnvAllow = %#v", got)
	}
	if got := cfg.JailPathAllow; len(got) != 1 || got[0] != "/tmp/secrets" {
		t.Fatalf("jailPathAllow = %#v", got)
	}
	if got := cfg.Agents["codex"].JailEnvAllow; len(got) != 1 || got[0] != "GH_TOKEN" {
		t.Fatalf("agent jailEnvAllow = %#v", got)
	}
	if got := cfg.Agents["codex"].JailPathAllow; len(got) != 1 || got[0] != "/tmp/codex" {
		t.Fatalf("agent jailPathAllow = %#v", got)
	}
}

func TestNormalizeJailAllowRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"HOME", "TMPDIR", "not-a-name", "1BAD"} {
		cfg := Default()
		cfg.JailEnvAllow = []string{name}
		if err := cfg.normalize(); err == nil {
			t.Fatalf("jailEnvAllow %q was accepted", name)
		}
	}
}

func TestNormalizePublicURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://df.tailnet.ts.net/", "https://df.tailnet.ts.net", true},
		{"https://df.tailnet.ts.net:443", "https://df.tailnet.ts.net:443", true},
		{"http://127.0.0.1:8787/", "http://127.0.0.1:8787", true},
		{"http://localhost:8787", "http://localhost:8787", true},
		{"http://df.tailnet.ts.net", "", false},
		{"https://df.tailnet.ts.net/df", "", false},
		{"https://df.tailnet.ts.net?x=1", "", false},
		{"https://df.tailnet.ts.net#x", "", false},
		{"https://user:pass@df.tailnet.ts.net", "", false},
		{"ftp://df.tailnet.ts.net", "", false},
		{"not a url", "", false},
	}
	for _, tc := range cases {
		got, err := normalizePublicURL(tc.raw)
		if (err == nil) != tc.ok {
			t.Errorf("normalizePublicURL(%q) error = %v, want ok=%v", tc.raw, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("normalizePublicURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeOpenCodeURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://df.tailnet.ts.net:8443/", "https://df.tailnet.ts.net:8443", true},
		{"http://127.0.0.1:26706", "http://127.0.0.1:26706", true},
		{"http://df.tailnet.ts.net:8443", "", false},
		{"https://df.tailnet.ts.net:8443/opencode", "", false},
		{"https://df.tailnet.ts.net:8443?x=1", "", false},
		{"https://df.tailnet.ts.net:8443#x", "", false},
		{"https://user:pass@df.tailnet.ts.net:8443", "", false},
		{"ftp://df.tailnet.ts.net:8443", "", false},
		{"not a url", "", false},
	}
	for _, tc := range cases {
		got, err := normalizeOpenCodeURL(tc.raw)
		if (err == nil) != tc.ok {
			t.Errorf("normalizeOpenCodeURL(%q) error = %v, want ok=%v", tc.raw, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("normalizeOpenCodeURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeOpenCodeAPIURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"http://127.0.0.1:26706/", "http://127.0.0.1:26706", true},
		{"http://localhost:26706", "http://localhost:26706", true},
		{"http://[::1]:26706", "http://[::1]:26706", true},
		{"https://127.0.0.1:26706", "https://127.0.0.1:26706", true},
		{"http://df.tailnet.ts.net:26706", "", false},
		{"https://df.tailnet.ts.net:26706", "", false},
		{"http://127.0.0.1:26706/api", "", false},
		{"http://127.0.0.1:26706?x=1", "", false},
		{"http://user:pass@127.0.0.1:26706", "", false},
		{"ftp://127.0.0.1:26706", "", false},
		{"not a url", "", false},
	}
	for _, tc := range cases {
		got, err := normalizeOpenCodeAPIURL(tc.raw)
		if (err == nil) != tc.ok {
			t.Errorf("normalizeOpenCodeAPIURL(%q) error = %v, want ok=%v", tc.raw, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("normalizeOpenCodeAPIURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeFilePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeFilePath("~/opencode/service.json")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "opencode", "service.json"); got != want {
		t.Fatalf("normalizeFilePath = %q, want %q", got, want)
	}
	if _, err := normalizeFilePath("relative/service.json"); err == nil {
		t.Fatal("relative path must be rejected")
	}
}

func TestLoadOpenCodeDefaults(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenCodeAPIURL != defaultOpenCodeAPIURL {
		t.Fatalf("OpenCodeAPIURL = %q, want %q", cfg.OpenCodeAPIURL, defaultOpenCodeAPIURL)
	}
	wantPassword := filepath.Join(root, "config", "opencode", "service-local-install.json")
	if cfg.OpenCodePasswordFile != wantPassword {
		t.Fatalf("OpenCodePasswordFile = %q, want %q", cfg.OpenCodePasswordFile, wantPassword)
	}
}

func TestLoadCreatesReusableToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Token) != 64 {
		t.Fatalf("token length = %d, want 64", len(cfg.Token))
	}
	if cfg.Agents["claude"].Command != "claude" {
		t.Fatalf("missing claude preset: %#v", cfg.Agents)
	}
	if cfg.Containment != "off" {
		t.Fatalf("default containment = %q, want off", cfg.Containment)
	}

	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.Token != cfg.Token {
		t.Fatal("token changed between loads")
	}
	if len(cfg.InstanceSecret) != 64 {
		t.Fatalf("instance secret length = %d, want 64", len(cfg.InstanceSecret))
	}
	if again.InstanceSecret != cfg.InstanceSecret {
		t.Fatal("instance secret changed between loads")
	}
	info, err := os.Stat(cfg.InstanceSecretPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("instance secret mode = %o, want 600", perm)
	}
	// The secret is a credential, not settings: it must stay out of config.json.
	raw, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), cfg.InstanceSecret) {
		t.Fatal("instance secret was written to config.json")
	}
}

func TestRotateSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	oldToken := cfg.Token
	oldSecret := cfg.InstanceSecret

	token, err := RotateToken(cfg.TokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if token == oldToken || len(token) != 64 {
		t.Fatalf("rotated token = %q", token)
	}
	secret, err := RotateInstanceSecret(cfg.InstanceSecretPath)
	if err != nil {
		t.Fatal(err)
	}
	if secret == oldSecret || len(secret) != 64 {
		t.Fatalf("rotated instance secret = %q", secret)
	}

	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.Token != token || again.InstanceSecret != secret {
		t.Fatal("a reload did not pick up the rotated credentials")
	}
	for _, path := range []string{filepath.Join(cfg.ConfigDir, "token"), filepath.Join(cfg.ConfigDir, "instance-secret")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, perm)
		}
	}
	// The atomic write must not leave temporary inodes behind.
	entries, err := os.ReadDir(cfg.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".secret-") {
			t.Fatalf("left a temporary secret file: %s", entry.Name())
		}
	}
}

func TestHTTPTimeouts(t *testing.T) {
	cfg := Default()
	got, err := cfg.Timeouts()
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadHeader != 10*time.Second || got.Read != time.Minute || got.Write != time.Minute || got.Idle != 2*time.Minute {
		t.Fatalf("defaults = %+v", got)
	}
	if got.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want 1 MiB", got.MaxHeaderBytes)
	}

	// "never", "off" and "0" all disable a deadline.
	cfg.ReadHeaderTimeout = "never"
	cfg.ReadTimeout = "0"
	cfg.WriteTimeout = "off"
	cfg.IdleTimeout = ""
	got, err = cfg.Timeouts()
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadHeader != 0 || got.Read != 0 || got.Write != 0 || got.Idle != 0 {
		t.Fatalf("disabled timeouts = %+v", got)
	}

	cfg = Default()
	cfg.ReadTimeout = "soon"
	if _, err := cfg.Timeouts(); err == nil {
		t.Fatal("a non-duration value was accepted")
	}
	if err := cfg.normalize(); err == nil {
		t.Fatal("normalize accepted a non-duration timeout")
	}

	cfg = Default()
	cfg.WriteTimeout = "-5s"
	if _, err := cfg.Timeouts(); err == nil {
		t.Fatal("a negative timeout was accepted")
	}

	cfg = Default()
	cfg.MaxHeaderBytes = -1
	if _, err := cfg.Timeouts(); err == nil {
		t.Fatal("a negative header cap was accepted")
	}
}
