# DarkFactory — requirements

**Status:** target specification, draft for review · 2026-10-29 · scope note
2026-11-02
**Keywords:** MUST / SHOULD / MAY (RFC 2119). **Priority:** P0 (blocks use),
P1 (daily driver), P2 (defensible), P3 (later). **Milestone:** M0–M4.

IDs are permanent. Add, never renumber, never reuse. If a requirement is
dropped, mark it `WITHDRAWN` with the reason.

**Current release scope.** This document is the target specification, not the
release plan. The release under review is narrower: a lean local workspace
manager (repos, worktrees, detached PTYs, runs with `command`/`exec`/`pty`
steps), the loopback browser, the optional Linux Wails window, and an Android
remote client that switches between saved hosts one at a time. Sections and
rows that describe the overseer, a workflow DAG, the PWA, storage tiering,
strict containment, budgets or an aggregated fleet are historical planning or
deferred; they are not acceptance criteria for this release. [../HANDOFF.md](../HANDOFF.md)
is the authority on current scope and gates, and
[release-checklist.md](release-checklist.md) holds the ordered commands.
Deferred items keep their IDs and are marked in place.

---

## 1. Product

### Future explicit upgrades

These requirements are deferred design targets, not current release claims.
The work packages and evidence are in [upgrade-foundation.md](upgrade-foundation.md).

| ID | Requirement | Acceptance |
|---|---|---|
| UP-1 | Check and apply require explicit operator action; the daemon performs no unsolicited update checks. | Observe network and filesystem effects of check, apply, and normal daemon startup. |
| UP-2 | Verify publisher-authenticated metadata and the selected artifact against an independently trusted key. | Reject tampered, replayed, expired, wrongly signed, and mismatched candidates. |
| UP-3 | Refuse unknown host protocol, state schema, package ownership, or rollback compatibility. | Exercise supported and unsupported old/new combinations and package-managed paths. |
| UP-4 | Stage, replace, and recover without terminating the detached session host. | Inject failures at each phase and verify live PTYs, session identities, attach, and safe recovery in isolated roots. |
| UP-5 | Show installed and running identities and require explicit authorization for restart. | Verify stale-binary, changed-candidate, and no-restart cases. |

### 1.1 What DarkFactory is

A Go-native factory for autonomous coding agents. It owns the workspace agents
run in — repos, worktrees, PTY sessions — the process that turns sessions into
work — runs, plans, steps, checkpoints, gates — and the governance around both:
which principal may act, on what, and what is recorded.

A *dark factory* is manufacturing with the lights off: work continues
unattended, every station is instrumented, and any station can be stopped. That
is the target product, not the current release. The release under review ships
the workspace-manager slice — repos, worktrees, detached PTYs, a browser and
Linux Wails window, and a phone remote client — and leaves the unattended
process and governance layers below where they are not implemented. Not an
agent, not a chat app, not an IDE.

### 1.2 Who it is for

| Persona | Job |
|---|---|
| **Operator** (one machine, many repos) | Run several agents in parallel without losing track, and stop them when needed |
| **Solo developer** | Give an agent a worktree, watch it work, review the diff, keep the repo clean |
| **Team lead** | See what ran unattended, with authority and outcome recorded |
| **Agent author** | Bring any CLI/TUI agent and have it work without an integration |

### 1.3 Jobs to be done

1. "Give this task to an agent in a throwaway checkout." (worktree + launch)
2. "Show me what all my agents are doing right now." (fleet view)
3. "Stop that one, keep the work." (session control + evidence)
4. "Let an agent run unattended, but not without a record." (governance)
5. "Do not let it touch anything I did not hand it." (capability scoping)

### 1.4 Success criteria

Measurable, and each becomes a test:

- **SC-1** A new user can add a repo, start an agent in the checkout or a new
  worktree, and see output in under 2 minutes from a cold start.
- **SC-2** Deleting a worktree never leaves a live PTY behind. (test)
- **SC-3** After a daemon kill and restart, no session is reported `running`
  unless it is actually running. A live session keeps its host PTY; if the host
  is gone it may be restarted under the same ID with a new PTY; an unresumable
  session is `lost`/`unverified`. (test)
- **SC-4** A worker capability cannot invoke any verb outside its five. (test)
- **SC-5** Every refused mutation returns a typed code with a stable value.
  (route-table test)
- **SC-6** Idle footprint: one binary, < 50 MB RSS, < 20 MB on disk excluding
  repos and worktrees.
- **SC-7** No outbound network connection after startup, unless a notification
  webhook is configured. (test with a syscall audit or a namespace with no route)
- **SC-8** A run with a failing required check cannot end `succeeded`, however
  it routed. (test)
- **SC-9** A replan changes no completed step, and the superseded plan version
  remains readable. (test)
- **SC-10** A 10-minute session at 1 MB/s output writes under 100 MB unless
  persistence is explicitly enabled. (measure)
- **SC-11** Revoking a device takes effect on the next request; no device
  session survives a restore without the instance secret. (test)
- **SC-12** A blocked run can be approved from a phone in two taps. (manual +
  UI test)

### 1.5 Positioning

DarkFactory is **not** a subset of Orca, a front-end for Hermes, or a clone of
OpenBot. It sits at the workspace + process + governance layers and rents the
agent layer.

| Layer | Owner |
|---|---|
| Agent loop, memory, skills, channels | the tenant (Hermes, Claude, Codex, …) |
| Workspace: worktrees, PTYs, restore, fleet | DarkFactory |
| Process: runs, plans, steps, checkpoints, gates | DarkFactory |
| Governance: principals, policy, audit, approvals | DarkFactory |

---

## 2. Functional requirements

### 2.1 Workspace and repos (WS)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| WS-1 | P0 | M0 | Add a repo by absolute path; MUST resolve symlinks and classify it as a git work tree or a plain directory | duplicate path → `409 repo_exists`; every repo becomes an in-place workspace at its path (WS-13) |
| WS-2 | P0 | M0 | Repos have an opaque id; the display name is never a key | renaming a repo changes no path and no foreign key |
| WS-3 | P1 | M1 | Remove a repo only when it has no worktrees or sessions; `--cascade` stops and removes them | attempt → `409 repo_busy` naming the offending ids; a workspace and its sessions are removed with the repo, files stay |
| WS-4 | P2 | M2 | List repos with cheap status (dirty, ahead/behind) that never blocks a request | a hung `git status` does not delay `/state` beyond 100 ms |
| WS-5 | P2 | M2 | Optional allow-list root; repo paths outside it are refused | `422 path_outside_root` |
| WS-11 | P1 | M0 | Worktree directory is under the configured root and ends with `<name>-<shortid>`; ids stay the keys | `cd` works; two repos with the same basename never collide |
| WS-12 | P1 | M0 | A non-git directory has exactly one in-place workspace at the repo path; sessions run there with no branch and no isolation; multiple sessions are allowed | non-git dir → workspace row; second workspace → `409 workspace_busy`; removing the repo stops sessions and leaves files |
| WS-13 | P1 | M1 | Every repo has exactly one in-place workspace at its path; for a git repo this is the checkout, so sessions share its files and agent history, with no branch and no isolation; linked worktrees are additional | `POST /repos` → workspace row at the repo path; startup or `POST /worktrees {"workspace":true}` repairs a legacy repo; a second workspace → `409 workspace_busy`; session cwd is the repo path |

### 2.2 Worktrees (WT)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| WT-1 | P0 | M0 | Create from repo + base ref + name; directory derived from ids, not the display name | two repos with the same basename never collide |
| WT-2 | P0 | M0 | Qualify the base ref before use: `refs/remotes/…` before `refs/heads/…` | a tag named like a branch cannot shadow it |
| WT-3 | P1 | M1 | Create is atomic: on failure no branch, no directory, no row | inject a failure at each step; assert clean state |
| WT-4 | P0 | M0 | Remove refuses while sessions are live; `--force` stops them first, then removes | `409 worktree_busy` with the live session ids, unless forced |
| WT-5 | P1 | M1 | Remove refuses a dirty worktree unless forced | `409 worktree_dirty`, listing uncommitted paths |
| WT-6 | P2 | M2 | Removal is resumable: `pending`/`failed` records survive restart; unreadable counts as present | kill between steps; retry completes or refuses honestly |
| WT-7 | P0 | M0 | All worktree paths stay under the configured root; traversal rejected | `../../etc` → `422 path_outside_root` |
| WT-8 | P1 | M1 | Each worktree gets a per-worktree `HOME` with read-only credential projection | agent sees its own config; no other worktree's state |
| WT-9 | P2 | M2 | Deletion is idempotent (`DELETE` twice → 204) | second call performs no side effect, writes no second row |
| WT-10 | P2 | M2 | A stale worktree (directory gone, git metadata present) is reconciled, not silently listed | `status` distinguishes `present` / `missing` / `orphaned` |
| WT-11 | P1 | M0 | Base refs and branch names pass `git check-ref-format`; every git argv is `--`-terminated | `--upload-pack=…` or `..` in a ref → `422 ref_invalid` |

Implemented in this build: WT-1 keeps two repos with the same basename apart
(the directory is `<name>-<short id>` under the repo's own name, not the display
name alone) and branches are named `darkfactory/<name>`. WT-4 and WT-5 are
enforced by `DELETE /worktrees/{id}`: it refuses a worktree with live sessions
or with uncommitted or untracked files (`409`, naming the session ids or paths
it would have destroyed), and it checks *before* it cancels runs, stops sessions
or drops rows, so a refused delete leaves everything intact. `?force=1` is the
explicit override, and the browser asks a second question rather than folding
the force into the first dialog. WT-3, WT-6, WT-8, WT-9, WT-10 and WT-11 are
still open: refs are validated by a local regex and git argv is not
`--`-terminated yet.

### 2.3 Sessions and terminals (SS)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| SS-1 | P0 | M0 | Start a shell or an agent preset in a worktree | session appears in `/state` with pid, worktree, status |
| SS-2 | P0 | M0 | Session ids are minted once and never derived from path/repo/branch | moving a worktree changes no session id |
| SS-3 | P0 | M0 | The detached session host owns the PTY; output is buffered in a bounded ring | ring overflow drops oldest, never grows unbounded |
| SS-4 | P2 | M2 | Attach returns `snapshot + seq`; clients dedupe by sequence; a snapshot never blanks a painted pane | reconnect mid-stream shows no gap and no duplicate bytes |
| SS-5 | P1 | M1 | Resize is debounced and journaled; the applied size is reported back | a resize storm produces one `Setsize`, and the client can detect coercion |
| SS-6 | P1 | M1 | Exit records `endedAt`, `exitCode`, `reason`; if the host is gone at startup, a session that was live is restarted under the same ID with a new PTY and bounded transcript, or marked `lost` with `endedAt` | kill the host and restart the daemon; the same ID is running with a new PTY, or is `lost`; never a silent guess |
| SS-7 | P2 | M1 | Live sessions survive a daemon restart because the detached session host owns the PTY; if the host is gone they are restarted under the same ID or explicitly `lost` | kill -9 the daemon; the same host PID and session PID are still live after restart; `session-host --stop` kills them |
| SS-8 | P0 | M0 | Stop escalates SIGTERM → SIGKILL to the process group, then waits | no orphan children after stop |
| SS-9 | P1 | M1 | Reaper kills PTYs whose cwd is gone or whose worktree was deleted | delete a worktree with a live shell → shell dies within 2 s |
| SS-10 | P2 | M2 | Status is `running / waiting_on_input / unknown / exited`; detection never blocks a launch | an agent that emits no signal shows `unknown`, not a guess |
| SS-11 | P2 | M2 | Input is a bounded POST; no per-byte framing on the stream path | 1 MB body → 413; stream is raw bytes |
| SS-12 | P1 | M1 | Terminal attach/input and OpenCode writes require workspace scope; read-only may read state, runs and transcripts but cannot open `/stream`, type or mutate | a read-only device cannot open `/stream` or prompt OpenCode |
| SS-13 | P1 | M1 | The emulator allows a fixed escape set; OSC 52 (clipboard) reads, OSC 8 (links), title-setting and palette/colour-setting (OSC 4/10/11/12 and the 104/110/111/112 resets) are ignored or inert, while colour queries are still answered; a native Ghostty window forwards an OSC 52 write only so Ghostty can show the content and ask the user to authorize it, never silently; a PTY title is never identity | fixture output cannot silently write the clipboard, spoof a session title, follow a link, decode an image, or repaint the palette |
| SS-14 | P1 | M0 | WITHDRAWN — this build allows multiple sessions per worktree; each has its own PTY. A writer lock moves to the future run/checkpoint layer. | two launches → two sessions, each with a tab |
| SS-15 | P2 | M1 | Concurrent streams per session are capped (default 4); excess subscribers are refused or drop oldest | 10 clients → 4 streams, memory stays bounded |
| SS-16 | P0 | M0 | An attach response is readable before the PTY next writes: the stream opens with a terminal no-op, so the client can read `X-DF-Redraw` and ask for the repaint | attach to an idle full-screen TUI paints on the first frame, on every attach and every tab switch |

Implemented in this build: SS-1 now exposes `pid`, `pgid`, `startTicks` and an
incarnation id; SS-7 keeps live PTYs in a detached `session-host` over a `0600`
Unix socket, so a daemon restart reattaches to the same PID and ring; SS-6
records `endedAt`/`exitCode`/`reason`, restarts sessions when the host is gone,
and marks the ones that cannot restart `lost`; SS-8/9 stop the process group and
reap a session whose cwd disappears; delete returns `stopped`,
`already_exited`, or `unverifiable`. Browser attach replays a bounded shell
tail trimmed to a newline, or suppresses history and asks the PTY for a
SIGWINCH repaint when the tail is a full-screen TUI; a shell that has not run
anything yet prints a prompt and no newline, so when nothing has been evicted
the whole buffer is replayed instead — that case used to attach black and stay
black until the user typed, on every attach and every tab switch. The TUI guess
requires a row/column address, because a colourised prompt redrawn on resize
looks like an escape-heavy newline-free repaint. A suppressed replay used to
send an empty body, and a browser fetch hands the response to the page only
once a body byte arrives: the page never read `X-DF-Redraw`, never asked for
the repaint, and an idle TUI stayed black until it wrote on its own. Every
attach now opens with one SGR reset, a terminal no-op, so the headers are
readable immediately (SS-16). SS-4's `snapshot + seq`
is still open: the in-process snapshot and subscription now join atomically, so
an attach no longer loses or repeats the chunk at the seam, but the wire still
has no sequence number and a reconnect can repeat bytes. The current browser
attach is exclusive: a new attach detaches
the previous stream with a notice, because one PTY has one size; SS-15's
multi-stream cap is target-state. SS-13 is partly implemented: the client drops
output-driven palette and colour sets, but xterm.js still parses the rest of the
escape set, so the fixed allowlist stays open. SS-15 is still open.

### 2.4 Agents (AG)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| AG-1 | P0 | M0 | A preset is `{command, args, env, dir}`; no shell string, no interpolation | a command with `;` is passed literally and fails to exec |
| AG-2 | P1 | M1 | Resolve env from a login-shell probe (`$SHELL -ilc`), cached, with a logged fallback | systemd-launched daemon still finds `~/.local/bin` agents |
| AG-3 | P1 | M1 | Launch is idempotent via a client request id | retry returns the same session, never a twin |
| AG-4 | P2 | M2 | Record the launched binary's version when obtainable; never gate features on it | `status` reports `claude 2.1.0`, no behaviour change |
| AG-5 | P0 | M0 | Built-in presets: `shell`, `claude`, `codex`, `opencode`, `hermes`; user presets allowed | each appears in `/state.agents` |
| AG-6 | P2 | M2 | Status comes only from a protocol we speak (OSC 7501 / hooks) or is `unknown` | no branch reads terminal text to decide state |
| AG-7 | P0 | M0 | **Constraint:** never parse agent TUI output to infer state | code review gate; no regex over the ring |
| AG-8 | P1 | M1 | Capability arrives on an inherited fd (`DARKFACTORY_FD`), env as fallback; the five verbs are optional; no heartbeat, preamble or required protocol | an agent that ignores the capability still completes via exit code |
| AG-9 | P1 | M0 | Automated git calls set `core.hooksPath=/dev/null`; running a repo's hooks is an explicit human action | a repo with a `post-checkout` hook creates a worktree without running it |

Implemented in this build: AG-3 holds when the request carries an
`Idempotency-Key`. The key is stored on the session row (`clientRequestId`) and a
repeat returns the existing session with `200` instead of starting a second PTY;
the browser sends one per launch intent and keeps it until the launch succeeds,
so a retry after a lost response replays rather than duplicates. A caller that
omits the header — curl, a script — can still create a twin, which is why the
header is now the documented way to launch.

### 2.5 Governance (GV)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| GV-1 | P1 | M1 | Principals `human / overseer / worker:<id>` resolved from transport, never from request fields | forging `sessionId`/`from` changes nothing |
| GV-2 | P2 | M2 | Per-session capability bound to `{session, worktree, incarnation}`, stored hashed, revoked on exit and rotated on a new incarnation (never on viewer attach) | stale capability → `401 capability_expired` |
| GV-3 | P2 | M2 | Worker verbs: `status`, `say`, `ask`, `done`, `log` (own session). Nothing else | a worker calling `worktree.create` → `403 capability_scope` |
| GV-4 | P1 | M1 | One gateway for every mutating verb: resolve → decide → **record** → act | forcing the ledger write to fail performs no effect |
| GV-5 | P2 | M2 | Policy is data; deny before allow; four fail-closed properties, each tested | empty policy denies; broken deny denies; broken allow does not permit; malformed policy refuses startup |
| GV-6 | P2 | M2 | Append-only ledger: hash-chained, `actor` + `initiator`, `policy_version`, redaction, audited export | a secret in a redacted field appears as a length, never a value |
| GV-7 | P1 | M1 | Refusals are `{code, rule, target, detail, remediable, hint}`; codes immutable | every route's failure path returns a code (table test) |
| GV-8 | P1 | M1 | Human-only verbs: add/remove repo, create/remove worktree, config, policy, approvals, panic | no capability can reach them, by construction |
| GV-9 | P2 | M2 | Handover state machine `waiting/taken/completed/expired/interrupted`; refuse-not-queue while a human holds control | a Bot action during `taken` → `409 human_has_control` |
| GV-10 | P0 | M0 | Loopback by default; any other bind requires `--allow-remote` and logs a warning; plaintext non-loopback additionally requires `--insecure-remote` | `--listen 0.0.0.0` without the flag → refuses to start |
| GV-11 | P2 | M2 | No agent-usable global credential; the human token is not accepted on the agent listener | worker token against the human API → 401 |
| GV-12 | P3 | M3 | Remote CLI control is default-off per host, opt-in, revocable | absent config = off; enabling writes a ledger row |
| GV-13 | P2 | M2 | Hardline floor no principal can cross: wipe the worktree root, force-push the base branch, kill the daemon | attempted → `403 policy_denied rule=hardline`, even for the overseer |
| GV-14 | P3 | M3 | Strict containment: separate uid or userns per worktree, `$HOME` unreadable, network denied | a strict agent cannot read `~/.config/darkfactory/token` or reach the network |
| GV-15 | P2 | M2 | Delegation depth and fan-out travel in a signed envelope, not in the request | self-reported depth is ignored; depth 2 with max 1 → `403 depth_exceeded` |
| GV-16 | P0 | M0 | `/state` never returns secret values: preset env is exposed as keys; secrets live in a separate `0600` file | a planted secret is absent from every response |
| GV-17 | P1 | M1 | Browser auth is `darkfactory open`: a one-time fragment code becomes an `HttpOnly` `SameSite=Strict` cookie; no token in a URL or `localStorage`; the cookie hash and expiry persist in bbolt, and logout deletes them | reload and daemon restart work without pasting; `document.cookie` is empty; a logged-out cookie is rejected |
| GV-18 | P2 | M2 | Ledger: decision and state change in one transaction; `audit --verify` recomputes the chain; migrations append, never rewrite; the limit is printed | tamper with a row → verify fails; a migration keeps old rows byte-identical |
| GV-19 | P1 | M1 | Containment is per worktree: `off` (labelled; the compatibility default), `jail` (opt-in: projected HOME, daemon state inaccessible, worktree writable, network allowed; optional `jailScrubEnv`, `jailHideCredentials`, `jailEnvAllow`, `jailPathAllow`, `jailHiddenPaths`), `strict` (separate uid, no network; not implemented) | a jailed agent reads its own HOME but not `~/.config/darkfactory/token`; a scrubbed jail does not inherit `DISPLAY`/`SSH_AUTH_SOCK` unless allowed; a masked credential path is unreadable; `off` lets OpenCode reach its shared service |
| GV-20 | P1 | M0 | Decision and effect share one transaction where possible; a crash leaves `unknown`, never an unrecorded action | kill between record and act → the effect is absent or `unknown`, never silent |
| GV-21 | P1 | M1 | Caller scope is stamped by the transport: the master bearer or a loopback-UI cookie may mint/revoke device credentials, while a paired device (even `full`) gets `403 host_admin`. A non-loopback listener refuses a direct public TCP peer before routing unless `allowPublicSources`/`--allow-public`; loopback/private/tailnet/on-link are accepted and forwarding headers never change the class | remote full cookie `POST /pair/code` → `403 host_admin`; public peer on `0.0.0.0` → `403 source_denied`; tailnet/LAN and loopback cookie still work; opt-in admits public |

Implemented in this build: a principal resolved from the transport for the human
surface (bearer token or paired device), never from a body field (GV-1); one
gateway for every authenticated mutation with the fixed order resolve → decide →
record → act, where a failing recorder leaves the effect unperformed (GV-4); the
refusal envelope `{code, rule, target, detail, remediable, hint}` with one
immutable code registry and a golden test (GV-7); the human-only gate that
refuses a worker or overseer principal on a mutating verb (GV-8); the
transport-stamped caller scope that keeps pairing/revocation on the host
credential and refuses direct public peers on a non-loopback listener
(GV-21); and an append-only, hash-chained ledger written by that gateway, with `GET
/api/v1/audit` and `darkfactory audit [--verify]` (GV-18: the limit is printed,
a prune appends a marker instead of rewriting a row, and verification resumes
from it; the chain half of GV-6, which still owes `actor`/`initiator`,
redaction and audited export, but now records the policy hash per row and prunes
on `ledgerRetentionDays`); and
policy as data (`internal/policy`, `$XDG_CONFIG_HOME/darkfactory/policy.json`),
evaluated deny before allow with no match meaning deny, where an empty policy
permits nothing, an uncompilable deny denies, an uncompilable allow does not
permit and a malformed file refuses startup (GV-5). The human-only gate runs
before policy, so no file can hand a worker a human verb. GV-18's
one-transaction requirement and GV-20 are met only as far as "decision durable
before the effect, outcome after it, crash leaves `unknown`"; the effect and the
decision are still two transactions. GV-13's hardline floor is not written
because no route performs a hardline act yet. Capabilities, delegation,
handover, budgets, ledger export and redaction, and the rest of
GV-2/3/6/9/11/13/15 are not implemented.

### 2.6 Overseer (OV)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| OV-1 | P3 | M3 | The overseer is a role, not a privilege: it emits proposals and holds no capability | the overseer calling a mutating verb → `403 not_a_principal` |
| OV-2 | P3 | M3 | Proposal is typed `{kind, target, args, rationale}`; the policy engine decides | a proposal containing a shell string is rejected at parse |
| OV-3 | P3 | M3 | No self-approval, no role self-grant, no overseer spawning an overseer | a proposal to grant itself `overseer` → `403` |
| OV-4 | P3 | M3 | Observes a typed event feed; no transcripts or file contents by default | feed contains ids, states, counts — not bytes |
| OV-5 | P3 | M3 | Budgets: workers, wall-clock, worktrees, spend; breaker flips to human-only | exceeding a budget stops dispatch and raises a refusal |
| OV-6 | P4 | M4 | Approval history may propose allow-list changes; never auto-applied; destructive classes never proposed | `rm -rf` approved 100× still yields no proposal |
| OV-7 | P3 | M3 | Approval cards show the exact effect: target, command, worktree, duration, `git diff --stat` and the checkpoint SHA | screenshot test with a fixed fixture |

### 2.7 Interface (UI)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| UI-1 | P0 | M0 | Browser UI served from the binary; frontend build tools are permitted during development, with generated assets supplied to Go embedding | the static daemon serves a working UI without Node or Bun installed at runtime |
| UI-2 | P1 | M1 | Vendored terminal emulator, pinned version, license + checksum recorded | `third_party/` lists version and sha256 |
| UI-3 | P1 | M1 | Fleet view: repos → worktrees/workspaces → sessions, with status and last activity | 20 sessions render under 100 ms |
| UI-4 | P2 | M2 | Refusals render as actions from `{code, hint}`; no string parsing | a new code adds a button without touching the renderer |
| UI-5 | P2 | M2 | Responsive layout usable from a phone over Tailscale | 390 px viewport: create session, read output, stop |
| UI-6 | P3 | M3 | Approval and handover surfaces share one component | same state machine drives both |
| UI-7 | P1 | M0 | The browser normally uses the `open` cookie; the paste fallback keeps the bearer token in `sessionStorage` for that tab only; Disconnect logs out and clears it; no token in a URL or `localStorage` | reload works without pasting; a new tab asks again; `localStorage` is empty |
| UI-8 | P1 | M0 | Add-repo dialog can browse server directories through an authenticated, read-only listing; only directories are returned, git repos are marked, and non-git folders open as in-place workspaces; the path field stays editable | 390 px: browse home, open a repo folder, add it without typing the path |
| UI-9 | P2 | M0 | On a local desktop session, Browse opens the OS folder picker through the daemon and returns the absolute path; remote or headless sessions fall back to the server-side directory browser | 127.0.0.1: clicking Browse opens the desktop picker; over Tailscale it opens the in-page browser |
| UI-10 | P1 | M0 | The session view shows a tab for every session in the current worktree/workspace; any tab, including a `lost` or `exited` one, can be stopped or removed | two sessions → two tabs; remove a lost row without restarting the daemon |
| UI-11 | P1 | M0 | The fleet tree expands a worktree/workspace to list its sessions; a session row opens that PTY, and its `×` or right-click menu stops/removes it | expand a workspace → sessions inline; right-click a lost row → Remove |
| UI-12 | P1 | M1 | The Runs view creates a run from a goal and plan steps, lists real runs, shows each step's state/output, and lets a human answer a waiting `pty` step | create a command run → see `succeeded` and output; create a `pty` run → answer it from the timeline |
| UI-13 | P1 | M1 | The fleet is repo → workspace → sessions; a workspace/worktree row starts a session there with only agent/title, and the top-level New session defaults to the checkout when no location is selected; existing worktrees and a new isolated worktree are explicit alternatives, and the dialog states which paths share history | from a workspace row, start a session without seeing the repo/location picker; from no worktrees, a top-level session uses the checkout; only the explicit new-worktree choice creates a worktree |
| UI-14 | P1 | M1 | The session view uses the remaining viewport height, offers a focus mode that hides fleet chrome, and has terminal font-size controls persisted as a local preference; it refits on `visualViewport`/orientation changes, and the connection pill reconnects after a daemon or network outage | a 1400 px-tall window gets a terminal taller than 620 px; an iPad with the software keyboard open keeps the terminal cursor and prompt above the keyboard; font size survives reload; an offline pill reconnects and re-enables Stop |
| UI-15 | P1 | M1 | An `exited` or `lost` session offers Restart in place: it starts a new PTY with the same workspace, agent and title, removes the tombstone, and says that the old output was not persisted | a session that exited on its own or failed to restore shows Restart; it starts a new PTY without opening the New session dialog; only one tab remains |
| UI-16 | P1 | M1 | The OpenCode panel is a view of one worktree, entered from that worktree or one of its sessions and never from the fleet: it lists and searches only that worktree's OpenCode sessions, renders their messages with native scrolling, load-older pagination and a jump-to-latest control, shows pending permission requests with allow-once/always/reject actions, sends prompts, interrupts a running turn, and starts a chat in that worktree; the daemon proxies the local OpenCode API with a server-side credential, using its volatile event stream as a hint with a slow fallback poll | open a worktree, tap Chat: the panel lists that directory's chats only; select one, scroll and jump to latest, load older messages, search, send a prompt, answer a permission request, interrupt a turn, start a chat; switching worktree switches the chat scope; there is no fleet-level chat destination; the browser never receives the OpenCode password; unauthenticated requests → `401` |
| UI-17 | P1 | M1 | The pair dialog lets the host name the device and choose `full` or `read`; the Devices dialog lists paired browsers and device keys with scope, last seen and expiry, and revokes one or all others; a `read` device gets a visible read-only state and mutations are refused | pair a read-only phone → it can view state but cannot prompt, stop, or revoke; revoke it from the desktop → its next request is `401`; revoke a device key → its next challenge is `401` and the client returns to Connect; `POST /devices/revoke-all {keepCurrent:true}` keeps the caller |
| UI-18 | P1 | M1 | On touch devices the shell is pinned to `visualViewport` height and offset, form controls are at least 16px, a vertical terminal pan becomes a synthetic wheel event on the xterm screen layer, Chromium/Blink and non-Linux WebKit use the WebGL2 renderer with a DOM fallback, WebKitGTK keeps the DOM renderer, and a fractional devicePixelRatio that clips glyphs also keeps the DOM renderer. Entering the session view and restoring it at boot force a measured fit/repaint, the terminal bar's ↻ control does the same and requests one SIGWINCH, and WebGL is attached only after the first real fit | iPad keyboard open/close lifts the terminal instead of covering it; a vertical swipe scrolls shell scrollback or reaches a TUI's wheel/arrow path; a tap does not scroll; WebGL loss does not blank the terminal; `?debug=1` names the GL renderer or `dom`; a fractional DPR keeps the DOM renderer; WebKitGTK stays on `dom`; returning from Fleet or another view paints the full grid without a manual reload; ↻ repaints a stale TUI without resizing the PTY |
| UI-19 | P1 | M0 | The mobile OpenCode chat switcher is a native picker whose options survive background polls: the list is rebuilt only when the chat set or a title changes, never while the picker is open, and choosing a chat closes it | select a chat from the phone picker: the panel switches and the popup closes; a chat that updates while the picker is open does not orphan it |
| UI-20 | P1 | M0 | The OpenCode panel has an opt-in chat layout that hides the fleet chrome and gives the chat the viewport, chosen per tab by a visible toggle or `?chat=1`; it is a layout mode over the worktree-scoped panel, never the default, and the terminal keeps its own focus mode | `?chat=1` or the Chat button fills the viewport with the chat and hides the fleet tree, topbar and mobile nav; `?chat=1` on a tab with no chat open scopes to the newest worktree; Exit or `?chat=0` restores the panel; the terminal's Focus mode and the panel layout are unchanged; a reload returns the tab to the layout it last chose |
| UI-21 | P1 | M1 | The OpenCode composer completes the commands OpenCode has registered and sends a chosen one through the command route rather than the prompt route; the command list is fetched for the chat's own directory, and the daemon accepts that location only when it is one of its worktrees | type `/` → a filtered list of commands; ↑/↓ and Enter or Tab insert one without sending; sending `/init` runs the command and produces no prompt; a name OpenCode has not registered is sent as plain text; a device with `read` scope cannot run one |
| UI-22 | P1 | M1 | Assistant and user messages render a markdown subset as DOM nodes: fenced code, headings, rules, quotes, flat lists, simple tables, inline code, emphasis and http/https/mailto links; raw HTML in a message is never parsed and an unsupported link scheme stays literal text | a message with a heading, list, table, fence and link renders each as the matching element; `<img onerror=…>` and `[x](javascript:…)` appear as text; no element or attribute from the message reaches the DOM |
| UI-23 | P2 | M1 | A worktree/workspace row can be reordered inside its repo by dragging its grip; the order is server state (`Worktree.Position`) written by an audited mutation, so it survives a reload and a second browser, the grip works on touch without stealing the sidebar's scroll, and the arrow keys move the focused grip one place | drag row 3 above row 1 → the fleet and `GET /state` both show it first after a reload; a touch starting on the row still scrolls the tree; `↑`/`↓` on the grip moves it and keeps focus; a new worktree lands last, not at the top; the ledger has one `worktree.order` pair naming the repo |
| UI-24 | P0 | M1 | A phone can open a workspace, create an OpenCode chat or PTY session through visible controls, interact, switch sessions, and return from background without losing the selected session | device evidence records the taps, resulting IDs, message round trip, keyboard behavior, and reconnect; API success or a painted pane alone does not pass; a browser reattach after a daemon restart restores the input modes the host tracked, even when the session ring no longer contains the enable |
| UI-25 | P1 | M1 | An optional desktop client loads the daemon UI and uses its existing authenticated API; it owns no PTY or state store and exposes no native authentication bypass. A first token login may provision a non-extractable device key; later launches sign a challenge and reconnect without pasting, and revoking that key returns to Connect. | closing and reopening the client leaves the daemon, host PID, and session IDs intact; a fresh launch after first login reaches connected with no token; native navigation and WebView cookie tests are recorded honestly |
| UI-26 | P1 | M1 | Desktop shell selection uses the same terminal workload and production UI across clients, with engine and renderer versions, process-tree memory, errors, startup, and interaction evidence | repeated interleaved browser/Wails trials preserve raw results; synthetic parser/echo timings alone cannot select a shell |
| UI-27 | P1 | M0 | The served client identifies its build and is gated by an executable layout contract: `UI_VERSION` and every `?v=` query agree, the daemon returns `build` (version + commit) in `/state`, the footer prints both, a stale served `app.js` fails, and desktop (1280×900) and phone (396×719) runs assert the shared layout and touch-target invariants | `make check` fails on a drifted version/id or a remote asset; `make ui-gate` fails against an older installed binary and passes against the working tree; `?debug=1` and the footer name the same `UI_VERSION` |
| UI-28 | P0 | M2 | The daemon serves its browser UI only to a loopback peer with a loopback Host and no forwarding headers; a remote browser receives `403`, while remote `/api/v1/...` remains available for a native client. The native Android client bundles the UI and owns its token. | remote `/` and `/app.js` → `403`; local `/` → `200`; remote `/healthz` and authenticated API still work; the Android source does not load the daemon UI or expose a JavaScript bridge |
| UI-29 | P1 | M1 | On a local Linux desktop, a workspace-scope user can open a live session in the tested Ghostty 1.3.1 as a separate window. The audited launch passes the caller credential over an inherited anonymous descriptor, attaches using the existing stream, forwards input and one terminal size, and closes without stopping the hosted PTY. Clipboard reads, links, title, palette, images and window operations remain restricted; an OSC 52 write is forwarded only to Ghostty's "Authorize Clipboard Access" dialog, and user-initiated copy/paste remains available. | Isolated launch and real-PTY input, resize, detach and OpenCode tests pass; read scope and forwarded requests are refused; hostile terminal fixture is contained; the right-click Copy/Paste menu was observed in a real window and the per-copy clipboard authorization design was accepted by the owner; real-agent streaming and the native Wails window are not yet verified |
| UI-30 | P2 | M1 | The Wails window hosts the terminal in-window in a native VTE GTK4 pane fed by the existing attach client over the stream, input, resize and redraw routes; the shell holds no master token and receives only a single-use, session-scoped pane capability from the page over the WebKit message bridge; the page skips the browser stream while the native pane owns the size and falls back if it fails. The VTE grid derives from its allocated pixels instead of the browser's xterm cols/rows, and the attach client registers SIGWINCH before its first resize and re-reads the PTY size after the handler is live. | Spike, real-PTY and capability gates in [native-terminal-in-app.md](native-terminal-in-app.md) pass; a resized window refills the pane with no clipped row or dead strip, and a resize that lands during attach still reaches the daemon; hostile native-VTE, OSC 8/image and latency gates remain |
| UI-31 | P1 | M1 | A session can be pinned to the top of its workspace from the sidebar's right-click menu; the pin is server state (`Session.Pinned`) written by an audited mutation, sorts before the dragged order, survives a reload and a second client, and a pinned row can only be dragged among pinned rows | pin the third session → it is first in the sidebar and `GET /state`, the row carries a pin marker, the ledger has one `session.pin` decision/outcome pair, and a reload shows it pinned; unpinning restores the previous order; a `read` scope cannot pin |
| UI-32 | P1 | M1 | A session row can be reordered inside its workspace by dragging its grip or with `↑`/`↓` on the focused grip; the order is server state (`Session.Position`) written by an audited mutation, a session the client does not name keeps its place behind the named ones, and a row from another workspace is refused | drag the last session above the first → the sidebar and `GET /state` agree after a reload; the ledger has one `session.order` pair naming the workspace; a foreign session id is refused with `session_order_invalid`; a newly created session lands last |
| UI-33 | P1 | M1 | The sidebar's session and workspace menus copy the workspace path and reveal the folder in the daemon machine's file manager; reveal takes a worktree id, never a path, refuses a caller that is not on the daemon's machine, and is an audited mutation | Copy path puts the worktree path on the clipboard; reveal opens the folder locally and records `worktree.reveal`; a remote client gets `reveal_remote`, a missing directory `reveal_unavailable`, and no route accepts an arbitrary path |
| UI-34 | P1 | M1 | The attention bell opens a list of activity instead of jumping to the newest event: the bell is always clickable and explains an empty list, each row names the PTY session or OpenCode chat it came from plus its workspace above a two-line body, an OpenCode permission or question carries its chat and workspace ids, a page loads from the retained ring so activity from before the page appeared is listed, following a row opens the target — a PTY row its session, an OpenCode row its workspace's terminal session and never the chat panel — and only an explicit dismiss or Clear all removes an event — both clear the daemon's ring too, as audited mutations, so a reload does not resurrect it | an exited session raises a row labelled with its title and workspace; a question raised before the page loaded still appears after a reload; an OpenCode question arrives as `opencode.question` with `openCodeSessionId` and a `worktreeId` and its row shows the chat and workspace above the answer text; clicking a PTY row opens its session and clicking an OpenCode row opens its workspace's session (the chat panel stays closed) while the bell stays raised; dismissing every row hides the count; a reload after a dismiss or Clear all does not bring the row back; the bell stays enabled when nothing is unread; `make ui-gate` and `test/browser/probe.sh activity` assert the sequence |
| UI-35 | P1 | M1 | The activity list records a finished OpenCode turn, not only prompts: when a chat's idle timestamp moves forward the daemon raises `opencode.replied` with the chat, its workspace and an excerpt of the answer, and the row keeps that source line above the clamped body; a chat that first appears after the watcher started reports its finished turn, while the priming poll stays silent so a daemon start does not replay every old chat; the same finished turn reports once | a normal "good morning" exchange produces a row within one poll with the answer text and its workspace; re-polling an unchanged chat adds nothing; a turn that fails says so in the body; `test/browser/probe.sh activity` asserts the row and its source and `TestNotificationReplyRaisesActivity` covers the dedup and priming cases |
| UI-36 | P1 | M1 | Alert noise is configurable without losing activity: an always-visible **Alerts** control in the topbar (icon-only on a phone) and the bell panel's Settings button edit `finishedSessions`, `replies`, `blockedRuns`, `prompts` and `childSessions` through `GET`/`POST /api/v1/notifications/settings` (persisted in the `settings` bucket); every row stays in the ring but only `alert:true` reaches `notify-send` or the Android service, replies are silent by default, and child/subagent chats are not polled unless enabled | open Alerts, turn replies on and finished sessions off: a reply notifies while a finished PTY only adds a ring row; the settings survive a daemon restart; a subagent permission raises nothing until child chats are enabled; the Android service posts only `alert:true` events, with blocks and prompts on the vibrating channel |
| UI-37 | P1 | M1 | The OpenCode panel supports both upstream `opencode` and the Rust rewrite `ocd`: the daemon discovers the newest reachable loopback registration from `$XDG_STATE_HOME/opencode/service*.json` (and the legacy local-install file), preferring `service.json`, and `/state` reports OpenCode available only when that service answers; an explicit non-default `opencodeAPIURL` remains the owner's override, and an `ocd` agent preset exists | run only `ocd` → the panel lists its chats and `/state` has `opencode:true`; stop it → `/state` reports false instead of trusting the stale file; a registration whose URL is not loopback is ignored; a configured test URL is never replaced by discovery |


UI-2 is implemented in this build by the vendored xterm.js 6.0.0,
addon-fit 0.11.0, and addon-webgl 0.19.0; see [`third_party/README.md`](../third_party/README.md). The
fixed escape allowlist in SS-13 is still open: xterm.js parses the full set.
The client does drop output-driven palette and colour sets before they reach it
(`web/app.js`, `filterPaletteOsc`), because repainting the theme is the one
surviving hostile-output effect: foreground equal to background hides text, and
a spoofed palette impersonates another agent. Colour queries are still answered,
and `make ui-gate` runs `test/browser/probe.sh hostile`, which asserts no
clipboard call, no title change, no navigation, no decoded image, no external
request and no palette repaint, and that the allowlisted replies still arrive.

The native Ghostty window is a separate case because users copy from its TUIs.
`internal/nativeattach/filter.go` forwards a bounded, base64-validated OSC 52
write and Ghostty runs with `clipboard-write=ask`, so every write shows the
content in Ghostty's "Authorize Clipboard Access" dialog. A hostile program can
therefore request a clipboard write but cannot replace it silently; reads and
PRIMARY/cut-buffer targets stay inert. `clipboard-read=deny`,
`mouse-shift-capture=never` (Shift always selects terminal text) and the
context menu are part of the same launch. The browser client remains fully
inert for OSC 52, and `test/native-terminal/interactive.sh` with
`DFB_TEST_HOSTILE=1` asserts the native filter still drops reads, titles, links
and images while forwarding the write to the dialog.

UI-27 is implemented by the two halves in [ui-gate.md](ui-gate.md). The static
half runs in `make check` and fails on a `UI_VERSION` / `?v=` drift, a missing
element id, or a remote asset. The browser half (`make ui-gate`) drives the real
page at 1280×900 and 396×719, asserts the shared layout and touch-target
contract, and hashes the served `app.js` against `web/app.js`. `/state` now
carries `build` (version + commit), and the footer prints it, so a stale daemon
is visible instead of looking like a UI regression.

A browser attach now restores the input modes a TUI enabled once (`?1`,
`?1000/1002/1003`, `?1005/1006/1015`, `?1004`, `?2004`) after the replay, because
the client resets xterm on every attach and a repaint does not repeat them. The
host owns the PTY and appends the modes it still tracks from that output to the
snapshot, so a daemon restart no longer loses an enable the bounded ring has
already evicted. UI-24's PTY half and the native app's OpenCode chat round trip
(create, prompt/reply, rotate including the landscape keyboard and Send,
background/foreground, switch sessions, daemon restart without re-pairing) are
device-verified; permission requests and interrupts were not exercised in the
native app. Evidence and limits:
[`client-validation.md`](client-validation.md) and the
native evidence.

UI-5/UI-14 mobile scrolling is implemented without rebuilding unchanged DOM on
every 3 s poll: a state poll that returns the same state does not call
`render()`, and a real update preserves the fleet tree, active view, runs card
and session-tab scroll positions. Scroll containers use momentum scrolling and
`overscroll-behavior: contain`, so a list gesture does not chain to the body.
xterm.js 6.0.0 has no native touch scrolling, so a vertical swipe is translated
to a synthetic wheel event dispatched directly on the xterm screen layer;
a shell scrolls its scrollback, and a full-screen TUI receives the wheel/arrow
equivalent it already understands. The ↑/↓ buttons send PageUp/PageDown, and
the ⇣ button sends End for the app's "jump to latest" action. The shell is
pinned to `visualViewport` height and offset, so the iPad keyboard lifts the
terminal instead of covering it. Terminal input is coalesced per frame with at
most one request in flight, and output is coalesced per animation frame. Only
one browser terminal may own a PTY at a time, so two viewport sizes cannot fight
over the same size.

An application with its own session model is not embedded in xterm.js. OpenCode
is a chat application, not a shell; the browser terminal is for plain shells and
other PTYs. The daemon exposes a native OpenCode panel backed by the local
OpenCode HTTP API: it lists sessions, renders messages with normal DOM scrolling
and a jump-to-latest control, sends prompts, and starts a chat in a DarkFactory
worktree. The panel is scoped to one worktree and opened from it (or from one
of its sessions), never from the fleet; the list shows only that directory's
chats. The daemon reads the OpenCode service password from
`opencodePasswordFile`, sends it as Basic auth on loopback, and never exposes it
to the browser or the state API. `opencodeURL` remains available in `/state` for
an external PWA or another client; it is not required for the panel. Do not
emulate an app's "jump to latest" with synthetic keys, and do not run the
OpenCode TUI in xterm.

The panel also has an opt-in chat layout (UI-20): the same DOM and the same
routes with the fleet chrome hidden, chosen per tab. It is a layout mode, not a
second chat client — the terminal keeps its own focus mode, and neither mode is
the default.

Messages render a markdown subset (UI-22) by building DOM nodes, never by
parsing HTML from the message: fenced code, headings, rules, quotes, flat lists,
simple tables, inline code, emphasis and http/https/mailto links. Raw HTML and
any other scheme stay literal text.

The composer completes OpenCode's registered commands (UI-21) and sends a
chosen one to `/api/session/:id/command`, not to the prompt route: a command is
an action, and a message that merely starts with `/` is still a message unless
OpenCode registered that name. Commands load per directory, and the daemon
forwards a `location` only when it names one of its own worktrees.

### 2.8 Operations (OP)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| OP-1 | P0 | M0 | One static binary: `serve`, `stop`, `token`, `version`, `open`, `status`, `audit`, `backup` | `ldd` shows static; no runtime deps; a second `serve` names the running pid instead of failing with bbolt's `timeout` |
| OP-2 | P0 | M0 | XDG config/state/data paths; every effective setting inspectable | `status --json` prints resolved paths and listen addr |
| OP-3 | P2 | M2 | systemd user unit shipped and documented | `systemctl --user start darkfactory` works |
| OP-4 | P2 | M2 | Backup/restore of state documented and tested | restore into a clean host resumes the fleet view |
| OP-5 | P2 | M2 | Forward-only schema: a newer database is refused, not half-read | open a v+1 db → `422 schema_newer`, no writes |
| OP-6 | P1 | M1 | Structured logs, redacted, to file/journald; secrets never logged | grep for a planted token in logs → none |
| OP-7 | P1 | M1 | `status` prints the containment mode per session | "same-uid (not a sandbox)" or `jail` is visible, not implied |
| OP-8 | P2 | M2 | Graceful shutdown: stop accepting, drain, stop PTYs, flush state | SIGTERM leaves no orphan and a clean db |
| OP-9 | P1 | M1 | `darkfactory open` prints a one-time URL and opens the browser; `backup` uses a bbolt read transaction; `audit --verify` checks the chain | a fresh install reaches the fleet view with one command |

Shipped in this build: `make dist` produces `linux/amd64` and `linux/arm64`
tarballs and `.deb` packages with `SHA256SUMS`; the `.deb` installs
the binary, the systemd **user** unit (OP-3), the app-menu entry and the icon;
`install.sh` installs under `$HOME`, verifies the checksum and can add the unit
and entry (`--desktop`); `darkfactory launch --desktop` is the desktop entry's
`Exec`, starts the daemon when nothing is listening, and opens the native
window when `darkfactory-desktop` is installed (else the browser). `.github/workflows/ci.yml` runs
`make check`, the cross-compile matrix and a second `make dist` to compare the
bytes; `release.yml` publishes from a tag. Those bytes are identical across
runs and across umasks: the Makefile pins tar member modes and normalizes the
`.deb` staging tree after package C found that the umask had leaked into the
artifacts. `backup` (OP-4), the schema gate
(OP-5), macOS artifacts and any artifact signature are not implemented. Windows
is out of scope: `internal/session` needs a POSIX PTY.

### 2.9 Protocols (EX)

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| EX-1 | P2 | M2 | OSC 7501 status parser (optional per session) | an agent that emits it shows `waiting_on_input` |
| EX-2 | P3 | M3 | MCP client for tools, scoped per session, with an allow-list | an ungranted server is never contacted |
| EX-3 | P4 | M4 | ACP client for structured agent control | an ACP agent can be driven without a PTY |

### 2.10 Runs and plans (RN)

A run is the unit of work. Without it, "unattended" just means "a terminal is
still open" and the overseer has nothing structured to supervise. The model is
deliberately smaller than a workflow engine: a flat ordered list of steps, an
immutable plan version, and a checkpoint after each step.

**Vocabulary.** Orca calls its coordination namespace a `Run` and a unit of work
a `Task`; Fabro calls the unit of work a `Run`. We take the second: a Run is the
work, a Step is Orca's Task, and an attempt is Orca's Dispatch. One word per
meaning, per layer.

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| RN-1 | P1 | M1 | A **Run** is `{id, worktreeId, goal, planHash, state, outcome, startedAt, endedAt}`; a terminal run is immutable | mutating a terminal run → `409 run_terminal` |
| RN-2 | P1 | M1 | A **plan version** is immutable and content-addressed, stored in daemon state — never in the agent-writable worktree. A run records the version that governed **each step**; replanning appends a version and moves the pointer | editing the worktree cannot change the plan; the same source yields the same hash |
| RN-3 | P1 | M1 | A **Step** is `command` (argv, no shell), `exec` (one-shot headless agent; exit code is the outcome), or `pty` (interactive; completed by a human or a protocol). **There is no `api` step.** | an unknown kind is refused at parse |
| RN-4 | P1 | M1 | Steps are append-only with an ordinal (`step@visit`); a resume creates a new instance and links `resumedFrom`, never rewriting a completed step | resume of a finished step leaves the original record byte-identical |
| RN-5 | P2 | M2 | Each completed step checkpoints the worktree on `darkfactory/run/<id>` with trailers `DarkFactory-Run` / `DarkFactory-Step` | `git log` shows one commit per step; the SHA is recorded on the step |
| RN-6 | P2 | M2 | A human gate resolves to a **named transition** recorded in the ledger, not a boolean | an answer outside the declared choices → `422 choice_invalid` |
| RN-7 | P2 | M2 | Goal gates: a run cannot be `succeeded` if any required check did not pass, however it routed | bypass the gate → run still ends `failed` |
| RN-8 | P2 | M2 | Repeated identical failures abort at a budget (failure signature + attempt/replan counts) | an infinite loop stops with a typed refusal |
| RN-9 | P1 | M1 | Step states are `pending / running / waiting / blocked / succeeded / failed / skipped`; outcomes are `succeeded / failed / partially_succeeded / skipped`; `blocked` is a state, not an outcome; `unknown` is never an outcome | a blocked run stays alive, visible, and answerable |
| RN-10 | P2 | M2 | Resume refuses when the worktree or checkpoint SHA is gone; it never pretends to resume | missing workspace → `409 resume_unavailable` |
| RN-11 | P2 | M2 | A **replan** is a proposal `{baseVersion, newPlan, reason}`; policy decides auto vs human gate by diff class; the diff is recorded and `maxReplans` bounds it | changing a target or scope requires approval; adding a retry path does not |
| RN-12 | P2 | M2 | A plan changes only at a **step boundary**. Mid-step there is no replan — only steer (if the protocol allows), wait, or cancel | a replan request during `running` → `409 step_in_flight` |
| RN-13 | P1 | M1 | Checkpoints are side refs (`refs/darkfactory/run/<id>/step/<n>`) written with `commit-tree`/`update-ref`; HEAD, branch and index never move | `git rev-parse --abbrev-ref HEAD` is unchanged after a step |
| RN-14 | P2 | M2 | Plan hashes are canonical (sorted keys, no insignificant whitespace); a replan carries `expectedPlanHash` and a mismatch is `409 plan_changed` | two concurrent replans → one applies, one refuses |

Implemented in this build: RN-1, RN-2, RN-3 (`command`/`exec`/`pty`), RN-4 for
the first visit, and RN-9's step/run states. A `pty` step waits for a human
answer from the run timeline. Checkpoints (RN-5/13), gates (RN-6/7), failure
budgets (RN-8), resume (RN-10), replan (RN-11/12/14) are still open.

Plan content is written at the level of **goals and acceptance criteria**, not
keystrokes. A plan that scripts every action turns every surprise into a
replan; the step owns the method, the plan owns the destination.

### 2.11 Storage and wear (ST)

Agent work is write-heavy: package installs, build trees, artifacts, checkpoints
and logs. The point is not to move writes onto slow media; it is to write less,
then tier what remains by write pattern. Terminal output is not the problem —
`node_modules` and `target/` are.

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| ST-1 | P1 | M2 | Three storage classes: `hot` (required), `cold` (optional), `ephemeral` (optional), each with a configurable path | `status --json` prints each class, its path and free space |
| ST-2 | P1 | M2 | The state DB, worktree files and `.git`, plan versions and ledger events are **hot-only**; cold or ephemeral is refused | pointing `state.db` at cold → startup error `storage_class_invalid` |
| ST-3 | P1 | M2 | Large payloads (artifacts, archived transcripts, exports) are content-addressed blobs; the ledger stores `{hash, size, class, media}` | moving the cold volume changes no ledger reference |
| ST-4 | P2 | M2 | A cold volume that is absent or full marks the blob `unavailable`; the run continues and the UI says so | no silent drop, no blocked agent |
| ST-5 | P2 | M2 | Per-class quotas and retention; `darkfactory gc` deletes only unreferenced, expired blobs | a referenced blob is never collected |
| ST-6 | P2 | M2 | Write accounting: bytes written in 24 h per class, top writers, fsync count | visible in `status --json` and the run view |
| ST-7 | P1 | M2 | Bounded defaults: no full transcript, 4 MiB ring, artifact caps (100 files / 10 MB each / 50 MB total); persistence is opt-in | a 10-minute session at 1 MB/s writes < 100 MB by default |
| ST-8 | P1 | M2 | Checkpoint hygiene: exclude `node_modules`, `target`, `.venv`, `dist`, `.cache`; `gc.auto=0`; per step, not per message; skip empty commits | `git count-objects` stays bounded over 100 steps |
| ST-9 | P3 | M3 | Loss-tolerant ephemeral scratch for agent `TMPDIR` and build caches, size-capped | disabling it changes no ledger-referenced hash |

Caveats to state in the docs, not to hide: **tmpfs can swap**, so it is not a
wear guarantee; **HDD is for sequential, cold blobs only** — never the DB, a git
worktree, or a package index. The strongest reason for cold storage is capacity
and backup, not wear.

### 2.12 Mobile (MO)

This release ships the native Android client (MO-11) and the loopback browser.
The installable PWA, push notifications, the tablet key bar and `snapshot + seq`
reconnect are deferred target-state, not release gates; they keep their IDs.

The server owns the PTYs, so the phone is a client that can disappear at any
moment. Phone and tablet are different products:

| Device | Mode | What it does |
|---|---|---|
| Phone (Samsung/iPhone) | **read-only or workspace** | fleet, run timeline, blocked badge and chat transcripts; workspace adds approve/deny, stop, one-line input, the full terminal and chat writes |
| Tablet / DeX | **workspace** | full xterm, accessory key bar, resize, scrollback |

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| MO-1 | P1 | M2 | Installable PWA from the binary; iOS 16.4+ and Android Chrome; native Android clients are permitted and must satisfy the same phone workflow gates | Add to Home Screen works for the PWA; an Android client records UI-24 evidence before daily-driver claims |
| MO-2 | P1 | M2 | Pairing chooses read-only or workspace; workspace is one capability covering the terminal and OpenCode writes, because chat can run workspace actions | a read-only phone can view state, runs and chat transcripts but cannot attach, prompt or mutate; a workspace phone can do both |
| MO-3 | P1 | M2 | Tablet/DeX gets a full terminal with an accessory key bar (Esc, Tab, Ctrl, arrows, PgUp/PgDn, `\|`, `~`, `/`) and paste | a TUI can be driven without a hardware keyboard |
| MO-4 | P1 | M2 | `visualViewport` follows the soft keyboard; the PTY resizes to the visible area; the shell is pinned to the visual viewport's height and offset, not the layout viewport | keyboard open/close does not hide the terminal cursor or prompt |
| MO-5 | P1 | M2 | Reconnect on `visibilitychange` and network loss using `snapshot + seq`; a pane is never blanked | background for 10 minutes, return, output is contiguous |
| MO-6 | P2 | M2 | Safe-area insets; `ResizeObserver` for Split View, Stage Manager and DeX; font-size control | 390 px and 1366 px both usable |
| MO-7 | P2 | M3 | Web Push for `blocked` / `needs_approval` on an installed PWA; webhook (ntfy/Telegram) fallback | a blocked run notifies without the app open |
| MO-8 | P2 | M2 | Terminal input disables autocapitalize, autocorrect and spellcheck; clipboard requires a secure context | no auto-capitalised commands |
| MO-9 | P3 | M3 | Hardware-keyboard shortcuts on iPad/DeX where the browser allows; reserved browser keys are not promised | documented key map |
| MO-10 | P2 | M2 | No vendor push service; notifications are a local desktop notification and an optional user-configured webhook, off by default | with no webhook configured, the daemon makes no outbound call |
| MO-11 | P1 | M2 | The Android client is a native APK with bundled UI assets, a Keystore-backed per-device token registry, a loopback-only app origin, no JavaScript auth bridge, no cookies, and navigation restricted to that origin. It pairs from the host code and keeps several hosts. A `401`, logout or AEAD failure returns to the picker or pairing when none remain but does not delete the host or its pin; only an explicit Forget removes an entry. A changed host key is refused and kept. It must still pass the UI-24 phone workflow on a real emulator before daily-driver claims. | `adb install` then pair, switch between two hosts, load the fleet, stream a PTY, send input, open a chat, background/foreground, revoke one device and keep the other; token absent from WebView storage and logs |

Implementation note (2026-11-02): MO-10 is implemented with a local daemon
attention feed, `notify-send` on the desktop, and an Android `dataSync`
foreground service that polls the feed over the pinned E2EE envelope. This is
not Web Push; MO-7 (PWA Web Push and the optional ntfy/Telegram webhook) remains
open. The feed is in memory and is not a durable notification history.

### 2.13 Device pairing and safety (DP)

The tailnet is the network gate; a one-time code is the application gate; the
device gets its own scoped credential, never the master token.

| ID | Pri | M | Requirement | Acceptance |
|---|---|---|---|---|
| DP-1 | P1 | M2 | Pairing is a host-initiated one-time code/QR, TTL ≤ 120 s, single use, hashed at rest, 5 attempts, never logged. The browser link carries the code in the URL fragment; the native QR carries the base URL and code in a `darkfactory://pair` URI. | reuse or expiry → `401 pair_invalid`; code absent from logs; camera-opened QR prefills the native form and still requires fingerprint confirmation |
| DP-2 | P1 | M2 | A cookie device credential is 256 bits in a `__Host-` `HttpOnly` `Secure` `SameSite=Strict` cookie; stored as `SHA-256(instanceSecret ‖ token)` | no master token on a paired device; restore without the secret fails closed |
| DP-3 | P1 | M2 | Scopes are `read` (view state, runs and chat transcripts) and `workspace` (full control: terminal, OpenCode, files and git); `admin` is host-only unless explicitly granted | a read-only device cannot mutate; a workspace device reaches PTY and chat routes |
| DP-4 | P2 | M2 | Workspace access is one per-device capability granted by full scope: PTY create/stream/input/resize and OpenCode writes share it, because an OpenCode turn can already run workspace actions. A read-only device is refused every PTY route and every mutation except logout and redraw, while state, runs and chat transcripts stay readable. The persisted `terminal` field is compatibility output only; it is derived from scope. | a full-scope device with a stale `terminal:false` row can stream (missing session -> 404); a read-only device gets `403 terminal_required` on a stream and cannot prompt OpenCode, but can read chat transcripts and request a redraw |
| DP-5 | P2 | M2 | Destructive verbs (repo delete, worktree force-remove, policy set, panic, revoke-all) require confirmation from a second station | a phone alone cannot force-remove a worktree |
| DP-6 | P1 | M2 | Revoke per device and all; idle expiry (30 d, renewed at most hourly, 90 d absolute); restore fails closed; revoking a device key also drops its sessions | revoke takes effect on the next request; a session idle past the window is refused and its row deleted; a revoked key cannot mint another session |
| DP-7 | P2 | M2 | Pairing, device-key registration and device actions are ledger events with `actor: human:device:<id>` | the ledger answers "approved from which device" |
| DP-8 | P1 | M2 | The agent listener has no pairing or device verbs; `/pair` is refused on any non-loopback plain-HTTP listener, even with `--allow-remote` | agent token against `/pair` → `404`; LAN bind refuses pairing |
| DP-9 | P1 | M1 | `Host`/`Origin` allowlist, no CORS, `X-DF-Request` on mutations, CSP `default-src 'self'`, `frame-ancestors 'none'` | cross-origin pairing attempt → `403 origin_denied` |
| DP-10 | P2 | M2 | `status` prints the honest limit: pairing is network-safe, not same-uid-safe | label visible without reading docs |
| DP-11 | P3 | M3 | Optional WebAuthn step-up for admin actions; user presence, not device binding | passkey prompt on an admin approval |
| DP-12 | P1 | M2 | Scopes are `read / workspace / admin`; `workspace` is full access to PTYs, OpenCode, files and git, and is granted only at pairing; `read` cannot type or mutate | a read-only device cannot attach, type, or prompt OpenCode |
| DP-13 | P1 | M2 | `/pair` is rate-limited globally and per source, returns a uniform `401`, and takes the device name from the host | 100 bad codes → 429; no response distinguishes wrong from expired |
| DP-14 | P1 | M2 | Device-key auth is additive to cookie auth: after an authenticated login a client may register an ES256 public key; the private key stays non-extractable in IndexedDB, the server stores only SPKI, and scope/workspace are inherited from the registering principal. Challenge is one-use and ≤120 s; verify returns a short-lived session bearer; challenge/verify require same origin, `X-DF-Request`, and rate limits. No cookie flags, CSP, or master-token storage change. | WebKitGTK: first token login registers a key; a new process with no token does `401 state` → challenge/verify → `200 state`; a daemon restart repeats it without typing; revoking the key → `401` and Connect; `privateKey.extractable` is false; no token in `localStorage` or a URL |
| DP-15 | P1 | M2 | Pairing returns the same 256-bit device credential as a bearer token and a stable `hostId` for native clients. The token is stored server-side only as a hash, accepted in `Authorization: Bearer`, and inherits the same scope, workspace, expiry and revocation as the cookie. The browser keeps using the HttpOnly cookie and ignores the token; no token, raw or hashed, enters a log, URL or query. | pair a native client → `GET /state` with `Bearer <token>` returns the same device, hostId and scope; revoke → next request `401`; a token absent from logs |
| DP-16 | P2 | M2 | `serve --idle-exit` closes the HTTP listener after no request has been in flight for the window; `launch --idle-exit` passes it to the detached daemon. The session host is not stopped, so PTYs survive and the next start reattaches. | isolated `serve --idle-exit 1s` logs the idle exit and leaves the session host; a live PTY survives the listener exit |
| DP-17 | P0 | M2 | Remote browser UI is refused by the daemon; the remote trust boundary is a separately distributed native client. The native client pins the daemon P-256 public key and encrypts request/response bodies, credentials, headers, and streams with app-layer ECDH/HKDF/AES-GCM, so a transport that can rewrite the served browser cannot read the client. The first pairing requires the user to compare the host fingerprint on both screens; this is not claimed for the local browser. | remote UI `403`; native client has no daemon-supplied HTML/JS; sealed pairing token; `/api/v1/e2ee` frames for state, PTY and input; replay/tamper refusal; a host-key change refuses; a proxy sees no prompt/terminal bytes or bearer token |
| DP-18 | P1 | M2 | A native client may keep several hosts in one Keystore-backed registry. Each host has its own endpoint, token, device id, client key, pinned host key, fingerprint, scope, and stable `hostId`; re-pairing refreshes the token under the same pin, and an identity change requires explicit Forget. One host is connected at a time; a switch destroys the previous loopback origin and WebView so ids cannot cross hosts. A down host stays in the registry and is shown offline; a changed host key refuses connection and preserves that host; forgetting a host is local and does not revoke it on the desktop. | two hosts with different `hostId`s; switch both ways; stop the active host → picker shows offline while the other connects; replace one host key → that entry refuses connection and keeps its pin; the other host stays; prefs contain one encrypted registry, no host id or token in plaintext |
| DP-19 | P0 | M2 | Android calculates the pairing fingerprint from the received host public-key DER and rejects a mismatch. The user compares that calculated value with the desktop. | A substituted key with the original fingerprint is rejected before confirmation. |
| DP-20 | P0 | M2 | The daemon records each authenticated encrypted request ID durably before dispatch and retains it through the timestamp acceptance window. Storage failure prevents dispatch. | A replay after restart or after five minutes with a future timestamp is refused; expired records are removed. |
| DP-21 | P0 | M2 | The native envelope supports a hybrid suite, `x25519-mlkem768-v1`: a fresh X25519 ephemeral and an ML-KEM-768 ciphertext per request, with both shared secrets required by one HKDF input. The suite is selected at pairing and stored per device. | A v2 pair and `/state` succeed; removing either shared secret changes the keys; `make android-pq-check` and `make android-pq-live` agree with the daemon |
| DP-22 | P0 | M2 | A device's suite is sticky. A v2 row refuses a v1 envelope; an authentication failure never falls back to another suite; a host response without the pinned suite is refused. | v1 envelope on a v2 row → `e2ee_suite_downgrade`; a tampered envelope → refusal, no retry at v1 |
| DP-23 | P1 | M2 | The host publishes a signed key bundle (P-256, X25519, ML-KEM-768, epoch). The phone verifies it under the P-256 key it carries and pins `fingerprintV2` over the identity and suite; a bundle rotation under the same identity and suite keeps the pin. | substituted bundle field → signature failure; same P-256 with new X25519/ML-KEM → accepted and re-saved; `TestE2EEV2BundleSignature`, `make android-pq-check` |
| DP-24 | P0 | M2 | Pairing enforces the host-configured minimum suite. With `--e2ee-min-suite=2` the unsealed legacy token and a legacy client key are refused with `e2ee_suite_downgrade`; a host advertising v2 without a valid bundle is refused by the client, not paired as v1. | v1 pair under min suite 2 → 403; stripped bundle → client refusal; `TestE2EEV2PairRequiresBundle`, `make android-pq-live` |
| DP-25 | P1 | M2 | A re-pair at a lower suite or a different `fingerprintV2` is an identity change. The phone refuses it and keeps the stored pin; only an explicit Forget removes the host. | changed bundle/pin → host shown with an error, row still present; `401`/AEAD failure does not delete a host |
| DP-26 | P0 | M2 | The accepted suite is authenticated inside the sealed pair payload and inside the envelope AAD/HKDF transcript; a rewritten version or suite cannot produce a key that opens the frames. | an outer suite hint cannot change the accepted sealed suite; a changed wire version or suite is refused before dispatch; `TestE2EEV2VersionFlipFailsClosed` |
| DP-27 | P0 | M2 | The phone confirms the calculated host fingerprint before transmitting a pairing code. A v2 pairing sends only an authenticated encrypted code bound to the confirmed host bundle and client ephemeral material. New Android pairings require a valid hybrid bundle regardless of advertised metadata. The default host minimum suite is 2; existing legacy connections require an explicit value of 1. | substituted ephemeral material cannot open `sealedCode`; a raw-code v2 request is refused; cancel before confirmation sends no pairing request |
| DP-28 | P0 | M2 | A v2 encrypted response authenticates completion with an end marker bound to the request and frame index. A missing, modified, reordered, or replayed marker is refused. Android bounds encrypted frame lengths before allocation and bounds accumulated notification bodies. | complete response succeeds; every truncated response prefix and a missing end marker fail; oversized frames fail before allocation |
| DP-29 | P1 | M2 | Pull-request security checks execute production client interoperability, disposable pairing, protocol fuzzing, Android compile/lint, and advisory scans for both Go modules and the pinned Java and JavaScript dependencies. | `make security-check`; security and vulnerability workflows; a scan failure fails its job |


Implemented in this build: a host-initiated 40-bit code with a 120 s TTL,
single use, hashed in memory, never logged, and carried in the browser URL
fragment; the dialog renders a native `darkfactory://pair?base=…&code=…` QR so
the phone camera can prefill the app (DP-1); when `publicURL` is set, the link
and QR use that origin instead of the request origin, and its host is allowed
automatically, so a phone can scan a Tailscale Serve URL without editing
loopback; refusal of non-loopback plain HTTP and forwarded plain requests (DP-8);
the `Host`/`Origin`/`X-DF-Request` gates (DP-9); a 5/min-per-source and
30/min-global limit with uniform `401 pair_invalid` (DP-13); and a `status`
line stating that pairing is network-safe but same-uid (DP-10). Each pairing now
creates a device record and its own hashed cookie row, with a host-chosen label
and `full` or `read` scope; `GET /devices`, per-device delete, and
`POST /devices/revoke-all` are implemented, and a `read` cookie is refused on
mutations except logout and a PTY redraw (SIGWINCH only). The cookie is
`__Host-df_session` (`Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`, no
`Domain`) and its stored key mixes a `0600` instance secret beside the token, so
a `state.db` restored without that file fails closed (DP-2); rotating it with
`darkfactory token --rotate-sessions` drops every old row, so no device stays
listed that can no longer authenticate. Workspace access is derived from full
scope, so terminal and OpenCode writes cannot drift apart (DP-4); the cookie
renews at most hourly while the device is active, and goes idle after 30 days
with a 90-day absolute cap (DP-6). Device-key auth
(DP-14) is additive: an authenticated client registers an ES256 public key,
keeps the non-extractable private half in IndexedDB, signs a one-use challenge,
and receives a 12-hour in-memory session bearer; the key inherits the
registering principal's scope and workspace access, appears in the devices list,
and its revocation drops both the key and its sessions. Pairing now also
returns the hashed device credential as a bearer token for native clients; the
same row, scope, expiry and revoke path apply (DP-15). The target `admin`
scope (DP-3), second-station confirmation (DP-5), and pairing/device ledger
events (DP-7/12) are not implemented; idle expiry (DP-6) is. The
five-attempt rule is
enforced as five attempts per source per minute, not as a per-code counter,
because the code has no separate record id.

DP-19 is implemented in the Android pairing path. `HostFingerprint.verified`
hashes the received host SPKI DER, compares that value with the response's
`fingerprint`, and refuses a mismatch before the token is used, so the
confirmation dialog shows a computed value rather than an echoed field.
`sh test/android/check-fingerprint.sh` pins the accepted and substituted-key
cases on a JVM; it is not an emulator or phone run. DP-20 is implemented in the
daemon: `Store.ConsumeE2EERequest` records the request ID in one bbolt
transaction before dispatch, keyed by device and ID, and retains it until
`envelope.ts + 5 min`. A future-dated envelope inside the ±5 min acceptance
window is therefore kept past its arrival, a replay is refused, expired rows
are pruned, and a storage failure refuses the request. `internal/store` covers
reopen, per-device separation and expiry; `TestE2EEReplayRefusedAcrossDaemonRestart`
adds a server restart and a future timestamp, but it is in the tree without a
recorded pass in an artifact. Records start with the first
request after the upgrade: requests accepted before it cannot be reconstructed.
Neither test replaces an external security review. The internal review at
[reviews/e2ee-protocol-review-2026-11-02.md](reviews/e2ee-protocol-review-2026-11-02.md)
confirmed two envelope defects — an encoded path that re-entered the handler
and an unbounded replay sweep per request — both fixed here, with tests, plus
hardening gaps recorded in its resolution table. DP-19's acceptance is met by
the computed-fingerprint check. DP-20's durability and its bounded sweep are
implemented. The envelope route is rate-limited. The hardened v2 format requires
an authenticated response terminator, and Android bounds incoming frames. Clock
offset handling remains open. No external security review has been performed.

Daemon-side hybrid envelope and Android v2 client (2026-11-09): `/api/v1/e2ee`
accepts a second suite, `x25519-mlkem768-v1`, selected by host policy
(`--e2ee-min-suite`, default 2; legacy requires an explicit value of 1) and stored per device row. The host identity now
carries a static X25519 and an ML-KEM-768 half in the same `e2ee` bucket,
published in a bundle signed by the P-256 identity; a legacy PKCS#8 row is
upgraded in place without changing the pin. A v2 request carries a fresh X25519
ephemeral key and an ML-KEM-768 ciphertext, and both shared secrets feed one
HKDF-SHA256 input. A v2 row refuses v1 envelopes, and `min-suite=2` also refuses
legacy rows and the unsealed pairing response with `e2ee_suite_downgrade`.
Covered by `TestE2EEV2PairAndState`, `TestE2EEV2RejectsV1Envelope`,
`TestE2EEV2VersionFlipFailsClosed`, `TestE2EEV2PairRequiresBundle`,
`TestE2EEV2MinSuiteRefusesLegacyRow`, `TestE2EEV2ReplayAndTamper`,
`TestE2EEV2BundleSignature` and `TestE2EEIdentityUpgradesLegacyKey`.

The Android client implements v2 as well (DP-21..DP-26). Its X25519 and ML-KEM operations use the pinned Bouncy Castle lightweight
APIs through Java adapters under
`android/app/src/main/java/dev/darkfactory/mobile/crypto/`. Application code
only encapsulates; it does not call the provider's ML-KEM decapsulation APIs. The bundle,
transcript and envelope code is Java too and is executed on a JVM twice:
`make android-pq-check` compares every derived value with Go and has Go
decapsulate the Java ciphertext; `make android-pq-live` pairs with and reads
`/api/v1/state` from a disposable `--e2ee-min-suite=2` daemon, and observes the
downgrade and replay refusals. The phone refuses a v2 advertisement with a
missing or invalid bundle instead of pairing as v1, and re-verifies the stored
bundle on every load. A changed dynamic half under the same P-256 key and suite
is re-saved; a changed pin is refused and the row is kept (D5/H-3), so only an
explicit Forget removes a host. These are JVM, synthetic-daemon and one
emulator (`dfphone`) results, not a physical-phone run. Forward secrecy against host-key theft is unchanged,
and the identity is still plaintext in `state.db`.

**Honest limit.** A same-uid agent can read `~/.config/darkfactory/token`, run
the CLI and pair a device. Pairing protects against the network, a lost phone
and casual misuse; it cannot erase bytes already rendered on the device, and
only containment (`jail`/`strict`, GV-14/19, OP-7) protects against a process
running as the same user.

---

## 3. Non-functional requirements

### 3.1 Performance

| ID | Requirement | Acceptance |
|---|---|---|
| NF-P-1 | Local reads p95 < 50 ms; mutations p95 < 200 ms excluding git | benchmark with 100 sessions |
| NF-P-2 | Terminal stream adds < 30 ms latency locally | 1 KB burst, measured at the client |
| NF-P-3 | Fleet view with 50 sessions stays under 100 ms server time | benchmark fixture |
| NF-P-4 | No O(n²) work on the stream path; per-chunk cost O(1) | profile under 10 MB/s |

### 3.2 Resources

| ID | Requirement | Acceptance |
|---|---|---|
| NF-R-1 | Idle RSS < 50 MB; per session < 8 MB excluding the ring | measure with 0 and 20 sessions |
| NF-R-2 | Ring is bounded and configurable; default 4 MiB/session | overflow test |
| NF-R-3 | Binary < 15 MB; disk footprint < 20 MB excluding worktrees | `ls -l` |
| NF-R-4 | Idle writes < 1 MB/h; output is never fsynced per chunk; decisions are fsynced before acting | 24 h soak with write accounting |
| NF-R-5 | No unbounded blob growth; per-class quotas are enforced, not advisory | fill the cold class; writes fail honestly |

### 3.3 Security and privacy

| ID | Requirement | Acceptance |
|---|---|---|
| NF-S-1 | No telemetry, no vendor push, no update check; the only outbound call is a user-configured notification webhook, off by default | SC-7 |
| NF-S-2 | Credentials and tokens never logged or ledgered; files 0600 | planted-secret test |
| NF-S-3 | Direct dependency budget ≤ 3 without a written exception | `go list -m` check in CI |
| NF-S-4 | Constant-time comparison for every secret | code review + one test per comparison |
| NF-S-5 | A paired device never holds the master token; cookie credentials are stored hashed, device-key private keys stay non-extractable on the client, and both are scoped | inspect the device record, cookie, IndexedDB key, and public-key row |
| NF-S-6 | Secrets never appear in `/state`, logs or the ledger; preset env values are write-only | planted-secret test across all three |
| NF-S-7 | The browser emulator ignores OSC 52 and OSC 8 and title sequences, and links are not rendered as clickable; the native window forwards an OSC 52 write only to Ghostty's authorization dialog and keeps reads inert | fixture output cannot silently write the clipboard or spoof a title |

### 3.4 Reliability

Status: CI runs `make check` (`vet`, the hermetic suite, `-race`, the static UI
gate, `clientbench-check`, `desktop-check`). There is no dedicated kill -9
restart job; restart and PTY survival are covered by package evidence under
`test/artifacts/` and must be repeated per release checklist. NF-REL-1 is
target-state.

| ID | Requirement | Acceptance |
|---|---|---|
| NF-REL-1 | Restart-correctness suite in CI: create sessions, kill -9, restart, assert states | CI job |
| NF-REL-2 | State writes are atomic (temp + rename / bbolt transaction) | fault injection |
| NF-REL-3 | No unbounded goroutine or fd growth over 24 h | soak test |
| NF-REL-4 | The suite runs under the race detector as part of `make check`; the shipped binary stays cgo-free | `make race` clean |
| NF-REL-5 | Byte-level attach heuristics carry fuzz targets whose seed corpus runs in the hermetic suite; the fuzzer asserts structural invariants, not classifications | `go test ./...` runs the corpus; `make fuzz` explores past it |

### 3.5 Portability

| ID | Requirement | Acceptance |
|---|---|---|
| NF-PORT-1 | Linux first (amd64/arm64); macOS next; Windows explicitly out of scope | build matrix |
| NF-PORT-2 | The daemon has no cgo and stays a static binary; optional client modules may use native dependencies | `CGO_ENABLED=0` root build; native client dependencies stay outside the daemon module |

### 3.6 Supply chain

| ID | Requirement | Acceptance |
|---|---|---|
| NF-SUP-1 | Vendored deps; `-trimpath`; reproducible build | rebuild → identical hash |
| NF-SUP-2 | Third-party assets (terminal emulator) pinned with version, license, sha256 | `third_party/README.md` |
| NF-SUP-3 | Release artifacts published with checksums | release script |
| NF-SUP-4 | A reachability scan (`make vuln`, govulncheck) reports no advisory this code can reach; the scan is dev-only and the shipped binary makes no outbound call | `make vuln` output |

### 3.7 Observability

| ID | Requirement | Acceptance |
|---|---|---|
| NF-O-1 | `status --json`: version, schema, listen, sessions, containment, deps | schema-stable fixture test |
| NF-O-2 | Every session has a lifecycle the ledger can answer for: who, what, when | replay a day of events |

---

## 4. Milestones

**Historical planning.** This table is the target roadmap, not the release
plan. A row that lists a deferred requirement does not put it in the current
release. The release gates are in [../HANDOFF.md](../HANDOFF.md).

| M | Name | Exit criteria | Requirements |
|---|---|---|---|
| **M0** | Foundation | Build, add repo, create worktree, launch agent, see output, loopback guard, CSP, safe stop | WS-1/2/11, WT-1/2/4/7/11, SS-1/2/3/8/11/14, AG-1/5/7/9, GV-10/16/20, UI-1, UI-9, OP-1/2, NF-S-6 |
| **M1** | Daily driver | Env probe, idempotent launch, per-worktree HOME, jail containment, `open` + cookie auth, cascade delete, reaper, resize, typed refusals, gateway seam, real terminal, fleet view, runs with command/exec/pty steps, native OpenCode panel | WS-3, WT-3/5/8, SS-5/6/9/12/13/15, AG-2/3/8, GV-1/4/7/8/17/19, UI-2/3/16/18, OP-6/7/9, DP-9, RN-1/2/3/4/9/13, NF-S-7 |
| **M2** | Defensible | Capabilities, policy, ledger, handover, restore, restart suite, checkpoints, gates, replan, storage tiering, installable PWA, paired devices | WT-6/9/10, SS-4/10, AG-4/6, GV-2/3/5/6/9/11/13/15/18, UI-4/5, OP-3/4/5/8, EX-1, RN-5/6/7/8/10/11/12/14, ST-1/2/3/4/5/6/7/8, MO-1/2/3/4/5/6/8/10, DP-1/2/3/4/5/6/7/8/10/12/13/14 |
| **M3** | Overseer | Proposals, budgets, depth, approval cards, strict containment, webhook notifications, WebAuthn step-up | GV-12/14, OV-1..5/7, UI-6, EX-2, ST-9, MO-7/9, DP-11 |
| **M4** | Autonomy | Learned allow-list proposals, ACP | OV-6, EX-3 |

---

## 5. Traceability

| Lesson | Requirement |
|---|---|
| R1 one identity | SS-2, WT-1 |
| R2 env probe | AG-2 |
| R3 visible failure | GV-7, UI-4 |
| R4 deletion blast radius | WT-4/9, SS-9 |
| R5 explicit binding | GV-10 |
| R6 idempotent launch | AG-3 |
| R7 honest status | SS-10, AG-6 |
| R8 detached host | SS-4/6/7 |
| R9 journaled resize | SS-5 |
| R10 bounded replay | SS-4 |
| R11 absence ≠ permission | SS-6 |
| R12 forward-only state | OP-5 |
| R13 capability scoping | GV-12 |
| R14 notes as product | OP-6 (log discipline) |
| S0 gateway | GV-4 |
| S1 no global agent credential | GV-11 |
| S2 transport principals | GV-1 |
| S3 binding | GV-10 |
| S4/S5 observer feed | OV-4 |
| S6 ledger | GV-6 |
| S7 policy | GV-5 |
| S8 handover | GV-9 |
| S9 no self-approval | OV-3 |
| S10 injection handling | OV-2, OV-4 |
| S11 auto-allow as data | OV-6 |
| S12 budgets/depth | GV-15, OV-5 |
| S13 containment | GV-14/19 |
| S14 honest label | OP-7 |
| S15 browser auth | GV-17 |
| S16 state redaction | GV-16, NF-S-6 |
| S17 terminal safety | SS-13, NF-S-7 |
| S18 session tabs | UI-10 |
| S19 checkpoint side refs | RN-13 |
| F1 run = unit of work | RN-1, RN-2 |
| F2 node@visit, append-only | RN-4 |
| F3 checkpoint branch per run | RN-5 |
| F4 named human gate | RN-6 |
| F5 goal gates | RN-7 |
| F6 failure signatures | RN-8 |
| F7 events as truth | RN-1, RN-9 |
| F8 honest isolation label | GV-14, OP-7 |
| F9 redaction as a utility | NF-S-2 |
| F10 pinned resolution at admission | AG-4 |

ST, MO and DP come from operational constraints — media wear, mobile process
suspension, device loss — not from a teacher.

---

## 6. Open decisions

1. **Terminal emulator**: vendored xterm.js (proven, 300 KB, JS) vs a Go
   emulator (no JS, immature). Blocks UI-2 and SS-4.
2. **Policy language**: named Go predicates vs CEL. Recommendation: predicates
   until users author rules.
3. **Detached host** (resolved): a separate `darkfactory session-host` process
   owns the PTYs and rings; `serve` attaches over a `0600` Unix socket and
   detaches on shutdown. Affects SS-7 and the binary count.
4. **Per-worktree HOME**: symlink farm vs bind mounts vs copy. Affects WT-8.
5. **Overseer transport** (resolved): same binary, proposals only; it never
   holds a capability. Affects OV-1 and GV-2.
6. **Containment default** (resolved): `off` for compatibility with agents
   that need the real HOME and a shared backend (OpenCode); `jail` is opt-in
   per worktree or agent preset, `strict` later.
7. **macOS**: when, and whether the PTY implementation shares the same code path.
8. **Plan authoring**: hand-written step list, or an agent drafts a plan that a
   principal freezes. Affects RN-2 and RN-11.
9. **Run shape**: flat ordered steps vs a DAG. Start flat; add edges only when a
   real workflow needs fan-out.
10. **Cold storage default**: hot-only unless configured, vs auto-detecting a
    second disk. Recommendation: hot-only; silent tiering surprises people.
11. **Pairing second factor**: host confirmation code vs WebAuthn. Host code in
    v1 (DP-5); passkeys later (DP-11).
12. **Phone terminal**: keep it, but do not pretend it is a separate security
    boundary from OpenCode chat. Pairing grants either read-only or workspace;
    workspace covers both (MO-2, DP-4).
13. **Android signing and distribution** (partly decided): a release-signed APK
    is attached to the `v0.1.0-rc.1` prerelease for direct sideload install.
    The release key lives outside the repository, and future updates must use
    the same key. Play/F-Droid distribution, an automated update path, and CI
    signing are not chosen. The emulator acceptance run used a debug APK; the
    real-phone run is still open.
14. **Packaged desktop release** (undecided): the Wails window is an evaluation
    shell built from `desktop/`, not a packaged installer. Whether it becomes a
    distributed artifact, and with what signing, is open.

---

## 7. Change control

- IDs are immutable; withdrawn requirements keep their id and gain a reason.
- Every MUST has at least one test before it is marked done.
- A requirement that cannot be tested is not a requirement — split it until it
  can.
- Priority changes are recorded with a date and a reason, not silently edited.
