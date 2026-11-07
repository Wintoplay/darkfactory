# Compare browser and Wails clients

Use the same vendored xterm workload to check the evaluation tools, then test
the production UI against an isolated daemon. The synthetic fixture contains
no PTY, HTTP streaming, agent, authentication, or OpenCode service. Its result
cannot establish phone usability or select a desktop framework on its own.

## Prepare

```sh
make clientbench-build clientbench-check
cd tools/clientbench
npm ci
npx playwright install chromium
cd ../..
make desktop-check desktop-build
```

The browser dependency is development-only and pinned in this tool directory.
Wails has a separate module lockfile and needs the platform native SDK. See
[desktop/README.md](../desktop/README.md). Do not install system packages or
change daemon dependencies just to make the optional shell build.

## Run an isolated trial

On a Linux desktop with a working display:

```sh
python3 tools/clientbench/run.py --fixture bin/clientbench --label browser \
  --out /tmp/df-browser-01 -- node tools/clientbench/browser.cjs '{url}'
python3 tools/clientbench/run.py --fixture bin/clientbench --label browser \
  --out /tmp/df-browser-webgl-01 -- node tools/clientbench/browser.cjs '{url}?renderer=webgl'
python3 tools/clientbench/run.py --fixture bin/clientbench --label wails \
  --out /tmp/df-wails-01 -- bin/darkfactory-desktop --url '{url}'
# WebGL inside the native shell; the shell's URL guard refuses a query, so the
# fixture's path form is used.
python3 tools/clientbench/run.py --fixture bin/clientbench --label wails \
  --out /tmp/df-wails-webgl-01 -- bin/darkfactory-desktop --url '{url}webgl'
```

The runner starts a new loopback fixture server and a new client process group.
It waits for output validation, observes the client after completion, writes
JSON plus logs to a new directory, and stops only those process groups. It
does not connect to a user's daemon or share a browser profile. An existing
output directory is refused. Inspect and retain both logs on failure.

For a tool-only smoke in a headless environment:

```sh
DF_BENCH_HEADLESS=1 python3 tools/clientbench/run.py \
  --fixture bin/clientbench --label browser --out /tmp/df-browser-smoke \
  -- node tools/clientbench/browser.cjs '{url}'
```

`DF_CHROMIUM` may point at an installed compatible Chromium executable.
`DF_PLAYWRIGHT_MODULE` may point at an installed Playwright package.
`DF_DPR=1.25` runs the fixture at a fractional device scale factor, which is
the case where the WebGL cell box can clip glyphs. Record these overrides with
the artifact. Headless Chromium versus a visible native WebView is not a fair
framework comparison.

## Read the observations

- `readyMs` is navigation start to terminal construction in the page. It is
  not process launch to first visible pixel.
- `launchToResultSeconds` includes process startup, navigation, workload,
  completion polling, and observation overhead. It is not startup latency.
- `parseMs` measures 1,049,600 ASCII output bytes through serial xterm write
  callbacks. Those callbacks prove parsing, not display presentation. `renderMs`
  is the interval from the parse callback to the next `onRender` after the
  final sentinel; `finalRenderMs` starts at workload start. `onRender` proves
  the renderer drew a frame, not that the compositor presented it to the
  display. The fixture waits for that frame and checks the final buffer
  sentinel.
- `renderer` is the GL renderer string when the fixture was loaded with
  `?renderer=webgl`, or `dom`. A WebGL result on `SwiftShader`/`llvmpipe` is a
  software path and does not answer the GPU question.
- `echoWriteMs` measures 30 synthetic `term.input` to echo-write callbacks.
  It excludes OS input, network, the PTY, an agent, and display latency.
- Each memory sample sums Linux PSS across descendants of the launched client.
  Shared pages are apportioned rather than counted as full RSS per process.
  Unreadable or exited processes are listed. Wails WebKit processes detached
  from this ancestry can be missed. Audit the process tree before reporting
  memory or extend the runner to a dedicated cgroup on the measurement host.
- The fixture validates byte count, sentinel, and echo count. A crash, timeout,
  or incorrect result exits nonzero. `smoke_passed` means only the tool flow
  completed. Inspect unreadable memory samples before trusting memory values.

The JSON records source hashes, root revision, local diff hash, fixture and
client executable hashes, overrides, workload, engine user agent, viewport,
DPR, and command template. Retain
the working tree or patch alongside artifacts if it differs from the revision.

## Decide only after the production flow

Use release builds, the same display geometry, GPU configuration, font, terminal
renderer, output fixture, and cache conditions. Record GTK, WebKit, Chromium,
Wails, kernel, and hardware versions. Check host load and available CPUs.
Run at least five trials per side in alternating order. Report raw runs,
median, and range. Treat differences within run variation as inconclusive.

Next, exercise UI-24/UI-25 against the same isolated daemon and real PTY. Check
session creation, interaction, mode restoration, auth, reconnect, backgrounding,
and window close/reopen. Measure actual keyboard-to-visible-echo latency and
first visible usable screen separately with platform-appropriate tracing or
video. Verify terminal bytes and session identity, not only a screenshot.

Profile separately from reported trials if one client is slow. Determine
whether parsing, rendering, transport, scheduling, or application work limits
the result. Do not infer a speedup from bundle size, framework marketing, or
Bun-versus-Node claims. Until both clients and these gates run, the verdict is
inconclusive.
