package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// pidFileName lives in the state directory, next to state.db. It is a hint for
// a human and for `darkfactory stop`, never an authority: the bbolt lock is
// what actually keeps two daemons apart.
const pidFileName = "serve.pid"

var errNoPID = errors.New("no pid file")

func pidPath(stateDir string) string {
	return filepath.Join(stateDir, pidFileName)
}

func writeServePID(stateDir string, pid int) error {
	return os.WriteFile(pidPath(stateDir), []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func removeServePID(stateDir string) error {
	err := os.Remove(pidPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readServePID(stateDir string) (int, error) {
	raw, err := os.ReadFile(pidPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return 0, errNoPID
	}
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file %s", pidPath(stateDir))
	}
	return pid, nil
}

// processAlive asks the kernel, not the pid file. Signal 0 performs the
// permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// looksLikeServe guards against pid reuse: a recycled pid must not receive a
// SIGTERM just because a stale file named it. /proc is Linux-only, so elsewhere
// liveness is all the check there is.
func looksLikeServe(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return true
	}
	cmdline := strings.ReplaceAll(string(raw), "\x00", " ")
	return strings.Contains(cmdline, "darkfactory") && strings.Contains(cmdline, "serve")
}

// alreadyServing turns store.ErrLocked into the instruction the user actually
// needs. The bbolt message alone ("timeout") says nothing about what to kill.
func alreadyServing(stateDir string) error {
	if pid, err := readServePID(stateDir); err == nil && processAlive(pid) && looksLikeServe(pid) {
		return fmt.Errorf("already serving %s (pid %d); stop it with `darkfactory stop` or `kill %d`",
			stateDir, pid, pid)
	}
	return fmt.Errorf("already serving %s (%s is locked); stop it with `darkfactory stop` or kill the process holding it",
		stateDir, filepath.Join(stateDir, "state.db"))
}

// stopServe signals the daemon recorded in serve.pid and waits for it to leave.
// The session host is deliberately not touched: it owns the PTYs, so stopping
// the daemon keeps live sessions.
func stopServe(_ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pid, err := readServePID(cfg.StateDir)
	if errors.Is(err, errNoPID) {
		return fmt.Errorf("no %s in %s; the daemon does not appear to be running", pidFileName, cfg.StateDir)
	}
	if err != nil {
		return err
	}
	if !processAlive(pid) || !looksLikeServe(pid) {
		_ = removeServePID(cfg.StateDir)
		return fmt.Errorf("not running (stale pid %d removed)", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		return fmt.Errorf("pid %d did not exit after SIGTERM; it still holds %s", pid, filepath.Join(cfg.StateDir, "state.db"))
	}
	_ = removeServePID(cfg.StateDir)
	fmt.Printf("stopped darkfactory (pid %d); the session host and its PTYs are untouched\n", pid)
	return nil
}

// lockedError is the store error a second `serve` sees. Kept here so the
// dispatch does not import bbolt.
func lockedError(err error) bool {
	return errors.Is(err, store.ErrLocked)
}
