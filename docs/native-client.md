# Native unified client

Status: **Android client built, pinned, multi-host registry, and
emulator-verified.**
The Wails desktop still loads the daemon UI, and the daemon refuses its browser
UI to remote peers. The Android client bundles `web/` and talks to the API
through a per-launch random `*.localhost` origin bound to native loopback. Its
native proxy uses app-layer P-256 ECDH + HKDF + AES-GCM and pins each daemon
host key; a host-key change removes only that host and returns to pairing. A
native picker switches between saved hosts, one at a time. The parallel
aggregated fleet is deferred and is not part of this release. Real-phone
Tailscale HTTPS acceptance and the protocol review remain open; the recorded
evidence is emulator work over `adb reverse` plus focused JVM and Go tests.
See [`../android/README.md`](../android/README.md) and the
evidence.

## Decisions

- **Remote browser UI is disabled.** The daemon serves `/`, `index.html`, and
  static assets only to a loopback peer with a loopback Host and no forwarding
  headers. Remote access is the native client API. This closes the
  daemon-served-JavaScript trust hole instead of calling browser crypto E2EE.
- **Transport encryption:** TLS 1.3 or Tailscale WireGuard between the native
  client and daemon, **plus app-layer E2EE for the native client**. The app
  pins the daemon P-256 host key, derives directional AES-GCM keys with
  ECDH/HKDF, and sends no bearer token or request/response body in cleartext.
  The local browser remains a loopback-only surface and is not an E2EE endpoint.
- **Access model:** one `workspace` capability covers PTY bytes/input and
  OpenCode prompts/commands/permissions. OpenCode can run workspace actions, so
  a separate terminal opt-in is not a security boundary. `read` is the
  read-only scope.
- **Multi-host:** a native client owns a host registry. Each host has its own
  endpoint, device token and scope. IDs are host-scoped because only one host is
  connected at a time; a merged `(hostId, id)` fleet is deferred.
- **Exposure:** host stays loopback by default; remote access is Tailscale
  Serve or an SSH tunnel. `serve --idle-exit` closes the listener after a quiet
  period, while the detached session host keeps PTYs alive.

## Why a native client

The browser cannot unify hosts without weakening the origin model:

- `GET /api/v1/state` uses relative same-origin URLs and the daemon has no
  CORS. Fetching another desktop's origin from one page would require CORS and
  would mix cookies for unrelated hosts.
- The daemon serves the UI. A reverse proxy or relay can alter that UI and its
  keys; moving the trust anchor into a separately distributed client is the
  only way to make that property meaningful.

The native client can call each daemon directly, keep each host's credentials
separate, and merge the results without a relay or a shared browser origin.

## Pairing and per-device credentials

`POST /api/v1/pair` sets the normal `__Host-df_session` cookie for the local
browser. A native client adds a P-256 `clientPublicKey`; the response contains
`hostId`, `deviceId`, the daemon `hostPublicKey`/`fingerprint`, and a
`sealedToken` instead of a plaintext token. The Android client computes the
fingerprint from the received `hostPublicKey` DER and refuses a response whose
`fingerprint` does not describe that key (DP-19), so the value it shows is not
an echoed field. The server stores only the hashed
row key and the client public key in the `Browser` row. The native client
derives one AES-GCM key in each direction and sends:

```http
POST /api/v1/e2ee
Content-Type: application/json
{ "v":1, "deviceId":"…", "id":"…", "ts":…, "nonce":"…", "ciphertext":"…" }
```

The ciphertext contains `{method, path, headers, body, token}`. The daemon
dispatches the inner request through the normal handler, so scope, workspace,
expiry, redraw rules, gateway audit and revocation are unchanged. The native
client must:

1. Put the sealed token and client private key in the OS keychain/keystore,
   never `localStorage` or a URL.
2. Keep one token and one host public key per host.
3. Delete both on revoke or logout; a revoked token returns `401`.
4. Compare the displayed host fingerprint with the desktop **Pair phone**
   dialog before accepting the first pairing.
5. Refuse an unsealed token or a changed host key. There is no plaintext-token
   fallback in the Android client.

The daemon records each authenticated encrypted request ID durably before
dispatch and keeps it until the envelope timestamp plus the 5-minute acceptance
window (DP-20); a storage failure refuses the request, and a replay is refused
after a daemon restart as well as in-process. Requests accepted before that
record existed cannot be rejected retroactively, and the wire format is
unchanged.

## Verification status

Verified in the tree and its artifacts (2026-11-02):

- Emulator (`dfphone`, Android 14, `adb reverse`): two-host Keystore registry,
  one-at-a-time switch, encrypted PTY stream and input, OpenCode chat create and
  prompt/reply, rotation and landscape keyboard send, background/foreground,
  daemon restart without re-pairing, revocation to pair, a changed host key
  removing only that host, and a fingerprint compared with the desktop dialog.
  APK hashes and commands are in the
  evidence.
- Focused tests reported in `HANDOFF.md`: the `e2eeReplay` store
  restart/expiry test, the storage-failure test, `TestE2EEHostKeySurvivesRestart`,
  and `sh test/android/check-fingerprint.sh` (DP-19). The httptest
  server-restart case (`TestE2EEReplayRefusedAcrossDaemonRestart`) is present in
  the tree but has no recorded pass.
- Linux Wails: six native checks in
  c3-wails; it is
  an evaluation shell, not a packaged desktop release.

Open, and not to be claimed from the above:

- A real phone over Tailscale HTTPS. The evidence used the emulator and
  `adb reverse`; an emulator does not substitute for the phone.
- Native permission allow/reject and interrupt, plus Forget and the 16-host cap,
  which the emulator run did not exercise.
- `make check` and `make ui-gate` at this revision, and an external protocol
  review. Both gates pass locally; the internal review's confirmed defects are
  fixed, and its remaining hardening gaps are accepted or deferred in the
  review's resolution table. See
  `docs/reviews/e2ee-protocol-review-2026-11-02.md`.
- Android store distribution and any packaged desktop release. A direct
  release-signed APK is attached to the `v0.1.0-rc.1` prerelease; there is no
  Play/F-Droid listing, update channel, or CI signing yet.

One-time codes/QRs remain host-initiated and single-use. The desktop QR encodes
`darkfactory://pair?base=<daemon-root>&code=<code>`; the Android app registers
that scheme and prefills the pair form, so the camera app can start pairing
without an in-app scanner or camera permission. A custom scheme is unverified
and can be claimed by another app, so the fingerprint confirmation remains the
trust check; the URL/code form is the fallback. A client may pair several
desktops and keep them in one registry.

## Host registry

The Android client stores one encrypted registry in Android Keystore-backed
storage. The decrypted shape is:

```json
{
  "v": 2,
  "activeHostId": "stable-host-id",
  "hosts": [
    {
      "hostId": "stable-host-id",
      "base": "https://desktop.tailnet.ts.net",
      "label": "desktop.tailnet.ts.net",
      "scope": "full",
      "token": "sealed per-device token",
      "deviceId": "device-id",
      "hostPublicKey": "P-256 SPKI",
      "clientPrivateKey": "P-256 private key",
      "clientPublicKey": "P-256 SPKI",
      "fingerprint": "host fingerprint",
      "lastSeenAt": 1762000000000
    }
  ]
}
```

`/state` and the pairing response carry a stable `hostId`; the display name is
derived from the endpoint. Re-pairing the same `hostId` replaces its entry. The
registry is capped at 16 hosts.

The current client connects one host at a time. Selecting a host destroys the
previous loopback origin and WebView and generates a new random local origin, so
session, worktree, and chat ids cannot leak across hosts. A down host is shown
as offline in the picker and is not discarded; a changed host key removes only
that entry. Forgetting a host removes the local token and pin but does not
revoke the device on the desktop.

A parallel aggregated fleet that loads every reachable host and keys ids as
`(hostId, id)` remains a design target. It must not merge ids from different
hosts or render a down host as an empty fleet.

## Client shape

The Android client is the first native target, because the phone is the client
that cannot fall back to a trusted local browser:

- Bundle `web/` in the APK. `MainActivity` owns the WebView and navigation
  policy; `LoopbackServer` serves the bundled assets at a per-launch random
  loopback origin.
- The native proxy owns the per-host token, client private key, and pinned host
  key in Android Keystore-backed storage. It seals every upstream request and
  opens every response frame; the page receives no token, no cookie, and no
  JavaScript bridge.
- A `NotificationService` foreground service polls the daemon’s attention feed
  through the same envelope and posts local notifications. It uses no FCM or
  vendor push service; the feed is in memory and the cursor is per host.
- Remote browser access to the daemon UI is refused; local browser use remains.
- A later Wails desktop client can reuse the loopback-origin and host-registry
  contract. Until it does, the Wails shell still loads the daemon UI and is a
  local evaluation shell, not a remote client.

The current Android app stores several hosts, switches one at a time, and tears
down the previous origin/WebView on a switch. A merged `(hostId, id)` fleet view
is still to do. QR pairing uses the phone camera and the `darkfactory://pair`
scheme; an in-app scanner is not implemented.

## On-demand exposure

`darkfactory serve --idle-exit 30m` and
`darkfactory launch --idle-exit 30m` stop the HTTP listener after no request has
been in flight for the window. The session host and PTYs survive, so the next
start reattaches them. The native client treats a closed listener as offline and
offers a start action (`tailscale serve`/SSH/`darkfactory launch`), rather than
keeping a public port open permanently.

## Phases

1. **Server primitives:** workspace capability, per-device bearer token, idle
   exit. Done in this tree.
2. **Remote UI boundary:** the daemon serves HTML/JS only on loopback. Done and
   tested; remote `/api/v1/...` access remains for native clients.
3. **Android client:** native pair, Keystore token, bundled UI, loopback proxy,
   streaming, logout/revocation return to pair. A release-signed APK is attached
   to the `v0.1.0-rc.1` prerelease; it was emulator-tested with a debug build,
   including the UI-24 chat half (create, prompt/reply, rotation and landscape
   keyboard/send, background/foreground, session switch, daemon restart).
4. **Pinned identity and app-layer E2EE:** P-256 host key, sealed pairing token,
   AES-GCM envelope, framed encrypted responses, durable replay records,
   tamper refusal, and a host-key-change return to pair. Implemented and tested
   in this tree, not security-reviewed; the first pairing still requires the
   user to compare the fingerprint on both screens, and DP-19 now makes the
   shown value the one computed from the received key.
5. **Native host registry:** multiple hosts, a picker, a Keystore-backed
   per-host token/key registry, offline-host handling, and host-scoped switching.
   Done and emulator-tested for two hosts; reuse the contract in Wails.
6. **Aggregated fleet:** load reachable hosts together, merge state with
   `(hostId, id)` keys, and keep a down host visible as offline. This is not
   implemented; the current client switches one host at a time.
