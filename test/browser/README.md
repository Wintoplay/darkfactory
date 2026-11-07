# Browser client probes

`docs/testing-android.md` is the real phone procedure. These probes are the same
client regressions run against a real daemon, a real PTY and a real Chromium,
without waiting for an emulator. They find bugs at the client/HTTP and
client/PTY boundaries, not phone layout bugs: a black pane from a bad viewport
or a keyboard that covers the compose row still needs the device.

`probe.sh` starts an isolated daemon under a temporary root (its own XDG
directories, token, state DB, and session host), seeds one repo and two shell
sessions, and points `$SHELL` at `fixture-tui.py`. The fixture enables mouse
tracking, SGR encoding, bracketed paste and application cursor keys exactly
once, then repaints on SIGWINCH without repeating them. It appends everything
it reads from the PTY to `input.log`, so a click or a keystroke cannot pass as
"the UI looked right".

The `opencode` mode also starts a disposable OpenCode v2 service under the same
root (its own `HOME` and `XDG_*` directories, its own database and generated
password), points the daemon at it, and seeds many chats. It needs no account
or API key: the seed only needs OpenCode to store the user message, and the
model may fail its turn without changing the assertion.

## Run it

```bash
make build
test/browser/probe.sh attach    # C1: click before and after switch and reload
test/browser/probe.sh session   # C2: session-tab "+" creates a live PTY
test/browser/probe.sh opencode  # UI: 16 chats, switch by click and by picker
test/browser/probe.sh gate      # UI: desktop + phone layout contract
test/browser/probe.sh hostile   # TUI: hostile escape output is contained
test/browser/probe.sh sidebar   # sidebar menu: pin, copy path, session order
test/browser/probe.sh activity  # bell list: a waiting question, source, manual clear
test/browser/probe.sh echo      # measurement: keyboard-to-visible-echo
```

`make ui-gate` runs `gate` and `hostile`; the other modes are evidence for
C1/C2/UI-16 and are not part of `make check`. `echo` is a measurement, not a
gate: it prints numbers and never fails on a threshold.

Requirements: Node, Python 3, Playwright and a Chromium build. The repository
pins Playwright in the dev-only `tools/clientbench` module:

```bash
cd tools/clientbench
npm ci
npx playwright install chromium
```

`gate.cjs` resolves that module automatically. For another installation, point
at it the same way `tools/clientbench` does:

```bash
export DF_PLAYWRIGHT_MODULE=/path/to/node_modules/playwright
export DF_CHROMIUM=/path/to/chrome-headless-shell   # optional
```

The `opencode` mode also needs the `opencode` v2 CLI on `PATH`. Set
`DFB_CHATS` to change the seeded chat count.

The daemon embeds the UI, so an installed binary can serve an old `app.js` even
though the repository is fixed. `DFB_BIN` runs the probe against another build:

```bash
DFB_BIN=~/.local/bin/darkfactory test/browser/probe.sh opencode
```

That is the check to run when a user reports a UI bug that a source fix does not
explain: if the probe fails on the installed binary and passes on
`bin/darkfactory`, the running daemon is stale, not the fix.

`DFB_KEEP=1` keeps the temporary root and prints its path; otherwise everything
the probe created is removed. Nothing touches the operator's daemon or the
operator's OpenCode service.

**Trap this probe encodes:** the OpenCode service is started with `env -i`
(plus `HOME`, `PATH` and the `XDG_*` directories). This shell may export
`OPENCODE_DB` or `OPENCODE_SESSION_ID` for a running agent; an "isolated"
service that inherits them reads that agent's session store instead of its own.
If you start an OpenCode service by hand for a probe, scrub those variables.

## What each probe proves

`attach` (C1) signs in, opens a session, and clicks the pane three times: on
the first attach, after switching to another session tab and back, and after a
full page reload. Every click must produce an SGR mouse report at the PTY. A
client that only repaints the screen passes the paint check and fails this one.

`session` (C2) opens the outer session-tab `+` (not the `+` that OpenCode draws
inside the TUI), asserts the dialog is scoped to the open worktree, submits it,
and asserts a `201` with a new id in that worktree that is `live`. It then types
a marker into the terminal and waits for the fixture to read it. The cold-load
cookie probe is delayed by 400 ms, so a late `401` from that unauthenticated
attempt lands after the token login; the client must stay signed in.

`opencode` (UI) creates 16 chats in one worktree through the daemon API, gives
three of them a user marker, then drives the panel:

- the desktop list is clicked in forward and reverse order, and every switch
  must change the title, the selected row, the picker value **and** the message
  pane. A chat with no messages must show the empty state — the previous
  chat's transcript may not survive a click;
- content → empty → empty, and a rapid two-click switch, cover the reset and
  race paths;
- the 396 px layout drives the native `<select>` the same way a phone's picker
  does (`change`), with the same assertions;
- any `400` from `/api/v1/opencode/commands` fails the run, because that route
  is how the `/` completer learns the commands and it used to fail on every
  switch with OpenCode v2.0.26.

This probe found both defects it now guards: a switch to an empty chat left the
previous transcript on screen (the reported "clicking to switch does nothing"),
and the command list was rejected with `400` because the daemon sent
`?location=<path>` where OpenCode v2.0.26 expects `?location[directory]=<path>`.

`gate` (UI) signs in, opens the first session, and repeats the same checks at
1280×900 and 396×719. It is a layout contract, not a pixel diff, because fonts
and GPUs differ:

- at desktop width the fleet sidebar and status bar are visible, the phone nav
  is hidden, the terminal keeps at least 45% of the viewport height, and it ends
  above the status bar;
- at phone width the sidebar and status bar are hidden, the phone nav is visible,
  the terminal keeps at least 40% of the viewport, the session-tab `+` is fully
  on screen, and every visible app control is at least 16×16 CSS px;
- neither width scrolls horizontally, and the session tabs, terminal pane and
  xterm screen are visible;
- the footer names both the `UI_VERSION` in `web/app.js` and the daemon's
  `build` (version + commit), the daemon reports a build, `/app.js` is sent with
  `Cache-Control: no-store`, and its SHA-256 equals `web/app.js`.

The last pair is the stale-delivery check: a pass means the page under test is
the source in this working tree, not an installed binary from yesterday. Run
`DFB_KEEP=1` to keep the root and its `ui-gate-{desktop,phone}.png` screenshots.

`hostile` (SS-13) swaps the fixture for `fixture-hostile.py` (`DFB_FIXTURE`,
with `DFB_EXTRA_SESSIONS=0` so one session owns `input.log`) and emits the
canaries on the first SIGWINCH, which is what a client attach causes: a late
attach cannot miss them. It writes an OSC 52 clipboard read and write, OSC 0/1/2
title and icon sets, an OSC 8 hyperlink, iTerm2/kitty/Sixel images, OSC
4/10/11/12 palette and colour sets, the 104/110/111/112 resets, and colour,
DA1 and DECRQSS queries. Three of the sets are split across separate writes.

The probe asserts no clipboard call, no title change, no navigation, popup or
dialog from the link, no image element or `data:`/`blob:` reference, no external
request and no CSP violation, and that no element in the terminal has a
repainted background. It also asserts the five allowlisted replies still reach
the PTY — a filter that swallowed everything would fail there. It runs at a
fractional device scale so the DOM renderer is active, where the link run is a
span and the palette is a readable computed style.

It fails with `terminal output repainted the palette` against a daemon built
before the client filter, which is the negative control.

`sidebar` (UI-31…UI-33) right-clicks a session row and checks the menu labels,
that Copy path puts the worktree path on the clipboard, that a pin survives a
reload, and that `ArrowDown` on a grip moves the row in the sidebar and in
`/state`. It never clicks Reveal in file manager: that would open a window on
the host.

`activity` (UI-34) points the daemon at
`fixture-opencode-api.py` (`DFB_OC_STUB=1`) instead of a real OpenCode service,
because the question has to be waiting *before* the page loads. It asserts the
bell is clickable when empty, that the waiting question arrives through the
baseline with its chat and workspace, that following it opens the chat without
clearing it, and that an answered question is cleared by hand and stays gone.

`echo` (latency) is measurement, not a gate. Against a real bash PTY it reports
keydown → request start, request → response, response → character in the DOM,
and the total, as p50/p90 over 20 keystrokes. Use it to compare two builds on
one host; the value depends on vsync and engine, so a cross-host comparison is
meaningless. `DFB_ECHO_SCALE` (default 1.5; a fractional scale keeps the DOM
renderer) and `DFB_ECHO_CHARS` (default 20) tune it.

## Exit codes

- `0` every observation held.
- `1` a precondition is missing (not run through `probe.sh`, Playwright failed).
- `2` the setup gesture never took effect (a chat was not created, the panel
  never listed the seeded chats).
- `3` a client regression: the click never reached the PTY, the pane never
  painted, the tab did not open live, the typed input never arrived, a valid
  token left the client on the connect card, a switch did not repaint the
  pane, the command route was rejected, a layout contract failed, or the served
  `app.js` is not `web/app.js`.

These probes are evidence, not a substitute for the Android gates in
[`docs/client-foundation.md`](../../docs/client-foundation.md). They need no
`npm install` and are not part of `make check`.

The negative controls for the `opencode` probe: with the client reset reverted
to `''`, it fails on `probe-02 did not render alone` (the pane still showed
`probe-01`'s failed turn); with the daemon change reverted, it fails on
`the command list was rejected by the daemon: 400 …`.
