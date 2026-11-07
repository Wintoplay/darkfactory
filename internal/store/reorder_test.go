package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
)

// seedWorktrees writes n worktrees into one repo, oldest first, and returns them
// in the order they were created.
func seedWorktrees(t *testing.T, st *Store, repoID string, n int) []model.Worktree {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows := make([]model.Worktree, 0, n)
	for i := 0; i < n; i++ {
		wt, err := st.PutWorktree(model.Worktree{
			RepoID:    repoID,
			Path:      "/tmp/wt-" + string(rune('a'+i)),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, wt)
	}
	return rows
}

func ids(rows []model.Worktree) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func TestReorderWorktreesSetsTheFleetOrder(t *testing.T) {
	st := openLedger(t)
	rows := seedWorktrees(t, st, "repo_1", 3)

	// Created order is a, b, c; ask for c, a, b.
	ordered, err := st.ReorderWorktrees("repo_1", []string{rows[2].ID, rows[0].ID, rows[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(ordered); got[0] != rows[2].ID || got[1] != rows[0].ID || got[2] != rows[1].ID {
		t.Fatalf("reorder returned %v", got)
	}
	for i, row := range ordered {
		if row.Position != i+1 {
			t.Fatalf("row %s has position %d, want %d", row.ID, row.Position, i+1)
		}
	}

	listed, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(listed); got[0] != rows[2].ID || got[1] != rows[0].ID || got[2] != rows[1].ID {
		t.Fatalf("ListWorktrees = %v, want the new order", got)
	}
}

func TestReorderKeepsSiblingsTheCallerDidNotName(t *testing.T) {
	st := openLedger(t)
	rows := seedWorktrees(t, st, "repo_1", 3)

	// A tab that only knows about c and a: b keeps its age order and goes last.
	if _, err := st.ReorderWorktrees("repo_1", []string{rows[2].ID, rows[0].ID}); err != nil {
		t.Fatal(err)
	}
	listed, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(listed); got[0] != rows[2].ID || got[1] != rows[0].ID || got[2] != rows[1].ID {
		t.Fatalf("ListWorktrees = %v, want c, a, then the unnamed b", got)
	}
}

func TestReorderPutsANewWorktreeLast(t *testing.T) {
	st := openLedger(t)
	rows := seedWorktrees(t, st, "repo_1", 2)
	if _, err := st.ReorderWorktrees("repo_1", []string{rows[1].ID, rows[0].ID}); err != nil {
		t.Fatal(err)
	}
	// Position 0 must sort after anything positioned, or a new row would jump to
	// the top of a repo the user has already arranged.
	fresh, err := st.PutWorktree(model.Worktree{
		RepoID:    "repo_1",
		Path:      "/tmp/wt-fresh",
		CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(listed); got[0] != rows[1].ID || got[1] != rows[0].ID || got[2] != fresh.ID {
		t.Fatalf("ListWorktrees = %v, want the arranged pair then the new row", got)
	}
}

func TestReorderRejectsAWorktreeFromAnotherRepo(t *testing.T) {
	st := openLedger(t)
	mine := seedWorktrees(t, st, "repo_1", 2)
	theirs := seedWorktrees(t, st, "repo_2", 1)

	_, err := st.ReorderWorktrees("repo_1", []string{mine[0].ID, theirs[0].ID})
	if !errors.Is(err, ErrOrderInvalid) {
		t.Fatalf("err = %v, want ErrOrderInvalid", err)
	}
	// Nothing moved.
	listed, _ := st.ListWorktrees()
	var after []model.Worktree
	for _, wt := range listed {
		if wt.RepoID == "repo_1" {
			after = append(after, wt)
		}
	}
	if got := ids(after); got[0] != mine[0].ID || got[1] != mine[1].ID {
		t.Fatalf("a refused reorder changed the order: %v", got)
	}
}

func TestReorderRejectsDuplicates(t *testing.T) {
	st := openLedger(t)
	rows := seedWorktrees(t, st, "repo_1", 2)
	if _, err := st.ReorderWorktrees("repo_1", []string{rows[0].ID, rows[0].ID}); !errors.Is(err, ErrOrderInvalid) {
		t.Fatalf("err = %v, want ErrOrderInvalid", err)
	}
}

func TestReorderLeavesOtherReposAlone(t *testing.T) {
	st := openLedger(t)
	mine := seedWorktrees(t, st, "repo_1", 2)
	theirs := seedWorktrees(t, st, "repo_2", 2)
	if _, err := st.ReorderWorktrees("repo_1", []string{mine[1].ID, mine[0].ID}); err != nil {
		t.Fatal(err)
	}
	listed, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	var other []model.Worktree
	for _, wt := range listed {
		if wt.RepoID == "repo_2" {
			other = append(other, wt)
		}
	}
	if got := ids(other); got[0] != theirs[0].ID || got[1] != theirs[1].ID {
		t.Fatalf("repo_2 order changed: %v", got)
	}
}

func TestReorderUnknownRepo(t *testing.T) {
	st := openLedger(t)
	if _, err := st.ReorderWorktrees("repo_missing", []string{"wt_1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// seedRepos writes n repos, oldest first, and returns them in creation order.
func seedRepos(t *testing.T, st *Store, n int) []model.Repo {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows := make([]model.Repo, 0, n)
	for i := 0; i < n; i++ {
		repo, err := st.PutRepo(model.Repo{
			Name:      "repo-" + string(rune('a'+i)),
			Path:      "/tmp/repo-" + string(rune('a'+i)),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, repo)
	}
	return rows
}

func repoIDs(rows []model.Repo) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func TestReorderReposSetsTheFleetOrder(t *testing.T) {
	st := openLedger(t)
	rows := seedRepos(t, st, 3)

	// Created order is a, b, c; ask for c, a, b.
	ordered, err := st.ReorderRepos([]string{rows[2].ID, rows[0].ID, rows[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := repoIDs(ordered); got[0] != rows[2].ID || got[1] != rows[0].ID || got[2] != rows[1].ID {
		t.Fatalf("reorder returned %v", got)
	}
	for i, row := range ordered {
		if row.Position != i+1 {
			t.Fatalf("repo %s has position %d, want %d", row.ID, row.Position, i+1)
		}
	}

	listed, err := st.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if got := repoIDs(listed); got[0] != rows[2].ID || got[1] != rows[0].ID || got[2] != rows[1].ID {
		t.Fatalf("ListRepos = %v, want the new order", got)
	}
}

func TestReorderReposKeepsUnnamedReposAndAppendsNewOnes(t *testing.T) {
	st := openLedger(t)
	rows := seedRepos(t, st, 3)
	// A stale tab names only c and a; b keeps its age order and goes last.
	if _, err := st.ReorderRepos([]string{rows[2].ID, rows[0].ID}); err != nil {
		t.Fatal(err)
	}
	// A new repo has Position 0 and must join after the arranged ones.
	fresh, err := st.PutRepo(model.Repo{
		Name:      "repo-fresh",
		Path:      "/tmp/repo-fresh",
		CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := st.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{rows[2].ID, rows[0].ID, rows[1].ID, fresh.ID}
	if got := repoIDs(listed); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("ListRepos = %v, want %v", got, want)
	}
}

func TestReorderReposRefusesDuplicatesAndUnknownIDs(t *testing.T) {
	st := openLedger(t)
	rows := seedRepos(t, st, 2)
	if _, err := st.ReorderRepos([]string{rows[0].ID, rows[0].ID}); !errors.Is(err, ErrRepoOrderInvalid) {
		t.Fatalf("duplicate err = %v, want ErrRepoOrderInvalid", err)
	}
	if _, err := st.ReorderRepos([]string{rows[0].ID, "repo_missing"}); !errors.Is(err, ErrRepoOrderInvalid) {
		t.Fatalf("unknown err = %v, want ErrRepoOrderInvalid", err)
	}
	if _, err := st.ReorderRepos(nil); !errors.Is(err, ErrRepoOrderInvalid) {
		t.Fatalf("empty err = %v, want ErrRepoOrderInvalid", err)
	}
	listed, _ := st.ListRepos()
	if got := repoIDs(listed); got[0] != rows[0].ID || got[1] != rows[1].ID {
		t.Fatalf("a refused reorder moved repos: %v", got)
	}
}

// seedSessions writes n sessions into one workspace, oldest first.
func seedSessions(t *testing.T, st *Store, worktreeID string, n int) []model.Session {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows := make([]model.Session, 0, n)
	for i := 0; i < n; i++ {
		sess, err := st.PutSession(model.Session{
			WorktreeID: worktreeID,
			Kind:       "shell",
			Command:    "/bin/sh",
			Title:      "s" + string(rune('a'+i)),
			Status:     model.SessionExited,
			CreatedAt:  base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, sess)
	}
	return rows
}

func sessionIDs(rows []model.Session) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func TestReorderSessionsSetsTheWorkspaceOrder(t *testing.T) {
	st := openLedger(t)
	rows := seedSessions(t, st, "wt_1", 3)

	ordered, err := st.ReorderSessions("wt_1", []string{rows[2].ID, rows[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	// The unnamed session keeps its place behind the named ones.
	want := []string{rows[2].ID, rows[0].ID, rows[1].ID}
	if got := sessionIDs(ordered); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("reorder returned %v, want %v", got, want)
	}
	all, err := st.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(all); got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("stored order = %v, want %v", got, want)
	}

	if _, err := st.ReorderSessions("wt_1", []string{"missing"}); !errors.Is(err, ErrSessionOrderInvalid) {
		t.Fatalf("unknown id error = %v, want ErrSessionOrderInvalid", err)
	}
	other := seedSessions(t, st, "wt_2", 1)
	if _, err := st.ReorderSessions("wt_2", []string{other[0].ID, rows[0].ID}); !errors.Is(err, ErrSessionOrderInvalid) {
		t.Fatalf("foreign workspace error = %v, want ErrSessionOrderInvalid", err)
	}
	if _, err := st.ReorderSessions("empty", []string{rows[0].ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty workspace error = %v, want ErrNotFound", err)
	}
}

func TestPinnedSessionsSortBeforeTheManualOrder(t *testing.T) {
	st := openLedger(t)
	rows := seedSessions(t, st, "wt_1", 3)

	if _, err := st.SetSessionPinned(rows[2].ID, true); err != nil {
		t.Fatal(err)
	}
	all, err := st.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if all[0].ID != rows[2].ID {
		t.Fatalf("pinned session = %q, want %q first", all[0].ID, rows[2].ID)
	}

	// A reorder still assigns every position; the pin keeps floating to the top.
	ordered, err := st.ReorderSessions("wt_1", []string{rows[0].ID, rows[1].ID, rows[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if ordered[2].Position != 3 {
		t.Fatalf("unnamed position = %d, want 3", ordered[2].Position)
	}
	all, err = st.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if all[0].ID != rows[2].ID {
		t.Fatalf("reorder dropped the pin: %v", sessionIDs(all))
	}

	if _, err := st.SetSessionPinned("missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin missing error = %v, want ErrNotFound", err)
	}
}
