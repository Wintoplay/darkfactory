# Lessons from Orca (stablyai/orca)

**Purpose.** DarkFactory is not a subset of Orca — it is a Go-native agent
factory that owns the workspace, the process, and the governance around them.
Orca is one of four teachers (with OpenBot, Hermes Agent and Fabro), and the
closest one to our workspace layer: Go daemon, git worktrees, PTY sessions,
terminal-first agents.
Before we grew it, we studied what Orca actually shipped over the last seven
months — release notes *and* commit history — to learn where the real cost sits
and how they eventually solved it.

This document records the evidence, the lessons, and the requirements those
lessons imply. It is an input to requirement refinement, not a spec.

- **Research date:** 2026-10-29
- **Sources:** release stream `v1.0.12` 2026-03-21 → `v1.4.243` 2026-10-28
  (990 releases, 17,658 unique PRs), and a local clone at **16,985 commits**
  (HEAD `2e40b990b8a`).
- **Method:** GitHub API + release-note parsing; commit-message classification;
  direct reading of the PTY, worktree, env, and persistence modules.
- **Caveat:** most of the 990 releases carry auto-generated PR lists, so
  user-visible severity for March–July is understated. Counts are unique PRs
  matching a theme, so areas overlap — relative weight, not a partition.
  Statements marked *inference* are ours, not Orca's.

---

## 1. The shape of the project

| Metric | Value |
|---|---|
| Commits in 7.5 months | 16,985 (~75/day, ~200/day in October) |
| Merge commits | **6** — squash-merge only; one PR = one commit |
| `fix` commits | **6,796 (40.0%)** |
| `feat` commits | 1,296 (7.6%) |
| `perf` / `test` commits | 1,157 / 905 |
| **fix : feat** | **5.24 : 1**, and worsening (Mar 79/39 → Oct 2,094/430) |
| Releases in 221 days | 990 (375 stable, 574 `-rc.`, 41 mobile) |

Three things stand out.

**Fixing dominated building.** A 5.2:1 fix-to-feature ratio is the clearest
signal in the corpus: this product category is mostly a correctness problem
wearing a feature set. `perf` is the third-largest commit type and `test` is
fourth — both were treated as first-class work, not as something hidden inside
features.

**The RC channel was retired, quietly.** From April to July, 66% of releases
were `-rc.` builds cut from `*-daily.*` commits. The last one ever was
`v1.4.184-rc.0` on 2026-08-16. Since then: stable only, roughly weekly, and
release notes grew from a median of 426 characters to 32,369 — notes became a
product surface instead of a build firehose.

**The scope vocabulary is the architecture.** Fix scopes: `terminal` 519,
`mobile` 426, `native-chat` 356, `ssh` 249, `runtime` 188, `relay` 132,
`daemon` 72, `agent-status` 65. `terminal` is the only scope that is
feature-mature yet still #1 in fixes — terminals are permanently hard.

### Where the effort went

| Area | Unique PRs |
|---|---|
| Mobile / web / remote (relay, SSH, `orca serve`) | 3,913 |
| Diff/review + git forge integrations + editor | 3,804 |
| Agent launch & status (Claude, Codex, OpenCode, Cursor, Copilot, …) | 3,139 |
| Terminal / PTY (tabs, scrollback) | 3,060 |
| Worktrees & workspaces | 2,509 |
| Packaging / updates / telemetry / CI | 2,414 |
| CLI & daemon (`orca`, `orcad`, runtime RPC) | 1,609 |
| Performance / memory | 1,289 |
| Native chat & transcripts | 1,221 |
| Settings / onboarding / skills | 889 |

---

## 2. The eight failure classes that never closed

Ordered by how much of Orca's history they consumed. This is the core lesson.

### 2.1 Restore and state loss — 751 PRs (130 terminal-restore alone)

> "Open files, live agent tabs and paused Claude or Codex resume offers survive
> an SSH reconnect and a restart, and tabs you closed long ago no longer come
> back as empty shells." — v1.4.223, 2026-10-08

> "Local worktrees load before terminals restore, and the sidebar says discovery
> is still running instead of showing an empty list." — v1.4.243, 2026-10-28

Present from `v1.0.31` ("remount terminal panes after shutdown to restore PTY
connections", 2026-03-24) through the latest release. *Inference:* restore has
to reconcile three stores — persisted layout JSON, a scrollback snapshot, and
live PTY/daemon identity — and any of the three can be stale at restart.

The tell is a ritual in the notes: **"New terminals start on a fresh terminal
host so this release's terminal fixes apply"** (v1.4.223, v1.4.241, v1.4.243).
Fixes only reach new terminals; existing ones stay on the old host. That is an
admission the backend cannot be hot-swapped.

### 2.2 Environment and PATH — 405 PRs

> "fix(agents): detect all user-installed CLIs by hydrating PATH from login
> shell" — v1.3.6-rc.0, 2026-04-19

> "an agent now uses Orca's own `orca` command even when shell startup files put
> an older global install first" — v1.4.221, 2026-10-05

The first is "the agent isn't found"; the second is the inverse, "the wrong
binary is found". `ZDOTDIR` was honored, reverted, then redone. This is not an
Orca-only problem: two of the local commits in our clone (`77065429a79`,
`2e40b990b8a`) are exactly this incident — a desktop-launched app grid got
`/usr/bin:/bin`, so `pnpm` (and anything in `~/.local/bin`, `~/.bun/bin`) was
invisible, and the fix was to harden the launcher PATH and write a log file.

### 2.3 Duplicate launches and twin owners — 187 PRs

> "Prevent duplicate agent resume tabs after wake" — v1.4.91-rc.0

> "closed or dragged-out panes no longer come back or appear twice" — v1.4.223

> "ResolveOwner refuses folder and project-group twins instead of guessing this
> computer" — v1.4.230

*Inference:* a pane could be identified by path, by repo, by host, or by a
generated id. Mixing those keys lets two rows point at one PTY, or two PTYs get
spawned for one row. They converged on a single owner resolver in October — and
removing the old one was itself a multi-PR refactor.

### 2.4 Resize, geometry, reflow — 288 PRs

> "fix(terminal): stop scroll drift during resize" — v1.3.21-rc.3

> "Windows terminals keep their history through width changes" — v1.4.243

Several fixes here were reverted in both directions. The deepest pitfall in the
entire repo lives here (see §3.4).

### 2.5 Readiness races — 353 PRs (87 explicitly "race")

> "fix(pi): prefill linked issue URL via env var to bypass paste-readiness race"
> — v1.3.35

The dominant sub-pattern is keystrokes arriving before the agent TUI is ready.
They kept adding detectors (terminal title, prompt marker, screen scrape) and
every vendor CLI update invalidated them.

### 2.6 Desync after reconnect — 508 PRs (202 in the SSH family)

> "revert(ssh): back out #13326 and #13928 — reconnect loses every tab" — v1.4.185

> "Keys you type during a network outage arrive exactly once and in order, and
> scrollback and pane size survive a reconnect" — v1.4.224

> "fix(terminal): never wipe a painted pane for a snapshot that carries no
> image" — v1.4.183-rc.0

*Inference:* they first modeled reconnect as tear down and rebuild. The fix
trajectory is the opposite: keep identity, replay a bounded log, dedupe by
sequence number, never blank a painted pane for an empty snapshot.

### 2.7 Permission and trust prompts — 291 PRs

> "it now answers the agent's 'Do you trust this folder?' question ahead of
> time" — v1.4.219

Same release, known issue: "On Alpine WSL, Orca can't copy the file permissions
it needs, so Claude shows its own trust prompt instead." Pre-answering vendor
prompts means writing into vendor config directories — see §5.

### 2.8 Upstream agent-CLI coupling — the signature risk

> "Orca had been waiting for a welcome-screen label that Codex 0.158 removed, so
> every Codex worker timed out; Orca now recognizes Codex's empty input box
> instead." — v1.4.217, 2026-09-29

> "OpenCode 2.0.14, 2.0.15, 2.0.17 and 2.0.18 leave the first message unsent
> when a model is picked, so Orca refuses model picks on those versions."
> — v1.4.241

Status detection is coupled to each vendor's TUI internals. Every vendor release
is a potential P0 for a product that reports agent state.

### 2.9 Smaller but instructive

- **Silent empty list instead of an error.** "surface worktree catalog failures
  instead of a silent empty list" (v1.4.168) was still being fixed as the
  "discovery is still running" item in v1.4.243.
- **Orphan processes.** "helper processes stop when Orca quits" (v1.4.223) and
  a dedicated "Quit and Stop All Sessions" (v1.4.243).
- **Auth drift.** "a briefly locked file on Windows no longer logs you out or
  opens the session under the wrong account" (v1.4.186).

---

## 3. How Orca actually built the parts we care about

Roughly 54 fix commits were classified by invariant; the mechanisms below are
what survived. This is the most directly reusable material.

### 3.1 PTY ownership: daemon owns the screen, renderer is a view

```
Renderer (xterm.js) ──IPC──► Electron main ──NDJSON / binary-v1──► orcad (owns PTYs)
                                                                      │
                                                    headless xterm + history on disk
```

- The daemon runs **headless xterm** (`@xterm/headless` + serialize addon), not
  just a byte pipe. The client renders a snapshot; the daemon is the source of
  truth for the screen.
- `PROTOCOL_VERSION = 52`, with the changelog kept *as comments above the
  constant*, under the rationale **"daemons survive app updates, so wire
  behavior must be version-gated."** Each line records what the bump absorbs,
  including the awkward cases: *"v48 skips v47, which the 1.4.227 release
  (1.4.224 plus picks) took; its daemon is adopted as previous."* Feature flags
  are per-version constants (`FENCED_TERMINATION_PROTOCOL_VERSION = 51`, …) and
  capabilities are negotiated, never assumed.
- `TerminalHost` serializes creates per session id across async spawn
  validation, uses **cancellable waits** ("let canceled callers leave rather
  than wait behind a cwd probe stuck on an unreachable share"), and reaps the
  emulator on exit — "dead sessions do not retain scrollback for the daemon's
  lifetime."

### 3.2 Identity: `incarnationId` fences every callback

Every PTY gets a UUID at spawn. Exit broadcasts carry it, so a client can
discard an exit belonging to a previous PTY that happened to reuse a session id.
There is a 2 s retired-incarnation window so a late inspection can still answer,
and a tombstone ring answering "was this killed by us?" after the object is
gone.

A representative fix: `d9a3c84b001` — a downgrade duplicated live terminals,
because adoption was keyed on creation time rather than identity. Adoption must
be id-based.

### 3.3 Teardown ordering (the comments are the gold)

Quoted verbatim from `session.ts`:

> "Capture termination intent before flipping the state to exited."

> "Free an exited child's handle without signalling its PID, which may have been
> reused."

> "Preserve operator-close intent even when process exit beats the control
> reply."

Fenced termination (protocol v51) returns `ended | already_exited |
refused{unsupported,unverifiable}` — a *verifiable* kill, so callers can tell
"gone" from "we don't know".

### 3.4 Resize: a model, not a number

- Every session exposes the size **actually applied**, and the renderer compares
  it to what it requested: *"Let the renderer detect dropped or coerced resizes
  from the applied dimensions."*
- The daemon stamps a `resizeModel` (`conpty-viewport-v1`) into the hello and
  each session's metadata, so replay resizes **the way the live screen did**.
- Unix PTYs reflow like xterm (cursor rows stay put); ConPTY pins the top
  viewport line and repaints from the top, so a wider grid pulls rows out of
  scrollback and then overwrites them. The fix re-anchors on a logical line's
  first row, and bails out when a DECSTBM scroll region is set or the parser is
  mid-escape.

### 3.5 Reconnect: snapshot + sequence, never a blank pane

Attach returns `snapshot` (serialized screen including scrollback) plus
`pendingOutput(seq)`, where `seq` closes the gap between "bytes already emitted"
and "bytes in the snapshot". Pending output is capped (2 MiB); overflow forces a
full re-anchor rather than an unbounded buffer. Backpressure has a 5 s
lost-resume failsafe for clients and a 60 s stall watchdog for streams, because
**a dropped resume notification must not wedge a shell**.

Terminal queries (DA1/DSR/XTVERSION) inside *dropped* output are salvaged —
"the hidden program may be waiting for their replies" — and they are never fed
back into the shell as if they were user input.

### 3.6 Environment: probe the login shell, never trust your own env

The app never spawns an agent with its inherited environment:

1. `$SHELL -ilc` with **marker-delimited** output, 5 s timeout, cache of 8
   environments, out-of-order results ignored. A fallback capture is kept *only
   until* a real one replaces it.
2. A **shell-native PATH probe** per shell family: POSIX `printf` markers,
   Nushell's list-valued `$env.PATH`, Git Bash through `cygpath -wp` (MSYS paths
   are meaningless to native children), PowerShell with profiles loaded.
3. rc-file parsing only when spawning a shell is not an option, and only the
   active shell family — "mixing files lets unused config override the real
   startup value".
4. Spawn-time stripping: `ELECTRON_RUN_AS_NODE`, AppImage vars, inherited
   `CLAUDECODE`/agent markers, Orca's own pane-identity vars, plus a git
   credential-prompt guard.

### 3.7 Agent status: two redundant channels, heuristics as data

| channel | mechanism | carries |
|---|---|---|
| managed hooks | per-agent hook POSTs to a **loopback hook server** | state, prompt, tool, session id, model |
| OSC 7501 | the agent writes status into its own PTY output | state, blocked kind, one line |

Hooks win; OSC fills gaps — hooks not installed, agents run by hand, and "hooks
that went silent for 2 minutes on a state the program has since left". A
finished turn is silent by design, so only new work revives it.

There is a third layer: per-agent **JSON rule packs** validated by a schema with
hard caps and safe-regex checks, each rule carrying a `why` field *as data*, plus
`strength: weak` + `requiresQuiet` so a shell auto-title cannot claim "idle" over
a busy turn. **Detection heuristics ship as data, not code.**

### 3.8 Worktrees: safety as a state machine

Create: conflict check against pending removals → pause `git maintenance` (a
concurrent ref pack races the branch write) → `git worktree add --no-track -b
<branch> <path> <qualified-base>` → persist lineage in local git config,
unsetting it if the write fails → bump the scan generation in `finally` so even
a failure invalidates the cache.

Base refs are qualified before use: `refs/remotes/<name>` is probed before
`refs/heads/<name>` — **"Qualify base-picker revisions so tags cannot shadow
branches."**

Remove: assert unlocked → `git worktree remove [--force]` under a concurrency
limit → clean up leftovers Git leaves on Windows → delete the branch serialized
per repo (ref-lock races), with force-delete reserved for rollback of a failed
create so user deletes keep unmerged commits.

Two integrity rules worth stealing:

> "Unreadable counts as present: only a checkout proven gone ends a failed
> delete."

Refusals are **typed by prefix** so both desktop and CLI can classify them and
offer "Force Delete": `Failed to physically stop every PTY for worktree:` /
`Refusing to remove worktree with running agent sessions:` / `Worktree is locked
by Git.` There is an explicit comment that these strings are a contract.

### 3.9 Persistence: generations, coalescing, sync-on-quit

Writes go through a writer worker and an `AtomicSnapshotWriter`: temp file +
rename, monotonic write generations, coalescing (a second write replaces the
pending serializer), skip-unchanged, and a **synchronous `writeSync` that
supersedes an in-flight async write** because quit paths cannot await.

A representative invariant: *"Missing `endedAt` would falsely authorize cold
restore on the next launch."* Absence of evidence must not be read as
permission.

### 3.10 Convention that produced all of the above

`AGENTS.md` is short and encodes judgement, not style trivia:

- One gate command before handoff; a single aggregate `verify` job in CI.
- **"First Class Citizens"**: every change must consider cross-platform, remote,
  mobile, and folder workspaces.
- **"Reuse Before Reimplementing"**, at any scale.
- **"Avoid Comments"** — but precisely: "Only use when code does not describe.
  **BE CONCISE. 1 LINE if possible (why not how)**." The result is ≈5,700
  `Why:` comments across `src/` and `mobile/`, and **19 TODO/FIXME/HACK lines
  under `src/`**. Reasons are recorded; deferred work is not.
- **"Never use vague names like `helpers`, `utils`, `common`, `misc`"** — name
  files after what they contain (`terminal-orphan-cleanup.ts`).
- `docs/bug-reproductions/<slug>/` records *"What was proved"* and *"what was
  NOT proved"* — a habit worth copying verbatim.
- Lint ratchets: `max-lines` with a **shrink-only baseline** whose header says
  "Do NOT add entries to get CI green — split the oversized file instead."

Cautionary: 70 CI workflows, 992 scripts, dozens of baseline files. That is what
seven months of "add a gate for every incident" looks like. Copy the *idea*, not
the volume.

---

## 4. What they reverted, and what that says

About 110 revert PRs. Reverts are the most honest signal in a repo — they mark
where complexity exceeded the team's ability to hold it.

| Reverted theme | Why it matters to us |
|---|---|
| Terminal renderer/perf series (5+ separate reverts, May 2026) | Scrollback/render tuning is a trap; correctness first |
| Windows update-survival chain (#7421→#7499, backed out wholesale) | Don't couple PTY lifetime to app updates |
| SSH reattach identity (reverted twice in one release) | Reconnect identity is genuinely hard |
| Unified tabs / split groups (5 reverts, Apr 2026) | Don't redesign the pane model early |
| Ownership resolver "Phase 3" (26 PRs, removed) | Get identity right first, then never re-key it |
| Mobile push (≥3 reverts) | Push infrastructure is not a side quest |

Also notable: **persisted state became forward-only.** "Orca now refuses to
install versions older than v1.4.214" (v1.4.223). That is a deliberate
schema-migration policy, not an accident — worth copying on purpose.

---

## 5. Security and privacy notes

- **Exposure by port fallback.** "Do not bind a loopback port another runtime
  already serves on all interfaces" (v1.4.229). A port-collision fallback had
  been exposing a service. Binding policy must never degrade silently.
- **Loopback until pairing.** Default `127.0.0.1:6768`; widening happens only on
  explicit pairing, on the same port, and failed widens restore loopback.
- **A disclosed credential-permission bug.**
  > "When Orca marked a folder as trusted for GitHub Copilot, it could leave
  > `~/.copilot/config.json` readable by other users on the same machine. That
  > file can hold your Copilot login tokens. v1.4.219 and v1.4.220 did this on
  > SSH hosts too… The file is now owner-only after Orca writes to it."
  > — v1.4.221, 2026-10-05

  Generalization: any "pre-answer the trust prompt" feature writes into
  `~/.claude`, `~/.codex`, `~/.copilot`. That is a privileged operation and
  must be opt-in, backed up, and `0600`.
- **Capability scoping, default off.** Orca's CLI on SSH hosts "reach only that
  host's own terminals" unless the user turns on "Allow this host's orca CLI to
  control Orca" (v1.4.224). Agents are untrusted callers; scope by default.
- **Telemetry was a gated project**, not a sprinkle: consent, `PRIVACY.md`, and
  a build-time check that twice broke releases. Copy the policy, not the
  pipeline.

---

## 6. Mapping to DarkFactory today

Honest gap analysis against the code in this repo (~1,750 LOC).

| Area | DarkFactory now | Gap vs the lessons |
|---|---|---|
| Identity | Opaque ids on `Repo`/`Worktree`/`Session`; sessions reference `worktreeId` | Worktree *path* is derived from `repo.Name + name`, not ids — a rename or a second repo with the same basename collides |
| Env | `session.Start` inherits `os.Environ()` and appends `TERM` | **Failure class #2 waiting to happen.** A daemon under systemd gets a minimal PATH; no agent CLI resolves |
| Screen ownership | PTY bytes in, raw bytes out; 4 MiB ring in memory | No server-side screen state, so replay after reconnect is bytes, not a terminal |
| Restore | Metadata durable; live PTYs are not. On restart every stored session is relabelled `lost` | Lesson #1 unaddressed. No scrollback replay, no `endedAt` discipline |
| Status | `running` / `exited` / `lost` | No "waiting on you", no adapter. Fine *because* we don't scrape — keep it that way |
| Launch | `POST /sessions` always creates | Not idempotent — a client retry spawns a twin |
| Deletion | `DELETE /worktrees/{id}` runs `git worktree remove --force`; sessions keep running in a deleted cwd | Orphaned PTYs; no cascade, no reaper, no typed refusal |
| Resize | Immediate `Setsize`, no debounce, no applied-size reporting | Acceptable now; must be journaled before reconnect exists |
| Errors | JSON 500s; `lost` status rendered | No silent-empty-list bug yet. Keep the rule explicit |
| Security | Loopback default, 32-byte token at `0600`, constant-time compare, no telemetry | `--listen 0.0.0.0` is accepted silently; no warning, no opt-in flag |

What we already do right and should protect:

- One process owns sessions, with the manager interface kept separable.
- No Node, no Electron, no package manager at build or runtime.
- Vendored dependencies, `CGO_ENABLED=0`, one static binary.
- A CLI (`token`, `version`) from day one — Orca converged on "the CLI is the
  API" only in October (`orca ports --json`, `orca file open --focus`).

---

## 7. Requirements implied by the evidence

Proposed for the requirements pass. Priority is about *when* it hurts, not
effort: P0 = we hit it as soon as anyone uses this daily.

### P0 — correct by construction

**R1. One identity per session, minted once.** A session id is never derived
from path, repo, branch, or host; those are attributes. Worktree directories are
keyed by `repoId` + worktree id, not display name. Every callback carries the
id it belongs to (an `incarnationId` once PTYs can outlive a request).
*Why:* 187 duplicate/twin PRs, plus the downgrade-duplication fix
(`d9a3c84b001`) — adoption must be id-based, never creation-based.

**R2. Environment resolution is a module, not `os.Environ()`.** Probe the login
shell once (`$SHELL -ilc`, marker-delimited), cache with a generation counter,
record which source won, inject into every spawn, and pin the absolute path of
our own binary. Fall back to the daemon environment when probing fails — and log
that it did, to a file, because there may be no TTY.
*Why:* 405 PRs over six months, still leaking — including in this repo's own
launcher.

**R3. Visible failure, never a silent empty state.** Every list endpoint
distinguishes `empty` from `failed`/`loading`, and the UI renders the difference.
*Why:* Orca fixed the same empty-list bug across five months.

**R4. Deletion has a defined blast radius and a typed refusal.** Deleting a
worktree stops its sessions first; deleting a repo stops its worktrees'
sessions. Refusals carry a machine-classifiable prefix so the CLI and UI can
offer "force" instead of parsing prose. A reaper kills PTYs whose cwd is gone.
`DELETE` is idempotent.
*Why:* orphan-process class; our `--force` remove currently leaves live shells
in a deleted directory. Orca treats refusal strings as a contract.

**R5. Binding is explicit.** Default `127.0.0.1:8787`. Any other address
requires `--listen` *and* `--allow-remote`, logs a warning, and never falls back
to a wildcard bind on port conflict.
*Why:* Orca's "loopback port another runtime serves on all interfaces".

### P1 — the things that decide whether it is a daily driver

**R6. Launch is idempotent.** `POST /sessions` accepts a client-generated
request id; a replay returns the existing session instead of spawning a twin.

**R7. Honest status.** `running` / `waiting_on_input` / `unknown` / `exited`.
Status comes only from adapters we own, each version-gated; no adapter means
`unknown`, never a guess. Detection never blocks a launch.
*Why:* 140 agent-status PRs and the Codex-0.158 timeout.

**R8. A detached session host with a versioned wire protocol.** Daemon owns the
PTY *and* the screen; the browser attaches to `snapshot + seq`. Protocol
version and per-feature flags are negotiated and documented at the constant, so
the host can outlive app restarts *and* upgrades.
*Why:* 130 restore PRs and the "fresh terminal host" ritual. Doing this early
is cheaper than retrofitting it once tabs exist.

**R9. Resize is journaled and reconciled.** Debounce client resizes, report the
size actually applied, persist the final size and the resize model, apply both
on reattach.
*Why:* 288 resize PRs, including ConPTY's top-anchored repaint.

**R10. Replay is bounded and sequenced.** Snapshot plus a monotonic sequence;
clients dedupe by sequence; overflow forces a re-anchor; never clear a painted
terminal for an empty snapshot.

**R11. Absence is not permission.** Persist explicit terminal state on exit
(`endedAt`, `exitCode`, `reason`); a session with no terminal record is
`unverified`, not `resumable`.
*Why:* "Missing `endedAt` would falsely authorize cold restore."

### P2 — worth designing now, building later

**R12. Forward-only state with an explicit gate.** Store a schema version;
refuse to open a database written by a newer build, with a readable error.
*Why:* Orca chose this deliberately at v1.4.214.

**R13. Capability scoping for anything that can drive the app.** Any future
agent-callable CLI verb gets an explicit allow-list, default off, per host.

**R14. Release notes as a product surface.** No RC channel. Weekly stable, with
notes a human wrote.

### Conventions to adopt now (cheap, compounding)

1. One `make verify` gate = build + vet + test + `gofmt -l` (empty output).
2. `Why:` comments only, one line; no TODO graveyards.
3. Name files after what they contain — no `utils.go`.
4. `docs/bug-reproductions/<slug>.md` with *what was proved* / *not proved*.
5. A shrink-only file-size baseline if any file exceeds ~600 lines.

### Confirmed non-goals (Orca's own cost data)

| Cut | Evidence |
|---|---|
| Hosted relay fleet, regional rehoming, mobile push | Largest revert cluster; needs server ops |
| Native chat / per-vendor transcript parsing | 372 PRs in 90 days, 5+ reverts; terminal-first is the cheap 80% |
| Monaco-class diff editor, forge integrations | 3,804 PRs; `git diff` in a scrollable view is enough |
| RC channel, multiple builds/day | Retired by Orca after five months |
| Writing trust/consent files into vendor config dirs | The `~/.copilot/config.json` disclosure |
| Telemetry pipeline | Keep "none", or opt-in with a written privacy note |

---

## 8. Open questions for the requirements pass

1. Do we accept "no session survives a server restart" for another milestone, or
   make the detached session host the next milestone? R8 is the acceptance test
   either way — and R1/R10 are its prerequisites, so they are not free to defer.
2. Do we adopt headless screen state (a Go terminal emulator such as
   `vt10x`/`xterm` bindings), or keep the byte ring and accept that reconnect
   replay is not screen-accurate? This is the biggest single fork in the road.
3. Is `waiting_on_input` worth the adapter tax now, or do we ship `unknown`
   until a user asks? Orca's answer cost them 140 PRs.
4. Worktrees under `~/.local/share/darkfactory/worktrees/<repo>/<name>` (current,
   readable) or next to the repo (`../repo.wt/<name>`, discoverable by plain
   `git worktree list`)? Orca moved to the former.
5. Should `--allow-remote` also require a TLS terminator check, or is "behind
   Tailscale/SSH" the only supported remote story?
6. Do we want an empty per-agent adapter interface in-tree now, so status never
   becomes a cross-cutting assumption? (Orca's answer: yes, as data.)
