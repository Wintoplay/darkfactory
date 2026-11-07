# UI gate

The UI gate exists because "the UI looks wrong" was not checkable. Four
different `app.js` builds all reported `ui 2026-11-01.1`, a daemon kept serving
the UI it was compiled with, and no test compared the served bytes or the
layout. The gate names the build and makes the layout contract executable.

## Two halves

**Static, in `make check`.** `internal/httpapi/ui_test.go`:

- `UI_VERSION` in `web/app.js` equals every `?v=` query in `web/index.html`;
- every element id `app.js` reads through `$('…')` or `getElementById('…')` is
  defined in the shell or in a template string `app.js` writes;
- `web/index.html`, `web/style.css`, and `web/app.js` contain no absolute URL,
  and every script/link reference is root-relative (`/…`).

This runs in a second, needs no browser, and catches the two failures that are
pure text: a version bump that missed the cache query, and a renamed id that
turns into a runtime null.

`make ui-check` also fails if `web/app.js` contains `innerHTML`,
`insertAdjacentHTML`, `outerHTML`, `document.write`, `new Function` or `eval(`.
The DOM-only message rule (UI-22) used to be convention; a future edit that
reached for HTML would now fail before it shipped.

**Hostile output, `make ui-gate`.** `test/browser/hostile.cjs`, driven by
`test/browser/probe.sh hostile`, runs a fixture that prints what a compromised
process would: an OSC 52 clipboard write and read, OSC 0/1/2 title and icon
sets, an OSC 8 hyperlink, iTerm2/kitty/Sixel images, palette and colour sets
(OSC 4/10/11/12 and the 104/110/111/112 resets), and colour, device-attribute
and DECRQSS queries. Three of the sets are split across separate writes so the
client's chunk-boundary handling is exercised, not assumed. The probe asserts:

- no clipboard call and no title change;
- a click on the OSC 8 run neither navigates, opens a page, nor raises a dialog;
- no image element and no `data:`/`blob:` reference appears;
- no external request and no CSP violation;
- no element in the terminal has a repainted background;
- the five allowlisted replies (OSC 10, 11 and 4 queries, DA1, DECRQSS) still
  reach the PTY, so filtering did not simply swallow everything.

It runs at a fractional device scale, which keeps the DOM renderer where the
link run is a span and the palette is a computed style. Pointed at a daemon
built before the filter, it fails with `terminal output repainted the palette`.

`make ui-gate` does not measure latency. `test/browser/probe.sh echo` does, with
no threshold; the recorded before/after of the input-scheduling and small-write
changes is in [client validation](client-validation.md).

**Browser, `make ui-gate`.** `test/browser/gate.cjs`, driven by
`test/browser/probe.sh gate`, signs in to an isolated daemon and a real PTY and
repeats the checks at 1280×900, the 1200×800 Wails default, and 396×719:

| Check | Desktop | Phone |
|---|---|---|
| Fleet sidebar | visible | hidden in the session view |
| Phone nav | hidden | visible |
| Status bar | visible | hidden |
| Terminal height | ≥ 45% of the viewport, ends above the status bar | ≥ 40% of the viewport, ends inside it |
| Session-tab `+` | — | fully on screen |
| App controls | — | every visible one is ≥ 16×16 CSS px |
| Drag grips | repo and workspace handles ≥ 16×16; mouse reorder | same, driven by touch; lone-workspace grip moves the repo |
| Horizontal scroll | none | none |
| Session tabs / pane / xterm | visible | visible |

It also asserts that the footer names both `UI_VERSION` and the daemon `build`,
that `/api/v1/state` returns a non-empty `build`, that `/app.js` is sent with
`Cache-Control: no-store`, and that its SHA-256 equals `web/app.js`.

Before opening the browser it seeds two extra one-workspace repos and two extra
worktrees in the probe repo, so every viewport has the layout that used to have
no movable row. Each viewport then checks fleet reordering:

- drag the last repo header grip to the top (mouse on desktop/narrow, CDP touch
  on phone) and assert the server and the DOM agree;
- reload and assert the order survived, because it is `Repo.Position`;
- focus a repo grip and press `↓` on desktop;
- drag a one-workspace repo’s row grip and assert its whole repo group moves;
- drag the last worktree in a multi-worktree repo above the first and assert
  only that row moves;
- measure every `.repo-grip` / `.wt-grip` and fail below 16×16 CSS px.

This is the regression test for the “I cannot reorder workspaces on my phone”
report: the old gate only exercised one repo with one workspace, where a
within-repo drag could not change anything. `test/browser/lib.sh` now sets
`XDG_DATA_HOME` inside the probe root, so seeded worktrees cannot land in the
operator’s real data directory.

This is a contract, not a pixel diff: fonts, GPUs, and antialiasing differ
across machines, so a golden screenshot would fail for reasons that are not
regressions. `DFB_KEEP=1` keeps the root and `ui-gate-{desktop,phone}.png` for
inspection.

## Build identity

- `make build` stamps `internal/httpapi.Build` and `main.commit` from
  `git describe --always --dirty`.
- `darkfactory version` and `darkfactory status` print the daemon's build;
  `status` reads it from `/state`, not from the client binary.
- `make install-status` compares the installed binary's build with the tree's,
  so a swap that was not followed by a restart says so instead of claiming the
  daemon is current.
- `/api/v1/state` carries `build`; the footer prints
  `ui <UI_VERSION> · <version> (<commit>)`.

A stale installed daemon now says so: its `/state` has no `build` (or an old
one), its footer lacks the current `UI_VERSION`, and the gate fails on the
served hash before any native test blames the shell.

## When `web/` changes

1. Bump `UI_VERSION` (`web/app.js`) and every `?v=` query (`web/index.html`)
   together. `make check` fails if they drift.
2. Run `make check` for the static half.
3. Run `make ui-gate` with Playwright/Chromium available; see
   [test/browser/README.md](../test/browser/README.md).
4. `make install-restart` (or `systemctl --user restart darkfactory`) before
   judging the app in a browser or in Wails. The daemon embeds `web/`.

Negative controls: point `DFB_BIN` at an older installed binary — the gate fails
with `the daemon does not report a build`; revert the `?v=` bump — the Go test
fails; remove the pinned add-control rule — the phone check fails.

## Limits

- It is not a pixel-parity test and does not compare against Orca's UI. There is
  no such target in this repository; `docs/ui-demo.html` is a design reference,
  not the served app.
- It does not prove Android/iOS keyboards, a real device, Wails' WebKit renderer,
  or agent output quality. Those remain the UI-24/UI-25 gates.
- It needs Playwright and Chromium and is therefore not part of `make check`.
  The static half still fails closed on every run. CI installs the pinned
  `tools/clientbench` Playwright module and runs `make ui-gate` in its own job;
  locally the gate is opt-in.
