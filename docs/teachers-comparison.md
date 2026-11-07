# Three teachers, three layers — and the layer above them

Orca, OpenBot and Hermes Agent are not three versions of the same thing. They
sit at three different layers, and most of the confusion about "should we build
that?" disappears once you place them. A fourth teacher, [Fabro](fabro-lessons.md),
sits above them:

```text
┌───────────────────────────────────────────────────────────────┐
│  PLATFORM / GOVERNANCE   →  OpenBot                           │
│  who may act, on what, with which credential, and what is      │
│  recorded. Bring any agent.                                    │
├───────────────────────────────────────────────────────────────┤
│  PROCESS / ORCHESTRATION →  Fabro                             │
│  runs, stages, checkpoints, gates: what happens, in what       │
│  order, and how it is verified.                                │
├───────────────────────────────────────────────────────────────┤
│  WORKSPACE / FLEET       →  Orca, and DarkFactory              │
│  where the work happens: worktrees, PTYs, sessions, launches.  │
│  Other people's agents run here.                               │
├───────────────────────────────────────────────────────────────┤
│  AGENT RUNTIME           →  Hermes Agent                       │
│  the loop itself: model, tools, memory, skills, channels.      │
└───────────────────────────────────────────────────────────────┘
```

They compose rather than compete. Orca already ships a `hermes` preset
(`hermes --tui`, with a startup-query prompt contract). OpenBot does **not** list
Hermes among its AG-UI harnesses — Hermes is not an AG-UI endpoint, so it would
need an adapter. DarkFactory, which owns the workspace and process layers, would
simply launch `hermes` like any other agent.

The rest of this note compares the three agent-facing tools on the axes that
matter, then extracts what each one actually teaches.

> **Fabro is the fourth teacher**, and it is compared separately: its unit of
> work is a *run*, not a session, so it does not fit the three-way table below.
> See [fabro-lessons.md](fabro-lessons.md).

---

## 1. Side by side

| | **Hermes Agent** | **Orca** | **OpenBot** |
|---|---|---|---|
| **Category** | The agent itself | Workspace/fleet for agents | Governance platform for agents |
| **Stack** | Python 3.14, SQLite+FTS5 | TypeScript, Electron + daemon | TypeScript, Bun/Hono, Postgres+pgvector |
| **Who owns the loop** | Hermes | The launched CLI (Claude, Codex, …) | The brought agent (AG-UI) |
| **Unit of work** | Session / profile | Worktree + PTY session | Bot + computer + thread |
| **Model choice** | 18+ providers, any model | Whatever each vendor CLI uses | Whatever each AG-UI agent uses |
| **Tools** | 70+ tools, ~28 toolsets, MCP, plugins | None of its own; it hosts CLIs | MCP + components + browser, all granted |
| **Execution** | 6 terminal backends: local, Docker, SSH, Modal, Daytona, Vercel Sandbox | Local PTY on the host | One container per Bot |
| **Multi-agent** | `delegate_task` subagents, profiles | Coordinator/worker: Run/Task/Dispatch | Hops with typed envelope + depth cap |
| **Memory** | First-class: skills, profiles, FTS5 history, Honcho | None (agents keep their own) | Intelligence threads + memory sources |
| **Persistence** | SQLite, session lineage, trajectories | bbolt/SQLite + PTY history on disk | Postgres + external Intelligence |
| **Footprint** | 1.37 GB repo, Python + Node + FFmpeg | ~250 MB app, Electron | 6 services, Postgres, 1.4 GB computer image |
| **Governance** | Approval ladder + deny rules + optional containers | Almost none, by design | Policy engine + audit + handover |
| **Maturity** | 258k stars, 56.6k forks, **51.7k open issues**, ~25k tests | 990 releases in 7 months, 17k commits | v0.2.0, ~2 months old, 703 test files |
| **Honest limit it states** | "Defense-in-depth, not a hard boundary" | "Not security sandboxes" | "Not a security boundary by itself" |

The last row is the most interesting thing in the table: all three say it.

---

## 2. The security models, side by side

This is the axis our own decisions turn on.

| | Hermes | Orca | OpenBot |
|---|---|---|---|
| **Who can act** | The agent, as your uid | The agent, as your uid; the CLI as owner | The gateway, on the Bot's behalf |
| **Decision point** | Per command, in the agent process | None | Server-side, before the effect |
| **Approval** | `smart` / `manual` / `off`, per surface (cron, one-shot, unattended all default deny) | None | Handover state machine |
| **Floor below "off"** | Hardline blocklist (rm -rf /, fork bomb, mkfs, dd to disk, pipe-to-shell) | None | Deny rules + fail-closed policy |
| **Isolation** | Optional container/cloud backend; local = none | None | Container per Bot + egress policy |
| **What is recorded** | Session DB, approval history | Session metadata | Every decision, actor + initiator |
| **Explicit bypasses** | `--yolo`, `mode: off`, container backends (checks skipped) | Everything | YOLO does not exist |

Three design choices I want to steal, and one I want to avoid:

**Steal — the named ladder.** `smart | manual | off` is a better vocabulary than
a boolean, because each mode has a defined behaviour on every surface. And the
per-context defaults are the good part: a cron job, a one-shot `-q` run, and a
webhook session all default to **deny**, because "there is no human who can
answer /approve". We will need exactly this the moment an overseer can act
unattended.

**Steal — a floor below the off switch.** `--yolo` still cannot `rm -rf /`. A
safety mode that can be switched off entirely is not a safety mode; it is a
preference. Our "human-only verbs" list should be unbypassable by any principal,
including the overseer and including a future "trust this repo" toggle.

**Steal — approval history mining.** `hermes approvals suggest` reads what you
approved, proposes glob patterns ranked by frequency, **never applies
automatically**, and **never proposes destructive classes** ("`rm -rf build/`
approved 100 times still never yields an `rm` entry"). That is the safest
possible learning loop for our overseer: it may propose tightening or widening
the allow-list, a human applies it, and some classes are never learnable.

**Avoid — stacked controls that duplicate each other.** Hermes skips dangerous
command checks inside a container "because the container itself is the security
boundary". That is coherent, but it means the *approval* guarantee disappears
the moment you switch backends, which is a surprising failure mode. We should
have one load-bearing control per threat and say which one it is.

---

## 3. What each one is genuinely best at

### Hermes — the loop, and the honesty about it

Hermes is the only one of the three that owns model calls, tools, memory, and
context. That buys it the ability to do things the others cannot: prompt caching
that survives a conversation, context compression with lineage, skills that
self-improve, trajectory export for training. Its design principles are worth
reading even if you never run it:

- **Prompt stability** — the system prompt does not change mid-conversation
  except on explicit user actions. (Cache-breaking mutations are a product
  decision, not an implementation detail.)
- **Observable execution** — every tool call is visible; the spinner is not
  decoration, it is the audit surface at human speed.
- **Platform-agnostic core** — one `AIAgent` serves CLI, gateway, ACP, batch and
  API; differences live in the entry point.
- **Profile isolation** — `hermes -p <name>` gets its own home, config, memory,
  sessions and gateway PID.

Its security docs are the best of the three, and unusually candid: they name
what does not work (write guards do not stop `cat`), what is skipped (container
backends skip command checks), and what the threat model excludes (renamed
binaries, aliases, functions).

### Orca — the workspace, and the cost of owning terminals

Orca is the only one that owns the *place* work happens: worktrees, PTYs,
scrollback, agent launch, and a coordination protocol for many agents. That is
why its bug classes are physical — restore, resize, reconnect, orphans — and why
its 5.2:1 fix-to-feature ratio is the number to remember.

Its lesson is not "add features"; it is that a terminal workspace is a
correctness product. See [orca-lessons.md](orca-lessons.md).

### OpenBot — governance, without owning the agent

OpenBot's bet is that the agent is someone else's problem. It supplies identity,
grants, policy, audit, computers and handover, and lets any AG-UI agent plug in.
That is the cleanest separation of concerns of the three, and the reason its
governance ideas port so well to our design.

Its lesson is the gateway order and the two-column ledger. See
[openbot-lessons.md](openbot-lessons.md).

---

## 4. What DarkFactory should take from each

Filtered through the triage in [agent-authority.md](agent-authority.md) §0 —
meaning: only the items whose trigger has already fired.

**From Hermes, eventually (trigger: approvals exist)**

1. The ladder: `paranoid | smart | manual | off` as policy data, with
   per-surface defaults that deny when nobody can answer.
2. A hardline floor no principal can cross — start with: wipe the worktree root,
   force-push to the base branch, write outside the worktree, kill the daemon.
3. `approvals suggest` semantics: propose allow-list changes from history, never
   auto-apply, never propose destructive classes.
4. Approval timeout → deny, and a timeout is not a denial (so the agent may ask
   again without being penalised).
5. Protected paths + a write root + an env passthrough allow-list + read-only
   credential mounts — the concrete answer to "agents need `$HOME`".

**From Hermes, now (cheap, no trigger needed)**

6. **Profile isolation → per-worktree `HOME`.** This is the shape our T1 jail
   needs anyway, and it fixes the agents-share-`~/.claude` problem.
7. **Prompt stability** for the overseer: a system prompt that changes per turn
   makes behaviour non-reproducible and un-auditable.

**From OpenBot, later (trigger: a second principal can act)**

8. Gateway order: resolve → decide → record → act.
9. Fail-closed policy with a test per property.
10. Handover instead of an approval queue; refusals while a human holds control.

**From Orca, now (already in the requirements)**

11. Worktree safety, env probe, restore discipline — R1–R5 in
    [orca-lessons.md](orca-lessons.md).

**From none of them**

12. The agent loop, 70 tools, 25 messaging platforms, a browser per agent, native
    chat, or a relay fleet. Each is a product of its own.

---

## 5. What surprised me

- **The hardline floor.** I had been treating "approval mode" as a binary, and
  Hermes's insight is that the off switch must still hit a wall. That is a
  one-afternoon change with a large effect on worst-case behaviour.
- **Approval-history mining that refuses to learn dangerous things.** A learning
  loop with a class it will never learn is a design pattern, not a limitation.
- **OpenBot's initiator column.** It turns "what happened" into "what happened
  while nobody was watching", which is the question an overseer actually raises.
- **All three write down their own boundary.** The teams that ship agents
  seriously are the ones that say "this is not a sandbox" in the same document
  as the sandbox flags.
- **Scale is not the same as fit.** Hermes is 258k stars and 51.7k open issues:
  enormous adoption, enormous triage debt. We should copy its judgement, not its
  surface area.
