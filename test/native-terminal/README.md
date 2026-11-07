# Native Ghostty research probe

These scripts are test-only. They open a native Ghostty process and an isolated
GTK WebKit window on disposable DarkFactory sessions. They are not an
interactive product client. In particular, the bridge injects a test command;
it does not forward normal keyboard input or implement DarkFactory's terminal
safety policy. Do not use it with real sessions or untrusted output.

## Procedure

Each pair starts a daemon and session host under temporary XDG roots and creates
one shell PTY. Ghostty and WebKit attach sequentially to that same PTY; pair
order alternates. Each triggers 2,000 80-character lines and a distinct marker.
A separate read-only host subscription checks both markers. Session ID, PID,
start ticks, incarnation, status, and host socket inode are checked before,
between, and after clients. Each client has a new temporary profile. Ghostty
uses `--gtk-single-instance=false` and cannot reuse an operator window.

Ghostty's bridge writes all received bytes to the native terminal and asks for
a cursor-position report after the marker. The report proves the emulator
processed the preceding bytes. It does not prove screen presentation. WebKit
records an xterm write callback and waits for its marker in the terminal DOM.
The dimensions, timing endpoints and client stacks differ; do not treat this as
a controlled speed ratio. Record client errors, missing markers and inactive
windows. A real adoption decision needs matched geometry, a real keyboard,
foreground visibility, presentation timing, OpenCode interaction and streaming,
attach modes, resizing, reconnect and the hostile-output controls.

Run on a Linux display with Ghostty, GTK4, WebKitGTK 6, PyGObject and local
sockets, after building the current daemon:

```sh
GOCACHE=/tmp/df-native-gocache go build -o /tmp/df-native-current ./cmd/darkfactory
python3 test/native-terminal/compare.py --daemon /tmp/df-native-current \
  --out /tmp/df-native-pairs --pairs 5
DFB_BIN=/tmp/df-native-current DFB_OUT=/tmp/df-native-opencode \
  test/native-terminal/opencode-probe.sh
```

The OpenCode probe runs a standalone TUI with empty temporary HOME and XDG
roots, injects one test string into its input, observes the corresponding PTY
output, and does not submit a prompt. No model response is requested. The paired
runner saves per-trial results and source/binary hashes, including failures.
Every output directory must be new. The test never installs or restarts an
operator service.

See `test/artifacts/native-ghostty-2026-11-02/REPORT.md` for the executed run.

## Clipboard policy for the product window

The research bridges here are not the product client. `cmd/darkfactory
native-attach` is, and its Ghostty launch is tested by `launch.sh`. It runs
with `clipboard-read=deny`, `clipboard-write=ask`, `link-url=false`,
`image-storage-limit=0`, `right-click-action=context-menu` and
`mouse-shift-capture=never`. The byte filter forwards only a validated OSC 52
clipboard write, so Ghostty shows its authorization dialog; reads stay inert.
`launch.sh` can hold a real window for a manual menu check:

```sh
DFB_HOLD_FOR_MENU=1 DFB_HOLD_SECONDS=600 \
  DFB_FIXTURE="$PWD/test/browser/fixture-opencode.sh" \
  DFB_BIN=/tmp/df-current test/native-terminal/launch.sh
```

The hold is test-only and defaulted to 300 s (`DFB_HOLD_SECONDS` overrides it).
