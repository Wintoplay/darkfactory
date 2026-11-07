# DarkFactory

DarkFactory is a private workspace manager for coding agents. It organizes
repositories and worktrees, keeps terminal sessions alive when you close a
client, and lets you connect from a browser, a Linux Wails shell, or Android.
The local Go daemon owns the work and the detached session host owns the PTYs.

The [threat model](docs/threat-model.md) describes the security target and
current limits. Do not expose the daemon directly to the Internet or treat a
worktree as a sandbox for untrusted agents.

DarkFactory is available under the [MIT License](LICENSE). The maintainers
request that others do not use the project to train AI models. This request is
nonbinding and does not change the permissions granted by the MIT License.
Third-party components retain their own licenses; see [third-party assets](third_party/README.md).

**Status:** development. The first prerelease, `v0.1.0-rc.7`, is published with
signed build provenance; see [install](#install-linux). A real phone has paired
over Tailscale, but full phone acceptance and an external security review remain
open. See [the release handoff](HANDOFF.md) for tested revisions, known limits,
and remaining work.

## Get started

On Linux, [install a release](#install-linux) or build from source. To build and
open the local interface from a checkout:

```bash
make build
./bin/darkfactory serve
```

In another terminal, run `./bin/darkfactory open`. Add a repository in the
browser, then start a session in its checkout or in a new worktree. To connect
an Android device, use **Pair phone** and compare the displayed fingerprints.
See [build and run](#build-and-run) for remote access and device scope.

The daemon has no automatic updater or unsolicited update checks. The
[upgrade foundation](docs/upgrade-foundation.md) describes a future explicit
upgrade flow; it is not implemented.

## Design at a glance

The daemon is a static Go binary with two direct Go dependencies. It has no
Electron, Node runtime, runtime package manager, telemetry, or automatic
updater. Browser assets are vendored and are not fetched at build or runtime.

Frontend build tools and native clients are permitted. The current UI remains
plain JavaScript. An optional [Wails desktop evaluation](desktop/README.md) has
its own Go module and native dependencies. It does not change the static daemon
release. No browser/Wails performance winner has been established. See the
[client foundation and agent tasks](docs/client-foundation.md).

## Requirements

- Go 1.27+
- git
- Linux (first), macOS (next)

## Install (Linux)

**The first tagged release is `v0.1.0-rc.7` (a prerelease).** The packaging
recipe (`make dist`) produces the artifact set described here, and the release
workflow publishes it with GitHub artifact attestations. Clean install, upgrade,
restart and a one-revision rollback were checked with isolated-root evidence kept
in a private test archive, recorded from a dirty working tree with a synthetic
version. That evidence, and the `test/artifacts/` trees referenced elsewhere in
these notes, are not published: the directory is gitignored because it contains
machine-specific screenshots and logs. This is not a verified upgrade at the
current candidate revision. Release checksums detect corruption; the attestation
authenticates the workflow and commit that built an artifact.

One static binary, no runtime dependencies, no Node, no package manager at
runtime. Pick one of three; they install the same file.

**Debian/Ubuntu (.deb, amd64 and arm64).** Once a release is tagged, download
`darkfactory_<version>_amd64.deb` from the
[releases page](https://github.com/Wintoplay/darkfactory/releases) and install it:

```bash
sudo apt install ./darkfactory_0.1.0_amd64.deb
```

It puts the binary in `/usr/bin`, a systemd **user** unit in
`/usr/lib/systemd/user/`, and the app-menu entry and icon under `/usr/share`.

**Script (installs under your home, verifies the checksum).** Once a release is
tagged, read it first; `--desktop` also installs the user unit and the app-menu
entry.

```bash
curl -fsSL https://raw.githubusercontent.com/Wintoplay/darkfactory/main/install.sh -o install.sh
less install.sh
sh install.sh --desktop
```

Use `--system` to install into `/usr/local` (with `sudo`), `--version v0.1.0`
to pin a release, `--dry-run` to see what it would do, and `--uninstall` to
remove what it wrote. The script checks the archive against the release's
`SHA256SUMS` before installing, and tells you what it left behind. To verify
provenance as well:

```bash
gh attestation verify darkfactory_linux_amd64.tar.gz --repo Wintoplay/darkfactory
```

**Tarball.** `darkfactory_linux_amd64.tar.gz` holds the binary, `README.md`, this
`install.sh`, `share/systemd/user/darkfactory.service`,
`share/applications/darkfactory.desktop` and the icon; copy what you need, or run
the bundled installer with `--version` so it fetches the same release.

### From source (development)

```bash
make update-source         # build, replace the installed binary, restart daemon
make install               # build and install without restarting the daemon
make install-status        # compare the installed and running binaries
```

`make update-source` uses the current checkout; it does not pull changes from
Git. It leaves the detached session host running. If systemd manages the daemon,
run `make install`, then `systemctl --user restart darkfactory` yourself. The
running daemon must use the default `serve` configuration when using this
source update command; custom command-line options need a manual restart.
The command reports when the daemon process starts, not when the app is ready.
Open the app and check the daemon log if it does not connect. A custom
`stateDir` in `config.json` needs a matching `DARKFACTORY_STATE` make variable.

`make install` keeps no backup binary: the repository and the release archives
are the source of truth. The daemon embeds `web/`, so it keeps serving the UI it
was built with until it is restarted. After changing `web/`, run
`make install-restart` (or `systemctl --user restart darkfactory` when the unit
is active). After a swap the running daemon's `/proc/<pid>/exe` reads
`.../darkfactory (deleted)`; that, or `make install-status`, is how you tell a
stale daemon from a stale browser tab.

Override `BINDIR` (`make install BINDIR=/usr/local/bin`) or
`DARKFACTORY_STATE` for another layout. Both targets leave the detached session
host alone, so hosted PTYs survive; only the daemon process is replaced.

### Desktop entry and start at login

Clicking **DarkFactory** in the app menu runs `darkfactory launch`: it starts
the daemon in the background if nothing is listening yet, then opens the
workspace in your browser. For start-at-login instead:

```bash
systemctl --user enable --now darkfactory
```

`systemctl --user stop` stops the PTYs with the daemon; `darkfactory stop` is
the gentler one and leaves live sessions running.

## Build and run

```bash
go build -o bin/darkfactory ./cmd/darkfactory
./bin/darkfactory serve
```

Then open the browser with one command:

```bash
./bin/darkfactory open
```

It asks the running daemon for a one-time fragment URL, opens the browser, and
the browser exchanges the fragment for an `HttpOnly`, `SameSite=Strict` cookie.
The cookie is stored as a hash with its expiry, so a daemon restart does not log
the tab out; run `open` again only for a new browser or after Disconnect.

For a phone, use **Pair phone** in the desktop UI. It shows a 120-second
one-time code, a `#pair=` link for a local browser, and a QR that opens the
native app with the daemon URL and code prefilled. The host chooses a device
name and either **Full control** or **Read-only**; the phone cannot change
either. The phone hashes the host public key it received, shows that computed
fingerprint, and refuses a response whose fingerprint does not describe that
key; compare it with the value on the desktop before the first connection.
Pairing is refused on remote
plain HTTP, so use Tailscale Serve or an SSH tunnel. Set `publicURL` in
`config.json` to the origin the phone can reach, for example
`https://df.tailnet.ts.net`; the pairing link and QR use it, and its host is
allowed automatically. `allowedHosts` remains for additional origins.

**Devices** lists every paired browser, its scope, last-seen time, and expiry,
and revokes one device or all others. A revoked device is refused on its next
request. This is still same-user convenience, not a sandbox: a full-control
device has the same workspace authority as the desktop browser, while pairing
and device revocation stay with the host's local UI or the master token
(`403 host_admin` otherwise). Read-only is an API/UI guard, not process
isolation. Disconnect deletes only that browser's
cookie.

### Notifications

The daemon records attention events in a small ring: a session exited or was
lost, a run is blocked or waiting, and an OpenCode permission request appeared.
It keeps the last 100 events in memory and exposes them at
`GET /api/v1/notifications`; a missing `after` cursor is a baseline, and
`after=<id>` returns newer events.

- **Desktop:** when `desktopNotify` is true (the default), the daemon calls
  `notify-send` for each event. This is a local freedesktop notification; it
  makes no outbound call. A headless host without a session bus simply gets no
  popup.
- **Android:** the paired app runs a `dataSync` foreground service. It polls the
  same feed through the pinned E2EE envelope, keeps a per-host cursor, and posts
  a local notification when the app is not in the foreground. There is no FCM,
  Google account, or vendor push service. Android 13+ asks for notification
  permission after the first connection; the persistent “Watching …” notification
  has a **Stop** action. Some vendors kill foreground services unless battery
  optimisation is disabled for DarkFactory; the service is restarted when the
  app opens.
- **Not implemented:** Web Push for an installed PWA and the optional
  ntfy/Telegram webhook remain targets (MO-7). The daemon never sends the event
  to a third party by itself.

The master token never enters a URL or `localStorage`; it is only needed for the
CLI and scripts:

```bash
./bin/darkfactory token     # created on first run at ~/.config/darkfactory/token
```

Browser sessions are hashed with a second `0600` file,
`~/.config/darkfactory/instance-secret`. Deleting it invalidates every browser
session at once, including paired phones; no data is lost, and each device
pairs again.

If a token or cookie may have leaked, rotate the credentials:

```bash
darkfactory token --rotate            # new master token; cookies keep working
darkfactory token --rotate-sessions   # new salt: every browser and phone is logged out
darkfactory token --rotate --rotate-sessions
```

Rotation writes the files atomically with mode `0600` and prints what changed,
not the secret. A **running** daemon keeps the credentials it read at startup,
so restart it afterwards (`darkfactory stop`, then `darkfactory serve`). Rows
minted under the old secret are dropped rather than lingering in **Devices**.

`darkfactory open --no-browser` prints the URL instead of launching a browser.
`darkfactory status` prints resolved paths, run count, and the containment mode
per session; `--json` is the machine-readable form. `darkfactory audit` prints
the decision ledger, one line per decision with the outcome the handler
answered, and `--verify` only recomputes the hash chain and exits 1 on the first
row that does not match it. Old rows are pruned on a retention window
(`ledgerRetentionDays`, default 30 days, `0` keeps everything); each prune
leaves a `ledger.prune` marker naming the last dropped sequence and hash, so the
chain still verifies and the deletion stays visible. `darkfactory stop` stops the
daemon recorded in `~/.local/state/darkfactory/serve.pid` with SIGTERM and
leaves the session host running, so live PTYs survive. Starting a second `serve`
against the same state directory is refused with that pid:

```text
darkfactory: already serving /home/you/.local/state/darkfactory (pid 1234); stop it with `darkfactory stop` or `kill 1234`
```

If you open the page directly, the token paste fallback still works and keeps
the token in `sessionStorage` for that tab.

For remote access use Tailscale Serve or an SSH tunnel. A non-loopback bind
requires `--allow-remote`; a plaintext one also requires `--insecure-remote`.
On a non-loopback listener the daemon also refuses direct peers from public
addresses: only loopback, RFC1918/ULA/link-local or on-link LAN, and Tailscale
`100.64.0.0/10`/`fd7a:115c:a1e0::/48` sources are accepted. Set
`"allowPublicSources": true` in `config.json` or pass `--allow-public` to opt
out, but direct plaintext internet exposure is not a supported deployment. The
check uses the TCP peer address only; `X-Forwarded-For` never changes it, so a
local TLS-terminating proxy is the operator's trust boundary.
Pairing a new device and revoking one also need the master token or the host's
loopback UI. A paired device, even a `full` one, keeps workspace access but
gets `403 host_admin` for those credential verbs; the local web UI is loopback
only, so its normal flow is unchanged.
When you only want the listener open while you are using it, start it with
`darkfactory serve --idle-exit 30m` (or `darkfactory launch --idle-exit 30m`).
The listener exits after that quiet period; the detached session host and its
PTYs stay alive for the next start. The server has no cloud relay, telemetry,
or update checks.

Listener deadlines are configurable in `config.json` and on `serve`. A value is
a duration (`30s`, `2m`) or `"never"`/`"0"` to disable that deadline:

```json
{
  "readHeaderTimeout": "10s",
  "readTimeout": "60s",
  "writeTimeout": "60s",
  "idleTimeout": "2m",
  "maxHeaderBytes": 1048576
}
```

`readTimeout` bounds a stalled request body and `writeTimeout` a response that
never finishes; `readHeaderTimeout` bounds a client that stalls mid-headers.
Terminal and event streams clear their own read and write deadlines, so a
finite default does not cut a live session. The same values are available as
`serve --read-header-timeout`, `--read-timeout`, `--write-timeout`,
`--idle-timeout` and `--max-header-bytes`; `--idle-exit` is separate and
controls the whole listener's quiet period.

## Policy

Which principal may run which verb is a JSON file, not code:
`~/.config/darkfactory/policy.json` (or `serve --policy <path>`). With no file
the built-in default allows the human surface every verb and nothing else.
`darkfactory policy` prints what is in force, where it came from, its hash and
the default rule.

```json
{
  "version": 1,
  "rules": [
    { "effect": "allow", "principal": "human*", "verb": "*", "rule": "human" },
    { "effect": "deny", "principal": "human*", "verb": "repo.delete", "rule": "no-deletes-here" }
  ]
}
```

`principal` and `verb` are globs (`*`, `?`, `[...]`) over the principal label
(`human`, `human:token`, `human:device:<id>`, later `worker`/`overseer`) and the
audit verb (`repo.delete`, `opencode.*`). Every deny is evaluated before any
allow, whatever the file order, and no match is a deny. It fails closed in four
ways, each tested: an empty rule list permits nothing, a deny whose pattern
cannot compile denies, an allow whose pattern cannot compile does not permit,
and a file that does not parse stops the daemon instead of being ignored. The
file is read at startup, so restart after editing it. Every decision row in the
ledger carries the policy hash that judged it.

A denied request answers `403 policy_denied` with the rule name and the hash,
so `darkfactory audit` and the browser both say which rule fired.

## Current capabilities

- Add a repo by absolute path, with the OS folder picker on a local desktop, or
  the server’s directory browser when remote/headless (git repositories are
  marked). One path is one repo row; adding the same resolved directory again
  is refused.
- Every repo gets one in-place workspace at its own path, so the fleet is
  repo → workspace → sessions. For a git repo that workspace is the checkout:
  no branch, no copy, and it shares the checkout’s agent history.
- Start a session directly from a workspace row’s `+` or the workspace view:
  only the agent and title are needed. The repo/location picker is the fallback
  when nothing is selected, and defaults to the checkout.
- Explicitly choose an existing worktree or a new isolated worktree when you
  need isolation. A worktree is created only when chosen.
- Create and remove optional git worktrees under
  `~/.local/share/darkfactory/worktrees`; each new worktree path carries a short
  id so two repos with the same basename cannot collide.
- Start a shell or a configured agent preset in a worktree or workspace;
  multiple sessions per worktree are allowed, each with its own PTY and tab
- Expand a worktree/workspace in the fleet tree to see its sessions inline;
  right-click a session for Open, New session, and Stop/Remove
- Drag a repo header’s grip to reorder the fleet groups, or focus it and press
  `↑`/`↓`; a workspace row’s grip reorders inside its repo, and when the repo
  has exactly one workspace that row moves the whole repo group. The order is
  stored on the daemon (`Repo.Position` / `Worktree.Position`), so it survives a
  reload and a second client, and a new repo or workspace joins at the end
- Get a local notification when a session finishes or is lost, a run blocks or
  waits for input, or an OpenCode permission request appears. The daemon uses
  `notify-send` on the desktop; the Android client runs a foreground service
  that polls the same notification feed, so it does not need FCM or a vendor
  push service. See [Notifications](#notifications)
- Stop a live session or remove a `lost`/`exited` session record from the tab bar
  or the inline `×`; the daemon verifies the PID start time before signalling and
  reports `stopped`, `already_exited`, or `unverifiable`
- Remove a worktree only after it is safe: a live session or an uncommitted /
  untracked file is refused with the session ids or paths named, and the daemon
  asks again (and only then accepts `?force=1`) instead of discarding work
- Start a session twice without getting two: a launch carries an
  `Idempotency-Key`, so a retry after a lost response returns the session that
  already exists
- Stream the PTY into a real browser terminal: fleet tree, worktree details,
  xterm.js screen, keyboard input, resize, and stop
- Reconnect the browser to a live session; a daemon restart reattaches to the
  same hosted PTY and ring, and a missing host falls back to restore or `lost`
- Pair a phone with a 120-second one-time code, then open the `#pair=` link or
  enter the code on the phone; no master token is pasted
- Reap a PTY whose worktree directory disappears; `serve` shutdown detaches,
  and `darkfactory session-host --stop` stops every hosted PTY
- Answer every route failure with one typed JSON refusal (`code`, `rule`, `target`,
  `detail`, `remediable`, `hint`); codes are stable and the browser shows the
  detail or hint, so a client never parses prose
- Route every authenticated mutation through one gateway (resolve the principal,
  decide, record, then act); a failed record leaves nothing changed
- Record every decision in an append-only, hash-chained ledger — the decision
  before the effect and the outcome after it — and read it back with
  `darkfactory audit` or `darkfactory audit --verify`, which recomputes the
  chain and exits 1 on the first row that does not match; rows older than
  `ledgerRetentionDays` (default 30) are pruned into a marker row, never edited
- Decide every mutation against a policy file, deny before allow, no match
  meaning deny; a malformed file refuses startup, and `darkfactory policy`
  shows the rules and the hash the ledger records
- Create a run from a goal and an immutable, content-addressed plan; run
  `command` and one-shot `exec` steps, and answer an interactive `pty` step from
  the run timeline
- Persist repo/worktree/session/run/plan/step metadata in bbolt; plan versions
  are also written under `~/.local/state/darkfactory/plans/`
- Desktop workspace and a 390 px phone layout with a bottom navigation bar

A workspace is the directory itself: no branch, no copy, and no git isolation.
Every repo gets one at its own path. For a git repo that is the checkout, and
sessions there share its files and agent history. It uses the same containment
setting as a worktree; a `jail` workspace also gets a per-worktree HOME.
Multiple sessions can run there, but there is no writer lock: concurrent agents
can race the same files or git index. Removing the repo stops its sessions and
drops the workspace record; the directory and files stay on disk. Session rows
store the PID, process-group id, and Linux start time. Removing a row signals the
process group only when that identity still matches; otherwise the response says
`unverifiable` instead of pretending the orphan was killed.

Agent CLIs usually key their project history by directory. A new worktree is a
new directory, so OpenCode/Claude history from the repo checkout does not appear
there. Choose **This checkout** to reuse the checkout, its uncommitted files, and
its existing agent history. Choose **New isolated worktree** only when you want
a separate branch and working tree.

Containment defaults to `off`: the agent uses your real `HOME`, so tools that
share a backend (OpenCode, for example) can find their existing service. Set
`"containment": "jail"` in `config.json`, or `"containment": "jail"` on an
agent preset, to opt in. A jail session gets a per-worktree `HOME` under
`~/.local/share/darkfactory/homes/<worktreeId>`; on Linux when `bwrap` can
create a user namespace, the daemon config and state directories are masked and
the worktree is writable. It still runs as your uid and still has network
access, so it is not `strict` containment. Without `bwrap`, or when the kernel
or AppArmor refuses its user namespace, the label falls back to `off`; `status`
prints the mode per session.

A jail can be hardened without pretending it is `strict`. These options are
opt-in and off by default:

```json
{
  "containment": "jail",
  "jailScrubEnv": true,
  "jailHideCredentials": true,
  "jailEnvAllow": ["SSH_AUTH_SOCK"],
  "jailHiddenPaths": ["~/.secrets"],
  "agents": {
    "opencode": {
      "command": "opencode",
      "jailPathAllow": ["~/.config/opencode"]
    }
  }
}
```

`jailScrubEnv` gives the session a minimal environment instead of the daemon's:
it keeps `PATH`, locale, `TERM`, the projected `HOME`/`TMPDIR`, the preset
`env` map, and only the variables named in `jailEnvAllow`. It drops desktop,
D-Bus, SSH-agent, keyring and cloud variables by default. `jailHideCredentials`
masks a built-in list of credential, keyring and browser-profile paths;
`jailHiddenPaths` adds more masks; `jailPathAllow` exempts an exact path from
either list. An allow cannot punch through a masked parent directory; the
session refuses to start instead of silently leaving the path hidden. If
`bwrap` is unavailable, a session with `jailScrubEnv` fails to start instead of
falling back to `off` without scrubbing. Values in `env` and `jailEnvAllow` are
capabilities: a prompt-injected agent can use or print them, so grant only what
that agent needs. The final mode is still same-uid and network-allowed; use
`strict` (not implemented) for a real separate-principal boundary.

The terminal is a real xterm.js view: ANSI, cursor addressing, alternate screen
and keyboard input work. It fills the remaining session height, has a focus mode
that hides the fleet chrome, and keeps a local font-size preference. The
connection pill is also the reconnect action; while the daemon is unreachable,
Stop is disabled instead of pretending the action can work. PTYs live in a
detached `darkfactory session-host`, so restarting or upgrading the daemon
reattaches to the same session ID, PID and scrollback; the tab does not become a
tombstone. A browser refresh never replays a partial full-screen TUI frame:
the stream suppresses that history and asks the PTY for a SIGWINCH repaint;
shells get a bounded tail trimmed to a newline. One browser terminal owns a
session at a time: attaching another detaches the previous with a notice,
because the PTY has one size. xterm.js 6.0.0 has no native touch scrolling, so
a vertical swipe is translated into a synthetic wheel event: a shell scrolls
its scrollback, and a full-screen TUI receives the wheel/arrow equivalent it
already understands. Chromium/Blink and non-Linux WebKit use the official
WebGL2 renderer for speed, falling back to the DOM renderer if the GPU context
is unavailable, lost, or software; WebKitGTK keeps the DOM renderer because its
masked GL path was not a win in the synthetic trial. A fractional
devicePixelRatio also keeps the DOM renderer because the WebGL cell box rounds
and clips glyphs. The ↑/↓ buttons send PageUp/PageDown and ⇣ sends End, the
TUI's "jump to latest" action. A TUI owns its own history, so xterm's scrollback
is disabled in that mode. On iPad the shell follows `visualViewport` height and
offset, so the keyboard lifts the terminal instead of covering it. Input is
coalesced per frame with at most one request in flight, and PTY output is
coalesced per animation frame, so a swipe is not one remote round trip per
notch. The ring (`ringBytes`, 4 MiB by
default) is held by the host; the daemon also persists a bounded transcript in
the 0600 state DB, used only when
the host is gone and the session has to be restarted. If even that fails, it is
marked `lost` and shows Restart, which starts a new PTY in the same workspace
with the same agent and title; that path says the old output was not persisted.

On the daemon host, the browser also offers an optional **Ghostty** button that
opens the same session in a separate native window. It requires Ghostty 1.3.1
on `PATH` and a graphical Linux session. The UI reads
`/state.nativeTerminal.available` and hides the button when the host cannot run
it, so a PC without Ghostty keeps the browser terminal and never shows a failing
action. The `.deb` does not install or depend on Ghostty; the desktop Wails
shell uses an in-app VTE pane instead and does not need it either.

xterm.js 6.0.0,
addon-fit 0.11.0, addon-webgl 0.19.0, and the MIT QRCode generator are vendored
under `web/vendor/` with their licenses and checksums recorded in
[`third_party/README.md`](third_party/README.md). OSC 8
links are ignored, window manipulation stays off, and the clipboard addon is not
shipped. The Runs view lists real runs, steps, outputs, and waiting interactive
steps; it does not invent checkpoints or approvals. The bearer token fallback is
kept in `sessionStorage` for the current tab only, never in `localStorage`; the
font-size preference is the only local UI value stored in `localStorage`.

### OpenCode has a native panel

OpenCode is not a shell. It is a chat application with its own message list,
scrollback, mouse targets, and keymap; xterm.js cannot see or drive that state.
Do not run the OpenCode TUI in a browser terminal and try to emulate its "Jump to
latest" with keys. DarkFactory keeps xterm.js for plain shells and talks to
OpenCode through its local HTTP API.

The OpenCode background service listens on `127.0.0.1:26706`. DarkFactory reads
its service password from `opencodePasswordFile` (default
`~/.config/opencode/service-local-install.json`) and proxies the session list,
messages, prompt, interrupt, permission, and create-chat routes on the server
side. The password never reaches the browser. The panel has native scrolling,
search, **Load older**, a jump-to-latest button, a composer, and a **Stop**
button for the current turn. Pending OpenCode permission requests render as
cards with **Allow once**, **Always allow**, and **Reject**; an agent blocked on
a prompt is visible instead of silently stalled. The message view updates from
OpenCode's event stream with a slow fallback poll, because that stream is
volatile. **New chat** runs OpenCode in a DarkFactory worktree. The panel is a
view of one worktree: open it with **Chat** from that worktree or one of its
sessions, and it lists only chats in that directory. There is no fleet-level
chat destination; switching worktree switches the chat.

```json
{
  "opencodeAPIURL": "http://127.0.0.1:26706",
  "opencodePasswordFile": "/home/you/.config/opencode/service-local-install.json"
}
```

Both fields have those defaults; you only need them if OpenCode uses a different
XDG config or port. **Chat** in the worktree or session view opens the panel. The OpenCode
service and the TUI share the same session database, so an existing session can
be continued in the panel; close the TUI client when you switch to avoid two
clients on one session.

`opencodeURL` is optional. It stays in `/state` for an external OpenCode PWA or
another client, but the panel does not need it. If you also want that external
client, expose OpenCode over Tailscale Serve on a second HTTPS port:

```sh
sudo tailscale serve --bg --yes --https=8443 http://127.0.0.1:26706
opencode pair --url https://<host>.<tailnet>.ts.net:8443
```

Ledger retention (`ledgerRetentionDays`, default 30) ships in this build.
Checkpoints and gates, audited export and redaction, and per-session
capabilities remain target-state in [docs/requirements.md](docs/requirements.md);
they are not part of the current release.

## Layout

```text
cmd/darkfactory      command entrypoint
internal/config      paths, config, token, instance secret
internal/store       bbolt metadata
internal/gitwt       git worktree operations
internal/session     PTY sessions, detached session host, process identity, ring
internal/run         immutable plans and the run/step coordinator
internal/httpapi     REST/streaming API and auth
web/                 browser shell; xterm.js is vendored under web/vendor/
jsconfig.json        checkJs settings for web/app.js (no build)
docs/                requirements and design notes
```

## Development

```bash
go test ./...
go vet ./...
make race         # the same suite under -race (test binary only)
make fuzz         # explore past FuzzBrowserSnapshot's committed corpus
make vuln         # govulncheck: reachable advisories (needs network, dev-only)
make ui-check     # node --check, plus bun check or tsc
make check        # vet + test + race + ui-check
```

`jsconfig.json` turns on `checkJs` for `web/app.js`; editors with the TypeScript
language service type-check it without a build step. `make ui-check` runs
`tsc -p jsconfig.json` when `tsc` is on `PATH`, otherwise `bun check` when Bun
1.4+ is installed, and only skips if neither exists. The browser code is plain
JS by design: the binary has no Node dependency at build or runtime.

The API is versioned under `/api/v1` and accepts either the master bearer token
or the `HttpOnly` cookie minted by `darkfactory open`. Cookie mutations also
require `X-DF-Request: 1` and a matching `Host`/`Origin`; there is no CORS. See
[DESIGN.md](DESIGN.md) for architecture and non-goals.

### Debugging the UI

Open the app with `?debug=1`, for example
`http://127.0.0.1:8787/?debug=1`. A debug panel appears in the corner and logs
API statuses, dialog opens/closes, folder-browser visibility, and the exact
element clicked. It has **Save**, **Copy**, and **Clear** buttons. The browser also posts its
debug lines to the daemon, so they appear in the server log below. The footer
always shows the browser `ui` version **and** the daemon build it is talking to;
if `?debug=1` shows no panel, the browser is running an older cached `app.js`.
The shared client gate is `make ui-gate` (desktop and phone layout, served bytes
equal to `web/app.js`); see [docs/ui-gate.md](docs/ui-gate.md).

For server-side request logs, start the daemon with `--debug`:

```bash
./bin/darkfactory serve --debug
```

It logs one line per request (method, path, status, duration) and mirrors the
log to `$XDG_STATE_HOME/darkfactory/debug.log` (`~/.local/state/darkfactory/debug.log`
by default). The terminal stream is long-lived, so its line appears when the
stream closes.

### Phone and tablet layout

Layout, safe-area, keyboard and paint bugs do not reproduce at desktop width, so
they are checked against an Android emulator — see
[Testing the Android client](docs/testing-android.md). Remote browser HTML is
now refused; the native app bundles the same UI, owns the sealed token, and
pins an app-layer E2EE host key. The
historical Firefox procedure is retained for layout evidence. iOS still needs
the iPad itself; there is no simulator on Linux.

## Design notes

- [Release checklist](docs/release-checklist.md) — the ordered release gates
  with their commands, and which items need a real phone or a signing decision.
- [Requirements](docs/requirements.md) — the specification: functional,
  non-functional, milestones, traceability.
- [Substrate lessons](docs/substrate-lessons.md) — what survives a move to
  WASM, a desktop shell, a native app, or an SSH/TUI client: PTY ownership,
  ring-vs-screen, redraw, pairing, and the tests that must be re-run.
- [Orca lessons](docs/orca-lessons.md) — seven months of release and commit
  history, and the reliability requirements that follow.
- [Agent authority](docs/agent-authority.md) — threat model, capabilities,
  approvals, containment; includes the build-now/park triage.
- [UI gate](docs/ui-gate.md) — build identity, the desktop/phone layout
  contract, and how a stale daemon is caught before it looks like a regression.
- [Client foundation](docs/client-foundation.md) — C0/C1/C2/C3 tasks and the
  phone/native acceptance gates.
- [Native unified client](docs/native-client.md) — the remote-UI boundary,
  per-device credentials, pinned app-layer E2EE, and the Android/Wails path.
  The Kotlin Android client is in [`android/`](android/README.md); a
  release-signed APK is attached to the `v0.1.0-rc.2` prerelease and was
  emulator-verified with two saved hosts and one-host-at-a-time switching.
- [Android client](android/README.md) — bundled UI, QR deep-link pairing,
  Keystore-backed multi-host token/key registry, pinned host keys, encrypted
  envelope, host picker, loopback origin, build prerequisites, and the current
  limits.
- [UI mockup](docs/ui-demo.html) — static HTML of the fleet, run, approvals,
  pairing and `open` screens; a design reference, not the served app. Open it
  directly in a browser.
- [OpenBot lessons](docs/openbot-lessons.md) — the governance patterns worth
  copying: gateway, policy, audit, handover.
- [Fabro lessons](docs/fabro-lessons.md) — the workflow runtime that shares our
  name: run/stage/checkpoint model, and why we take the process layer but not
  the agent loop.
- [Three teachers](docs/teachers-comparison.md) — how Orca, OpenBot and Hermes
  Agent fit together.
- [Feature leverage](docs/feature-leverage.md) — launch products, speak
  protocols, copy designs; what to build and what never to build.
- [Android testing](docs/testing-android.md) — the emulator recipe, the `adb`
  traps, and the phone baseline numbers.
