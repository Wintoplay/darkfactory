# Agent authority and the overseer

**Scope.** Can we add an AI supervisor ("overseer") that watches the fleet and
acts on our behalf — without a rogue agent using the same machinery to take
control, read every repo, or exfiltrate the machine? This document answers
three questions: what Orca already has, what its trust boundary actually is,
and what we should build instead.

Companion to [orca-lessons.md](orca-lessons.md), which covers the reliability
lessons. This one covers authority. Read that first if you have not.

---

## 0. Triage: build now, park, never

This document is a design space, not a backlog. At 1,750 LOC, single-user,
loopback-only, with **no control channel for agents at all**, most of it is
premature. What is actually worth building next is ~300 lines:

| # | Item | Cost | Why now |
|---|---|---|---|
| 1 | Loopback default + explicit `--allow-remote` + warning | ~20 lines | T8, and it is a one-line mistake to leave open |
| 2 | Cascade delete + PTY reaper (stop sessions before `worktree remove --force`) | ~60 lines | Live bug: orphaned shells in a deleted cwd |
| 3 | Key worktree directories by ids, not display names | ~30 lines | Cheap now, a migration later (R1) |
| 4 | Environment probe module (login shell, cached, logged) | ~100 lines | Agents "not found" under systemd/GUI launch |
| 5 | One `authorize(principal, action, target)` seam in front of every side effect | ~100 lines | Architectural: makes policy a wrapper later instead of a rewrite (S0) |

Everything else has a trigger:

| Parked | Build it when |
|---|---|
| Policy engine (CEL or Go rules) | a second principal can act: an overseer, an agent-callable CLI, or remote clients |
| Audit ledger | same trigger — and start as append-only JSONL, not a schema |
| Handover state machine | the first action the overseer may take without you |
| Per-session capability tokens | the moment an agent gets *any* verb |
| container/uid jail (T3) | you run a repo you do not trust |
| Delegation depth/fan-out | there is more than one overseer |

The uncomfortable truth underneath the table: **today an agent misbehaves as
you.** It holds no credential we gave it, but it runs under your uid, so it can
read `~/.config/darkfactory/token` and use the HTTP API by hand. No in-process
policy fixes that; only uid/namespace isolation does. So the honest choices are
(a) accept it, label it, and keep the surface small, or (b) T1. There is no
middle option, and building the governance stack while leaving the token
readable would be security theatre.

We choose (b) at M1: the systemd scope (T1) is no longer parked, because
per-worktree HOME makes it cheap. Containment lands before the M2 governance
stack, so capabilities and policy sit on something that can refuse. Where a host
cannot jail (`off`), the stack is **audit, not enforcement**, and every
acceptance test says which of the two it is testing.

---

## 1. What Orca already has

Orca's feature set, filtered to what matters for a terminal-first Go clone.
"Keep" means it is in scope for DarkFactory; "cut" means we already decided
against it in the lessons doc.

| Feature | Orca evidence | Ours |
|---|---|---|
| **Worktrees as the primary object** | 2,509 PRs; sidebar is the product | Keep |
| **Out-of-process PTY host** (`orcad`) | 3,060 PRs; owns PTY + headless screen | Keep (R8) |
| **Agent launch presets + status** | 3,139 PRs; ~45 agents in one table | Keep (R7) |
| **Orchestration / supervised workers** | 1,609 CLI+daemon PRs; see §1.1 | Keep the protocol, change the authority |
| **CLI as the only API** | "There is no public REST API or Orca MCP server. Agents control Orca by running the CLI" | Keep |
| **Agent-callable skills** | 889 PRs; version-matched guides inside the binary | Later |
| Native chat / transcripts | 1,221 PRs, 5+ reverts | Cut |
| Diff review + forge integrations | 3,804 PRs | Cut for now |
| Relay / mobile / browser automation | 3,913 PRs, most reverts | Cut |

### 1.1 The orchestration model (this is the "overseer")

Orca's supervision is not a separate service. It is **a role played by an
ordinary agent**, given vocabulary and bookkeeping by the daemon:

| Object | Meaning |
|---|---|
| `Run` | A durable namespace and the coordinator's inbox. Does not schedule. |
| `Task` | A unit of work. |
| `Dispatch` | One authoritative *attempt* at a Task. |
| Delivery | A FIFO message (question, escalation, `worker_done`) that replays until acked. |

The coordinator agent loops:

```sh
orca orchestration run-create --objective "..."
orca orchestration worker-start --spec "..." --worktree current --agent codex
orca orchestration check --wait --types "worker_done,escalation,question"
orca orchestration reply --id <message_id> --body "..."
```

Workers get an injected preamble with their Task/Dispatch IDs, must heartbeat,
must `ask` through the coordinator (never open a local prompt), and must send
`worker_done` exactly once with `--outcome succeeded|failed`. Nesting is capped,
and **a worker cannot bind the Run it works for, or an ancestor**. The guide's
safety floor is the best sentence in the repo:

> "Preserve work and authority and report the state as unknown or
> `unverifiable`. Only positive proof of exit authorizes stop, abandon, or
> retry, and only an accepted settlement authorizes release."

That is *lifecycle* authority: who owns this attempt, is it still live, may I
retry it. It is well designed and we should copy it almost verbatim.

---

## 2. What Orca does not have

This is the part that matters for "a rogue agent cannot control and steal
everything". Three verbatim facts from the source and docs:

1. **The control plane is single-user trust.**
   `runtime-rpc-request-admission.ts` accepts a request if its `authToken`
   equals the runtime's one shared token; the token lives in a mode-0600
   metadata file next to the socket. Any process running as you can read it and
   becomes `callerScope: owner` with the full RPC surface.

   The orchestration caller resolver says it outright:

   > "ids identify same-user callers, not credentials."

2. **There is no per-agent restriction.** `isAgentRpcCaller()` exists, and its
   only consumer attributes browser activity. An agent and the human use the
   same token, the same socket, the same verb set. `runtime-rpc-mobile-method-
   allowlist.ts` scopes *phones*; nothing scopes *agents*.

3. **Agents are explicitly not sandboxed.** From `ORCA-CLI.md`:

   > "Orca-managed worktrees are normal git worktrees, not security sandboxes."

   > "Orca worktrees and agent terminals are not sandboxes. Check permissions
   > and avoid exposing the mobile/runtime port publicly."

So: Orca has an overseer in the *coordination* sense and nothing in the
*enforcement* sense. Its guards are anti-confusion guards (don't let two
coordinators own a Run, don't let a worker bind its own ancestor, don't trust a
terminal title or a copied ID). They stop accidents and mixed wiring. They do
not stop a process that decides to `curl` the runtime with the token it just
read.

That is a defensible choice for a desktop app where the agent is the user's own
tool. It is the wrong choice for us the moment we add an overseer that can act
on many worktrees at once — because the overseer becomes a high-value target and
the agents it supervises are, by definition, autonomous.

---

## 3. Threat model

Principals: **human** (you, at the browser), **overseer** (a supervised agent
with fleet visibility), **worker** (an agent in a worktree), **outsider** (repo
content, a malicious file, a compromised dependency, anything an agent reads).

| # | Attack | Today in DarkFactory | Required mitigation |
|---|---|---|---|
| T1 | Worker reads the API token and drives the daemon as the human | Token is at `~/.config/darkfactory/token`, 0600, readable by any process you run | Per-session capability (M2) **and** `jail` (M1); until then it is labelled |
| T2 | Worker kills or hijacks another worktree's session | `DELETE /sessions/{id}` accepts any id | Capability bound to session + worktree; cross-scope denied (M2, M3) |
| T3 | Worker creates worktrees anywhere on disk, or adds `/` as a repo | `POST /repos` takes an absolute path, validated only as a directory | Human-only verb; path allow-list; no repo add from agents (M3, M5) |
| T4 | Worker exfiltrates `.env`, `~/.ssh`, other repos | Agents run as you, with your filesystem | `jail` (M1) hides `~/.ssh`, other repos and the daemon token; `strict` (M3) for untrusted repos |
| T5 | Prompt injection in a repo file tells the overseer to approve a destructive action | n/a | Typed proposals, policy engine decides, untrusted content cannot grant capability (M4, M8) |
| T6 | Overseer is compromised or wrong, and stops the fleet mid-work | n/a | Budgets, no self-approval, panic only from the human plane (M9) |
| T7 | A malicious repo's git hooks run on worktree create | `git worktree add` runs hooks | Guarded git invocation for automated paths; explicit "run hooks" consent (M7) |
| T8 | Remote/SSH peer drives the local runtime | `--listen` accepts any address silently | Loopback default; non-loopback refused without `--allow-remote` (M0) |
| T9 | Nobody can tell what happened afterwards | No audit trail | Hash-chained ledger, agents cannot write it through the API (M6) |
| T10 | Repo content or agent output emits a terminal escape (OSC 52 clipboard, OSC 8 link, title) and acts on the human | Raw `<pre>` today; xterm later | Emulator escape allowlist; titles are never identity (SS-13) |
| T11 | XSS or a malicious vendored script reads the token from `localStorage` and drives the daemon | Token in `localStorage` | `open` → `HttpOnly` cookie, CSP, no token in JS (GV-17) |
| T12 | `/state` returns a preset's API key to a `view` device | Presets serialized whole | Env values never serialized (GV-16) |

Note what is *not* on the list: protecting you from your own shell. If a worker
can run arbitrary commands as you, it can read your files. Only uid/container
isolation changes that, and it costs ergonomics. We should be honest about which
mode we are in rather than implying safety we do not have.

---

## 4. Design: three planes, one decision point

```text
  HUMAN PLANE            CONTROL PLANE                 AGENT PLANE
  browser (cookie)  ─┐   ┌──────────────────────┐   ┌───────────────────┐
  human CLI (token) ─┼──►│ principal resolver   │◄──│ worker capabilities│
                     │   │ policy engine        │   │ (per session)     │
                     │   │ approval queue       │   └───────────────────┘
                     │   │ audit ledger         │            ▲
                     │   └──────────┬───────────┘            │
                     │              │ proposals              │ observation
                     └──────────────┴──────────► overseer (LLM, no verbs)
```

The single most important rule, and the whole design in one line:

> **The overseer never holds a capability. It emits proposals. The policy
> engine holds the capabilities, and the human approves anything that matters.**

Everything below is machinery for that sentence.

### M1. Principal comes from transport, never from a parameter

Every request resolves to exactly one principal:

- `human` — browser session cookie, or the human CLI token.
- `overseer` — a session explicitly granted the role by a human action.
- `worker:<sessionID>` — a PTY process holding that session's capability.

Request fields like `sessionId`, `from`, or `caller` are *targets*, never
identity. (Orca's own rule: "Transport-proven identity, never request params,
controls durable ledger namespaces.")

### M2. Per-session capabilities instead of one global token

At spawn, the daemon mints a 32-byte capability bound to
`{sessionID, worktreeID, incarnationID}`, stores only its hash, and hands it to
the child on an inherited fd (`DARKFACTORY_FD`); an environment variable
(`DARKFACTORY_CAP`) is the documented fallback, because `/proc/<pid>/environ`
and a sibling process share your uid. Under `strict` the env is not readable by
another uid; under `jail` it still is, so the fd is the default and the env is
only a fallback.

Verbs available to a worker capability: `status`, `say`, `ask`, `done`, `log`
(own session only). Not "the API with a smaller token" — a deliberately tiny
surface. Everything else is `denied` with a machine-readable reason.

**The verbs are optional.** An agent that never calls them still completes on
its exit code; `exec` steps and plain PTYs need no integration. There is no
heartbeat, no injected preamble, and no protocol to read. The daemon exposes
`DARKFACTORY_BIN` and `DARKFACTORY_SOCKET` so an agent that *wants* `ask`/`done`
runs one command from the binary it already has.

Revoked at exit and rotated when the session incarnation changes (daemon restart
or a new process) — never on a viewer reattaching, which would break a running
agent at the moment a human looks at it. Never persisted in plaintext.

### M3. Verb allow-list per principal, default deny

| Verb | worker | overseer | human |
|---|---|---|---|
| read own session/log | ✅ | ✅ | ✅ |
| read fleet state | ❌ | ✅ (redacted) | ✅ |
| send message to own coordinator | ✅ | — | ✅ |
| dispatch a worker | ❌ | ✅ within budget | ✅ |
| read another worktree's files | ❌ | ❌ | ✅ |
| create/remove worktree, add repo | ❌ | ❌ | ✅ |
| stop a session | own only | own dispatch only | ✅ |
| change config / agents / token | ❌ | ❌ | ✅ |
| approve a proposal | ❌ | ❌ (no self-approval) | ✅ |

Cross-scope access is denied by construction, not by checking a string.

### M4. Proposals are data, decisions are policy

The overseer's only output shape:

```go
type Proposal struct {
    Kind      string   // "dispatch", "message", "retry", "stop", "escalate"
    Target    string   // session or worktree id
    Args      map[string]string
    Rationale string   // untrusted text, shown to the human
}
```

`policy.Evaluate(principal, proposal) → allow | deny | needs_human`. Auto-allow
is a short list, configurable, empty by default:

- message a worker, request status, retry a failed worker *in the same worktree
  with the same agent*, stop a worker the overseer itself dispatched.

Everything else queues an approval card. The card shows the exact effect, not a
summary: `stop session 3f2a (codex, worktree "login-fix", 14m idle)`.

No shell strings anywhere. Proposals are structs; the daemon builds argv.

### M5. The human channel is separate and unreachable from agents

Browser: `darkfactory open` turns a one-time fragment code into an `HttpOnly`
`SameSite=Strict` cookie (the same mechanism as pairing), plus a CSRF token and
a `Host`/`Origin` check. The master token never appears in a URL, never in
`localStorage`, and is not served by `/state`. The agent socket and the human
API are different listeners with different credentials; a worker capability
cannot be replayed against the human API.

Remote: `--listen` alone is not enough. `--allow-remote` must be explicit, logs
a warning, never widens on port conflict, and per-host agent-CLI control is
default **off** (Orca's `allowRemoteCliControl` migration sets absent hosts to
`false`; copy that). A plaintext non-loopback bind also needs
`--insecure-remote`; TLS or `tailscale serve` is the supported path.

### M6. Audit ledger agents cannot write through the API

Append-only, hash-chained:

```text
{seq, ts, principal, verb, target, args_digest, decision, approver, prev_hash}
```

Writer: the daemon only. Readers: human UI and CLI. If a rogue agent does
something through a legitimately held capability, the ledger still shows
`worker:3f2a said ...`, `overseer proposed ...`, `human approved ...`. Retain
the last N days, export JSON.

In this build retention exists (`ledgerRetentionDays`, default 30 days, `0`
keeps everything) and a prune appends a marker row naming the last dropped
sequence and hash instead of rewriting anything, so `audit --verify` resumes
from the marker. Export, redaction, `actor`/`initiator` and `approver` are still
open; the stored row is `{seq, at, principal, verb, target, decision, rule,
policy, outcome, status, prevHash, hash}`.

Honest limit: the chain is tamper-evident against the API, not against the uid.
A same-uid process can rewrite `state.db` and recompute the chain; `jail` or
`strict` is what makes the claim stronger. `audit --verify` recomputes the chain
and reports the first bad row; migrations append a `migration` event and never
rewrite a row, so a restore starts a new epoch rather than pretending the old
one survived.

### M7. Containment: the only real answer to "steal everything"

Containment is a property of the worktree, in one of three modes:

- **`off`** — same uid, labelled "not a sandbox" in `status`.
- **`jail`** (default on Linux) — a `systemd-run --user` transient scope with a
  projected per-worktree HOME: worktree `rw`, repo `ro`, `$HOME` unreadable
  except the projection, no `~/.ssh`, no `~/.config/darkfactory`, network on
  (agents need it), `NoNewPrivileges`, `PrivateTmp`, memory/tasks caps. This is
  what stops T1 without breaking `claude`/`codex`/`opencode`.
- **`strict`** — a distinct ephemeral uid, or a user namespace with
  `bwrap`/`landlock`, and no network. For repos you do not trust.

The daemon keeps its token and socket in a directory the agent's mode cannot
read, so even a path escape finds an `EACCES`. Git invocations on automated
paths use `-c core.hooksPath=/dev/null`; running a repo's hooks is a separate,
human-approved action (T7).

This is the difference between "we logged it" and "it could not".

#### What a "private computer" costs

OpenBot ships one container per Bot with its own Chromium, logins, and
workspace. Its own published measurements (`docs/deployment.md`):

| | Measured (one Bot, one page) | Floor | Comfortable |
|---|---|---|---|
| Memory | 409 MB idle · 498 MB @ 3 page loads · 548 MB @ snapshot | 2 GB | 4 GB |
| vCPU | 3–6% at rest, bursty | 1 | 2 |
| Disk | **1.4 GB image** | 4 GB | 8 GB with `/workspace` |

Their own footnote: each additional concurrent page is another ~100–200 MB, and
Playwright's guidance is ~1 GB per concurrent browser. Idle computers are
**suspended by a culler** (`COMPUTER_SANDBOX_IDLE_AFTER`, default 30m), which is
what keeps a fleet from costing the sum of its Bots.

Two things follow, and they matter more than the raw numbers:

1. **Almost all of the 1.4 GB is Chromium and its deps**, not the container.
   A terminal-only jail needs no image at all — the host rootfs is the image.
   Disk for containment is therefore near zero for us.
2. **RAM is per live browser, not per Bot.** With no managed browser, a session
   costs one agent process plus its scrollback; our 4 MiB ring caps the rest.

Tiers we can actually offer, cheapest first:

| Mode | Mechanism | Added disk | Added RAM | Stops |
|---|---|---|---|---|
| **off** | same uid | 0 | 0 | nothing — label it |
| **jail** (default) | `systemd-run --user` transient scope: `ProtectSystem=strict`, `ProtectHome=tmpfs` + a projected HOME, `ReadOnlyPaths=<repo>`, `ReadWritePaths=<worktree>,<home>,<tmp>`, `InaccessiblePaths=<config>,<state>`, `PrivateTmp`, `NoNewPrivileges`, `MemoryMax`, `TasksMax` | 0 | ~0 | reads outside the worktree and its HOME, other sessions, the daemon token, `~/.ssh`; **network stays on** because agents need it |
| **strict** | separate ephemeral uid or userns (`bwrap`/`unshare -Ur` + landlock) | 0 | ~2–5 MB | all of the above plus no network and no shared uid |
| **container** | rootless podman/docker per worktree | ~1.4 GB once, shared layers | +50–400 MB/bot | all of the above plus an image-defined toolchain |
| **microVM** | container + gVisor (`runsc`) or a microVM | same image | +100–300 MB | kernel-level escapes |

**Decision for DarkFactory: `jail` by default on Linux, `off` elsewhere with a
visible label, `strict` for repos you do not trust.** The jail keeps agent CLIs
working because WT-8 projects their own config into the worktree HOME; hiding
`$HOME` without that projection breaks every agent, which is why containment
used to look expensive. Copy the boundary and the culler from OpenBot, not the
container. The *jail is a property of the worktree*, created before the agent
starts and destroyed with it — never something the agent can ask for and never
something it can leave.

### M8. Prompt injection is the overseer's own attack surface

- Every observation is tagged `trust: system` or `trust: untrusted`
  (repo content, agent output, commit messages, test logs).
- Trust tags never influence the policy decision — the principal and verb do.
  Content cannot grant capability.
- The overseer's prompt is assembled from typed events, not raw files; anything
  untrusted is quoted as data with delimiters.
- A proposal whose rationale echoes untrusted content is flagged `needs_human`
  even for otherwise auto-allowed verbs.

### M9. Budgets and a panic button

Max concurrent workers, max nesting depth, max wall-clock per dispatch, max
worktrees per repo, rate limit on proposals. `panic` (stop every PTY) is a
human-plane verb only; after a panic the overseer cannot act without a human
re-enabling it.

### M10. Fail closed, and say when you do not know

Unknown state is `unknown`, never `idle` or `done`. No proof of exit means no
retry, no release, no cleanup. This is Orca's safety floor; adopt it literally.

### M11. One gateway, and the record comes before the effect

Every side effect — PTY input, file read, worktree create, session stop, git
call — goes through one function in the daemon, in this order:

1. resolve the target from daemon-held state (never from request arguments that
   the agent controls);
2. evaluate policy;
3. **write the ledger row for the decision**;
4. perform the effect only if the decision forwards;
5. write a second row if a forwarded effect fails; a crash between 4 and 5
   leaves the outcome `unknown`, which `audit --verify` reports.

Where the effect is a bbolt write, the decision row and the state change share
one transaction; where it is not (a PTY write, a git call), the decision is
durable before the effect and the outcome row follows. Two consequences worth
stating: refusal is a first-class outcome with a named rule, and a crash cannot
produce an unrecorded action.

In this build steps 1–5 exist for every authenticated mutation, with `decide`
holding the human-only gate and the policy file (GV-5/GV-8), and with the
decision and the effect in two transactions rather than one. `POST
/sessions/{id}/input` is recorded like any other verb, one row per request and
no digest yet, which is what the retention window exists to bound.

### M12. Handover, not an approval queue

Approval is a state machine, and while a human holds control the agent is
**refused, not queued**:

```text
agent --requestHelp--> waiting --take--> taken --release--> completed
                        |--cancel--> cancelled
                        |--TTL--> expired
                        +--interrupt--> interrupted (keeps page/state)
```

- In-flight work drains (bounded) before the human takes over, so the agent
  cannot "win the race" by finishing a partially-approved action.
- A queued approval invites a rogue agent to spam proposals until one is
  clicked. A refusal is not exploitable.
- A help request records *where* it was made (session, worktree, last command),
  so handing back resumes at the problem, not at a blank prompt.
- A request survives a daemon restart.

### M13. Named refusals, not prose

A refusal is a value, not a sentence:

```go
type Refusal struct {
    Code       Code   // stable: worktree_busy, policy_denied, unknown_state, ...
    Target     string // repo/worktree/session id
    Rule       string // which guard fired, as data
    Detail     string // one sentence, no secrets
    Remediable bool   // can the caller do something about it?
    Hint       string // "stop 2 sessions", "pass --force"
}
```

Five rules:

1. **Codes are an API contract.** Add, never rename. `worktree_busy` means the
   same thing in the CLI, the web UI, and a future agent.
2. **The rule travels as data**, so the ledger can quote it:
   `decision=deny rule=no-remove-with-live-sessions`.
3. **`Remediable` + `Hint` turn a UI button into a function of the value**, not a
   string match. Orca does this with prefixes and a comment begging future
   editors to keep the classifier and the hint together; we can do it with
   types.
4. **`Detail` is user-facing.** No secrets, no stack traces, no paths outside
   the worktree.
5. **A refusal is audited exactly like an action** — same row shape, `deny`,
   with the rule.

HTTP mapping: 404 unknown, 409 state conflict, 403 capability/policy, 422
invalid input, 429 budget. Never 500 for something the caller can fix.

Until the refusal envelope landed, `handleDeleteWorktree` answered a
`fmt.Errorf("worktree path already exists: %s")` with `http.Error(w, err.Error(),
409)`. The caller could not tell "already exists" from "git failed" without
parsing English. It now answers `worktree_remove_failed` with the git error in
`detail`, and the code is the stable branch point. That is the bug this
mechanism exists to prevent, and it costs ~50 lines.

#### What the guard costs

`guard()` is a pure Go function: principal lookup, switch on verb, scope check,
one append to the ledger. No model, no network, no shell. Nanoseconds to a few
microseconds; the only I/O is the decision record.

It runs on **verbs, not bytes**. Session input and stream output are not
individually audited — the same read/write split OpenBot uses to keep the trail
useful. There are about ten guarded verbs.

If we ever add Hermes-style "smart" approval (an auxiliary model scoring a
command), that is the only place tokens appear. Keep it advisory: it may escalate
to a human, never authorize.

---

## 5. Requirements

Phase numbers are the delivery order, not priority. S-numbers are for the
requirements pass.

**Phase 0 — hygiene (do with R1–R5, before any overseer)**

- **S0. One gateway.** Every side effect resolves, is decided, is recorded, then
  happens. No handler writes to a PTY or shells out around it. *Test: a
  side-effecting request with the ledger write forced to fail performs nothing;
  a crash after the effect leaves `unknown`, never a silent success.*
- **S1. No agent-usable global credential.** The human token is never readable
  by an agent process under `jail`/`strict`, and never accepted from the agent
  listener. *Test: a process in a jailed worktree cannot read the token or get a
  response the human API accepts; a worker capability against the human API
  returns 401.*
- **S2. Principal resolution is transport-derived.** No verb accepts identity
  from a request field. *Test: forging `sessionId`/`from` changes nothing.*
- **S3. Binding is explicit** (R5 restated): loopback default, `--allow-remote`
  required, warning logged, no wildcard fallback. Non-loopback plaintext
  additionally requires `--insecure-remote`.
- **S15. Browser auth without a pasted token.** `darkfactory open` → `HttpOnly`
  cookie; the token never enters `localStorage`, a URL, or `/state`. *Test:
  `document.cookie` is empty after `open`; a request with `?token=` is refused.*
- **S16. Secrets are write-only.** Preset env values never leave the daemon.
  *Test: a planted key is absent from `/state`, the log and the ledger.*
- **S17. Terminal output is untrusted.** The emulator ignores OSC 52/8 and
  title-setting. *Test: a fixture emitting those sequences changes no clipboard,
  no link and no title.*
- **S18. One writer per worktree.** A second session is refused or queued.
  *Test: two launches → one session, or one waits.*

**Phase 1 — observer (read-only overseer)**

- **S4. Typed event feed.** Session lifecycle, status transitions, exits with
  reason, worktree changes, approval outcomes. No raw file contents.
- **S5. Read-only fleet view** for the overseer, redacted: no transcripts, no
  env, no file contents by default.
- **S6. Audit ledger** with hash chain, **`actor` and `initiator`** (`person`,
  `deployment`, `routine`, `overseer`, `handoff`), a redaction policy, and a
  "nobody watching" filter. Export is capped, and is itself an event.
  *Test: a secret passed through a redacted field appears as a length, never a
  value; an export writes an `audit.exported` row.*

**Phase 2 — advisor (proposals + human approval)**

- **S7. Proposal schema + policy engine** with an empty auto-allow set.
- **S8. Handover, not a queue** (M12): `waiting / taken / expired /
  interrupted`, refuse-not-queue while a human holds control, bounded drain
  before handover, state survives restart.
- **S9. No self-approval, no escalation loop.** The overseer cannot approve,
  cannot grant itself the overseer role, cannot spawn an overseer.
- **S10. Injection handling**: trust tags on every observation; rationale
  echoing untrusted content forces human review.

**Phase 3 — bounded autonomy (after the approval log is boring)**

- **S11. Ship a default auto-allow list**, small and reversible: message,
  status, retry-in-place, stop-own-dispatch. It is **policy data**, not `if`
  statements, and ships with four fail-closed tests: empty policy permits
  nothing; a broken deny denies; a broken allow does not permit; a malformed
  policy refuses to start.

  In this build the file and all four properties exist (`internal/policy`,
  `darkfactory policy`), but the default list is the human surface rather than
  agent verbs: `message`/`status`/`done` arrive with the agent listener, and the
  human-only gate still refuses a worker before policy is consulted.
- **S12. Budgets** (M9) enforced in the policy engine — workers, wall-clock,
  worktrees — plus **delegation depth and fan-out taken from a signed envelope**
  the overseer cannot increment about itself, with a circuit breaker that flips
  to human-only on breach.

**Phase 4 — containment (for untrusted repos)**

- **S13. Strict mode**: per-agent uid or sandbox, `$HOME` unreadable, daemon
  socket unreachable, git hooks disabled by default.
- **S14. Honest label**: `darkfactory status` prints the containment mode per
  session, so nobody mistakes "logged" for "sandboxed".

---

## 6. Copy, change, refuse

| From Orca | Verdict |
|---|---|
| Run / Task / Dispatch / Delivery objects, FIFO + ack, replay-on-unacked | **Copy** — this is the right vocabulary |
| Heartbeat, `ask`/`reply`, `worker_done` with explicit outcome, escalation | **Copy** |
| Depth cap on nesting; a worker cannot bind its own or an ancestor Run | **Copy** |
| "Only positive proof of exit authorizes stop/retry/release" | **Copy, literally** |
| Caller identity resolved server-side from transport | **Copy, and go further**: per-session capabilities |
| Trusting `$SHELL -ilc` probe for env | **Copy** (from the PTY lessons) |
| One gateway: resolve → decide → **record** → act (OpenBot) | **Copy** — S0 |
| Policy as data with four fail-closed properties (OpenBot) | **Copy** — S11 |
| Audit rows carry `actor` **and** `initiator` (OpenBot) | **Copy** — S6 |
| Handover state machine, refuse-not-queue (OpenBot) | **Copy** — S8, M12 |
| Delegation depth and fan-out from a signed envelope (OpenBot) | **Copy** — S12 |
| One shared runtime token for every local process | **Change** — per-session capability |
| Overseer = ordinary agent with full CLI | **Change** — overseer has no verbs, only proposals |
| "Worktrees are not sandboxes" as documentation only | **Change** — `jail` by default on Linux, `strict` opt-in, and label `off` honestly |
| Remote CLI control by default | **Refuse** — default off per host |

---

## 7. Decisions

1. **Overseer authority** (resolved): advisory now; the auto-allow list is
   empty until the ledger is boring, then policy data, never `if` statements.
2. **Containment** (resolved): `jail` by default on Linux, `off` elsewhere with
   a visible label, `strict` for untrusted repos. Per-worktree HOME (WT-8) is
   what makes `jail` usable; it ships in the same milestone.
3. **First deliverable** (resolved): the M0 hardening (S1–S3, S15–S18) first,
   then the observer + ledger (S4–S6) in M2, on top of a jail that can refuse.
