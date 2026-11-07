package run

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func testCoordinator(t *testing.T) (*Coordinator, *store.Store, *session.Manager, model.Worktree) {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.WorktreeRoot = t.TempDir()
	cfg.Agents["shell"] = config.AgentPreset{Command: "/bin/sh", Args: []string{"-l"}}
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := session.NewManager(1024)
	t.Cleanup(mgr.CloseAll)
	repo, err := st.PutRepo(model.Repo{Name: "demo", Path: t.TempDir(), Git: true})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := st.PutWorktree(model.Worktree{RepoID: repo.ID, Path: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))), st, mgr, wt
}

func waitRun(t *testing.T, st *store.Store, id string) model.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.State.Terminal() {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, _ := st.GetRun(id)
	t.Fatalf("run did not finish: %#v", run)
	return model.Run{}
}

func TestPlanHashIsStableAndImmutable(t *testing.T) {
	coord, _, _, _ := testCoordinator(t)
	steps := []model.PlanStep{{Kind: "command", Name: "test", Command: []string{"go", "test", "./..."}}}
	first, err := coord.CreatePlan(steps)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coord.CreatePlan(steps)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("plan changed: %#v %#v", first, second)
	}
	path := filepath.Join(coord.cfg.StateDir, "plans", first.Hash+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coord.CreatePlan(steps); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("plan file was rewritten")
	}
}

func TestRunCommandSucceedsAndStoresPlan(t *testing.T) {
	coord, st, _, wt := testCoordinator(t)
	run, err := coord.Create(wt.ID, "say hi", []model.PlanStep{{
		Kind: "command", Name: "hello", Command: []string{"/bin/sh", "-c", "printf 'hello run'"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(coord.cfg.StateDir, "plans", run.PlanHash+".json")); err != nil {
		t.Fatalf("plan file: %v", err)
	}
	if _, err := coord.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	final := waitRun(t, st, run.ID)
	if final.State != model.RunSucceeded || final.Outcome != model.OutcomeSucceeded {
		t.Fatalf("run = %#v", final)
	}
	steps, err := st.ListSteps(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].State != model.StepSucceeded || steps[0].ExitCode == nil || *steps[0].ExitCode != 0 {
		t.Fatalf("steps = %#v", steps)
	}
	if steps[0].Output == "" {
		t.Fatal("command output was not recorded")
	}
}

func TestRunCommandFailureStopsRun(t *testing.T) {
	coord, st, _, wt := testCoordinator(t)
	run, err := coord.Create(wt.ID, "fail", []model.PlanStep{{
		Kind: "command", Name: "fail", Command: []string{"/bin/sh", "-c", "exit 7"},
	}, {
		Kind: "command", Name: "never", Command: []string{"/bin/true"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coord.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	final := waitRun(t, st, run.ID)
	if final.State != model.RunFailed || final.Outcome != model.OutcomeFailed {
		t.Fatalf("run = %#v", final)
	}
	steps, _ := st.ListSteps(run.ID)
	if len(steps) != 2 || steps[0].State != model.StepFailed || steps[0].ExitCode == nil || *steps[0].ExitCode != 7 {
		t.Fatalf("steps = %#v", steps)
	}
	if steps[1].State != model.StepPending {
		t.Fatalf("later step ran after failure: %#v", steps[1])
	}
}

func TestRunPTYWaitsForHumanAnswer(t *testing.T) {
	coord, st, _, wt := testCoordinator(t)
	run, err := coord.Create(wt.ID, "interactive", []model.PlanStep{{
		Kind: "pty", Name: "shell", Agent: "shell",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coord.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := st.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == model.RunWaiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not wait: %#v", current)
		}
		time.Sleep(20 * time.Millisecond)
	}
	steps, _ := st.ListSteps(run.ID)
	if len(steps) != 1 || steps[0].State != model.StepWaiting || steps[0].SessionID == "" {
		t.Fatalf("steps = %#v", steps)
	}
	if _, err := coord.Answer(run.ID, 1, "succeeded"); err != nil {
		t.Fatal(err)
	}
	final := waitRun(t, st, run.ID)
	if final.State != model.RunSucceeded {
		t.Fatalf("run = %#v", final)
	}
}

func TestRunCancelStopsOneShot(t *testing.T) {
	coord, st, _, wt := testCoordinator(t)
	run, err := coord.Create(wt.ID, "cancel", []model.PlanStep{{
		Kind: "command", Name: "sleep", Command: []string{"/bin/sh", "-c", "sleep 30"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coord.Start(run.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := coord.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	final := waitRun(t, st, run.ID)
	if final.State != model.RunCancelled || final.Outcome != model.OutcomeSkipped {
		t.Fatalf("run = %#v", final)
	}
}
