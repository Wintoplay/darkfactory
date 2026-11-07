# Terminal surface audit (2026-11-02)

This is an internal source audit of the browser terminal, not a security review
and not a decision to replace xterm.js. It records what the shipped client
enables today, which gaps matter for the hostile-text attacker in
[the threat model](../threat-model.md), and the assertions a replacement engine
would have to pass. It supports [the terminal evaluation](../terminal-evaluation.md).

Scope: `web/app.js`, `web/vendor/xterm.js` (0.19-era bundle), `web/index.html`,
`internal/httpapi` headers, `internal/session` bounds, and the existing tests.
No code was changed for this audit; no network access was used.

## What is enabled today

One `Terminal` is constructed at `web/app.js:4150` with `allowProposedApi:
false`, `convertEol: false`, `scrollback: 5000` (forced to `0` for a TUI at
`web/app.js:3981`), `linkHandler` with a no-op `activate`, and
`windowOptions: {}`. Only two addons load: `addon-fit` always and `addon-webgl`
lazily under the platform, DPR and software-renderer rules at
`web/app.js:4058-4138`. No clipboard, links, search, unicode11, image or
serialize addon is vendored.

PTY output reaches the page only through `terminal.write()`
(`web/app.js:4351-4372`). `web/app.js` contains no `innerHTML`,
`insertAdjacentHTML`, `document.write` or `DOMParser`, and the sandboxed
message renderer builds every node with `createElement`/`textContent`
(`web/app.js:2736-2780`, requirement UI-22). The bundle itself is text-node
based and never touches `document.title`.

Reachable escape behaviour, and what controls it:

| Sequence | Today | Control |
|---|---|---|
| OSC 52 clipboard write/read | Not reachable | No handler and no clipboard addon; `navigator.clipboard` is only `writeText` behind a user click (`web/app.js:938-943`, `1410-1411`) |
| OSC 0/1/2 title, icon | Parsed, ignored | `onTitleChange` swallows it (`web/app.js:4175`); the title is static in `web/index.html:10` |
| OSC 8 hyperlinks | Rendered, inert | The no-op `linkHandler` (`web/app.js:4167`); the bundle's fallback would `confirm()` then `window.open()` and set `location.href` |
| OSC 4/10/11/12 colours | **Applied and answerable** | The bundle's colour handler mutates the theme and replies to `?` queries |
| OSC 1337, kitty, Sixel | Discarded | No APC handler; unknown DCS is converted and dropped |
| DECRQSS, DA1/DA2, DECRQM | Answered into the PTY | Bundle handlers; replies leave through the same `onData` path as keystrokes |
| Mouse 9/1000/1002/1003, SGR 1006/1016 | Enabled on request | 1005/1015 are explicitly unsupported |
| Focus 1004, bracketed paste 2004 | Enabled on request | — |
| Window ops `CSI t` | Ignored | `windowOptions: {}` |

The same `onData` path (`web/app.js:4173`) carries both keystrokes and
emulator replies; the daemon cannot distinguish them and records both as
`verbSessionInput`.

## Gaps, ranked

1. **No escape allowlist (SS-13 is still open).** `docs/requirements.md`
   already says so: xterm.js parses the full set and three settings (no OSC 8
   action, no window options, no clipboard addon) are configuration, not a
   policy. Each is one line from regressing, and nothing tests them.
2. **Output-driven palette mutation is accepted.** OSC 4/10/11/12 lets a
   process repaint foreground, background and cursor. Setting foreground equal
   to background hides text, and spoofing an agent's prompt colours is a
   deception primitive. This is the strongest surviving effect of hostile
   output and SS-13's wording (OSC 52, OSC 8, title) does not cover it.
3. **OSC 8 safety is one no-op function.** Removing `linkHandler` restores
   navigation. No test pins the behaviour.
4. **No test covers any of the above.** `make check` asserts replay policy and
   mode restore (`internal/session/attach_test.go`, `modes_test.go`) but nothing
   about clipboard, title, links, images, palette, or reply bytes.
5. **Reply traffic is indistinguishable from input.** Short and mostly fixed,
   but a replacement engine must keep an equally narrow reply set.
6. **Bounds are per component, not end-to-end:** 4 MiB daemon ring, 256 KiB
   replay, 32 KiB stream chunks, 256 KiB client staging, 10 MB per OSC/DCS
   sequence, 5000 scrollback lines.
7. **The CSP is tuned to xterm today.** `default-src 'self'` with a style nonce
   (`internal/httpapi/server.go:596-624`) blocks eval, WASM (`'wasm-unsafe-eval'`
   is absent), `data:`/`blob:` images, remote fonts and CDN assets, and there is
   no COOP/COEP, so no shared memory. Any engine that needs WASM, workers with
   `blob:`, `data:` images or a CDN font would force a CSP change, which is a
   security decision and must be reviewed rather than widened incidentally.
8. **`altClickMovesCursor` is left at its `true` default,** so Alt+click injects
   arrow keys into the PTY. Unreviewed input path.
9. **DOM-safety is convention.** No lint or gate bans `innerHTML`; `make
   ui-check` only syntax/type-checks.

## Assertions required of any engine (including xterm.js)

Each is scriptable with a hostile fixture shell, `test/browser/input.log` as
PTY-side truth, Playwright page assertions and request interception:

1. OSC 52 (write and `?`) makes zero clipboard calls and appends zero bytes to
   `input.log`.
2. OSC 0/2 and OSC 1 change no `document.title`, element text, or favicon.
3. OSC 8 renders text that cannot navigate: no `window.open`, no `confirm`, no
   `location.href` change; non-`http(s)` URIs are not links.
4. OSC 1337, APC kitty and DCS Sixel create no image, no `data:`/`blob:` URL,
   and no network request.
5. Every sequence that emits bytes has an allowlisted exact reply (DA1
   `ESC[?1;2c`, DA2, DECRQSS, DECRQM, colour queries, focus, mouse, bracketed
   paste); everything else emits zero bytes.
6. Palette sequences may change only colours produced by a validated colour
   parser; synchronized output auto-flushes; `CSI t` neither resizes nor
   replies.
7. No PTY byte reaches the DOM outside the grid; every engine-injected
   `<style>`/`<link>` carries the CSP nonce.
8. Hostile load stays bounded: a 10 MB OSC/DCS, escape-dense output and a long
   stream must not grow queues without limit or wedge rendering.
9. Parity with today's numbers: scrollback, replay trim, stream chunk, input
   body, staging cap and mode restore (`internal/session/modes.go:178-204`).
10. The engine runs unmodified under the current CSP string, with zero
    `securitypolicyviolation` events and zero outbound requests; any required
    CSP widening is an explicit reviewed diff.
11. No clipboard read, no `window.open`, no `document.write`, no cross-origin
    `fetch`, and storage limited to the documented font preference.
12. A static gate in `make ui-check` bans `innerHTML`, `insertAdjacentHTML` and
    `document.write` in `web/app.js`.

## Consequences for the evaluation

A replacement engine is justified by comparable measurements and a smaller
reviewed surface, not by its language or GPU use. Before any comparison, the
cheap work is to close gaps 1-4 and 12 on xterm.js itself, so the baseline is a
policy rather than a habit. Downloading pinned `ghostty-web` or `riotermjs`
builds needs explicit approval; nothing was fetched for this audit.

## Status after this audit

Implemented in the same working tree:

- Gap 2 is closed for the palette. `web/app.js` (`filterPaletteOsc`) drops
  output-driven OSC 4/10/11/12 sets and the 104/110/111/112 resets before they
  reach xterm, and still forwards colour queries so a TUI can detect the theme.
  It holds a sequence split across stream chunks and drops an unterminated one
  past 4 KiB.
- Gaps 3 and 4 are covered. `test/browser/hostile.cjs` asserts the clipboard,
  title, link, image, external-request, CSP and palette guarantees, and that
  the allowlisted replies still arrive. `make ui-gate` runs it after the layout
  gate. Pointed at a pre-filter build it fails with
  `terminal output repainted the palette`.
- Gap 9 is closed. `make ui-check` greps `web/app.js` for `innerHTML`,
  `insertAdjacentHTML`, `outerHTML`, `document.write`, `new Function` and
  `eval(`.

Still open: gap 1 (the full escape allowlist — every other sequence is still
parsed by xterm), gap 5 (reply traffic is not separated from keystrokes), gap 6
(bounds are per component), gap 7 (the CSP is still xterm-shaped), gap 8
(`altClickMovesCursor` remains at its default).

## Native window addendum (2026-11-02)

The separate Ghostty 1.3.1 window is not the browser client and does not share
its clipboard outcome. A live check showed that OpenCode's copy action uses
OSC 52 and therefore reported "copied" while writing nothing when the window
started with `clipboard-write=deny`. The owner rejected that as abnormal
behaviour. The current policy is:

- `clipboard-read=deny`: OSC 52 reads stay inert.
- `clipboard-write=ask`: `internal/nativeattach/filter.go` forwards only a
  `OSC 52 ; c|"" ; <base64>` write, validates the payload and bounds the whole
  sequence at 1 MiB (`maxClipboard`). Ghostty shows "Authorize Clipboard
  Access" with the content and the user allows or denies it. Other targets,
  empty payloads, `?` reads, invalid base64 and oversized writes are dropped.
- `mouse-shift-capture=never`: Shift always extends Ghostty's selection, so a
  TUI that enables mouse reporting cannot swallow Shift+drag; the context menu
  Copy/Paste remains the manual path.
- The right-click context menu Copy/Paste was observed working in a real plain
  shell window, and the owner accepted the per-copy OSC 52 authorization design
  in place of the silent denial.

This does not close gaps 1, 5 or 6: the filter is still a denylist-shaped
targeted filter, OSC 52 authorization is a Ghostty feature whose behaviour was
checked on 1.3.1 only, and the bound is per sequence.
