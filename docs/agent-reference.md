# Agent implementation reference

Read the section relevant to the code you are changing. This document preserves
protocol details and known failure modes moved out of the always-loaded
`AGENTS.md`. It describes current contracts, not a mandate to expand scope.
Verify implementation facts against code before changing them.

For current priorities and unfinished client work, read `../HANDOFF.md` and
`client-foundation.md`. For the target specification, read `requirements.md`.
The root `AGENTS.md` owns the model policy and project-wide instructions.

## Commands

- `make build` → `bin/darkfactory` (`CGO_ENABLED=0`, `-trimpath`, version via
  ldflags).
- `make test` / `go test ./...` — seconds, hermetic. One package:
  `go test ./internal/httpapi`. One test:
  `go test ./internal/httpapi -run TestHealthAndAuth`.
- `make race` runs the same suite under `-race` (cgo is enabled for the test
  binary only; the shipped binary stays `CGO_ENABLED=0`). `make fuzz` explores
  past the committed corpus of `FuzzBrowserSnapshot` for 30 s.
- `make vuln` runs `govulncheck ./...`, a call-graph reachability scan against
  the Go vulnerability database: it reports what this code can reach, not what
  its pinned versions merely match. It needs network on the first run, so it is
  dev-only and **not** in `make check`; the shipped binary still makes no
  outbound call. Install with
  `go install golang.org/x/vuln/cmd/govulncheck@latest`.
- `make vet` and `make fmt` are the quality gate. No linter config and no
  pre-commit hook; CI (`.github/workflows/ci.yml`) runs `make check`, a
  cross-compile matrix and `make dist` on every push and pull request.
- `make dist VERSION=0.1.0` → `dist/`: two static Linux binaries
  (`amd64`, `arm64`), one `.tar.gz` per arch, one `.deb` per arch, and
  `SHA256SUMS`. It is the release recipe, run by CI from a tag; see
  `docs/releasing.md`. Two runs from one commit produce identical bytes
  (`SOURCE_DATE_EPOCH`, sorted tar members, `-buildvcs=false`), and CI checks
  that with a second build. Package C found that a different umask changed the
  staging modes that tar and dpkg recorded; the Makefile now pins tar modes and
  normalizes the `.deb` staging tree, and a cross-umask rebuild was verified
  identical. See `test/artifacts/release-2026-11-02/REPORT.md`.
- `make check` = `vet` + `test` + `race` + `ui-check` + `clientbench-check` +
  `desktop-check`. The added checks need Python 3 and Node's built-in test
  runner, but no npm install, native SDK, or Wails download. `make ui-check` runs
  `node --check web/app.js`, then `tsc -p jsconfig.json` when installed, else
  `bun check` on Bun 1.4+. `jsconfig.json` enables `checkJs`; `web/` stays plain
  JS today. Future frontend build tools must not become daemon runtime dependencies.
- `make run` → `go run ./cmd/darkfactory serve`.
- `sh test/android/check-fingerprint.sh` compiles `HostFingerprint` and its JVM
  test with `javac`/`java`; it pins DP-19 (the displayed fingerprint hashes the
  received host key) without an emulator. `make android-pq-check` extends the
  same idea to the v2 crypto and protocol (`tools/pqcheck` supplies Go vectors,
  Go decapsulates the Java ML-KEM ciphertext); `make android-pq-live` runs the
  same classes against a disposable `--e2ee-min-suite=2` daemon. The Android
  client builds with `android/gradlew -p android assembleDebug` (or
  `assembleRelease` for a signed APK) with JDK 21 and Build-Tools 35.0.0.
  Release signing reads `android/keystore.properties`, which is gitignored; the
  keystore itself stays outside the repository.
- The ordered release gates and their commands are in
  [release-checklist.md](release-checklist.md); `HANDOFF.md` owns current scope
  and process state.
- Dependencies are vendored and tracked: after a `go.mod` change run
  `go mod tidy && go mod vendor`, or Go reports inconsistent vendoring.
- Go 1.27+ (`go.mod` `1.27.3`, the smallest release with every stdlib
  advisory fixed). Never downgrade the `go` directive.

## Where things are

- `cmd/darkfactory` — `serve`, `stop`, `session-host`, `open`, `status`,
  `token` (`--rotate`, `--rotate-sessions`), `version`. `serve` writes `$XDG_STATE_HOME/darkfactory/serve.pid`
  after it holds the state lock; `stop` signals only that pid (after checking it
  is alive and is this binary) and leaves the host, so PTYs survive. A second
  `serve` reports the pid instead of bbolt's `timeout`. `session-host --stop`
  stops every hosted PTY. `serve --idle-exit 30m` exits the HTTP listener after
  no request has been in flight for the window; `launch --idle-exit` passes it
  to the detached process. The session host is not stopped, so PTYs survive.
- `internal/httpapi` — every route and the auth check. `Server.Handler()` is the
  single wiring point: a route not registered there is unreachable. `server.go`
  is long on purpose, one handler per route.
- `internal/session` — PTY manager with a bounded in-memory ring; `host.go`,
  `hostclient.go` and `remote.go` are the detached host (the host owns PTYs and
  rings, `serve` mirrors frames). `//go:build !windows`.
- `internal/gitwt` — shells out to `git -C <repo> worktree ...`; no git library.
- `internal/store` — bbolt, JSON values, buckets `repos` `worktrees` `sessions`
  `transcripts` `browsers` `deviceKeys` `runs` `plans` `steps` `ledger` `e2ee`
  `e2eeReplay`. No schema-version gate. The `e2ee` bucket holds the daemon
  identity file: P-256 PKCS#8, X25519 scalar, ML-KEM-768 seed and epoch (a
  legacy bare PKCS#8 row is upgraded in place); `e2eeReplay` holds hashed
  `(deviceID, id)` request records with an expiry millisecond stamp; `browsers`
  rows may carry a client SPKI public key or a negotiated envelope suite.
- `internal/config` — XDG paths, `config.json`, token creation; first run
  creates the directories, config and token. `policy.json` beside it is
  optional: absent means the built-in default.
- `internal/policy` — the policy file, `Parse`/`Load`/`Decide`, the four
  fail-closed properties and `Hash` (what the ledger stores). Pure Go, no I/O at
  decision time.
- `web/` — currently `go:embed`, plain HTML/JS/CSS, no build step and no dev server:
  rebuild the binary after editing. `index.html`, `style.css`, `app.js`, pinned
  `vendor/`. Desktop auto-opens the newest live session; mobile starts on Fleet
  with terminal bytes opt-in, and the Runs view lists real runs and steps.
  `localUIOnly` refuses this tree to remote peers; it is the local browser and
  the Android asset source, not a remote client.
- `android/` — Kotlin native Android shell: pair screen, Keystore token,
  build-time copy of `web/`, loopback origin plus API proxy, WebView navigation
  lockdown. It has its own Gradle project, needs JDK 21 and Build-Tools 35.0.0,
  and is not a root Go dependency. A release-signed APK is attached to the
  `v0.1.0-rc.1` prerelease and is emulator-verified with two saved hosts, one
  connected at a time; real-phone acceptance remains open. See
  `android/README.md`.
- `internal/httpapi/refusal.go` — the refusal catalogue (stable code → status,
  remediable, hint) and the JSON envelope; `gateway.go` — principal, decision,
  the ledger recorder and the `mutate` wrapper every authenticated mutation
  uses; `audit.go` — `GET /api/v1/audit`, the ledger read.
- `test/android/` — the phone harness for `docs/testing-android.md`: `lib.sh`
  (boot, reverse, shot, tap, swipe, nav), `check-blank.py` (ink share in the
  pane), `probe-pane.sh` (capture → switch → capture, exits 3 on a blank pane),
  `probe-opencode.sh` (chat picker + jump to latest, tapping the boxes the page
  logs rather than fixed coordinates). Load with `?debug=1&panel=0` so the log
  still posts but the panel does not cover the pane a screenshot check measures.
  The numbered test cases are in `docs/testing-android.md` under "What to test".
- `packaging/` — what a release installs beside the binary: the systemd **user**
  unit (`systemd/darkfactory.service`), the app-menu entry whose `Exec` is
  `darkfactory launch`, the SVG icon, and `debian/control.in` for the `.deb`.
  `install.sh` at the repo root is the curl-able installer: `--system`,
  `--prefix`, `--version`, `--desktop`, `--dry-run`, `--uninstall`; it always
  checks the archive against the release `SHA256SUMS` first.
- `.github/workflows/` — `ci.yml` (check, cross-compile matrix, dist +
  reproducibility), `release.yml` (tag → publish, manual run → artifact only),
  `vuln.yml` (weekly govulncheck).

## Invariants

- No telemetry, outbound calls, CDN assets or auto-update; the binary stays
  self-contained. The UI is part of that surface: `index.html`, `style.css` and
  `app.js` contain no absolute URL, and `internal/httpapi/ui_test.go` fails if
  one appears.
- The served UI identifies its build. `internal/httpapi.Build` is stamped from
  `git describe --always --dirty`, returned as `build` in `/state`, and printed
  in the footer with `UI_VERSION`. Bump `UI_VERSION` and every `?v=` query in
  `index.html` together; the static UI test fails if they drift. `make ui-gate`
  hashes the served `/app.js` against `web/app.js`, so a stale binary or cached
  client is caught before it is mistaken for a layout regression. See
  [ui-gate.md](ui-gate.md).
- The daemon serves `/`, `index.html`, and static assets only to a loopback
  peer with a loopback Host and no `Forwarded`/`X-Forwarded-*`/`X-Real-IP`
  header. A remote browser gets `403`; remote `/api/v1/...` stays available for
  a native client. Do not relax this so a remote browser can load the UI, and do
  not call browser-side crypto E2EE: the daemon would still supply the code.
- The native Android client bundles `web/`, keeps its per-host token in Android
  Keystore, and serves the page from a loopback origin. Its proxy attaches the
  bearer token upstream; the page has no token, cookie, or JavaScript bridge.
  Transport is TLS/Tailscale, with pinned host identity and app-layer encryption.
  Real-phone acceptance and protocol review remain open. See
  `docs/native-client.md` and `android/README.md`.
- Token: 32 random bytes hex, file `0600`, directories `0700`, constant-time
  compare in `httpapi.auth`. Never weaken.
- Browser sessions are bbolt rows keyed
  `SHA-256(instanceSecret ‖ cookie token)` — never the raw token — carrying
  device ID, label, scope, created, last-seen and expiry. The instance secret is
  a `0600` file beside the token (`instance-secret`), never in `config.json` and
  never logged; losing it fails closed, so every browser runs `darkfactory open`
  again. Each row carries a `SecretTag` (a hash prefix, never the secret), so
  rotating the secret drops every old row instead of listing devices that can
  never authenticate. `darkfactory token --rotate[-sessions]` writes both files
  atomically at `0600`; a running daemon picks them up only on restart. The cookie is `__Host-df_session`: `Secure`, `HttpOnly`,
  `SameSite=Strict`, `Path=/`, no `Domain`. Those flags are unconditional
  because the prefix requires them; loopback is a trustworthy origin, so
  `http://127.0.0.1:8787` still accepts the cookie. Idle expiry is 30 days with a
  90-day absolute cap from `createdAt`; the cookie is re-issued at most hourly
  while a device is active, and the check runs on load and on every auth.
  Logout deletes the row. Persisting them is what lets an open tab survive a
  daemon restart.
- Device-key auth is additive to cookies. After an authenticated login,
  `POST /api/v1/device-keys` (mutation verb `device.key.create`) stores an ECDSA
  P-256 SPKI public key and nothing else; the private half is non-extractable in
  the client's IndexedDB. `POST /api/v1/device-keys/challenge` and `/verify` are
  unauthenticated bootstrap routes: they require the daemon's own origin and
  `X-DF-Request: 1`, are rate-limited, and use a one-use 120 s nonce. The signed
  payload is `darkfactory-device-auth-v1\n<device-id>\n<challenge>`; WebCrypto
  emits raw `r||s`, which `verifyES256` accepts instead of ASN.1. Verify returns
  a short-lived (12 h) in-memory bearer session, hashed in `deviceSessions`; no
  cookie is set and no master token is exposed. The key inherits the registering
  principal's scope and workspace access, appears in `GET /api/v1/devices` with
  `kind: "key"`, and revoking it deletes the key and all its sessions. A 401 on
  challenge means revoked/reset; the client drops the stale key. A failed
  verify alone does not delete it (the nonce may have expired). Never add a
  native auth bridge or put the key or master token in localStorage.
- The wire value remains `full` for compatibility; the UI and product language
  call it **workspace**. `read` is the read-only value.
- Workspace access is the full-scope capability behind the terminal and the
  OpenCode write surface; they are deliberately not separate grants because an
  OpenCode turn can run workspace actions. A read-only cookie or device-key
  session is refused `POST /sessions`, `/stream`, `/input`, `/resize` and
  OpenCode prompt/command/permission mutations (`403 terminal_required` or
  `403 read_only`) while state, runs, chat transcripts and redraw stay
  available. The bearer token always has workspace access. The persisted
  `terminal` field is compatibility output derived from scope, not an
  independent switch.
- Caller scope is transport-stamped, never read from a body. The gateway marks
  a principal `Local` when it arrived with the master bearer token or through
  the host's loopback UI (loopback peer, loopback `Host`, no forwarding
  header). `pair.code`, `device.revoke` and `devices.revoke_all` are
  `host-credential` verbs: a remote paired device, even `full`, gets `403
  host_admin`. `device.key.create` is deliberately not in that tier because a
  native client on another machine registers its key after entering the master
  token. The local web UI is loopback-only, so pairing and revocation from the
  normal host flow are unchanged.
- A non-loopback listener classifies the TCP peer (loopback / private /
  tailnet / public) and refuses a public source before routing with `403
  source_denied`, unless `allowPublicSources`/`--allow-public` is set. LAN
  global IPv6 on one of the host's own interface prefixes counts as private.
  Forwarding headers never change the classification; a loopback peer behind a
  TLS proxy is trusted as that proxy. `/healthz` is not exempt.
- `POST /api/v1/pair/code` is authenticated; `POST /api/v1/pair` is code-gated
  and unauthenticated. The code is 40 bits, 120 s TTL, one-time, hashed in
  memory, never logged, and carried in the URL fragment. `/pair` requires
  `X-DF-Request`, matching `Host`/`Origin`, and HTTPS for any remote or forwarded
  request — never plain remote HTTP. Rate limit 5 attempts/min per source,
  30/min globally. Link and QR use `publicURL` when configured and allow its
  host automatically; the QR is `darkfactory://pair?base=<publicURL>&code=<code>`
  for the native app, while the link is the browser URL. A loopback QR is for
  the local emulator only, not a real phone.
  The host chooses the device label and `full`/`read` scope; full is workspace
  access, the grant carries it to `/pair`, and the phone cannot override either.
  The legacy `terminal` request field is accepted but ignored; a full-scope
  device gets workspace even if an older client sends `terminal:false`.
- `/pair` sets the HttpOnly cookie. A browser uses only that cookie. A native
  client sends `clientPublicKey` (P-256 SPKI) and receives the 256-bit
  credential only as `sealedToken`, decrypted with the ECDH/HKDF key; a client
  that omits the key still gets the legacy `token` field for the local browser.
  The native client stores the sealed result in the OS keychain and sends
  `Authorization: Bearer <token>` inside the encrypted envelope, never in a URL,
  query, localStorage, log or error. A sealed pairing suppresses the
  `Set-Cookie` header entirely, so the raw token cannot reappear in a response
  header. Legacy native tokens are refused by the Android client; the local
  browser is unaffected.
- **Native E2EE envelope:** `GET /api/v1/e2ee/host-key` is public metadata
  `{v, maxVersion, suite, hostId, publicKey, fingerprint, fingerprintV2, bundle}`.
  The P-256 identity, a static X25519 key and an ML-KEM-768 key live in the
  `e2ee` bucket; `bundle` is those public halves plus an epoch, signed by the
  P-256 key. `POST /api/v1/e2ee` is the native transport. Its v1 JSON body is
  `{v, deviceId, id, ts, nonce, ciphertext}`; `ciphertext` is AES-GCM over
  `{method, path, headers, body, token}` with AAD `req|deviceId|id|ts`. The
  daemon looks up the paired row by device ID, derives the directional keys from
  the stored client public key and the host private key (HKDF-SHA256, salt
  `SHA-256(hostSPKI|clientSPKI)`), verifies the envelope token hash, rejects a
  timestamp outside ±5 min, records the request ID in `e2eeReplay` before
  dispatch, and retains it until `ts + 5 min` (DP-20), then dispatches the inner
  request through `Server.Handler()` with the bearer header. A storage failure
  refuses the request; a reused ID is refused after a daemon restart as well as
  in-process; expired records are pruned on the next consume. Requests accepted
  before this record existed cannot be rejected retroactively. Inner mutations
  still go through `Server.mutate`; the envelope route is only an authenticated
  bootstrap exemption because the credential is inside the ciphertext.
  The v2 suite `x25519-mlkem768-v1` adds `epk` (32-byte X25519 ephemeral public
  key) and `kem` (1088-byte ML-KEM-768 ciphertext) to the body and to the pair
  request (`{v:2, suite, epk, kem, sealedCode}`). The phone confirms the
  fingerprint before posting and encrypts the one-time code using a separate
  `darkfactory-e2ee-pair-init-v2` transcript, so the outer transport cannot
  transplant a visible code into its own key exchange. Keys are HKDF-SHA256 over
  `X25519(host, epk) || ML-KEM-Decapsulate(kem)` with the transcript as salt and
  `darkfactory-e2ee-v2` as info; both secrets are required. A v2 row has no
  stored client key and refuses a v1 envelope; under `--e2ee-min-suite=2` a
  legacy row and the unsealed pairing response are refused with
  `e2ee_suite_downgrade`. The minimum defaults to 2; setting 1 explicitly opts
  into legacy compatibility. Invalid pairing material is rejected before a
  grant is consumed or a device is created. The pair response puts the token, device id and
  accepted suite inside `sealedPayload`, and the phone pins `fingerprintV2`
  (identity plus suite). See
  `docs/reviews/e2ee-pq-hybrid-review-2026-11-09.md`.
- **Encrypted responses:** the outer response is always `200`,
  `application/x-df-e2ee`, chunked. Frame 0 is an encrypted
  `{status, headers}` head; later frames are body chunks with AAD
  `resp|deviceId|id|frameIndex`. `Set-Cookie`, `Content-Length`,
  `Transfer-Encoding` and `Connection` are stripped. Frames are flushed as the
  inner handler flushes, so PTY and OpenCode streams keep working. The Android
  proxy writes the decrypted bytes to the local WebView and never exposes the
  token or envelope route to the page. Completed v2 streams require an
  encrypted `DFE2EE-END-v2` frame under the distinct end-marker AAD; EOF before
  that marker is truncation, not successful completion. Legacy framing remains
  available only with the explicit compatibility policy.
  The envelope budget is `1200/min` per source and `3600/min` globally: a
  keystroke is one envelope, so the earlier 300/min budget throttled ordinary
  typing. A 429 is passed through as `e2ee_rate_limited` (not `e2ee_failed`);
  the page requeues the exact input batch in front of newer keystrokes and
  retries with a short backoff instead of dropping it.
- **Host pin:** the Android client pins `hostPublicKey` and computes the
  SHA-256 fingerprint from that key's SPKI DER, refusing a pairing response
  whose `fingerprint` does not match it (DP-19). For v2 it verifies the signed
  bundle under that same P-256 key, computes `fingerprintV2` from the identity
  and suite, and compares it with the value shown on the desktop; it also
  refuses a host that advertises `maxVersion >= 2` without a valid bundle
  rather than pairing as v1. New Android pairing always requires the hybrid
  bundle, including when every v2 advertisement is missing. Existing stored v1
  rows remain a host-policy compatibility exception. The desktop **Pair phone** dialog shows the
  matching fingerprint and suite. The user must compare them. A changed key or
  bundle fails `verifyHost` before the WebView loads, but the stored row and
  its pin are **kept**: only an explicit Forget removes a host, so a network
  peer cannot force a re-pair and a downgrade. A change during a live request
  fails AEAD authentication and returns to the host list for the same reason.
  A bundle rotation under an unchanged P-256 key and suite is accepted and
  re-saved. The first pairing remains trust-on-first-use if the user does not
  compare; do not claim otherwise.
- **Android v2 implementation:** X25519 and ML-KEM use pinned Bouncy Castle
  lightweight APIs through Java adapters in `crypto/`. The JVM download and
  Gradle build verify the provider artifact checksum. The application's wire
  code (`HostKey`, `V2Protocol`, `Envelope`) remains plain Java so JVM checks
  run the production code. Kotlin owns JSON, HTTP and UI. `make android-pq-check`
  checks interoperability and malformed inputs; `make android-pq-live` pairs
  with a disposable daemon and checks downgrade, replay and encrypted frames.
  The security workflow runs these on pull requests, compiles/lints Android,
  and fuzzes the protocol and terminal parsers. Neither a JVM run nor a
  symbolic protocol model substitutes for physical-phone acceptance or an
  external implementation review.
- Pairing creates a device record with its own hashed cookie row, never a shared
  cookie. Device-key rows use the same device list but a separate bucket.
  `GET /api/v1/devices` lists both (`kind: "key"` for a public-key device);
  `DELETE /api/v1/devices/{id}` and `POST /api/v1/devices/revoke-all` revoke.
  A `read` cookie or device-key session is refused on every mutation except
  logout and a PTY redraw (SIGWINCH only; resize stays blocked). Revoking a key
  also drops its in-memory sessions, so the next request is `401`.
  This is same-user convenience, not a sandbox: do not describe read-only as
  containment.
- Attach never replays a raw ring tail into a full-screen TUI: the stream
  suppresses that history, sends `X-DF-Redraw: 1`, and the client calls
  `POST /sessions/{id}/redraw`, a PTY size nudge that delivers SIGWINCH and
  leaves the size unchanged. Shells get a 256 KiB tail trimmed to a newline. The
  ring is a byte log, never a screen snapshot. A newline-free snapshot replays
  only if the ring never evicted, and the "escape-heavy, newline-free" TUI guess
  also requires a row/column address; otherwise a fresh shell attaches black
  until the user types.
- Every attach response starts with one SGR reset (`\x1b[0m`), a terminal no-op.
  A browser fetch hands nothing to the page until the first body byte, so a
  suppressed replay would leave the page blind to `X-DF-Redraw` and an idle TUI
  black. Keep the first byte non-empty and the client's attach/redraw path
  behind it.
- After the replay, a browser attach appends the input modes the app has enabled
  (`?1`, `?1000/1002/1003`, `?1005/1006/1015`, `?1004`, `?2004`). The client
  resets xterm before every attach, and a SIGWINCH repaint does not repeat the
  one-time enables, so without this the screen paints while clicks, paste,
  arrows and focus reports send nothing. Restore only observed, still-enabled
  modes; never guess one, and never recover them by replaying the ring. The
  host owns this state: it survives a `serve` restart and sees every PTY byte,
  so it appends its tracked restore to the attach snapshot
  (`internal/session/host.go`). Do not seed durable mode state from a bounded
  ring tail alone, and do not add a host frame kind that an older daemon would
  treat as a protocol error.
- One browser terminal owns a session; a new attach detaches the previous stream
  with a notice. Never let two clients resize one PTY. xterm.js 6.0.0 has no
  native touch scrolling (#5489): a vertical pan becomes a synthetic
  `WheelEvent` dispatched on the xterm screen layer, not the outer `.xterm`
  node, to reach xterm's wheel pipeline (scrollback, or the TUI's wheel/arrow).
  ↑/↓ send PageUp/PageDown; ⇣ sends End. Touch layouts load the official WebGL2
  renderer addon; if WebGL2 is unavailable or the context is lost, dispose it
  and keep the DOM renderer.
- A local workspace-scoped user may start Ghostty 1.3.1 through the audited
  `/sessions/{id}/native` route. `/state.nativeTerminal` reports whether the
  host can run it (`available`, `version`, or a `reason` such as
  `not_installed`, `unsupported_version`, `no_graphical_session`, `not_linux`);
  the UI hides the Ghostty action unless `available` is true, so a PC without
  Ghostty keeps the browser terminal and never offers a button that can only
  fail. The caller credential goes to `native-attach`
  on an anonymous inherited descriptor; the client uses that same principal's
  input, resize and redraw routes. It must not fall back to the master token.
  SIGWINCH is registered before the first resize and the size is read once more
  after the handler is live: a VTE reflow between the first POST and
  `signal.Notify` used to be lost, leaving the PTY at the old grid. The desktop
  VTE pane does not apply the browser's xterm cols/rows (the font metrics
  differ and a larger natural size clipped or under-filled the rectangle); VTE
  derives its grid from the allocated bounds and the attach client posts that
  size through its own PTY. After the pane reports ready, the page re-sends
  measured bounds so the widget matches the final layout.
  The native output filter and Ghostty flags constrain terminal-driven
  clipboard, links, title, palette, images and window operations; they do not
  complete SS-13's fixed allowlist. Re-run the isolated native attach, hostile
  output and real-window checks before changing them or the version gate.
- PTY output is filtered before it reaches xterm: `filterPaletteOsc` in
  `web/app.js` drops OSC 4/10/11/12 sets and the 104/110/111/112 resets, and
  forwards colour queries so a TUI can still detect the palette. Repainting the
  theme is the one hostile-output effect configuration does not stop: it can
  hide text (foreground equal to background) or impersonate another agent.
  Keep the filter chunk-safe (an introducer split between stream chunks) and
  keep `test/browser/probe.sh hostile` green; it runs in `make ui-gate`.
  Never widen the dropped set without a test, and never drop a query.
- The app shell is pinned to the visual viewport with `--app-top` /
  `--app-height`, never the layout viewport: on iPad the keyboard shrinks only
  the visual one, and a `100dvh` body leaves the terminal behind it. Touch form
  controls stay 16px so Safari does not zoom on focus. `visualViewport.scroll`
  fires at pan speed and each write relayouts the fixed shell, so those writes
  coalesce to one per frame and skip unchanged values. Never fit the terminal
  from a fixed timer: the keyboard animation outlasts focus and iOS reports the
  final size late, so poll and fit once it holds still for two frames (capped at
  1 s). Skip a no-op fit, since every fit repaints the visible rows. Focus the
  terminal on a confirmed tap only, or the keyboard shrinks the viewport under a
  scrolling finger. Entering the session view, restoring it at boot, and
  switching back to it on a phone force one measured fit and repaint after two
  frames; the terminal bar's **↻** control does the same on demand and asks the
  PTY for one SIGWINCH without changing the size. A WebGL renderer is attached
  only after the first successful fit, never while the page is still hidden:
  an Android WebView that created the context at zero size stayed blank until a
  reload or view switch.
- Route failures are one JSON shape, `{code, rule, target, detail, remediable, hint}`
  (GV-7). Codes live in `internal/httpapi/refusal.go` and are pinned by a golden
  test: add a code, never rename or re-status one. `remediable` is always
  present and a 5xx body carries no internal detail. Never answer an `/api/v1`
  route with `http.Error`; the private session-host socket keeps its own small
  protocol.
- Fleet order is server state, not a browser preference. There are two levels:
  `Repo.Position` (1..N across the fleet) is written by
  `POST /api/v1/repos/order` (verb `repo.order`, target `fleet`), and
  `Worktree.Position` (1..N inside one repo) by
  `POST /api/v1/worktrees/order` (verb `worktree.order`, target = repo id).
  Both are ordinary mutations, so the ledger explains a reorder and a second
  client sees it. `ListRepos` sorts positioned groups first and unpositioned
  ones after, by `CreatedAt`; `ListWorktrees` does the same inside a repo, so a
  new repo or workspace lands at the end instead of jumping to the top of an
  arrangement. A reorder names the ids in order; a sibling the caller did not
  name keeps its age order and goes last, so a stale tab cannot lose a row, and
  an id from another repo (worktree) or an unknown repo is refused rather than
  moved. The drag starts only on `.repo-grip` / `.wt-grip`: a pointerdown
  anywhere else in the row must still scroll the sidebar on a phone, so the
  grips alone are `touch-action: none`. A repo with exactly one workspace has
  no sibling row to swap, so its `.wt-grip` starts a repo drag instead of a
  dead handle. `renderTree` must not rebuild under an active drag, and a drop
  reorders the local list before the POST so the row does not snap back. A
  worktree cannot cross repos.
- Attention events are server state too, but not mutations: the daemon's
  in-memory ring keeps the last 100 `Notification` rows (session exited/lost,
  run blocked/waiting, OpenCode permission or question) and
  `GET /api/v1/notifications?after=<id>` returns newer ones. Omitting `after`
  is a baseline (return the current sequence, no old alerts); an explicit
  `after=0` asks for the whole retained ring. The Android foreground service
  persists a per-host cursor and baselines once, so a restart does not replay
  old events. The daemon sends local `notify-send` notifications when
  `desktopNotify` is true; it never sends the feed to a vendor push service.
  The OpenCode watcher polls at most 20 recently updated sessions for two kinds
  of blocking prompt: a permission request and a question form
  (`/api/session/{id}/form`, emitted as `opencode.question`). Both are removed
  from the seen set when answered, so a later request is a new event. It also
  watches each chat's `time.idle`: a forward move is a finished turn and raises
  `opencode.replied` with an excerpt of the last assistant text, because a
  normal answer asks nothing and used to leave the bell empty. The priming poll
  only records the idle values, so starting the daemon does not replay every
  old chat; a chat that appears later reports its turn. A prompt that is still
  waiting when the daemon starts is not alerted, but it does join the ring: the
  list a page loads later has to show what is blocking an agent, and an open
  prompt whose `worktreeId` is learned later is refreshed in the ring on the
  next poll. The web client starts at `after=0` and treats the ring as its
  activity list, so a page that loads after an event still shows it instead of
  baselining the event away; it re-reads the ring when the list opens, which is
  how a row picks up a workspace link that arrived after it did, and how a
  window that was hidden catches up. A baseline read (`after` omitted) stays
  silent for the Android service. Events are deduplicated by subject and a 30 s
  cooldown. Every event names its source: `sessionTitle` always, `sessionId`
  for a PTY session, and `openCodeSessionId` plus a `worktreeId` mapped from
  the chat's `location.directory` for a permission or question (an OpenCode
  chat is not a PTY session, so the two id spaces stay separate). The client
  renders the bell as a list of those events and clears one only on an explicit
  dismiss or Clear all: opening a row must not lose an event the owner has not
  read. `POST /api/v1/notifications/dismiss` (`{ids}`) and `POST
  /api/v1/notifications/clear` are audited mutations with no audit target: they
  remove rows from the shared ring, so a reload does not resurrect what the
  owner cleared. The bell is never disabled; with nothing unread it opens the
  list with an empty state, and only the count and colour change with activity.
  Every row renders its title, then the source line (session or chat,
  workspace, time) *above* the body, and the body is clamped to two lines: a
  long answer must not push the source out of the visible row.
- Alerts are configurable without losing the activity list.
  `GET /api/v1/notifications/settings` and
  `POST /api/v1/notifications/settings` (verb `notification.settings`) own the
  switches `finishedSessions`, `replies`, `blockedRuns`, `prompts` and
  `childSessions` in the `settings` bucket. Each `Notification` carries
  `alert`: the ring keeps every row, but only `alert:true` may call
  `notify-send` or wake the Android service. Defaults are quiet: finished
  sessions, blocked runs and prompts alert; replies do not; child/subagent
  OpenCode chats are not polled at all unless `childSessions` is on. The web
  bell's **Settings** button and the always-visible **Alerts** control in the
  topbar (icon-only on a phone) edit the same policy. Android posts alerting
  events on the HIGH vibrating channel only for blocks, prompts and lost
  sessions; finished sessions and replies use the non-vibrating activity
  channel.
- Sidebar order is server state, not a browser preference: `Worktree.Position`
  inside a repo (`POST /api/v1/worktrees/order`), `Session.Position` inside a
  workspace (`POST /api/v1/sessions/order`), and `Session.Pinned` for a session
  that sorts above both (`POST /api/v1/sessions/{id}/pin`, body
  `{"pinned": true|false}`, explicit so a retry cannot flip it back). The ids a
  client sends are a preference: an unnamed sibling keeps its place behind the
  named ones, and an id from another workspace or repo is refused rather than
  moved. `SessionLess` is the one comparator the store and `/state` share, so
  pinned rows float and a `Position` of 0 sorts after the dragged ones.
- `POST /api/v1/worktrees/{id}/reveal` opens a worktree's folder in the daemon
  machine's file manager (`xdg-open`, `gio open`, `open`, `explorer`) and
  records `worktree.reveal`. The path comes from the store, never from the
  request, so no route can reveal an arbitrary directory; a non-loopback caller
  gets `reveal_remote`, a missing directory `reveal_unavailable`. The helper is
  started and released, not waited on: a file manager window outlives the
  request, and waiting would hold the audited outcome row open with it.
- Every authenticated mutation goes through `Server.mutate` (GV-4): resolve the
  principal from the transport, decide, record, then act; a failing recorder
  leaves the effect unperformed. The route-table entry names the audit verb, and
  `TestEveryMutationGoesThroughGateway` fails when one is missing. `POST /open`
  and `POST /pair` are code-gated exemptions; `POST /device-keys/challenge` and
  `/verify` are the device-key bootstrap exemptions. `POST /e2ee` is an
  encrypted-envelope exemption: it authenticates its inner bearer before
  dispatch, and the inner route still records through `Server.mutate`. Their
  authority is a one-time code, a public-key signature, or the sealed credential,
  not a resolved principal at the outer route.
- Policy is data, not code (`internal/policy`): `$XDG_CONFIG_HOME/darkfactory/policy.json`,
  or `serve --policy`. `{version, rules[{effect, principal, verb, rule}]}` with
  `path.Match` globs over the principal label (`human*`, `human:device:abc`,
  `worker`) and the gateway verb. Every deny is evaluated before any allow, and
  no match is a deny. A missing file means the built-in default (human `*`,
  nothing else). A malformed file refuses startup rather than being ignored, and
  a pattern that cannot compile fails closed: a broken deny denies, a broken
  allow does not permit. The human-only gate (GV-8) runs before policy, so no
  policy can hand a worker a worktree delete. Policy applies to the human
  surface today because no other principal can reach the listener yet.
- The listener applies finite deadlines by default: `readHeaderTimeout` 10s,
  `readTimeout` 60s, `writeTimeout` 60s, `idleTimeout` 2m and
  `maxHeaderBytes` 1 MiB. Each is a duration in `config.json` (or a `serve`
  flag), and `"never"`/`"0"` disables that one. A terminal or event stream
  clears its own read and write deadlines at handler start, so a finite
  `writeTimeout` does not truncate a live session; a new streaming handler
  must do the same. `/api/v1/e2ee` has a per-source (300/min) and global
  (900/min) budget checked before the body is read, so an unauthenticated
  caller cannot force unbounded JSON, ECDH or AEAD work.
- The ledger (`internal/store/ledger.go`) is append-only and hash-chained: SHA-256
  over the row's canonical JSON with the hash blanked, and every row carries the
  previous row's hash. A decision is two rows — `allow`/`deny` before the effect,
  the outcome after it — so a crash between them leaves a decision with no
  outcome, which `darkfactory audit` prints as `unknown` and never as success. A
  denied principal is recorded with its rule. A create cannot name its object
  before it exists, so the decision row says `request` and the handler calls
  `setAuditTarget` to name it on the outcome row. Never update or delete a row:
  history is appended, and a broken chain is `audit --verify`'s exit 1, not a
  repair. The route is `GET /api/v1/audit`; the CLI goes through HTTP because
  `serve` holds the bbolt lock. Retention is `ledgerRetentionDays` (default 30,
  `0` keeps everything): `serve` prunes at startup and every six hours, and each
  prune writes a `ledger.prune` row carrying `{throughSeq, throughHash, dropped}`
  in one transaction with the deletes, so the survivors still verify and a
  deletion without a marker is a broken chain. Adding a row field means appending
  it with `omitempty`, or old rows stop verifying; `TestLedgerHashIgnoresAbsentFields`
  pins that.
- Releases are built by CI from a tag with `make dist`, never uploaded by hand;
  the artifact names (`darkfactory_linux_<arch>.tar.gz`, `SHA256SUMS`) are a
  contract `install.sh` depends on. Artifacts must stay byte-reproducible: do not
  add a step that embeds a time, host, user or build path. Every release
  publishes `SHA256SUMS`; note it is integrity, not authenticity — it is served
  from the same account, and nothing is signed yet.
- Workflows hold one secret, the default `GITHUB_TOKEN`, scoped to
  `contents: read` except `release.yml` (`contents: write`, tags only). Never add
  a token, a telemetry step or a third-party action that needs one without
  saying so here.
- IDs come from `internal/id` (128-bit hex) and are opaque; names are display
  only. Request bodies are capped at 1 MiB; `decodeJSON` rejects unknown fields.
- `GET /api/v1/fs/dirs` is the add-repo picker: bearer-gated, read-only,
  directories only (names and paths, no file contents), capped at 400 entries.
  Never a file browser, never an unauthenticated route.
- `POST /api/v1/fs/pick` opens the desktop folder chooser through
  `zenity`/`kdialog`/`osascript` and returns an absolute path. Bearer-gated, and
  only for loopback requests whose Host is also loopback (Tailscale Serve must
  fall back). Arguments as an argv, never through a shell; cancel is `204`;
  missing tool/display is `409` so the UI falls back to `/fs/dirs`.
- Bind `127.0.0.1:8787` by default. `config.ValidateListen` refuses a
  non-loopback bind without `--allow-remote`, and a plaintext non-loopback bind
  without `--insecure-remote`.
- The OpenCode panel is a view of one worktree, not a destination of its own.
  It is entered from the worktree or one of its sessions (topbar and mobile nav
  carry no chat entry), the list is filtered to that worktree's directory, and
  selecting another worktree switches the scope instead of leaving the panel
  pointing at the old one. A chat with no worktree is not a view.
- The OpenCode panel is a native chat client for the local service, not xterm.
  `opencodeAPIURL` stays loopback; the daemon reads the password from
  `opencodePasswordFile`, sends Basic auth itself, and never exposes the
  password, API URL or raw upstream errors to `/state`, the browser or the log.
  Both upstream `opencode` and the Rust rewrite `ocd` register their live URL
  and password in `$XDG_STATE_HOME/opencode/service.json` (or
  `service-v2.json` / the older `service-local-install.json`). The daemon
  probes the newest registrations and uses the first one that answers, caching
  the choice for 10 s; only registrations whose URL is loopback are eligible, so
  a writable state file can never redirect the service password. An explicit
  non-default `opencodeAPIURL` is the owner's override and skips discovery.
  `available()` reports a reachable service, not the existence of a stale
  password file. The `ocd` agent preset runs the same API. New chats resolve a
  DarkFactory worktree ID; the browser cannot pass an arbitrary directory.
  Pending permission requests must be visible and answerable
  (`once`/`always`/`reject`), or the agent stalls with no visible cause.
  Pagination omits `order` when a cursor is present, which already carries it.
  The event stream is volatile: use it as a hint and keep the slow fallback
  poll. Close the OpenCode TUI before using the panel so one session never has
  two active clients. The panel has an opt-in chat layout (`?chat=1`,
  remembered per tab, `data-chat`) that hides the fleet chrome; it is layout only
  — never the default, and it must not touch the terminal's Focus mode
  (`data-zen`) or the panel's routes. Message text renders a markdown subset by
  building DOM nodes: never `innerHTML`, never raw HTML from a message, and links
  only for http/https/mailto. `make ui-check` now fails on `innerHTML`,
  `insertAdjacentHTML`, `outerHTML`, `document.write`, `new Function` and
  `eval(` in `web/app.js`, so this is a gate, not a convention. A chosen slash
  command goes to OpenCode's command
  route, not the prompt route, and the `location` the command list is fetched for
  must be one of this daemon's worktrees.
- On a phone the chat switcher is a native `<select>` whose popup lives in the
  browser UI: never rebuild its options or reassign its value while it is
  focused, or Gecko leaves the popup on screen, the row tap still fires
  `change`, and the switcher behind it swallows every tap. Options key on chat
  id/title/agent/directory only (never `time.updated`, which ticks per message),
  the rebuild waits for `focusout`, and `change` blurs the select to end the
  popup. Debug builds log the picker, list and Latest-button boxes in device
  pixels so `test/android/` can tap what the page reports.
- The browser keeps the bearer token in `sessionStorage`, never `localStorage`;
  Disconnect clears it. CSP is `default-src 'self'`; the index response adds a
  per-response `style-src` nonce for xterm's injected `<style>` nodes and
  `style-src-attr 'unsafe-inline'` for xterm's dynamic truecolor attributes.
  Never add inline scripts, `onclick=` or authored `style=` attributes, and
  never widen `style-src` itself to `unsafe-inline`. Wire handlers with
  `addEventListener` in `web/app.js`; the nonce is stamped in that file before
  xterm starts. Responses are `Cache-Control: no-store`.
- `web/vendor/` is pinned xterm.js 6.0.0, addon-fit 0.11.0, addon-webgl 0.19.0
  and the MIT QRCode generator. Versions, provenance and SHA-256 values are in
  `third_party/README.md`. Never fetch production assets from a CDN. Pinned frontend build tools are permitted; the daemon needs no JavaScript runtime.
- `?debug=1` shows the in-page debug log (API status, dialog/browser events,
  click targets, Save/Copy) and posts it to `POST /api/v1/debug/client`;
  `serve --debug` logs one line per request and mirrors both to
  `$XDG_STATE_HOME/darkfactory/debug.log`. Both stay opt-in; never log tokens,
  request bodies or file contents.

## Landmines (verified in the current code)

- `POST /worktrees` builds `WorktreeRoot/<repo.Name>/<name>-<short id>` and
  branch `darkfactory/<name>`. The short id separates repos sharing a basename,
  but the display name must still be path-safe (`validName`), so a repo named
  with a slash cannot create a worktree. Rows created before the rename keep the
  `orca/` prefix: read `Branch` from the row, never rebuild it.
- `POST /repos` resolves symlinks, refuses a second row for an already-resolved
  directory (`409 repo_exists`), and creates one in-place workspace at the repo
  path: no branch, no isolation, no second worktree, and sessions write the real
  directory. The display name must pass `validName`; a non-git path is still a
  valid workspace.
- `POST /worktrees` on a non-git repo opens that workspace (`409` if it is
  already open); the name and base ref are ignored.
- `DELETE /worktrees/{id}` refuses (`409`, message names `force=1`) on
  uncommitted or untracked files, and checks *before* it cancels runs, stops
  sessions or drops rows, so a refused delete leaves the session intact.
  `?force=1` passes `--force` to `git worktree remove`; the browser asks a
  second, explicit question first. A worktree whose directory is already gone
  counts as forced, because git will not forget it otherwise. Workspaces are
  refused (`409`): remove their repo.
- `DELETE /repos/{id}` refuses (`409`) while git worktrees exist. Workspace
  records and their sessions are stopped and removed with the repo; files stay
  on disk. No `--cascade` yet.
- `/state` returns agent preset env **keys**, never values (`redactAgents`). 5xx
  bodies are generic, the detail goes to the log. Empty lists encode as `[]`
  (the UI still normalizes them in `normalizeState`).
- `POST /sessions` is idempotent only with an `Idempotency-Key`: it is stored on
  the row as `clientRequestId` and a repeat returns the existing session with
  `200` instead of a second PTY. The browser sends one per launch intent and
  reuses it until it succeeds; curl without the header creates a twin. Multiple
  live sessions per worktree are allowed and there is no writer lock, so
  concurrent agents can race the git index.
- PTYs live in `darkfactory session-host`, not `serve`. `serve` must detach from
  remote sessions on shutdown and never call `Stop`/`CloseAllWithReason` on
  them. The socket is `$XDG_STATE_HOME/darkfactory/session-host.sock`, `0600`,
  Unix-only; never expose it on TCP.
- `Manager.UseHost` must run before `httpapi.New` reconciles rows, or a live host
  session looks missing and gets a duplicate PTY.
- A restart keeps the same host PID, session ID and ring. If the host is gone, a
  live row restarts under the same ID with a new PTY and bounded transcript, or
  is marked `lost`. Never claim the original PTY survived. On a lost row,
  `DELETE /sessions/{id}` removes the record only: no PID is stored, so an
  orphan cannot be signalled.
- If `session-host` dies while `serve` runs, its sessions are marked `lost`
  (`host_lost`) and it is not respawned until the next `serve` start; those rows
  are restorable then.

## Tests

- Hermetic: `t.TempDir`, `httptest`, `t.Setenv`; no network, database server or
  external service.
- `internal/session` tests need a POSIX PTY (`/bin/sh`); the package does not
  build on Windows.
- `make check` includes `-race`. A mutex or channel change in `internal/session`,
  `internal/httpapi` or `internal/run` is not done until it is race-clean.
- Fuzz targets keep a committed seed corpus so `go test ./...` exercises them;
  `make fuzz` explores past it. `FuzzBrowserSnapshot` asserts the attach
  policy's invariants (replay is a suffix, starts on a line boundary, bounded,
  never beside a redraw), not a particular classification.
- Prefer the targeted package while iterating; the full suite is still cheap.
- `make ui-gate` is the browser half of the UI gate and needs Playwright,
  Chromium and an isolated daemon; it is evidence, not a unit test. It asserts
  the layout contract at 1280×900 and 396×719 and that the served `app.js`
  matches `web/app.js`. `make check` carries the static half, so a skipped
  browser run still fails on a version/id/asset drift.
