# Substrate lessons

**Purpose.** DarkFactory currently ships a browser UI over an HTTP daemon and a
detached PTY host. The UI is replaceable; the PTY host, the session contract and
the auth rules are not. This document records what must survive a move to a
different client substrate — WASM, a desktop shell, a native mobile app, or a
plain SSH/TUI client — and what has to be re-proven on the new substrate.

The browser and the optional Linux Wails window are the shipped local clients;
the native Android client is the shipped remote one, connecting to one host at
a time. Their implementation gates are in [client-foundation.md](client-foundation.md).
This document states what those clients must preserve.

## The short version

1. The session host owns PTYs and rings. The client owns rendering. Do not put a
   terminal emulator, cookie jar, or UI state in the host.
2. A raw ring is a byte log, not a screen snapshot. Never replay a ring tail
   into a fresh terminal and expect a coherent screen.
3. A full-screen TUI attach is either **a clean redraw** or **nothing**. A
   line-oriented shell attach may replay a bounded tail trimmed to a newline.
   A full-screen TUI owns its scrollback; the terminal emulator's scrollback is
   empty on the alternate screen, so scrolling must be PageUp/PageDown or the
   app's own keys. Do not infer "TUI" from xterm's alternate-screen bit; the
   attach policy's redraw signal is the contract.
4. The redraw primitive is a terminal-size nudge that delivers `SIGWINCH`.
   It must leave the final size unchanged and be safe to call repeatedly. One
   client owns the PTY size at a time; a second attach detaches the first rather
   than letting two viewports resize the same screen.
5. Pairing is a protocol: one-time code in the URL fragment, short TTL, hashed,
   rate-limited, HTTPS for remote, never logged. The QR is a local rendering of
   that URL, not a second protocol. The QR must encode an origin the phone can
   actually reach; a loopback origin is desktop-only. Use `publicURL` for a
   Tailscale/SSH origin instead of asking the user to hand-edit the link.
6. Pairing creates a device record with its own hashed cookie and a host-chosen
   `full` or `read` scope, and per-device/all-other revoke takes effect on the
   next request. Workspace access (terminal and OpenCode writes) is one
   full-scope capability; do not describe read-only as process isolation.
7. The stream has no sequence numbers yet. A new substrate does not make
   reconnects duplicate-free; `snapshot + seq` (SS-4) remains the fix.
   A screen redraw also does not prove restoration of terminal modes. The
   client resets xterm on attach and the server suppresses TUI history. A TUI
   that redraws text without re-emitting its startup mouse modes can therefore
   look correct while clicks send no bytes. Preserve or reconstruct proven
   modes without replaying a raw ring tail. Mode state must be carried by the
   process that owns the PTY, the host, because the daemon that tracks it is
   recreated on restart and a ring tail is not a source of truth. Migration
   test: enable SGR mouse tracking, attach and click a known cell, switch away
   and reattach, click the same cell, and assert the received press/release
   bytes and resulting action. A "survives a restart" claim needs a test that
   crosses the restart boundary: evict the enable from the ring, replace the
   daemon, and reattach. An in-process attach test passes without exercising
   the failure.
   A synthetic Chromium probe confirmed mode-enabled taps work and reset plus
   paint-only taps emit no mouse bytes. This was verified on the live OpenCode
   TUI on 2026-11-01 and fixed by carrying the host's mode state on attach; see
   `test/artifacts/client-foundation/tui-mode-restore/evidence.txt`.
8. Input and output are streams, not one request per key or network chunk.
   Coalesce per frame and keep at most one input request in flight; a remote
   round trip per wheel notch feels broken even when every byte arrives.
   The same applies to the host-to-daemon mirror: write queued output in a
   bounded batch and flush once per batch. A fast PTY that writes line by line
   otherwise becomes one frame, one flush and one emulator write per line.
   A subscriber-channel close is a detach, not proof that the PTY exited. If a
   mirror falls behind and the host drops it, sending an exit frame for the
   still-running session makes the daemon kill the session row and freeze the
   pane. Close the stream without an exit frame and let the daemon reattach to
   a fresh snapshot; the snapshot must be replayed to viewers, not treated as a
   silent continuation of the old ring.
9. Do not embed a full-screen application that already ships a session/chat API
   into a terminal emulator. The terminal cannot see the app's message list,
   scroll position, mouse targets, or keymap; synthetic PageUp/PageDown/End is a
   guess. Use the app's API and render a native panel; keep the terminal for
   plain shells and PTYs. A link to the app's own client is a fallback, not an
   integration.
10. A local service credential belongs on the server side of the proxy. Read the
    OpenCode password from its `0600` service config, send Basic auth only to a
    loopback URL, and never put the password, API URL, or raw upstream error in
    `/state`, the browser, or the log. The browser gets DarkFactory auth and
    proxied JSON, nothing else.
11. Treat an app's event stream as a hint, not a delivery guarantee. OpenCode's
    SSE endpoint documents missed events and slow-consumer failure; debounce it
    for latency and keep a slow fallback poll. A permission prompt is also an
    event the native panel must answer, or the agent stalls with no visible
    cause. Pagination cursors can encode their own order; do not send an
    `order` parameter beside a cursor unless the upstream contract allows it.
12. A terminal emulator's touch layer is not a platform guarantee. xterm.js
    6.0.0 shipped no native touch scrolling; a vertical pan produced no wheel
    and no scroll. Translate the pan into a synthetic wheel on the screen layer
    and keep `touch-action: none` on the host. Also pin the app shell to the
    visual viewport, not the layout viewport: iOS resizes only the visual
    viewport for the keyboard, so a fixed layout-viewport footer is hidden.
13. A viewport hook is a layout trigger, not a data event. `visualViewport.scroll`
    fires at pan speed and every `--app-height` / `--app-top` write relayouts the
    fixed shell and the terminal canvas inside it, so coalesce writes to one per
    frame and skip unchanged values. Do not size a terminal on a fixed delay: the
    keyboard animation finishes after focus and iOS reports the final size late,
    so a timer-driven fit lands on the wrong row count and repaints again. Poll
    the size, fit once it holds still, and skip a fit that proposes the grid
    already applied. A touch that scrolls must not focus the terminal either —
    focusing mid-gesture opens the keyboard and shrinks the viewport under the
    finger, which reads as a jump.
14. A full-bleed terminal cannot copy a chat app's keyboard strategy. A chat
    composer can float above the keyboard and clip the log; a terminal has to
    keep its grid focused and visible, so the shell must track the visual
    viewport rather than let the page scroll. Do not port a "composer bar" to a
    PTY: readline, full-screen TUIs and mouse reporting all need the keystroke,
    not a line buffer.
15. An HTTP response is not readable until its body produces a byte. A browser
    `fetch` promise (and any streaming HTTP client) can stay pending on a
    zero-byte 200, so a streaming endpoint must send something immediately,
    even when there is no payload yet. DarkFactory's attach suppressed the
    replayed tail for TUIs and sent nothing, so Gecko never handed the response
    to the page, the `X-DF-Redraw` header was invisible, and the repaint was
    never requested: an idle TUI attached to a black pane. Send a protocol
    no-op first (an SGR reset for a terminal stream) and keep headers that gate
    client behaviour behind it. Any substrate that streams over HTTP inherits
    this; a raw socket client does not.
16. A failure is data, not prose. One JSON envelope with a stable code,
    `remediable` and a hint lets a client branch without parsing English, and a
    golden test pins the code set so a rename is a deliberate contract change.
    Every route, including auth and the streaming endpoints before they switch
    protocols, answers with that envelope; a 5xx body says only that it failed,
    and the detail stays in the log. A browser client renders `detail`, then
    `hint`, then `code`; it never prints the raw body.
17. A crypto format is a byte contract, and a second implementation must be
    executed against the first, not reviewed by eye. The Android v2 envelope is
    Java wire code around a pinned cryptographic provider, so the JVM
    gate compiles the app's real classes and compares hashes, X25519, the bundle
    signature, transcripts, HKDF keys, AADs and the fingerprint against Go, then
    has Go decapsulate the Java ML-KEM ciphertext. A second gate pairs and reads
    a real endpoint from a disposable daemon, because matching vectors can still
    be wired to the wrong request. Passing both is interoperability, not a
    side-channel review. Single-use secrets still need side-channel protection;
    use reviewed implementations rather than assuming fresh randomness makes
    variable-time arithmetic safe.

## What is actually substrate-independent

| Contract | Current implementation | Why it must survive |
|---|---|---|
| PTY ownership | `darkfactory session-host` over a `0600` Unix socket | The daemon and any UI can restart without killing agents |
| Session identity | Same session ID and PID across `serve` restarts; restore under the same ID only with a new PTY | Users trust the tab and the row |
| Ring | Bounded byte ring in the host; daemon mirrors it | Bounded memory; the source for shell history |
| Attach policy | `SubscribeBrowser` returns a shell tail or a redraw hint | Avoids replaying a partial TUI frame |
| Redraw | Resize nudge (rows−1, rows) → `SIGWINCH` | Works with any full-screen CLI, no app-specific key |
| Auth | Master bearer token, hashed cookie sessions, and public-key device sessions | Same-user access without putting the master token in a URL |
| Pairing | 40-bit code, 120 s TTL, one-time, hashed, 5/min/source, 30/min global | The security properties are protocol-level |
| Device key | Non-extractable ES256 private key in IndexedDB; SPKI row; one-use 120 s challenge; 12 h session bearer | Survives a WebView that rejects Secure cookies without a native bridge |
| Pairing transport | `POST /pair/code` authenticated; `POST /pair` code-gated | The code is the gate, not the QR image |

## Substrate map

### Browser + xterm.js (current)

- Terminal: xterm.js 6.0.0 + fit addon; `terminal.reset()`/`clear()` on attach.
- Attach: `GET /sessions/{id}/stream`; `X-DF-Redraw: 1` suppresses history;
  `POST /sessions/{id}/redraw` nudges the PTY.
- Auth: `__Host-df_session` — `Secure`, `HttpOnly`, `SameSite=Strict`,
  `Path=/`, no `Domain` — keyed at rest with `SHA-256(instanceSecret ‖ token)`;
  bearer token only in `sessionStorage` for the tab; font size in
  `localStorage`.
- The `__Host-` prefix forbids a non-`Secure` cookie, and loopback is a
  trustworthy origin, so the same shape works over plain
  `http://127.0.0.1:8787`, which is what `open` and `adb reverse` use. Go's
  `cookiejar` does not model that exception, so HTTP tests replay the cookie by
  hand instead of relying on a jar. Idle expiry is 30 days with a 90-day cap,
  so the cookie is re-issued on an authenticated response at most hourly; a
  stream sets that header before its handler writes, which is why renewal lives
  in the middleware rather than in a handler. Migrating from an unprefixed `df_session`
  cookie invalidates open tabs once — the old cookie is unknown, and the page
  falls back to `darkfactory open` or a pair code. Rows written with the old key
  carry no `keyVersion` and are deleted on load, so the Devices dialog never
  lists a phantom entry.
- Pairing: QR is a DOM grid built from the URL; code/link fallback.
- Browser-only: `TextDecoder`, `fetch` streaming, `visualViewport`, `dvh`,
  `navigator.clipboard`, `sessionStorage`, `localStorage`.

### WASM client

- Keep the host and HTTP/Unix-socket boundary. WASM cannot own a host PTY; it
  must connect to the same daemon.
- Use a terminal emulator in the WASM/browser runtime (xterm.js is the reference)
  and the same attach policy. If the WASM client uses a WebSocket instead of
  HTTP streaming, carry the redraw hint as a control frame, not a header.
- Browser storage rules still apply if it runs in a browser; do not move the
  master token to `localStorage` because the code is WASM.
- The QR can be rendered by the same generator or by a host-provided matrix;
  the code remains the source of truth.

### Desktop shell

- Use a native terminal widget or embed xterm.js; the attach policy is the same.
- The Wails evaluation loads the ordinary loopback HTTP UI. WebKitGTK rejects
  the daemon's Secure `__Host-` cookie over plain loopback, so the client uses
  device-key auth: after one token login it stores a non-extractable ECDSA
  P-256 private key in IndexedDB, registers the SPKI public half, and signs a
  one-use challenge on each launch. The private key never becomes bytes and no
  native credential bridge is added. Challenge/verify return a short-lived
  session bearer, not a cookie; keep the daemon's scope, terminal, revoke, and
  origin checks. Do not add a second plaintext token config or move the master
  token to `localStorage`.
- Wails v3.0.0 has no Go navigation hook. Attach a GTK/WebKitGTK
  `decide-policy` handler to the window's existing WebView after `WindowShow`:
  allow only the validated loopback origin for top-level navigation and ignore
  every new-window action. This is native defense in depth and exposes no page
  method; the daemon's CSP and origin checks remain authoritative. A page-level
  `beforeunload` or click handler is not a substitute.
- `X-DF-Redraw` is HTTP-specific; a native client can call the redraw endpoint
  directly or ask the host for `SIGWINCH`.
- QR scanning is optional; a desktop can show the QR for a phone, but the
  desktop itself can use `POST /pair` only if it is the pairing target.
- Native folder pickers are already behind `/fs/pick`; keep the argv-only,
  loopback-only rule.

### Native mobile / tablet

- Terminal widget plus soft-keyboard handling. On resume, reattach and ask for a
  redraw; do not replay a TUI ring.
- Pairing: scan the QR or enter the code. The phone gets its own device record
  with `full` or `read` scope and can be revoked; read-only is an API/UI guard,
  not process isolation.
- Background/foreground and orientation are new sources of resize; the redraw
  contract handles them. On iPad the layout viewport does not shrink for the
  keyboard; pin the shell to `visualViewport.height` and `visualViewport.offsetTop`,
  or the composer/terminal stays behind the keyboard even though `100dvh` looks
  correct.
- xterm.js 6.0.0 removed native touch scrolling (#5489). Translate a vertical
  touch pan into a synthetic `WheelEvent` dispatched on the screen/rows element
  under the finger; the outer `.xterm` node does not receive the wheel handler.
  This keeps shell scrollback and TUI wheel/arrow behavior in one path.

### SSH / plain TUI

- No pairing and no QR. Use the bearer token or a local socket.
- Attach directly to the host stream; the terminal size is the SSH client's.
- The same ring/TUI rule applies if a wrapper replays output.

## Non-negotiable tests after a substrate change

- **TUI refresh:** open OpenCode/vim/htop, refresh or reconnect, and confirm the
  screen is painted from the top, not a partial SGR frame. The attach must not
  send the 4 MiB ring.
- **Shell scrollback:** reconnect a shell and confirm recent lines survive and
  the first replayed line starts at a newline.
- **Attach invariants:** the replay policy is a heuristic, so what a new
  substrate must preserve is structural: the bytes are a contiguous suffix of
  the ring, they start on a line boundary or are the whole buffer, they never
  exceed 256 KiB, and none are sent beside a redraw request.
  `FuzzBrowserSnapshot` encodes exactly these and runs in the hermetic suite.
- **One owner:** attach a second browser to the same session; the first gets a
  detached notice and stops, and only the second resizes the PTY.
- **Input modes:** open a TUI that enables mouse tracking once, click, switch
  away and back, reload the page, and click again. Every click must produce an
  SGR report at the PTY. The attach carries the last-seen `?1`, `?1000/1002/1003`,
  `?1005/1006/1015`, `?1004` and `?2004` state because a redraw repaints the
  screen without repeating them. Never turn a mode on that the app did not
  enable, and never replay the raw tail to recover it.
- **TUI scroll:** in a full-screen TUI, wheel and vertical swipe reach the
  TUI's wheel/arrow path (the ↑/↓ buttons send PageUp/PageDown); a normal shell
  still scrolls its own scrollback. A swipe must not scroll the page or leave a
  blank canvas.
- **Redraw:** call the redraw path twice; the final terminal size is unchanged
  and the app repaints.
- **Pairing:** QR, code entry, expiry, one-time use, loopback, a configured
  non-loopback `publicURL` (Tailscale Serve), HTTPS proxy, and the
  `Host`/`Origin`/`X-DF-Request` gates.
- **Auth:** cookie survives a daemon restart; logout deletes it; no master token
  in a URL, log, or persistent browser storage.
- **Session continuity:** kill and restart `serve`; the same host PID and
  session PID are still live. Do not test this by restarting the host.
- **Mobile:** keyboard, orientation, safe areas, reconnect, and scrolling. A
  periodic state poll must not rebuild unchanged lists; preserve scroll
  positions when a real update arrives, or the page will appear to slide.

## Open gaps that a substrate change must not hide

- The TUI decision is a tail heuristic, not a terminal-state snapshot. A client
  that can serialize a screen (a real VT emulator) should do so; the current
  contract only guarantees "do not replay a partial frame."
- A prompt is not a TUI. A fresh shell writes colour, `CR` and line erases and
  no newline at all, which reads exactly like an escape-heavy partial repaint;
  classifying it as one attaches a black pane that stays black until the user
  types, and does so again on every tab switch. Two rules keep it honest: an
  "escape-heavy, newline-free" tail only counts as a repaint when it contains a
  row/column address, and a newline-free snapshot still replays when nothing
  has been evicted from the ring, because then it begins at the session's first
  byte and is coherent by construction. Replay still repeats the prompt once
  per redraw that happened while the ring was whole; that is honest history,
  not a blank screen.
- `snapshot + seq` (SS-4) is not implemented; reconnects can duplicate bytes.
- The mode tracker is not a terminal snapshot either. It remembers input modes
  from output it has seen; the host, which owns the PTY, carries those modes on
  attach, so a daemon restart no longer loses an enable that the bounded ring
  has evicted. A mode the app never enabled is still never guessed. Output
  modes such as cursor visibility, the alternate screen, and insert mode are
  deliberately not restored: guessing them would corrupt the paint.
  `?1000/1002/1003` and `?1005/1006/1015` are tracked as one last-writer-wins
  family each, which is how applications use them.
- A renderer is not a speedup by itself. On the real Intel GPU, three headed
  Chromium trials of the 1 MiB clientbench burst were ~5% faster end-to-final-
  frame with WebGL than with the DOM (median 550.8 vs 577.9 ms); the same
  WebGL path on headless SwiftShader was ~4x slower at parse (2031.8 vs
  552.2 ms). Log the GL renderer string, refuse a software context, and do not
  claim a desktop win from "WebGL enabled" or from a synthetic parse burst. A
  full-screen repaint remains unmeasured. In the Wails/WebKitGTK shell the same
  burst parsed in ~3.57 s versus ~0.55 s in Chromium, and WebGL had no reliable
  win (one run took 21 s to the final frame), so WebKitGTK keeps the DOM
  renderer. The fixture writes serially; the app coalesces per frame, so treat
  the parse gap as a signal to measure real keyboard-to-visible echo, not as
  the app's measured latency. Another renderer flag will not close it.
- Trustworthy-origin cookie rules are engine-specific. Chromium stored a
  Secure `__Host-` cookie from `http://127.0.0.1`; WebKitGTK stored only the
  non-Secure control, so the daemon's session cookie never reached the Wails
  shell and a relaunch returned to the connect card. Verify the actual WebView
  before promising cookie persistence; do not weaken `Secure`/`__Host-` or move
  the bearer to `localStorage` to compensate. A non-extractable WebCrypto key
  does survive IndexedDB across Wails launches (verified with ECDSA P-256), so
  a challenge-response credential is the portable answer for this WebView.
  Keep the cookie path unchanged for Chromium and phones.
- Device records and per-device revoke exist, with workspace access derived from
  scope, idle expiry, and a `terminal` field kept only as compatibility output.
  Device-key rows share the device list but not the cookie bucket. Scopes are
  still only `full`/`read`, so the target `admin` scope, second-station
  confirmation, and pairing/device ledger events are open. Read-only is not
  containment.
- Upgrading the session host still requires stopping hosted PTYs. A substrate
  change must not pretend the host is hot-upgradable.
- The QR is a convenience; a substrate without a camera can still pair by code.
- Device-key bootstrap is auth, not a normal mutation: challenge/verify must
  enforce same origin, `X-DF-Request`, and a rate limit; the nonce is consumed
  before signature verification; verify re-reads the key under the lock so a
  concurrent revoke cannot mint a session; and the client deletes its local
  key only on a challenge `401`, not on a failed verify (the nonce may merely
  have expired).

## Native client E2EE envelope

- A native client cannot trust a TLS terminator on the tailnet. Pair a P-256
  ECDH host key, seal the per-device token in the pairing response, and keep the
  bearer inside an authenticated envelope. The app must pin the host public key
  and show the fingerprint next to the desktop's local Pair phone dialog; a
  first-use pin without a comparison is only trust-on-first-use.
- The envelope must carry the method, path, headers, body, and token together.
  Encrypting only the body leaves the bearer visible to the transport and makes
  the claim false. Dispatch the inner request through the ordinary
  `Server.Handler()` so `auth`, `mutate`, scope, audit, and read-only rules do
  not fork into an E2EE-only path.
- Streaming changes the response contract: frame 0 is the encrypted status and
  headers, later frames are body bytes with a request-bound AAD, and `Flush`
  must reach both the inner handler and the outer HTTP response. A local WebView
  abort (`Broken pipe` on the loopback socket) is not a host-key failure; it
  must not clear the paired device. Decryption failure is different and must
  return to pairing.
- Replay and clock checks belong on the server before the inner dispatch. A
  captured mutation with a valid token is otherwise repeatable. The first
  pairing code is still a low-entropy secret; the fingerprint comparison, not
  the TLS path, is what detects a substituted host key.

- Android calculates the fingerprint from the received host key and rejects a
  mismatch before confirmation (DP-19); run `sh test/android/check-fingerprint.sh`
  with a JDK. Encrypted request IDs are committed before dispatch and retained
  until the envelope timestamp plus the 5-minute skew window (DP-20), so a
  future-dated request inside that window still has a record after it would
  otherwise have expired. `internal/store` reopens the database and covers
  per-device separation, expiry and the future timestamp;
  `TestE2EEReplayRefusedAcrossDaemonRestart` adds the HTTP server-restart case.
  A storage failure refuses dispatch. The bucket is keyed by expiry and each
  call sweeps at most 256 expired records, so one request cannot pay for the
  whole backlog. Existing clients need no wire migration. Requests accepted
  before upgrading have no persisted record, so the upgrade is not retroactive.
  The 2026-11-02 internal review's confirmed defects are fixed and tested; its
  remaining hardening gaps are recorded as accepted/deferred in the review's
  resolution table. This is not an external security review.
- A second envelope suite must be a property of the device row, not a per-message
  flag. Put the suite and the transcript in the HKDF salt/info and the AAD, put
  the accepted suite inside the sealed pair payload, and refuse the old suite
  with a distinct code (`e2ee_suite_downgrade`) so a client cannot retry it as a
  corrupted frame. The host's minimum is policy; a client claim must not lower
  it. Hybrid means both secrets concatenated into one input, never either/or.
- The v2 pair transcript includes the device id, which the client only learns
  from the response. That is safe because a rewritten id changes the salt, so
  the sealed payload fails to open, and the payload repeats the id and suite.
  The host proves its X25519 and ML-KEM keys with a P-256 signature over a
  domain-tagged, length-prefixed bundle, so rotating those halves does not move
  the pin. Upgrading the old bare PKCS#8 identity row in place is what keeps
  existing phones paired; a malformed identity file must fail closed instead of
  generating a new key.

## OpenCode proxy contracts (v2.0.26)

- The daemon proxies OpenCode, so OpenCode's query shapes are a contract the
  daemon has to match. `/api/command` takes the location as an object:
  `?location[directory]=<path>`. A bare `?location=<path>` (what this daemon
  sent before 2026-11-01) is rejected with `400 InvalidRequestError`, which
  shows up as an empty `/` completer and an error on every chat switch. The
  probe asserts the route returns 200; the Go test asserts the proxy path.
- A chat with no messages is not a special case for the network, but it is for
  the render: an empty message list hashes to the empty string. Any "skip if the
  signature is unchanged" shortcut must not reuse that value as its reset key,
  or switching from a chat with messages to an empty chat leaves the previous
  transcript on screen. The pane is per-session state; key it to the session,
  not only to the message list.
- The OpenCode session list includes child sessions (`parentID`) created by the
  `task` tool. They are selectable like any chat; the pane must show their own
  (possibly empty) message list, never the parent's.

## Separate native terminal

- A local Ghostty window attaches through the same authenticated session routes
  and takes over the one terminal size. It receives the caller credential on an
  inherited anonymous descriptor, never in its command line, URL, log, or an
  on-disk credential file. Read-only and forwarded requests cannot start it.
- `test/native-terminal/interactive.sh` exercises actual PTY input, resize,
  close and host identity in isolated XDG roots. Its OpenCode mode does not
  submit a prompt. `test/native-terminal/launch.sh` starts the real Ghostty
  process and checks attach, resize and PTY survival. The hostile fixture checks
  the byte filter, not the physical clipboard or the GUI context menu.
- Clipboard is the part users notice. OpenCode copies with OSC 52, so a
  `clipboard-write=deny` window reports "copied" and changes nothing; the owner
  called that abnormal. Denying silently and allowing silently are both wrong.
  Forward a bounded, validated OSC 52 write and run Ghostty with
  `clipboard-write=ask`: the user sees the exact content in "Authorize Clipboard
  Access" and decides per copy. Reads, PRIMARY targets and malformed payloads
  stay inert, and the browser client stays fully inert. Do not widen this to
  `allow` without recording the clipjacking risk.
- A TUI that enables mouse reporting (OpenCode sets 1000/1002/1003/1006) also
  captures drag selection. `mouse-shift-capture=never` makes Shift always
  extend Ghostty's selection, so the context menu Copy has something to copy.
  Without it, Shift is sent to the application and the menu copy path appears
  broken.
- A byte filter must not treat C1 bytes as introducers: 0x9B also occurs as a
  UTF-8 continuation byte (U+041B is D0 9B), so doing that corrupts text. The
  filter handles ESC-introduced OSC/DCS/APC/PM/SOS and CSI, bounds each control
  string, and was tested at every chunk split, including oversized and
  malformed payloads.
- A readiness byte on an inherited fd is what distinguishes "window opened" from
  "attached": the launch waits for it, and kills its own child on failure or
  caller cancellation. A repeated request with the same idempotency key returns
  success without opening a second window; a failed launch can retry.
- The UI only enables the native launch for local desktop sessions, and only
  when `/state.nativeTerminal.available` is true. The daemon probes the host
  (Linux, a graphical session, Ghostty on `PATH`) and caches the answer for
  30 s; a missing or unsupported Ghostty hides the button instead of offering
  a failing action. The current implementation refuses Ghostty versions other
  than 1.3.1, because the escape and configuration behavior was not checked for
  other versions. The filter and version gate must be reviewed together when
  upgrading.

- An in-app pane is the attach client inside a native emulator, not a second
  streaming implementation: VTE owns the grid and PTY size, `native-attach`
  keeps the audited stream/input/resize/redraw path and the SS-13 filter. Do
  not feed raw stream bytes to VTE directly; that would bypass the filter and
  the single size owner.
- The Wails shell loads a page served by the daemon, so the Wails JS runtime
  (`window.wails`, `Events`) is absent even though the window is native. A page
  bridge must use a WebKit primitive the shell controls: register
  `window.webkit.messageHandlers.dfNative` and inject the shim on every
  `load-changed`. Do not build the shell around Wails bindings that only exist
  for pages served by Wails' asset server.
- A VTE pane has no OSC 52. The attach filter may forward a validated write for
  Ghostty's `ask` dialog, but VTE ignores it, so a TUI's own copy is inert in
  the in-app pane while the context menu still copies. State this rather than
  claiming copy parity; do not add an unfiltered VTE clipboard hook to close
  the gap.
- Native UI acceptance can be automated when the app runs on XWayland: drive
  it with XTEST through libX11/libXtst from Python `ctypes` (no package), and
  read the clipboard with `wl-paste`/`xclip`. Focus is the trap: log
  `gtk_window_get_focus` or click twice before asserting a key path. This is
  test scaffolding, not shipped code.
- Generation skew defeats everything else: after a full day in which real fixes
  "did not work", the causes were an installed daemon three commits behind the
  tree, a session host six hours older and executing a deleted inode, and test
  windows running unlinked binaries. Match `/proc/<pid>/exe` for every process
  in the serving path and remove `(deleted)` windows before believing any A/B.
- A streaming fix split across daemon and host only works when both come from
  the same build; a daemon-only restart attaches a new daemon to an old host
  and changes the symptom, not the diagnosis. Replace the pair together and
  verify the version string in `status --json`.
- Judge a lag report against the surfaces it names. On 2026-11-02 the phone and
  browser clients were fast on the identical transport while the WebKitGTK
  desktop view lagged, which single-handedly excluded the bridge and backend;
  the in-app VTE pane then streamed the same session smooth at 30 KiB/s. Do
  not rewrite a framework when the engine is the differential — see
  [the terminal lag post-mortem](reviews/terminal-lag-2026-11-02.md).

## Related

- [Requirements](requirements.md) — SS-3/SS-4/SS-6/SS-7, DP-1/DP-6/DP-8/DP-9/DP-13/DP-17/DP-19/DP-20, UI-14/UI-16/UI-17/UI-18.
- [Design](../DESIGN.md) — detached host, pairing, stream contract.
- [AGENTS.md](../AGENTS.md) — invariants and landmines.
- [Orca lessons](orca-lessons.md) — the reliability evidence behind the host and
  terminal choices.

## Pairing and stream hardening (2026-11-09)

- Confirm the host fingerprint before transmitting the pairing code. Seal that
  code to the confirmed hybrid key exchange. A signed public bundle alone does
  not stop a transport from replacing client ephemeral material and stealing a
  token when the code itself is sent in the clear.
- Derive and validate pairing keys before persisting a device. Invalid low-order
  points must return a client refusal without creating a device.
- Authenticate stream completion. Frame authentication and sequence numbers
  cannot distinguish a deliberately truncated prefix from a complete response.
- Retain unusable pinned host rows. Missing bundle fields are an error to show,
  not permission to discard the identity and begin trust from scratch.
- Run the real client code in pull-request checks, and fuzz untrusted protocol
  parsers as well as terminal output. A dependency scan detects known
  advisories; it cannot establish that custom protocol logic is safe.
