# DarkFactory — design

The current release scope is in `HANDOFF.md`. The native client checks that its
pairing fingerprint hashes the received host key. The daemon durably records
encrypted request IDs before dispatch for the full timestamp acceptance window.
These controls require a trustworthy initial fingerprint comparison and do not
replace a protocol security review. See requirements DP-19 and DP-20, and
[docs/release-checklist.md](docs/release-checklist.md) for the gates and
commands that evidence them.

## Decision

The planned explicit upgrade flow and its security and recovery gates are in
[docs/upgrade-foundation.md](docs/upgrade-foundation.md). It does not change the
current no-auto-update or no-unsolicited-network policy.

Build a lean Go-native workspace manager for agent work, with detached sessions
and private client access. The current release scope and deferred work are in
`HANDOFF.md`. Later sections retain historical design decisions and targets;
they do not expand the current release scope.

- Language: Go 1.27
- Repo: `github.com/Wintoplay/darkfactory`
- Interface: responsive browser UI on loopback; the native Android client
  bundles the same UI; frontend build tools are permitted
- Remote: localhost by default; the daemon refuses its browser UI to remote
  peers and leaves `/api/v1/...` for the native client; Tailscale/SSH transport;
  `serve --idle-exit` closes the listener after a quiet period while PTYs
  survive
- Agents: launched as processes in PTYs or one-shot headless — never implemented
  here
- Persistence: one daemon, bbolt metadata, bounded scrollback
- Storage (target): hot SSD by default; optional cold (HDD) for blobs; optional
  tmpfs scratch
- Mobile: the Android client covers the phone workflow; workspace access
  (terminal and chats together) is one full-scope grant, read-only is the other
  choice, and the host chooses the scope when pairing
- Scope: a lean daily driver for worktree-based agent work; runs and governance
  are target layers, not current release scope

### Where it sits

| Layer | Owner |
|---|---|
| Agent loop, memory, skills, channels | the tenant (Hermes, Claude, Codex, …) |
| **Workspace: worktrees, PTYs, restore, fleet** | **DarkFactory** |
| **Process: runs, plans, steps, checkpoints, gates** | **DarkFactory** |
| **Governance: principals, policy, audit, approvals** | **DarkFactory** |
| Model/provider/auth | the tenant |

### The client is replaceable

The Go daemon remains independent of the client. The optional Wails desktop
evaluation lives in `desktop/`, a separate Go module with its own dependencies
and native build requirements. It loads the same daemon-served UI and owns no
PTYs, database, or authentication authority. The static daemon release does not
depend on Wails, cgo, Node, or Bun. No shell has been selected on performance
evidence yet. See [the client foundation](docs/client-foundation.md) and
[the comparison procedure](docs/client-comparison.md).

Frontend tooling is a development choice. Compiled assets can be embedded in
the daemon without requiring a JavaScript runtime on the user's machine. The
current `web/` implementation remains plain JavaScript; a component migration
is a separate task. Native Android development and selective reuse of Orca code
are permitted. A smaller dependency count cannot excuse a broken phone flow.

The browser is the current client, not the contract. The PTY host, session
identity, attach policy, auth and pairing rules must survive a WASM, desktop,
native, or SSH client. See
[substrate lessons](docs/substrate-lessons.md) for the portability contract and
the tests that must be re-run.

### Objects, not bots

There is no `Bot` object. A **workspace** is the in-place row whose path is the
repo path, with no branch and no isolation. Every repo gets one, so the normal
fleet is repo → workspace → sessions. For a git repo the workspace is the
checkout, which is how a session reuses the checkout's files and agent history.
A **worktree** is an optional linked git worktree with its own branch, projected
`HOME` and containment mode. A **session** is one PTY process inside an existing
workspace or worktree — a shell or an agent preset. A **run** is the accountable
unit of work over that location. The session API never creates a worktree; the
browser defaults a new session to the workspace and creates a worktree only when
the user explicitly chooses **New isolated worktree**. A worktree outlives any
number of sessions.

Agent memory, skills, channels and model access stay with the tenant's CLI.
DarkFactory owns the workspace and the process, not the bot: it does not invent
a persistent agent identity, a container, or a browser. The closest thing to a
bot identity is the per-worktree `HOME`, which is why containment attaches to
the worktree rather than to a session.

### Memory is the tenant's

DarkFactory does not store semantic memory or assemble prompts. The per-worktree
`HOME` (WT-8) is the agent's memory location: `CLAUDE.md`, `AGENTS.md`, session
resume, whatever the CLI already uses. The daemon persists *history* — runs,
plans, steps, checkpoints, the ledger — but never retrieves it into a prompt.

The reason is not purity. Persistent memory is a durable prompt-injection
surface: a poisoned repo file becomes a "fact" the next run trusts. It also goes
stale, and it needs retention, redaction and deletion rules that a second store
would duplicate. The trigger to revisit is a concrete workflow where agents lose
context across sessions and users work around it by hand; until then, a file in
the worktree is the right store. If it ever lands, it is per-worktree,
human-readable, opt-in, and never auto-injected.

The process layer is the lesson from [Fabro](docs/fabro-lessons.md): take the
run/stage/checkpoint model, refuse the agent loop. See
[docs/feature-leverage.md](docs/feature-leverage.md) for the build/launch/copy
rules this follows.

## Non-goals

- An agent loop, model calls, prompt management, memory or skills
- A workflow DSL (DOT/Graphviz); v1 runs are a flat ordered step list
- Model routing, provider fallback, token accounting for model calls
- Electron as the default desktop runtime; optional Wails evaluation is in scope
- Hosting PTYs or daemon state inside desktop or phone clients
- A hosted relay, cloud account, or sync
- Vendor transcript scraping; the existing OpenCode API panel remains supported
- Persisting full transcripts or terminal scrollback by default
- A browser per agent; browser automation
- GitHub/Jira/Linear integrations
- Telemetry, auto-update, vendor push, or unsolicited outbound calls (a
  user-configured notification webhook remains a deferred target)
- Windows/ConPTY until the Linux and macOS paths are solid

Agent CLIs may themselves be Node-based. DarkFactory removes Node/Electron from
the host, not from the agents it launches.

## Architecture

```text
Browser (plain JS, vendored terminal emulator)
    │ HTTP + streaming
    ▼
Go binary ───────────────────────────────────────────────
├── config + token
├── bbolt store (repos, worktrees, sessions, runs, plans, policy, schema)
├── storage classes (hot / cold / ephemeral, content-addressed blobs)
├── git worktree manager              ── workspace plane
├── PTY session manager (host, ring, reattach)
├── run coordinator                   ── process plane
│   ├── plan store (content-addressed, daemon-owned)
│   ├── step executor (command / exec / pty)
│   └── checkpoint writer (darkfactory/run/<id>)
├── gateway (resolve→decide→record→act) ── governance plane
├── policy + ledger + refusals
├── overseer (proposals only, later)
├── device sessions (pairing, scopes, revoke)
├── HTTP API + agent capability listener
└── embedded web assets
        │
        ▼
   shell / agent process(es)
```

`darkfactory serve` owns the HTTP server, bbolt store and run coordinator.
`darkfactory session-host` owns the PTYs and their bounded rings. The daemon
attaches to the host over a `0600` Unix socket; a daemon restart or upgrade
detaches and reattaches without stopping the PTYs. The host is started on
demand with `Setsid`, and `session-host --stop` stops every hosted session
(SS-7). The API and UI see the same session IDs either way.

The process plane is a **coordinator, not a harness**: it never calls a model,
never owns a prompt, and never parses agent output to decide state. It knows
only steps, exit codes, checkpoints and gates.

Governance is a choke point, not a framework: one `guard()` in front of every
mutating verb, a policy table, and an append-only ledger. See
[docs/agent-authority.md](docs/agent-authority.md).

The policy table is a file, not code: `$XDG_CONFIG_HOME/darkfactory/policy.json`,
with `{version: 1, rules: [{effect, principal, verb, rule}]}`. `principal` and
`verb` are globs over the principal label (`human`, `human:token`,
`human:device:<id>`, and later `worker`/`overseer`) and the audit verb; `rule`
is the name a refusal and a ledger row quote. Evaluation is deny-first, no match
is a deny, and the failure modes are all closed: an empty file permits nothing,
a pattern that cannot compile denies when it is a deny rule and does not permit
when it is an allow rule, and a file that does not parse stops the daemon
instead of being ignored. `darkfactory policy` prints the file in force, its
hash and the default; `serve --policy` points at another file. With no file the
built-in default keeps the human surface at `*` and nothing else.

The hardline floor (GV-13) is not written: no route wipes the worktree root or
force-pushes the base branch, so there is nothing to floor yet, and policy
cannot grant what the human-only gate already refuses.

## Runs, plans and roadblocks

This section is target design. The current release runs a flat ordered step list
with `command`, `exec` and `pty` steps and a human answer; checkpoints, gates,
replans and failure budgets are not implemented. The requirement IDs and their
status are in [requirements](docs/requirements.md) §2.10.

A **run** is `goal + worktree + plan version`, executed as ordered **steps**.
It is the unit of accountability: the ledger gets a subject, the overseer gets
something structured to supervise, and "unattended" gets a definition. The
normative version is RN-1..RN-12 in [docs/requirements.md](docs/requirements.md)
§2.10.

### Plans are versions, not files

```text
v1 ──────► v2 ──────► v3        content-addressed, immutable
            ▲
   step 5 hits "the endpoint doesn't exist"
   steps 1–4 ran under v1; step 5 onward under v2 — forever answerable
```

- A plan **version** is immutable and content-addressed (`plans/<sha256>.json`).
- Each step records the hash that governed it; `Run.planHash` is the current
  pointer. A replan moves the pointer; it never rewrites a version.
- Plans live in daemon state, **not in the worktree**. A worker that can edit
  its own orders is not governed by them.
- Replanning is a proposal `{baseVersion, newPlan, reason}`; the policy decides
  auto vs human gate by diff class, the diff is recorded, and `maxReplans`
  bounds adaptation.
- A plan changes **only at a step boundary**. Mid-step the options are steer
  (if the protocol allows), wait, or cancel.

"Immutable" protects history and authority, not present intent. Most
roadblocks never touch the plan:

| Roadblock | Response | Plan changes? |
|---|---|---|
| Tests, lint, type errors | in-step fix loop, bounded by attempts | no |
| Wrong path, missing dep, bad assumption | agent adapts in-step, or the step retries | no |
| Approach wrong / scope changed | replan → new version at the next step | yes |
| Credentials missing, service down, decision needed | step → `blocked`, run pauses, escalate | no |
| Forbidden action | policy refuses, typed refusal, run fails | no |

Plan content is written at the level of **goals and acceptance criteria**, not
keystrokes. A plan that scripts every action turns every surprise into a
replan; the step owns the method, the plan owns the destination.

### Step kinds

| Kind | Runs | Completion signal | Notes |
|---|---|---|---|
| `command` | argv in the worktree | exit code | deterministic work: tests, lint, build |
| `exec` | an agent preset, one-shot, headless (`claude -p`, `codex exec`, `opencode run`) | exit code | the cheap middle: no loop ownership, no TUI parsing |
| `pty` | an agent preset in a PTY | a human, or a protocol (OSC 7501 / ACP) | interactive work; visible and steerable |

There is **no `api` step**. If DarkFactory ever calls a model itself, it has
become a different product; that is a deliberate boundary, not a gap.

### Gates and checkpoints

- A **human gate** is a step in `waiting` with a fixed choice set. The answer is
  a named transition (`approve`, `revise`, `abort`), recorded in the ledger —
  not a boolean and not free text.
- A **goal gate** is a required step (usually `verify`). The run cannot be
  `succeeded` if it did not pass, however execution routed.
- After each completed step the coordinator writes a checkpoint commit as a
  **side ref** — `refs/darkfactory/run/<id>/step/<n>` — with `DarkFactory-Run` /
  `DarkFactory-Step` trailers, using `git commit-tree` + `update-ref`. HEAD, the
  branch and the index never move under the agent, so a checkpoint cannot
  switch what the agent is editing. Review and resume use plain `git`.
- `blocked` is a **state**, not an outcome. A blocked run is alive, visible and
  answerable; it is never reported as failed to make the board look tidy.
- Steps are append-only. A resume creates `step@2` linked to `step@1` through
  `resumedFrom`; the original record is never edited.

## Storage and wear

This section is target design, not current behaviour: storage classes, blobs,
quotas and write accounting (ST-1..ST-9) are not implemented. The current build
keeps the state DB, worktrees and plans on the hot path and has no cold or
ephemeral class.

Agent work is write-heavy, and not uniformly: terminal output is megabytes a
day, while package installs and build trees are gigabytes. Write less first,
then tier by pattern.

| Class | Holds | Rule |
|---|---|---|
| `hot` (SSD) | state.db, worktree files and `.git`, plans, ledger | required; random small writes |
| `cold` (HDD, optional) | artifacts, archived output, exports, backups | sequential blobs only |
| `ephemeral` (tmpfs, optional) | agent `TMPDIR`, build scratch | lossy; never the only copy |

- **Hot-only**: the DB, worktree git data, plan versions and ledger are refused
  on cold or ephemeral (`storage_class_invalid`). Random page writes and fsync
  on a spinning disk are how you get a slow, corrupt daemon.
- **Content-addressed blobs**: the ledger references `{hash, size, class}`, so
  moving the cold volume breaks no reference.
- **Honest degradation**: a missing or full cold volume marks the blob
  `unavailable`; the run continues and the UI says so. Never a silent drop,
  never a blocked agent.
- **Bounded by default**: 4 MiB ring, no full transcript, artifact caps,
  checkpoint exclude-globs, `gc.auto=0`, batched ledger transactions.
- **Accounting**: `status --json` reports bytes written per class in 24 h, the
  top writers, and fsync count.

Caveats worth repeating: tmpfs can swap, so it is not a wear guarantee; a
spinning disk must never hold the DB or a worktree. The strongest reason for
cold storage is capacity and backup, not wear.

## State

```text
~/.config/darkfactory/config.json
~/.config/darkfactory/token
~/.config/darkfactory/instance.key                  not in backups or exports
~/.local/state/darkfactory/state.db
~/.local/state/darkfactory/plans/<sha256>.json      immutable plan versions
<COLD>/objects/<sha256>                             optional cold blobs (HDD)
<EPHEMERAL>/                                        optional tmpfs scratch
~/.local/share/darkfactory/worktrees/<repoName>/<name>-<shortid>/
~/.local/share/darkfactory/homes/<worktreeId>/      per-worktree HOME (M1)
```

Models:

```text
Repo     { id, name, path, git, createdAt }
Worktree { id, repoId, path, branch, baseRef, workspace, containment, createdAt }
Session  { id, worktreeId, kind, agent, command, args, title, status,
           cols, rows, exitCode, endedAt, incarnationId, capabilityHash,
           createdAt }
Plan     { hash, steps, createdAt }         // hash = canonical JSON
Run      { id, worktreeId, goal, planHash, state, outcome,
           startedAt, endedAt }
Step     { id, runId, ordinal, kind, spec, planHash, state, attempt,
           exitCode, checkpointSha, resumedFrom, startedAt, endedAt }
Device   { id, name, platform, scope, tokenHash, createdAt, lastSeenAt,
           lastIP, revokedAt }
Blob     { hash, size, class, media, createdAt, lastRefAt }
Event    { seq, actor, initiator, verb, target, decision, rule,
           policyVersion, prevHash, createdAt }
```

Run states: `draft / running / waiting / blocked / succeeded / failed /
cancelled`. Step states: `pending / running / waiting / blocked / succeeded /
failed / skipped`. Terminal runs and steps are immutable.

Session output is a bounded ring (4 MiB default) plus, later, a persisted tail.
Metadata is durable. The schema carries a version; a newer database is refused
rather than half-read (OP-5).

## API

```text
GET    /healthz
GET    /api/v1/state
POST   /api/v1/open/code              bearer, mints one-time fragment URL
POST   /api/v1/open                   code + loopback + X-DF-Request
POST   /api/v1/open/logout            clears the browser cookie
POST   /api/v1/repos
DELETE /api/v1/repos/{id}
POST   /api/v1/worktrees
POST   /api/v1/worktrees/order        fleet order of one repo's rows
POST   /api/v1/worktrees/{id}/reveal  show the folder in the daemon host's file manager
DELETE /api/v1/worktrees/{id}
POST   /api/v1/sessions
POST   /api/v1/sessions/order         row order inside one workspace
POST   /api/v1/sessions/{id}/pin      keep a session at the top of its workspace
DELETE /api/v1/sessions/{id}
GET    /api/v1/sessions/{id}/stream
POST   /api/v1/sessions/{id}/input
POST   /api/v1/sessions/{id}/resize
POST   /api/v1/sessions/{id}/redraw
GET    /api/v1/opencode/sessions
POST   /api/v1/opencode/sessions
GET    /api/v1/opencode/sessions/{id}/messages
POST   /api/v1/opencode/sessions/{id}/prompt
POST   /api/v1/opencode/sessions/{id}/interrupt
GET    /api/v1/opencode/sessions/{id}/permissions
POST   /api/v1/opencode/sessions/{id}/permissions/{requestID}/reply
GET    /api/v1/opencode/events        SSE proxy, volatile
POST   /api/v1/runs
GET    /api/v1/runs/{id}
POST   /api/v1/runs/{id}/start
POST   /api/v1/runs/{id}/cancel
GET    /api/v1/runs/{id}/timeline
POST   /api/v1/runs/{id}/steps/{ordinal}/answer
POST   /api/v1/runs/{id}/replan         target; not routed yet
POST   /api/v1/pair/code                authenticated, mints a pairing code
POST   /api/v1/pair                     code-gated, unauthenticated
GET    /api/v1/e2ee/host-key            public identity: signed bundle, v1+v2
POST   /api/v1/e2ee                     sealed native transport (v1 or v2 suite)
POST   /api/v1/device-keys              register a device public key
POST   /api/v1/device-keys/challenge    device-key bootstrap
POST   /api/v1/device-keys/verify       device-key bootstrap
POST   /api/v1/open                     code-gated, host browser only
GET    /api/v1/devices
DELETE /api/v1/devices/{id}
POST   /api/v1/devices/revoke-all
GET    /api/v1/audit             decision ledger, chain recomputed
POST   /api/v1/confirm                  host-only, one-time confirmation
GET    /api/v1/events            ledger, filtered
POST   /api/v1/approvals/{id}    later
```

Every authenticated mutating route goes through the gateway. The two
bootstrap routes, `POST /open` and `POST /pair`, are exempt because their
authority is a one-time code, not a resolved principal. The order (record
before act) is enforced: the decision row is durable before the effect and the
outcome row follows it, so a crash leaves a decision with no outcome rather
than a silent success. `GET /api/v1/audit` reads the chain and recomputes it.
Before either, the human-only gate refuses a worker or an overseer, and then
the policy file decides (§ Governance, `internal/policy`): deny before allow, no
match means no. Every allow row carries the policy hash that judged it.
Every failure answers with the same refusal envelope
(`code`, `rule`, `target`, `detail`, `remediable`, `hint`) and a code from one
registry, so a client never parses prose.

The agent listener is a UNIX
socket at `$XDG_RUNTIME_DIR/darkfactory/agent.sock` (mode `0600`, directory
`0700`), checks `SO_PEERCRED`, and accepts a per-session capability. The human
API never accepts an agent capability. The five worker verbs are optional: an
agent that never calls them still completes on its exit code.

`/replan` accepts a proposal, not an edit: it returns `202` with a decision
(auto-applied or pending approval) and never moves the pointer mid-step.

A local Linux workspace-scope user can open a live session in a separate
Ghostty 1.3.1 window (UI-29). The audited launch passes the existing caller
credential through an inherited anonymous descriptor, then the attach client
uses the same authenticated stream, input, resize and redraw routes as the
browser. A new attach takes over the single terminal view; closing its window
detaches without stopping the session host. The client filters output-driven
OSC strings, image/device-control strings and window operations, while retaining
colour queries and DECRQSS. Ghostty starts with clipboard reads and image
protocols disabled, auto-detected URL links disabled, the context menu selected,
and `mouse-shift-capture=never` so Shift always extends the terminal selection
even when a TUI enables mouse reporting. A filtered, bounded OSC 52 *write* is
forwarded to Ghostty with `clipboard-write=ask`, so a TUI copy shows the text in
Ghostty's "Authorize Clipboard Access" dialog; reads stay inert. This is a
version-specific configuration and narrow filter, not completion of SS-13's
fixed escape allowlist or a security certification. The right-click menu was
observed in a real window and the per-copy clipboard authorization design was
accepted by the owner; real-agent streaming and the native Wails window remain
acceptance checks.

The stream endpoint returns a bounded, newline-trimmed tail for shell sessions,
or no history plus `X-DF-Redraw: 1` for a full-screen TUI, followed by live
bytes. One browser terminal owns a session at a time: a new attach detaches the
previous stream with a notice, because the PTY has one size and two viewports
cannot both own it. xterm.js 6.0.0 has no native touch scrolling, so a vertical
swipe is translated to a synthetic wheel event dispatched directly on the xterm
screen layer; that reaches xterm's wheel pipeline, which scrolls shell
scrollback or forwards the TUI's wheel/arrow equivalent. The client loads the
official WebGL2 renderer on Chromium/Blink and non-Linux WebKit, falling back
to the DOM renderer when the GPU context is unavailable, lost, or software.
WebKitGTK (the Wails shell on Linux) keeps the DOM renderer: its masked GL path
was slower and erratic in the synthetic trial, and parse, not paint, is its
bottleneck. A fractional devicePixelRatio also keeps the DOM renderer because
the WebGL cell box rounds and clips glyphs. The ↑/↓ buttons send
PageUp/PageDown and ⇣ sends End for the app's "jump to latest" action. The shell
is pinned to `visualViewport` height and offset, so the iPad keyboard lifts the
terminal rather than covering it. Output is coalesced per frame, except that a
chunk of 8 KiB or less is written to the emulator immediately so interactive
echo does not wait for a vsync. Input leaves in the microtask checkpoint after
the key event and one input request is in flight at a time, so order is
preserved without a fixed hold before the first key. The sequence-numbered
`snapshot + seq` protocol is still M2 (SS-4); the browser terminal is xterm.js
today.

OpenCode is not run through that terminal. It is a chat application with its own
session, message, scroll, mouse and keymap model; attaching its TUI to xterm and
synthesizing PageUp/PageDown/End is a guess, not an integration. DarkFactory
serves a native OpenCode panel backed by the local service at
`opencodeAPIURL` (default `http://127.0.0.1:26706`). Both upstream `opencode`
and the Rust rewrite `ocd` register their live endpoint and password in
`$XDG_STATE_HOME/opencode/service.json` (with `service-v2.json` and the older
`service-local-install.json` as fallbacks); the daemon probes the newest
loopback registration and uses the first that answers, so a service restart on
a new port does not leave the panel pointing at a dead one. An explicit
non-default `opencodeAPIURL` is the owner's override and skips discovery. The
daemon reads the password from `opencodePasswordFile` (default
`~/.config/opencode/service-local-install.json`) when a registration carries
none, authenticates with HTTP Basic on loopback, and proxies only the session
list, message list, prompt, interrupt, permission and create-chat routes. The password and API URL never reach the
browser or `/state`. New chats must name a DarkFactory worktree; the daemon
resolves its path, so the browser cannot ask OpenCode to open an arbitrary
directory. Message history loads newest-first and follows OpenCode's cursor for
**Load older**; the cursor already carries its order, so the proxy must not send
`order` alongside it. Pending permission requests render as cards and reply with
`once`, `always`, or `reject`. `GET /api/v1/opencode/events` forwards OpenCode's
SSE stream, but the stream is explicitly volatile: the browser debounces it as a
hint and keeps a 15 s fallback poll. `opencodeURL` remains available in `/state`
for an external PWA or another client, but the panel does not depend on it.
xterm remains the client for plain shells and other PTYs. Close the OpenCode TUI
before using the panel so one session does not have two active clients.

The panel is a two-pane list and chat on a desk and one chat column on a phone.
It also has an opt-in chat layout (`?chat=1`, remembered per tab) that hides the
fleet chrome so a phone can read the conversation without the tree, topbar or
bottom nav: the same DOM, the same routes, and never the default. The terminal's
own Focus mode is a separate mechanism and is untouched by it.

`POST /repos` classifies the path, refuses a duplicate resolved directory, and
creates one in-place workspace at that path for every repo, and startup repairs
a legacy repo that has none. For a git repo this is the checkout; linked
worktrees are additional rows. `POST /worktrees` with `{"workspace":true}` is
the manual repair path; a second workspace is `409`. Removing a repo cascades
its workspace record and sessions but refuses while git worktrees exist; the
directory is never deleted.

## Security

Short version; the full model is in
[docs/agent-authority.md](docs/agent-authority.md).

- Bind `127.0.0.1:8787` by default. Any other bind requires `--allow-remote`,
  and a non-loopback plaintext bind additionally requires `--insecure-remote`.
  A non-loopback listener classifies each TCP peer as loopback, private
  (RFC1918/ULA/link-local/on-link), tailnet (`100.64.0.0/10`,
  `fd7a:115c:a1e0::/48`) or public, and refuses a public peer before routing
  (`403 source_denied`) unless `allowPublicSources`/`--allow-public` is set.
  Forwarding headers are ignored for that decision; a local proxy is trusted
  only because it is the operator's TLS gateway. CSP and `nosniff` are always
  on; no CORS. `style-src` stays nonce-gated, but
  `style-src-attr 'unsafe-inline'` is required because xterm writes truecolor
  values as style attributes; inline scripts and authored style attributes stay
  forbidden. `Host`/`Origin` allowlists land with the cookie (`open`,
  GV-17/DP-9): a bearer token cannot be replayed by a rebinding page, a cookie
  can.
- Browser: `darkfactory open` asks the daemon for a one-time fragment URL and
  opens it. The page posts the fragment to `/api/v1/open` and receives an
  `HttpOnly`, `SameSite=Strict` cookie. The code is single-use, TTL 60 s, and
  the exchange is loopback-only with `X-DF-Request`. Cookie mutations require
  that header plus a matching `Host`/`Origin`; there is no CORS. The cookie hash
  and expiry are persisted in bbolt, so a daemon restart does not log the tab
  out; logout deletes the row. The master token is for the CLI and scripts; it
  is never in a URL, never in `localStorage`, and never served by `/state`. The
  paste fallback keeps the bearer token in `sessionStorage` for that tab only;
  Disconnect calls `/open/logout` and clears it.
- The add-repo picker uses an authenticated, read-only directory listing: names
  and paths only, no file contents, capped at 400 entries. It grants no authority
  beyond the bearer token that already permits `POST /repos`.
- Human token: 32 bytes, mode `0600`, constant-time compare.
- Agent presets keep secrets out of the state API: `/state` returns env **keys**,
  never values. Secrets live in a separate `0600` file and are never serialized.
- Agents get per-session capabilities over an inherited fd (env as documented
  fallback), never the human token. The five verbs are optional: no heartbeat,
  no injected preamble, no protocol the agent must read.
- Paired devices hold their own cookie and a device record, never the master
  token; the current build supports `full` and `read` scopes, and revocation
  takes effect on the next request. The gateway stamps a caller `Local` from
  the transport (master bearer, or a cookie/session through the host's loopback
  UI) and gives `pair.code`, `device.revoke` and `devices.revoke_all` the
  `host-credential` tier: a remote paired device, even `full`, gets `403
  host_admin` for them. Device-key registration is not in that tier so a
  native client on another machine can still complete its first login with the
  master token. The target `admin` scope, second-station confirmation, and
  pairing/device ledger events are still open.
- Destructive verbs need a second station; `admin` is host-only by default.
- Containment is a property of the worktree, chosen before the agent starts:
  `off` (same uid, real HOME, labelled; the compatibility default), `jail`
  (opt-in when `bwrap` can create a user namespace: per-worktree HOME, daemon config/state
  masked, worktree writable, network allowed because agents need it), `strict`
  (separate uid, no network, untrusted repos; not implemented). `jail` still
  runs as the user's uid and does not hide every file under `$HOME`; the UI and
  `status` say so. An agent preset may override the mode (for example
  `opencode` can stay `off` while another agent is jailed). `status` prints the
  mode per session. A jail may opt into `jailScrubEnv` (minimal environment),
  `jailHideCredentials` (built-in credential/browser/keyring masks),
  `jailEnvAllow` and `jailPathAllow` (explicit grants), and `jailHiddenPaths`
  (extra masks). Those still leave the session same-uid and network-allowed;
  they narrow data exposure, not the principal. `strict` remains the separate
  principal/no-network target.
- Terminal output is untrusted: the browser emulator allows a fixed escape set
  and ignores OSC 52 (clipboard), OSC 8 (links) and title-setting. PTY titles
  are never identity. The client also drops output-driven palette and colour
  sets (OSC 4/10/11/12 and the 104/110/111/112 resets) before the bytes reach
  the emulator, because repainting the theme hides text and impersonates another
  agent; colour queries are still answered. The native Ghostty window forwards
  an OSC 52 write only to Ghostty's authorization dialog and keeps reads inert
  (see the native terminal section).
- Plans live in daemon state; a worker cannot rewrite its own orders. Plan
  hashes are canonical, and a replan carries `expectedPlanHash`.
- Replans that change targets, scope or destructive potential require a human.
- Default deny on every mutating verb; typed refusals with stable codes.
- Every decision is recorded before the effect, in the same transaction as the
  state change where possible; a crash leaves `unknown`, never an unrecorded act.
- The ledger is tamper-evident against the API, not against the uid.
  `audit --verify` recomputes the chain; `jail`/`strict` make the claim stronger.
  Migrations append an event and never rewrite a row.
- Multiple sessions per worktree are allowed; each has its own PTY and tab.
  There is no writer lock in this build, so concurrent agents can race the same
  git index. Use separate worktrees for parallel agents that touch git. A plain
  workspace has no git index, branch, or rollback: agents edit the real directory.
- Git hooks are off for automated calls (`core.hooksPath=/dev/null`); running a
  repo's hooks is an explicit, human-approved action.
- No telemetry, no vendor push, no auto-update. Notifications are local: the
  daemon calls `notify-send`, and the paired Android app polls the daemon's
  attention feed in a foreground service. An optional user-configured webhook
  remains a target (MO-7), not a current outbound call.

### Friction budget

Security that costs the agent a protocol, or the user a ceremony, gets turned
off in practice. So the defaults are the cheap path: the agent receives its
capability on an inherited fd it never has to ask for, the five verbs are
optional, `jail` projects the agent's own config instead of hiding it, and the
browser needs one command (`open`) instead of a token to paste. A control that
requires a new habit has to justify the habit.

## Mobile and devices

This release ships the native Android client and the loopback browser. The PWA,
tablet key bar and `snapshot + seq` reconnect below are target-state; see
MO-1..MO-11 in [requirements](docs/requirements.md). Local notifications are in
the current build (MO-10): the daemon's attention feed drives `notify-send` on
the desktop and an Android foreground service. Web Push for a PWA and an
optional webhook are not implemented (MO-7).

The PTYs live on the session host, which is what makes mobile work: neither the
browser nor the daemon has to stay alive. iOS suspends JavaScript and kills
sockets on background, so reconnect is the primary feature, not an error path.

- **Transport**: `tailscale serve --bg 8787` (HTTPS, tailnet-only, MagicDNS) or
  an SSH tunnel. Never `--listen 0.0.0.0`. The native client is the remote UI;
  a remote browser is refused.
- **Phone = control plane**: fleet, run timeline, blocked badge, approve/deny,
  stop, one-line input. A full TUI on a 390 px screen is a demo, not a tool;
  the terminal is opt-in.
- **Tablet / DeX = terminal**: xterm, accessory key bar (Esc, Tab, Ctrl,
  arrows, PgUp/PgDn), `visualViewport` keyboard handling, `ResizeObserver` for
  Split View and Stage Manager. The session view owns the remaining height;
  focus mode hides the fleet chrome without pinning a fixed pane, and the font
  size is a local preference, not server state.
- **Reconnect** with `snapshot + seq` (SS-4); a pane is never blanked.
- **PWA** (deferred): installable, standalone, safe-area insets. No service
  worker caching of the terminal UI — a stale shell is worse than a reload.
- **Notifications** (current): no vendor push service. The daemon keeps a
  100-event attention ring (`session exited/lost`, `run blocked/waiting`,
  `OpenCode permission`) and serves it at `GET /api/v1/notifications`. A
  `desktopNotify` daemon (default on) calls `notify-send`; the Android client
  polls the same feed in a `dataSync` foreground service and posts local
  notifications with a per-host cursor. A new client asks without `after` to
  baseline, then polls `after=<id>`; the Android service persists the cursor so
  a restart does not replay the ring. Web Push and a user-configured webhook
  (ntfy, etc.) remain targets; if added, the webhook is the only permitted
  outbound call.

### Pairing

**Current build.** `Pair phone` calls `POST /api/v1/pair/code` and shows a
40-bit code with a 120 s TTL, a `#pair=` link, and a locally rendered QR. The
host chooses the device label and `full` or `read` scope; both travel with the
grant, not with the phone's request. The link uses `publicURL` from
`config.json` when set (for example a Tailscale Serve origin), otherwise the
request origin; a configured `publicURL` host is allowed automatically.
`POST /api/v1/pair` is code-gated, requires `X-DF-Request` and a matching
`Host`/`Origin`, refuses non-loopback plain HTTP (including forwarded plain
requests), and is limited to 5 attempts/min per source and 30/min globally.
The host chooses whether the device has **workspace** access or is read-only.
Workspace is one capability: terminal attach/input and OpenCode writes are not
separable, because an OpenCode turn can already run workspace actions. A
read-only device is refused `POST /sessions`, `/stream`, `/input`, `/resize`,
and every mutation except logout and a redraw, while state, runs and chat
transcripts stay readable. Each pairing creates a device record and its own
hashed cookie row;
`GET /api/v1/devices` lists labels, scopes, workspace, last-seen and expiry, and
`DELETE /api/v1/devices/{id}` or `POST /api/v1/devices/revoke-all` revokes. A
`read` cookie is refused on mutations except logout and a PTY redraw (SIGWINCH
only; resize and input stay blocked). The target `admin` scope, second-station
confirmation, and pairing/device ledger events are still target-state.

The target below keeps the tailnet as the network gate, the one-time code as
the application gate, and a device-specific scoped credential instead of the
master token or the browser cookie.

```text
host$ darkfactory pair --name "iPad"
      https://df.tailnet.ts.net/pair#k=7Q2M9XKD41   [QR]   120 s
device: POST /pair { code } → Device + __Host-df_session
host$  paired: iPad (control)
```

- Code in the URL **fragment** (never sent to the server), hashed at rest,
  single use, five attempts. `/pair` is rate-limited globally and per source and
  returns a uniform `401`: wrong, expired and unknown are indistinguishable.
- Device name comes from the host (`--name`), never from the device.
- `__Host-` cookie: `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`, no
  `Domain`; stored as `SHA-256(instanceSecret ‖ token)`, with the secret in a
  `0600` file beside the token, so a restore elsewhere fails closed. The flags
  are unconditional: loopback is a trustworthy origin, so a `Secure` cookie is
  accepted over `http://127.0.0.1:8787` and there is no unprefixed fallback.
- Scopes: `read` (metadata and chat transcripts; no PTY or chat writes) and
  `workspace` (full control: PTY, OpenCode, files and git); `admin` stays
  host-only unless granted. Workspace is not separable into terminal and chat.
- Destructive verbs need a **second station**: a host-shown code confirmed on
  the device (DP-5). Typing into a live PTY counts as destructive.
- Revoke per device / all; idle expiry; `panic` revokes all and rotates the
  instance secret.
- Every pairing and device action is a ledger event with a device principal.

The honest limit, printed by `status`: a same-uid agent can read the token and
pair a device, and revocation cannot erase bytes the device already rendered.
Pairing is network-safe, not process-safe; only containment (`jail` / `strict`)
changes that.

## Build and supply chain

- `CGO_ENABLED=0`, `-trimpath`, static binary
- `go mod vendor`; offline builds
- Direct dependencies: `creack/pty`, `bbolt` (budget: 3)
- No npm at build or runtime; xterm.js 6.0.0 + addon-fit 0.11.0 +
  addon-webgl 0.19.0 are vendored with license, checksum, and a restricted
  link/window/clipboard surface
- Reproducible release artifacts with a published checksum: `make dist` builds
  `darkfactory_linux_{amd64,arm64}.tar.gz` and `darkfactory_<version>_{amd64,arm64}.deb`
  plus `SHA256SUMS`, CI runs it from a tag and checks a second build for
  byte equality, and `install.sh` verifies the checksum before writing anything.
  Byte equality no longer depends on the umask: tar is given an explicit
  `--mode` and the `.deb` staging tree is chmod-normalized. A cross-umask
  rebuild was verified identical after fixing the package C finding in
  `test/artifacts/release-2026-11-02/REPORT.md`.
- Install paths are the user's: `.deb` → `/usr/bin` + user unit + app menu,
  tarball → wherever you extract it, `install.sh` → `~/.local/bin` with
  `--desktop` adding the unit and icon; `darkfactory launch` is the desktop
  entry's `Exec`, so the icon starts the daemon when it is not already running

## Milestones (historical planning)

This is the older roadmap summary, kept for traceability. It is not the release
plan and does not expand current scope; [HANDOFF.md](HANDOFF.md) owns the
release and [requirements.md](docs/requirements.md) §4 holds the milestone table.
Requirement detail and acceptance criteria are in
[docs/requirements.md](docs/requirements.md). Summary:

1. **M0 Foundation** — repos, worktrees, sessions, launch, loopback guard, CSP,
   `/state` redaction, multiple sessions per worktree with tabs, safe stop.
2. **M1 Daily driver** — env probe, idempotent launch, per-worktree HOME, jail
   containment, `open` + cookie auth (`Host`/`Origin`), cascade delete, reaper,
   resize, typed refusals, gateway seam, real terminal (escape allowlist), fleet
   view, and runs with `command`/`exec`/`pty` steps.
3. **M2 Defensible** — capabilities, policy, ledger, handover, restore, restart
   suite, plan versions, checkpoints, gates, replan, storage tiering, installable
   PWA, paired devices.
4. **M3 Overseer** — proposals, budgets, depth, approval cards, strict
   containment, webhook notifications, WebAuthn step-up.
5. **M4 Autonomy** — learned allow-lists (proposals only), protocol adapters
   (OSC 7501, MCP, ACP).

Enforcement arrives in M1, not M2. M2 records what M1 can already refuse; it does
not claim to contain a same-uid agent.

## Known limitations

- The ledger is append-only and hash-chained, and pruned on a retention window
  (`ledgerRetentionDays`, default 30; `0` keeps everything) that leaves a marker
  row stating the last dropped sequence and hash, so `audit --verify` still says
  something after a prune. It does not export or redact a field yet, and
  `session.input` writes a row per keystroke, which is why the window exists.
  Pairing and device actions are not in it — they are the two code-gated
  bootstrap routes. A decision and its effect are two transactions, not one: a
  crash between them is recorded as `unknown`, never replayed.
- Policy decides for the human surface only, because a worker or an overseer
  cannot reach the listener yet; the rules for those principals are written but
  unreachable. Budgets, delegation depth and the hardline floor are not
  implemented, and the policy file is not watched: a change needs a restart.
- Runs are a flat ordered step list; no fan-out or DAG until a real workflow
  needs it. `command` and one-shot `exec` steps run to an exit code; `pty` steps
  wait for a human answer. Checkpoints, gates, replans and retries are not in
  this build.
- No `api` step and no model routing; an agent brings its own model.
- Replanning is only possible at step boundaries.
- Terminal attach uses a bounded shell tail or a SIGWINCH redraw for a TUI, not
  `snapshot + seq`; reconnects can still duplicate bytes until SS-4 lands.
  Within one process the backlog and the live stream join atomically — the
  snapshot and the registration share the writer's lock — so a fresh attach
  neither drops nor repeats the chunk at the seam.
- Attach restores the tracked input modes, not the whole terminal state. Cursor
  visibility, the alternate screen and insert mode are not replayed. The host
  tracks modes from the PTY and carries them on attach, so ring eviction no
  longer loses an enable; the tracker still never guesses a mode the app did
  not enable.
- Two browser tabs cannot share one PTY screen at different sizes; the newest
  attach takes over and the older is detached. Full-screen TUIs own their
  scrollback; PageUp/PageDown is a request to the app, not an xterm buffer.
- xterm.js parses the full escape set; OSC 8 links, window manipulation and the
  clipboard addon are disabled, but the fixed allowlist (SS-13) is still open.
- The original PTY survives a `serve` restart because it lives in the detached
  session host. If the host itself is gone, a session that was live is restarted
  under the same ID with a new PTY and its bounded transcript; if that fails it
  is `lost`. A non-live row is still a tombstone: Restart starts a new PTY in
  the same workspace with the same agent and title, and that path does not
  preserve output. If `serve` is running when the host dies, it marks those
  sessions `lost` and does not respawn the host until the next `serve` start.
- Linux first; macOS next; Windows out of scope for now.
- API has no TLS of its own; remote access is Tailscale/SSH.
- `off` is the compatibility default: same uid, real HOME, labelled. `jail` is
  opt-in and still allows network; `strict` (separate uid, no network) is not
  implemented.
- The phone UI is a control plane; a full TUI is tablet/DeX only.
- Storage tiering is target design: `tmpfs` can swap, so it is not an SSD-wear
  guarantee.
- Pairing is network-safe, not same-uid-safe; `jail`/`strict` change that.
- No vendor push; notifications are local or a webhook you configure.
- A checkpoint is a side ref; nothing is pushed to a remote. Checkpoints and
  gates are target-state and are not in this build.
- E2EE replay records start with the first request after the upgrade: a request
  accepted by an older daemon was never recorded and cannot be rejected later.
  The native envelope has two suites: v1 (P-256 ECDH) and v2 (X25519 +
  ML-KEM-768, host policy `--e2ee-min-suite`). The daemon and the Android client
  implement both; the phone verifies the signed bundle and pins
  `fingerprintV2`, and a v2 row never falls back. The post-quantum primitives
  use pinned Bouncy Castle lightweight APIs with a JVM↔Go interop gate and a
  disposable-daemon run. The minimum defaults to 2. Legacy APKs require an
  explicit compatibility value of 1, which permits weaker pairing. First-pair
  fingerprint confirmation precedes transmission of the encrypted pairing code.
  Completed v2 responses carry an authenticated end marker. These checks do not
  replace an external cryptographic review. v2 does not add forward
  secrecy against host-key theft, and the host identity is still plaintext in
  `state.db`.
  The daemon-side store and httptest restart tests, the JVM fingerprint
  substitution check, the PQ interop gate and the live envelope run are focused
  evidence. The 2026-11-02 internal review's
  confirmed defects (encoded nested envelope, unbounded replay sweep) are fixed
  and covered by tests; its hardening gaps are either fixed or recorded as
  accepted/deferred limits in the review's resolution table. This is still not
  an external security review.
- Cold storage accepts sequential blobs only; the DB and worktrees stay hot.
