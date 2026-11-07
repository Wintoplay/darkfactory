# DarkFactory agent instructions

## Model and cost

Use `gpt-6.1-terra` for Codex and every subagent, with medium reasoning by default.
Do not use Astra, another fallback, or a premium service tier unless the user
explicitly requests the tier. If Sol is unavailable, report it. Pass this
policy to subagents and keep small tasks in the primary agent. For opencode, use DeepSeek V4.1 flash.

## Start here

Read `HANDOFF.md` for release scope, current tasks, ownership, and acceptance
checks. Check code and evidence before claiming a feature works. Read the
relevant section of `docs/agent-reference.md` before changing authentication,
sessions, HTTP, browser behavior, persistence, or releases. Use `DESIGN.md` for
architecture and `docs/requirements.md` for permanent requirement IDs. Older
roadmaps describe possible features, not the current release scope.

The static, cgo-free Go daemon and detached session host own durable work.
`desktop/` is a separate module. Do not add a root `go.work`, import Wails into
the daemon, downgrade Go below 1.27, or require Node at daemon runtime. Vendor
root dependency changes with `go mod tidy` and `go mod vendor`.

## Invariants

- Daemon shutdown detaches from hosted PTYs. Preserve attach modes, redraw and
  resize ordering, first stream byte, and one terminal size owner. Do not replay
  arbitrary TUI ring tails. Call `Manager.UseHost` before HTTP reconciliation.
- Route authenticated mutations through `Server.mutate`: resolve, decide,
  record, then act. Recorder failure prevents the effect. `/open` and `/pair`
  are one-time-code exemptions. Use the refusal catalogue for API errors.
- Preserve cookie flags, credential hashing, scope enforcement, pairing checks,
  key pins, CSP, and DOM-only message rendering. Full scope grants workspace
  access; read scope cannot use PTYs or write OpenCode. Never weaken auth for a
  WebView or put credentials in URLs, logs, errors, or browser localStorage.
- Policy denies precede allows. Preserve the hash-chained ledger and its marked
  transactional retention prune. Refuse dirty worktree deletion before stopping
  sessions or runs. In-place workspaces are not isolated.
- Reuse a launch intent's idempotency key until success. Preserve worktree-scoped
  OpenCode chat, permission requests, keyboard layout, picker focus, and touch
  scrolling. Keep telemetry, CDN assets, auto-update, and unsolicited outbound
  calls out of the daemon.
- Preserve upstream revision, license notices, and behavior tests when adapting
  code. Use isolated temporary XDG roots for live tests. Never overwrite another
  agent's uncommitted work or touch the operator's active agents.

## Refresh binaries before presenting

Never demonstrate, measure, or claim behavior ("it works", "it is fast") from a
stale binary. Rebuild `bin/darkfactory` — and `bin/darkfactory-desktop` for
desktop claims — from the current tree immediately before presenting, then
state which build the running stack actually executes: binary mtime, the
version in `darkfactory status --json`, the embedded `UI_VERSION`, and
`/proc/<pid>/exe`. A fix that exists only in the tree proves nothing about
behavior, and neither does an operator stack running an older generation.
Installing or restarting the operator stack still needs the operator's
agreement; if the presented build is not yet deployed, say so plainly.

## Verify changes

Run the checks relevant to the change in `HANDOFF.md`. `make check` alone does
not prove Android usability, a native Wails window, or a performance win. For
`web/` changes bump `UI_VERSION` and the `?v=` queries together, run the UI gate,
and rebuild the daemon before judging its embedded UI. Do not install or restart
an operator service merely to run a test. Update the relevant requirement ID,
`DESIGN.md`, and `docs/substrate-lessons.md` when their contracts change.

## Terminal delivery and lag rules (2026-11-02 post-mortem)

The day the OpenCode terminal kept "not getting fixed" cost a full session; the
knowledge distilled here must survive. Full history and numbers live in
[`docs/reviews/terminal-lag-2026-11-02.md`](docs/reviews/terminal-lag-2026-11-02.md).

- The serving stack is three generations: desktop shell, `serve`, session host.
  A fix is not landed until all of them execute the tree's build. Before
  judging any behavior, check `darkfactory status --json` version, binary
  mtimes, and `/proc/<pid>/exe` on every serving process; drop windows whose
  exe is `(deleted)` — they are dead code and have been the false negative
  source before.
- Replace the pair as a unit: the stream batching lives in both the daemon and
  the host. A daemon-only restart attaches a new daemon to an old host, changes
  the symptom, and "still slow" is then true and yet the fix is fine. Swap the
  pair from one build and verify the version string after the restart.
- Deploy recipe: build with the Makefile's exact `-ldflags "-X main.version …
  -X main.commit …"` (a bare `go build` loses the identity), `darkfactory
  stop`, atomic-install with `mktemp`+`mv`, relaunch `serve`, then `status
  --json` must show the new commit. Killing the host kills its PTY sessions;
  state that cost plainly and get consent first.
- The desktop engine verdict: WebKitGTK + xterm.js DOM renderer is the
  bottleneck (parse ~6.5x Chromium, activation stalls). The in-app VTE pane is
  the fast desktop path today (~30 KiB/s streamed smooth, 5–9% shell CPU).
  Phone and browser on the identical transport are healthy — never propose a
  framework or backend migration before that A/B logic points there; Tauri
  embeds WebKitGTK on Linux, Neutralino is architecturally what we already
  run, Fyne/Gio mean writing a terminal emulator from scratch.
- Reporting a fix: acceptance is the real scenario (user's terminal, live
  opencode, physical keyboard, model streaming). Synthetic keydowns and
  disposable-daemon probes do not count and never have. Match the complaint
  layer first: typing lag, streaming lag, and during-tool-silence stalls are
  three different defects with different owners.
- Development instruments: `DF_STREAM_DEBUG=1` turns on per-client rate meters
  (`kind=daemon|pane|host`) and a `stream_ended_early` warning for the
  previously invisible subscriber-takeover path. A 1 Hz CPU capture of
  `darkfactory|WebKit|opencode|codex` processes from the day sits at
  `/tmp/user/1000/opencode/cpu-capture.sh`; regenerate it, do not trust it
  across reboots.
- Never leave a `serve` or console-instrument change deployed without a
  HANDOFF entry saying which generation is live; the next agent must be able
  to reconstruct the generation at a glance.
