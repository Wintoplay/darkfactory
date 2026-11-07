# Lessons from Fabro (fabro-sh/fabro)

**Why this teacher.** Fabro is the closest thing to our *name* and the furthest
from our *architecture*: a Rust workflow runtime that calls itself "the open
source dark software factory". It answers a question our requirements do not
yet ask — **what is the unit of work?** Its answer (an immutable workflow
version, a run, stages, checkpoints) is the missing layer between "a PTY
session" and "unattended work".

Companion to [orca-lessons.md](orca-lessons.md) (reliability),
[openbot-lessons.md](openbot-lessons.md) (governance) and
[feature-leverage.md](feature-leverage.md) (build/launch/copy rules).

- Studied 2026-10-29 from the docs site, `Cargo.toml`, and the repo layout.
  No clone, no build, no install.
- MIT, Rust. 1.8k stars, 194 forks, **5,605 commits since 2026-03-13** (7.5
  months, ~25/day), 127 open issues, ~181 MB repo, `v0.400.0-nightly.0`.
- 4 apps + 19 components + 20 foundation crates. SQLite (bundled), Axum, SSE,
  React 19 (Bun, embedded via `rust-embed`).
- Four external sibling libraries, all git deps on `main`: `pebble` (agent
  loop), `petri` (workflow engine), `sandbox-driver`, `lithos-llm`.

**Verdict up front:** steal the *run model*, refuse the *agent loop*. Fabro is
two products in one repo — a workflow runtime and a coding-agent harness. We
want the first and must not build the second.

---

## 1. How it works

### Author time

Three inputs: a **workflow graph** in Graphviz DOT (`.fabro`), an optional
**run config** (`.toml`), and credentials. Nodes are `agent`, `prompt`,
`command`, `human` (hexagon), `wait`, `conditional` (diamond), `parallel`
(component), `merge` (tripleoctagon), plus `start`/`exit` shapes. Edges carry
conditions and are the routing logic. A CSS-like **model stylesheet** assigns
models per node/class, with ordered provider fallbacks.

### Create and admit

The CLI packages the workflow and its file references, registers an
**immutable, content-addressed workflow version**, then asks the server to
create a run from it. The engine lowers the graph, validates it, **pins the
models/providers each node names**, and either admits the graph or refuses the
run with diagnostics. The resolved settings are persisted with the run, so a
resume cannot silently pick a different provider.

This two-step (register version → create run) is a governance idea in
disguise: at any later point you can answer "what was supposed to happen?"
without trusting the working tree.

### Execute

Per node the engine resolves context, dispatches to the node's handler,
collects an outcome (`succeeded`, `failed`, `partially_succeeded`, `skipped`),
selects the next edge, and **checkpoints**. A **node** is author-time; a
**stage** is one execution of it. Loops produce `work@1`, `work@2`, … — the
ordinal is independent of retry count and graph visit count. Resuming an
in-flight stage allocates a new stage id (`work@2`) with
`resumed_from_stage_id = work@1` and leaves the old record immutable.

### Persist

- **Durable run store** = the event log; projections (stages, checkpoints,
  conclusions) are folds over it. Resume reads the store, not scratch files.
- **Run branch** `fabro/run/<id>` = one git commit per completed node:
  `fabro({run_id}): {node} ({status})` with `Fabro-Run` and `Fabro-Completed`
  trailers. Inspect with plain `git log/show/diff`; resume resets the
  workspace to the recorded SHA.
- **CAS** = artifacts and offloaded context; **DuckDB** for SQL over events.
- Human questions are rows served over HTTP/SSE; the engine blocks the stage
  until answered.

### Two interfaces, one engine

`fabro run` (CLI, starts a local server if needed, follows the run) and
`fabro server start` (HTTP + scheduler, FIFO, `max_concurrent_runs`, Unix
socket by default). Same graph, same engine, same sandboxes.

---

## 2. The agent-level question

Since 2026-05-18 there are exactly **two** agent backends, and the legacy
`cli` backend is rejected outright:

| Backend | Who owns the loop | What Fabro owns | What it gives up |
|---|---|---|---|
| `api` (default) | **Fabro** (via `pebble`) | prompts, 8 built-in tools (`shell`, `read_file`, `write_file`, `edit_file`, `grep`, `glob`, `web_search`, `web_fetch`), context/fidelity, sub-agents, MCP, compaction, provider failover, token budgets | all agent behaviour is now its problem |
| `acp` | the external ACP process | sandbox, cancellation, `git diff` before/after | session caching, sub-agents, failover |

So: **yes, Fabro's default meddles with the agent level — completely.** It has
to, because a workflow needs a *completion signal*: "this node is done" and
"this node failed" are not observable from a terminal. There are only three
ways to get that signal:

1. own the loop (Fabro `api`; the expensive path),
2. speak a protocol (Fabro `acp`; ACP, OSC 7501, hooks),
3. invoke the agent **once, headless, and read the exit code** (the cheap
   middle Fabro does not offer).

They deliberately removed the third-party CLI path rather than keep it: it
made credential flow ambiguous and behaviour untestable. Their docs still
have stale "CLI-backed agents" text in the permissions page — a reminder that
at 5,600 commits, docs drift.

There is **no PTY path at all**. Fabro states plainly that it is not a REPL.
That is the cleanest evidence for our own boundary: DarkFactory's terminal-
first model and Fabro's workflow model are different products that compose at
the run/step boundary, not competitors.

Cost of owning the loop, from their side: `pebble` is a *separate library*
with its own tools, sub-agents, skills, compaction and loop detection, and
Fabro still needed 5,600 commits of platform around it. We should not build
`pebble`. We should add a `Run` whose steps are `exec` (one-shot CLI), `pty`
(interactive, human-gated) or `command` (shell).

---

## 3. What is worth copying

Ranked by value to us per line of code.

| # | Idea | Why it matters here | Cost |
|---|---|---|---|
| F1 | **Run = goal + immutable plan + resolved settings snapshot** | gives the ledger a subject; makes "who approved what" answerable | model + 2 endpoints, ~150 LOC |
| F2 | **Node vs stage, `node@visit`, append-only on resume** | loops and resumes stop rewriting history; matches our `incarnationId` instinct | ~60 LOC in the store |
| F3 | **Checkpoint commit per step on a dedicated branch** | every step leaves a reviewable diff and a resume point; plain `git` is the UI | ~80 LOC |
| F4 | **Human gate = named edge choice**, not a boolean | `[A] Approve` / `[R] Revise` is a typed decision the ledger can record | ~40 LOC + schema |
| F5 | **Goal gates** — required checks fail the run even at `exit` | prevents "route around the verification" | ~40 LOC |
| F6 | **Failure signatures + loop budget** | stops the same failure from retrying forever | ~50 LOC |
| F7 | **Events are truth; state is a projection** | crash-consistent resume, no mutable row to corrupt | scope to runs/stages, ~200 LOC |
| F8 | **Environment catalog with honest isolation labels** | `local` is documented as "same access as the host"; validates OP-7 | config only |
| F9 | **Redaction as a shared utility**, late secret resolution | matches NF-S-2; they redact agent output before persistence | ~60 LOC |
| F10 | **Persist the resolved model/provider at admission** | retries and resumes are deterministic | free, if we record preset resolution |
| F11 | **Strategy docs per subsystem** (logging, events, testing, errors, migrations) | cheap discipline that survives contributor churn | docs |

Copy budget: **F1–F6 ≈ 420 LOC**, which is the same size as our current
"build-now" list and buys the missing layer.

---

## 4. What not to copy

- **The agent loop and its tool set.** A product, not a feature; conflicts
  with our boundary rule and with "never parse agent internals".
- **DOT/Graphviz as the authoring language.** Free layout, but a DSL tax, a
  vendored C library (`graphviz-sys`), and a second parser to secure. A flat
  list of steps is enough for v1; a DAG later, in JSON/TOML.
- **A 43-crate workspace with four git dependencies tracking `main`.** Their
  lockfile pins commits, but the shape is a supply-chain liability we
  explicitly refuse (NF-S-3).
- **Telemetry.** `fabro-telemetry` ships Segment analytics and Sentry crash
  reporting. Directly contrary to NF-S-1; noted, not copied.
- **Cloud sandboxes, GitHub App, Slack, React SPA, Bun.** Out of scope.
- **Their permission model.** `read-only` / `read-write` / `full`, with
  "always" upgrading the whole session. It is a per-tool prompt, not a
  principal/capability model; unknown/MCP tools default to the *most*
  dangerous class. Ours is stronger (GV-1..GV-3) — keep it.
- **Parallel fan-out on a shared checkout.** They document it themselves:
  branches share one working directory, "concurrent writes can race or
  overwrite each other… Fabro does not isolate branch files". We already have
  the better primitive: one worktree per session.

---

## 5. Where we are already ahead

| Dimension | Fabro | DarkFactory |
|---|---|---|
| Principals | none: single-tenant, every authenticated user is equal, no ACLs | human / overseer / worker, capability-scoped |
| Interactive work | none by design ("not a REPL") | PTY attach, resize, replay |
| Isolation per agent | shared checkout per run | worktree per session |
| Outbound calls / telemetry | Segment + Sentry, upgrade check | none (NF-S-1) |
| Dependency shape | 43 crates, 4 git deps on `main` | 2 direct deps, vendored |
| Threat model | sandbox + tool permissions | principals + policy + ledger + optional jail |

Their own security page is candid: no multi-tenancy, no roles, no rate
limiting, research preview, no pen test. That is not a flaw to mock — it is a
scope choice — but it means Fabro cannot be the thing that stops a rogue
agent from controlling everything. That remains our job.

---

## 6. The gap this exposes

Our model is `Repo → Worktree → Session`. There is **no unit of work**: no
goal, no plan, no outcome, no "what was supposed to happen". That is why
"unattended" currently means "a terminal is still open", and why the overseer
has nothing structured to supervise.

### 6.1 Immutable history, movable pointer

The plan is not a frozen file. It is a **sequence of immutable versions**, and
the run holds a pointer to the version in force:

```text
v1 ──────► v2 ──────► v3        content-addressed, immutable
            ▲
      step 5 hits "the endpoint doesn't exist"
      steps 1–4 ran under v1; step 5 onward under v2 — forever answerable
```

"Immutable" protects *history and authority*, not present intent. Four things
break if the plan is a mutable file:

1. **Audit** — "step 7 ran under the approved plan" is unanswerable unless the
   step records *which* plan.
2. **Resume** — after a crash you need what was intended, not what someone has
   edited since.
3. **Approval binding** — a human approves a hash, not a path that can change
   underneath.
4. **Tamper resistance** — if the plan lives in the worktree, a worker with
   write access can edit its own orders and then act "legitimately". A plan the
   agent can edit is a suggestion, not a control. Store it in daemon state.

Roadblocks do not all require touching the plan:

| Roadblock | Response | Plan changes? |
|---|---|---|
| Tests/lint/type errors | in-step fix loop, bounded by attempts | no |
| Wrong path, missing dep, bad assumption | agent adapts in-step, or the step retries | no |
| Approach wrong / scope changed | **replan** → new version, pointer moves at the next step | yes |
| Credentials missing, service down, needs a decision | step → `blocked`, run pauses, escalate | no |
| Forbidden action | policy refuses, typed refusal, run fails | no |

Mechanics: a replan is a **proposal** `{baseVersion, newPlan, reason}`; policy
decides auto vs human by diff class (adding a retry path is cheap; changing a
target or scope is not). It applies **only at a step boundary** — mid-step the
options are steer (if the protocol allows), wait, or cancel. `maxReplans`
bounds adaptation so it does not become thrash.

Fabro's shape agrees: workflow versions are immutable, steering is injected as
a message rather than a graph edit, and forks/retries/rewinds create new runs
instead of mutating the old one. Recovery paths live *inside* the plan
(`condition="outcome=failed"`), which is why most surprises never need a
replan at all.

### 6.2 Proposed RN domain

For [requirements.md](requirements.md) §2.10 (now drafted):

| ID | Pri | M | Requirement |
|---|---|---|---|
| RN-1 | P1 | M1 | A **Run** is `{id, worktreeId, goal, planHash, state, outcome, startedAt, endedAt}`; a terminal run is immutable |
| RN-2 | P1 | M1 | A **plan version** is immutable and content-addressed, stored in daemon state — never in the agent-writable worktree. Each step records the version that governed it; a replan appends a version and moves the pointer |
| RN-3 | P1 | M1 | A **Step** is `command` (shell), `exec` (one-shot headless agent, exit code is the outcome), or `pty` (interactive; human- or protocol-completed). **No `api` step.** |
| RN-4 | P1 | M1 | Steps are append-only with an ordinal (`step@visit`); a resume creates a new instance and links `resumedFrom` |
| RN-5 | P2 | M2 | Each completed step checkpoints the worktree on `darkfactory/run/<id>` with trailers `DarkFactory-Run` / `DarkFactory-Step` |
| RN-6 | P2 | M2 | A human gate resolves to a **named transition** recorded in the ledger, not a boolean |
| RN-7 | P2 | M2 | Goal gates: a run cannot be `succeeded` if any required check did not pass |
| RN-8 | P2 | M2 | Repeated identical failures abort at a budget (failure signature + counts) |
| RN-9 | P1 | M1 | Step states include `blocked` (a state, not an outcome); `unknown` is never an outcome |
| RN-10 | P2 | M2 | Resume refuses when the worktree or checkpoint SHA is gone; it never pretends |
| RN-11 | P2 | M2 | A replan is a proposal; policy decides auto vs gate by diff class; `maxReplans` bounds it |
| RN-12 | P2 | M2 | Plans change only at a step boundary; mid-step is steer, wait, or cancel |

Three cheap experiments, in order:

1. **Run + Step model, `command`/`exec`/`pty` only** (~300 LOC). This is the
   whole thesis; if it feels wrong, stop before building more. The v1 plan is a
   single frozen version of the ordered step list; replanning lands with the
   gateway.
2. **Checkpoint branch + `darkfactory timeline`** (~80 LOC). Pure git, no new
   storage.
3. **Named human gates + goal gates** (~80 LOC). Makes approvals and
   "definition of done" first-class.

Defer: ACP steps (already EX-3), DAG/parallel steps, DOT, event-sourced
projections, cost accounting.

---

## 7. Naming

Fabro's tagline is literally "the open source dark software factory", and
"dark factory" is now a category term (FANUC, then Dan Shapiro's five levels,
then Fabro's docs page). Internal use is fine. If DarkFactory is ever
published, either differentiate explicitly — *a local workspace and
governance layer for agent processes, not a workflow runtime* — or rename.
The overlap is conceptual, not just lexical: their product is what our name
promises, which is exactly why this document exists.

---

## 8. The decision this leaves us

Fabro proves the process layer is buildable without owning an agent loop only
if you accept one of three completion signals. The question for DarkFactory is
not "can we learn from it" — we can — but:

> Do we want to be a workspace that sessions happen in, or a factory that
> runs happen in?

Our name and the overseer requirement both point at the second. The
requirements currently describe the first.
