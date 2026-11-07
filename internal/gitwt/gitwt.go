package gitwt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var validPart = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func Create(ctx context.Context, repoPath, worktreePath, branch, baseRef string) error {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return fmt.Errorf("repo path is not a directory: %s", repoPath)
	}
	if !validBranch(branch) {
		return fmt.Errorf("invalid branch name: %q", branch)
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if strings.HasPrefix(baseRef, "-") || strings.ContainsAny(baseRef, " \t\n") {
		return fmt.Errorf("invalid base ref: %q", baseRef)
	}
	worktreePath, err = filepath.Abs(worktreePath)
	if err != nil {
		return fmt.Errorf("resolve worktree: %w", err)
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree path already exists: %s", worktreePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o700); err != nil {
		return fmt.Errorf("create worktree parent: %w", err)
	}
	if err := run(ctx, repoPath, "worktree", "add", "-b", branch, worktreePath, baseRef); err != nil {
		return err
	}
	return nil
}

func Remove(ctx context.Context, repoPath, worktreePath string, force bool) error {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	worktreePath, err = filepath.Abs(worktreePath)
	if err != nil {
		return fmt.Errorf("resolve worktree: %w", err)
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return run(ctx, repoPath, append(args, worktreePath)...)
}

// Dirty lists uncommitted and untracked entries. A worktree exists to hold what
// an agent produced, so removing one is not a cleanup operation: the caller has
// to ask, and the paths are what makes the question answerable.
func Dirty(ctx context.Context, worktreePath string) ([]string, error) {
	out, err := output(ctx, worktreePath, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		// Porcelain v1 is "XY <path>", with a rename adding " -> <new>".
		paths = append(paths, strings.TrimSpace(line[3:]))
	}
	return paths, nil
}

// output runs a git command whose stdout is the answer.
func output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return string(out), nil
}

func run(ctx context.Context, repoPath string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func validBranch(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, "..") {
		return false
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if !validPart.MatchString(part) {
			return false
		}
	}
	return true
}
