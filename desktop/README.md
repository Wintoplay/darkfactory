# Desktop evaluation client (C3)

This optional Wails v3.0.0 module opens the UI served by an independent daemon.
It registers no Go services, reads no token file, and owns no PTYs or database.
Closing the window must leave the daemon and sessions running. This is an
evaluation shell, not a packaged desktop release.

The root module and its static release stay independent of Wails and cgo.
There is intentionally no `go.work` joining the two modules.

## Status

C3 has a native Linux acceptance run: the shell, the loopback-only URL guard,
its tests, and the WebKit navigation/new-window policy guard exist. The native
window **does** build and open on this host (GTK4 + WebKitGTK 6.0 is present); `make desktop-preflight` names missing pkg-config modules and
package names without installing anything. If it fails, the Debian/Ubuntu
packages are (needs the operator's approval):

```sh
# Debian/Ubuntu; review before running, it is a system change.
sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev libsoup-3.0-dev libvte-2.91-gtk4-dev
```

`libvte-2.91-gtk4-dev` is a build dependency of the Linux cgo shell (the in-app
terminal uses it); the daemon itself stays static and does not link VTE. The
in-app terminal is still opt-in at runtime with `--in-app-terminal`.

C3 native acceptance is **green on this Linux host** as of 2026-11-01: all six
checks below have recorded evidence, including terminal streaming/resize/input,
tab identity, close/reopen and daemon restart, native navigation/new-window
refusal, `--url` refusals, and the host/version record. Evidence:
`test/artifacts/client-foundation/c3-wails/native-checks/`. This is still an
evaluation shell, not a packaged desktop release; do not claim other platforms
or a packaged installer from these checks.

WebKitGTK still rejects the daemon's Secure `__Host-` cookie over plain http
loopback (Chromium stored all three probe cookies; WebKitGTK stored only the
non-Secure control). The client therefore registers a non-extractable ECDSA
P-256 key in IndexedDB after the first token login, keeps the private half
unreadable, and signs a one-use server challenge on later launches. A fresh
window and a daemon restart both reconnect without the token. This does not
weaken `Secure`/`HttpOnly`/`SameSite`/`__Host-`, does not move the bearer to
`localStorage`, and exposes no native auth bridge. Revoking the key returns the
window to Connect. Evidence:
`test/artifacts/client-foundation/c3-wails/device-key/`.

The probe and both engine logs are in
`test/artifacts/client-foundation/c3-wails/cookie-probe/`; it needs no daemon
or token. The daemon's cookie remains `Secure`, `HttpOnly`, `SameSite=Strict`,
`Path=/`, and `__Host-`; do not relax it for the shell.

## Build and open

Install the platform prerequisites from the
[Wails installation guide](https://v3.wails.io/getting-started/installation/)
if `make desktop-preflight` does not pass.

From the repository root:

```sh
make desktop-preflight   # names missing pkg-config modules; installs nothing
make desktop-build       # compiles bin/darkfactory-desktop
./bin/darkfactory serve
```

In a second terminal:

```sh
make desktop-run         # builds, then opens the default loopback URL
# or: ./bin/darkfactory-desktop --url http://127.0.0.1:8787/
# diagnostics panel: ./bin/darkfactory-desktop --url http://127.0.0.1:8787/ --debug
# experimental in-app terminal (VTE overlay, fed by native-attach):
#   ./bin/darkfactory-desktop --url http://127.0.0.1:8787/ --in-app-terminal
```

The daemon does not have to be running: by default the shell starts it with
`darkfactory launch --listen <url host> --no-browser` and waits for `/healthz`.
Pass `--autostart=false` when you point it at a fixture or a daemon you manage
yourself. Auto-start only applies to the literal loopback HTTP URLs that
`--url` already accepts. Sign in through the existing connect form. Use only an
isolated test daemon for evaluation. Do not disable cookie flags or add a native
authentication bypass to make the shell work.

`--url http://127.0.0.1:PORT/` selects a loopback daemon or the synthetic fixture.
The flag refuses hostnames, remote addresses, credentials, query strings, and
fragments. `--debug` adds `?debug=1` after validation; it is the only query the
shell adds and carries no credentials. The URL is not an interface for passing
bearer tokens or pairing codes.

No Wails Go services are registered and no Wails JS runtime is loaded: the page
is served by the independent daemon, so `window.wails` does not exist. With
`--in-app-terminal` the shell adds `?native=1` after validation, wraps the
WebView tree in a `GtkOverlay`, and uses WebKit's own
`window.webkit.messageHandlers.dfNative` channel for the page's `open`,
`bounds` and `close` messages. The page mints a session-scoped pane capability
from the daemon; the shell passes it to `darkfactory native-attach` on fd 3 and
never reads the token file. Without the flag, and whenever the pane cannot
start, the normal web terminal is used.

After load, the GTK4/WebKitGTK shell attaches a native `decide-policy` guard to
the same WebView: every later top-level navigation must stay on the validated
origin, and every new-window action is refused. An external link or
`window.open` is therefore blocked and written to stderr, not handed to an OS
browser; there is deliberately no native browser bridge. A non-Linux or non-cgo
build compiles a no-op stub and is not C3 evidence. The guard is defense in
depth, not a replacement for the daemon's origin, CSP, cookie, and token checks.

## Verify

Run the same gates as the browser, then the native ones:

```sh
make check               # includes desktop-check: go vet + target tests
make ui-gate             # desktop 1280x900, narrow 1200x800, phone 396x719
make desktop-build
```

`make ui-gate` is the shared client gate: the Wails window loads the same
`web/` assets, so a passing gate means the shell is not opening a stale UI.
It also compares the served `app.js` with `web/app.js` and prints the daemon
build, which is how a stale binary is caught before a native test blames Wails.

On a native host, C3 is not done until every line below has recorded evidence.
All six were recorded on 2026-11-01; the raw logs, screenshots, probe, and
versions are in `test/artifacts/client-foundation/c3-wails/native-checks/`.

1. Sign in through the connect form on the actual OS WebView; the first login
   provisions a device key, and a window close/reopen reaches connected with no
   token. Record the native cookie behavior honestly: WebKitGTK rejects the
   `Secure` `__Host-` cookie over plain loopback; do not weaken it. Revoking the
   key must return to Connect.
2. The terminal streams, resizes, and accepts input; a tab switch keeps the
   session id and PTY identity.
3. A daemon restart leaves the window usable after reconnect, and closing the
   window leaves the daemon and session host alive.
4. A navigation or `window.open` to another origin is refused; an external
   `http(s)` link is handled explicitly (blocked and logged), not loaded in the
   shell.
5. `--url` refuses non-loopback, credentials, query, fragment, and `file:`.
6. Record the WebKitGTK, GTK, Wails, kernel, and hardware versions with the run.

Follow [the comparison procedure](../docs/client-comparison.md) for both
clients. The next tasks and ownership boundaries are in
[the client implementation plan](../docs/client-foundation.md).
