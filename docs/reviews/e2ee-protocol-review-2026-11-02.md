# Native E2EE envelope and Android pairing — protocol review (2026-11-02)

Status: **internal review by a single reviewing agent**. This is not an external
security review, not a penetration test, and not evidence that the release gates
in `HANDOFF.md` pass. It is a source review of one change set.

## Scope

Read in full:

- `internal/httpapi/e2ee.go`, `internal/httpapi/e2ee_test.go`
- `internal/store/e2ee_replay.go`, `internal/store/e2ee_replay_test.go`, the
  E2EE/replay parts of `internal/store/store.go`
- `android/app/src/main/kotlin/dev/darkfactory/mobile/E2EE.kt`, `NativePair.kt`,
  `LoopbackServer.kt`, `TokenStore.kt`
- `android/app/src/main/java/dev/darkfactory/mobile/HostFingerprint.java`,
  `test/android/HostFingerprintTest.java`

Read for context: `HANDOFF.md`, `docs/agent-reference.md` (native E2EE
envelope, host pin, pairing), `docs/requirements.md` DP-17..DP-20,
`docs/substrate-lessons.md` ("Native client E2EE envelope"), plus the adjacent
code an envelope request necessarily passes through: `internal/httpapi/server.go`
(`routeTable`, `Handler`, `auth`, `authenticate`, `hostAllowed`, `handlePair`,
`mintBrowserSessionWithKey`), `internal/httpapi/refusal.go`,
`internal/httpapi/gateway.go` (`mutate`), `internal/httpapi/localui.go`,
`cmd/darkfactory/main.go` (`serve` http.Server), `android/.../MainActivity.kt`
(connect, verify, forget paths).

Not run: the Go unit suite, `make check`, `make ui-gate`, Gradle, the emulator,
or any live daemon. One scratch program outside the repository was executed to
confirm Go `net/http` `ServeMux` matching for percent-encoded paths (finding
C-1); no repository file was created or modified except this review.

## Threat model

Assets

- the per-device bearer token (full or read scope) and the access it grants
  (workspace, PTYs, OpenCode);
- PTY bytes, chat text, prompts, permission decisions and file listings;
- the host private key (impersonation of the daemon to already-paired phones);
- the client private key (decryption of that phone's past and future traffic);
- the user's decision that a given fingerprint belongs to their daemon.

Adversaries considered

1. **On-path transport attacker** (malicious tailnet peer, TLS terminator,
   compromised proxy). Can read, rewrite, reorder, inject, delay and truncate.
   Cannot compute ECDH with the host or client private key.
2. **Unauthenticated remote host** that can reach the listening port.
3. **Paired but low-scope device** (read scope) trying to exceed its grant, or a
   paired device trying to exhaust the daemon.
4. **Replay attacker** holding a captured envelope.
5. **Local attacker with filesystem access** to `state.db` or to the Android
   prefs/Keystore (only relevant to residual risk).

Explicit non-goals (as documented): a compromised desktop, a rooted phone, a
malicious daemon, and the local browser over loopback. First pairing is
trust-on-first-use; the fingerprint comparison, not the transport, detects a
substituted host key (`substrate-lessons.md` §Native client E2EE envelope).

## Findings

| ID | Severity | Class | Location | Finding |
|---|---|---|---|---|
| C-1 | Medium | Confirmed defect | `internal/httpapi/e2ee.go:375-390,412` | A percent-encoded inner path re-enters `handleE2EE`; the "no nested envelope" guard inspects the raw string, not the decoded path, so nesting depth is bounded only by the 16 MiB body |
| C-2 | Medium | Confirmed defect (resource) | `internal/store/e2ee_replay.go:19-31` | Every envelope request scans the entire replay bucket and deletes without a bound, inside bolt's single write transaction |
| H-1 | Medium | Hardening gap | `internal/httpapi/e2ee.go:312-320` | 16 MiB of unauthenticated body is buffered and parsed per request on a route with no rate or concurrency limit; the largest inner body any handler reads is 1 MiB |
| H-2 | Low | Hardening gap | `internal/httpapi/e2ee.go:473-484`; `LoopbackServer.kt:355-389` | The response has no authenticated terminator, so a truncated response is indistinguishable from a complete one |
| H-3 | Low | Hardening gap | `LoopbackServer.kt:334-344,395-440`; `MainActivity.kt:425-429,727-735` | A forged outer `401`/garbage frame makes the app delete the pinned host, so an on-path attacker can force re-pairing |
| H-4 | Low | Hardening gap | `MainActivity.kt:692`; `LoopbackServer.kt:203-207` | The "must have E2EE" gate exists only at startup; a registry row without E2EE is still connectable from the picker and would send a plaintext bearer |
| H-5 | Low | Hardening gap | `LoopbackServer.kt:683`; `internal/httpapi/e2ee.go:515-535` | No negotiated maximum frame size; the client aborts above 20 MiB while the daemon will seal any single `Write` as one frame |
| H-6 | Low | Hardening gap | `server.go:1378-1415`; `TokenStore.kt:61-81` | Re-pairing mints a new device row and never revokes the previous one |
| H-7 | Low | Hardening gap | `E2EE.kt:51`; `internal/httpapi/e2ee.go:330-335` | The phone's own clock gates every request; there is no offset discovery and no pairing-time skew check |
| H-8 | Low | Hygiene | `LoopbackServer.kt:357,379-380` | Frame lower bound is `13` instead of `28`, and a frame-1 error branch is empty |
| H-9 | Informational | Hardening gap | `NativePair.kt:112-153` | `verifyHost` compares the key but not `hostId` |

---

## Confirmed defects

### C-1 — Percent-encoded inner path re-enters the envelope handler

`internal/httpapi/e2ee.go:375-379`

```go
if request.Path == "" || !strings.HasPrefix(request.Path, "/api/v1/") ||
    request.Path == "/api/v1/e2ee" || strings.HasPrefix(request.Path, "/api/v1/e2ee/") {
```

The guard is deliberately an anti-recursion guard: the inner request is
dispatched through `s.Handler()` (`e2ee.go:412`), so a path that reaches
`/api/v1/e2ee` again would nest. The guard tests `request.Path`, the **raw**
string from the decrypted JSON. `http.NewRequestWithContext` at
`e2ee.go:390` parses that string with `url.Parse`, which percent-decodes:

```go
inner, _ := http.NewRequest("POST", "/api/v1/%652ee", nil)
inner.URL.Path == "/api/v1/e2ee"   // RawPath == "/api/v1/%652ee"
```

Go's `ServeMux` matches on `r.URL.Path`, so the encoded form reaches the same
handler. Confirmed by running a scratch `ServeMux` outside the repository:
`/api/v1/%652ee` (and `/api/v1/%65%32%65%65`) dispatch `POST /api/v1/e2ee`,
while the pre-1.22 "clean path redirect" no longer rewrites the request.

Failure scenario, with one paired device of **either** scope (the envelope route
is `auth: false` at `server.go:455`, and the nesting happens before the inner
auth runs): the device seals a normal request whose inner path is
`/api/v1/%652ee` and whose inner body is a second valid envelope for the same
device with a fresh `id`/`ts`. Level 2 decrypts, passes the token check,
commits another replay record, and dispatches level 3 the same way. Each level
costs an ECDH+HKDF, an AES-GCM open, a fresh `s.Handler()` mux build (mux +
`routeTable()` + a closure chain per route), and a bolt write transaction that
sweeps the replay bucket — i.e. roughly 250–300 bytes of attacker payload per
level, up to tens of thousands of levels inside the 16 MiB envelope. That is a
large CPU, allocation, memory and fsync amplification from a single
authenticated request, and it holds the store's single writer repeatedly.

Fix

- Normalize before the guard and dispatch the normalized target:
  `u, err := url.ParseRequestURI(request.Path)`; `target := u.Path` (plus
  `"?"+u.RawQuery`); run the prefix/`e2ee` checks on `target`, and pass `target`
  to `NewRequestWithContext`. Optionally refuse any request where
  `u.RawPath != ""` — no API path needs escaping.
- Defence in depth: put a marker in the inner request context
  (`context.WithValue(ctx, e2eeInnerKey{}, true)`) and refuse to enter
  `handleE2EE` when it is already set, so a future route-table change cannot
  reintroduce the recursion through a different spelling.

Test that proves it: `TestE2EERefusesEncodedNestedEnvelope` — build the level-1
envelope with `path: "/api/v1/%652ee"` and inner body = a valid second envelope;
assert the outer response refuses (no 200 head frame that would require a second
dispatch) and that only one replay record was written. A second test should
assert the plain `/api/v1/e2ee` and `/api/v1/e2ee/x` paths are still refused.

### C-2 — The replay store sweeps the whole bucket on every request

`internal/store/e2ee_replay.go:17-42` keys records as `SHA-256(deviceID\0id)`
(a 32-byte opaque key) and stores only 8-byte absolute expiries, so it cannot
range-scan by order. `ConsumeE2EERequest` therefore iterates **every** live
record on every call, inside `db.Update`:

```go
for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
    if int64(binary.BigEndian.Uint64(v)) < now.UnixMilli() { cursor.Delete() }
}
```

Concrete failure: n grows with the device's own request rate over a ~10 minute
window (one record per request, `e2ee.go:366`). A phone that streams a PTY and
polls state, or any client that issues a few thousand requests, leaves a few
thousand live records. From then on every envelope request — and therefore every
PTY byte batch, chat send and poll — pays O(n) bucket iteration plus, at the
window edge, an unbounded number of `cursor.Delete`s, all inside a write
transaction that (a) serializes against session persistence and ledger appends
and (b) fsyncs on commit (`store.Open` does not set `NoSync`). The cost is
quadratic in the request rate for the whole window, and the deletion burst can
hold the writer after a traffic spike. A paired device that deliberately bursts
turns this into a sustained daemon-wide stall. C-1 and H-1 make it worse by
multiplying the write transactions per request.

Fix (keep DP-20's "record before dispatch" semantics)

- Add a second bucket `e2eeReplayExpiry` keyed by
  `big-endian(unixmilli) || SHA-256(deviceID\0id)`, so a single
  `cursor.Seek(now)` walks only expired entries and deletes in O(expired), and
  the membership check stays a single `Get` on the hash bucket.
- Or keep one bucket but drive expiry from an in-memory min-heap and a janitor
  goroutine, leaving the request path as `Get` + `Put` only.
- In all cases bound the work per call (e.g. at most 256 deletions) so a single
  request can never hold the writer for a whole bucket.

Test that proves it: seed 20 000 live records and assert
`ConsumeE2EERequest` performs a bounded number of key visits (instrument with a
counter or time a 10k-request loop), plus a correctness test that an expired
record is removed and a live one is retained.

---

## Risks and hardening gaps

### H-1 — Unauthenticated 16 MiB body on an unthrottled route

`internal/httpapi/e2ee.go:312-320` reads `e2eeMaxBody+1` (16 MiB) **before** any
device lookup, key derivation or token check, and `json.Unmarshal` then allocates
again for the base64 fields. `handleE2EE` has no per-source limiter (compare
`allowPair`, `server.go:1434-1458`, 5/min per source, 30/min global) and
`cmd/darkfactory/main.go:890-895` sets no body or concurrency cap. Any host that
can reach the listener, with **no credential**, can hold ~16–48 MiB per
in-flight request; a few dozen concurrent requests are a plausible OOM/GC stall
on a small node.

The cap is also far above need: the largest inner body any handler reads is
`maxBody = 1 << 20` (`server.go:44`, used by `decodeJSON` and every `ReadAll`),
so a 16 MiB envelope only ever carries a ≤ 12 MiB body that handlers then reject.
Base64 + JSON overhead means ~1.4 MiB is sufficient.

Fix: reduce `e2eeMaxBody` to ~2 MiB, reject an over-cap `Content-Length` before
reading, and add the pairing limiter (a shared `pairWindow` keyed by source) to
the envelope route. Test: an unauthenticated 16 MiB POST returns
`codeRequestBodyTooLarge`/429 without the body being retained, and a legitimate
1 MiB inner body still works.

### H-2 — Truncation is indistinguishable from a complete response

The daemon ends a response by flushing whatever the handler wrote
(`e2ee.go:473-484`) — there is no final, authenticated "response complete"
frame. The Android reader treats a clean 4-byte EOF as success
(`LoopbackServer.kt:356` → `break`) and then closes the local chunked body
normally (`:386-389`). When the stream dies mid-frame it throws
`IOException` and, if the head was already written, does the same thing
(`:401-425`).

Failure scenario: an on-path attacker who cannot forge frames can still cut the
connection at a frame boundary. The page receives a short `200` body that looks
complete. For JSON endpoints this is usually a parse error; for
`/api/v1/sessions/{id}/stream` and `/api/v1/opencode/events` it is a clean
end-of-stream, so the page treats a truncated answer as finished. The
authenticated head frame (status, headers) is not affected, so this cannot flip
a refusal into a success — the impact is truncation/rollback of content, not
forgery.

Fix: emit a terminal frame under a distinct AAD label
(`end|deviceId|id|<frameIndex>`, empty plaintext or a final length), and have
the client require it; without it the client must abort the local response (close
the socket or write an incomplete-chunk terminator) instead of appending
`0\r\n\r\n`. Test: a fake daemon that closes after frame 1 must make the proxy
abort the local body, not complete it; a real response must include the
terminator.

### H-3 — An unauthenticated failure deletes the pinned host

`proxyE2EE` maps every outer non-200 to `onLogout` when it is `401`
(`LoopbackServer.kt:334-344`), and maps `AEADBadTagException`, `JSONException`
and `IllegalStateException` to `onLogout` (`:395-400`), as well as any other
exception after the head was written (`:438-440`). `onLogout` calls
`forgetHost` → `tokenStore.remove(hostId)` (`MainActivity.kt:425-429`,
`:727-735`), which deletes the token **and the pinned key**.

A transport attacker cannot produce a valid frame, but can produce a bare
`HTTP/1.1 401` on the outer route, or a length prefix followed by garbage. That
erases the pin and returns the user to the pairing screen. Confidentiality still
depends on the fingerprint comparison, but the design converts a cheap DoS into
"the user is now being asked to pair again", which is exactly the state in which
a substituted key would be presented.

Note this is partly intentional — `substrate-lessons.md` says "Decryption
failure is different and must return to pairing" — and the local-client-abort
case is correctly excluded. The gap is the *strength* of the reaction.

Fix: keep the row and mark it `needsRepair` (show "re-pair required", preserve
the fingerprint for comparison), and only treat an **authenticated inner 401**
(a successfully decrypted head frame with status 401) as revocation. Test: a
fake upstream that answers `401` to the envelope route (and one that sends a
bad frame) must leave `TokenStore` unchanged.

### H-4 — The E2EE gate exists only at startup

`openInitial` removes the active host when `!active.hasE2EE`
(`MainActivity.kt:72-77`), but `showHosts` renders a Connect button for every
registry row (`:692` → `startWeb`), `startWeb` only calls `verifyHost` (which
returns immediately for `!hasE2EE`, `NativePair.kt:113`), and `startWebNow`
passes `e2ee = null` (`:416-418`). `proxy` then falls through to
`proxyLegacy` (`LoopbackServer.kt:203-207`), which sends
`Authorization: Bearer <token>` — the documented "legacy native tokens are
refused by the Android client" is enforced by one cleanup site, not by the
transport.

Reachable case: `openInitial` is skipped when the app is cold-started from a
`darkfactory://pair` deep link (`MainActivity.kt:55-57`), so a pre-E2EE registry
row survives the load, the new host is added by `save()`, and the old row
remains connectable from the picker. A row that loses any of the four E2EE
fields (migration or future bug) reaches the same path.

Fix: gate in `startWeb`/`startWebNow` (refuse and remove a row without a usable
E2EE key set — `Connection.hasE2EE` is already the predicate) and delete the
`proxyLegacy` bearer branch, or make it refuse unconditionally. Test: a
registry containing one non-E2EE row, connected through the picker, must be
removed with a "pair again" message and must never open an upstream connection
(the fake daemon asserts it receives no request).

### H-5 — No negotiated frame ceiling

`e2eeMaxBody` bounds requests only. The daemon seals any single `Write` as one
frame (`e2ee.go:515-535`) and `writeJSON` passes the whole marshalled body to
one `Write` (`server.go:3249-3253`). The client rejects `length > 20 MiB`
(`LoopbackServer.kt:683`) and would throw, surfacing `e2ee_failed`; with
`readTimeout = 0` for streams it can also block indefinitely on a length prefix
the peer never completes.

Failure scenario: a large `/api/v1/audit` or OpenCode messages body crosses
20 MiB and the phone cannot load the view at all, with no client-visible reason
beyond a generic error.

Fix: split daemon writes above 64 KiB into multiple frames, publish the ceiling
in the head frame or in `/api/v1/e2ee/host-key`, and add a read deadline for the
length prefix even on streams. Test: a handler writing 25 MiB produces N frames
each ≤ 64 KiB and the test client reassembles them; a frame of exactly
`MAX_FRAME+1` is refused with a specific code, not a logout.

### H-6 — Re-pairing leaves the previous device row valid

Each `NativePair.pair` generates a fresh client key pair (`NativePair.kt:27-29`),
and `handlePair` always mints a new row (`mintBrowserSessionWithKey`,
`server.go:1465-1506`). `TokenStore.save` replaces the registry entry keyed by
`hostId` (`TokenStore.kt:61-81`), so the phone forgets the old token, but the
daemon keeps the old device row, its replay-free credential and its entry in
`GET /api/v1/devices`. A token extracted before the re-pair stays valid, and the
device list accumulates phantom rows the phone can no longer revoke (it no
longer knows the old `deviceId`).

Fix: before replacing the entry, call `DELETE /api/v1/devices/{oldDeviceId}`
with the old (still valid) token, or have `handlePair` revoke prior `browser`
rows carrying the same `hostId` + label. Test: pair twice against one daemon and
assert `GET /api/v1/devices` lists one row for that host and that the old token
returns 401.

### H-7 — The phone's clock gates every request

`E2EE.kt:51` uses `System.currentTimeMillis()` and the daemon accepts only
±5 minutes (`e2ee.go:330-335`). A phone whose clock is off (manual change, long
offline boot before NTP) gets an outer `400`, which the proxy turns into a 502
whose body it prints. The same applies to a daemon whose clock jumps. There is
no offset discovery, and pairing does not check skew, so the failure appears
only after a successful pair.

Fix: read the daemon's `Date` header (or a `serverTime` field in
`/api/v1/e2ee/host-key`) at verify time and store an offset with the connection;
apply it to `ts`. Treat the refusal code as a distinct, actionable error. Test:
a JVM unit test with a fixed `ts` 10 minutes behind a fake daemon's `now` is
refused, and with an offset applied it succeeds.

### H-8 — Hygiene

- `LoopbackServer.kt:357` rejects `length < 13`, but every frame is at least
  nonce(12) + tag(16) = 28 bytes; `E2EE.openFrame` catches 13..27 afterwards
  (`E2EE.kt:76`) and the handler reports it as an identity failure
  (`:395-400`). Use `28` so a malformed length is reported as a malformed frame.
- `LoopbackServer.kt:379-380` is an empty `if (frameIndex == 1 && innerStatus >= 400) {}`.
  Delete it, or implement the intent (it looks like error-body suppression that
  was removed).

### H-9 — `verifyHost` does not compare `hostId`

`NativePair.kt:112-153` fetches `/api/v1/e2ee/host-key` and compares only
`publicKey`. A rotated instance secret changes `hostId` but not the host key, so
the check passes while the registry key and the server's `hostId` diverge. In
practice the same rotation invalidates the device row by hash, so this is
informational; comparing `hostId` (or updating the entry) makes DP-18's "stable
`hostId`" explicit.

---

## Considered and not a defect

- **Authentication before the replay record** (`e2ee.go:352-374`: decrypt,
  parse, token check, then `consumeE2EEReplay`). Correct ordering: an
  eavesdropper holding the plaintext `id` from the wire cannot burn a device's
  request ids, because the envelope body cannot be authenticated without the
  token. Recording still precedes dispatch (`:412`), so a crash between record
  and effect loses the effect, never duplicates it.
- **Storage failure prevents dispatch**: `ConsumeE2EEReplay` returns the bolt
  error, `handleE2EE` calls `s.fail` and returns before `Handler().ServeHTTP`
  (`:366-370`); covered by `TestE2EEReplayStorageFailureRefusesConsumption`.
  A malformed record (`len(v) != 8`) also fails the transaction closed.
- **Keying by `(deviceId, id)`**: `SHA-256(deviceID + "\0" + requestID)`
  (`e2ee_replay.go:17`) — fixed-width digest, unambiguous separator, no
  concatenation confusion, no plaintext id in the database.
- **Expiry vs. the clock window, including both boundaries.** Acceptance rejects
  strictly *more* than ±5 min (`now.Sub(at) > skew || at.Sub(now) > skew`), so
  `ts == now±5min` is accepted. Expiry is `at+5min` and the sweep deletes only
  when `expiry < now`. Therefore `expiry < now_sweep` ⇔ `now_sweep - at > 5min`
  ⇔ the timestamp check would reject the replay: a record can never be evicted
  while its envelope would still be accepted. Future timestamps can only extend
  retention (up to 10 min), never shorten it. This equivalence is subtle and
  currently untested at the exact boundary; see the tests below. A refactor that
  changes either comparison (or computes expiry from `now` instead of `at`)
  breaks it silently.
- **Concurrency on one id**: bolt's single write transaction makes
  check-then-put atomic; the store-less fallback holds `e2eeMu`
  (`e2ee.go:288-300`). Two concurrent identical envelopes cannot both pass.
- **Daemon restart**: replay records and the host key live in bbolt
  (`store.go:60`, `:375-392`), so a restart neither accepts a replay nor changes
  the identity; `TestE2EEHostKeySurvivesRestart` and
  `TestE2EEReplayPersistsUntilEnvelopeExpires` cover the store half (HANDOFF
  correctly notes there is no end-to-end restart test through HTTP).
- **Unreadable/corrupt host key**: `loadOrCreateE2EE` returns an error,
  `New` logs and leaves `s.e2ee == nil`, and every envelope route refuses
  (`e2ee.go:258-262,304-306`) instead of minting a new identity. Fail-closed.
- **AAD binding.** Request AAD is `req|deviceId|id|ts`; method, path, headers,
  body and token are inside the ciphertext, so GCM authenticates them. Response
  AAD uses a positional frame index (`resp|deviceId|id|<n>`), so dropping,
  reordering or replaying a frame into another request fails authentication
  (`E2EE.kt:75-81` counts positionally rather than trusting a wire index). The
  pairing blob uses a third `pair|hostId|deviceId|clientPublicB64` label
  (`server.go:1399-1403`, `NativePair.kt:71-75`), which also binds the sealed
  token to the client's ephemeral pairing key: a substituted host key cannot
  open it.
- **Header allowlist** (`e2ee.go:399-404`): only
  accept/content-type/last-event-id/x-df-request/origin/user-agent are copied,
  and `Authorization` is set *after* the loop, so it cannot be overridden.
  Forwarding `Origin` is harmless because authentication is a bearer credential,
  not a cookie.
- **Host and RemoteAddr** (`e2ee.go:395-397`): the inner request inherits both
  from the outer connection. Taking `RemoteAddr` from the socket (not
  `127.0.0.1`) is the safe choice — it keeps loopback-gated paths gated. Taking
  `Host` from the client only feeds `hostAllowed`/origin checks, neither of which
  is an authentication boundary for a token holder. Note that when the daemon is
  fronted by a loopback proxy, the inner `RemoteAddr` is loopback, so
  loopback-only routes (`/api/v1/open`) become reachable inside the envelope;
  that grants a full-scope device nothing it does not already have, and a
  read-scope device cannot call `/api/v1/open/code` to obtain a code.
- **Path traversal / mux surprises.** The guard requires a `/api/v1/` prefix.
  `%2e%2e` decodes to an unclean `URL.Path`, which Go 1.22+ `ServeMux` no longer
  redirects but routes to the `"/"` catch-all; that handler is `localUIOnly`
  (refuses a remote peer, and refuses a non-loopback `Host`) and
  `http.FileServer` re-cleans the path against the embedded FS, so neither the
  filesystem nor a daemon-private file is reachable. A trailing-encoded slash
  (`/api/v1/e2ee%2F`) becomes `/api/v1/e2ee/`, which matches no route and falls
  to the same catch-all. Only the encoded-character spelling (C-1) crosses the
  guard.
- **Reading untrusted headers into the outer response.** The head frame is
  filtered only for set-cookie/content-length/transfer-encoding/connection
  (`e2ee.go:498-507`); the values come from the trusted daemon, and the Android
  side re-emits them into the local response without CRLF sanitizing
  (`LoopbackServer.kt:590-611`). With a trusted daemon this is bounded (a
  malicious daemon can already run script in the page); a header value derived
  from client input would be an injection, so keep new handlers from echoing
  request values into headers.
- **Detecting a substituted host key at pairing.** `HostFingerprint.verified`
  rejects any supplied fingerprint that does not describe the received key,
  including an empty one (`HostFingerprint.java:8-14`, proven by
  `HostFingerprintTest`), and the value shown to the user is the locally
  computed one (`NativePair.kt:69,85`). A substituted key therefore cannot ride
  in under the original fingerprint. What remains is TOFU plus the human
  comparison — correctly documented as such, and DP-19's acceptance text is met.
- **Revocation.** `DELETE /api/v1/devices/{id}` removes the row from
  `s.browsers` and the store, so `browserByID` fails and the next envelope gets
  an outer 401 (`e2ee.go:337-341`); the app then forgets the host. The in-memory
  session and the idle/expiry checks in `authenticate` continue to apply to the
  inner request.

## Tests recommended

Go (`internal/httpapi`, `internal/store`)

1. `TestE2EERefusesEncodedNestedEnvelope` — C-1; assert no second dispatch and
   one replay record.
2. `TestE2EEReplayBoundaryWindow` — `ts == now-5m` and `ts == now+5m` accepted,
   `±5m+1ms` refused; then a replay after the record's expiry instant is refused
   by the timestamp check, and the record is kept at `expiry == now` and removed
   at `expiry < now`. (This pins the fragile equivalence above.)
3. `TestE2EEReplaySweepIsBounded` — C-2; 20 000 seeded records, bounded key
   visits per call.
4. `TestE2EEEnvelopeBodyLimitAndRate` — H-1; unauthenticated oversized body
   refused before allocation becomes observable, per-source limit returns 429.
5. `TestE2EEResponseTerminator` — H-2; the final frame is present, and a
   truncated stream is detectable.
6. `TestE2EEConcurrentSameRequestId` — two goroutines, one acceptance, one
   refusal (guards the bolt atomicity claim).
7. `TestE2EEHostKeyCorruptFailsClosed` — a corrupt `e2ee` value returns 503 on
   the envelope route and does not regenerate the key.

Android JVM unit tests (extend `check-fingerprint.sh` with a tiny main, as the
project already does rather than introducing JUnit)

8. `HostFingerprintTest` — add: empty fingerprint rejected; fingerprint of a
   different key rejected (exists); identical key accepted.
9. `FrameReaderTest` — a truncated frame, a zero length, a `MAX_FRAME+1` length
   and a clean EOF each produce distinct outcomes (H-2, H-5, H-8).
10. `LegacyConnectionRefusedTest` — H-4; a non-E2EE `Connection` cannot open an
    upstream request (assert with a local fake).
11. `ForgetOnUnauthenticatedFailureTest` — H-3; a bare 401 and a bad frame leave
    the registry intact.
12. `ReplayAADTest` — swapping two response frames, or replaying frames from
    another request id, fails to open (already correct; pin it).

Device tests (`test/android/`) remain package B's scope; the fingerprint
comparison on a real phone over Tailscale is not replaced by any of the above.

## Residual risk

- **`state.db` read access defeats the pin.** The host private key is stored as
  plaintext PKCS#8 in the `e2ee` bucket (`store.go:42,375-392`) with file mode
  0600. An attacker who can read it can decrypt every phone's envelope traffic,
  recover the bearer tokens inside the ciphertext, and forge responses to
  already-paired clients **without changing the key**, so `verifyHost` never
  fires. This is the strongest attack on the design and it is a local-filesystem
  attack, not a network one. If that boundary is not acceptable, the key needs
  to live behind an OS keystore or an operator passphrase.
- **No forward secrecy.** Static-static ECDH with a long-lived host key and a
  long-lived per-device client key: prefs + Keystore (or the client private key
  alone) plus a recorded transcript decrypts all past traffic for that host.
  Rotation is manual (re-pair) and, per H-6, does not revoke the old row.
- **The client private key is not hardware-bound.** `TokenStore` encrypts the
  registry with an AndroidKeyStore AES-GCM key with
  `setUserAuthenticationRequired(false)` (`TokenStore.kt:281-291`); on an
  unlocked/rooted device or a debuggable build it is extractable.
- **First pairing is TOFU.** A user who does not compare fingerprints accepts
  whatever key answered. The pairing code is low entropy but one-time and rate
  limited; it is not what detects a substituted key.
- **Availability:** the envelope route is a single point of failure with no
  rate limit (H-1), a quadratic replay sweep (C-2) and a recursion vector (C-1).
  A paired- or unpaired-remote attacker can degrade the whole daemon, including
  detached PTY control, without touching confidentiality.
- **After the fix for C-1**, the anti-recursion property is still path-based;
  prefer the context marker so a future route spelling cannot reopen it.
- This review did not run any gate listed in `HANDOFF.md`, so none of the above
  is confirmed fixed or non-regressed by execution. The only executed check was
  a scratch `ServeMux` program (C-1) and the reading of `HostFingerprintTest`.

## Resolution status (integration, 2026-11-02)

This section was added by the integration owner after the review. The findings
above are preserved as written; this table records what changed and what is
still open. It is not a second review.

| ID | Status | Change |
|---|---|---|
| C-1 | Fixed | `handleE2EE` now validates `url.ParseRequestURI(request.Path).Path`, requires origin-form (`Scheme`/`Host` empty), and dispatches `target.RequestURI()`. `e2eeInnerContextKey` in the inner request context refuses a second entry regardless of spelling. `TestE2EERefusesEncodedNestedEnvelope` covers `%652ee`, full percent-encoding, the plain route and `e2ee/state`; it also proves the nested request was not consumed. |
| C-2 | Fixed | `ConsumeE2EERequest` keys records as `big-endian expiry || SHA-256(deviceID NUL requestID)`, walks expired records with the ordered cursor, and deletes at most `replaySweepLimit` (256) per call. A replay has the same authenticated timestamp, so membership stays one `Get`. Tests: bounded sweep with 778 seeded expired records, expiry-instant retention, concurrent same-id, restart. |
| H-1 | Reduced | `e2eeMaxBody` is 4 MiB (inner handlers cap at 1 MiB); `Content-Length` above the cap is refused before reading. `TestE2EEEnvelopeBodyLimit` asserts 413. No per-source rate limit was added; that remains an availability risk. |
| H-2 | Deferred | An authenticated terminator changes the response wire format and both clients. Truncation can roll content back but cannot turn a refusal into a success; recorded as an accepted limit for this release. |
| H-3 | Deferred | A forged outer `401`/bad frame can still make the app forget a host (local denial of pairing, not key substitution). The app must forget on an outer `401` because a revoked token is refused the same way, so this needs a paired daemon+APK protocol change, not a one-sided patch. |
| H-4 | Fixed | `startWeb` refuses a registry row without `hasE2EE`, and `proxy` refuses `/api/` when no cipher exists. `proxyLegacy` now handles only public routes (`/healthz`) and no longer sets `Authorization`; the unused `token` constructor parameter was removed. |
| H-5 | Deferred | Frame splitting and a negotiated ceiling are wire changes. The daemon still seals one `Write` per frame; the client's 20 MiB frame cap remains. |
| H-6 | Deferred | Re-pairing still leaves the previous daemon device row valid. Revoking it requires a server-side pairing semantic (which prior row belongs to this phone) or a client revoke call with the old token; recorded before release. |
| H-7 | Deferred | No clock-offset discovery. A phone more than five minutes off gets a plain 400; the proxy surfaces the detail. Pairing does not check skew. |
| H-8 | Fixed | The client rejects frames shorter than 28 bytes and the empty frame-1 branch is gone. |
| H-9 | Fixed | `verifyHost` compares the returned `hostId` with the registry entry and treats a mismatch as a changed identity. |

Residual risks in the section above are unchanged: plaintext host key in
`state.db`, no forward secrecy, software-held client key, TOFU first pairing,
and the unthrottled envelope route.
