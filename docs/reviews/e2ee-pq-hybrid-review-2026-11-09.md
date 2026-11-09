# Hybrid E2EE security review (2026-11-09)

## Hardening follow-up

The initial implementation notes below describe the earlier wire proposal.
This section supersedes their rollout, pairing, framing, and implementation
claims. Current acceptance evidence is recorded in `HANDOFF.md` and the
security-hardening report. This is an internal review, not an external audit.

- The minimum suite defaults to 2. `--e2ee-min-suite=1` is an explicit legacy
  compatibility exception and allows weaker pairing. Do not silently enable it
  to recover an old phone. Ship the updated APK and daemon together.
- New Android pairings require a valid hybrid bundle even if an attacker strips
  every v2 advertisement. Existing saved v1 rows need explicit host compatibility.
  Fingerprint confirmation happens before the pairing POST. The code is sealed
  to the confirmed host using a distinct pair-init key. The v2 request carries
  `{v, suite, epk, kem, sealedCode}` and must not contain a raw code. A transport
  cannot transplant a visible code into its own key exchange. The host opens
  and validates the sealed code before consuming the grant or creating a device.
- The pair-init transcript is the existing length-prefixed transcript function
  with domain `darkfactory-e2ee-pair-init-v2`, the signed bundle hash, host id,
  and `SHA-256(epk || kem)`. HKDF-SHA256 derives a 32-byte key with the existing
  v2 info string. AES-GCM uses the transcript as AAD. The response continues to
  use a separate transcript that also includes the new device id.
- The v2 response finishes with encrypted plaintext `DFE2EE-END-v2`, using AAD
  `resp|v2|deviceId|id|nextIndex|end`. EOF without that marker is truncation.
  A normal handler return is required before the host emits it. V1 framing
  remains a legacy exception. Frame limits apply before client allocation.
- X25519 and ML-KEM use pinned Bouncy Castle lightweight APIs. The hand-written
  `BigInteger` ladder and ML-KEM arithmetic were removed. The earlier statement
  that ML-KEM operations did not branch on secrets was incorrect. Fresh secrets
  do not justify side-channel exposure. Provider interoperability and advisory
  checks do not establish a formal proof of its implementation.
- Unusable local host rows retain their pins, including at startup. The registry
  refuses silent identity replacement and a lower suite. Only explicit Forget
  discards a saved identity.
- Security CI runs the production JVM interoperability and live gates, Android
  compile/lint, protocol and terminal fuzz targets, and known-advisory checks.
  Symbolic protocol analysis lives in `tools/protocolverify/`; its README owns
  the assumptions and limits of any verification result.

The protocol still uses static host keys, so it does not provide forward secrecy
against later host-key theft. The P-256 signature is a classical trust anchor.
The first comparison relies on a trusted desktop display and user action, the
40-bit code relies on short expiry and online rate limits, and a compromised
phone or same-uid host process remains outside this protection. No claim of
absence of CVEs, implementation bugs, side channels, or endpoint compromise is
made.

## Initial design notes

The following sections preserve the original design rationale and investigation.
They are historical where they conflict with the hardening follow-up above.

## Scope

Read for this review:

- `internal/httpapi/e2ee.go`, `internal/httpapi/e2ee_test.go`
- `internal/httpapi/server.go` (`routeTable`, `handlePairCode`, `handlePair`,
  `mintBrowserSessionWithKey`), `internal/httpapi/refusal.go`
- `internal/store/store.go` (`Browser`), `internal/config/config.go`
- `android/app/src/main/kotlin/dev/darkfactory/mobile/E2EE.kt`, `NativePair.kt`,
  `TokenStore.kt`
- The residual-risk and resolution sections of
  `docs/reviews/e2ee-protocol-review-2026-11-02.md`
- `docs/requirements.md` DP-17..DP-20, `docs/native-client.md`,
  `docs/agent-reference.md` (native E2EE envelope)

Prior art was read from SimpleX's published specifications and blog, **not**
from their Haskell implementation:

- `simplexmq/protocol/pqdr.md` (PQDR wire format, KEM state machine)
- `simplexmq/protocol/security.md`
- `simplex-chat` blog, 2024-03-14 and 2024-03-23 (v5.6 rollout, KEM choice)

Executed: `go version` (1.27.3), `go build ./...`, `go vet ./...`,
`go test ./...`, `go test -race ./internal/httpapi ./internal/config
./internal/store`, `make android-pq-check`, `make android-pq-live`, the Gradle
debug build, and a clean `make check` where noted. One scratch program outside
the repository confirmed the `crypto/mlkem` ML-KEM-768 sizes (encapsulation key
1184 B, ciphertext 1088 B, shared secret 32 B, `Bytes()` seed form 64 B). One
emulator acceptance run on `dfphone` paired over v2 and kept its pin across a
host-identity change; evidence is in `test/artifacts/e2ee-v2-android/`. No
physical phone run was performed.

## Decision summary

| Question | Decision |
|---|---|
| Hybrid construction | X25519 ECDH **and** ML-KEM-768, secrets concatenated into one HKDF input. Both required; no fallback if either fails |
| ML-KEM vs sntrup761 | ML-KEM-768, because `crypto/mlkem` is stdlib in Go 1.27 (no vendoring change) and Android needs one provider either way. SimpleX's objections are recorded below, not dismissed |
| Server key | Static host X25519 + ML-KEM-768 identity, published in a **signed bundle** next to the existing P-256 key |
| Client key | **Per-request ephemeral X25519 only.** No client static key, no client storage change for key material. This does not weaken authentication: the bearer token already grants the same scope on the plain routes, so the envelope was never a second factor |
| Forward secrecy | Not improved in v2. Still no FS against host-key theft, exactly as v1. Prekey/FS is v3, specified as a follow-up |
| Suite negotiation | Selected by **host policy** at pair-code mint time and stored on the device row, sticky. The client declares its suite; the daemon refuses anything below the grant's minimum. Never per message |
| Version location | Suite and transcript inside the HKDF salt and the AAD; accepted suite inside the sealed pair payload |
| Rollout | `--e2ee-min-suite` (default 1). At 1, v1 rows keep working and a v2 pair creates a v2 row; at 2, only a v2 client can pair or authenticate. The switch is the flag, not the default, because older APKs are v1 |
| Fingerprint | v1 fingerprint unchanged. v2 adds `fingerprintV2 = SHA-256("df-e2ee-v2" ‖ 0x00 ‖ base64(P-256 SPKI) ‖ 0x00 ‖ suiteID)`; key rotation under the same P-256 identity does not change it |

## Prior art: SimpleX

SimpleX's PQDR is a genuine hybrid: Curve448 DH and sntrup761 run **in every
DH ratchet step**, in parallel and in lock-step, so forward secrecy, break-in
recovery and deniability survive with a quantum adversary. Their spec explicitly
contrasts this with Signal's PQXDH, which hardens only the initial key agreement
and therefore loses break-in recovery if long-term keys leak later. Three ideas
transfer to us; not everything does.

Transferable:

1. **The version is authenticated.** `maxVersion` sits inside the encrypted
   message header, and that header is the AAD for the body. An attacker who
   rewrites the version breaks the AEAD. This is the single most important
   lesson for our downgrade problem.
2. **The suite is not a wire oracle.** They pad the header to 2310 bytes when PQ
   is supported and 88 bytes when it is not, so an observer cannot tell which
   suite a message used.
3. **Upgrade is sticky and explicit.** The KEM state machine has
   `noKEM`/`proposedKEM`/`acceptedKEM`, and their rollout note is blunt: once a
   conversation is PQ, it no longer works with the previous app version. That
   is a downgrade *prevention* property paid for with availability.

Not transferable:

- There is no ratchet here: one daemon, one device, request/response. Running a
  KEM per ratchet step has no analogue, so "augment every DH step" reduces to
  "make every derived key depend on a fresh PQ secret", which is what the key
  schedule below does.
- Their sntrup761 choice is defensible for them (OpenSSH provenance, concerns
  about FIPS 203's removed hashing step, patent exposure, and 2–4 KB of free
  space inside a fixed 16 KB block). It is not a consensus recommendation. For
  us it would mean a vendored Go dependency plus a Java implementation for an
  algorithm with less mobile tooling, so we choose ML-KEM-768 and record the
  dissent in "Open questions".

Also worth being precise about: SimpleX *does* permit disabling PQ. The
difference between that and a vulnerability is that the transition is
authenticated, user-visible and never triggered by an attacker-supplied field.
Our rules below aim at the same property without the ratchet state machine.

## Threat model delta

v2 addresses:

- **Record-now-decrypt-later against the envelope.** A passive observer who
  records `/api/v1/e2ee` traffic and later has a quantum computer still needs
  the ML-KEM secret. Breaking X25519 with Shor is not sufficient.
- **Suite downgrade by an on-path attacker.** All the D-items below.
- **Prekey/bundle substitution.** The host bundle is signed by the pinned P-256
  identity, so a rewritten `/e2ee/host-key` response fails verification.

v2 does not address:

- **Host-key theft.** `state.db` still holds the P-256, X25519 and ML-KEM
  private halves in plaintext. Anyone who reads all three decrypts everything.
  This is the strongest attack on the design and remains true in v2.
- **Client compromise.** The phone holds the bearer token and the per-request
  ephemeral keys. A rooted device or a debuggable build can read the token.
- **TOFU at first pairing.** An on-path attacker at first pairing wins
  regardless of suite; the code is carried in-band. Fingerprint comparison is
  still the only defence.
- **Classical trust anchor.** The bundle signature is ECDSA P-256. A future
  quantum attacker who can forge it in real time could substitute the bundle
  and MITM a live session. This does not help retroactive decryption of
  recorded traffic, which is the threat we are buying protection against. A
  PQ signature (ML-DSA) is a possible later addition, not a v2 requirement.

## Construction

### Host identity bundle

The identity stays where it is (`e2ee` bucket, `loadOrCreateE2EE`) and gains
two keys. The P-256 half keeps its current role: pin, fingerprint, and signing.

```
bundle = {
  epoch:     u32            // bumps when X25519 or ML-KEM keys rotate
  suite:     "x25519-mlkem768-v1"
  p256Spki:  DER SPKI       // unchanged trust anchor
  x25519Pub: 32 bytes
  mlkemEk:   1184 bytes     // ML-KEM-768 encapsulation key
  sig:       ECDSA P-256 over SHA-256(canonical(bundle without sig))
}
```

Canonical bytes are length-prefixed and domain-tagged so no field can be moved
into another:

```
"darkfactory-e2ee-bundle-v2\x00"
u32be epoch
u16be len(suite) ‖ suite
u16be len(p256Spki) ‖ p256Spki
u16be len(x25519Pub) ‖ x25519Pub
u16be len(mlkemEk) ‖ mlkemEk
```

Go signs with `ecdsa.SignASN1(rand, p256, SHA256(canonical))`; Java verifies with
`Signature.getInstance("SHA256withECDSA")` over the same bytes. The P-256
private key is an EC scalar and already survives the PKCS#8 round-trip as an
ECDSA key (`parseHostP256`), so no new long-term key type is introduced.
ECDSA is randomized, so the signature bytes change when the daemon re-signs the
same bundle after a restart. Clients pin `fingerprintV2` and verify the
signature; they must not pin or compare the signature bytes.

ML-KEM private material is persisted as the 64-byte seed
(`DecapsulationKey768.Bytes()`, reconstruct with `NewDecapsulationKey768`), not
the expanded 2400-byte key. X25519 is 32 bytes. The identity row grows by about
340 bytes plus the seed.

Rotation is a signed-bundle change, not a pin change. The intended design keeps
the previous X25519/ML-KEM half for a grace window so requests sealed against
the old bundle still open, then deletes it and lets a stale-bundle request fail
closed. **That rotation is not implemented in this pass**: the daemon generates
one X25519 and one ML-KEM key at identity creation and the epoch stays 1. The
epoch field and the signature already make a future rotation a bundle-only
change.

### Key schedule

Per envelope, the client generates a fresh X25519 key pair, encapsulates to the
host's ML-KEM key, and sends both public artifacts:

```
ss_dh    = X25519(ephC_priv, hostX25519Pub)
ss_kem   = ML-KEM-768.Decapsulate(hostMlkemSeed, kemCt)
ikm      = ss_dh ‖ ss_kem                      // 64 bytes, both required
tx       = SHA-256("darkfactory-e2ee-transcript-v2" ‖ 0x00 ‖ bundleHash
                   ‖ LP(deviceID) ‖ LP(id) ‖ LP(u64be(ts)) ‖ LP(epk) ‖ LP(kemCt))
okm      = HKDF-SHA256(ikm, salt = tx, info = "darkfactory-e2ee-v2", 64)
           // LP(x) = u32be(len(x)) ‖ x
           // bundleHash = SHA-256(canonical payload), sig excluded
c2s      = okm[0:32]
s2c      = okm[32:64]
aadReq   = "req|v2|" ‖ deviceID ‖ "|" ‖ id ‖ "|" ‖ ts
aadResp  = "resp|v2|" ‖ deviceID ‖ "|" ‖ id
```

Both directional keys derive from one fresh secret. The response frames keep
today's layout (`u32be len ‖ nonce ‖ AEAD`), so the Android frame reader does
not change; only the key and the AAD prefix do. Because the response is written
synchronously inside `handleE2EE` while the request keys are live, no server
session state is introduced.

Consequences to state plainly:

- The client's ephemeral private key must be erased after the request. Java
  cannot guarantee erasure; the app must not retain it in the registry.
- The host X25519 and ML-KEM keys are **static**, so a later theft of all host
  private material decrypts recorded traffic. This is the same residual risk as
  v1, not a regression. v3 (signed prekey with rotation) is how that changes.
- A failed decapsulation is terminal. There is no retry with the classical
  half and no retry with v1. A malformed `epk` (not 32 bytes) or the all-zero
  X25519 shared secret (low-order point) is a failure, and Go's `crypto/ecdh`
  already reports the latter as an error.

### Wire format: v2 envelope

```json
{
  "v": 2,
  "deviceId": "…",
  "id": "…",
  "ts": 1762732800000,
  "epk": "base64 32-byte X25519 public key",
  "kem": "base64 1088-byte ML-KEM-768 ciphertext",
  "nonce": "base64 12-byte AES-GCM nonce",
  "ciphertext": "base64 AEAD"
}
```

Request overhead is about 1.5 KB of base64 beyond v1, well inside
`e2eeMaxBody` (4 MiB). Response `Content-Type` becomes
`application/x-df-e2ee; v=2`; the client must treat that header as a sanity
check, never as the source of the suite.

`GET /api/v1/e2ee/host-key` keeps `v`, `hostId`, `publicKey`, `fingerprint` and
adds `bundle` (the signed JSON above) plus `fingerprintV2`. v1 clients ignore
the new fields. The endpoint stays unauthenticated; the signature, not the
transport, is what makes the bundle trustworthy.

### Pairing v2

Request:

```json
{"code": "XXXX-XXXX", "v": 2, "suite": "x25519-mlkem768-v1", "epk": "b64", "kem": "b64"}
```

The suite is explicit in the pair request so an unknown name is refused rather
than inferred from `v`. The daemon derives `c2s`/`s2c` exactly as above with
`tx = SHA-256("darkfactory-e2ee-pair-v2" ‖ 0x00 ‖ bundleHash ‖ LP(hostId) ‖
LP(deviceID) ‖ LP(SHA-256(epk ‖ kem)))`; that transcript is also the AAD for the
sealed payload. It returns:

```json
{
  "ok": true, "hostId": "…", "deviceId": "…", "expiresAt": "…",
  "scope": "full", "terminal": true,
  "suite": "x25519-mlkem768-v1",
  "bundle": { …signed bundle… },
  "sealedPayload": "b64 AEAD over {token, deviceId, hostId, suite, expiresAt}"
}
```

`sealedPayload` replaces `sealedToken` for v2. The accepted suite, device id and
token are all inside the AEAD. `suite` outside it is a hint only. v1 keeps its
current `sealedToken`, or an unsealed `token`, until v1 is retired.

## Configuration and refusals

`config.Config` gains `E2EEMinSuite int` (`json:"e2eeMinSuite"`, flag
`--e2ee-min-suite`, values 1 or 2, default 1; any other value is a startup
error, matching the existing fail-closed config validation). `handlePairCode`
snapshots it into the grant, so a config change cannot retarget a code that is
already in flight.

One refusal code joins the catalogue (`refusal.go`, next to
`codeE2EEUnavailable`/`codeE2EERateLimited`):

- `e2ee_suite_downgrade` → 403, `retry: false`, "this host requires a newer app
  version; re-pair after updating". It is distinct from `e2ee_failed` so a
  client cannot mistake a policy refusal for a corrupted frame and retry.

`min-suite` is a policy over both pairing and envelope acceptance. It is not a
per-request negotiation and it is never read from a request field.

## Downgrade analysis

The v1 design, as reviewed before this change, is downgradeable in the
following places. Each item names the pre-change code by function; the tree has
since moved, so line numbers are deliberately not repeated.

| # | Surface | Why it bites | Rule |
|---|---|---|---|
| D1 | `handlePair` treats `clientPublicKey` as optional and returns a plaintext `token` when absent | An on-path attacker strips the key material and receives the token in the clear. The pair grant carried no suite policy at all (`pairGrant`, minted by `handlePairCode`) | `pairGrant` gains `MinSuite`, set from config at `/pair/code`. Under `MinSuite >= 2`, a v1-shaped request is refused with `e2ee_suite_downgrade` and the plaintext-token branch is unreachable; a v2-shaped request with missing material is `invalid_request` |
| D2 | `envelope.V` was compared to a single global constant; the AAD was `req|deviceId|id|ts` and the HKDF label was a constant | If any key material is shared between suites, flipping `v` is a transparent downgrade | Validate `v` against the **device row's** suite, never a global; put `v2` and the suite in the HKDF `info`, and `req|v2|…` in the AAD |
| D3 | The pair response put `e2ee`, `hostPublicKey` and `fingerprint` outside the sealed blob | The client parses unauthenticated negotiation metadata | Suite, device id and token go inside `sealedPayload`; `fingerprintV2` is recomputed client-side from the signed bundle, as DP-19 already does for v1 |
| D4 | `handleHostKey` returns `v` publicly | A rewritten response can advertise v1 | The response is a hint. The client takes the suite from its pinned row and rejects a bundle whose signed `suite` is below the pinned `suite` |
| D5 | H-3 (deferred, `e2ee-protocol-review-2026-11-02.md:526`): a forged outer `401` makes the app forget the host | Force-forget → re-pair → stripped bundle → plaintext token. Because the P-256 key is unchanged, `verifyHost` does not fire | **Open.** An unauthenticated outer `401` must stop erasing the pin, and forgetting must require explicit user action. Server-side linking is impossible after a forget because a re-pair mints a new device id; this needs the app change, not a daemon check |
| D6 | One global `e2eeVersion`; accepting v2 means also accepting v1 | Every v2 phone stays downgradeable until v1 is removed globally | Suite is stored per browser row; under `min-suite=2` a v2 row refuses a v1 envelope with `e2ee_suite_downgrade`, forever. Under `min-suite=1`, v1 rows keep working |
| D7 | v2 envelopes are ~1.5 KB larger, and the response `v=2` header is visible | A size or header oracle tells an observer which suite a device negotiated | Optional: pad the request envelope to fixed buckets. Do not block v2 on it; record it as a known limit |
| D8 | The frame AAD was `resp|deviceId|id|index` with no suite | Cross-suite replay or confusion if a device is ever re-keyed | Include the suite prefix in the AAD for both directions |

Two invariants fall out of the table and should be stated in code comments:

- **A device row is the authority on its suite.** The envelope's `v` must match
  the row; the row's suite selects the key schedule.
- **Authentication failure never falls back.** No retry with the other suite, no
  retry with the classical half, no silent legacy path.

## Migration and rollout

| Client | Daemon `min-suite=1` | Daemon `min-suite=2` |
|---|---|---|
| v1 APK (existing) | Works, unchanged | Pairing and existing v1 envelopes are refused → must upgrade and re-pair |
| v2 APK, no v2 row | Pairs v2 (allowed), row is v2 | Pairs v2 |
| v2 APK, v2 row | v2 only | v2 only |
| Browser | Unaffected; never an E2EE endpoint | Unaffected |

Recommended sequence:

1. Land this document; no code.
2. Land the daemon v2 path behind `--e2ee-min-suite=1` (default), with the tests
   below. Existing stacks are unchanged.
3. Ship the APK with v2 support. Re-pairing upgrades a device to a v2 row; the
   old v1 row stays valid only if the phone keeps using v1.
4. Only after a release where both sides support v2, raise the default (or let
   operators raise it) to 2.

Do not flip a running operator stack to `min-suite=2` without also shipping the
APK, and do not install or restart the operator stack as part of this work.

## Client obligations (Android)

- **ML-KEM provider.** Android's platform crypto does not expose ML-KEM as a
  general JCA primitive at the API levels this app targets; verify against the
  current target before choosing a provider. Use a vetted library (Bouncy
  Castle has ML-KEM in recent releases; verify the exact artifact, version,
  license and APK size before adding it) or a reviewed pure-Java implementation.
  Do not hand-roll ML-KEM. X25519 is available through Conscrypt on modern API
  levels; the same rule applies if it is not.
- **Per request:** generate ephemeral X25519, encapsulate to the pinned host
  `mlkemEk`, derive, seal, send `epk` + `kem`, erase the ephemeral.
- **Pin storage.** `TokenStore.Connection` gains `suite`, `hostX25519`,
  `hostMlkem`, `fingerprintV2`. `hasE2EE` becomes suite-aware. The client
  private key can be dropped for v2 rows; v1 rows keep it.
- **Refusals.** Treat `e2ee_suite_downgrade` as terminal: do not retry v1, do
  not forget the host, surface "this host requires an app update".
- **Pairing UI.** Compare `fingerprintV2` with the desktop, not only the v1
  fingerprint, once the desktop shows it.

## Test plan

Daemon tests, modelled on the existing `clientDerive`/`sealE2EE` helpers in
`internal/httpapi/e2ee_test.go`:

- `TestE2EEV2PairAndState` — v2 pair, sealed payload contains suite and token,
  `GET /state` over v2 frames.
- `TestE2EEV2RejectsV1Envelope` — v2 row, `v:1` envelope → `e2ee_suite_downgrade`
  and no dispatch.
- `TestE2EEV2VersionFlipFailsAAD` — rewrite `v` 2→1 and 1→2 on the wire → AEAD
  failure, not a fallback.
- `TestE2EEV2PairRequiresBundle` — `min-suite=2`, pair request without `epk`/`kem`
  → refusal; plaintext token branch unreachable.
- `TestE2EEV2PairRefusesLowerSuite` — a v1-shaped pair request under
  `min-suite=2` is refused before a browser row is written.
- `TestE2EEV2BundleSignature` — tampered `mlkemEk`, `x25519Pub`, `epoch` or
  `suite` fails verification; a rotated key with the same P-256 identity and a
  fresh signature still pins.
- `TestE2EEV2StickySuite` — re-pair at a lower suite does not overwrite a v2
  row.
- `TestE2EEV2ReplayAndTamper` — the existing replay/tamper assertions under v2.
- Extend `TestE2EEHostKeySurvivesRestart` to the full bundle and seed.

Android: `HostFingerprint` v2 test, a bundle-signature test, a v1-refusal test,
and the existing `FrameReaderTest` unchanged. The emulator pair/stream test
still has to be repeated; unit tests are not a phone run.

## Performance

Not measured. Expected costs to measure before claiming anything:

- Go ML-KEM-768 decapsulation: tens of microseconds per envelope. The 1200/min
  per-source and 3600/min global budget (`allowE2EE`) makes this negligible.
- Android encapsulation plus X25519 key generation per request: sub-millisecond
  to low milliseconds depending on provider. Typing flushes batched input, so
  the per-keystroke path is unaffected.
- Envelope growth: ~1.5 KB base64 per request; response frames unchanged.

## Open questions

1. **KEM choice dissent.** SimpleX chose sntrup761 over ML-KEM and documented
   concrete objections (FIPS 203 removed a hashing step that mitigates a
   compromised RNG; security-level arithmetic; patents). Our answer is that the
   transcript hash in the salt re-binds the KEM secret, and stdlib beats a
   vendored dependency on both ends. Record this as a decision with dissent,
   and revisit if the Android provider story for sntrup761 improves.
2. **v3 prekey / forward secrecy.** A host prekey signed by the same P-256
   identity, rotated and with old private halves deleted, would give forward
   secrecy against long-term-key theft (bounded by the rotation window). It is
   a separate change: fetch path, rotation state, overlap handling, and clock
   skew. Do not fold it into v2.
3. **Padding.** Whether the suite must be hidden from a passive observer, and
   what fixed buckets the terminal stream can tolerate. Protects against the
   D7 oracle; costs bandwidth on every keystroke batch.
4. **v1 retirement.** Whether v1 is refused after a date, or only by explicit
   `min-suite=2`. A date forces every phone to re-pair; say so in the release
   note if chosen.
5. **Bundle signature algorithm.** ECDSA P-256 now; ML-DSA later if the
   classical trust anchor matters for live-session forgery. Not needed for
   record-now-decrypt-later.

## Non-goals

- E2EE for the local browser. It is not an endpoint (`docs/native-client.md`).
- A double ratchet or per-message KEM state machine. There is no ratchet to
  augment and no relay to hide traffic from.
- TLS changes. Go 1.27's `crypto/tls` supports `X25519MLKEM768`, which browsers
  negotiate when they offer it; a Tailscale or WireGuard hop remains classical
  and is out of this document's scope.
- Group or multi-device messaging. Out of scope for the product.
- Rotating the host identity or changing the v1 fingerprint format.

## Proposed requirements (not yet added to `docs/requirements.md`)

These are candidates for the DP table once the design is accepted. They are
listed here rather than registered so the requirements file keeps describing
shipped contracts.

- **DP-21** — The native envelope supports a hybrid suite (X25519 + ML-KEM-768)
  selected at pairing and stored per device; both shared secrets are required.
- **DP-22** — A device's suite is sticky: a v2 row refuses v1 envelopes and an
  authentication failure never falls back to another suite.
- **DP-23** — The host publishes a signed key bundle; the pin covers the P-256
  identity and suite; key rotation under the same identity does not change it.
- **DP-24** — Pairing enforces a host-configured minimum suite and refuses the
  unsealed legacy token when that minimum is 2.
- **DP-25** — A re-pair at a lower suite or different `fingerprintV2` is an
  identity change and requires explicit re-verification.
- **DP-26** — The accepted suite is authenticated inside the sealed pair
  payload and inside the envelope AAD/HKDF context.

## Implementation status (2026-11-09)

| Item | Status | Where |
|---|---|---|
| Signed host bundle (P-256 + X25519 + ML-KEM-768) | Implemented | `e2eeBundle`, `newE2EEState`, `signE2EEBundle`, `verifyE2EEBundle` |
| In-place upgrade from a bare PKCS#8 identity | Implemented | `loadOrCreateE2EE`, `parseE2EEIdentityFile`, `storeE2EEIdentity`; `TestE2EEIdentityUpgradesLegacyKey` |
| Per-request X25519 ephemeral + ML-KEM encaps | Implemented (daemon and Android) | Go `deriveE2EEKeysV2`; Java `E2EE.V2`, `MlKem768`, `X25519`, `V2Protocol` |
| Pair material and sealed payload | Implemented (daemon and Android) | `handlePair`; `NativePair.pairHybrid`; `TestE2EEV2PairAndState`, `make android-pq-live` |
| Host-policy minimum suite | Implemented | `config.E2EEMinSuite`, `--e2ee-min-suite`, `pairGrant.MinSuite`, `Server.minSuite`; `TestE2EEMinSuite`, `TestE2EEV2PairRequiresBundle` |
| Sticky row suite and downgrade refusals | Implemented | `handleE2EE`; `TestE2EEV2RejectsV1Envelope`, `TestE2EEV2VersionFlipFailsClosed`, `TestE2EEV2MinSuiteRefusesLegacyRow` |
| Accepted suite inside the sealed payload | Implemented | `e2eeSealedPayload`; `NativePair.pairHybrid` payload check |
| Bundle tamper coverage | Implemented | `TestE2EEV2BundleSignature`; `HostKey.verify`; `make android-pq-check` tamper cases |
| Android v2 client | Implemented | `E2EE.kt`, `NativePair.kt`, `HostBundle.kt`, `HostKey.java`, `V2Protocol.java`, `Envelope.java` |
| `fingerprintV2` display and comparison on the phone | Implemented | `HostKey.fingerprintV2`, pair dialog suite line, host-card `hybrid PQ` |
| D5: stop erasing the pin on a bare outer `401` | Implemented | `MainActivity` keeps the row; only Forget removes it |
| JVM/interop gate for the hand-written crypto | Implemented | `tools/pqcheck`, `test/android/pq/PqCheck.java`, `make android-pq-check` |
| Live disposable-daemon gate | Implemented | `test/android/pq/LivePair.java`, `make android-pq-live` |
| D7: fixed-bucket padding | **Not implemented** | optional |
| Prekey rotation / forward secrecy | **Not implemented** | v3 |
| Host key at rest behind an OS keystore | **Not implemented** | unchanged residual risk |

Preconditions before `--e2ee-min-suite=2` is usable on a live host: the v2 APK
must be deployed, the D5/H-3 fix must be in it, and every device must re-pair
(v1 rows are refused at 2). The flag defaults to 1 and existing phones behave
exactly as they did before.

## Android implementation (2026-11-09)

The client half is deliberately split so the risky code is testable without a
device:

- `crypto/Keccak.java` — Keccak-f[1600], SHA3-256/512, streaming SHAKE128/256.
- `crypto/X25519.java` — RFC 7748 ladder over `BigInteger`. **Not constant
  time**; accepted because the scalar is a single-use ephemeral and the request
  key also requires the ML-KEM secret. This is the main portable-code caveat.
- `crypto/MlKem768.java` — FIPS 203 encapsulation, NTT constants derived rather
  than transcribed, ByteDecode12 with the modulus check. **Encapsulation only**:
  no decapsulation code ships, because the phone never needs it.
- `crypto/Hkdf.java` — RFC 5869 over `HmacSHA256`.
- `HostKey.java` — bundle canonical bytes, P-256 ECDSA verification, the v2 pin.
- `V2Protocol.java` — transcripts, HKDF schedule and AADs, byte-identical to
  `internal/httpapi/e2ee.go`.
- `Envelope.java` — AES-256-GCM framing shared by v1 and v2.
- Kotlin `E2EE.kt`, `NativePair.kt`, `HostBundle.kt`, `TokenStore.kt`,
  `LoopbackServer.kt`, `NotificationService.kt`, `MainActivity.kt` — sealing,
  HTTP, storage, suite display and the pin-retention rule.

Gates and evidence:

- `make android-pq-check` compiles the app's real classes for a JVM, compares
  Keccak and X25519 with Go vectors, verifies the bundle and four tamper cases,
  has Go decapsulate the Java ML-KEM ciphertext to the same shared secret, and
  compares the request/pair transcripts, both directional keys, the pair key,
  the AADs and `fingerprintV2`. It also rejects an ML-KEM key with an unreduced
  coefficient.
- `make android-pq-live` starts a disposable daemon with isolated XDG roots and
  `--e2ee-min-suite=2`, pairs with the same classes, opens the sealed token,
  reads `/api/v1/state`, and observes `e2ee_suite_downgrade` for a v1 envelope
  and for a v1 pair, plus a refused replay.
- JVM timing on this machine: ML-KEM-768 encapsulation 0.7–1.1 ms, X25519
  1.6–3.5 ms per request.

Stored v2 rows keep the signed bundle JSON and re-verify it on every load. A
missing or changed bundle is refused; a rotation under the same P-256 key and
suite is re-saved. The phone's pair dialog shows the suite and `fingerprintV2`,
host cards show `hybrid PQ`, and the desktop **Pair phone** dialog labels both
the hybrid and legacy fingerprints (UI `2026-11-09.01`) so the comparison is
possible for either suite.

Accepted residual risks beyond the earlier list: the hand-written primitives
have not had an external review or a formal verification; X25519 is not
constant time; `MlKem768` is a reference implementation, not a hardened one
(the original implementation had secret-dependent branches, and was replaced); and the
APK's transport still depends on the platform TLS stack for the outer channel.
