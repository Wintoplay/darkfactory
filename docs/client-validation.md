# Client foundation validation

This file records executed checks and outstanding limitations for the client
foundation. It does not carry task completion state for later implementations.

## 2026-11-01 workspace access and on-demand listener

- Workspace access is now one capability. A full-scope device gets PTY and
  OpenCode writes; a read-only device gets neither. `terminal` is compatibility
  output derived from scope, so an existing full-scope `terminal:false` row is
  workspace on its next request.
- `go test ./internal/httpapi` covers full-scope stream admission, read-only
  refusal on create/stream/input/resize, read-only redraw and chat reading,
  and a paired bearer token before and after revoke.
- `POST /api/v1/pair` returns the same 256-bit credential as a bearer token and
  a stable `hostId`; the test proves `GET /state` with it and a `401` after
  `DELETE /devices/{id}`. The browser still uses the HttpOnly cookie; the token
  is not put in `localStorage` or a URL.
- `make check` passes. `make ui-gate` passes with UI `2026-11-01.11` at
  1280x900, 1200x800 and 396x719, and the served `app.js` hash matches
  `web/app.js`.
- A read-only 396x719 probe paired without terminal, showed **This device is
  read-only**, kept **Open chat**, and opened the worktree panel with one seeded
  chat. The chat-count chip now updates to `1 chat` after the list loads instead
  of staying at `0 chats`.
- An isolated `serve --idle-exit 1s` exited after 1 s with
  `darkfactory idle exit`; the detached session host remained. This is a
  listener idle test, not a PTY-survival replay.
- The old Android “no terminal access” evidence is retained as historical and
  marked superseded; the workspace change was not re-run on the physical AVD
  in this record.
- Network transport remains TLS/Tailscale. No application-layer E2EE is
  claimed for the daemon-served browser; the native multi-host target is in
  [native-client.md](native-client.md).

## 2026-11-01 baseline review

- The original root `make check` passed outside the restricted socket sandbox,
  including Go tests, race tests, and the JavaScript type check.
- A Chromium touch probe against the original UI emitted SGR press/release
  bytes with mouse tracking enabled. Reset followed by paint-only output left
  tracking at `none` and emitted no click bytes. This was a synthetic browser
  reproduction, not a real OpenCode or Android test.
- `adb devices` reported no attached Android device.
- The registry supplied Wails `v3.0.0`; this evaluation pins that release.
- The host has GTK4 metadata but lacks the required WebKit pkg-config metadata.
  Native compilation and a real Wails window require separate verification.

## Foundation checks

- `make check build` passed. The expanded gate includes the existing Go suite,
  race detector and UI check, the comparison tool syntax checks, Orca snapshot
  hash verification, six mouse encoder tests, and desktop URL tests.
- The desktop URL tests also passed with `GOPROXY=off`, `GOSUMDB=off`, and an
  empty `GOMODCACHE`. They do not require downloading Wails or installing a
  native SDK. The root daemon module, checksums, and vendor tree are unchanged.
- The isolated headless Chromium smoke passed with the output sentinel,
  1,049,600 workload bytes, 30 echo observations, no workload errors, and no
  unreadable PSS samples in this run. Raw evidence is in
  `browser-smoke/result.json`.
  It uses the installed Playwright package and Chromium override recorded in
  the artifact. This is one tool smoke, not a comparative benchmark.
- A deliberately failing `/bin/false` client was rejected with exit 1 and
  verdict `incomplete`, with error `client exited before completion: 1`.
- `make desktop-build` failed in Wails' native dependencies because
  `webkitgtk-6.0` and `libsoup-3.0` pkg-config packages are unavailable. No real
  Wails window, cookie behavior, streaming, or native performance was verified.
- Six upstream Orca files were preserved from pinned git objects. The local
  encoder's six tests passed. The upstream Vitest/DOM tests were not run here,
  and the encoder has not been connected to production input.

No performance winner or completed phone workflow is claimed. Next work is C1
and C2 in [client-foundation.md](client-foundation.md), with C3 on a native-SDK
host. No native SDK or Android package was installed by this change.

## 2026-11-01 C1/C2 attach repair (with device evidence)

Environment: the existing `dfphone` AVD (Android 34, `google_apis/x86_64`,
1080×2340 at 440 dpi) with its installed Firefox for Android, driven by `adb`
over `adb reverse`. The daemon ran from `bin/darkfactory` with temporary
`XDG_CONFIG_HOME`/`XDG_STATE_HOME`, one git repo, one workspace, and a
deterministic fixture as `$SHELL`. The fixture enables mouse tracking
(`?1000`), SGR encoding (`?1006`), bracketed paste (`?2004`) and application
cursor keys (`?1`) once, paints a cursor-addressed screen, repaints on
`SIGWINCH` without repeating the enables, and records every byte it reads.
It is committed at [`test/browser/fixture-tui.py`](../test/browser/fixture-tui.py).

### C1: the reproduced failure

A TUI attach suppresses the ring tail (`X-DF-Redraw: 1`, first body chunk was
the 4-byte SGR-reset hello). The suppressed tail was also the only place the
one-time DECSET enables existed, and the client had just called
`terminal.reset()`, so xterm had every input mode off. A click on the freshly
painted pane reached no PTY: the input log stayed empty. The same happened
after switching tabs and back, and after a page reload. This is not a paint
bug — `screen … rows=11` and the repaint text were on screen the whole time.

Fix: `internal/session/modes.go` tracks the last private-mode decision in the
PTY output (`?1`, `?1000/1002/1003`, `?1005/1006/1015`, `?1004`, `?2004`) and
`SubscribeBrowser` appends the corresponding enable sequences after the replay.
Output modes (cursor visibility, alternate screen, insert mode) are deliberately
not restored. A sequence split across reads is carried; an enable the app turned
off again is not re-enabled. Remote sessions seed the tracker from the host
snapshot and then follow live frames.

Executed evidence:

- Go: `TestBrowserAttachRestoresInputModes` starts a real `/bin/sh` PTY with the
  same shape, asserts one `?1000h` in the output, a redraw attach with no raw
  tail, all four modes in the replay, a `SIGWINCH` repaint that does not
  re-emit them, and an identical restore on reattach.
- Negative control: with the restore line removed, the browser probe fails at
  the first click (`FAIL (3): … no SGR mouse report`); restored, it passes.
- Headless Chromium (396×719, DPR 2.727…): three clicks produced three SGR
  press/release pairs — first attach, after tab switch, after reload.
- On the AVD: a device tap produced `ESC[<0;14;3M` + `m`; after alpha → beta →
  alpha another pair; after a Firefox refresh and reopening the session another
  pair. The host PTY was the same session throughout.

### C2: the session-tab "+" and a connect race

The outer DOM `+` does launch a session: on the AVD it opened
`dlg-session` scoped to the checkout (`repo / repo — … · checkout`), and
**Start session** created `d0ccf825cf8e903016673753ea6966a6`, `live`, with the
same worktree id (`48db9c65b9a4f1c7d81332ce174cb961`). A tap on the new pane
encoded, and typed text reached the new PTY. Home → relaunch kept that session
id live (pid 107110) instead of creating another.

Two real defects were found while reproducing it:

1. With several sessions, the `+` sat past the last tab in a horizontally
   scrolled strip. The only `+` a user could see was the one the TUI draws inside
   the terminal — which is not the session control. `.session-tab-add` is now
   `position: sticky; right: 0` and `renderSessionTabs` scrolls the active tab
   into view. Negative control: with the sticky rule removed, the headless
   probe fails (`the session-tab add control is scrolled out of view: x=477`);
   with it, the AVD shows the pinned `+` at [973,471]-[1053,565] and taps it
   without scrolling.
2. A cold load starts an unauthenticated cookie probe. Pasting a token before
   that probe's `401` landed ran `lostAuth()`, which cleared the new token,
   hid the app and reported "connect failed: unauthorized". `api()` now only
   tears down when the request used the session that is still current, and
   `connect()` keeps only the newest attempt's outcome. The probe delays the
   unauthenticated `state` request by 400 ms to make the race deterministic;
   without the fix it never leaves the connect card.

New committed checks:

- `go test ./internal/session`: mode parser, split/grouped sequences, disable
  and output-mode cases, and the real-PTY attach regression.
- `test/browser/probe.sh attach|session`: the two browser regressions, with
  exit 2 for a setup gesture that did not happen and exit 3 for a client
  regression. Neither is in `make check`; both need Playwright and Chromium.

`make check` passed after the changes, including the race detector on
`internal/session` and `internal/httpapi`.

### Still open

- No OpenCode round trip, permission answer, or provider chat creation was
  exercised in this run: this daemon had no OpenCode provider configured. The
  chat half of C2 is covered separately below.
- The fixture is a stand-in TUI. It reproduces the attach/mode contract, not
  OpenCode's own drawing or its on-screen `+`.
- Keystroke input was proven by typing through the focused terminal; soft
  keyboard open/close, touch scrolling, and text entry in the compose row were
  not re-tested in this run. (The 2026-11-01 terminal phone section below
  covers them.)
- Wails, iOS, and Android Chrome were not exercised.

## 2026-11-01 C2 chat round trip (with device evidence)

Environment: the same `dfphone` AVD and Firefox for Android, on the same host.
The operator's live OpenCode service (`127.0.0.1:26706`) and its database were
never used. Instead:

- A second OpenCode v2.0.26 service ran on `127.0.0.1:26707` under its own
  temporary `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME` and state directory, with
  its own SQLite database and Basic-auth password. No operator credential was
  copied into it.
- Its model was the free hosted `opencode/beam-free` (cost 0), and its config set
  `permissions: [{action: "shell", resource: "*", effect: "ask"}]` so a
  permission card is part of the round trip.
- An isolated daemon from `bin/darkfactory` used temporary XDG directories, one
  git repo, and `opencodeAPIURL: http://127.0.0.1:26707` with the test service's
  password file. The repo's workspace worktree id was
  `7e0fb768899cbaf1c4e2b46dd94054a2` (repo `81cc217ba8bf62395da5579a5a100afa`).
- The phone reached it through `adb reverse tcp:8787 tcp:8787`, signed in by
  pasting that daemon's token, and opened
  `?debug=1&panel=0&chat=1`.

### Executed evidence

The committed probe is
[`test/android/probe-chat.sh`](../test/android/probe-chat.sh). One run exited 0
with `ok: New chat created ses_e6af4a9a9ffeciZwDrywEZ2Klw`, `ok: prompt sent,
permission answered, reply rendered`, and `ok: turn interrupted from the phone`.
The probe's stdout, the client log, and a trimmed message dump are preserved in
`test/artifacts/client-foundation/chat-round-trip/`.

What the taps drove, from the client log:

- **New chat** (panel `btn-oc-new`) opened `dlg-oc-new` scoped to `repo / repo`;
  **Start chat** posted `/api/v1/opencode/sessions` and the panel selected the new
  chat `ses_e6af4a9a9ffeciZwDrywEZ2Klw`. Its OpenCode location is that worktree.
  A separate manual run created `ses_e6aff5e99ffe273iHtKrrtNkL6` the same way.
- **Compose row**: tapping the textarea raised the soft keyboard
  (`mInputShown=true`) and the page re-reported its boxes (`prompt 25,992`
  instead of `25,1759`); typed text went to the prompt, **Send** posted
  `/prompt` and the reply rendered. No client defect surfaced here; one run's
  text arrived scrambled, which is the known `adb shell input text` artifact, not
  a client result.
- **Permission**: the prompt asked for a shell command, the policy asked, the
  card reported `oc allow box 54,403,201,73`, **Allow once** posted
  `/permissions/<id>/reply`, the tool completed, and the reply `df-ok` rendered.
  In the earlier manual run the same path produced `pong` for a text-only prompt.
- **Interrupt**: **Stop** opened the `Interrupt OpenCode?` confirmation, the
  confirm button posted `/interrupt` (200) and the turn's idle outcome became
  `interrupted`. A first attempt confirmed after the turn had already finished
  and correctly interrupted nothing; the probe now requires the Stop box, which
  only exists while a turn runs.
- **Touch scroll and jump**: two swipes in the message list reported
  `latest show top=483 max=574`; tapping **⇣ Latest** reported
  `latest jump … to=573 max=574` and hid the button.
- **Switch**: the native picker listed the eight chats in that directory; a row
  tap logged `oc select …` and `picker closed`.
- **Background/foreground and reload**: Home → relaunch kept the same chat and
  messages; reloading the tab (same tab, so `sessionStorage` survives) kept the
  session and reloaded the chat history from OpenCode.

### Harness defects this exposed

None of these are product regressions; all are in the phone harness or the
debug instrumentation it reads.

1. `df_open` used `org.mozilla.firefox/.App`, which does not exist in current
   Fenix builds, so the explicit intent silently fell through to Chrome; and the
   unquoted `&` in the query was read by the device shell, dropping `panel=0` and
   `chat=1`. It now launches `org.mozilla.firefox/org.mozilla.fenix.IntentReceiverActivity`
   with the URL quoted.
2. The page reports boxes from `getBoundingClientRect()`, which is relative to
   the layout viewport below Firefox's toolbar. `adb input tap` is screen
   relative, so every page y was short by 312 px on this AVD. `page-offset.py`
   measures that row and `df_page_tap` applies it; `probe-opencode.sh` now uses
   the same helpers.
3. Boxes were only reported when the panel re-rendered, so chat mode or the soft
   keyboard moved a control and left a stale box. `reportOpenCodeBoxes()` runs
   after layout changes and `visualViewport` resizes, and the confirm dialog now
   reports its primary button and logs `dialog confirm open`.
4. `probe-chat.sh` itself first closed over a Python syntax error and lost a
   non-zero exit status; both are fixed, and the probe now exits 3 on a failed
   assertion instead of 0.
5. `df_keyboard_shown` trusted `mInputShown=true`, which can outlive a dismissed
   IME. A Back sent on that false positive navigated Firefox away from the chat
   panel, and `probe-opencode.sh picker` then tapped the connection pill instead
   of the select. The helper now also requires that the focused IME client is
   Firefox, and the picker/latest flows no longer send Back at all: the page
   boxes already reflect the keyboard inset, and tapping the select handles it.

### Limits

- This is one model on one AVD. It proves the client/daemon/OpenCode round trip,
  not OpenCode's behaviour with another provider, a real project prompt, or
  streaming under a slow link.
- `probe-opencode.sh` was moved onto the shared helpers and its parsing checked
  offline, but its picker/latest run was not repeated after the move: the trial
  run hit the Back bug above, then a chat too short to scroll. The same picker
  switch, touch scroll and jump were driven manually with the same helpers during
  the chat runs.
- The probe needs a signed-in page and `DF_TOKEN`; it does not pair or type a
  token itself.
- Android Chrome, iOS/Safari, Wails, a genuinely remote phone, and performance
  comparison remain unverified.


## 2026-11-01 terminal phone cases (with device evidence)

Environment: the same `dfphone` AVD and Firefox for Android, on an isolated
daemon (temporary XDG directories, one git repo, one workspace). Three sessions
on one worktree: two fixture-TUI sessions (`alpha`, `beta`) that enable mouse
tracking and log every byte they read, and one real `bash -l` session (`gamma`)
where the compose row is not hidden by the TUI rule. The page was signed in with
a one-time `/open` link and then loaded at `?debug=1&panel=0`; no token was typed
and the operator's `127.0.0.1:26706` was not involved. Screenshots, log lines and
byte dumps are in
`terminal-phone/`.

What the taps and swipes drove:

- **Soft keyboard on a focused terminal**: a tap on the pane logged
  `viewport focusin xterm-helper-textarea` and the layout viewport shrank from
  396×719 to 396×438; Back while the keyboard was up restored 396×719 and the
  page stayed on the same attach. A TUI session hides the compose row by design;
  the nav and the pane stayed above the IME.
- **Compose row**: on the bash session, tapping the textarea raised the keyboard
  (`focusin TEXTAREA`, viewport 438) with the compose row still visible above
  it; `echo df-phone-ok` plus Enter posted `/input` (204) and the PTY echoed both
  the command and `df-phone-ok`.
- **Touch scrolling**: two finger-down drags on the bash session paged 200 lines
  of scrollback from 178..200 to 167..191; the pan did not raise the keyboard.
  On the fixture TUI, an upward pan delivered 20 `ESC[6~` (PageDown) bytes to the
  PTY, which the fixture records, and ⇣ on the shell returned to the prompt.
- **↑ / ↓ / ⇣ on a TUI**: the three controls delivered `ESC[5~`, `ESC[6~` and
  `ESC[F` (PageUp/PageDown/End), not arrow keys, to the PTY.
- **Detach and reconnect**: stopping and restarting the daemon ended the stream;
  the page reported `detached`, kept the scrollback, and the compose row said
  "Detached; tap the connection pill to take over". Tapping the pill attached the
  same session (`b28d7022…`) with the same PTY pid (165201) and `vp=180/180`.
  The daemon restart did not kill the PTY, and the client did not reattach on its
  own — one attached terminal owns a PTY's size, so it asks the user to take over.

### Defect this found

`renderSession()` derived the compose placeholder from `input.disabled`, which is
true both for a session with no live PTY and for a live session this tab is
detached from. A detached-but-running session therefore said "No live PTY; tap
Restart to start one" — inviting the user to replace a live PTY through Restart.
The placeholder now distinguishes the two: "Detached; tap the connection pill to
take over" for a live detached session, and the Restart hint only when the PTY is
really gone. Verified on the device in both states (detached screenshot and the
reconnected session).

### Limits

- These are manual, coordinate-driven cases, not a committed probe. The
  `dfphone` AVD's screenshot scaling makes taps from an image ~15% off; the
  executed coordinates are in the evidence file, and `test/android/lib.sh`
  documents the trap.
- Only Firefox for Android was exercised; Android Chrome and iOS/Safari
  keyboards are untested. The AVD uses the system IME, not a physical keyboard.
- The shell scrollback check is visual (screenshots); the TUI wheel check is
  byte-level through the fixture log.

### Android “This device has no terminal access” (2026-11-01)

> Historical: the separate terminal opt-in was superseded by the workspace
> capability later on 2026-11-01. Full scope now grants terminal and OpenCode
> writes together; read-only gets neither. Keep this section for the device
> evidence and the old failure mode, not as current policy.

The message is the DP-4 per-device terminal opt-in, not a broken PTY. On the
`dfphone` AVD, a pasted master token (terminal=true) and a pair code generated
with **Allow terminal** checked both attached the shell (`49x25`), streamed, and
accepted `echo ANDROID_OK` (input 204). A pair code with the default unchecked
(`scope=full`, `terminal=false`) showed the exact blocked panel, opened no
stream, and kept `/state` and the fleet usable. The live daemon currently lists
one paired `Phone` with `terminal=false`, which explains the report. The host
must re-pair with the checkbox (new code/link) and revoke the old row. Evidence:
`android-terminal-access/`.
Limits: emulator + Firefox 159 + `adb reverse`, not a remote phone or iOS.

## 2026-11-01 OpenCode chat switching with many chats

The earlier chat evidence switched between eight chats through the native
picker, but never checked that the *message pane* changed with the selection, and
never used the desktop list. A report that clicking chats "does not switch"
turned out to be real, and depended on the chat being empty.

The new probe is [`test/browser/probe.sh opencode`](../test/browser/probe.sh).
It starts a disposable OpenCode v2.0.26 service (`env -i`, own `HOME`/`XDG_*`,
own database and password — a caller's `OPENCODE_DB` must not leak in) and a
disposable daemon, seeds 16 chats in one worktree, gives three of them a user
marker, and runs Chromium at 1280×900 and at 396×719.

Executed evidence (`test/artifacts/client-foundation/opencode-switch/`):

- Every row is clicked forward and in reverse; each switch must change the
  title, the `.on` row, the picker value and the message pane, and the pane may
  contain only that chat's marker. Content → empty → empty and a 60 ms
  two-click switch cover the reset and race paths.
- The 396 px layout drives the native `<select>` the way the phone's picker
  does, with the same assertions, over five chats.
- Any `400` from `/api/v1/opencode/commands` fails the run. The pass line is
  `PASS opencode: 16 chats switched by click and by picker; empty chats clear
  the pane; commands 200`.

Two defects it found:

1. **An empty chat kept the previous transcript.** `renderOpenCodeMessages`
   skips when the message key equals the stored signature, and an empty list
   keys to `""` — which `selectOpenCodeSession` also used as its reset, so the
   skip ran and the old transcript stayed. The title and the selection moved,
   so the click looked like it worked. Child sessions (`parentID`) were affected
   the same way: a child showed its parent's messages. Fixed with a sentinel
   reset (`OPENCODE_MESSAGE_STALE`) in both places the selection moves.
2. **The command list was rejected on every switch.** The daemon proxied
   `GET /api/command?location=<path>`, which OpenCode v2.0.26 answers with
   `400 Expected object | undefined at ["location"]`; the phone's `/` completer
   was empty and every switch logged an error. The v2 shape is
   `?location[directory]=<path>`, now sent by the daemon and asserted by the Go
   test.

Negative controls: with the client reset reverted to `""` the probe fails with
`probe-02 did not render alone after the switch` and the previous chat's text;
with the daemon change reverted it fails with `the command list was rejected by
the daemon: 400 …`. Both were restored; `make check` passes.

**Device confirmation.** The native picker was driven on the dfphone AVD with
the same 16 chats: the popup is a scrollable radio list, and tapping its rows
switches chats in one gesture. Tapping `probe-01` (which has a message) and then
`probe-03`/`probe-02` (empty) cleared the pane to "No messages yet"; tapping
`probe-07` and then `probe-12` repainted their own markers. Tapping a row in a
scrolled popup works too, and the switch logs
`GET /api/v1/opencode/commands?location=…` → `200` with `commands 2`. Raw
screenshots are the `phone-*.png` files in the same artifact directory.

Limits: the free `beam-free` model is not asserted for assistant output, so
this is not evidence about a provider's replies. The child-session case was
checked once by hand, not by the probe. The device run used Chrome's `<select>`
popup in Firefox for Android only; other Android browsers are untested.

### Stale deployed daemon reproduced (2026-11-01)

A desktop user still saw the switching failure after the fix. The cause was the
running daemon, not the source: `~/.local/bin/darkfactory` was built on
2026-10-31 23:25 and served its own embedded `app.js` from `127.0.0.1:8787`
(`Cache-Control: no-store`, but the bytes were old), and its proxy still sent
`?location=<path>`.

Reproduction with that exact binary, against the same 16-chat fixture:

```
$ DFB_BIN=$HOME/.local/bin/darkfactory test/browser/probe.sh opencode
FAIL (3): forward: probe-02 did not render alone after the switch:
{"title":"probe-02",...,"text":"est failed: Model is unavailable.turn failed..."}

$ test/browser/probe.sh opencode
PASS opencode: 16 chats switched by click and by picker; empty chats clear the pane; commands 200
```

The installed binary was then replaced with `make install-restart`, which is
now the development install path: one atomic rename, no backup copy, then the
running daemon is restarted with its own pid file (only when
`/proc/<pid>/exe` is the installed binary). The detached session host (pid
178024, started earlier) was reused, so hosted PTYs survived; the new daemon
loaded four paired browser sessions and serves an `app.js` containing the fix.
`make install-status` reports "daemon pid … is running the installed build",
and the probe run against the installed binary passes.

Operational lesson: `darkfactory version` printed `0.1.0-dev` for every build, so
it could not show that a daemon was stale. It now prints the stamped commit,
`/state` returns `build`, and `make install-status` compares the installed
binary's build with the tree's. Before believing a phone/browser bug is fixed,
check `make install-status` and the footer, or run the probe with `DFB_BIN`
pointing at the installed binary.

### UI gate and build identity (2026-11-01)

The operational lesson above is now enforced. `make build` stamps the commit,
`/state` returns `build`, and the footer prints `ui <UI_VERSION> · <build>`.
`make check` carries the static half (`internal/httpapi/ui_test.go`): version
and `?v=` drift, an unresolved element id, a remote asset, or a missing `build`
field fails without a browser. `make ui-gate` drives the real page at 1280×900
and 396×719 with an isolated daemon and a real PTY.

Executed:

```
$ make ui-gate
PASS ui-gate: desktop 1280x900, phone 396x719; ui 2026-11-01.2;
daemon 0.1.0-dev (08ee1de-dirty); served app.js matches web/app.js

$ DFB_BIN=$HOME/.local/bin/darkfactory test/browser/probe.sh gate
FAIL (3): desktop: the daemon does not report a build
```

The second run is the negative control: the installed binary predates
`/state.build` and the footer change. It is the check to run before believing a
UI report; a pass means the page under test is `web/app.js` in this tree.

Limits: the browser half needs Playwright and Chromium; it was run with a
sibling checkout's Playwright module
(`~/node_modules/playwright`)
and Chromium 1243, not an installed repo dependency. It does not replace the
Android/iOS or Wails gates. The Wails shell builds on this host now
(`make desktop-preflight` reports the native SDK present) and opens the daemon
UI. Relaunch is fixed by device-key auth (below), and the remaining native
navigation, stream, resize, tab-identity, and close/reopen checks now pass on
this host; see “Wails native acceptance” below.

### Desktop renderer: DOM vs WebGL (2026-11-01)

The client now loads the WebGL2 addon on every layout, keeps the DOM fallback,
and refuses a software GL context. The clientbench fixture gained
`?renderer=webgl`, `DF_DPR`, and a paint interval; the raw runs are in
`test/artifacts/client-foundation/desktop-webgl/`.

Three alternating headed Chromium trials on the Intel GPU (`ANGLE (Intel,
Mesa Intel(R) Graphics (RPL-S), OpenGL ES 3.2)`), 1,049,600-byte parse burst:

| Variant | finalRenderMs median (range) | parseMs median |
|---|---|---|
| DOM | 577.9 (559.7–616.5) | 576.0 |
| WebGL | 550.8 (541.9–561.5) | 549.5 |

The difference is about 5%, five of six pairs favor WebGL, and it is a
synthetic parse-plus-final-frame proxy, not display latency. The headless
SwiftShader control was ~4x slower at parse with WebGL than with the DOM
(2031.8 vs 552.2 ms), which is why a software renderer is a fallback trigger.

Limits: one machine, one GPU, one workload. It does not measure a full-screen
TUI repaint, keyboard-to-visible echo, or the Wails/WebKitGTK renderer. Open
the native shell with `--debug` and read `webgl renderer:` before claiming a
desktop GPU result.

#### Wails/WebKitGTK: parse is the bottleneck (2026-11-01)

The same fixture ran inside `bin/darkfactory-desktop` (`--url '{url}'` and
`--url '{url}webgl'`, the latter using the fixture's query-free `/webgl` path),
three trials each. The shell reports WebGL2 with the masked renderer
`Apple GPU`; the fixture recorded:

| Run | Renderer | parseMs | renderMs | finalRenderMs |
|---|---|---|---|---|
| dom-1 | `dom` | 3571 | 880 | 4451 |
| dom-2 | `dom` | 3578 | 4989 | 8567 |
| dom-3 | `dom` | 3561 | 15 | 3576 |
| webgl-1 | `Apple GPU` | 3574 | 3035 | 6609 |
| webgl-2 | `Apple GPU` | 3598 | 17390 | 20988 |
| webgl-3 | `Apple GPU` | 3599 | 26 | 3625 |

Parse is ~3.57 s in every run, about 6.5x the ~0.55 s Chromium parse. The
fixture awaits every 8 KB write; the app coalesces output per animation frame,
so this is a tool-level parse comparison, not measured app latency. The
render/final intervals are erratic (DOM 3.6–8.6 s, WebGL 3.6–21.0 s), so this
fixture cannot show a WebGL win in WebKitGTK and one WebGL run is a severe
outlier. The client therefore keeps the DOM renderer on WebKitGTK and logs the
skip; WebGL remains on Chromium/Blink and non-Linux WebKit.

Limits: the render interval is noisy on WebKitGTK, the renderer string is
masked, and this is a synthetic parse burst. The consistent finding is the
parse gap, not the paint gap. A native terminal widget or a different WebView
is the path to closing it; another renderer flag is not.

#### Keyboard-to-visible echo (2026-11-02)

The desktop complaint was typing latency, not burst throughput, so echo was
measured end to end: isolated daemon, a real bash PTY, headless Chromium at
dpr 1.5 (the DOM renderer, which is the renderer the Wails shell uses), and
in-page `performance.now()` marks for the keydown, the input fetch, and the
first mutation that contains the echoed character. `test/browser/probe.sh echo`
reproduces it; it prints numbers and does not gate.

| Segment (p50) | Before | After |
|---|---|---|
| keydown → input request starts | 8.3 ms | 0.1-0.2 ms |
| request start → promise resolves | 1.9 ms | 9.1-10.4 ms |
| response → character in the DOM | 31.7 ms | 16.8-16.9 ms |
| **keydown → character in the DOM** | **42.3 ms** | **26.8-27.4 ms** |
| p90 | 48.3 ms | 32.8-35.9 ms |

The before figures are one run and the after figures are three runs on the same
host; the harness is `test/browser/probe.sh echo` in both cases.

The daemon answered `/input` in 0-1 ms in the after run (the isolated daemon's
`serve.log`), so the change is entirely client-side: the input request now
leaves in the microtask checkpoint instead of after a `setTimeout(0)`, and a
PTY chunk of 8 KiB or less is written to xterm immediately instead of on the
next animation frame. The remaining 16.8 ms
is one 60 Hz frame from xterm's own render scheduler; the request segment moved
because the fetch now starts inside the keydown turn and its callback waits for
the frame in progress.

Limits: Chromium's DOM renderer, not WebKitGTK's. This host cannot synthesize
keystrokes into a WebKitGTK WebView, so the native window's echo latency is
still unmeasured; expect it to be higher, because WebKitGTK parses ~6.5x slower
on a 1 MiB burst. That parse gap, not the frame budget, is the cost that a
different engine would have to remove.

### WebKitGTK cookie probe (2026-11-01)

The daemon's session cookie is `__Host-df_session`: `Secure`, `HttpOnly`,
`SameSite=Strict`, `Path=/`, 30-day `Max-Age`. A loopback probe served three
cookies and reported what came back:

| Engine | Cookies on `/checks` |
|---|---|
| Chromium 1243 | `__Host-df_probe`, `df_secure`, `df_plain` |
| WebKitGTK 2.52.6 (Wails) | `df_plain` only |

Chromium treats `http://127.0.0.1` as a trustworthy origin for Secure cookies;
WebKitGTK does not store them. The shell's relaunch-to-connect-card is the
cookie being rejected at set time, not the daemon row or `Max-Age`. A
non-Secure `Max-Age` control also did not survive an abrupt SIGTERM/SIGKILL
relaunch; that result is inconclusive about a graceful close and is not used as
a persistence claim.

Artifacts: `test/artifacts/client-foundation/c3-wails/cookie-probe/`. The
viable fix is a trusted HTTPS loopback origin, device-key auth, or accepting
re-auth per launch; weakening the cookie flags or moving the bearer to
`localStorage` violates the auth contract. The trusted-HTTPS branch was
superseded: it needs a system CA install, while device-key auth needs no sudo.

### Wails/WebKitGTK device-key auth (2026-11-01)

WebKitGTK 2.52.6 supports WebCrypto and persists IndexedDB across separate Wails
launches. A probe generated a non-extractable ECDSA P-256 key, exported the
SPKI public half (91 bytes), stored the `CryptoKey` in IndexedDB, and signed
with it in a second process (64-byte raw `r||s`); both runs reported
`privateKey.extractable=false`. No private key bytes are readable from page
script.

The client now uses that key as an additive credential:

- First token login: `GET /api/v1/state` `200`, then
  `POST /api/v1/device-keys` `201`; the Devices list shows
  `kind=key`, `scope=full`, `terminal=true`, `Desktop (WebKitGTK)`.
- Fresh launch with no token: `GET /api/v1/state` `401` →
  `POST /device-keys/challenge` `200` → `/verify` `200` → `/state` `200`.
  `?debug=1` logs `device key auth ok`; the Connect card never stays up.
- Daemon restart under the open window: the new process logs
  `loaded device keys count=1`, then the same `401` → challenge → verify →
  `200` sequence. The window returns to the fleet.
- Revoke the key: `DELETE /api/v1/devices/{id}` `204`; the next poll is `401`,
  challenge is `401`, the stale local key is cleared, and the window lands on
  Connect with no retry loop.

The first login still needs the master token once; later launches and daemon
restarts do not. The 12-hour in-memory session bearer is kept in
`sessionStorage`, never `localStorage`. The raw cookie path is unchanged.
`make check` passes; `make ui-gate` passes with UI `2026-11-01.8`.
Evidence: `test/artifacts/client-foundation/c3-wails/device-key/`.

Limits: one host and WebKitGTK 2.52.6; the app was forced to `GDK_BACKEND=x11`
for XTest and screenshots. C3 check 1 is covered here; the other native checks
are recorded below.

### Wails native acceptance (2026-11-01)

Two shell sessions were created in an isolated daemon (`one` PID 80556 and
`two` PID 80569). In the real Wails/WebKitGTK window:

- Terminal input and resize: typing `echo dfmarker123` produced output, the
  window was resized from 1280×900 to 1000×700, the client posted
  `/sessions/{id}/resize` `204`, the terminal refit to 91×25, and `stty size`
  printed `25 91`. A tab switch returned `one`/PID 80556 and `two`/PID 80569
  with the same ids and separate terminal content.
- Close/reopen: closing the shell left daemon PID 80061, session-host PID
  80069, and both PTY PIDs alive. A fresh shell logged `device key auth ok` and
  reattached to the same session after `401` → challenge → verify → `200`.
- Daemon restart: stopping the daemon left the host and PTYs alive; the new
  daemon (PID 93843) logged `loaded device keys count=1`, and the open window
  logged `lostAuth` → `device key auth ok` → stream reattach.
- Navigation: a native WebKitGTK `decide-policy` handler now keeps later
  top-level navigation on the validated loopback origin and refuses new-window
  actions. A fixture's `target="_blank"` link and `location.href` attempt to
  `127.0.0.1:8894` were blocked (`blocked new window` / `blocked navigation`),
  `window.open` returned `null`, the external server received no request, and
  the shell stayed on the fixture origin.
- `--url` refusals: hostname, remote address, HTTPS, credentials, query,
  fragment, and `file:` each exited 1 with the loopback-URL error.

Environment: Wails v3.0.0, Go 1.27.3, GTK 4.14.5, WebKitGTK 2.52.6, libsoup
3.4.4, Ubuntu 24.04.5, kernel 6.14.0-24-generic, i5-13600K, UHD 770 / RX 7900
XT; Wayland session with `GDK_BACKEND=x11` for XTest. Evidence and artifacts:
`test/artifacts/client-foundation/c3-wails/native-checks/`.

Limits: the resize was an X11 client resize, not a window-manager drag; only one
host was tested. The shell has no native browser handoff, so an external link is
explicitly refused and logged rather than opened in the OS browser.
