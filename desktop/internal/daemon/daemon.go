// Package daemon makes the optional desktop shell self-starting.
//
// The shell is a client of the independent daemon. `darkfactory launch` starts
// that daemon and normally opens the browser; the desktop window wants the same
// daemon without the browser, so it runs the launcher with --no-browser and
// waits for /healthz before loading the page.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// These are variables so tests can shorten the wait without a second code path.
var (
	probeTimeout = 700 * time.Millisecond
	startTimeout = 15 * time.Second
	pollInterval = 200 * time.Millisecond
)

// EnsureReachable returns nil when the daemon at u answers /healthz. Otherwise
// it starts `darkfactory launch --listen <u.Host> --no-browser` and waits for
// the listener. It only starts a local daemon; u must already be loopback.
func EnsureReachable(ctx context.Context, u *url.URL) error {
	client := &http.Client{Timeout: probeTimeout}
	health := *u
	health.Path = "/healthz"
	health.RawQuery = ""
	health.Fragment = ""

	if reachable(ctx, client, &health) {
		return nil
	}
	binary, err := findBinary()
	if err != nil {
		return err
	}
	listen := u.Host
	if _, _, err := net.SplitHostPort(listen); err != nil {
		listen = net.JoinHostPort(u.Hostname(), "8787")
	}
	cmd := exec.CommandContext(ctx, binary, "launch", "--listen", listen, "--no-browser")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// launch waits for the daemon itself; a non-zero exit is not fatal here
	// because the probe below is the authority.
	_ = cmd.Run()

	deadline := time.Now().Add(startTimeout)
	for {
		if reachable(ctx, client, &health) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon at %s did not start", u.Host)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func reachable(ctx context.Context, client *http.Client, u *url.URL) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// findBinary prefers an explicit override, then PATH, then the daemon installed
// next to the desktop shell (the usual ~/.local/bin layout).
// Binary is the DarkFactory executable the shell runs for launches and the
// native attach client.
func Binary() (string, error) {
	return findBinary()
}

func findBinary() (string, error) {
	if override := os.Getenv("DARKFACTORY_BIN"); override != "" {
		return override, nil
	}
	if found, err := exec.LookPath("darkfactory"); err == nil {
		return found, nil
	}
	executable, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(executable), "darkfactory")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	return "", errors.New("darkfactory binary not found; install it beside the desktop shell or set DARKFACTORY_BIN")
}
