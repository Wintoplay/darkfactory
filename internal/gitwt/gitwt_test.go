package gitwt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndRemoveWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "main")
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "-m", "initial")

	worktree := filepath.Join(root, "wt")
	if err := Create(context.Background(), repo, worktree, "darkfactory/test", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), repo, worktree, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestDirtySeesUntrackedAndModifiedFiles(t *testing.T) {
	repo, worktree := newWorktree(t)

	dirty, err := Dirty(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 0 {
		t.Fatalf("a fresh worktree should be clean, got %v", dirty)
	}

	// An agent's output is usually untracked, which is exactly what a plain
	// `git diff` would miss and what --force would throw away.
	if err := os.WriteFile(filepath.Join(worktree, "agent-output.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = Dirty(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 || dirty[0] != "agent-output.txt" {
		t.Fatalf("dirty = %v, want [agent-output.txt]", dirty)
	}

	// Removing without force must be refused by git itself as a backstop.
	if err := Remove(context.Background(), repo, worktree, false); err == nil {
		t.Fatal("removing a dirty worktree without force should fail")
	}
	if _, err := os.Stat(filepath.Join(worktree, "agent-output.txt")); err != nil {
		t.Fatalf("the file was removed by a refused delete: %v", err)
	}

	if err := Remove(context.Background(), repo, worktree, true); err != nil {
		t.Fatal(err)
	}
}

func newWorktree(t *testing.T) (repo, worktree string) {
	t.Helper()
	root := t.TempDir()
	repo = filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "main")
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "-m", "initial")
	worktree = filepath.Join(root, "wt")
	if err := Create(context.Background(), repo, worktree, "darkfactory/test", "HEAD"); err != nil {
		t.Fatal(err)
	}
	return repo, worktree
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
