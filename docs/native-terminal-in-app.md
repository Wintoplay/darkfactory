# In-app native terminal (2026-11-02)

Status: **S1–S3 implemented; the pane is the desktop default, S4 audit
pending.** The shell runs it unless `--web-terminal` is passed (`?native=1` is
what the page needs). The pane renders and accepts input in the real Wails
window, fed by the existing attach client. The separate Ghostty window (UI-29)
stays available, but the web terminal is no longer the desktop default: WebKitGTK
plus xterm.js was the slow path measured in
[the terminal lag review](reviews/terminal-lag-2026-11-02.md).

## Goal

The terminal must live **inside the Wails window**, not in a second window.
Everything else in the window stays the existing web UI.

## Why not Ghostty

Ghostty 1.3.1 exposes no embeddable GTK widget; libghostty is not available in
this version. The only in-app native option on this stack (GTK4 + WebKitGTK 6)
is VTE, the GTK terminal widget (`vte-2.91-gtk4`, 0.76). Choosing it means a
second emulator engine to audit against SS-13, not a drop-in reuse of Ghostty.

## Architecture (implemented)

- `desktop/internal/terminal` is a small cgo package wrapping `VteTerminal`
  (`pkg-config: vte-2.91-gtk4 gtk4`); no new Go module dependency.
- `--in-app-terminal` makes the shell add `?native=1` to the validated daemon
  URL. The page only takes the native path when that marker and the shell's
  bridge are both present; a browser never has either.
- The shell wraps the window's WebView tree in a `GtkOverlay` on `WindowShow`.
  The VTE lives in a `GtkBox` overlay child placed at the rectangle the page
  reports for `#terminal`; moving/resizing only updates that child.
- The page keeps its xterm mounted underneath but does **not** open a browser
  stream when the native pane is active. If the pane cannot start, the shell
  calls back and the page reopens the browser stream.
- The pane is fed by the existing attach client, exactly like the Ghostty
  window: VTE spawns `darkfactory native-attach` on a local PTY with a pane
  capability on fd 3. VTE owns the grid and the PTY size; the attach client
  posts `stream`, `input`, `resize` and `redraw`. No new streaming or input
  protocol exists.
- The page reports geometry from `#terminal.getBoundingClientRect()` and
  re-reports on its `ResizeObserver`; the shell sets the overlay child's margin
  and size request.

## Credential handoff (implemented)

The shell never reads the master token, and the page never hands the shell a
reusable bearer or cookie.

1. The web UI calls the audited `POST /api/v1/sessions/{id}/native-pane` (verb
   `session.native_pane`, local UI only, running session only). The daemon
   stores only `hash(pane)` with the caller's principal, the session and an
   expiry, and returns the random pane id.
2. The page posts `{type:"open", sessionId, pane, x, y, w, h}` to the shell over
   the WebView message channel.
3. The shell writes the pane id to a pipe and passes it as fd 3 to
   `native-attach`; it never appears in argv, `/proc`, a URL or a log.
4. The daemon accepts the pane as a bearer-like credential only on `stream`,
   `input`, `resize` and `redraw` for that one session. The first stream claims
   it atomically (a second stream is refused) and extends its TTL from 2 minutes
   (unused) to 12 hours. When the stream closes the grant is deleted, so input
   stops with the window.
5. The capability is bound to the minting principal for ledger decisions and
   cannot read state, another session, or any other route.

Rejected alternatives: the shell reading the token file (violates the shell
boundary); the page passing its bearer/cookie (reusable credential in native
code); a daemon URL with a token in the query (credential in a URL and logs); a
per-session bearer that lives as long as the window.

## Fast output and stream recovery

The host and daemon now coalesce queued PTY output into frames of up to 256 KiB
and flush once per batch. Without that, a TUI that redraws line by line turned
each write into an HTTP frame, a native-attach write and a VTE render, and the
host's 256-chunk subscriber buffer could overflow.

A closed subscriber is not a session exit. The host closes a mirror that falls
behind, but the PTY may still be running; the old code sent an exit frame with
the running model, which made the daemon mark the live session exited and froze
the pane. The host now sends an exit frame only when the session is no longer
running. The daemon treats a dropped mirror as a detach: it reattaches, replaces
the ring with the host's snapshot, restores the tracked modes and broadcasts the
snapshot to viewers. `TestHostDroppedMirrorIsNotAnExit` and
`TestDroppedMirrorReattaches` pin both halves. Fixing a running `serve` and a
running session host requires new processes; the host owns the PTYs, so a host
restart ends them.

## Bridge surface

The page is served by the daemon, not the Wails asset server, so the Wails JS
runtime (`window.wails`) is absent and its event API cannot be used. The shell
uses WebKit's own primitive:

- `webkit_user_content_manager_register_script_message_handler("dfNative")`
  plus `script-message-received::dfNative`; the shell injects
  `window.__dfNativeSend` on every load (`load-changed`). Messages are JSON and
  are the only page→shell input: `open`, `bounds`, `hide`, `show`, `close`.
- Shell→page uses `ExecJS` for exactly two callbacks: `__dfTerminalReady` and
  `__dfTerminalFailed`.
- No daemon URL, no token, no filesystem access, no arbitrary exec is exposed.
  The navigation guard stays in force.

## Modal dialogs and the pane (2026-11-02, fixed)

A `GtkOverlay` child always paints above its main child, so the pane sat above
the WebView. Every modal in the client is an HTML `<dialog>` inside that
WebView, and the session rectangle covers the centre of the window: a 440 px
dialog centred in a 1280×900 window lands at x 420–860 inside a terminal that
starts at x 280. "Stop session" (a confirmation) and "New session" (a form) are
both opened from the session view, where the pane is up, so both dialogs were
painted under the pane — invisible and unclickable, and the click landed on the
terminal instead. It read as a hang and produced no request: the daemon ledger
never saw the `session.delete` or `session.create` the owner attempted.

The page now emits `hide` before a modal appears and `show` when it ends, from
`openDialog`, `confirmAction`, the dialog `close` event (Cancel, Escape and
`form method=dialog` all end there) and a pane opened while a dialog is already
up. The shell hides the holder with `gtk_widget_set_visible(FALSE)`; it does not
detach. The PTY stays attached, VTE keeps its screen, and hiding the focused
widget hands keyboard focus back to the WebView. VTE cannot render under the
WebView instead: GDK has no "draw below" child, and a second window is the
Ghostty path the pane replaced.

Evidence: with a fixture page that opens a pane and then a dialog, the shell's
real window shows `dialog.open=true` with no dialog on screen before the fix,
and the dialog above a hidden pane after it; `test/browser/native-dialog.cjs`
drives the real page (via `test/browser/probe.sh native-dialog`) and asserts the
`open` → `hide` → `show` → `close` sequence for both controls, failing on a
build without the page change.

## The pane follows the terminal element (2026-11-03)

The pane is an overlay child, so it stays where it was last placed. Leaving the
session view (Fleet, Chat, Runs) collapses the terminal element; the page stops
sending bounds, `df_place` keeps its last rectangle, and the pane kept painting
a dead dark box over the chat — reported as "I click and the terminal below goes
blank dark". The page now calls `syncNativePaneVisibility()` from the same frame
that sends bounds and hides the pane whenever `nativeBounds()` is null, i.e.
whenever the terminal element is off screen, not only for a dialog or the
activity panel. Hiding does not detach: the PTY keeps its screen and the pane
returns with the session view.

## Security review status

- Escape surface: the attach client's existing SS-13 filter
  (`internal/nativeattach/filter.go`) still runs before VTE for stream bytes.
  VTE has no OSC 52 implementation (audited: the library exposes only explicit
  copy/paste APIs), so a forwarded OSC 52 is inert; a TUI's own copy does not
  reach the clipboard in the in-app pane. Context-menu copy is the supported
  path. OSC 8 links and image protocols still need a focused VTE test.
- Clipboard: right-click Copy/Paste and `Ctrl+Shift+C/V` are implemented on the
  VTE widget. The XTEST probe confirms selection + `Ctrl+Shift+C` puts the text
  on the clipboard when the VTE has keyboard focus; the earlier in-app failure
  was a focus/state artifact during a logged-out page.
- Capability lifecycle: mint is local-UI only and audited; wrong session, wrong
  verb, second stream, read scope and forwarded mint are tested; the hash is
  stored; the grant dies with the stream.
- Reparenting the WebView into the overlay at `WindowShow` has been exercised
  with the real page; the navigation guard and cookie flow are unchanged.

## Acceptance gates

1. Spike: VTE renders in the Wails window; the WebView still loads, stays on
   its origin and resizes. **Passed.**
2. Real PTY: type, resize, switch sessions, close and reattach; the PTY
   survives a window close. **Typing and resize passed; session switching and
   window close passed in the disposable run.**
3. Capability: replay, expiry, wrong session, read scope and forwarded requests
   refused, with tests. **Passed (`native_pane_test.go`).**
4. Hostile output: the filter suite covers clipboard/title/link/image/palette
   sequences; a native VTE hostile run is still **pending**.
5. Latency: compare typing into the in-app pane with the xterm.js path; record
   medians rather than claiming a win. **Partially measured.** The earlier
   xterm.js probe measured 26.8–27.4 ms p50 keydown-to-DOM (native WebKitGTK,
   DOM renderer). In the fixed in-app pane, the first clean XTEST probes
   measured 5–6 ms from keydown to the daemon's `/input` request, and a 60 fps
   110×34 synthetic TUI used 5–10% desktop CPU on XWayland. A full real-OpenCode
   A/B and a pixel-present latency median remain open.
6. Clipboard: copy/paste by context menu and by a TUI (OSC 52) behave as
   decided, with the user visible in the loop. **Context menu and
   `Ctrl+Shift+C/V` passed; TUI OSC 52 copy is inert in VTE and documented.**
7. Default: only after 4–6, decide whether the pane replaces the web terminal.

## Spike evidence

- S1: VTE reparented beside the WebView; typing, divider, right-click and
  `Ctrl+Shift+C/V` confirmed by the owner.
- S2: pane fed by `native-attach` against a disposable daemon; typing and
  resize confirmed by the owner.
- S3: the capability route and tests, the overlay pane in the real shell, the
  page bridge, and an end-to-end run: `native-pane 200`, one `resize 204` from
  the attach client, native VTE showing the live session, input accepted.
- Automated clipboard probe (XTEST + `wl-paste`): select a line in the VTE,
  `Ctrl+Shift+C`, read the selection back from the clipboard. Passed in the
  spike window.

## Dependency

`libvte-2.91-gtk4-dev` (Ubuntu 24.04, VTE 0.76) plus `libvte-2.91-gtk4-0`,
`gir1.2-vte-3.91` and gnutls/unbound dev dependencies. Removal:
`sudo apt remove libvte-2.91-gtk4-dev libvte-2.91-gtk4-0 gir1.2-vte-3.91`.
The app's Linux build then needs this package; `make desktop-preflight` must
report it in the missing-modules list.
