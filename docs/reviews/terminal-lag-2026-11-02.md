# Terminal lag post-mortem (2026-11-02)

The owner's OpenCode terminal streamed slowly inside the Linux Wails desktop
shell while a model ran. Several fixes appeared to fail. This doc records why
they failed, what actually fixed the symptom, and the rules that keep the loop
from repeating. Facts here were verified against running processes on this
host today; the acceptance gaps are listed at the end and have not been closed
by this work.

## The failure loop

The defect was never only in the code. It was in the gap between "fix exists
in a tree" and "fix executes on the operator stack". Four stacked conditions
produced the impression that fixes did not work:

1. **Fixes sat in the tree while the operator stack ran older generations.**
   The commit `6b25e34` ("Fix terminal stalls on fast PTY output") existed at
   20:34. The deployed daemon (16:40, UI `2026-11-02.25`) and live session host
   (12:41) predated the whole in-app and streaming-fix work. Nothing aimed at
   the complaint reached the serving path.
2. **Test windows ran deleted binaries.** Two `darkfactory-desktop` processes
   were executing unlinked files from earlier builds. `A/B` checks performed in
   those windows measured dead code. `/proc/<pid>/exe -> (deleted)` is the
   marker.
3. **Partial deploys changed the symptom, not the diagnosis.** Deploying the
   daemon while the host stayed old moved the stack to "new client, mid daemon,
   old host", where a streaming fix split across both processes cannot have its
   effect. One such deploy (16:40) was real but incomplete; a later full swap
   was required.
4. **Every measurement was synthetic until 21:24.** Disposable daemons,
   synthetic keystrokes, DOM markers, excluded warmups: the instruments never
   ran the reported workload (a real agent streaming into a real foreground
   window). The reports said so, but the loop treated "does not establish the
   user's lag is solved" as a footnote instead of a stop sign.

## What fixed it

- **Full-generation deployment** of the prebuilt `6b25e34` daemon, restarted
  `serve` plus a session host spawned by the *same* binary, closing the gap
  that only-serve restarts leave open. The stream batching lives in both
  processes (up to 256 KiB per flush, no exit frame for a running session,
  mirror reattach on stream drop — see `internal/session/host.go` and
  `handleStream` in `internal/httpapi/server.go`).
- **Closing stale windows.** Every deprecated process with a deleted exe is
  banished before any judging happens.
- **The A/B that convicted the engine.** The same OpenCode session streamed to
  two surfaces on the identical transport:
  - the in-app VTE pane: ~30 KiB/s delivered, shell at 5–9 % CPU, described as
    smooth while streaming;
  - the web view (WebKitGTK DOM renderer): lagged with `WebKitWebProcess` near
    0–11 % CPU — a rendering-path problem, not transport saturation.
  Phone and browser clients had already shown the same transport delivering
  fine. The conviction falls on WebKitGTK, not on Wails bridge or backend.
- **Live measurement, finally real.** Under `DF_STREAM_DEBUG=1`, the daemon
  logged per-client rates (`kind=daemon` for web, `kind=pane` for the native
  pane). A `stream_ended_early` warning covers the previously silent
  subscriber-takeover path.

## What did not change

The user-visible architecture: daemon serves one UI to every client; Wails
loads the same loopback UI. No framework migration was performed or is
recommended on the evidence of this investigation. RFC-grade alternatives
(Tauri, Neutralino, Fyne, Gio) were assessed against the measured facts in the
session and set aside: Tauri embeds WebKitGTK on Linux; Neutralino's
local-server model is the existing design; Fyne/Gio would mean writing a
terminal emulator. The native pane is the local, tested answer for the desktop
client.

## Standing rules added

- `AGENTS.md` "Refresh binaries before presenting": no demo or claim from a
  stale build; state which build the running stack executes (binary mtime,
  `status --json` version, `/proc/<pid>/exe`).
- `docs/substrate-lessons.md`: deployment skew is a correctness sink; a fix is
  not "landed" until the operator stack's generation matches the tree that
  proves it.

## Instruments left in place

- `DF_STREAM_DEBUG=1` — env-gated two-second rate line per client stream, plus
  lifetime totals at stream end (`internal/session/streammeter.go`).
- `stream_ended_early` — log warning when a subscriber closes while a stream
  is still serving; previously invisible.
- `tools/cpu-capture.sh` script was executed ad hoc during the session at
  `/tmp/user/1000/opencode/cpu-capture.sh` (1 Hz `/proc` jiffy dashbaord, no
  repo dependency). Restart it if a lag report arrives again.
- The repo-side `bin/darkfactory` was rebuilt with the Makefile's exact
  `-X main.version/-X main.commit` flags before install; a bare `go build`
  loses the version string and hides stack identity (this happened once).

## Verification boundaries that remain open

- The pane path's S4 gates are untouched: hostile-output filter, latency
  probe, and the OSC-52 copy behavior (VTE has none; context-menu copy only).
- The fresh web view under sustained stream in the new generation has not been
  re-measured; the pane result closed the daily blocker but the WebKitGTK
  engine cost remains as documented.
- `make check` and `make ui-gate` were not rerun for these instrument commits
  at the time of writing; run both before the next release cut.
- Nothing in `~/.local/state/darkfactory/` beyond the listed processes was
  modified; the operator's legacy snapshot at 16:40 built remains as-installed
  except for the binary replaced on disk at 21:24.
