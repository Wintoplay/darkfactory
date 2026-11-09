# DarkFactory Android client

Status: **built, E2EE-pinned, multi-host, and emulator-verified** on the
`dfphone` AVD (1080×2340). The Kotlin APK stored two hosts with separate tokens,
client keys, pinned daemon keys, and host ids; switched between them; showed a
down host as offline instead of losing the registry; confirmed each fingerprint;
reloaded without re-pairing; streamed a live PTY through encrypted frames; and
cleared the current token on logout or revocation. Evidence:
`test/artifacts/client-foundation/android-native/`.

The hybrid post-quantum envelope (v2) is covered by the JVM interop gate, a
live disposable daemon run, and an emulator acceptance run on `dfphone`:
paired over v2, loaded `/state`, showed `hybrid PQ` on the host card, and kept
the saved pin after the host identity changed. See
`test/artifacts/e2ee-v2-android/`. A
physical-phone Tailscale run under v2 is still open.

## What this is

A native Android shell with a bundled UI, written in Kotlin. It is deliberately
**not** a remote browser pointed at the daemon:

- `MainActivity` pairs with a daemon over HTTPS (plain HTTP is accepted only for
  loopback or the Android emulator). New pairing requires the advertised v2
  suite and its P-256-signed key bundle. The phone shows the computed
  `fingerprintV2` before it sends the one-time code; then it seals that code to
  the staged ephemeral X25519 and ML-KEM values. Older saved v1 rows can still
  use their existing P-256 client key, but new v1 pairing is refused.
- One encrypted registry holds several hosts. Each entry has its own endpoint,
  token, device id, E2EE keys, fingerprint, scope, and host id. A native picker
  connects, adds, or forgets a host; the app connects one host at a time.
- Before pairing sends the one-time code, the native dialog requires the user
  to compare the verified host fingerprint with the desktop **Pair phone**
  dialog. V2 sends only `sealedCode`; a substituted bundle, X25519 key, or
  ML-KEM key cannot redirect the code to another peer. The desktop QR encodes
  `darkfactory://pair?base=…&code=…`, so the phone camera can prefill the pair
  form without an in-app scanner or camera permission.
- `LoopbackServer` binds `127.0.0.1:<ephemeral>` but serves the APK's bundled
  `web/` tree only under a per-launch random `df-<nonce>.localhost` Host. The
  WebView loads that origin, not the daemon. A different app on the phone cannot
  guess the origin and use the proxy as a confused deputy; a root attacker still
  can.
- API requests from the page go to the same local origin. The native proxy
  seals `{method, path, headers, body, token}` into an AES-GCM envelope, posts
  it to `/api/v1/e2ee`, and opens the encrypted response frames. V2 requires an
  authenticated end marker; missing, invalid, or trailing frames fail the local
  response. The bearer
  token, request bodies, response headers, and terminal/OpenCode bytes are not
  visible to the transport. The page never receives the token, a cookie, or a
  JavaScript bridge.
- The proxy streams decrypted terminal and OpenCode frames without buffering.
  A local WebView abort is not a host-identity failure; it ends that response
  and lets the page reconnect.
- A `401`, successful logout, or AEAD failure returns to the host list (or to
  pair when there is no host left) but **does not delete the host or its pin**.
  Only an explicit Forget removes an entry, so a network peer cannot force a
  re-pair and a downgrade by answering with a bare `401`. A changed host key or
  bundle is likewise reported and refused, not erased. Other saved hosts are
  preserved. A daemon that returns an unsealed token is refused.
- Switching hosts destroys the previous loopback origin and WebView before the
  next one loads. Session, worktree, and chat ids therefore never cross hosts;
  there is no parallel merged fleet view.
- The WebView accepts only its own loopback origin for navigation, has no file
  access, no mixed content, no cookies, and no JavaScript interface.

## Remote browser policy

The daemon now serves `/`, `index.html`, and static assets only to a loopback
peer with a loopback Host and no forwarding headers. A remote `/api/v1/...`
request still works for this native client. A remote browser receives `403` and
must use the native client.

This is the trust boundary the earlier design was missing: the daemon no longer
hands JavaScript to a remote browser that the transport can rewrite.

## Hybrid post-quantum envelope (v2)

A v2 host advertises suite `x25519-mlkem768-v1` and publishes X25519 and
ML-KEM-768 public halves in a bundle signed by the P-256 pin. Every request
carries a fresh X25519 ephemeral key and an ML-KEM-768 ciphertext; the envelope
keys come from HKDF-SHA256 over both shared secrets plus a transcript that binds
the suite, bundle, device, request id and timestamp. The key schedule requires
both the X25519 and ML-KEM shared secrets. The suite is fixed per host row; a v2
row never falls back to v1. New pairing refuses a host that omits the verified
v2 bundle, even if its metadata resembles a legacy host.

X25519, ML-KEM-768 encapsulation, SHA3/SHAKE, and HKDF use Bouncy Castle's
maintained lightweight APIs (`org.bouncycastle:bcprov-jdk18on:1.86`). The
small Java wrappers in `app/src/main/java/dev/darkfactory/mobile/crypto/`
validate wire lengths and canonical ML-KEM coefficients, then delegate the
cryptographic operations to the library. The APK contains no ML-KEM
decapsulation path. This is reviewed library code, not a formal verification
claim. The test-time JVM fetch pins the artifact SHA-256; Android runtime does
not fetch dependencies or need network access to the library.

`HostKey`, `V2Protocol`, `Envelope`, and the bounded encrypted-response reader
are plain Java too, so a JVM runs the app's crypto and wire code; Kotlin keeps
JSON, HTTP and UI. Two gates:

```sh
make android-pq-check   # app classes vs Go vectors; Go decapsulates the Java ciphertext
make android-pq-live    # pair + encrypted /state against a disposable min-suite=2 daemon
```

The first checks bundle signatures, stored pin/suite replacement, epoch rollback,
Go/Java encapsulation interoperability, authenticated stream completion, and
frame/body limits. The second checks pre-pair code substitution refusal,
pairing, encrypted `/state`, downgrade, replay, and the authenticated end marker
against a disposable daemon. Neither gate is a phone run.

The v2 pin is `fingerprintV2 = SHA-256("df-e2ee-v2\0" + base64(P-256 SPKI) +
"\0" + suite)`. It covers the identity and suite but not the rotating
X25519/ML-KEM halves, so a bundle rotation under the same P-256 key keeps the
pin and is re-saved. A missing or rewritten bundle is refused, never downgraded.
Epochs may advance with the same identity and suite. Lower epochs and changed
bundles at the same epoch are rejected; explicit Forget is required before
changing an existing host pin or lowering its suite.

## Build

Prerequisites:

- JDK 21 with `javac`.
- Android SDK platform 34.
- Android SDK Build-Tools 35.0.0 (AGP 8.12 refuses 34.0.0).
- The checked-in Gradle 9.0.0 wrapper.

```sh
export JAVA_HOME="$HOME/.local/opt/jdk-21.0.13+9"   # or your JDK 21
export ANDROID_HOME="$HOME/Android/Sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
./android/gradlew -p android assembleDebug
```

Or from the repository root:

```sh
make android-build
```

`versionName` is the nearest git tag (without its `v`). `versionCode` is a
checked-in high-water mark in [`version.properties`](version.properties), not a
commit count, because Android refuses to install a lower code and this
repository's history is a curated export. Bump it before tagging a release.
Override either with `-PdfVersionName=… -PdfVersionCode=…`.

CI can build the same APK without a local toolchain: run the **android**
workflow (Actions → android → Run workflow) and download the
`darkfactory-android` artifact. With `ANDROID_KEYSTORE_BASE64` and
`ANDROID_KEYSTORE_PROPERTIES` set it builds the release-signed APK
(`CN=DarkFactory Android`); without them it falls back to a debug APK, which
will not install over a release-signed install.

The build copies `../web` into the APK assets at build time; there is no second
copy of `index.html` or `app.js` to drift.

Release builds are signed from `android/keystore.properties` (gitignored) and a
keystore outside the repository:

```sh
./android/gradlew -p android assembleRelease
# android/app/build/outputs/apk/release/app-release.apk
```

The `v0.1.0-rc.2` prerelease carries
`darkfactory_0.1.0-rc.2_android.apk` (`versionCode 2`, UI `2026-11-02.20`),
signed by `CN=DarkFactory Android`
(SHA-256 `A6:71:F0:BA:89:65:99:E0:AA:37:76:A1:F3:75:A3:97:B8:92:B3:7D:E4:B8:4C:52:35:92:34:E6:D3:C6:F8:33`).
Back up the keystore: losing it means future updates cannot install over this
one without uninstalling first.

## Install on a phone

No terminal or `adb` is needed:

1. Download the APK from the GitHub release on the phone.
2. Open it and allow **Install unknown apps** for Samsung Internet, Chrome, or
   My Files when Android asks.
3. Open DarkFactory and pair by scanning the desktop **Pair phone** QR or by
   entering the daemon's HTTPS URL and one-time code.
4. Compare the fingerprint the phone computes with the desktop value.

The daemon must be reachable from the phone over HTTPS (for example a Tailscale
HTTPS URL). Plain remote HTTP pairing is refused.

## Install and launch on the emulator

```sh
make android-run
# equivalent:
# adb install -r android/app/build/outputs/apk/debug/app-debug.apk
# adb shell am start -n dev.darkfactory.mobile/.MainActivity
```

For the emulator, pair against a loopback daemon through `adb reverse`:

```sh
adb reverse tcp:8792 tcp:8792
# Repeat with tcp:8793 tcp:8793 for a second isolated daemon.
# In the app: scan the Pair phone QR, or enter http://127.0.0.1:8792 and its code.
```

For a real phone, use each daemon's HTTPS Tailscale URL. Pairing over plain
remote HTTP is refused by the daemon and by `NativePair`; add as many hosts as
you need from the **Hosts** picker.

## Security notes

- Every host's token, client private key, host public key, device ID, and
  endpoint live inside one Keystore-backed AES-GCM registry blob, not
  `localStorage`, `sessionStorage`, a URL, a log, or a WebView cookie. A sealed
  pairing response has no `Set-Cookie`, so the raw token cannot reappear in a
  response header.
- The registry is keyed by the daemon's stable `hostId`. A replacement must
  preserve the P-256 identity and suite pin; v2 bundle rotation must advance
  its epoch. Replacing an unusable row, changing identity, or lowering the v2
  suite requires explicit Forget. Forgetting removes only the local token and
  pin, so revoke the device on the desktop as well.
- The page is served from the APK, so a Tailscale or proxy cannot replace
  `app.js`.
- App-layer E2EE uses P-256 ECDH between a per-pair client key and the daemon's
  persistent host key, HKDF-SHA256 directional keys, and AES-GCM frames. The
  host public key is pinned. The first pairing is trust-on-first-use until the
  user compares the fingerprint on the phone and desktop; the dialog and the
  desktop Pair phone dialog show the same value.
- A changed host key, suite, or bundle rollback is refused before the WebView
  loads; its saved row and pin remain in the host picker until explicit Forget.
  A change during a live request fails AEAD authentication. The app refuses an
  unsealed token.
- `android:usesCleartextTraffic="true"` is set because the local proxy is HTTP
  and the emulator uses loopback. `NativePair` separately refuses plaintext
  remote URLs; do not rely on the manifest alone. The encrypted envelope still
  protects the token and bodies, but a remote plaintext URL is not accepted.
- No app-layer E2EE claim is made for the local browser, and the daemon refuses
  its remote UI.

## Verified

On `emulator-5554` (`dfphone`, 1080×2340), against isolated daemons on
`127.0.0.1:8792` and `127.0.0.1:8793` with `adb reverse`:

- Pairing succeeded with `http://127.0.0.1:8792`, a one-time code, and a
  P-256 client key. The pair response carried `sealedToken`, never `token`; the
  daemon's Browser row contained the client `e2eePublicKey`.
- The native verify dialog showed the 64-hex-character fingerprint; the desktop
  **Pair phone** dialog showed the same value.
- The desktop QR encoded the custom-scheme pairing URI; a cold `am start`
  prefilled the native form, and the fingerprint dialog still gated connection.
- The old single-host state migrated into the registry. Pairing
  `http://127.0.0.1:8793` added a second entry with a different `hostId`, so the
  picker showed two hosts with separate scopes and credentials.
- Switching hosts destroyed the previous loopback origin and WebView; the page
  and top bar followed the selected host. A stopped active host showed
  `8792 is offline: The host is unreachable from this phone.` while `8793`
  remained connectable.
- Replacing 8792 with a fresh host key removed only 8792, kept 8793, and opened
  a pair form prefilled with `http://127.0.0.1:8792`.
- `/api/v1/e2ee` carried `/state`, session creation, PTY stream, resize, and
  input; no bearer token was on the wire. `pwd` returned
  `/tmp/user/1000/opencode/df-native/repo`.
- A native OpenCode chat created with **New chat** sent `Reply PINEAPPLE ONLY`
  and rendered the assistant reply through the encrypted path. The selected
  chat survived portrait/landscape rotation, background/foreground, a switch
  to a PTY and back, and a daemon restart without re-pairing. Short-height
  and prompt-focus CSS rules keep the composer and Send inside the viewport
  above the landscape keyboard; one tap sent `ONE TAP OK`.
- Switching Fleet/Session aborted a local stream without clearing the device;
  the encrypted stream reconnected and kept working.
- Force-stop and relaunch reached the last host without re-pairing; a down
  last host opened the picker instead of discarding the registry.
- Revoking one device from a host API caused a `401`; the app removed that host
  and kept the others, returning to pair only when none remained.
- A grep of `app_webview` found no `darkfactory-token`, `Authorization`,
  `Bearer `, or 64-hex bearer-shaped string.
- `shared_prefs/darkfactory-native.xml` contains only encrypted registry
  `state` ciphertext and its `iv`; no host id, endpoint, or token is plaintext.
- The isolated daemon returned `200` for local `/` and `/app.js`, and `403` for
  a remote Host, a forwarded header, and remote `/app.js`. `/healthz` and an
  authenticated API request with a forwarded header stayed available.

## Notifications

The phone does not use FCM or any vendor push service. `NotificationService` is
a `dataSync` foreground service that polls the paired daemon’s
`GET /api/v1/notifications` feed through the pinned E2EE envelope, keeps a
per-host cursor in `SharedPreferences`, and posts a local notification when the
app is not in the foreground. The feed covers a finished/lost session, a
blocked/waiting run, and a new OpenCode permission request.

- Android 13+ asks for `POST_NOTIFICATIONS` after the first connection. If the
  permission is denied, the service can still run but event notifications stay
  hidden; the foreground “Watching …” notification remains.
- The persistent notification has a **Stop** action. Opening DarkFactory again
  starts the service; forgetting the last host stops it.
- The service starts on the main thread, does network work on a single
  `ScheduledExecutorService` every 15 s, and never logs the token, endpoint
  query, or notification body. The lock-screen public version is generic.
- A daemon restart resets the event sequence. The service accepts the new
  `next`, so it may miss events that happened while the daemon was down; the
  ring is in memory and is not durable.
- Some vendors kill `dataSync` foreground services; disable battery
  optimisation for DarkFactory if notifications stop. Android 14/15 may limit
  background starts; the service is started from the activity, and `START_STICKY`
  is best-effort, not a guarantee.

Emulator evidence on 2026-11-02 (API 34, isolated daemon over `adb reverse`,
debug APK): pairing, the foreground service, a session exit, and the posted
notification (`Agent finished` / `phone notify test exited`) were observed with
the app backgrounded. The real-phone notification path is not yet part of a
recorded package-B run.



- One host is connected at a time. A parallel aggregated fleet that merges
  several hosts and keys ids as `(hostId, id)` is still a design target.
- Host labels are derived from the endpoint (`host:port`); there is no rename
  UI. The registry cap is 16 hosts.
- In-app QR scanner; QR uses the external camera app and the custom
  `darkfactory://pair` scheme. The URL/code form is the fallback.
- The phone computes the pairing fingerprint from the received host SPKI DER
  and refuses a key that does not match the supplied fingerprint (DP-19). The
  human comparison of that computed value with the desktop is still the trust
  step; there is no SAS/PAKE. The dialog makes the comparison explicit.
- A native OpenCode chat round trip was exercised: New chat, a prompt, an
  assistant reply, rotation, background/foreground, a session switch, and a
  daemon restart without re-pairing. Permission requests and interrupts were
  not exercised there. A real phone has paired over Tailscale HTTPS and the
  owner reported normal use, but the full MO-11 matrix (notification delivery,
  permission allow/reject, interrupt, revocation, Forget, host cap) is not
  recorded as a package-B run.
- A store listing and an automated update channel. Direct sideload distribution
  is decided; CI signing is configured, and the release-signed APK installs
  over the prerelease. Web Push for an installed PWA and the optional
  ntfy/Telegram webhook remain targets; Android local notifications are
  implemented.
- A native terminal widget; the existing xterm UI is bundled in the WebView.
