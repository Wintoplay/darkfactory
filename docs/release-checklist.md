# Release checklist

One ordered list of the release gates in [HANDOFF.md](../HANDOFF.md), with the
command that produces each result. `HANDOFF.md` owns scope, ownership, the
current revision and process state; this file does not restate them. Run every
step at the release revision, on a host that allows local TCP and Unix sockets
(a review sandbox may deny both). Do not install or restart the operator's
service: use isolated XDG roots and a non-default port.

For each step record the command, `git rev-parse HEAD` plus a clean/dirty
marker, build identity (`darkfactory version`, `/state.build`, `UI_VERSION`),
the result, and where the output lives. A step without recorded evidence is
open.

**Legend:** **[host]** ordinary Linux host · **[phone]** needs a real Android
phone · **[decision]** needs a human decision, not a test.

## 1. Freeze the revision [host]

- [ ] `git status --short` and `git rev-parse HEAD`; note uncommitted work.
- [ ] Packages A–D are merged by the integration owner; shared paths (`web/`,
      `internal/`, `android/`, `test/android/`) have one owner at a time.
- [ ] Scope matches `HANDOFF.md`: lean local workspace manager, loopback
      browser, optional Linux Wails window, Android remote client, one host at
      a time.
- [ ] Rename/copy the evidence directory for this run (for example
      `test/artifacts/release-YYYY-MM-DD/`) and keep commands and outputs there.
      The directory is gitignored: evidence stays local and is not published.

## 2. Build the artifacts [host]

- [ ] `make build`
- [ ] `make dist VERSION=<version>` → `dist/` tarballs, `.deb` files and
      `SHA256SUMS`; record `sha256sum` of every artifact.
- [ ] `./bin/darkfactory version` and the `build` field in `/state` match the
      revision.
- [ ] Two `make dist` runs from one commit under the **same umask** produce
      identical bytes (CI does this too). Repeat under a different umask; the
      Makefile pins tar member modes and normalizes the `.deb` staging tree
      after package C found the umask leaking into the artifacts.

Recorded 2026-11-02 in
test/artifacts/release-2026-11-02/REPORT.md:
clean build, `make dist` for both arches, `sha256sum -c`, staticness, and
same-umask reproducibility passed. Cross-umask reproducibility then failed; the
integration owner added `tar --mode` and a `.deb` staging `chmod`, and reran
`make dist VERSION=0.1.0-test` under `umask 002` and `umask 022` — the two
`SHA256SUMS` files matched exactly. That run used the dirty `0c9e2b0-dirty` tree
and a synthetic version, so repeat it at the release revision.

This proves the recipe builds. It does not publish a release, and it is not a
packaged Wails build.

## 3. Protocol and security gate (package A; reviewed, fixes applied) [host]

The changed code is `internal/httpapi/e2ee.go`,
`internal/store/e2ee_replay.go`, the Android pairing path and the proxy.

- [x] `go build ./...`
- [x] `go test ./internal/httpapi -run 'TestE2EE'`
- [x] `go test ./internal/store -run 'TestE2EEReplay'`
- [x] `go test ./internal/httpapi -run 'TestE2EEReplay'` — storage failure and
      the httptest restart/future-timestamp case.
- [x] `sh test/android/check-fingerprint.sh` with a JDK (DP-19: a substituted
      key with the old fingerprint is rejected).
- [x] `git diff --check`
- [x] Independent review of identity, replay, frame truncation, key lifecycle,
      clocks, storage failure and resource limits; threat model, findings and
      tests are in `docs/reviews/e2ee-protocol-review-2026-11-02.md`.
- [x] Fix or explicitly accept every finding. C-1 (encoded nested envelope),
      C-2 (unbounded replay sweep), H-1 (16 MiB unauthenticated body, reduced to
      4 MiB), H-4 (non-E2EE row could send a plaintext bearer), H-8 (frame
      hygiene) and H-9 (`hostId` not compared) are fixed with tests. H-2
      (authenticated terminator), H-3 (forget on a forged 401), H-5 (frame
      ceiling), H-6 (re-pair revocation) and H-7 (clock offset) are deferred as
      wire/protocol changes and recorded in the resolution table.
- [x] Limits stated: replay records start with the first request after the
      upgrade; the fingerprint is trust-on-first-use unless compared; the
      envelope route has no per-source rate limit; there is no external
      security review.

Integration run 2026-11-02: the review and fixes were developed on the
`0c9e2b0-dirty` tree; the full gates then ran on the clean `fd56baa` commit.
All focused Go tests pass under `-race` on a host with local TCP, and the JVM
fingerprint check passes with JDK 21. Do not mark this gate closed from a
passing test suite alone; the review's disposition table is the evidence.

## 4. Shared gates (package E) [host]

- [x] `make check` — vet, hermetic tests, `-race`, static UI half,
      `clientbench-check`, `desktop-check`.
- [x] `make ui-gate` — needs Node, Playwright, Chromium, an isolated daemon and
      local sockets; asserts the desktop (1280×900), narrow (1200×800) and
      phone (396×719) layout and that served `/app.js` equals `web/app.js`.
- [x] Record the daemon `build`, `UI_VERSION`, and the APK hash that pair
      together.

Integration run 2026-11-02 at clean `da651ef`: `make check` exit 0 and
`make ui-gate` PASS at desktop/narrow/phone, UI `2026-11-02.22`, daemon
`0.1.0-rc.4 (da651ef)`, served `app.js` matched `web/app.js`; the
gate also checks the static running dot, the always-visible disabled bell, the
unread bell from a killed session, the phone tab badge, clear-on-open and the
finished check. The gate used the
Python Playwright driver through `DF_PLAYWRIGHT_MODULE` and the installed
Chromium through `DF_CHROMIUM` because `tools/clientbench/node_modules` is
absent here. The same gate seeds three one-workspace repos and two extra
worktrees and asserts repo drag, lone-workspace drag, within-repo drag, keyboard
moves, reload persistence and 16 px grips at all three viewports; see
`test/artifacts/notifications-reorder-2026-11-02/REPORT.md`.

`make check` alone does not prove a native Wails window, Android usability or a
performance win.

## 5. Linux Wails window [host]

- [ ] `make desktop-preflight` (names missing pkg-config modules, installs
      nothing).
- [ ] `make desktop-build` → `bin/darkfactory-desktop`.
- [ ] Start an isolated daemon (step 7) and run
      `make desktop-run` or
      `./bin/darkfactory-desktop --url http://127.0.0.1:<port>/`.
      The shell auto-starts the daemon by default (`--autostart=false` opts out).
- [ ] Sign in, close and reopen the window, restart the daemon, stream a
      terminal, resize and send input, and confirm a foreign navigation or
      `window.open` is refused.
- [ ] Re-run after the security change: the existing native checks in
      `test/artifacts/client-foundation/c3-wails/native-checks/` predate it.

This is an evaluation shell built from `desktop/`, not a packaged desktop
release. Do not claim an installer or another platform from these checks.

## 6. Android artifact [host]

- [x] `android/gradlew -p android assembleDebug` with JDK 21 and
      Build-Tools 35.0.0.
- [x] `android/gradlew -p android lintDebug`; no notification/foreground-service
      errors.
- [x] Record the APK SHA-256 and the APK's UI asset identity.
- [x] Release signing: `assembleRelease` reads `android/keystore.properties`
      (gitignored) and a keystore outside the repository; the signed APK is
      attached to the `v0.1.0-rc.4` prerelease with a `.sha256` file.
- [ ] CI signing, an update channel, and a store listing are undecided. The
      prerelease APK is built and signed on a workstation, not in CI.

Integration run 2026-11-02 at `da651ef`: debug APK SHA-256
`bedf6b4dc418ad7db80bb511ef4c400d923da530e71c647abb6be573c924d567`; signed release APK SHA-256
`59af1e9ece8abbe2fb991918d1d3041489782b68d9a341e05379a6edcb73bb0d`, certificate
`CN=DarkFactory Android`, SHA-256
`A6:71:F0:BA:89:65:99:E0:AA:37:76:A1:F3:75:A3:97:B8:92:B3:7D:E4:B8:4C:52:35:92:34:E6:D3:C6:F8:33`.
The signed APK bundles `web/` at build time (UI `2026-11-02.22`) and has
`versionCode 4`, `versionName 0.1.0-rc.4`.

Emulator notification evidence: API 34, isolated daemon over `adb reverse`,
debug APK, app backgrounded, session exit produced a local `Agent finished`
notification while `NotificationService` stayed foreground. See
`test/artifacts/notifications-reorder-2026-11-02/REPORT.md`.
This is not a real-phone notification run.

## 7. Clean install, upgrade, rollback, restart [host]

Use a scratch root and a non-default port. Never point these variables at the
operator's real `~/.config`, `~/.local/state` or `~/.local/share`.

```sh
root=$(mktemp -d "${TMPDIR:-/tmp}/df-release.XXXXXX")
mkdir -p "$root/config" "$root/state" "$root/data" "$root/bin"
export XDG_CONFIG_HOME="$root/config"
export XDG_STATE_HOME="$root/state"
export XDG_DATA_HOME="$root/data"

# clean install from this revision
make build
make install BINDIR="$root/bin" DARKFACTORY_STATE="$root/state/darkfactory"
"$root/bin/darkfactory" serve --listen 127.0.0.1:8899 >"$root/serve.log" 2>&1 &
until "$root/bin/darkfactory" status --json >/dev/null 2>&1; do sleep 0.2; done
"$root/bin/darkfactory" status --json
"$root/bin/darkfactory" token
```

- [ ] Clean install: empty roots → add a repo, start a shell, see output, and
      record the session id and PTY pid. The state DB, token, instance secret
      and session host all live under `$root`.
- [ ] Upgrade: install the previous release artifact over the candidate (or the
      reverse), restart the daemon, and confirm the fleet and PTY survive and
      the new build is the one serving (`/state.build`, `make install-status`
      with the same `DARKFACTORY_STATE`).
- [ ] Restart: `"$root/bin/darkfactory" stop` then `serve` again; the detached
      session host and its PTY keep running and the same session id reattaches.
      A `serve` restart is not a host restart.
- [ ] Rollback: install the previous binary again and restart. There is **no
      schema-version gate** (OP-5 is open), so an older binary does not refuse a
      newer database. Record what actually happens; do not mark rollback as
      supported until it has evidence and a decision. The host process is not
      rolled back: it keeps running the build that started it until it exits.
- [ ] Leave the operator's service, pid files and XDG roots untouched.

Recorded 2026-11-02 in
test/artifacts/release-2026-11-02/REPORT.md:
clean install and uninstall, old → new upgrade, new → old rollback (including a
host started by the new build), `stop`, double `serve`, `--idle-exit`, and a
restart all passed across `HEAD^` ↔ working tree, with the same session id and
PTY. Limits from that run: one revision pair, one shell session per root, no
schema change, no `.deb` install, no systemd user manager, no real release tag.
Rollback worked because bbolt accepted the DB, not because a schema gate
approved it. Repeat at the release revision.

## 8. Real-phone acceptance (package B; open) [phone]

- [ ] Install the step 6 APK on a real phone and reach the daemon over
      Tailscale HTTPS (record the exact `tailscale serve`/SSH invocation and the
      URL).
- [ ] Compare the fingerprint the phone computes from the received host key
      with the desktop **Pair phone** value. A mismatch must be refused; a match
      is the only accepted state.
- [ ] Exercise PTY input, OpenCode chat, permission allow and reject,
      interrupt, reconnect, revocation, Forget, and the host cap.
- [ ] Grant notification permission, background the app, end a session and
      create an OpenCode permission request; confirm the local notifications
      arrive, the persistent service notification stays, and **Stop** stops it.
      Deny permission and confirm the app still connects.
- [ ] Record device model, Android version, APK and daemon hashes, commands and
      results.

An emulator does not substitute for the phone. Current evidence is the
`dfphone` emulator over `adb reverse` in
`test/artifacts/client-foundation/android-native/`; real-phone Tailscale is not
verified.

## 9. Documentation gate (package D) [host]

- [ ] `README.md`, `DESIGN.md`, `docs/requirements.md`,
      `docs/agent-reference.md`, `docs/substrate-lessons.md` and
      `docs/native-client.md` agree with the code and with the evidence above.
- [ ] No claim of real-phone Tailscale acceptance, an external security review,
      a packaged Wails release, or a signed/distributed Android artifact.
- [ ] Permanent requirement IDs are intact; deferred items are marked, not
      deleted.
- [ ] The integration owner has updated `HANDOFF.md` with the exact revision and
      remaining limits.

## 10. Release record

| Gate | Evidence path | Result | Open item |
|---|---|---|---|
| Artifacts | `test/artifacts/notifications-reorder-2026-11-02/`, `dist/SHA256SUMS` | PASS at `da651ef`; Linux tarballs/debs and signed APK attached to `v0.1.0-rc.4` | Clean install/rollback not repeated at rc.4 |
| Protocol review | `docs/reviews/e2ee-protocol-review-2026-11-02.md` | C-1/C-2/H-1/H-4/H-8/H-9 fixed; H-2/H-3/H-5/H-6/H-7 deferred | Not an external review; unthrottled envelope route |
| `make check` | `test/artifacts/notifications-reorder-2026-11-02/make-check.log` | PASS (exit 0) at clean `3a986aa` | — |
| `make ui-gate` | `test/artifacts/notifications-reorder-2026-11-02/ui-gate.log` | PASS 3 viewports, UI `2026-11-02.22`, daemon `0.1.0-rc.4 (da651ef)`; static-dot/always-visible-bell/unread checks pass | — |
| Fleet reorder gate | `ui-gate-*-reorder.png`, `REPORT.md` | PASS: 3 one-workspace repos, mouse/touch, lone grip, within-repo, keyboard, reload, 16 px | Real-phone reorder not recorded |
| Notifications | `REPORT.md`, `desktop-notify.log` | PASS: Go tests, desktop `notify-send`, API 34 emulator foreground service + session-exit notification | Real-phone notification matrix open; no FCM/Web Push/webhook |
| Wails native checks | `test/artifacts/client-foundation/c3-wails/native-checks/` | Prior evidence predates this change; daemon auto-start unit-tested | Re-run the native window |
| APK build | `android/app/build/outputs/apk/release/app-release.apk`, GitHub prerelease asset | PASS; signed APK SHA-256 `cda0e274…`, `versionCode 4`, rc.4 | CI signing/store listing; real phone |
| Clean install / upgrade / rollback / restart | `test/artifacts/release-2026-11-02/REPORT.md` | PASS at `0c9e2b0-dirty` (isolated XDG, PTY survived); live rc.4 refresh kept the hosted PTYs and running session ids | Repeat at rc.4 with clean install; `.deb`/systemd/arm64 untested |
| Real phone over Tailscale | owner report on rc.1; no artifact | Pairing/use worked informally | Full package B incl. notifications |
| Docs | README/DESIGN/requirements/agent-reference/substrate-lessons/native-client/checklist | Aligned 2026-11-02 | Keep current with code |

A release is ready only when every row has evidence and the open column is
empty or explicitly accepted by the owner. The gates themselves are in
[HANDOFF.md](../HANDOFF.md); this table records the result, not the process
state.
