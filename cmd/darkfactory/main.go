package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/httpapi"
	"github.com/Wintoplay/darkfactory/internal/nativeattach"
	"github.com/Wintoplay/darkfactory/internal/policy"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

var version = "0.1.0-dev"

// commit is stamped by the Makefile. It is the only way to tell two local
// builds apart: version stays 0.1.0-dev for every development build.
var commit = "unknown"

// buildString is what `version`, `status` and the UI print.
func buildString() string {
	if commit == "" || commit == "unknown" {
		return version
	}
	return version + " (" + commit + ")"
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "session-host":
		err = runSessionHost(os.Args[2:])
	case "native-attach":
		err = runNativeAttach(os.Args[2:])
	case "open":
		err = runOpen(os.Args[2:])
	case "status":
		err = runStatus(os.Args[2:])
	case "audit":
		err = runAudit(os.Args[2:])
	case "policy":
		err = runPolicy(os.Args[2:])
	case "launch":
		err = runLaunch(os.Args[2:])
	case "stop":
		err = stopServe(os.Args[2:])
	case "token":
		err = printToken(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(buildString())
		return
	case "help", "--help", "-h":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "darkfactory:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  darkfactory serve [--listen addr] [--ring-bytes n] [--allow-remote] [--insecure-remote] [--allow-public] [--idle-exit 30m] [--debug]
  darkfactory launch [--listen addr] [--idle-exit 30m] [--no-browser]
  darkfactory session-host [--socket path] [--ring-bytes n] [--stop]
  darkfactory native-attach --listen addr --credential-fd n session-id
  darkfactory open [--listen addr] [--no-browser]
  darkfactory status [--listen addr] [--json]
  darkfactory audit [--limit n] [--json] [--verify]
  darkfactory policy [--path file] [--json]
  darkfactory stop
  darkfactory token [--rotate] [--rotate-sessions]
  darkfactory version

The server binds to localhost by default. A non-loopback bind requires
--allow-remote; a plaintext one also requires --insecure-remote. Prefer
Tailscale Serve or an SSH tunnel; do not expose it to the public internet.
--idle-exit stops the listener after the given quiet period; the detached
session host and its PTYs stay alive. --debug logs one line per HTTP request
(method, path, status, duration).
`)
}

// runNativeAttach receives the existing browser's credential over a private
// inherited descriptor. It must not accept credentials in arguments or URLs.
func runNativeAttach(args []string) error {
	fs := flag.NewFlagSet("native-attach", flag.ContinueOnError)
	listen := fs.String("listen", "", "loopback daemon address")
	credentialFD := fs.Int("credential-fd", -1, "inherited credential descriptor")
	readyFD := fs.Int("ready-fd", -1, "inherited readiness descriptor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("native attach requires a session id")
	}
	return nativeattach.Run(*listen, fs.Arg(0), *credentialFD, *readyFD)
}

// runOpen asks the running daemon for a one-time browser code and opens the
// fragment URL. The master token never enters the URL; the browser exchanges
// the fragment for an HttpOnly cookie.
func runOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "print the URL without opening a browser")
	listen := fs.String("listen", "", "daemon listen address (defaults to config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	base, err := listenBaseURL(cfg.Listen)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/open/code", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon is not reachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("open failed: %s", refusalText(raw))
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode open response: %w", err)
	}
	if result.URL == "" {
		return errors.New("daemon returned an empty open URL")
	}
	fmt.Println(result.URL)
	if *noBrowser {
		return nil
	}
	if err := openInBrowser(result.URL); err != nil {
		fmt.Fprintln(os.Stderr, "darkfactory: could not open a browser:", err)
		fmt.Fprintln(os.Stderr, "darkfactory: open the URL above, or rerun with --no-browser")
	}
	return nil
}

// refusalText turns the daemon's refusal envelope into one CLI line and falls
// back to the raw body for an older daemon.
func refusalText(raw []byte) string {
	var body struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Hint   string `json:"hint"`
	}
	if err := json.Unmarshal(raw, &body); err == nil {
		switch {
		case body.Detail != "":
			return body.Detail
		case body.Hint != "":
			return body.Hint
		case body.Code != "":
			return body.Code
		}
	}
	return strings.TrimSpace(string(raw))
}

type statusSession struct {
	ID          string `json:"id"`
	WorktreeID  string `json:"worktreeId"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Containment string `json:"containment"`
	PID         int    `json:"pid,omitempty"`
}

type statusWorktree struct {
	ID          string `json:"id"`
	Containment string `json:"containment"`
}

type statusState struct {
	Build     string            `json:"build"`
	Worktrees []statusWorktree  `json:"worktrees"`
	Sessions  []statusSession   `json:"sessions"`
	Runs      []json.RawMessage `json:"runs"`
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	listen := fs.String("listen", "", "daemon listen address (defaults to config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	base, err := listenBaseURL(cfg.Listen)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/state", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon is not reachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status failed: HTTP %d", resp.StatusCode)
	}
	var state statusState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return err
	}
	if state.Build == "" {
		// Do not print the client's own version: that is the staleness bug this
		// field exists to remove.
		state.Build = "unknown (daemon did not report a build)"
	}
	type sessionOut struct {
		ID          string `json:"id"`
		WorktreeID  string `json:"worktreeId"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		Containment string `json:"containment"`
		PID         int    `json:"pid,omitempty"`
	}
	out := struct {
		Version      string       `json:"version"`
		Listen       string       `json:"listen"`
		ConfigDir    string       `json:"configDir"`
		StateDir     string       `json:"stateDir"`
		WorktreeRoot string       `json:"worktreeRoot"`
		HomeRoot     string       `json:"homeRoot"`
		Sessions     []sessionOut `json:"sessions"`
		RunCount     int          `json:"runs"`
		Limit        string       `json:"limit"`
		Pairing      string       `json:"pairing"`
	}{
		Version:      state.Build,
		Listen:       cfg.Listen,
		ConfigDir:    cfg.ConfigDir,
		StateDir:     cfg.StateDir,
		WorktreeRoot: cfg.WorktreeRoot,
		HomeRoot:     cfg.HomeRoot,
		Sessions:     []sessionOut{},
		RunCount:     len(state.Runs),
		Limit:        "same-uid unless a session says jail; jail masks daemon state, not every file",
		Pairing:      "network-safe, same-uid; a code-gated browser session, not a sandbox",
	}
	for _, sess := range state.Sessions {
		containment := sess.Containment
		if containment == "" {
			// Rows from before containment existed ran as the same uid.
			containment = "off"
		}
		out.Sessions = append(out.Sessions, sessionOut{
			ID: sess.ID, WorktreeID: sess.WorktreeID, Title: sess.Title,
			Status: sess.Status, Containment: containment, PID: sess.PID,
		})
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("darkfactory %s\n", out.Version)
	fmt.Printf("listen       %s\n", out.Listen)
	fmt.Printf("config       %s\n", out.ConfigDir)
	fmt.Printf("state        %s\n", out.StateDir)
	fmt.Printf("worktrees    %s\n", out.WorktreeRoot)
	fmt.Printf("homes        %s\n", out.HomeRoot)
	fmt.Printf("runs         %d\n", out.RunCount)
	fmt.Printf("pairing      %s\n", out.Pairing)
	if len(out.Sessions) == 0 {
		fmt.Println("sessions     none")
	} else {
		for _, sess := range out.Sessions {
			fmt.Printf("session      %s  %-8s  containment=%s  pid=%d  %s\n", sess.ID, sess.Status, sess.Containment, sess.PID, sess.Title)
		}
	}
	fmt.Printf("limit        %s\n", out.Limit)
	return nil
}

// runAudit reads the decision ledger through the daemon, the same way status
// does, so it works while `serve` holds the database lock. The chain is
// recomputed on every read; --verify only prints that verdict and exits 1 when
// the chain is broken.
func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	verifyOnly := fs.Bool("verify", false, "only recompute the chain")
	limit := fs.Int("limit", 100, "rows to print (1-1000)")
	listen := fs.String("listen", "", "daemon listen address (defaults to config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	base, err := listenBaseURL(cfg.Listen)
	if err != nil {
		return err
	}
	url := base + "/api/v1/audit?limit=" + strconv.Itoa(*limit)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon is not reachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("audit failed: %s", refusalText(raw))
	}
	var out auditState
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	broken := !out.Chain.OK
	if *verifyOnly {
		if broken {
			return fmt.Errorf("ledger chain is broken at row %d: %s", out.Chain.BadSeq, out.Chain.Error)
		}
		fmt.Printf("chain ok (%d rows checked)\n", out.Chain.Checked)
		return nil
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		printAudit(out)
	}
	if broken {
		return fmt.Errorf("ledger chain is broken at row %d: %s", out.Chain.BadSeq, out.Chain.Error)
	}
	return nil
}

type auditState struct {
	Entries []struct {
		Seq       uint64    `json:"seq"`
		At        time.Time `json:"at"`
		Principal string    `json:"principal"`
		Verb      string    `json:"verb"`
		Target    string    `json:"target"`
		Decision  string    `json:"decision"`
		Rule      string    `json:"rule"`
		Policy    string    `json:"policy"`
		Outcome   string    `json:"outcome"`
		Status    int       `json:"status"`
		Pruned    *struct {
			ThroughSeq  uint64 `json:"throughSeq"`
			ThroughHash string `json:"throughHash"`
			Dropped     int    `json:"dropped"`
		} `json:"pruned"`
		Hash string `json:"hash"`
	} `json:"entries"`
	Total     int  `json:"total"`
	Limit     int  `json:"limit"`
	Truncated bool `json:"truncated"`
	// RetentionDays is what a prune keeps; 0 means nothing is pruned.
	RetentionDays int `json:"retentionDays"`
	Chain         struct {
		OK      bool   `json:"ok"`
		Checked int    `json:"checked"`
		BadSeq  uint64 `json:"badSeq"`
		Error   string `json:"error"`
	} `json:"chain"`
}

// printAudit prints one line per decision, merging the outcome row the daemon
// writes after it. The store keeps them apart so the chain can say what was
// decided before anything happened; a reader wants them together.
func printAudit(out auditState) {
	verdict := fmt.Sprintf("chain ok (%d rows checked)", out.Chain.Checked)
	if !out.Chain.OK {
		verdict = fmt.Sprintf("chain BROKEN at row %d", out.Chain.BadSeq)
	}
	fmt.Printf("darkfactory audit  %s  %s\n", plural(out.Total, "row", "rows"), verdict)
	if out.RetentionDays > 0 {
		fmt.Printf("retention      %d days (older rows are pruned; a prune leaves a marker)\n", out.RetentionDays)
	}

	type decisionLine struct {
		seq       uint64
		at        time.Time
		principal string
		verb      string
		target    string
		decision  string
		rule      string
		outcome   string
		status    int
		pruned    string
	}
	var lines []decisionLine
	for _, entry := range out.Entries {
		if entry.Pruned != nil {
			lines = append(lines, decisionLine{
				seq: entry.Seq, at: entry.At, principal: entry.Principal, verb: entry.Verb,
				target:   entry.Target,
				decision: entry.Decision,
				pruned: fmt.Sprintf("%d rows through seq %d (hash %s)",
					entry.Pruned.Dropped, entry.Pruned.ThroughSeq, shortHash(entry.Pruned.ThroughHash)),
			})
			continue
		}
		last := len(lines) - 1
		// An outcome row carries the same verb and target as the decision it
		// follows and one more sequence number: merge it instead of printing a
		// second line. The window can start mid-pair, which is why the merge
		// checks the fields rather than trusting the position.
		if entry.Outcome != "" && last >= 0 && lines[last].verb == entry.Verb &&
			entry.Seq == lines[last].seq+1 && lines[last].outcome == "" {
			lines[last].outcome = entry.Outcome
			lines[last].status = entry.Status
			// A create resolves its target only once it has one, so the outcome
			// row names the object the decision row could not.
			if entry.Target != "" && entry.Target != "request" {
				lines[last].target = entry.Target
			}
			continue
		}
		lines = append(lines, decisionLine{
			seq: entry.Seq, at: entry.At, principal: entry.Principal, verb: entry.Verb,
			target: entry.Target, decision: entry.Decision, rule: entry.Rule,
			outcome: entry.Outcome, status: entry.Status,
		})
	}

	for _, line := range lines {
		if line.pruned != "" {
			fmt.Println(strings.TrimRight(fmt.Sprintf("%5d  %s  %-20s  %-28s  %-24s  %-7s  %s",
				line.seq, line.at.UTC().Format(time.RFC3339), line.principal, line.verb,
				line.target, line.decision, line.pruned), " "))
			continue
		}
		outcome := line.outcome
		switch {
		case outcome == "" && line.decision == "allow":
			// No outcome row followed: the daemon stopped between the two, or
			// the write failed. Never print that as success.
			outcome = "unknown"
		case outcome == "":
			outcome = "denied"
		}
		status := ""
		if line.status != 0 {
			status = strconv.Itoa(line.status)
		}
		rule := ""
		if line.rule != "" {
			rule = "  rule=" + line.rule
		}
		fmt.Println(strings.TrimRight(fmt.Sprintf("%5d  %s  %-20s  %-28s  %-24s  %-7s  %-7s  %-5s%s",
			line.seq, line.at.UTC().Format(time.RFC3339), line.principal, line.verb,
			line.target, line.decision, outcome, status, rule), " "))
	}
	if out.Truncated {
		fmt.Printf("showing the newest %d of %d rows; --limit n (max 1000) shows more\n", len(out.Entries), out.Total)
	}
}

// runPolicy prints the policy in force and where it came from. It reads the
// file locally, so it works whether or not the daemon is running, and it prints
// the same hash the ledger stores for a decision.
func runPolicy(args []string) error {
	fs := flag.NewFlagSet("policy", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	pathname := fs.String("path", "", "policy file (defaults to the config directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *pathname != "" {
		cfg.PolicyPath = *pathname
	}
	par, err := policy.Load(cfg.PolicyPath)
	if err != nil {
		return err
	}
	source := "file"
	if _, err := os.Stat(cfg.PolicyPath); errors.Is(err, os.ErrNotExist) || cfg.PolicyPath == "" {
		source = "default"
	}
	out := struct {
		Path    string        `json:"path"`
		Source  string        `json:"source"`
		Hash    string        `json:"hash"`
		Version int           `json:"version"`
		Rules   []policy.Rule `json:"rules"`
	}{Path: cfg.PolicyPath, Source: source, Hash: par.Hash(), Version: par.Version, Rules: par.Rules}
	if out.Rules == nil {
		out.Rules = []policy.Rule{}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	label := out.Path
	if source == "default" {
		label += "  (missing: built-in default)"
	}
	fmt.Printf("policy       %s\n", label)
	fmt.Printf("hash         %s\n", out.Hash)
	fmt.Printf("version      %d\n", out.Version)
	for _, rule := range out.Rules {
		fmt.Printf("rule         %-5s  %-18s  %-24s  %s\n", rule.Effect, rule.Principal, rule.Verb, rule.Rule)
	}
	if len(out.Rules) == 0 {
		fmt.Println("rule         none: every verb is denied")
	}
	fmt.Println("default      deny when no rule matches; every deny is evaluated before any allow")
	return nil
}

// plural keeps "1 row" out of the output without a format rule at each site.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// shortHash trims a 64-character chain hash for a human column without
// panicking on a value that is shorter than that (a hand-edited row).
func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// runLaunch is what a desktop entry calls: make sure the daemon is up, then
// open the browser. `serve` owns the PTYs and outlives the window, so launch
// starts it detached and never waits on it.
func runLaunch(args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	listen := fs.String("listen", "", "daemon listen address (defaults to config)")
	noBrowser := fs.Bool("no-browser", false, "print the URL without opening a browser")
	idleExit := fs.Duration("idle-exit", 0, "exit the daemon after this long with no HTTP activity (0 disables)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *idleExit < 0 {
		return fmt.Errorf("--idle-exit must not be negative")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	base, err := listenBaseURL(cfg.Listen)
	if err != nil {
		return err
	}
	if !daemonHealthy(base) {
		if err := startDetachedServe(cfg, *idleExit); err != nil {
			return err
		}
		if err := waitForDaemon(base, 15*time.Second, filepath.Join(cfg.StateDir, "launch.log")); err != nil {
			return err
		}
	}
	openArgs := []string{"--listen", cfg.Listen}
	if *noBrowser {
		openArgs = append(openArgs, "--no-browser")
	}
	return runOpen(openArgs)
}

// daemonHealthy is the unauthenticated liveness check; it says nothing about
// state or sessions, only that something is listening on our port.
func daemonHealthy(base string) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
	return resp.StatusCode == http.StatusOK
}

// startDetachedServe starts `darkfactory serve` in its own session so closing
// the terminal or the launcher does not take the daemon with it. The log lands
// next to state.db, which is where `status` and the docs already point.
func startDetachedServe(cfg config.Config, idleExit time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(cfg.StateDir, "launch.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	args := []string{"serve", "--listen", cfg.Listen}
	if idleExit > 0 {
		args = append(args, "--idle-exit", idleExit.String())
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// The daemon is not our child to wait on; the pid file is how `stop` finds
	// it. Releasing the handle avoids a zombie if serve exits immediately.
	return cmd.Process.Release()
}

func waitForDaemon(base string, timeout time.Duration, logPath string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if daemonHealthy(base) {
			return nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("the daemon did not answer at %s; see %s", base, logPath)
}

func listenBaseURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func openInBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{url}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		command, args = "xdg-open", []string{url}
	}
	if _, err := exec.LookPath(command); err != nil {
		return err
	}
	cmd := exec.Command(command, args...)
	return cmd.Start()
}

func runSessionHost(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("session-host", flag.ContinueOnError)
	socket := fs.String("socket", "", "unix socket path")
	ringBytes := fs.Int("ring-bytes", 0, "per-session scrollback ring size")
	stop := fs.Bool("stop", false, "stop the running session host")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *socket == "" {
		*socket = session.HostSocketPath(cfg.StateDir)
	}
	if *stop {
		client := session.NewHostClient(*socket)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return client.Shutdown(ctx)
	}
	if *ringBytes <= 0 {
		*ringBytes = cfg.RingBytes
	}
	check := session.NewHostClient(*socket)
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	err = check.Health(checkCtx)
	checkCancel()
	if err == nil {
		return fmt.Errorf("session host already running at %s", *socket)
	}
	if err := os.MkdirAll(filepath.Dir(*socket), 0o700); err != nil {
		return fmt.Errorf("create session host dir: %w", err)
	}
	_ = os.Remove(*socket)
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		return fmt.Errorf("listen session host: %w", err)
	}
	if err := os.Chmod(*socket, 0o600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("secure session host socket: %w", err)
	}
	defer os.Remove(*socket)

	manager := session.NewManager(*ringBytes)
	manager.StartReaper(2 * time.Second)
	stopCh := make(chan struct{})
	shutdown := func() {
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
	}
	server := &http.Server{
		Handler:           session.NewHostHandler(manager, shutdown),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("session host listening", "socket", *socket)
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()

	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	select {
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	case <-stopCh:
	}

	manager.CloseAllWithReason("host_shutdown")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	return nil
}

func serve(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", cfg.Listen, "listen address")
	ringBytes := fs.Int("ring-bytes", cfg.RingBytes, "per-session scrollback ring size")
	allowRemote := fs.Bool("allow-remote", false, "allow a non-loopback bind")
	insecureRemote := fs.Bool("insecure-remote", false, "allow plaintext non-loopback (unsafe)")
	allowPublic := fs.Bool("allow-public", cfg.AllowPublicSources, "accept direct peers from public addresses (unsafe; use a TLS gateway)")
	idleExit := fs.Duration("idle-exit", 0, "exit after this long with no HTTP activity (0 disables)")
	debug := fs.Bool("debug", false, "log every HTTP request")
	policyOverride := fs.String("policy", "", "policy file (defaults to the config directory)")
	readHeaderTimeout := fs.String("read-header-timeout", cfg.ReadHeaderTimeout, `listener header deadline; "never" disables`)
	readTimeout := fs.String("read-timeout", cfg.ReadTimeout, `listener request-body deadline; "never" disables`)
	writeTimeout := fs.String("write-timeout", cfg.WriteTimeout, `listener response deadline; "never" disables (streams clear it)`)
	idleTimeout := fs.String("idle-timeout", cfg.IdleTimeout, `keep-alive idle deadline; "never" disables`)
	maxHeaderBytes := fs.Int("max-header-bytes", cfg.MaxHeaderBytes, "maximum request head size in bytes (0 keeps the Go default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *idleExit < 0 {
		return fmt.Errorf("--idle-exit must not be negative")
	}
	cfg.Listen = *listen
	cfg.RingBytes = *ringBytes
	cfg.AllowPublicSources = *allowPublic
	cfg.ReadHeaderTimeout = *readHeaderTimeout
	cfg.ReadTimeout = *readTimeout
	cfg.WriteTimeout = *writeTimeout
	cfg.IdleTimeout = *idleTimeout
	cfg.MaxHeaderBytes = *maxHeaderBytes
	timeouts, err := cfg.Timeouts()
	if err != nil {
		return err
	}
	if err := config.ValidateListen(cfg.Listen, *allowRemote, *insecureRemote); err != nil {
		return err
	}
	// Loaded before the store opens: a malformed policy refuses startup instead
	// of running with something nobody wrote (GV-5). A missing file is the
	// built-in default, which is what the daemon has always done.
	policyPath := cfg.PolicyPath
	if *policyOverride != "" {
		policyPath = *policyOverride
	}
	par, err := policy.Load(policyPath)
	if err != nil {
		return err
	}
	cfg.Policy = par

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if *debug {
		debugPath := filepath.Join(cfg.StateDir, "debug.log")
		file, err := os.OpenFile(debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open debug log: %w", err)
		}
		defer file.Close()
		log = slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, file), &slog.HandlerOptions{Level: slog.LevelInfo}))
		log.Info("debug log", "path", debugPath)
	}
	st, err := store.Open(filepath.Join(cfg.StateDir, "state.db"))
	if err != nil {
		if lockedError(err) {
			return alreadyServing(cfg.StateDir)
		}
		return err
	}
	defer st.Close()
	// Written after the lock is held, so a pid file never claims a daemon that
	// failed to start. A killed process leaves it behind; stopServe checks that
	// the pid is alive and is actually this binary before signalling it.
	if err := writeServePID(cfg.StateDir, os.Getpid()); err != nil {
		log.Warn("write pid file", "error", err)
	} else {
		defer func() { _ = removeServePID(cfg.StateDir) }()
	}

	host, err := session.EnsureHost(cfg.StateDir, cfg.RingBytes, log)
	if err != nil {
		return err
	}
	sessions := session.NewManager(cfg.RingBytes)
	if err := sessions.UseHost(host); err != nil {
		return err
	}
	sessions.StartReaper(2 * time.Second)
	defer sessions.CloseAll()

	// The ledger is append-only, not unbounded: rows older than the retention
	// window are pruned at startup and every six hours, and each prune leaves a
	// marker row so the survivors still verify (GV-18).
	pruneLedger := func() {
		if cfg.LedgerRetentionDays <= 0 {
			return
		}
		cutoff := time.Now().UTC().Add(-time.Duration(cfg.LedgerRetentionDays) * 24 * time.Hour)
		marker, err := st.PruneLedger(cutoff)
		switch {
		case err != nil:
			log.Warn("prune ledger", "error", err)
		case marker != nil:
			log.Info("pruned ledger", "rows", marker.Marker.Dropped, "through", marker.Marker.ThroughSeq, "retentionDays", cfg.LedgerRetentionDays)
		}
	}
	pruneLedger()

	httpapi.Build = buildString()
	activity := newActivityTracker()
	api := httpapi.New(cfg, st, sessions, log)
	handler := activity.wrap(api.Handler())
	if *debug {
		handler = debugRequests(log, handler)
	}
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: timeouts.ReadHeader,
		ReadTimeout:       timeouts.Read,
		WriteTimeout:      timeouts.Write,
		IdleTimeout:       timeouts.Idle,
		MaxHeaderBytes:    timeouts.MaxHeaderBytes,
	}
	sources := "loopback, LAN and Tailscale"
	if cfg.AllowPublicSources {
		sources = "any address (allowPublicSources)"
	}
	errs := make(chan error, 1)
	go func() {
		log.Info("darkfactory listening",
			"url", "http://"+cfg.Listen,
			"sources", sources,
			"tokenFile", cfg.TokenPath,
			"readHeaderTimeout", timeouts.ReadHeader.String(),
			"readTimeout", timeouts.Read.String(),
			"writeTimeout", timeouts.Write.String(),
			"idleTimeout", timeouts.Idle.String(),
		)
		errs <- server.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	api.StartNotifications(ctx)
	idle := activity.waitIdle(ctx, *idleExit)
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pruneLedger()
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	case <-idle:
		log.Info("darkfactory idle exit", "idleFor", idleExit.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

type debugResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *debugResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *debugResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *debugResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *debugResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// debugRequests logs one line per request without touching headers or bodies.
// It is opt-in because the terminal stream is long-lived and noisy.
func debugRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &debugResponseWriter{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(start).Round(time.Millisecond),
		)
	})
}

func printToken(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	rotate := fs.Bool("rotate", false, "replace the master token")
	rotateSessions := fs.Bool("rotate-sessions", false, "replace the instance secret, logging out every browser and paired device")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*rotate && !*rotateSessions {
		fmt.Println(cfg.Token)
		return nil
	}
	var changed []string
	if *rotate {
		if _, err := config.RotateToken(cfg.TokenPath); err != nil {
			return err
		}
		changed = append(changed, "master token")
	}
	if *rotateSessions {
		if _, err := config.RotateInstanceSecret(cfg.InstanceSecretPath); err != nil {
			return err
		}
		changed = append(changed, "browser sessions")
	}
	fmt.Printf("rotated %s\n", strings.Join(changed, " and "))
	if pid, err := readServePID(cfg.StateDir); err == nil && processAlive(pid) && looksLikeServe(pid) {
		fmt.Printf("pid %d is still serving the old credentials; restart it: `darkfactory stop && darkfactory serve`\n", pid)
	}
	if *rotateSessions {
		fmt.Println("every browser and paired device must sign in or pair again")
	}
	return nil
}
