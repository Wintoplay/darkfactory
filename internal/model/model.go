package model

import "time"

type Repo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Git  bool   `json:"git"`
	// Position is the repo group's place in the fleet, set by dragging its
	// header or a lone workspace row: 1..N after a reorder, 0 for a repo nobody
	// has moved (it sorts after the positioned ones, oldest first).
	Position  int       `json:"position,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Worktree struct {
	ID          string `json:"id"`
	RepoID      string `json:"repoId"`
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	BaseRef     string `json:"baseRef"`
	Workspace   bool   `json:"workspace,omitempty"`
	Containment string `json:"containment,omitempty"`
	// Position is the row's place in the fleet, set by a drag in the UI and
	// scoped to the repo: 1..N after a reorder, 0 for a row nobody has moved
	// (it sorts after the positioned ones, oldest first).
	Position  int       `json:"position,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type SessionStatus string

const (
	SessionRunning SessionStatus = "running"
	SessionExited  SessionStatus = "exited"
	SessionLost    SessionStatus = "lost"
)

type RunState string

const (
	RunDraft     RunState = "draft"
	RunRunning   RunState = "running"
	RunWaiting   RunState = "waiting"
	RunBlocked   RunState = "blocked"
	RunSucceeded RunState = "succeeded"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

func (s RunState) Terminal() bool {
	return s == RunSucceeded || s == RunFailed || s == RunCancelled
}

type RunOutcome string

const (
	OutcomeSucceeded          RunOutcome = "succeeded"
	OutcomeFailed             RunOutcome = "failed"
	OutcomePartiallySucceeded RunOutcome = "partially_succeeded"
	OutcomeSkipped            RunOutcome = "skipped"
)

type PlanStep struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name,omitempty"`
	Command []string `json:"command,omitempty"`
	Agent   string   `json:"agent,omitempty"`
	Args    []string `json:"args,omitempty"`
	Title   string   `json:"title,omitempty"`
}

type Plan struct {
	Hash      string     `json:"hash"`
	Steps     []PlanStep `json:"steps"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Run struct {
	ID         string     `json:"id"`
	WorktreeID string     `json:"worktreeId"`
	Goal       string     `json:"goal"`
	PlanHash   string     `json:"planHash"`
	State      RunState   `json:"state"`
	Outcome    RunOutcome `json:"outcome,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type StepState string

const (
	StepPending   StepState = "pending"
	StepRunning   StepState = "running"
	StepWaiting   StepState = "waiting"
	StepBlocked   StepState = "blocked"
	StepSucceeded StepState = "succeeded"
	StepFailed    StepState = "failed"
	StepSkipped   StepState = "skipped"
)

func (s StepState) Terminal() bool {
	return s == StepSucceeded || s == StepFailed || s == StepSkipped
}

type Step struct {
	ID          string     `json:"id"`
	RunID       string     `json:"runId"`
	Ordinal     int        `json:"ordinal"`
	Visit       int        `json:"visit"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name,omitempty"`
	Spec        PlanStep   `json:"spec"`
	PlanHash    string     `json:"planHash"`
	State       StepState  `json:"state"`
	Attempt     int        `json:"attempt"`
	ExitCode    *int       `json:"exitCode,omitempty"`
	Output      string     `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	SessionID   string     `json:"sessionId,omitempty"`
	ResumedFrom string     `json:"resumedFrom,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type Session struct {
	ID            string        `json:"id"`
	WorktreeID    string        `json:"worktreeId"`
	Kind          string        `json:"kind"`
	Agent         string        `json:"agent,omitempty"`
	Origin        string        `json:"origin,omitempty"`
	Command       string        `json:"command"`
	Args          []string      `json:"args,omitempty"`
	Title         string        `json:"title"`
	Status        SessionStatus `json:"status"`
	Cols          uint16        `json:"cols"`
	Rows          uint16        `json:"rows"`
	ExitCode      *int          `json:"exitCode,omitempty"`
	PID           int           `json:"pid,omitempty"`
	PGID          int           `json:"pgid,omitempty"`
	StartTicks    uint64        `json:"startTicks,omitempty"`
	IncarnationID string        `json:"incarnationId,omitempty"`
	Containment   string        `json:"containment,omitempty"`
	// Pinned keeps this session at the top of its workspace's list. It is a
	// preference, not a status: a pinned session still sorts among the pinned
	// by Position, then age.
	Pinned bool `json:"pinned,omitempty"`
	// Position is the session's place inside its workspace, set by dragging
	// its row: 1..N after a reorder, 0 for a row nobody has moved (it sorts
	// after the positioned ones, oldest first).
	Position int `json:"position,omitempty"`
	// ClientRequestID is the Idempotency-Key the client sent when it asked for
	// this session. A retried create finds this row and returns it instead of
	// starting a second PTY.
	ClientRequestID string     `json:"clientRequestId,omitempty"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}
