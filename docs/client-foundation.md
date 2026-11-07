# Client foundation and implementation tasks

The goal is a usable phone client and an optional lightweight desktop window,
with the existing Go daemon owning all durable work. This plan follows the
2026-11-01 repository review. It is not evidence of Android usability or a
desktop performance improvement. This is a planning record, not the current
release scope: `HANDOFF.md` owns that. The aggregated multi-host fleet (C6) is
deferred; the app connects one host at a time.

## Decisions in force

- Keep the static daemon and detached PTY host. Clients use the authenticated
  HTTP API. Closing a client cannot stop either process or open the state DB.
- Keep the static browser UI as the local client, with the optional Wails v3
  shell in the separate `desktop/` module. Android is a separate native remote
  client. Do not introduce Electron or select Mirin for this work.
- Permit frontend builds, TypeScript, and components. Generated assets can be
  embedded in Go. Node or Bun need not run on the user's machine.
- Permit native Android work and selective Orca code adaptation. Retain notices
  and record revision, source paths, local changes, and migrated tests. Do not
  assume Orca's mobile RPC protocol is compatible with DarkFactory's API.
- Prioritize UI-24 through UI-26 before additional governance or storage work.
  Preserve the existing security and session ownership invariants.
- Follow the model and credit policy in the root `AGENTS.md` for every task.

## Ownership

| Location | Owns | Does not own |
|---|---|---|
| `internal/session` | PTYs, rings, detached host, attach protocol | UI rendering or cookies |
| `internal/httpapi` | Authentication, authorization, state and mutation routes | Native window lifecycle |
| `web/` | Current UI, terminal rendering, input, worktree-scoped OpenCode panel | PTY processes or service credentials |
| `desktop/` | Optional Wails window loading the same UI | Daemon startup/shutdown, database, token files, native service bindings |
| `tools/clientbench/` | Isolated synthetic workload and Linux process observations | Product acceptance or a performance verdict |
| `clients/terminal/` | Tested portable mouse-click encoding adapted from Orca | Viewport conversion, mode inference, event dispatch, production input wiring |
| `third_party/orca/` | Pinned source/test snapshots, license, and provenance | A standalone copy of Orca's runtime |
| Future Android client | Phone navigation, keyboard, gestures, authenticated API client | Local agent execution or a copied Orca backend |

The desktop client currently accepts an initial literal loopback HTTP URL,
enforces that origin for later navigation, refuses new windows in the native
WebKitGTK policy layer, and exposes no Go services. It does not implement a
native authentication bridge, cookie migration, installer, updater, tray, or
daemon autostart. These are explicit remaining work, not implied capabilities.

## Start with these tasks

The tasks are independently assignable where the dependency column permits.
Agents must inspect the working tree and preserve another agent's edits. Use
separate branches or worktrees for concurrent changes to the same files.
Start with C0 (the UI gate), then C3. C1/C2 are retained and must stay green;
C4 and C5 do not block reproducing today's bugs. C6 is the native unified fleet
target after the workspace and device-token contract is stable. C3 needs a machine with the
native GTK/WebKit SDK; `make desktop-preflight` names what is missing.

| Task | Depends on | Write scope | Required result |
|---|---|---|---|
| C0: UI gate and build identity | none | `web/`, `test/browser/gate.cjs`, `internal/httpapi/ui_test.go`, `Makefile`, docs | The served `app.js` is provably `web/app.js`; the footer names `UI_VERSION` and the daemon build; a desktop (1280×900) and phone (396×719) layout contract passes. A stale daemon or un-bumped `?v=` fails the gate. |
| C1: reproduce and repair terminal attach modes | none | `internal/session`, relevant HTTP stream code, terminal input in `web/app.js` | A real PTY enables mouse tracking once, repaints on SIGWINCH without re-enabling it, and receives correct clicks before and after reattach. Preserve application cursor keys and bracketed paste where applicable. Never replay an arbitrary raw ring tail or force guessed modes. |
| C2: phone session creation and OpenCode flow | none; coordinate app.js edits with C1 | session controls and OpenCode panel in `web/`, `test/android`, phone test docs | Tap the outer session-tab `+` to launch a preset, and separately create a chat through the OpenCode panel. Assert resulting IDs, send/receive a message, answer permission, stop, switch, and reconnect. Do not confuse the TUI's drawn `+` with the DOM control. |
| C3: verify Wails on Linux | native GTK/WebKit SDK | `desktop/`, comparison artifacts | Start with `make desktop-preflight`; build and open the real window. Verify auth without weakening the Secure cookie (device-key re-auth is the current path), streaming, navigation/new-window restrictions, daemon restart, and window close/reopen. Keep no native privileged methods. Compare the production UI and synthetic workload with the browser. |
| C4: frontend module boundary | C1/C2 stable; preserve their gates | `web/`, frontend tooling, embedding and CI | First extract terminal lifecycle/input and OpenCode client/view ownership from the monolith. Choose a component stack after tracing these boundaries. Preserve routes, IDs, focus modes, CSP, and phone controls. Check generated assets deterministically before embedding. |
| C5: Android client prototype | C2's acceptance procedure and stable auth contract | new client directory, provenance notes, device tests | Evaluate reusable Orca gesture, keyboard, and session UI components against a DarkFactory adapter. Show a full device workflow before selecting React Native, WebView, or another client. Keep the agent on the host. |
| C6: native unified fleet | C3/C5 stable; workspace and device-token contract | `desktop/`, new native client, `docs/native-client.md` | One host registry, per-host device token, direct TLS/Tailscale calls, host-scoped IDs, aggregated fleet. Bundle the client UI instead of loading it from each daemon. No native auth bypass, no token in localStorage/URLs/logs, and no app-layer E2EE claim for the browser. Start with two hosts and a down-host state. |

### Android client slice

`android/` contains the first native Android shell: Kotlin, native pairing, a
Keystore-backed device token and client key, the bundled `web/` tree served from
a per-launch random loopback origin, P-256 ECDH app-layer E2EE with a pinned
daemon host key, encrypted streaming proxy frames, navigation lockdown, and
return-to-pair on `401`/logout/host-key change. The daemon refuses its browser UI
to remote peers, so the remote trust boundary is the APK, not the served
JavaScript. A debug APK was built and tested on the `dfphone` AVD: fingerprint
confirmation, relaunch without re-pairing, encrypted PTY stream plus input,
tab-switch reconnect, logout/revocation, changed-host-key refusal, and a
Keystore-backed multi-host registry with a picker, host switching, and a
down-host state. A later run added the native OpenCode chat round trip and the
UI-24 rotation/background/session-switch checks. See
[`../android/README.md`](../android/README.md) and the
evidence.
This closes the C5 implementation shape, per-host device evidence, and the
pinned/E2EE gate. C6's parallel aggregated fleet (and the real-phone Tailscale
run) remain; the current app connects one host at a time.

## C0 and C3 starting points

C0 is the gate that makes every later client claim checkable. The static half
runs in `make check`: `internal/httpapi/ui_test.go` requires `UI_VERSION` to
match every `?v=` query, every element id `app.js` looks up to exist, and the
served assets to stay local. The browser half is `make ui-gate`
([gate.cjs](../test/browser/gate.cjs)): it signs in to an isolated daemon, opens
a real PTY at 1280×900 and 396×719, asserts the layout contract, and compares
the SHA-256 of the served `/app.js` with `web/app.js`. The daemon returns its
`build` (version + commit) in `/state`, and the footer prints both halves, so a
stale binary or cached client is visible instead of mysterious. The gate and
its limits are in [ui-gate.md](ui-gate.md).

C3 started with the existing `desktop/` shell. `make desktop-preflight` checks
GTK4, WebKitGTK 6.0, libsoup 3, and the GLib/GIO modules Wails needs, and prints
the Debian/Ubuntu package names without installing anything. The SDK is present
on this host and the native window builds and opens. Relaunch uses device-key
auth (the Secure `__Host-` cookie is not weakened). The six native checks in
[desktop/README.md](../desktop/README.md) are all recorded on this host; the
evidence is in
`test/artifacts/client-foundation/c3-wails/native-checks/`. This closes native
Linux acceptance, not a packaged installer or another platform.

## C1 evidence and starting points

`web/app.js` calls `resetTerminal()` when opening a session. The server's
`internal/session/attach.go` suppresses TUI history and signals a redraw. A
redraw may paint content without repeating startup mouse modes. In Chromium,
the vendored xterm emits SGR press/release bytes from a touch tap when modes
are enabled, and emits none after reset plus paint-only output. This narrows
the earlier claim that touch clicks were entirely unsupported.

That mechanism is now diagnosed and repaired: the suppressed ring tail also
held the one-time DECSET enables, so a fresh xterm had every input mode off.
`internal/session/modes.go` tracks the last private-mode decision and
`SubscribeBrowser` restores it after the replay. Regression:
`TestBrowserAttachRestoresInputModes` (real PTY) and
`test/browser/probe.sh attach`. Device evidence and the remaining limits are in
[client-validation.md](client-validation.md). The outer `session-tab-add`
control was reproduced separately: it does open a worktree-scoped dialog, and
it is now pinned to the visible edge of the scrolling tab strip.

Useful Orca files in the sibling checkout:

- `mobile/src/terminal/document/surface-tap.ts`
- `mobile/src/terminal/document/tap-dispatch.ts`
- `mobile/src/terminal/document/mouse-input-encoding.ts`
- `mobile/src/terminal/document/mouse-click-drag.ts`
- `mobile/src/terminal/terminal-mouse-authority.test.ts`

The initial pinned source/test packet and license are preserved in
`third_party/orca/`. The pure encoder adaptation in `clients/terminal/` has six
local tests, but is not wired into browser input. See its provenance notes
before extending it. Migrate viewport and mode tests alongside the next port.

## Product acceptance gates

1. Start an isolated daemon and host with temporary XDG directories. Add a test
   repo and workspace. Never use the operator's active agents as test fixtures.
2. On Android, open that workspace, tap the session `+`, choose OpenCode, and
   assert the created DarkFactory session ID and worktree ID. If using the API
   panel, record its separate OpenCode chat ID. Show errors in the UI.
3. Verify TUI clicks and scroll, text entry, soft-keyboard open/close, navigation,
   and the newest message. Record a screenshot or video and input evidence.
4. Switch to another session and back. Background and foreground the app.
   Disconnect and reconnect. Assert the same live session ID and host PTY
   identity. Lost or replaced PTYs must be reported honestly.
5. For the chat panel, send a prompt and observe the reply, answer a permission
   request, and interrupt. Use an explicitly configured test provider for a
   real agent round trip. Mock responses prove client wiring only.
6. For Wails, close the window and reopen it. The independent daemon and PTY
   host must remain alive. Verify auth on the actual OS WebView without
   weakening cookie, origin, or token rules.

`make check`, `desktop-check`, nonblank screenshots, debug log messages, and
successful HTTP calls each prove a narrower property. None substitutes for
these gates. Native SDK availability and actual device access must be reported.

## Foundation status

The foundation status: the repository has a separate Wails evaluation module, a
UI gate with build identity, a shared synthetic terminal workload, a Linux
process-tree observation runner, pinned Orca source and tests, a portable
mouse-click encoder, and revised client requirements. C0 is implemented and
passing; C3 native acceptance has been exercised on the WebKitGTK 6.0 SDK,
including device-key relaunch, terminal streaming/resize/input, tab identity,
close/reopen and daemon restart, and native navigation/new-window refusal. C1's attach-mode defect is repaired and has both a Go regression
and an Android-device reproduction; C2's session-tab `+` creates a live PTY on
the device. The chat half of C2 now has device evidence for the full round trip:
New chat in a worktree, a prompt and reply, a permission request answered with
Allow once, and an interrupted turn, against an isolated OpenCode service and a
free test model (see [client-validation.md](client-validation.md)). The
terminal's own phone cases were driven on the same AVD afterwards: soft keyboard
open/close on a focused pane, compose-row entry into a bash session, touch
scrollback and TUI wheel bytes, the ↑/↓/⇣ controls, and detach/reconnect after a
daemon restart with the same session id and PTY pid. Those cover UI-24's listed
phone behaviours — open a workspace, create a chat and a PTY session, interact,
switch sessions, return from background, and reconnect — with recorded device
evidence. Still open: the parallel multi-host native fleet (host switching is
done), real-phone Tailscale HTTPS, a packaged desktop distribution, and
browser/Wails performance selection.

Chat switching now has its own headless probe:
[`test/browser/probe.sh opencode`](../test/browser/probe.sh) seeds 16 chats and
clicks/picks through all of them, asserting that the message pane changes with
the selection. It found that a switch to an empty chat kept the previous
transcript (and that child sessions showed their parent's), and that OpenCode
v2.0.26 rejects the daemon's old command-location query. Both are fixed; the
probe fails on either regression. The 16-chat picker was also driven on the AVD
by tapping the native popup: it scrolls, a row tap switches, and an empty chat
clears the pane there too.

Follow [client-comparison.md](client-comparison.md) to collect evidence. Add
results and limitations to [client-validation.md](client-validation.md).
