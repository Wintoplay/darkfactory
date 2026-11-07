package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestServePIDFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := readServePID(dir); !errors.Is(err, errNoPID) {
		t.Fatalf("readServePID on an empty dir = %v, want errNoPID", err)
	}
	if err := writeServePID(dir, 4242); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(pidPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("pid file mode = %o, want 600", perm)
	}
	pid, err := readServePID(dir)
	if err != nil || pid != 4242 {
		t.Fatalf("readServePID = %d, %v; want 4242", pid, err)
	}
	if err := removeServePID(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := readServePID(dir); !errors.Is(err, errNoPID) {
		t.Fatalf("readServePID after remove = %v, want errNoPID", err)
	}
}

func TestReadServePIDRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(pidPath(dir), []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readServePID(dir); err == nil || errors.Is(err, errNoPID) {
		t.Fatalf("readServePID on garbage = %v, want a parse error", err)
	}
}

func TestProcessAliveFollowsARealChild(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !processAlive(pid) {
		t.Fatal("a running child is reported dead")
	}
	if looksLikeServe(pid) {
		t.Fatal("sleep is mistaken for a darkfactory serve process")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if processAlive(pid) {
		t.Fatal("a reaped child is reported alive")
	}
}
