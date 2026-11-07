# Handoff: separate Ghostty terminal

## Current decision and status

Follow the model policy in `AGENTS.md` (DeepSeek V4.1 flash for OpenCode, medium
reasoning by default). Do not pick a premium fallback.

The owner accepts a separate native terminal window. The working tree contains an
implementation that is **unfinished, not deployed and not security-certified**.
Preserve all pre-existing uncommitted changes. Do not install or restart an
operator service to test it. Do not describe it as released.

The right-click **copy/paste menu** was verified in a real window. The owner
also hit the OpenCode copy problem: OpenCode copies with OSC 52, so a window
started with `clipboard-write=deny` reported "copied" and changed nothing. The
owner rejected that as abnormal. The current policy is a per-copy authorization
dialog, which the owner accepted:

- `clipboard-read=deny` — OSC 52 reads stay inert.
- `clipboard-write=ask` plus a narrow filter that forwards only a bounded,
  base64-validated `OSC 52;c;<payload>` write. Ghostty shows "Authorize Clipboard
  Access" with the content; the user allows or denies each write. This keeps
  normal TUI copy without allowing a silent clipboard overwrite.
- `mouse-shift-capture=never` — Shift always extends Ghostty's selection even
  when a TUI enables mouse reporting.

## What was observed in a real window (2026-11-02)

- A disposable bash session in the checkout: right-click **Copy** and **Paste**
  work (owner report).
- A disposable OpenCode TUI: **Paste** works; OpenCode's own copy failed under
  the old deny policy and reported success anyway (owner report). This is the
  finding that changed the policy.
- OpenCode's standalone TUI was verified from its PTY bytes to enable mouse
  modes 1000/1002/1003/1006 and bracketed paste 2004.
- The owner accepted the authorization-dialog design. Shift+drag plus the
  context-menu Copy inside OpenCode was not separately re-confirmed after
  `mouse-shift-capture=never` was added; a future agent should ask for that one
  observation rather than claim it.
- The test windows were disposable: one daemon, one session host and temporary
  XDG roots per run. No operator session or service was touched and no model
  prompt was submitted.

## Implementation in this tree

- `web/index.html` and `web/app.js` add a local desktop **Ghostty** button for a
  live, workspace-scoped session. UI assets are version `2026-11-02.26`. The
  button preserves a launch idempotency key until success, re-renders in
  `finally`, and is hidden on the phone layout and on non-loopback origins.
- `internal/httpapi/native_terminal.go` and the route table add audited
  `POST /api/v1/sessions/{id}/native`. The handler requires the local UI
  boundary and a running session. It starts Ghostty as a separate process and
  accepts only Ghostty 1.3.1, the version exercised in the probes. Flags:
  `--gtk-single-instance=false --clipboard-read=deny --clipboard-write=ask
  --link-url=false --image-storage-limit=0 --right-click-action=context-menu
  --mouse-shift-capture=never --confirm-close-surface=false`. Confirm every flag
  against the installed version before widening the version gate; in 1.3.1,
  `image-storage-limit=0` disables image protocols and `ask` is a real
  authorization dialog.
- The caller's existing bearer or cookie credential is passed through inherited
  anonymous fd 3, not through argv, URLs, environment variables, logs, or an
  on-disk credential file. The attach client uses that same principal for the
  existing stream, input, resize and redraw routes. fd 4 reports readiness
  after attach, resize and any required redraw. The launch handler waits
  `nativeReadyTimeout` (10 s), and kills its own Ghostty child on readiness
  failure or caller cancellation. The in-memory launch deduplication cache keys
  on principal, session and request ID; success is kept for 10 minutes, the map
  is capped at 2048 entries, and a failed intent can retry.
- `cmd/darkfactory/main.go` adds `native-attach` and
  `internal/nativeattach/attach_linux.go` implements raw terminal input,
  stream output, resize and detach. The terminal is restored on exit. A new
  attach takes over the existing single browser/native viewer. The host keeps
  the PTY when the window closes.
- `internal/nativeattach/filter.go` filters terminal output. It blocks OSC
  strings except colour queries and an authorized-write-shaped OSC 52, blocks
  APC/PM/SOS, DCS except DECRQSS, and CSI window operations. It handles split
  sequences and bounds control strings at 4 KiB, except a clipboard write,
  which is bounded at 1 MiB (`maxClipboard`). Clipboard reads, PRIMARY/cut
  buffers, empty payloads, invalid base64 and oversized writes are dropped.
  The filter is byte-oriented on purpose: treating 0x9B as a C1 introducer
  would corrupt UTF-8 such as U+041B (D0 9B).
  This is a targeted filter, **not** the complete SS-13 escape allowlist and not
  a security certification. The native window is more permissive than the
  browser client, which remains fully inert for OSC 52. Review the filter,
  terminal replies, paste safety and resource bounds against
  `docs/reviews/terminal-surface-audit-2026-11-02.md`.

## Evidence on the final tree

All live tests used disposable daemons, PTYs and XDG roots. Final binary:
`/tmp/df-handoff-bin4`, SHA-256
`46e2b314186ddd27869422bec91e281054b8c33fd2c4bdb4e1f7ee0c493430c1`.

| Check | Result |
|---|---|
| `go test -race ./internal/httpapi ./internal/nativeattach` | pass; caller credential fd, local-only access, read-scope denial, recorder failure, version gating, readiness-failure kill, cancellation kill, duplicate launch and failed-launch retry, filter split/bounds/UTF-8/clipboard cases |
| `test/native-terminal/interactive.sh` (bash) | `ok`, input, resize, identity preserved |
| `... DFB_TEST_OPENCODE=1` | `ok`, input, resize, OpenCode accepted a test string without submitting a prompt |
| `... DFB_TEST_HOSTILE=1` | filter pass; reads/titles/links/images/palette still dropped, the OSC 52 write is forwarded to the dialog |
| `test/native-terminal/launch.sh` | `apiLaunch`, `nativeAttachAndResize`, `identityPreserved` |
| `test/native-terminal/ui-handoff.sh` | `nativeTakeover`, `browserReattached`, `browserInput` |
| `make check` | exit 0 on the final tree |
| `make ui-gate` | PASS at 1280×900, 1200×800, 396×719; UI `2026-11-02.26`; served `app.js` matches `web/app.js`; hostile browser probe 12 canaries and 5 allowlisted replies |
| `make desktop-preflight` / `make desktop-build` | native SDK present; `bin/darkfactory-desktop` built |

The editable OpenCode and bash fixtures used for the manual probe lived in
`/tmp/user/1000/opencode/` and are not part of the repository. `launch.sh`
holds a window for a manual check with `DFB_HOLD_FOR_MENU=1` and
`DFB_HOLD_SECONDS` (default 300).

## Next agent: remaining work, in this order

1. Re-run the real-window check inside OpenCode with the final flags: Shift+drag
   should select with Ghostty, right-click **Copy** should reach the clipboard,
   and OpenCode's own copy should show Ghostty's authorization dialog. Record
   exactly what the owner reports. Do not overwrite the clipboard without their
   participation.
2. Exercise the native Wails window (`bin/darkfactory-desktop --url
   http://127.0.0.1:PORT/`) against an isolated daemon, and a real OpenCode
   turn with streaming and typing. The existing tests use synthetic input or a
   PTY driver; they do not prove keyboard latency, screen presentation, the
   context menu, clipboard safety or a full agent turn in the Wails shell.
3. If any of that changes the design, update UI-29, SS-13/NF-S-7, `DESIGN.md`,
   `docs/substrate-lessons.md` and the terminal audit with the new evidence.
   Keep the browser client's OSC 52 inert.
4. Do not publish, install or restart a service. The owner has not approved a
   deployment here.

## Rerunnable commands

Use a host that permits loopback TCP and Unix sockets, with a graphical Linux
session for the Ghostty probes. None of these installs or restarts the operator
daemon. Set `DF_PLAYWRIGHT_MODULE` and `DF_CHROMIUM` to locally available
binaries as needed.

```sh
GOCACHE=/tmp/df-handoff-go-cache go build -o /tmp/df-handoff-bin ./cmd/darkfactory
DFB_BIN=/tmp/df-handoff-bin test/native-terminal/interactive.sh
DFB_BIN=/tmp/df-handoff-bin DFB_FIXTURE="$PWD/test/browser/fixture-opencode.sh" DFB_TEST_OPENCODE=1 test/native-terminal/interactive.sh
DFB_BIN=/tmp/df-handoff-bin DFB_FIXTURE="$PWD/test/browser/fixture-hostile.py" DFB_TEST_HOSTILE=1 test/native-terminal/interactive.sh
DFB_BIN=/tmp/df-handoff-bin test/native-terminal/launch.sh
DFB_BIN=/tmp/df-handoff-bin DF_PLAYWRIGHT_MODULE=/path/to/playwright DF_CHROMIUM=/path/to/chrome test/native-terminal/ui-handoff.sh
GOCACHE=/tmp/df-handoff-go-cache make check
GOCACHE=/tmp/df-handoff-go-cache BIN=/tmp/df-handoff-ui DFB_BIN=/tmp/df-handoff-ui DF_PLAYWRIGHT_MODULE=/path/to/playwright DF_CHROMIUM=/path/to/chrome make ui-gate
```

For a held menu check, point the fixture at the session you want and hold the
window open long enough for a person to answer:

```sh
DFB_HOLD_FOR_MENU=1 DFB_HOLD_SECONDS=900 \
  DFB_FIXTURE="$PWD/test/browser/fixture-opencode.sh" \
  test/native-terminal/launch.sh
```
