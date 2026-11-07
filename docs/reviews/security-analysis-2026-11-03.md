# Security analysis — 2026-11-03

Internal source review of the daemon, browser client, native pane and Android
envelope path at `edcfdee`. It is not an external audit and it adds no evidence
beyond what the cited lines show. It complements
[the E2EE protocol review](e2ee-protocol-review-2026-11-02.md) and
[the threat model](../threat-model.md); where they disagree, this document cites
the code.

## Verdict

No authentication bypass, command injection, path traversal, or script injection
was found. Every route in the table is either authenticated, code-gated, or
loopback-only; mutations pass through the gateway; untrusted strings reach the
DOM as text nodes; child processes are spawned with argv, never a shell.

The remaining risk is concentrated in four places:

1. **Availability and resource bounds** on the unauthenticated envelope route and
   the HTTP server itself.
2. **Deferred protocol findings** (H-2, H-3, H-5, H-7) whose worst case is an
   on-path attacker forcing a client to forget a host or a phone to allocate.
3. **Origin/CSRF precision**: the check ignores scheme and port, and GET routes
   still have side effects.
4. **Credential lifecycle**: durable credentials are created and survive logout
   or revocation longer than the UI implies.

## Priority

| # | Sev | Finding | Evidence |
|---|---|---|---|
| 1 | High | `POST /api/v1/e2ee` has no rate limit or concurrency bound | `internal/httpapi/e2ee.go:313-433` |
| 2 | High | HTTP server has no read/write timeout or header cap | `cmd/darkfactory/main.go:911-916` |
| 3 | High | Origin check ignores scheme/port; GET side effects are CSRF-reachable | `internal/httpapi/server.go:647-653,1045-1047,2177,2191` |
| 4 | High | H-3: a forged outer 401 or garbage frame makes the phone forget the host | `android/.../LoopbackServer.kt:341-357,406-411`; `MainActivity.kt:761-770` |
| 5 | Medium | H-2/H-5/H-7: no authenticated terminator, no frame ceiling, no clock offset | `e2ee.go:493-504,535-555`; `NotificationService.kt:188-205` |
| 6 | Medium | Pasted master token silently provisions a 180-day device key | `web/app.js:1850-1868,1317`; `web/index.html:252` |
| 7 | Medium | `/pair` trusts `X-Forwarded-Proto`; legacy reply carries the raw token | `server.go:1359-1372,1559-1561,1463-1465` |
| 8 | Medium | Two loopback helpers; the weaker one is used for `/open`, `/fs/pick`, `/worktrees/{id}/reveal` | `picker.go:23-37`; `localui.go:30-49`; `server.go:1248,2799` |
| 9 | Medium | Pane capabilities survive logout and device revocation | `native_pane.go:105-109`; `server.go:1563-1591,1984-2088` |
| 10 | Medium | Read scope reaches the full filesystem, ledger, device list and every chat | `server.go:469,479,490,2284`; `opencode.go:161-227` |
| 11 | Medium | Pair limiter order and keying; DP-1 "5 attempts" is 5/min/source | `server.go:1485-1509` |
| 12 | Low | Device-existence oracle (401 vs 400) | `e2ee.go:355-374` |
| 13 | Low | Browser escape policy lives only in the client, for 8 OSC verbs | `web/app.js:4748-4809` |
| 14 | Low | `handleClientDebug` logs arbitrary client text; request log keeps query strings | `server.go:3400-3422`; `main.go:989-1005` |
| 15 | Low | `authFrom` defaults an empty scope to full | `server.go:723-729` |
| 16 | Low | No `SECURITY.md` / private reporting channel | repo root |

## 1. Envelope route has no throttle (H-1 still open)

`handleE2EE` (`e2ee.go:313-433`) caps the body at 4 MiB and pre-checks
`Content-Length` (`:326-338`), but nothing throttles the route. The pairing
limiter exists only on `/pair` (`server.go:1485-1509`); `allowDeviceAuth` guards
challenge/verify only. Per unauthenticated request the daemon does: a JSON
unmarshal, a linear scan of browser rows under `openMu` (`e2ee.go:283-292`), and
— if the `deviceId` is known, which is sent in plaintext — an ECDH, HKDF and
AES-GCM open.

Fix: reuse a token bucket keyed by source on `/api/v1/e2ee`, and bound concurrent
envelope work with a small semaphore so a burst cannot occupy every handler
goroutine. The 401/400 split (finding 12) makes the deviceId probe cheap today,
so the throttle matters more than the body cap.

## 2. Server timeouts

`cmd/darkfactory/main.go:911-916` sets `ReadHeaderTimeout: 10s` and
`IdleTimeout: 2m` only. A slow body on any route holds a goroutine and a
connection indefinitely; `MaxHeaderBytes` is left at the default rather than
stated. Loopback exposure is mild, but `--allow-remote` and the Tailscale path
are supported, and threat-model item 2 asks for exactly this bound.

Fix: `ReadTimeout`, `WriteTimeout` (careful: streams are long-lived — use
`ResponseController.SetWriteDeadline` per stream instead), an explicit
`MaxHeaderBytes`, and a connection cap.

## 3. Origin check and GET side effects

`sameHost` compares `hostOnly` only (`server.go:1045-1047`), so an origin on the
same host and a different port passes. The cookie is `SameSite=Strict`, but
"site" ignores port, so any page served on `127.0.0.1:<other>` is same-site and
its cookie is sent. There is no `Sec-Fetch-Site` check anywhere in the tree.

That is only dangerous because GET routes mutate:

- `GET /api/v1/state` writes session rows (`server.go:2177,2191`).
- `GET /api/v1/sessions/{id}/stream` detaches the previous subscriber
  (`internal/session/session.go:559-587`). A cross-origin `fetch(..., {mode:
  'no-cors'})` cannot read the response but can still kick the owner's terminal
  off its stream. `isMutation` treats GET as non-mutating
  (`server.go:1089-1096`), so `X-DF-Request` is not required.

Fix: compare full origin (scheme, host, port); reject `Sec-Fetch-Site:
cross-site`; make the stream attach a POST or require `X-DF-Request` on it; move
the `/state` status sync out of the read path (background reconcile, or a
dedicated audited mutation).

## 4. Forged outer errors (H-3)

Refusals are plaintext JSON and the outer HTTP status is not authenticated
(`refusal.go:236-250`). `LoopbackServer` maps an outer 401, an `AEADBadTagException`,
a `JSONException`, and a generic post-head exception all to `onLogout`
(`LoopbackServer.kt:341-357,406-411,449-450`), which calls `forgetHost`
(`MainActivity.kt:761-770`) and deletes the token and pinned key
(`TokenStore.kt:107-122`).

Consequence: anyone on path can force the phone to drop a host, repeatedly, with
a bare `401`. That is availability, not key recovery, but it is a cheap attack
and it also destroys the pinned state the user would need to notice a real
substitution. Fix: only a *successfully authenticated* refusal may clear local
state; treat every unauthenticated failure as "retry / show offline".

## 5. Terminator, frame ceiling, clock (H-2, H-5, H-7)

- No end frame: `finish()` only flushes (`e2ee.go:493-504`), and the client
  treats clean EOF at a frame boundary as a complete body
  (`LoopbackServer.kt:368-400`). Truncation is indistinguishable from success.
- No server-side frame ceiling: every `Write` becomes one frame
  (`e2ee.go:535-555`). `NotificationService` accepts any positive 4-byte length
  and allocates it (`NotificationService.kt:188-205`); `LoopbackServer` caps at
  20 MiB, the notification path has no cap. `readTimeout = 0` on streams
  (`LoopbackServer.kt:326-327`) means a hostile host can also hang the reader.
- No clock offset discovery: the client sends `System.currentTimeMillis()`
  (`E2EE.kt:51`) against a ±5 min window (`e2ee.go:34,348-353`). A phone with a
  skewed clock fails every request with no way to correct.

Fix: an authenticated final frame (and a client that requires it), a negotiated
frame limit enforced on both sides, read deadlines on every client frame, and a
`serverTime` in the pair response plus a stored offset.

## 6. A pasted token becomes a durable credential

`connect(token)` calls `ensureDeviceKey()` whenever a token was supplied
(`web/app.js:1850-1868`), which registers an ES256 key with a 180-day TTL
(`server.go:56,1639`) and stores the non-extractable private half in IndexedDB
(`app.js:1317`). From then on `deviceKeyAuth` mints 12-hour session bearers
without the master token (`app.js:1333-1370`).

The connect card says "A pasted token stays in this tab only"
(`web/index.html:252`). The string does; the authority does not. A user who
pastes a token once on a shared browser profile has created a full-scope
credential that outlives the tab, and nothing in the UI says so. Revocation
exists (Devices panel, `server.go:2039-2088`) but the user has no prompt telling
them to use it.

Fix: ask before provisioning ("Remember this device for 180 days?"), show the
new device in the Devices list immediately, and make Disconnect offer to revoke
it. Alternatively, do not auto-register from a pasted master token — only from
an `/open` or `/pair` login.

## 7. Pairing over a plaintext proxy

`isSecureRequest` returns true for `X-Forwarded-Proto: https`
(`server.go:1559-1561`), and `handlePair` accepts a loopback peer with that
header (`server.go:1359-1372`). A reverse proxy that forwards client headers can
therefore let a plaintext client through. The legacy (unsealed) reply then
returns the raw token in JSON (`server.go:1463-1465`); Android refuses unsealed
tokens, but another client would not.

Fix: do not trust forwarding headers unless the peer is an explicitly configured
trusted proxy; require TLS on the listener for any non-loopback client; delete
the unsealed-token reply and require `clientPublicKey`.

## 8. Two loopback helpers with different strictness

`isLocalUIRequest` rejects `Forwarded`, `X-Forwarded-For`, `X-Forwarded-Host`,
`X-Forwarded-Proto` and `X-Real-IP` (`localui.go:30-49`). `isLocalRequest`
checks only `RemoteAddr` and `Host` (`picker.go:23-37`), and `handleOpen` checks
only `isLoopbackRequest` plus the host allowlist (`server.go:1248`).

Used by `POST /api/v1/fs/pick` (`picker.go:53`), `POST /worktrees/{id}/reveal`
(`server.go:2799`) and `POST /api/v1/open` (`server.go:1248`). A loopback proxy
— Tailscale Serve is the documented deployment and connects from loopback — that
rewrites `Host` can make these look local. `reveal` opens a file manager and
`pick` opens a dialog on the operator's desktop; `open` mints a full cookie.

Fix: one helper. Use `isLocalUIRequest` for all five call sites, or make the
proxy-header rejection explicit in each.

## 9. Pane capabilities outlive revocation

`dropPane` runs only when the pane's stream closes (`server.go:3338`) or the TTL
expires (`native_pane.go:82-84,96-100`; 12 h once opened). Logout
(`server.go:1563-1591`), device revoke (`:2039-2088`) and revoke-all
(`:1984-2037`) never touch `s.panes`. A pane minted before revocation keeps
typing into its session until the window closes.

Fix: clear panes by principal on logout/revoke, and by session on
`session.delete`.

## 10. Read scope is wider than the requirement text

DP-3/DP-4 describe `read` as "view state, runs and chat transcripts" and say it
"cannot type or mutate". The mutation and terminal gates do hold — a read-scope
cookie cannot mint a pane or attach a stream, because those are POSTs blocked at
`server.go:674-677`. But read scope can also:

- enumerate any directory on the host (`GET /api/v1/fs/dirs`,
  `server.go:2284-2330`);
- read the whole decision ledger (`GET /api/v1/audit`, `server.go:469`);
- list every device (`GET /api/v1/devices`, `server.go:479`);
- read every OpenCode chat and message, not only one worktree's
  (`opencode.go:161-227`; the worktree filter is client-side only).

That is a product decision, not a bug, but it should be decided explicitly:
`/fs/dirs` to repo roots, `/audit` and `/devices` to full scope, or a written
statement that read scope is fleet-wide.

## 11. Pairing limiter

`allowPair` consumes the global budget before checking the per-source window
(`server.go:1493-1500`), so one source can exhaust the 30/min global budget after
being denied itself. The per-source key is `RemoteAddr` (`:1487-1490`), which
collapses to the proxy address behind a reverse proxy. And DP-1's "5 attempts" is
implemented as 5/min/source, not a per-code counter; a code stays valid for its
full 120 s (`docs/requirements.md:559,609-611`).

Fix: source first, then global; key on the forwarded client address only from a
trusted proxy; count failures per code and burn the code after N.

## Lower-severity items

- **Device oracle** (`e2ee.go:355-374`): unknown device is 401, known device with
  bad ciphertext is 400. Return one uniform failure.
- **Browser escape policy** (`web/app.js:4748-4809`): only OSC 4/10/11/12 sets
  and 104/110/111/112 resets are dropped, only for the 7-bit introducer. OSC 52
  is inert solely because the vendored xterm.js has no handler; an upgrade
  silently changes that. The native path has a real allowlist
  (`internal/nativeattach/filter.go:116-164`) — the browser path should share it,
  plus a test that fails if xterm gains OSC 52. Reply traffic (DA, DECRQSS,
  DECRQM, colour, mouse) still shares the keystroke path (SS-13 open).
- **Log surfaces**: `handleClientDebug` logs arbitrary client lines
  (`server.go:3400-3422`); `debugRequests` logs full request URIs including query
  strings (`main.go:989-1005`). Sanitize control characters and drop query
  strings.
- **Fail-open default**: `authFrom` treats an empty scope as full
  (`server.go:723-729`). Default to read.
- **No reporting channel**: no `SECURITY.md`, and
  `docs/publication-checklist.md:22` still has "provide a private
  vulnerability-reporting channel" unchecked.
- **Jail limit**: `bwrap` hides the config and state directories
  (`session.go:763-770`) but `--ro-bind / /` leaves the rest of the home
  readable — SSH keys, browser profiles, other repos — and network is allowed.
  That is documented ("same-uid, permits network") and should stay documented.
- **Unbounded collections**: `deviceSessions`/`deviceChallenges` prune only above
  1024 (`server.go:1714-1720,1828-1834`); `panes` has a hard cap. Add caps.
- **E2EE replay janitor**: expired rows are removed only when another envelope
  arrives (`store/e2ee_replay.go:41-55`). Correctness is fine; disk retention is
  not bounded.
- **No HSTS / no TLS listener**: expected behind a proxy, but then the proxy must
  be the trust boundary and finding 7 applies.
- **Vendor listing**: `http.FileServer` on the embed FS lists `/vendor/` names
  (`server.go:537`).

## What is solid and should not regress

- Centralized `auth` → `mutate` → handler order; every `auth=true` route has a
  typed-401 test (`refusal_test.go:184-211`); every mutation has a unique gateway
  verb (`gateway_test.go:111-148`); a recorder failure prevents the effect
  (`gateway.go:199-232`).
- `__Host-` cookie, `HttpOnly`, `Secure`, `SameSite=Strict`, no `Domain`
  (`server.go:1167-1177`); browser rows stored as
  `SHA-256(instanceSecret ‖ token)` (`server.go:1139-1146`), so a stolen
  `state.db` cannot mint cookies.
- One-time, hashed, short-lived, unlogged open/pair codes; host-chosen scope and
  label (`server.go:1223-1349,1469-1509`).
- Constant-time master-token comparison (`server.go:1179-1184`).
- E2EE: P-256 ECDH, HKDF-SHA256, AES-256-GCM, per-frame positional AAD, replay
  recorded durably before dispatch, storage failure fails closed, envelope/device
  binding enforced by requiring the inner token to hash to the same row
  (`e2ee.go:375-392`), nested envelopes refused (`:318-321,398-410`).
- Client rendering is text-node only; no `innerHTML`/`eval`; link schemes
  allowlisted; no CORS, no CDN, no telemetry; CSP is a real header with
  `frame-ancestors 'none'` and `base-uri 'none'`.
- One-time codes travel in the URL fragment and are stripped before any network
  call (`web/app.js:2010-2028`).
- Pane capability: hashed at rest, loopback-mint-only, single stream, four
  routes, one session, dies with its stream (`native_pane.go`, `server.go:659-669`).
- Git and process execution use argv with validated branch names and `--`
  separators (`gitwt.go:24-32,45,60-64`); `notify-send` uses `--` before
  user-controlled text (`notifications.go:847`); the picker uses `--filename=`
  (`picker.go:128`).
- Secret files are `0600` written atomically (`config.go:389-415`); `state.db` is
  `0600` (`store.go:60`).
- `govulncheck` runs in CI (`.github/workflows/vuln.yml`), and the daemon is
  CGO-free with a vendored tree.

## Changes landed in this tree (2026-11-03)

Implemented and tested here, not built into `bin/` and not deployed:

| Finding | What changed |
|---|---|
| 1 | `/api/v1/e2ee` has a per-source (300/min) and global (900/min) budget checked before the body is read, with the new `e2ee_rate_limited` 429 refusal (`allowE2EE`). |
| 2 | Listener deadlines are configurable in `config.json` and as `serve` flags: `readHeaderTimeout` 10s, `readTimeout` 60s, `writeTimeout` 60s, `idleTimeout` 2m, `maxHeaderBytes` 1 MiB. `"never"`/`"0"` disables one. Streams clear their own deadlines. |
| 3 | `sameOrigin` compares the port when both sides name one, and `Sec-Fetch-Site` must be `same-origin`/`none` when present. GET side effects are no longer reachable from another loopback port. |
| 8 | `isLocalRequest` delegates to `isLocalUIRequest`, so `/open`, `/fs/pick` and `/worktrees/{id}/reveal` reject `Forwarded`/`X-Forwarded-*`/`X-Real-IP` like the local UI does. |
| 9 | Logout, revoke-all and device delete drop the pane capabilities they authorized; session delete drops the session's panes. |
| 11 (partial) | Pair and device limiters spend the global budget only after the per-source check. Per-code attempt counting and trusted-proxy keying are still open. |
| 14 | `handleClientDebug` strips control characters; `debugRequests` logs `r.URL.Path`, not the query string. |
| 15 | `authFrom` defaults an empty scope to read, not full. |
| — | CSP gained `object-src 'none'` and `frame-src 'none'`; a `Permissions-Policy` header is set on every response. |

Still open: 4 (client-side H-3), 5 (H-2/H-5/H-7), 6 (pasted-token consent), 7
(trusted proxy / unsealed pairing reply), 10 (read-scope surface), 12 (device
oracle), 13 (browser escape allowlist), 16 (reporting channel).

## Suggested order of work

1. Throttle `/api/v1/e2ee` and add server timeouts (findings 1-2). Both are small
   and close the only "unauthenticated attacker" path in the threat model.
2. Fix the origin comparison and remove the GET side effects (finding 3).
3. Land the client-side H-3 fix (finding 4): never clear local state on an
   unauthenticated failure.
4. One loopback helper, and pane revocation on logout/revoke (findings 8-9).
5. Decide the pasted-token policy and the read-scope surface (findings 6, 10).
6. Schedule H-2/H-5/H-7 as one wire change with a version bump (finding 5).
