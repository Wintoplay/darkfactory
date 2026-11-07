// Package run owns the process plane: immutable plans and ordered steps.
// It never calls a model and never parses agent output to decide state.
package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

var (
	ErrInvalidPlan = errors.New("invalid plan")
	ErrRunTerminal = errors.New("run is terminal")
	ErrStepNotWait = errors.New("step is not waiting")
)

const maxStepOutput = 64 << 10

type Coordinator struct {
	cfg      config.Config
	store    *store.Store
	sessions *session.Manager
	log      *slog.Logger

	mu        sync.Mutex
	locks     map[string]*sync.Mutex
	cancel    map[string]context.CancelFunc
	executing map[string]bool
}

func New(cfg config.Config, st *store.Store, sessions *session.Manager, log *slog.Logger) *Coordinator {
	if log == nil {
		log = slog.Default()
	}
	return &Coordinator{
		cfg:       cfg,
		store:     st,
		sessions:  sessions,
		log:       log,
		locks:     make(map[string]*sync.Mutex),
		cancel:    make(map[string]context.CancelFunc),
		executing: make(map[string]bool),
	}
}

func (c *Coordinator) runLock(runID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock := c.locks[runID]
	if lock == nil {
		lock = &sync.Mutex{}
		c.locks[runID] = lock
	}
	return lock
}

// CreatePlan validates and content-addresses a plan. The same steps always
// produce the same hash; plans are written under daemon state, never a worktree.
func (c *Coordinator) CreatePlan(steps []model.PlanStep) (model.Plan, error) {
	if len(steps) == 0 {
		return model.Plan{}, fmt.Errorf("%w: at least one step is required", ErrInvalidPlan)
	}
	if len(steps) > 100 {
		return model.Plan{}, fmt.Errorf("%w: at most 100 steps", ErrInvalidPlan)
	}
	clean := make([]model.PlanStep, 0, len(steps))
	for i, step := range steps {
		step.Kind = strings.TrimSpace(step.Kind)
		step.Name = strings.TrimSpace(step.Name)
		if step.Name == "" {
			step.Name = fmt.Sprintf("step %d", i+1)
		}
		switch step.Kind {
		case "command":
			if len(step.Command) == 0 || strings.TrimSpace(step.Command[0]) == "" {
				return model.Plan{}, fmt.Errorf("%w: step %d command needs argv", ErrInvalidPlan, i+1)
			}
			step.Agent = ""
			step.Args = nil
		case "exec", "pty":
			step.Agent = strings.TrimSpace(step.Agent)
			if step.Agent == "" {
				return model.Plan{}, fmt.Errorf("%w: step %d needs an agent", ErrInvalidPlan, i+1)
			}
			if _, ok := c.cfg.Agents[step.Agent]; !ok {
				return model.Plan{}, fmt.Errorf("%w: unknown agent %q", ErrInvalidPlan, step.Agent)
			}
			step.Command = nil
		default:
			return model.Plan{}, fmt.Errorf("%w: unknown kind %q", ErrInvalidPlan, step.Kind)
		}
		clean = append(clean, step)
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return model.Plan{}, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	plan := model.Plan{Hash: hash, Steps: clean, CreatedAt: time.Now().UTC()}
	if existing, err := c.store.GetPlan(hash); err == nil {
		plan = existing
	} else if _, err := c.store.PutPlan(plan); err != nil {
		return model.Plan{}, err
	}
	if err := c.writePlan(plan); err != nil {
		return model.Plan{}, err
	}
	return plan, nil
}

func (c *Coordinator) writePlan(plan model.Plan) error {
	dir := filepath.Join(c.cfg.StateDir, "plans")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create plan dir: %w", err)
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := filepath.Join(dir, plan.Hash+".json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	return nil
}

func (c *Coordinator) Create(worktreeID, goal string, steps []model.PlanStep) (model.Run, error) {
	worktreeID = strings.TrimSpace(worktreeID)
	goal = strings.TrimSpace(goal)
	if worktreeID == "" || goal == "" {
		return model.Run{}, errors.New("worktreeId and goal are required")
	}
	if _, err := c.store.GetWorktree(worktreeID); err != nil {
		return model.Run{}, err
	}
	plan, err := c.CreatePlan(steps)
	if err != nil {
		return model.Run{}, err
	}
	run, err := c.store.PutRun(model.Run{
		WorktreeID: worktreeID,
		Goal:       goal,
		PlanHash:   plan.Hash,
		State:      model.RunDraft,
	})
	if err != nil {
		return model.Run{}, err
	}
	for i, step := range plan.Steps {
		if _, err := c.store.PutStep(model.Step{
			RunID:    run.ID,
			Ordinal:  i + 1,
			Visit:    1,
			Kind:     step.Kind,
			Name:     step.Name,
			Spec:     step,
			PlanHash: plan.Hash,
			State:    model.StepPending,
		}); err != nil {
			return model.Run{}, err
		}
	}
	return run, nil
}

func (c *Coordinator) Start(runID string) (model.Run, error) {
	lock := c.runLock(runID)
	lock.Lock()
	run, err := c.store.GetRun(runID)
	if err != nil {
		lock.Unlock()
		return model.Run{}, err
	}
	if run.State.Terminal() {
		lock.Unlock()
		return run, ErrRunTerminal
	}
	if run.State == model.RunRunning || run.State == model.RunWaiting || run.State == model.RunBlocked {
		lock.Unlock()
		return run, nil
	}
	now := time.Now().UTC()
	run.State = model.RunRunning
	run.StartedAt = &now
	run, err = c.store.PutRun(run)
	lock.Unlock()
	if err != nil {
		return model.Run{}, err
	}
	c.launch(runID, 1)
	return run, nil
}

func (c *Coordinator) launch(runID string, ordinal int) {
	c.mu.Lock()
	if c.executing[runID] {
		c.mu.Unlock()
		return
	}
	c.executing[runID] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.executing, runID)
			c.mu.Unlock()
		}()
		c.execute(runID, ordinal)
	}()
}

func (c *Coordinator) Answer(runID string, ordinal int, result string) (model.Run, error) {
	lock := c.runLock(runID)
	lock.Lock()
	run, err := c.store.GetRun(runID)
	if err != nil {
		lock.Unlock()
		return model.Run{}, err
	}
	if run.State.Terminal() {
		lock.Unlock()
		return run, ErrRunTerminal
	}
	steps, err := c.store.ListSteps(runID)
	if err != nil {
		lock.Unlock()
		return model.Run{}, err
	}
	var target *model.Step
	for i := range steps {
		if steps[i].Ordinal == ordinal && !steps[i].State.Terminal() {
			target = &steps[i]
			break
		}
	}
	if target == nil || target.State != model.StepWaiting {
		lock.Unlock()
		return run, ErrStepNotWait
	}
	now := time.Now().UTC()
	target.EndedAt = &now
	switch result {
	case "succeeded":
		target.State = model.StepSucceeded
	case "failed":
		target.State = model.StepFailed
	case "skipped":
		target.State = model.StepSkipped
	default:
		lock.Unlock()
		return run, fmt.Errorf("%w: result must be succeeded, failed or skipped", ErrInvalidPlan)
	}
	if _, err := c.store.PutStep(*target); err != nil {
		lock.Unlock()
		return model.Run{}, err
	}
	if target.SessionID != "" {
		_, _ = c.sessions.RemoveWithResult(target.SessionID, "run_step_answered")
	}
	if target.State == model.StepFailed {
		run.State = model.RunFailed
		run.Outcome = model.OutcomeFailed
		run.EndedAt = &now
	} else {
		run.State = model.RunRunning
	}
	run, err = c.store.PutRun(run)
	lock.Unlock()
	if err != nil {
		return model.Run{}, err
	}
	if target.State != model.StepFailed {
		c.launch(runID, ordinal+1)
	}
	return run, nil
}

func (c *Coordinator) Cancel(runID string) (model.Run, error) {
	lock := c.runLock(runID)
	lock.Lock()
	run, err := c.store.GetRun(runID)
	if err != nil {
		lock.Unlock()
		return model.Run{}, err
	}
	if run.State.Terminal() {
		lock.Unlock()
		return run, nil
	}
	now := time.Now().UTC()
	run.State = model.RunCancelled
	run.Outcome = model.OutcomeSkipped
	run.EndedAt = &now
	run, err = c.store.PutRun(run)
	c.mu.Lock()
	cancel := c.cancel[runID]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	steps, _ := c.store.ListSteps(runID)
	for i := range steps {
		if steps[i].State == model.StepWaiting || steps[i].State == model.StepRunning {
			if steps[i].SessionID != "" {
				_, _ = c.sessions.RemoveWithResult(steps[i].SessionID, "run_cancelled")
			}
			steps[i].State = model.StepSkipped
			steps[i].EndedAt = &now
			_, _ = c.store.PutStep(steps[i])
		}
	}
	lock.Unlock()
	return run, err
}

func (c *Coordinator) execute(runID string, startOrdinal int) {
	run, err := c.store.GetRun(runID)
	if err != nil {
		c.log.Error("run lookup", "run", runID, "error", err)
		return
	}
	if run.State.Terminal() || run.State == model.RunWaiting {
		return
	}
	steps, err := c.store.ListSteps(runID)
	if err != nil {
		c.finishRun(&run, model.RunFailed, model.OutcomeFailed, err.Error())
		return
	}
	for i := range steps {
		current, err := c.store.GetRun(runID)
		if err != nil || current.State.Terminal() {
			return
		}
		run = current
		step := &steps[i]
		if step.Ordinal < startOrdinal || step.State.Terminal() {
			continue
		}
		if run.State == model.RunCancelled {
			return
		}
		switch step.Kind {
		case "command", "exec":
			if c.runOneShot(&run, step) {
				continue
			}
			return
		case "pty":
			if err := c.startPTYStep(&run, step); err != nil {
				now := time.Now().UTC()
				step.State = model.StepFailed
				step.Error = err.Error()
				step.EndedAt = &now
				_, _ = c.store.PutStep(*step)
				c.finishRun(&run, model.RunFailed, model.OutcomeFailed, err.Error())
			}
			return
		default:
			now := time.Now().UTC()
			step.State = model.StepFailed
			step.Error = "unknown step kind"
			step.EndedAt = &now
			_, _ = c.store.PutStep(*step)
			c.finishRun(&run, model.RunFailed, model.OutcomeFailed, "unknown step kind")
			return
		}
	}
	c.finishRun(&run, model.RunSucceeded, model.OutcomeSucceeded, "")
}

// runOneShot returns true when the step succeeded and the run may continue.
func (c *Coordinator) runOneShot(run *model.Run, step *model.Step) bool {
	now := time.Now().UTC()
	step.State = model.StepRunning
	step.Attempt++
	step.StartedAt = &now
	step.EndedAt = nil
	if _, err := c.store.PutStep(*step); err != nil {
		c.finishRun(run, model.RunFailed, model.OutcomeFailed, err.Error())
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel[run.ID] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.cancel, run.ID)
		c.mu.Unlock()
	}()

	command, args, env, err := c.commandFor(step)
	if err != nil {
		c.failStep(run, step, err.Error())
		return false
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = c.worktreePath(run.WorktreeID)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	if strings.TrimSpace(c.cfg.HomeRoot) != "" {
		home := filepath.Join(c.cfg.HomeRoot, run.WorktreeID)
		if err := os.MkdirAll(home, 0o700); err == nil {
			cmd.Env = append(cmd.Env, "HOME="+home)
		}
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var output cappedBuffer
	output.max = maxStepOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	step.Output = output.String()
	if err != nil {
		step.State = model.StepFailed
		step.Error = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			step.ExitCode = &code
		}
		if !c.commitStep(run.ID, step) {
			return false
		}
		c.finishRun(run, model.RunFailed, model.OutcomeFailed, err.Error())
		return false
	}
	code := 0
	step.ExitCode = &code
	step.State = model.StepSucceeded
	step.Error = ""
	if !c.commitStep(run.ID, step) {
		return false
	}
	return true
}

// commitStep serializes the final step write with Cancel. A cancel that lands
// while a command is running wins; the step is recorded skipped, not succeeded.
func (c *Coordinator) commitStep(runID string, step *model.Step) bool {
	lock := c.runLock(runID)
	lock.Lock()
	defer lock.Unlock()
	run, err := c.store.GetRun(runID)
	if err == nil && run.State == model.RunCancelled {
		now := time.Now().UTC()
		step.State = model.StepSkipped
		step.Error = "run cancelled"
		step.EndedAt = &now
		_, _ = c.store.PutStep(*step)
		return false
	}
	if _, err := c.store.PutStep(*step); err != nil {
		c.log.Error("commit step", "run", runID, "step", step.ID, "error", err)
		return false
	}
	return true
}

func (c *Coordinator) startPTYStep(run *model.Run, step *model.Step) error {
	preset, ok := c.cfg.Agents[step.Spec.Agent]
	if !ok {
		return fmt.Errorf("unknown agent %q", step.Spec.Agent)
	}
	now := time.Now().UTC()
	step.State = model.StepRunning
	step.Attempt++
	step.StartedAt = &now
	if _, err := c.store.PutStep(*step); err != nil {
		return err
	}
	title := step.Spec.Title
	if title == "" {
		title = step.Name
	}
	wt, _ := c.store.GetWorktree(run.WorktreeID)
	containment := strings.TrimSpace(wt.Containment)
	if containment == "" || containment == "auto" {
		if session.JailAvailable() {
			containment = "jail"
		} else {
			containment = "off"
		}
	}
	if strings.TrimSpace(preset.Containment) != "" {
		containment = preset.Containment
		if containment == "auto" {
			if session.JailAvailable() {
				containment = "jail"
			} else {
				containment = "off"
			}
		}
	}
	if containment == "jail" && !session.JailAvailable() {
		containment = "off"
	}
	home := ""
	if containment == "jail" && strings.TrimSpace(c.cfg.HomeRoot) != "" {
		home = filepath.Join(c.cfg.HomeRoot, run.WorktreeID)
	}
	sess, err := c.sessions.Start(session.Spec{
		WorktreeID:  run.WorktreeID,
		Dir:         wt.Path,
		Kind:        "agent",
		Agent:       step.Spec.Agent,
		Origin:      "run",
		Command:     preset.Command,
		Args:        append(append([]string(nil), preset.Args...), step.Spec.Args...),
		Env:         preset.Env,
		Title:       title,
		Containment: containment,
		Home:        home,
		HiddenPaths: []string{c.cfg.ConfigDir, c.cfg.StateDir},
	})
	if err != nil {
		return err
	}
	if _, err := c.store.PutSession(sess.Model()); err != nil {
		sess.Close()
		return err
	}
	lock := c.runLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	current, err := c.store.GetRun(run.ID)
	if err == nil && current.State == model.RunCancelled {
		sess.Close()
		return nil
	}
	step.State = model.StepWaiting
	step.SessionID = sess.ID
	if _, err := c.store.PutStep(*step); err != nil {
		sess.Close()
		return err
	}
	run.State = model.RunWaiting
	if _, err := c.store.PutRun(*run); err != nil {
		return err
	}
	return nil
}

func (c *Coordinator) commandFor(step *model.Step) (string, []string, map[string]string, error) {
	if step.Kind == "command" {
		if len(step.Spec.Command) == 0 {
			return "", nil, nil, errors.New("command step has no argv")
		}
		return step.Spec.Command[0], step.Spec.Command[1:], nil, nil
	}
	preset, ok := c.cfg.Agents[step.Spec.Agent]
	if !ok {
		return "", nil, nil, fmt.Errorf("unknown agent %q", step.Spec.Agent)
	}
	args := append(append([]string(nil), preset.Args...), step.Spec.Args...)
	return preset.Command, args, preset.Env, nil
}

func (c *Coordinator) worktreePath(id string) string {
	wt, err := c.store.GetWorktree(id)
	if err != nil {
		return "."
	}
	return wt.Path
}

func (c *Coordinator) failStep(run *model.Run, step *model.Step, message string) {
	now := time.Now().UTC()
	step.State = model.StepFailed
	step.Error = message
	step.EndedAt = &now
	_, _ = c.store.PutStep(*step)
	c.finishRun(run, model.RunFailed, model.OutcomeFailed, message)
}

func (c *Coordinator) finishRun(run *model.Run, state model.RunState, outcome model.RunOutcome, message string) {
	lock := c.runLock(run.ID)
	lock.Lock()
	defer lock.Unlock()
	if current, err := c.store.GetRun(run.ID); err == nil && current.State.Terminal() {
		*run = current
		return
	}
	now := time.Now().UTC()
	run.State = state
	run.Outcome = outcome
	run.Error = message
	run.EndedAt = &now
	if _, err := c.store.PutRun(*run); err != nil {
		c.log.Error("finish run", "run", run.ID, "error", err)
	}
}

type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.max = maxStepOutput
	}
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := b.buf.String()
	if b.truncated {
		text += "\n[output truncated]\n"
	}
	return text
}
