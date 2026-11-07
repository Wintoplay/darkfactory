# Ghostty-web comparison

This is an isolated synthetic fixture, not a DarkFactory terminal replacement.
It compares xterm's DOM renderer with ghostty-web 0.4.0 in Wails/WebKitGTK.
Neither side connects to a daemon or session. Both receive 1,049,600 bytes in
128 writes followed by a sentinel. The fixture waits for the sentinel in the
terminal buffer, then two animation frames. It does not measure display
presentation, real PTY input, realistic TUI repaint, or the complete app.
`parseMs` in this fixture means elapsed time to observe the sentinel at an
animation frame, including scheduling. `finalRenderMs` includes two further
frame callbacks and does not prove the sentinel was presented on screen.
Write callback times are not comparable: ghostty-web schedules its callback on
a frame, whereas xterm's callback has different semantics. Do not treat the
previous fixture's serial-write results as comparable to this fixture.

## Plan

Use five alternating trials for each engine in the same native shell and on the
same host. Check the sentinel and report the median and range, all failures,
engine versions and hashes. Treat overlapping variation as inconclusive.
If fixture code or candidate assets change, rerun both sides. A speed claim
requires real PTY echo, presentation, realistic TUI workloads, and profiles;
production adoption also requires the security and interaction matrix in
`docs/terminal-evaluation.md`.

## Reproduce

Fetch the exact npm package to a temporary directory, verify SHA-256, and unpack:

```sh
npm pack ghostty-web@0.4.0 --pack-destination /tmp
sha256sum /tmp/ghostty-web-0.4.0.tgz
# expected 90bf473b6c7f43ab5e52ee98d8295e04fb1c6b07b928e9795489df1e8cb8802e
mkdir -p /tmp/df-ghostty
tar -xzf /tmp/ghostty-web-0.4.0.tgz -C /tmp/df-ghostty
go build -o /tmp/df-clientbench ./tools/clientbench
python3 tools/clientbench/ghostty/compare.py --fixture /tmp/df-clientbench \
  --ghostty /tmp/df-ghostty/package/dist --desktop bin/darkfactory-desktop \
  --out /tmp/df-ghostty-comparison --trials 5
```

Build the Wails executable separately with `make desktop-build` if needed. The
runner requires a display and local TCP sockets and creates new XDG directories
beneath its new output directory. It explicitly disables daemon autostart.
Results, program hashes, source hashes, and errors are recorded per trial.

The executed record is `test/artifacts/ghostty-web-2026-11-02/REPORT.md`.
