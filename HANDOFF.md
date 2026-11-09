# Current release handoff

## Working rule from the last refresh

Do not chase CI failures one push at a time. Batch fixes, run targeted package
tests while iterating, and use `make check` once before pushing. The cost
ledger and the classification rule for flakes are in
`docs/reviews/ci-chase-2026-11-09.md`.

## Dependency refresh and Dependabot (2026-11-09)

Dependabot now watches both Go modules, the Android Gradle project and GitHub
Actions. Alerts and automated security updates are enabled in the repository
settings; grouped minor/patch PRs keep the queue small. AGP and Kotlin majors are
ignored because they are toolchain migrations, not version edits. The gates and
the items Dependabot cannot see (Gradle wrapper, AGP minimum Build-Tools, SDK
platform, JDK) are in `docs/dependency-policy.md`.

Go and Wails moved to current releases: `bbolt` 1.3.11 → 1.5.0, `x/sys`
0.47/0.48 → 0.49.0, Wails 3.0.0 → 3.1.0. `make check` passes. A copy of the
414 MB live `state.db` was opened by the new daemon under isolated XDG roots; it
restored all four running session rows, so bbolt 1.5 reads the 1.3.11 database.
The live daemon and desktop shell were not restarted and still run the previous
build.

Android moved to AGP 9.5.1, Gradle 9.9.0, Kotlin 2.4.21 through AGP's built-in
Kotlin (the `kotlin-android` plugin is gone), `compileSdk`/`targetSdk` 36 and
Build-Tools 36.0.0; SDK platform 36 is installed in `~/Android/Sdk`. Gradle 9.9
deprecations were fixed rather than hidden: the tag lookup is a `ValueSource`,
and the DSL uses assignment syntax. `targetSdk 36` routes back gestures to the
predictive-back dispatcher, so `MainActivity` now registers an
`OnBackInvokedCallback` for API 33+ and keeps `onBackPressed` for older
releases; lint's `GestureBackNavigation` error is gone.

Verified locally: `lintDebug assembleDebug assembleDebugAndroidTest`,
`assembleRelease` (signed, certificate unchanged, `versionCode 161`),
`make android-pq-check` and `make android-pq-live`. The release APK asset on
`v0.1.0-rc.8` was not replaced. `make protocol-verify` could not run on this
host because `ocamlyacc` is absent; CI runs it. No emulator or physical-phone
run covers `targetSdk 36` yet, so predictive back and edge-to-edge remain open
for Android acceptance.

## Attach seam race (2026-11-09)

CI failed once in `TestHostSurvivesDaemonDetach`: the host held `host-marker`,
the daemon's attach snapshot stayed empty for the 3 s deadline. The cause was
`Session.Subscribe`: it copied the ring and registered the chunk channel in two
separate lock acquisitions, while the reader wrote the ring and fanned out
separately. A chunk written between them was in neither, so a prompt printed
once at startup vanished; the mirroring order could also deliver it twice.
`writeChunk` now writes the ring, the input modes and the fan-out under the
same lock `Subscribe` takes for snapshot plus registration, and the browser's
exclusive attach does the same. `TestSubscribeKeepsTheBytesAtTheSeam` attaches
200 times while a numbered token stream runs and requires the joined snapshot
and first chunk to be consecutive; with a 2 ms scheduling gap put back into the
old shape it fails on the first iteration. A full 4 MB ring attach costs ~2.2 ms
and blocks the reader for that copy, once per attach. SS-4 (`snapshot + seq` on
the wire) remains open.

## Session stop verdict race (2026-11-09)

CI exposed a rare `Stop` misreport: closing the PTY master hangs up the
terminal, and SIGHUP can kill the process group before SIGTERM is sent, so
`signalGroup` returned ESRCH and `Stop` answered `already_exited` for a session
it had just terminated. `Stop` now treats a group that disappeared after the
call saw it alive as a stop it caused, and waits for the done channel; a 3000×
race run of `TestSessionStopRecordsIdentity` is green. The verdict contract is
unchanged: `already_exited` still means the session was not running when `Stop`
started.

## Hybrid native transport hardening (2026-11-09)

The working tree closes the findings from the hybrid E2EE review. It is
released as `v0.1.0-rc.8` and installed on the operator stack; the rc.8 section
below records that upgrade. The daemon and updated Android APK must ship
together; this revision changes the unreleased v2 pairing and completion wire.
The minimum suite now defaults to 2. Explicit `--e2ee-min-suite=1` retains legacy
compatibility and its weaker guarantees. New Android pairing requires a valid
hybrid bundle even when a transport strips every v2 advertisement; previously
saved v1 connections require that explicit host compatibility setting.

The phone confirms the calculated, suite-bound fingerprint before sending a
pairing request. V2 sends `sealedCode`, encrypted to the confirmed hybrid bundle
and bound to the host and exact ephemeral material, rather than a visible code.
Known pins and bundle epochs are checked before transmission as well as at
registry save. Invalid material cannot consume the code or create an orphan
device. Unknown versions and suites fail closed. A persisted empty or malformed
host identity cannot silently generate a new pin.

X25519 and ML-KEM now use pinned Bouncy Castle 1.86 lightweight APIs; the custom
variable-time ladder and ML-KEM arithmetic were removed. JVM and Android builds
verify the provider artifact. Production code only encapsulates; the provider
jar also contains other algorithms, including decapsulation, so the APK is no
longer described as containing no decapsulation implementation. Its license
notice is in `third_party/bouncycastle/`.

V2 responses require an authenticated end marker. The daemon emits it only on
normal completion with successful writes. Android rejects missing, reordered,
modified, oversized, or trailing frames and does not convert a failed upstream
stream into successful local HTTP completion. Notification bodies are bounded.
Startup and re-pair paths preserve unusable host rows; explicit Forget is the
identity-reset action. Bundle epochs cannot roll backward under an existing pin.

Security checks now run on pull requests and pushes: production JVM interop,
disposable-daemon pairing, protocol fuzz targets, Android compile/lint and
emulator acceptance, symbolic protocol models, both Go-module advisory scans,
and the Java/Kotlin/JavaScript inventory. Branch protection must require these
jobs to prevent bypassing them. `make security-check` is the local protocol gate;
`test/security/README.md` documents the commands and their scope.

ProVerif 2.05 checks the abstract pairing, key-combiner, and response models.
Honest-trace queries prevent vacuous success; deliberately broken models must
produce attack traces. The models cover one exchange under ideal cryptography
and trusted out-of-band comparison. They do not verify Go/Java code, Android UI,
side channels, concurrency, expiry, persistent replay, or the algorithms'
computational security. See `tools/protocolverify/README.md`.

Validation and build provenance are in
`test/artifacts/security-hardening-2026-11-09/REPORT.md`. The rc.8 section below
records the operator upgrade that followed. No physical-phone run or external
cryptographic audit is claimed. Static host keys still provide no forward
secrecy against later host-key theft; private identity material remains in
`state.db`, and the bundle trust anchor is P-256.

## v0.1.0-rc.8: hybrid E2EE released, operator stack upgraded (2026-11-09)

`v0.1.0-rc.8` is the first public release carrying the hybrid transport. The
Linux archives are the CI `make dist` build; the signed APK was built from the
same tag and attached with its `.sha256`. The daemon and APK must be installed
together because the v2 pairing and completion wire changed; the daemon refuses
legacy v1 rows by default, so a phone still on the rc.7 APK cannot connect
until it installs this APK and pairs again.

Operator upgrade, same day: `make install-restart` replaced `serve` with
`0.1.0-dev (v0.1.0-rc.7-3-g2980c63)` and the session host was never restarted,
so all five running sessions kept their PTYs. The desktop shell was rebuilt
from this tree, installed and relaunched; the previously installed shell
predated `desktop/cmd/darkfactory-desktop/main.go`. Host keys and pins survived:
`/api/v1/e2ee/host-key` now advertises `maxVersion: 2`, suite
`x25519-mlkem768-v1`, and a signed bundle.

Open at release time: no physical-phone run of the v2 client, no external
cryptographic audit, no branch protection requiring the new security jobs, and
no evidence for rolling a database forward to an older binary. Local upgrades
are one command: `make upgrade` swaps the daemon, rebuilds and installs the
desktop shell, and reopens an open window; the session host is untouched. The
README is now a front page, with the detailed behaviour in `docs/usage.md`. The install
pipeline itself was fixed before the tag: `install-status` and
`install-restart` treated a zombie `serve` as running, which aborted the swap
and left the daemon down; they now read `/proc/<pid>/stat` and treat `Z` as
exited.

## App-menu entry prefers the native shell again (2026-11-08)

Restoring the launcher after the uninstall leak installed the packaged entry,
whose `Exec` was `darkfactory launch` — the browser path. The owner's
hand-written entry had pointed at `darkfactory-desktop`. The session journal
proves the regression: on 2026-11-07 at 22:35 and 23:16 GNOME launched
`darkfactory.desktop` and the process logged Wails `Build Info` and
`darkfactory: navigation guard active`; today at 10:05 the restored entry printed
a one-time `#code=` browser URL. The deleted file was not in Trash and its old
backup under `/tmp/user/1000/opencode` was gone, so its exact `Exec` line is not
recoverable; the behavior is.

`darkfactory launch` now takes `--desktop`. It starts/uses the daemon exactly as
before, then runs `darkfactory-desktop --url <loopback base>` when that optional
shell is a sibling of the running binary (preferred) or on `PATH`, and otherwise
prints one line and opens the browser. `--no-browser` wins over `--desktop` and
opens nothing: the shell's own autostart runs `launch --no-browser`, so the
handoff must not recurse. `packaging/darkfactory.desktop` now runs
`darkfactory launch --desktop`, which keeps a package-only install working
(browser fallback) and stops a reinstall from clobbering the native-first
behavior. Tests: `TestExecutableFileNeedsARegularExecutable`,
`TestDesktopClientPrefersTheSiblingBinary`,
`TestDesktopClientPassesTheLoopbackURLAndItsExitStatus`. A stub-sibling run of
`launch --desktop` against the live daemon handed off
`--url http://127.0.0.1:8787` and exited 0.

Live on this host after `make install` at 10:20: `~/.local/bin/darkfactory` is
`0.1.0-dev (v0.1.0-rc.5-9-g2d40c8a-dirty)`, and
`~/.local/share/applications/darkfactory.desktop` carries
`Exec=darkfactory launch --desktop` with the icon and
`StartupWMClass=dev.darkfactory.Desktop`. The running daemon pid 7585 and the
detached session host still execute the previous generation; the new code is
client-side (`launch`), so no restart was needed and the live PTYs were not
touched. `v0.1.0-rc.7` artifacts still ship the browser `Exec`; the next release
picks up `--desktop`.

## Installer uninstall leak fixed in v0.1.0-rc.7 (2026-11-08)

`v0.1.0-rc.6` shipped an `install.sh` whose `--uninstall` removed
`~/.local/share/applications/darkfactory.desktop`, its icon and the user unit
even when `--prefix` pointed at a disposable root: those paths come from
`$HOME`/XDG, not `$PREFIX`. A probe uninstall did exactly that to the
maintainer's desktop, which is why the app icon disappeared and the window fell
back to its `dev.darkfactory.Desktop` app id.

`v0.1.0-rc.7` removes the desktop entry, icon and unit only when `BINDIR` is the
default user prefix or the unit's `ExecStart` names the binary being removed.
`test/install-uninstall.sh` pins both directions and runs as
`install-script-check` in `make check`. The desktop entry, icon and unit were
already correct in the release, and no other artifact changed. The restored
live launcher keeps `StartupWMClass=dev.darkfactory.Desktop`.

## Host-credential scope, public-source refusal, Ghostty capability (2026-11-07)

**Installed and live on this host.** `make install-restart` at 2026-11-08
01:42 installed `~/.local/bin/darkfactory` (mtime 01:42, 10,420,384
bytes, `0.1.0-dev (v0.1.0-rc.5-6-g116c120)`). `serve` pid 765012 executes
that path (not deleted), listens on `127.0.0.1:8787`, and logged
`sources="loopback, LAN and Tailscale"`. The embedded UI is `2026-11-07.03`.
The previous daemons pid 7022, 562036, 580744 and 614785 were stopped; the
detached session host pid 7028 still executes the previous build
(`(deleted)`) and its live PTYs survived (3 running after the restart). That
is acceptable here because the slice is daemon/UI-only; a host-generation
replacement still needs an explicit decision because it kills PTYs.

- The gateway now stamps a principal `Local` from the transport: the master
  bearer token, or a cookie/session that arrived through the host's loopback UI
  (loopback peer, loopback `Host`, no `Forwarded`/`X-Forwarded-*`/`X-Real-IP`).
  `pair.code`, `device.revoke`, and `devices.revoke_all` are `host-credential`
  verbs and refuse a non-local device with `403 host_admin` /
  `rule=host_only`. `device.key.create` stays workspace so a remote native
  client can still register its key after entering the master token. The local
  web UI is loopback-only, so its normal pairing/revocation flow is unchanged.
- A non-loopback listener classifies the TCP peer as loopback, private
  (RFC1918/ULA/link-local/on-link), tailnet (`100.64.0.0/10`,
  `fd7a:115c:a1e0::/48`) or public, and refuses a public peer before routing
  with `403 source_denied`. `allowPublicSources` in `config.json` or
  `--allow-public` overrides it. The check uses `RemoteAddr` only;
  `X-Forwarded-For` cannot change the class. A same-LAN peer on a globally
  routable IPv6 address is accepted when it falls inside one of the host's own
  interface prefixes. The default loopback bind is unaffected.
- Refusal registry gains `source_denied` (403, not remediable) and `host_admin`
  (403, remediable). `TestRefusalCodesAreStable` golden updated.
- `/state.nativeTerminal` now tells the local UI whether the optional Ghostty
  window can work on the daemon host: Linux, `DISPLAY`/`WAYLAND_DISPLAY`, and a
  `ghostty` on `PATH` whose `--version` is exactly `1.3.1`. The probe is cached
  for 30 s and reports `not_linux`, `no_graphical_session`, `not_installed`, or
  `unsupported_version`. The UI hides the Ghostty button unless `available` is
  true, so a PC without Ghostty keeps the browser terminal and never offers a
  button that can only fail. Live here: `{available:true, version:1.3.1}`.
- `normalizeState` in `web/app.js` whitelists the fields the page keeps and
  used to drop `nativeTerminal` and `ui`. The Ghostty action therefore stayed
  hidden even when `/state` reported a usable 1.3.1, and the stale-shell reload
  never ran. Both fields now pass through; the browser gate reads the host
  capability and asserts visibility only when it is available.
- Android terminal touch scrolling sent one PageUp/PageDown per touchmove to a
  TUI, so a fast swipe jumped many screens: the mouse-tracking fixture recorded
  9 pages for one 300 px swipe. The touch handler now accumulates finger travel
  and pages once per 160 px (1 page for the same swipe); a normal shell
  scrollback still follows the finger row by row. The OpenCode composer also
  gained `autocorrect="off"` and `autocapitalize="none"`, which it was missing.
  UI bumped to `2026-11-07.03`. A release-signed APK
  (`dist/darkfactory_0.1.0-rc.5-6-g116c120_android.apk`, versionCode 158,
  same `CN=DarkFactory Android` certificate as rc.4) is ready to sideload; no
  device was attached, so it was not installed here.
  Measurement on the disposable TUI fixture: one 300 px swipe sent 9
  `PageDown` sequences before the change and 1 after; a normal shell stayed at
  7 scrollback rows for the same swipe.
- Deferred on purpose: authenticating the session-host control plane
  (`~/.local/state/darkfactory/session-host.sock`). It is a same-uid `0600`
  socket, so a capability there is not a boundary against the agent that can
  already run commands as that uid; adding one now would add a three-generation
  upgrade path and risk orphaned PTYs. `strict` remains the separate-principal
  target. The jail options still say same-uid and network-allowed.

Verification: `/usr/local/go/bin/gofmt -l` clean; `/usr/local/go/bin/go vet
./...`; `/usr/local/go/bin/go test ./...`; focused `-race` on
`./internal/httpapi ./internal/config ./internal/store`; `make ui-check` (node
syntax and no-injection grep); `make ui-gate` (desktop, narrow and phone; set
`DF_CHROMIUM` when the cached Chromium build differs from Playwright's). New
tests:
`TestClassifySource`, `TestPublicSourceGuardFollowsListener`,
`TestHostCredentialVerbsNeedHostAuthority`,
`TestDeviceKeyRegistrationIsNotHostAdmin`, `TestProbeNativeTerminal`,
`TestStateReportsNativeTerminalCapability`.

Live checks after the install: `/healthz` 200, `/` 200, `/api/v1/state` 200,
`POST /api/v1/pair/code` with the master token 200, and `/api/v1/devices` 200.
The live `/state` reports `ui=2026-11-07.03` and
`nativeTerminal={available:true, version:1.3.1}`; the served `index.html` and
`app.js` carry `?v=2026-11-07.03` and the `nativeAvailable` gate, and 3 sessions
were still running across the restart.
The public-source refusal and the remote-cookie `host_admin` refusal are
covered by the unit tests; this loopback listener has no public peer to exercise
them live. To see `host_admin` live, bind a non-loopback listener and call
`POST /pair/code` with a paired device token from a non-local source, or use
the E2EE path from a tailnet peer whose `Host` is not loopback.

## Jail environment and credential options (2026-11-07)

In this tree, **not built into `bin/` and not deployed**. `jail` gains opt-in
hardening, all off by default so existing configurations are unchanged:

- `jailScrubEnv` gives a jailed session a minimal environment instead of
  `os.Environ()`: `PATH`, locale, `TERM`, the projected `HOME`/`TMPDIR`, the
  preset `env` map, and `jailEnvAllow` names. Desktop, D-Bus, SSH-agent,
  keyring, shell-hook and cloud variables are dropped.
- `jailHideCredentials` masks the built-in `CredentialHiddenPaths` list:
  `~/.ssh`, `~/.aws`, `~/.config/opencode`, keyrings, browser profiles, cloud
  config, and package credentials.
- `jailEnvAllow` and `jailPathAllow` grant exact environment names and paths
  globally or per agent; `jailHiddenPaths` adds masks. An allow inside a masked
  parent is refused at session start, not silently ignored.
- This is not `strict`. The session still runs as the same uid and still has
  network access; the default `off` mode still exposes the master token, state
  DB, and unauthenticated session-host socket. The analysis is in
  [`docs/reviews/rogue-agent-threat-2026-11-07.md`](docs/reviews/rogue-agent-threat-2026-11-07.md).

Verification: `/usr/local/go/bin/gofmt -l` clean; `go vet ./...` and
`go test ./...` pass. New tests: `TestJailEnvironmentScrubKeepsOnlyAllowedValues`,
`TestSessionEnvironmentInheritsWithoutScrub`, `TestCredentialHiddenPathsCoverSecrets`,
`TestMergeUniquePathsDropsRootAndDuplicates`, `TestNormalizeJailHiddenPaths`,
`TestNormalizeJailAllowLists`, `TestNormalizeJailAllowRejectsUnsafeNames`,
`TestJailAllowListsExemptAndCombine`. No daemon, session host, or desktop
window was started or restarted.

## Alert settings, ocd discovery, E2EE typing budget, terminal sizing (2026-11-06)

Tree commit `a36cd1a` (dirty) built and deployed as `0.1.0-dev
(v0.1.0-rc.5-2-ga36cd1a-dirty)`. The daemon was replaced at 2026-11-06
07:15: `serve` pid 86729 runs `~/.local/bin/darkfactory` (mtime
07:15), `darkfactory status --json` and `/api/v1/state` report the new build
and UI `2026-11-06.04`. `darkfactory stop` left the detached session host
pid 5643 and its six running PTY sessions untouched. The desktop binary is
also the tree build in `~/.local/bin`; the window running since 07:07 (pid
75516) executes the 07:02 build (its inode now reads `(deleted)`), which
includes the VTE pane fix, but a window opened before 07:02 was still the old
pane and its screenshot showed the 110×32 stale grid.

- **Alert settings.** `Notification` carries `alert`; the ring keeps every
  row and only `alert:true` calls `notify-send` or the Android service.
  `GET/POST /api/v1/notifications/settings` (verb `notification.settings`)
  persists `finishedSessions`, `replies`, `blockedRuns`, `prompts` and
  `childSessions` in the new `settings` bucket. Defaults are quiet: replies
  and child/subagent chats do not alert; finished/lost PTYs, blocked runs and
  prompts do. An always-visible **Alerts** control in the topbar (icon-only on
  a phone) and the bell panel's **Settings** button open the dialog. Android
  posts only alerting rows; blocks/prompts/lost sessions use the HIGH vibrating
  channel, finished sessions and replies use a new non-vibrating activity
  channel. Child chats are not even polled unless enabled. The first build had
  a checkbox-id typo (`alertBlocked` vs `alert-blocked`) that made the dialog
  load every box unchecked; the UI gate now asserts the real defaults.
- **E2EE typing.** The envelope budget moved from 300/900 to 1200/3600 per
  minute (source/global). The Android proxy passes a 429 through as
  `e2ee_rate_limited` with `Retry-After` instead of relabeling it
  `e2ee_failed`; `flushInput` puts the failed batch back in front of newer
  keystrokes and retries with a short backoff, so a throttle no longer drops
  typed bytes. Crypto (P-256 ECDH/HKDF/AES-GCM, replay, clock window) is
  unchanged.
- **ocd / OpenCode discovery.** `openCodeClient` discovers the newest reachable
  loopback registration from `$XDG_STATE_HOME/opencode/service.json`,
  `service-v2.json`, `service-local-install.json` and the legacy config file,
  preferring the canonical `service.json`; registrations are probed with their
  advertised Basic password (a JSON `data` array is required) and cached for
  10 s, so a stopped service does not add a probe to every `/state` poll.
  `available()` now means a live service, not a stale file. A non-default
  `opencodeAPIURL` still skips discovery. An `ocd` agent preset was added. A
  manual probe on this host resolved `http://127.0.0.1:49374` (ocd 2.2.2), not
  the older 26706 row.
- **Terminal fill.** The native VTE pane no longer applies the browser's xterm
  cols/rows (different font metrics); VTE derives its grid from the allocated
  bounds and the attach client posts that size through its own PTY. The attach
  client registers SIGWINCH before the first resize and re-reads the PTY size
  after the handler is live, closing the lost-signal window. The page
  re-sends measured bounds after the pane reports ready, forces one measured
  fit/repaint when it enters or restores the session view, and offers a **↻**
  control that refits, repaints and requests one SIGWINCH without resizing the
  PTY. WebGL is attached only after the first successful fit: creating the
  context while the page was hidden left some Android WebViews blank until a
  manual switch. The phone APK still bundles the older client until CI rebuilds
  it; the daemon-served desktop/browser UI is current.

Verification run: `go vet ./...`, `go test ./...`,
`CGO_ENABLED=1 go test -race ./internal/httpapi ./internal/store
./internal/config`, `make ui-check`, `make ui-gate` (with
`DF_CHROMIUM=/snap/bin/chromium`; both gate and hostile passed, UI
`2026-11-06.01`), `make desktop-check`, `make clientbench-check`. New tests:
`TestNotificationSettingsSeparateAlertsFromTheRing`,
`TestNotificationSettingsEndpointRoundTrip`,
`TestChildSessionsFollowTheAlertSwitch`,
`TestOpenCodeDiscoveryPrefersTheLiveRegistration`,
`TestOpenCodeExplicitURLSkipsDiscovery`. Android was not compiled locally
because `ANDROID_HOME`/`sdk.dir` is absent; the Kotlin changes are small and
compile-checked by CI. After deployment `/state` reported `opencode:true` and
the seven hosted sessions stayed `running`, but real-phone notification
delivery and the native VTE window are still unverified: the desktop window
must be reopened before judging the pane.

## CI green: the gate, the probes, and release signing (2026-11-03)

`ci` is green on `7818d31`: `vet, test, race, ui`, all four builds, `ui gate
(browser)` (gate + hostile), and `release artifacts`.

The gate fixes, in the order the failures surfaced:

- The narrow repo drag reloaded while the order POST was in flight; the test
  now waits for `/api/v1/repos/order` instead of trusting `drag()`'s settle.
- `centerWorktree` scrolls the tree, so calling it for both ends moved the
  dragged row after its box was read. Center the dragged row once, read the
  target without scrolling, and wait for `/api/v1/worktrees/order`.
- `hostile` required `playwright` directly and could not see CI's dev-only
  install under `tools/clientbench`. Every probe now uses
  [`test/browser/playwright.cjs`](test/browser/playwright.cjs), which prefers
  `DF_PLAYWRIGHT_MODULE`, then a local install, then the clientbench module.
- `TestSessionCapturesOutput` sampled the ring once after `Done`, which can
  close before the reader copies the final PTY chunk; it now polls briefly.
- The attention check finds the row for the session it just created; a probe
  fixture can exit mid-viewport and land a newer row, which made the
  "newest row" assertion flaky.

Release signing is configured: `ANDROID_KEYSTORE_BASE64` and
`ANDROID_KEYSTORE_PROPERTIES` are repository secrets. Android run `41628458623`
produced `darkfactory-android-release.apk` signed by `CN=DarkFactory Android`
(cert SHA-256 `a671f0ba…`), `versionCode 4`, UI `2026-11-03.38`, with
`assets/web/app.js` byte-identical to the tree. It installs over the prerelease
APK without uninstalling. Back up the keystore: losing it makes future
in-place updates impossible.

The APK version is no longer hardcoded. `android/app/build.gradle` reads
`dfVersionName`/`dfVersionCode`; the Makefile and the android workflow derive
them from `git describe --tags --always --dirty` and the commit count, and the
workflow checks out full history so both are real. The workflow then verifies
the built APK's `aapt` badging against those values, so a checked-in number
cannot silently return. A build from a tagged commit reports the tag; one
commit later reports `0.1.0-rc.5-1-g<sha>`.

## The jail probe, CI-only fixes, and phone verification (2026-11-03)

`JailAvailable` only checked that `bwrap` was on `PATH`. On a host whose kernel
or AppArmor refuses unprivileged user namespaces (the `ubuntu-24.04` runner
does), `containment: "jail"` started bwrap and the session died with
`bwrap: setting up uid map: Permission denied` instead of falling back to
same-uid. `JailAvailable` now probes once with the same `--unshare-pid` shape
the jail uses (`jailUsable`); a failed probe reports `off` honestly.
`README.md` and `DESIGN.md` say the fallback also covers a refused namespace.
`TestJailUsableRejectsBrokenBwrap` covers the failed probe;
`TestJailProjectsHomeAndMasksHiddenPath` skips when the namespace is refused.

CI fixes the gate exposed, in order: `State` lacked the `ui` property (the
local `make ui-check` skips `tsc` when it is absent, so only CI saw it); the
runner did not have the GTK4/WebKitGTK/VTE headers `desktop-check` needs; and
the runner denies the user namespace above. `ci.yml` installs the headers and
sets `kernel.apparmor_restrict_unprivileged_userns=0` when the key exists.
`check` is green on `6bb2e97`. `ui gate (browser)` failed at
`reorder (narrow): repo order did not survive a reload`; that was the harness,
not the app: on the `dfphone` AVD (same `web/` bundle, real touch) a repo drag
persisted across force-stop and relaunch, and `/api/v1/state` reported the new
positions. It is fixed in the entry above. Evidence:
`test/artifacts/android-bell-session-2026-11-03/REPORT.md`.

The same AVD run verified the bell change end to end: paired over `adb reverse`
with the fingerprint compared, the OpenCode question row opened the **Session**
view with the live PTY (not the chat panel), and `echo ANDROID_OK` round-tripped.
The APK was built from this tree with `assembleDebug --offline`. It is
debug-signed; the CI artifact remains debug-signed until the keystore secrets
exist.

## Bell rows follow the session, not the chat (2026-11-03)

UI `2026-11-03.38`. Clicking an OpenCode row in the bell (question, permission,
reply) used to enter the chat panel. It now opens the workspace's terminal
session: the live session whose agent is `opencode` first, then any live
session, then the newest; a workspace with no session falls back to its session
page. PTY rows already opened their session and are unchanged.
`web/app.js` `followAttention` + `chatSessionTarget`; `test/browser/activity.cjs`
now expects `data-view="worktree"` for its no-PTY fixture. Requirement UI-34
amended to say the chat panel is never the destination.

The same build closes the generation trap that made the first `install-restart`
look broken: an open desktop window keeps the app.js it already ran even though
the daemon serves the new shell with `no-store`. `/api/v1/state` now carries
`ui` (the shell version embedded in the binary, `web.Version`). When that
differs from the page's own `UI_VERSION`, the page fetches `/` and reloads only
if the shell this origin would serve is also different (`reloadIfStaleUi` +
`servedShellVersion`, guarded by `sessionStorage` so a rollback or a cache that
ignores `no-store` cannot loop). The origin check is what keeps the phone safe:
the APK serves its own copy of `web/` from assets, so a newer daemon alone must
not reload the page. A page already running a pre-.38 shell has no guard and
still needs one manual reopen.

Verified in the tree: `make check` exit 0; `TestUIVersionMatchesAssetQueries`
checks `web.Version` against the shell constant; `TestStateResponseCarriesBuild`
checks the `ui` field; `bin/darkfactory` rebuilt (sha256 `037da5d7…`, embeds
`2026-11-03.38`). **Not run:** `test/browser/probe.sh activity` / `make ui-gate`
— the Playwright node module is not installed here. The installed daemon was
still the `.37` build (`882ab9d5…`) when this entry was written; the operator
stack was not restarted.

## Listener deadlines, an envelope budget, and origin precision (2026-11-03)

Security hardening from the source review in
[docs/reviews/security-analysis-2026-11-03.md](docs/reviews/security-analysis-2026-11-03.md).
**In this tree: not built into `bin/`, not deployed, and no operator service was
touched.**

- `/api/v1/e2ee` has a per-source (300/min) and global (900/min) budget checked
  before the body is read (`allowE2EE`), with the new `e2ee_rate_limited` 429
  refusal. The pair and device limiters now spend the global budget only after
  the per-source check, so one source cannot exhaust the shared allowance.
- Listener deadlines are configurable in `config.json` and as `serve` flags:
  `readHeaderTimeout` 10s, `readTimeout` 60s, `writeTimeout` 60s,
  `idleTimeout` 2m, `maxHeaderBytes` 1 MiB; `"never"`/`"0"` disables one.
  Terminal and event streams clear their own read and write deadlines, so a
  finite `writeTimeout` does not truncate a live session.
  `TestTerminalStreamOutlivesListenerTimeouts` fails on a build without that
  clearing (`unexpected EOF`).
- Origin checks compare the port as well as the host, and a browser request
  whose `Sec-Fetch-Site` is not `same-origin`/`none` is refused. This closes the
  same-host, different-port path that `SameSite=Strict` does not separate.
  `isLocalRequest` now delegates to `isLocalUIRequest`, so `/open`, `/fs/pick`
  and `/worktrees/{id}/reveal` reject `Forwarded`/`X-Forwarded-*`/`X-Real-IP`
  like the local UI already did.
- Logout, revoke-all and device delete drop the native pane capabilities they
  authorized; session delete drops that session's panes. `authFrom` defaults to
  read scope; client debug lines are stripped of control characters;
  `debugRequests` logs `r.URL.Path`, not the request URI; the CSP gained
  `object-src`/`frame-src 'none'` and a `Permissions-Policy`.

Verified: `make check` exit 0 (vet, tests, `-race`, ui-check, clientbench,
desktop). New tests: `TestE2EEBudgetIsPerSourceAndBounded`,
`TestE2EERouteRefusesOverBudgetTyped`, `TestOriginMustMatchPortAndFetchSite`,
`TestLogoutDropsNativePaneCapability`,
`TestTerminalStreamOutlivesListenerTimeouts`, `TestSanitizeLogLine`,
`TestHTTPTimeouts`. **Not run:** `make ui-gate` — the Playwright node module is
not installed on this host, and installing it was not requested. No browser,
phone, or desktop window was exercised for these changes.


## The activity list shows what happened, and Clear stays cleared (2026-11-03)

Follow-up to the reply/activity change, after the owner reported two live
defects: a "hello" answer in `icl_eval` never appeared in the bell, and clicking
an entry made the terminal below go blank dark.

- **The ring is the activity list.** A page load asked for a baseline, got only
  the prompts still waiting, and advanced its cursor to the current sequence, so
  every event that already existed was skipped. The live daemon held the
  `icl_eval` reply (row 2) while the window showed nothing. The web client now
  starts at `after=0`, so a page that loads after an event shows it. Opening the
  list re-reads the ring, which is how a row picks up a workspace link learned
  after the event and how a window that was hidden catches up. A prompt waiting
  at daemon start joins the ring without alerting; an open prompt is refreshed
  in the ring on later polls.
- **Clear all and × now reach the daemon.** They are audited mutations with no
  audit target (`POST /api/v1/notifications/dismiss` with `{ids}`, `POST
  /api/v1/notifications/clear`) that remove rows from the shared ring, so a
  reload does not resurrect what was cleared. `TestDismissAndClearNotifications-
  AreMutations` covers the ring edit and the ledger rows.
- **The source cannot be pushed out of the row.** The source line (session or
  chat, workspace, time) renders above the body and the body is clamped to two
  lines. The name falls back from `sessionTitle` to the PTY session title, then
  the loaded chat title, then `chat <shortId>`.
- **The pane no longer paints over a collapsed terminal.** `syncNativePane-
  Visibility()` hides the in-app GTK pane whenever `nativeBounds()` is null, in
  the same frame that sends bounds; it was only hiding for dialogs and the
  activity panel, so leaving the session view left a full-size dark overlay.

Verified in the tree at UI `2026-11-02.36`: `make check` exit 0; `make ui-gate`
PASS at 1280×900, 1200×800, 396×719 (the gate clears leftover ring rows before
asserting the empty state and waits up to 8 s for the Ghostty action across a
state-poll boundary); `test/browser/probe.sh activity` →
`{"baselineQuestion":true,"source":true,"manualClear":true,"noReplay":true,
"replyActivity":true}`, with `03-question-source-above-body.png` and
`04-reply-source-above-body.png` showing `Chat Q · repo · time` above the answer.

**Deployed generation (owner approved, 2026-11-03 09:13).**
`DF_STREAM_DEBUG=1 make install-restart` from `9d19c67`. What is live now:
`0.1.0-dev (v0.1.0-rc.4-18-g9d19c67)` per `status --json`, `serve` pid 92902
(`/proc/92902/exe` is the installed binary, not `(deleted)`), `/app.js` serving
`UI_VERSION = '2026-11-02.36'`. The session host (pid 12472) was not touched and
the five live PTYs stayed attached. A headless page against the live daemon
reports `ui 2026-11-02.36 · 0.1.0-dev (v0.1.0-rc.4-18-g9d19c67)`, a connected
state, an enabled bell and the explained empty list, and its debug log shows the
poll asking `GET /api/v1/notifications?after=0` — the ring read that replaces the
old baseline. The daemon restart reset the in-memory ring, so the two events from
before the restart (this chat's reply and the `icl_eval` one) are gone; new
activity repopulates it. The desktop shell was already running, so the owner's
window needs a reload to pick up `.36`.

## A normal OpenCode answer now reaches the bell (2026-11-03)

The owner asked why the list was blank after a "good morning" exchange with an
OpenCode chat: the agent answered and nothing appeared. The feed only contained
things that need a decision (finished PTY sessions, permissions, questions), so
a normal turn produced no row at all.

The daemon now watches each recent chat's `time.idle`: a forward move is a
finished turn and raises `opencode.replied` with the last assistant text as the
body, the chat title, and the workspace mapped from the chat's directory. The
last reply is fetched only when a turn finishes. The priming poll records the
idle values without reporting, so a daemon start is not a storm of every old
chat, while a chat that first appears after the watcher started reports its
turn. The same finished turn reports once. The OpenCode poll moved from 10 s to
3 s and the client attention poll to 3 s, so a reply shows up within a few
seconds.

Verified: `make check` exit 0; `make ui-gate` PASS at 1280×900, 1200×800 and
396×719 at UI `2026-11-02.35` (the body is clamped to two lines);
`test/browser/probe.sh activity` reports
`{"baselineQuestion":true,"source":true,"manualClear":true,"noReplay":true,"replyActivity":true}`
and its `02-reply-activity.png` shows `OpenCode replied / Good morning! What can
I help you with today? / Chat Q · repo · time`. New Go test
`TestNotificationReplyRaisesActivity` covers the report, the failed-turn body,
the priming silence and the unchanged-timestamp dedup. The owner's `icl_eval`
worktree is a registered workspace, so the row links there.

**Deployed generation (owner approved).** `make install-restart` with
`DF_STREAM_DEBUG=1`, built from `daed2fd`: `0.1.0-dev (v0.1.0-rc.4-16-gdaed2fd)`,
`serve` pid 61601, `/app.js` serving `UI_VERSION = '2026-11-02.35'`. The session
host (pid 12472) was not touched and the four live PTYs stayed attached. The
desktop shell is unchanged; a window reload picks up `.35`.

## The bell was a disabled button and questions never rang it (2026-11-03)

The owner reported that after the sidebar/activity work the bell was still "a
faint bell" that "doesn't interact with click". Two defects stacked:

- `renderAttention` set `disabled` whenever nothing was unread (and the markup
  shipped with `disabled`), so the bell ignored every click and painted muted.
- The OpenCode watcher only polled `/api/session/{id}/permission`. An agent
  asking a question raises a *form* (`/api/session/{id}/form`), which nothing
  watched: questions never became events at all.

Fixed in this tree, UI `2026-11-02.33`:

- The bell is never disabled. With nothing unread it opens the same list with an
  empty state that says what appears there; only the count and colour change.
- The watcher polls forms beside permissions and emits `opencode.question`
  ("Question for you", body = the form title) with `sessionTitle`,
  `openCodeSessionId` and the `worktreeId` mapped from the chat's directory.
  Both kinds share one open-prompt set, so a waiting prompt keeps its row and an
  answered-then-new prompt is a new event.
- A baseline read returns the prompts that are still waiting, so a page that
  loads after the question was raised still sees it. The ring is still not
  replayed: answered prompts do not come back. The page refreshes that baseline
  when the list opens.
- An open prompt that had no workspace link yet is refreshed on a later poll,
  and the client fills the link into the row it already has.

Verified: `make check` exit 0; `make ui-gate` PASS at 1280×900, 1200×800 and
396×719 at UI `.33` (the gate now asserts the bell is not disabled, that the
empty list is explained and Escape closes it, plus the old sequence); new
`test/browser/probe.sh activity` against `fixture-opencode-api.py` reports
`{"baselineQuestion":true,"source":true,"manualClear":true,"noReplay":true}`.
It proves the question raised before the page loaded reaches the bell, names its
chat and workspace, survives a reload while waiting, and stops after being
answered and dismissed. New Go test
`TestNotificationQuestionsReachTheBellWithTheirSource`.

**Deployed generation (owner approved).** `make install-restart` with
`DF_STREAM_DEBUG=1` installed `0.1.0-dev (v0.1.0-rc.4-14-gc240ee5-dirty)` —
`c240ee5` plus this change, built before the commit below — and restarted
`serve` (pid 40702). `/app.js` serves `UI_VERSION = '2026-11-02.33'`. The
session host (pid 12472, started 08:06, still `(deleted)`) was not touched, so
the four live PTYs stayed attached. A headless check against the live daemon
confirmed the bell is enabled, labelled "Agent activity: nothing unread", and
opens the explained empty list; the throwaway device that check created was
revoked. The desktop shell is unchanged.

## Sidebar actions and the activity list (2026-11-02 night)

Two requests from the owner, both implemented in this tree, **not deployed**:
the running daemon is still `0.1.0-dev (v0.1.0-rc.4-11-gbaf2899-dirty)` at UI
`2026-11-02.31`, i.e. the build from the previous entry.

**The attention bell was not usable when OpenCode asked a question.** Clicking
it jumped to the newest event and cleared that session; an OpenCode permission
is not a PTY session, so it had no target and the click only produced a toast.
The bell now opens an activity list. Each row names its source
(`sessionTitle` + workspace), shows the event body and time, opens the target
(PTY session, OpenCode chat, or worktree), and stays unread until the owner
presses its `×` or Clear all. `openSession` no longer clears events, which is
the "I manually clear it" contract. The daemon attaches the missing source: the
OpenCode permission event carries `openCodeSessionId` and a `worktreeId`
resolved from the chat's `location.directory` (longest-prefix match, so a chat
started in a subdirectory still names its workspace). The panel is an overlay
in the terminal rectangle, so `syncNativePaneVisibility` hides the in-app pane
while it is open, the same way it does for a modal dialog.

**The sidebar grew the actions the owner asked for** (UI-31…UI-33):
- Right-click a session or workspace row → Copy path and Reveal in file manager.
  `POST /api/v1/worktrees/{id}/reveal` takes an id, never a path, is
  loopback-only, and records `worktree.reveal`; the helper is started and
  released so a file-manager window does not hold the request open.
- Right-click a session → Pin session / Unpin session. `Session.Pinned` is
  server state written by `POST /api/v1/sessions/{id}/pin` (`{"pinned":bool}`,
  explicit so a retry cannot toggle it), audited as `session.pin`.
- Drag a session grip (or `↑`/`↓` on it) inside its workspace. `Session.Position`
  is written by `POST /api/v1/sessions/order` (audited `session.order`); pinned
  and unpinned rows are separate sort groups, so a drop never crosses the pin
  boundary and snaps back. `SessionLess` is the one comparator the store and
  `/state` share.

Both fixes touch `web/` (UI `2026-11-02.32`) and the daemon (new routes, model
fields, refusal codes `session_order_invalid`, `reveal_remote`,
`reveal_unavailable`, `reveal_failed`). The desktop shell is unchanged.

Verified: `make check` exit 0; `make ui-gate` PASS (with the rewritten
attention contract: list opens, row names its session, following it leaves the
bell raised, dismissing every row clears it); a new
`test/browser/probe.sh sidebar` asserts the menu labels, that Copy path puts
the worktree path on the clipboard, that a pin survives a reload, and that
`ArrowDown` on a grip moves the session in the sidebar and in `/state`. The
reveal probe never clicks Reveal: that would open a file manager on the host.
Not verified: the real GNOME file manager opening, an OpenCode permission
raised by a live agent, and the panel inside the Wails window (the pane-hide
path is the one already proven for dialogs).

**Deployed generation (owner approved, 2026-11-02 ~23:42).** `make
install-restart` with `DF_STREAM_DEBUG=1` installed
`0.1.0-dev (v0.1.0-rc.4-13-g6167ba1)`, which is the commit before this
paragraph (this note is docs-only). `serve` is pid 276477 running
`~/.local/bin/darkfactory` with no `(deleted)`, `GET /app.js` serves
`UI_VERSION = '2026-11-02.32'`, and the new routes answer (`reveal` on an
unknown id → 404, `pin` on an unknown id → `session_not_found`). The session
host was not restarted (pid 15718, still `(deleted)`, started 21:12), so the
PTYs survived; one OpenCode session finished on its own around the same time
(`exited`, no reason), which a daemon restart cannot cause. The desktop shell
binary is unchanged by this work. A live Reveal smoke test was deliberately
not run: it would open a file manager on the operator's desktop.

## Session Stop/New hung, and the desktop icon identity (2026-11-02 late)

Two desktop defects, both fixed in this working tree and **not deployed**: the
operator daemon and the installed `~/.local/bin/darkfactory-desktop` are older
builds.

**Stop and New session hung.** Both open a modal (`dlg-confirm`, `dlg-session`)
from the session view, where the in-app VTE pane is up. A `GtkOverlay` child
paints above the WebView, so the dialog rendered *under* the pane: invisible,
unclickable, and the click went to the terminal. Nothing reached the daemon, so
nothing could time out or error — it just looked hung. Measured: a 440 px
centred dialog lands at x 420–860 inside a terminal rectangle that starts at
x 280. Fixed by hiding the pane (not detaching it) while a modal is open: the
page emits `hide`/`show` from `openDialog`, `confirmAction` and the dialog
`close` event, and the shell calls `gtk_widget_set_visible` on the holder, so
the PTY stays attached and VTE keeps its screen. UI `2026-11-02.31`.

Evidence: a fixture page in the real shell window shows `dialog.open=true` with
no dialog on screen (pane on top), then the dialog above a hidden pane after
the change; `test/browser/native-dialog.cjs` (`test/browser/probe.sh
native-dialog`) drives the real page against an isolated daemon and asserts
`open → hide → show → close` for the "+" tab button and Stop, and fails on a
build from `baf2899` with "the New session dialog did not hide the pane". The
daemon ledger has no `session.create`/`session.delete` around the operator's
attempts, which is why the hang was silent.

**Deployed generation (owner approved, 2026-11-02 ~22:36).** `make
install-restart` with `DF_STREAM_DEBUG=1` installed
`0.1.0-dev (v0.1.0-rc.4-11-gbaf2899-dirty)`; `serve` is pid 196566 running
`~/.local/bin/darkfactory` (no `(deleted)`), and `GET /app.js` serves
`UI_VERSION = '2026-11-02.31'`. The desktop shell at
`~/.local/bin/darkfactory-desktop` is the same bytes as
`bin/darkfactory-desktop` (sha256 `74b27ce5a89d6a85…`). The session host was
**not** restarted (pid 15718, `(deleted)`, started 21:12), so the running PTYs
survived but the stream pair is now mixed: that host predates the 21:24 install.
Restarting it would end the four live sessions; the owner has to ask for it.
The pane and identity changes live in the page and the shell, so the mixed pair
does not affect them. The owner's launcher
(`~/.local/share/applications/darkfactory.desktop`) gained
`StartupWMClass=dev.darkfactory.Desktop`; a backup of the previous file is at
`/tmp/user/1000/opencode/darkfactory.desktop.bak`.

**The window was a second, unpinned dash icon.** The shell's window reported
WM_CLASS/app_id `darkfactory-desktop` (the executable name), which matches no
desktop file, so a pinned DarkFactory icon never grouped with the running
window. `Linux.ApplicationID` is now `dev.darkfactory.Desktop`; the program name
inherits it, so Wayland reports `xdg_toplevel.set_app_id("dev.darkfactory.Desktop")`
(measured with `WAYLAND_DEBUG=1`) and X11 reports the same WM_CLASS (measured
with `xprop`). `packaging/darkfactory.desktop` keeps its id `darkfactory.desktop`
— the id the owner has pinned — and now carries
`StartupWMClass=dev.darkfactory.Desktop`; GNOME 46's
`get_app_from_window_wmclass` checks that map before the file name
(`shell-window-tracker.c`, `shell-app-system.c` at tag 46.0).
**The owner's hand-written `~/.local/share/applications/darkfactory.desktop`
needs the same one-line `StartupWMClass` to group; that edit was not made.**

Not verified: a real OpenCode session with a physical click in the owner's
window, and the GNOME grouping itself (only the inputs to the lookup were
measured). `make check` and `make ui-gate` pass at this tree after these edits.

## Terminal lag resolution (2026-11-02 late)

The owner's OpenCode streams were slow in the Linux desktop shell. The failure
loop, evidence and standing rules are in
[`docs/reviews/terminal-lag-2026-11-02.md`](docs/reviews/terminal-lag-2026-11-02.md);
the short version: fixes sat in the tree while stale generations served the
operator (including test windows running deleted binaries), one daemon-only
deploy confirmed that the stream fix needs serve and host from the same build,
and an A/B on the new stack convicted WebKitGTK — the in-app VTE pane streamed
the same session smooth at ~30 KiB/s while the web view lagged. The owner
confirmed the fresh app is fast while streaming. Decisions still open: make
the pane the default desktop terminal (currently behind `--in-app-terminal`),
the pane's S4 gates (hostile output, latency probe, OSC 52), and note that
`docs/substrate-lessons.md` now carries the generation-skew and
full-pair-replacement lessons.

## Continuation handoff: separate Ghostty terminal (2026-11-02, updated)

The owner accepts a separate native terminal window. The implementation in this
working tree is **unfinished, not deployed and not security-certified**. Start
with [`docs/handoffs/native-ghostty-2026-11-02.md`](docs/handoffs/native-ghostty-2026-11-02.md)
for the current code, evidence and remaining acceptance work.

Verified in a real window on this host: the right-click **Copy/Paste** menu
works in a plain shell session. OpenCode's own copy uses OSC 52, which reported
"copied" while the window denied it; the owner rejected that. The current policy
forwards only a bounded, base64-validated OSC 52 write and runs Ghostty with
`clipboard-write=ask`, so the user sees the text in Ghostty's "Authorize
Clipboard Access" dialog and decides per copy. Reads and every other
OSC/DCS/APC string stay inert; the browser client is unchanged.
`mouse-shift-capture=never` keeps Shift+drag selecting terminal text when a TUI
enables mouse reporting. Final tree: `make check` and `make ui-gate` pass at UI
`2026-11-02.26`; native attach, OpenCode input/resize, the hostile filter,
the API launch and browser→native→browser probes pass against
`/tmp/df-handoff-bin4` (SHA-256 `46e2b314…`). Remaining: real-agent streaming
and typing, and the native Wails window. No operator service was installed or
restarted.

## In-app native terminal (2026-11-02, implemented behind a flag)

The owner wants the terminal **inside** the Wails window, not a second window.
Ghostty 1.3.1 has no embeddable widget, so the shell hosts a VTE GTK4 pane in a
`GtkOverlay` over the web terminal rectangle. It is fed by the existing attach
client, so input, resize, redraw and the SS-13 filter are the audited path.
The page mints a session-scoped pane capability (`POST
/api/v1/sessions/{id}/native-pane`, audited verb `session.native_pane`), posts
it over WebKit's script-message handler, and the shell passes it to
`native-attach` on fd 3. The shell never sees the master token; the capability
is single-stream, expires while unused, dies with the stream, and is refused on
every other route or session. Wails' JS runtime is absent because the page is
served by the daemon, so the bridge is `dfNative` rather than Wails events.

`--in-app-terminal` enables it; the default app still shows the web terminal and
the separate Ghostty button. S3 ran end to end against a disposable daemon: the
native pane appeared over the terminal area, typing and resize worked, and an
XTEST probe confirmed selection + `Ctrl+Shift+C` copies when VTE has focus.
VTE has no OSC 52, so a TUI's own copy is inert in this pane (context-menu copy
is supported); that and the hostile/latency gates are the remaining S4 work.
See [`docs/native-terminal-in-app.md`](docs/native-terminal-in-app.md).

**Fast-output stream fix (2026-11-02).** The owner reported that a shell is fast
but OpenCode is slow inside the pane. The host was writing one HTTP frame and
flush per PTY read, and when its 256-chunk subscriber buffer overflowed it sent
an **exit frame for a session that was still running**. The daemon trusted it,
marked the live session exited, and the pane froze on its last frame. Reproduced
with `seq 1 200000`: the old daemon delivered ~35 KB, then reported `exited`
while the host reported `running`. The fix batches queued chunks (256 KiB cap)
and flushes once per batch in both the host and daemon streams, sends no exit
frame when the session is running, and reattaches the daemon mirror to a fresh
snapshot when its stream drops. The fixed build delivered 1.49 MB in 47 ms
(~31 MB/s) and kept the session running. In the real in-app overlay on XWayland,
a 60 fps 110×34 synthetic TUI used 5–10% desktop CPU, 5–6% daemon, 1–2% attach,
and the session stayed running; VTE rendering is not the bottleneck. The first
key→daemon input probes measured 5–6 ms. A full real-OpenCode and xterm.js A/B
is still open. The fix needs new `serve` **and** session-host processes; neither
was installed or restarted on the operator's daemon.

## Rust terminal evaluation (2026-11-02)

The owner requires a right-click copy/paste menu and chose to skip standalone
Alacritty. Alacritty and Rio native PTY probes ran in isolated temporary roots;
Rio's geometry varied and a follow-up cursor-query smoke failed, so no fair Rio
performance result is claimed. Rio 0.5.28's core contains unsafe blocks despite
an upstream blog's broad safe-Rust description. Ghostty's installed default is
`right-click-action = context-menu`, but the menu and OpenCode mouse-mode
behavior have not been driven. Research artifacts and exact limits are in
`test/artifacts/native-rust-2026-11-02/REPORT.md`. None is a production client.

## Native Ghostty investigation (2026-11-02)

A disposable native Ghostty probe exercised the same hosted PTY as the WebKit
client across five alternating pairs. Ghostty processed all five bursts; the
WebKit probe completed three and timed out twice, with its window inactive at
the end of every run. The timing endpoints and geometries differ, so this is
not a foreground speed comparison. PTY identity and host socket inode survived
all client switches. One isolated OpenCode TUI accepted a test string in
Ghostty without submitting a prompt. Rio was not tested. The bridge is research
only and lacks keyboard forwarding and terminal safety controls. Evidence and
remaining gates: `test/artifacts/native-ghostty-2026-11-02/REPORT.md`.

## Native typing investigation (2026-11-02)

The running local daemon serves the same UI as this working tree. A controlled
WebKitGTK real-PTY test measured a median of 80 ms at `020c397` versus 27 ms in
the current tree for typing into an isolated OpenCode TUI, with five trials per
side and the startup key excluded. Shell medians were 71 ms versus 20 ms. The
probe uses synthetic key events and observes DOM text, not physical input or
pixel presentation, and the OpenCode test did not submit a prompt. The current
changes are therefore effective in this test but do not resolve the reported
lag conclusively. In a separate synthetic burst, an inactive but mapped WebKitGTK test
window stalled in stream delivery or frame batching; a timer fallback did not
solve painting and was removed. A run started with the GTK window active completed the burst, but the
window was inactive by completion; another timed out. Sustained active-window
behavior and real OpenCode streaming remain unverified. See `test/artifacts/native-typing-2026-11-02/REPORT.md`.

## Continuation: source update and desktop lag (2026-11-02)

The checkout has uncommitted changes based on `020c397`. Preserve them. The
release candidate below describes prior release evidence, not validation of
the current working tree. No active operator service was installed or
restarted during this work.

The user reports that the Linux Wails desktop shell's typing, terminal
updates, and UI feel slow. The shell is the relevant client: on Linux it is
WebKitGTK, and `webglPlatformAllows()` deliberately returns false there, so it
uses xterm's DOM renderer. Keyboard-to-visible echo was reproduced and measured
in that renderer at **42.3 ms p50 / 48.3 ms p90** (keydown to the character in
the terminal DOM, isolated daemon, real PTY, dpr 1.5). The server answered the
input request in 0-1 ms, so the loss was client scheduling, not the daemon or
the ledger.

Two deferrals were removed, each worth about one frame:

- the input request leaves in the microtask checkpoint after the keydown
  instead of a `setTimeout(0)` (that timer alone measured 8.3 ms p50);
- a PTY chunk of 8 KiB or less is written to xterm immediately instead of
  waiting for the next animation frame; larger bursts still coalesce per frame.

Measured after the change, same host and harness (three runs): **26.8-27.4 ms
p50 / 32.8-35.9 ms p90**. The remaining budget is ~9-10 ms before the request
resolves and ~17 ms until the character is in the DOM, which is one 60 Hz frame
from xterm's own render scheduler; that is the floor for this engine without a
protocol change. UI assets are version `2026-11-02.25`.

This is measured in headless Chromium with the same DOM renderer, not in the
native window: this host has a display and the GTK/WebKitGTK SDK, but no way to
synthesize keystrokes into a WebKitGTK WebView. The native shell still needs a
`?debug=1` run (`webgl skipped: WebKitGTK uses the DOM renderer`, or
`fractional dpr`) and a human timing. Large-output throughput is a separate
matter: the recorded WebKitGTK parse for a 1 MiB burst is ~6.5x Chromium's, so
"terminal updates feel slow" is an engine cost that renderer choice does not
fix; changing it means the engine evaluation, not this patch.

Verification on the socket-capable host: `make check` exited 0 and `make
ui-gate` passed at 1280×900, 1200×800 and 396×719 with UI `2026-11-02.25`,
daemon `0.1.0-dev (v0.1.0-rc.4-2-g020c397-dirty)`, and the served `app.js`
matching `web/app.js` (`DF_PLAYWRIGHT_MODULE` pointed at a local
`playwright-core`, `DF_CHROMIUM` at a local Chromium build). The C1 and C2
probes also pass: clicks reach the PTY and typed input reaches a new session.
None of that measures typing latency or renderer behaviour on the user's
desktop, and the rebuilt binary was not installed.

The uncommitted `Makefile` adds `make update-source` as a source-build install
and unmanaged daemon restart. It stages the binary with `mktemp`, refuses a
systemd-managed restart before installation, and stops on a shutdown timeout.
An isolated run under `/tmp/user/1000/opencode/df-update-shard` (port 8798,
XDG roots of its own, the operator daemons untouched) verified: first install
and start, upgrade over a live daemon, exit 0 in both, a real shell session
keeping its id, pid, `startTicks` and `incarnationId`, the same session host
and socket inode, and a new input round trip through the replacement daemon.
It also refuses a `serve.pid` that points at another program without killing
it.

Live PTY survival is therefore verified. Application readiness is not: success
only means "a new live pid appeared in `serve.pid`", with no health check, and
the daemon logs `listening` before `ListenAndServe` returns, so `serve.log`
claims success for a bind failure. The target starts `serve` with no flags, so
a daemon run with `--listen` or `--debug` comes back on the default port; it
compares `/proc/<pid>/exe` to the literal install path, so a symlinked or
relative install path refuses after the binary is already replaced; and a
failed start leaves the previous daemon dead with no rollback. `DARKFACTORY_STATE`
also disagrees with a `stateDir` in `config.json`, which can make the target
believe no daemon is running. Review those before relying on it, and do not
restart the operator service for a test.

The Wails shell on Linux is WebKitGTK, and `webglPlatformAllows()` deliberately
returns false there, so the desktop client uses the DOM renderer; a fractional
display scale skips WebGL as well. That is a plausible cause of the reported
typing and repaint lag, but it is still unmeasured and the shell has not been
run here. Do not change the renderer path or claim a win from the input timer
until the native window is reproduced and timed.

The terminal hardening slice is in this tree. `web/app.js` filters PTY output
through `filterPaletteOsc`, which drops OSC 4/10/11/12 sets and the
104/110/111/112 resets while still forwarding colour queries; that closes the
one hostile-output effect that survived configuration (repainting the theme to
hide text or impersonate another agent). It holds a sequence split across
stream chunks and drops an unterminated one past 4 KiB. `make ui-check` now
fails on `innerHTML`, `insertAdjacentHTML`, `outerHTML`, `document.write`,
`new Function` or `eval(` in `web/app.js`, and `make ui-gate` runs a second
browser probe, `test/browser/probe.sh hostile`, against a fixture that prints
clipboard, title, link, image, palette and query canaries. It asserts no
clipboard call, title change, navigation, image, external request or palette
repaint, and that the allowlisted replies still arrive. UI `2026-11-02.24`.
`make ui-gate` passed both halves and `make check` exited 0 here; the same probe
fails with `terminal output repainted the palette` against a daemon built from
`020c397`, so the assertion has teeth. The Android APK embeds the same `web/`,
so it inherits the filter, but no phone or native Wails window was run for this
slice.

Other uncommitted work adds the MIT license with a separate nonbinding request
against AI training, a threat model, a publication checklist, and upgrade and
terminal evaluation foundations. The
[terminal surface audit](docs/reviews/terminal-surface-audit-2026-11-02.md)
records what the shipped client enables, the ranked gaps (the still-open SS-13
allowlist, reply traffic that is indistinguishable from keystrokes, per-component
bounds, and an xterm-shaped CSP), and the assertions a replacement engine must
pass. It fetched nothing. The project license is not yet included in release
tarballs or Debian packages. No history rewrite or publication was performed.
See `docs/publication-checklist.md` before either action.

## Scope and status

Build a lean, private workspace manager for local agent work: repositories,
worktrees, detached PTY sessions, the local browser and Linux Wails shell, and
an Android remote client. The daemon remains the authority and survives client
closure. Android switches between saved hosts; an aggregated fleet is not part
of this release unless the user explicitly adds it. The larger supervisor,
governance, PWA, and multi-platform roadmap in older documents is historical
planning, not an active release commitment.

The current release candidate is `v0.1.0-rc.4`, built from
`da651ef`. It adds honest session state glyphs and an always-visible
topbar unread bell on top of the rc.2 reorder and notification slice. The
security slice it builds on is `fd56baa`; the previous client slice is `0c9e2b0`, with evidence in
[the previous handoff](docs/history/handoff-2026-11-02.md) and
test artifacts. Older paths and process
state there are historical, not instructions to use a running operator service.

This release candidate adds:

- Fleet order is two-level server state. `Repo.Position` orders repo groups via
  `POST /api/v1/repos/order`; `Worktree.Position` keeps the within-repo order.
  The fleet tree has a repo-header grip, and a one-workspace repo’s row grip
  moves the whole group. Keyboard `↑`/`↓` moves the focused grip. The UI gate
  seeds three one-workspace repos and two extra worktrees and verifies repo
  drag, lone-workspace drag, within-repo drag, keyboard moves, reload
  persistence and 16 px touch targets at desktop, narrow and phone widths.
- The daemon keeps a 100-event attention ring (session exited/lost, run
  blocked/waiting, OpenCode permission) at `GET /api/v1/notifications`.
  `desktopNotify` (default true) calls local `notify-send`. The Android app
  runs a `dataSync` foreground service that polls the feed through the pinned
  E2EE envelope, persists a per-host cursor, and posts local notifications. It
  uses no FCM, vendor push or webhook.
- Session tabs, fleet rows and the session header use state glyphs: a static
  green running dot, a finished check, a lost mark, and an amber bell for unread
  activity. A live PTY is not treated as proof of work: blank Codex and idle
  OpenCode screens stay still instead of spinning. Notification events carry
  `sessionId`/`worktreeId`; the topbar bell is always visible, dimmed and
  disabled when there is nothing unread, and shows a count when there is. The
  phone Fleet tab keeps its badge. Opening a session clears its bell; the bell
  opens the newest target. The UI gate checks the static running dot and the
  always-visible bell, creates and kills a real session, then checks the unread
  bell, phone badge, clear-on-open path and finished check at three viewports.
- The Wails shell probes `/healthz` and starts the daemon if it is not running
  (`7e1846f`; `--autostart=false` opts out).
- The security slice's DP-19 fingerprint check, DP-20 durable replay record,
  encoded-nested-envelope refusal, 4 MiB unauthenticated body cap, Android
  non-E2EE refusal, frame hygiene and `hostId` comparison remain in force.

Still not in this candidate: Web Push for an installed PWA and the optional
ntfy/Telegram webhook (MO-7); a merged multi-host fleet; and a full real-phone
package-B run. The notifications are local and the feed is in memory.

`docs/reviews/e2ee-protocol-review-2026-11-02.md` is an internal source review,
not an external security review. It records the threat model, every finding and
a resolution table. H-2 (authenticated response terminator), H-3 (forget on a
forged outer 401), H-5 (frame ceiling), H-6 (re-pair revocation) and H-7 (clock
offset) are deferred as wire/protocol changes; the rest are fixed.

## Verification at `da651ef` (2026-11-02)

Clean tree for the builds, Go 1.27.3, JDK 21.0.13+9, Gradle 9.0.0, Linux 6.14
x86_64, local TCP and Unix sockets available.

| Check | Result |
|---|---|
| `make check` | exit 0 |
| `make ui-gate` | PASS at 1280×900, 1200×800, 396×719; UI `2026-11-02.22`; static running dot, always-visible bell, unread-bell, clear-on-open and phone-badge assertions passed |
| `sh test/android/check-fingerprint.sh` (JDK 21) | exit 0 |
| `android/gradlew lintDebug assembleDebug assembleRelease` | BUILD SUCCESSFUL |
| `make dist VERSION=0.1.0-rc.4` | exit 0; daemon `0.1.0-rc.4 (da651ef)` |
| Clean install + `stop`/`serve` restart | rc.2 evidence; live rc.4 refresh kept the hosted PTYs and the running session ids (7485…, 6e24…, fb62…) |
| Signed APK | `cda0e2744aa3f15d8395e5433fbf9d2b78cd45e4008643b60f78309efdaf86af`; cert `CN=DarkFactory Android` |
| Debug APK | `5d07725d53d3fbe34954891ca963eac48953533d64eac93ca4c67be1a0f53109` |
| Desktop notification integration | rc.2 isolated daemon + fake `notify-send`; rc.4 changes only the in-app bell and glyph |
| Android notification integration | rc.2 API 34 emulator over `adb reverse`; rc.4 APK has the same service and new UI |

The reorder, notification and activity evidence is in
`test/artifacts/notifications-reorder-2026-11-02/REPORT.md`,
including desktop/narrow/phone reorder and unread-bell screenshots. The UI gate
used `DF_PLAYWRIGHT_MODULE` and `DF_CHROMIUM` because
`tools/clientbench/node_modules` is absent on this host. The daemon build is
CGO-free; Android uses the user-local JDK and SDK Build-Tools 35.0.0.

The signed APK and Linux artifacts from `v0.1.0-rc.4` are kept in the private
archive; they are not published in this repository. The APK has
`versionCode 4`, `versionName 0.1.0-rc.4`, and UI
`2026-11-02.22`, so it updates an rc.3 install signed by the same key.

## Verification at `fd56baa` (security slice, 2026-11-02)

Clean tree, Go 1.27.3, JDK 21.0.13+9, Gradle 9.0.0, Linux 6.14 x86_64, with
local TCP and Unix sockets available. The previous sandbox could not run these.

| Check | Result |
|---|---|
| `make check` (vet, tests, `-race`, UI check, clientbench, desktop) | exit 0 |
| `make ui-gate` | PASS at 1280×900, 1200×800, 396×719; UI `2026-11-02.19`; daemon `0.1.0-dev (fd56baa)`; served `app.js` matched `web/` |
| `sh test/android/check-fingerprint.sh` (JDK 21) | PASS |
| `android/gradlew -p android assembleDebug` | BUILD SUCCESSFUL |
| `android/gradlew -p android assembleRelease` | BUILD SUCCESSFUL, apksigner verified |
| `go test ./internal/httpapi ./internal/store -race` | ok |
| `make dist` under `umask 002` then `umask 022` | identical `SHA256SUMS` |
| Daemon binary | `19da11d2fab19a979e866b8ea695ee168284a94e780fd9ebee6dde030e9a4612` |
| Debug APK | `30707a70f8b1304f7bb12276c6c37c48c360d6feac6b039f0d8eb2bda45d6a18` |
| Signed release APK | `5c870856e80bf346bebc15912cb9ecb25327513a4d68bd667b907baba78888bc` |

The UI gate used `DF_PLAYWRIGHT_MODULE` pointing at the installed Python
Playwright driver and Chromium, because `tools/clientbench/node_modules` is
absent on this host. The daemon build is CGO-free; Android uses the user-local
JDK at `~/.local/opt/jdk-21.0.13+9` and SDK Build-Tools 35.0.0.

Focused tests added or extended: `TestE2EEReplayRefusedAcrossDaemonRestart`
(present and future-within-skew envelopes, real server close/reopen),
`TestE2EERefusesEncodedNestedEnvelope`, `TestE2EEEnvelopeBodyLimit`,
`TestE2EEReplaySweepIsBounded`, `TestE2EEReplayRecordSurvivesAtExpiryInstant`,
and concurrent same-id consumption.

Release/recovery evidence is in
test/artifacts/release-2026-11-02/REPORT.md:
clean build and `dist` for amd64/arm64, `sha256sum -c`, staticness, clean
install/uninstall from local artifacts, old → new upgrade, new → old rollback,
daemon restart, `stop`, double `serve`, and `--idle-exit` — all in isolated XDG
roots with the same session id and PTY surviving. That run used `0c9e2b0-dirty`
plus the integration changes and a synthetic `VERSION=0.1.0-test`; the
cross-umask fix was verified after it.

This does **not** prove a native Wails window after the security change, real
phone operation, or `.deb`/systemd/arm64 execution. Those remain open below.

CI run `41393941434` passed every job at `b3b5ec7` (check, UI gate, four
cross-builds, release artifacts). Tag `v0.1.0-rc.1` then published the
prerelease in the private archive with the Linux tarballs/debs, `SHA256SUMS`,
and a release-signed Android APK
attached by hand. The APK is signed by `CN=DarkFactory Android` (SHA-256
`A6:71:F0:…:33`) and installs by sideload; CI does not build or sign it yet.

## Work packages

| Package | Status | Evidence and remaining work |
|---|---|---|
| A. Protocol review | Done (internal) | `docs/reviews/e2ee-protocol-review-2026-11-02.md`; confirmed defects fixed with tests; deferred items listed in its resolution table. Not an external review. |
| B. Android device acceptance | Partial | A real phone installed the rc.1 APK and paired over Tailscale; the owner reported normal use. The full matrix (notification delivery, permission allow/reject, interrupt, Forget, 16-host cap) is not recorded, so package B remains open. |
| C. Release and recovery | Done for rc.4 artifacts | `test/artifacts/notifications-reorder-2026-11-02/` for this slice; clean install/upgrade/rollback/restart evidence is from `fd56baa` and was not repeated. Direct APK sideload is decided; CI signing/store distribution remain open. |
| D. Scope and documentation | Done | README, DESIGN, requirements, agent-reference, substrate-lessons, native-client, Android README and `docs/release-checklist.md` aligned. |
| E. Integration | Done at `da651ef` | Shared gates rerun at the release candidate; this handoff records the revision and limits. |

## Remaining limits

- A verified release upgrade mechanism is a future design. `make update-source`
  builds and installs the current checkout, then restarts an unmanaged daemon.
  [Upgrade foundation](docs/upgrade-foundation.md) defines ownership, trust,
  compatibility, recovery, and acceptance gates. No automatic checks are added.
  An isolated run verifies install, upgrade, restart and PTY survival; readiness
  proof and rollback are still missing.
- SS-13's escape allowlist is still open. xterm.js parses the full escape set;
  the client now drops output-driven palette and colour sets, and clipboard,
  links, images and the title stay inert by configuration, but reply traffic
  (DA, DECRQSS, DECRQM, colour queries, mouse) still shares the keystroke path.
  The [terminal surface audit](docs/reviews/terminal-surface-audit-2026-11-02.md)
  lists the gaps and the assertions any engine has to pass.
- The [threat model](docs/threat-model.md) sets a stronger target for hostile
  workloads and automated attackers. That target is not yet met. The
  [publication checklist](docs/publication-checklist.md) and
  [terminal evaluation](docs/terminal-evaluation.md) record open work.
- Real-phone package-B acceptance is not complete. The phone paired and worked
  over Tailscale on rc.1, but notification delivery, permission allow/reject,
  interrupt, revocation, Forget and the host cap are not recorded as a full run.
  An emulator and `adb reverse` do not substitute for that matrix.
- The native Wails window has not been re-run after the security change. Its
  six checks predate it in `test/artifacts/client-foundation/c3-wails/native-checks/`.
  Daemon auto-start was added in `7e1846f` and unit-tested; the native window
  itself still needs a fresh acceptance run.
- The notification ring is in memory; a daemon restart loses old events and the
  Android cursor accepts the reset. Web Push and the optional ntfy/Telegram
  webhook are not implemented (MO-7); the daemon makes no outbound notification
  call.
- Android foreground services are subject to vendor battery policy and Android
  14/15 background-start/`dataSync` limits. The service starts from the
  activity; `START_STICKY` is best-effort.
- The envelope route has no per-source rate limit; H-1 reduced the unauthenticated
  body from 16 MiB to 4 MiB but did not add throttling.
- Exact `ts == now ± 5 min` acceptance is covered by the store boundary test and
  code inspection, not by an HTTP test; the HTTP restart test uses a future
  timestamp 4 minutes ahead.
- Requests accepted before the security upgrade have no replay record and cannot
  be rejected later.
- The host private key is plaintext in `state.db`; ECDH is static-static (no
  forward secrecy); the client key is software-held; first pairing is TOFU.
- A release-signed Android APK is attached to `v0.1.0-rc.4` (SHA-256
  `cda0e274…`). The signing keystore lives outside the repository and must be
  backed up; CI signing, an update channel and a store listing do not exist. No
  `.deb` install, systemd user manager or arm64 execution was tested.
- No external security review has been performed.

## Release gates

- [x] Confirmed protocol findings are fixed and tested; restart replay and the
      future-timestamp window have passing tests. Deferred hardening gaps are
      recorded as accepted limits, not silently closed.
- [x] `make check` and `make ui-gate` pass at `da651ef`; daemon and APK hashes,
      the reorder gate, and the UI version are recorded above.
- [x] Fleet reordering is covered at three viewports with mouse and touch,
      including the one-workspace-per-repo layout and reload persistence.
- [x] Local desktop and Android notifications are implemented and covered by Go
      tests plus isolated desktop and emulator integration runs.
- [x] Honest running/finished/lost glyphs and an always-visible unread bell
      are covered by the UI gate at three viewports, including a real killed
      session, the phone Fleet badge and clear-on-open.
- [x] Hostile terminal output is contained: a fixture that prints clipboard,
      title, link, image, palette and query canaries produces no clipboard
      call, title change, navigation, image, external request or palette
      repaint, while the allowlisted replies still reach the PTY.
- [ ] Real-phone acceptance passes, including notifications, permissions and
      failure recovery.
- [ ] Linux clean install, upgrade, rollback, restart and PTY survival are
      verified at this exact revision. Isolated `update-source` evidence covers
      install, upgrade, restart and PTY survival in this working tree; rollback
      is not implemented.
- [x] README, requirements, design and this handoff describe actual scope and the
      remaining security and platform limits.

## Check commands

`make check`, `make ui-gate`,
`sh test/android/check-fingerprint.sh` with a JDK, and
`android/gradlew -p android lintDebug assembleDebug` (or `assembleRelease` with
`android/keystore.properties`). For Android interactions use
`test/android/README.md`. Check `make desktop-preflight` before native Wails
builds. The browser gate needs Playwright and Chromium; set
`DF_PLAYWRIGHT_MODULE` and `DF_CHROMIUM` when the repo's dev dependency or
browser build is absent. Use a host that allows local TCP and Unix sockets, and
isolated XDG roots for live tests. The desktop notifier can be exercised with a
fake `notify-send` on `PATH`; see
`test/artifacts/notifications-reorder-2026-11-02/REPORT.md`.
