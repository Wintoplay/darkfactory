# DarkFactory

DarkFactory is a private workspace manager for coding agents. It organizes
repositories and worktrees, keeps terminal sessions alive when you close a
client, and lets you connect from a browser, a Linux Wails shell, or Android.
A static Go daemon owns the work; a detached session host owns the PTYs, so
restarting or upgrading the daemon reattaches to the same sessions.

Primary development targets Linux. macOS compiles but is not packaged yet.
[Threat model](docs/threat-model.md) · [Using DarkFactory](docs/usage.md) ·
[Architecture](DESIGN.md) · [Release handoff](HANDOFF.md)

**Status:** prerelease. `v0.1.0-rc.8` is published with signed build provenance
and a hybrid post-quantum transport between the daemon and the Android client.
No external security review and no physical-phone run of the v2 client yet; see
the [release notes](https://github.com/Wintoplay/darkfactory/releases) and the
[handoff](HANDOFF.md) for tested revisions and open work.

MIT licensed. The maintainers request, without changing the license, that the
project not be used to train AI models. Third-party components keep their own
licenses; see [third-party assets](third_party/README.md).

## Install (Linux)

**.deb** (amd64/arm64) from the [releases page](https://github.com/Wintoplay/darkfactory/releases):

```bash
sudo apt install ./darkfactory_0.1.0-rc.8_amd64.deb
```

**Script**, installing under `$HOME` and verifying the release checksum:

```bash
curl -fsSL https://raw.githubusercontent.com/Wintoplay/darkfactory/main/install.sh -o install.sh
less install.sh
sh install.sh --desktop
```

`--system` installs into `/usr/local`, `--version` pins a release, `--dry-run`
shows the plan, `--uninstall` removes what it wrote. To check provenance:

```bash
gh attestation verify darkfactory_linux_amd64.tar.gz --repo Wintoplay/darkfactory
```

**Tarball:** the binary, this README, `install.sh`, the systemd user unit, the
desktop entry and the icon.

Clicking **DarkFactory** in the app menu runs `darkfactory launch --desktop`: it
starts the daemon if nothing is listening, then opens the native window when
`darkfactory-desktop` is installed, otherwise the browser. For start at login:
`systemctl --user enable --now darkfactory`.

## Upgrade

From a checkout, one command rebuilds and swaps everything that is running:

```bash
make upgrade
```

It builds the daemon, atomically replaces the installed binary, reinstalls the
desktop shell when its build dependencies are present, restarts only the
daemon, and reopens the window if one is open. The session host is never
restarted, so hosted PTYs survive. Use `make install` to skip the restart,
`make install-status` to see whether the running daemon is stale, and
`git pull` first to upgrade the source itself.

From a release, rerun the installer with the new version (or `apt install` the
new `.deb`). The Android client must be updated whenever the daemon's transport
changes: since `v0.1.0-rc.8` it speaks hybrid E2EE, so an older APK cannot pair
until it installs the matching release.

There is no automatic updater, telemetry or update check. The
[upgrade foundation](docs/upgrade-foundation.md) describes a future explicit
flow.

## Quick start

```bash
make build
./bin/darkfactory serve      # in one terminal
./bin/darkfactory open       # in another
```

Add a repository, then start a session in its checkout or in a new worktree.
`open` trades a one-time fragment for an `HttpOnly`, `SameSite=Strict` cookie,
so a daemon restart does not log the tab out.

For a phone, use **Pair phone** in the UI, compare the shown fingerprint on both
sides, and choose full control or read-only. Pairing is refused on remote
plaintext HTTP; use Tailscale Serve or an SSH tunnel and set `publicURL` in
`config.json`. Details: [running and connecting](docs/usage.md#running-and-connecting).

## What it does

- Repos, one in-place workspace per repo, and optional isolated git worktrees.
- Multiple agent or shell sessions per workspace, each with its own PTY and tab.
- A real xterm.js terminal with reconnect, resize, touch scrolling and an
  optional in-app VTE pane in the Wails window.
- A native OpenCode chat panel (messages, prompts, permissions) instead of
  pretending its TUI works in a terminal.
- Local notifications when a session finishes or an agent blocks; on Android
  through the paired app, with no FCM or vendor push.
- One typed JSON refusal per failure, denial by default, and an append-only,
  hash-chained decision ledger (`darkfactory audit --verify`).
- Runs built from an immutable plan, with interactive steps answered inline.
- Hybrid E2EE between daemon and phone: P-256-signed bundle, X25519 + ML-KEM-768,
  sealed pairing code, authenticated end marker, pinned fingerprints.

## Security and limits

Same-user convenience, not a sandbox: a full-control device has the same
workspace authority as the local browser, and containment defaults to `off`.
`jail` gives a per-worktree `HOME` and optional credential masking, but it still
runs as your uid with network access. Pairing and revocation need the host's
local UI or the master token. Do not expose the daemon directly to the
Internet; a non-loopback bind needs `--allow-remote` and a TLS-terminating
proxy or tunnel. Read the [threat model](docs/threat-model.md) and
[agent authority](docs/agent-authority.md) before running untrusted agents.

## Development

```bash
make check        # vet + test + race + ui-check + clientbench + install + desktop
make ui-gate      # real browser at desktop, narrow and phone widths
make security-check  # Android PQ interop, fuzz, symbolic protocol models
make fuzz         # explore past the committed corpus
make vuln         # reachable Go advisories (dev-only, needs network)
make android-build   # APK with JDK 21 and Build-Tools 36.0.0
```

Requirements: Go 1.27.4+, git, Linux. The daemon is CGO-free and has no Node
runtime; `web/` is vendored and embedded. The optional Wails shell has its own
module under `desktop/`. Commands and gates:
[development](docs/agent-reference.md), [release checklist](docs/release-checklist.md).

```text
cmd/darkfactory      command entrypoint
internal/            daemon: config, store, gitwt, session host, runs, HTTP API
web/                 browser shell; xterm.js is vendored under web/vendor/
desktop/             optional Wails shell (separate Go module)
android/             optional Kotlin client
docs/                requirements, design notes, reviews
```

## Documentation

- [Using DarkFactory](docs/usage.md) — pairing, devices, notifications, policy,
  containment, terminal behaviour, OpenCode panel, UI debugging.
- [Requirements](docs/requirements.md) — the specification and traceability.
- [Agent reference](docs/agent-reference.md) — commands, layout, invariants.
- [Dependency policy](docs/dependency-policy.md) — Dependabot scope, gates, and
  the Android toolchain refresh procedure.
- [Release checklist](docs/release-checklist.md) — gates, evidence, open items.
- [UI gate](docs/ui-gate.md) — build identity and the desktop/phone contract.
- [Native client](docs/native-client.md),
  [Android client](android/README.md),
  [Android testing](docs/testing-android.md) — the phone path.
- [Substrate lessons](docs/substrate-lessons.md),
  [Orca lessons](docs/orca-lessons.md),
  [OpenBot lessons](docs/openbot-lessons.md) — why the session and governance
  layers look like this.
- [UI mockup](docs/ui-demo.html) — static design reference, not the served app.
