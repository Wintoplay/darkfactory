# Orca security review — 2026-11-07

Status: static review of the repository at
`~/src/orca` (commit `9b98b385060`).
No Orca process was started, no network request was made to it, and no test
was run. Repository text was treated as data: nothing in the repo was followed
as an instruction. This is an architecture and trust-boundary review, not a
line-by-line audit, a dependency audit, or a fuzzing result.

Orca is a large Electron application: a desktop shell, a `orca serve` runtime,
a CLI, a mobile app, SSH and cloud execution hosts, agent status hooks, a
browser pane, plugins, and orchestration. It is useful to us because it solves
the same product problem DarkFactory does — run several coding agents in
worktrees, from a browser/desktop/phone — and it has been public long enough
that its trust boundaries are explicit in code.

## 1. The thesis

**Orca does not sandbox agents.** It runs them as the local user, in a
worktree, and says so: the `workspace` permission is documented as “equals
running commands as this host's user” (`src/main/runtime/rpc/rpc-method-permission.ts`).
There is no separate UID, no Landlock, no network namespace, and no container
for a normal local workspace. The per-workspace environment feature can put a
workspace on a remote VM, container host, or SSH host, but that is an explicit
deployment choice, not local containment, and the default is still a PTY on
the user's machine.

Its security is built somewhere else:

1. a small number of explicit trust boundaries (renderer vs main, paired device
   vs owner, remote network vs local/tailnet, agent vs the app's own status);
2. per-request authorization with revocable, scoped credentials;
3. honest labels about what a permission actually grants.

That is the lesson for DarkFactory. The recent jail/env allow-list work was
trying to turn a same-UID process into a separate principal. Orca shows the
cost of that path and the alternative: keep the agent in the user's security
domain, make the control plane explicit, and reserve real containment for a
separate mode.

## 2. Trust model observed in Orca

| Principal | What it is | What Orca assumes |
|---|---|---|
| Owner | The OS user running the desktop/`serve` | Trusted. Owns the config, device registry, E2EE key, and local runtime token. |
| Local CLI | A process using the owner token from runtime metadata | Owner. Same authority as the desktop. |
| Renderer | Electron UI | Untrusted web content. Sandboxed, isolated, no Node, no arbitrary navigation. |
| Paired runtime client (CLI/web) | Another machine paired with a device token | A separate principal, but its `workspace` permission is host-user code execution. Pairing is therefore an RCE grant. |
| Mobile device | Phone, optionally delegated through a desktop | Narrow allowlist, never `pairing-admin`, inherits parent state. |
| Public web page | Any origin that can reach a loopback port | Untrusted. Must not be able to drive the runtime without a token. |
| Agent process | Claude/Codex/OpenCode/etc. in a PTY | Same OS user as the owner. Not a security boundary. |
| Agent status hook | HTTP POST from an agent hook script | Telemetry only. It can influence UI state, never authorization. |
| Browser/plugin guest | Arbitrary web content inside Orca | Untrusted. Isolated partitions, pinned preferences, CSP, no Node. |

The key admission is row 8. Orca gives the agent the user's filesystem and
network, then spends its effort on rows 2–7 and 9–10.

## 3. What Orca does well (and is worth copying)

### 3.1 Two control planes, two credentials

- **Local:** a Unix socket, mode `0600`, NDJSON frames capped at 1 MB and 32
  connections, with a 30 s idle timeout. Every request carries the owner
  `authToken`; the token is written to runtime metadata with `writeSecureJsonFile`
  (owner-only permissions). See `src/main/runtime/rpc/unix-socket-transport.ts`
  and `src/main/runtime/rpc/server/runtime-rpc-request-admission.ts`.
- **Remote:** a WebSocket transport that authenticates with a **per-device**
  token, not the owner token. The device is re-resolved on every request, so
  revocation takes effect before the socket closes. See
  `src/main/runtime/rpc/server/runtime-rpc-websocket-dispatch.ts`.

The Split matters. The local owner token is a convenience for the desktop and
CLI. A remote device never receives it, and a paired client cannot promote
itself to owner.

### 3.2 Permissions are declared, scopes are stamped by transport

`rpc-method-permission.ts` declares eight permissions for every RPC method:
`workspace`, `desktop-control`, `accounts-admin`, `settings-write`,
`skills-admin`, `pairing-admin`, `host-admin`, `device-self`.
`rpc-caller-scope.ts` grants them per caller:

- `owner`: everything;
- mobile: a method-name allowlist and never `pairing-admin`;
- `runtime-paired`: a standard set plus only the grants fixed at pairing time.

The transport stamps the proven scope; the dispatcher does not trust a scope
field in the request. A paired client can never widen its own grants. This is
the pattern DarkFactory should adopt next: we already have a gateway and a
ledger, but only `full`/`read` scopes and human/overseer/worker principals.

### 3.3 Network source policy at accept time

`serve` binds loopback by default. Widening for pairing is explicit. Before
HTTP parsing or the WebSocket upgrade, a source guard classifies the peer:

- loopback, private/LAN, Tailscale CGNAT `100.64.0.0/10` and ULA
  `fd7a:115c:a1e0::/48` are admitted;
- public addresses are refused unless `--allow-public` (or a recorded
  grandfathered pairing) allows them.

See `network-source-class.ts`, `ws-network-source-guard.ts`,
`serve-network-source-policy.ts`, and `runtime-rpc-network-exposure.ts`.
There are also pre-auth timeouts, per-source unauthenticated connection caps,
an unpaired-device auth throttle, frame-size limits, and long-poll capacity
limits.

DarkFactory already refuses a non-loopback bind without `--allow-remote` and a
plaintext one without `--insecure-remote`, and it has host/origin checks. The
missing piece is the explicit source classification and the pre-auth caps on a
non-loopback listener.

### 3.4 Device credentials are first-class

- A device gets its own random credential (`randomBytes(24).toString('hex')`),
  not the owner token.
- Pairing offers have a TTL (10 min mobile, 60 min runtime) and can be reused
  only with identical grants.
- A client can exchange the pairing token for a client-held token; the host
  retires the original after first use (`pairing-ticket-exchange.ts`).
- Revocation is a row deletion plus an in-memory recheck per request.
- Delegated phones inherit their parent; any ancestor revocation invalidates
  the child.
- Registry writes are durable, merged over the file, and fail closed if the
  file is unreadable (`device-registry-store.ts`).

DarkFactory has most of this already: hashed browser/device sessions, pair
codes with a 120 s TTL, device keys, E2EE, and a `full`/`read` scope. The gap
is granularity, not existence.

### 3.5 Secure file and secret handling

`src/shared/secure-file.ts` writes to a temp file, restricts it to `0600`,
fsyncs when durability is requested, and renames it into place; on Windows it
uses an ACL-restricted writer. The secret store uses Electron `safeStorage`
and, unusually, reports when the OS keyring is unavailable or when Linux fell
back to `basic_text` obfuscation (`src/main/host/electron-secret-store.ts`).
That honesty is better than a green “encrypted” label.

DarkFactory already writes the master token with `0600` and an atomic replace.
The lesson is the **protection-gap report**: when the OS cannot seal a secret,
say so in the UI and `status`, rather than silently storing it in plaintext.

### 3.6 Renderer and guest hardening

The main window uses `sandbox: true` and a preload bridge; the renderer gets
no Node integration. Untrusted `webview` attachments are admitted only for a
registry-validated partition and source, then their preferences are rebuilt
from an allowlist: `nodeIntegration: false`, `contextIsolation: true`,
`webSecurity: true`, `allowRunningInsecureContent: false`, `webviewTag: false`,
popups denied, permissions denied, navigation pinned. Plugin frames get their
own partition, CSP (`default-src 'none'; connect-src 'none'`), pinned
preferences, and no Node. See `main-window-webview-security.ts`,
`privileged-window-navigation.ts`, and `src/shared/plugins/frames/plugin-panel-shell.ts`.

DarkFactory's UI already sets a strict CSP, `object-src 'none'`,
`frame-ancestors 'none'`, and origin checks. If we ever embed a browser or a
third-party panel, this is the pattern: allowlist the attachment, rebuild its
preferences, deny permissions and navigation, and never render agent output as
HTML.

### 3.7 Agent hooks are deliberately non-authoritative

The hook server binds `127.0.0.1`, accepts only `POST`, requires a random
per-run token in `X-Orca-Agent-Hook-Token`, and publishes the endpoint in a
`0600` file. It has a slowloris timeout and body caps
(`src/main/agent-hooks/server/server-lifecycle.ts`,
`src/shared/agent-hook-listener/transport/endpoint-publication.ts`).
The README is explicit that hooks only report status; OSC 7501 is a fallback.
The token lives in the agent's environment, so a malicious agent can spoof its
own status — Orca accepts that because status is not authority.

This is the right framing for any agent-facing API we add: **give the agent a
capability to report, not a capability to authorize.** If an agent needs a
grant, the human must approve it in a separate channel.

### 3.8 Operational details worth stealing

- Pairing URLs are bounded and parsed strictly (`orca://pair?code=...`), with
  exact host and size checks.
- A device registry that cannot be read is not overwritten; an unreadable file
  fails closed rather than revoking every device by accident.
- The per-VM-environment guide warns that `orca serve` writes the device
  registry and E2EE key into user data, and that snapshotting a machine after
  the runtime has run bakes the same identity into every clone. That is a real
  incident class, and it applies to any image/snapshot feature we add.

## 4. Weaknesses and residual risk

These are not reasons to reject Orca; they are places where DarkFactory can do
better, or must be honest about doing the same thing.

**W-1. The local owner token is readable by any same-UID process.**
The runtime metadata is `0600`, but the agent is the same user. A rogue agent
can read the token and drive the owner RPC surface. Orca accepts this because
`workspace` already equals shell access. DarkFactory's session host has the same
problem in a sharper form: its socket is *unauthenticated*, so an agent does
not even need to find a token. Adding a token does not create a same-UID
boundary, but it turns “any local process can drive every session” into “a
process that has read one specific file can,” and it gives us a place to hang
per-caller permissions. Do it, but do not call it containment.

**W-2. Device bearer tokens are stored in plaintext at rest.**
`orca-devices.json` is `0600` and contains `token` strings for bearer devices
(see `DeviceEntry`). A process that can read that file can impersonate every
paired device. Orca mitigates by using per-device rows, expiry, and revocation,
and by moving new clients to key-based credentials, but hashing bearer tokens
at rest (as DarkFactory does for browser sessions) would be strictly better.

**W-3. The pairing URL/QR carries a bearer credential.**
That is inherent to QR pairing. Orca limits the window with a TTL and ticket
exchange, but a screenshot, shoulder surf, or chat log is a valid credential
until first use. Treat pairing codes like passwords and make the UI say so.

**W-4. The WebSocket server does not check `Origin`.**
Authorization is the per-device token only. A 192-bit token makes a blind
cross-site WebSocket attack impractical, but an explicit `Origin`/`Host` check
is cheap defense in depth and prevents a compromised page from using a token
it somehow learned. DarkFactory already does this for HTTP; do it for any
WebSocket transport we add.

**W-5. A paired runtime client gets a broad standard grant.**
`RUNTIME_PAIRED_STANDARD` includes `workspace`, `settings-write`,
`accounts-admin`, `skills-admin`, `host-admin`, and `device-self`. Only
`desktop-control` must be granted at pairing, and `pairing-admin` is never
granted. Since `workspace` is already RCE, the rest are incremental, but the
UI should show that pairing a CLI is equivalent to giving that machine a shell
on this host. DarkFactory already treats full scope as workspace access; keep
the label blunt.

**W-6. The hook token is not secret from the agent.**
An agent can read `ORCA_AGENT_HOOK_TOKEN` and post arbitrary status. The code
has many “authority” fences around status, which is the right mitigation, but
it is still a large, subtle surface. If DarkFactory adds agent hooks, keep them
strictly presentational and never let a hook row decide a grant, a deletion, or
a policy.

**W-7. No agent containment.**
This is the headline. Same UID, same network, real `HOME` unless the agent CLI
implements its own sandbox (Codex does; others vary). Worktrees isolate Git
state, not secrets. Any security claim that suggests otherwise is wrong.

**W-8. Linux secret storage may be obfuscation.**
`basic_text` is not encryption. Orca reports it, which is good, but the
underlying protection is weak. DarkFactory should either require a real
keyring for secret-bearing features or report the same gap.

**W-9. Token comparisons are not constant-time.**
`request.authToken === this.authToken` and the hook header comparison are
ordinary string equality. With 256-bit random tokens the timing signal is not
practically exploitable over a local socket, but `crypto.timingSafeEqual` is
free. DarkFactory's `secureEqual` already does this; keep using it.

**W-10. The attack surface is very large.**
Hundreds of RPC methods, integrations for dozens of agents, a browser with
extensions, plugins, SSH, cloud relays, and orchestration. Each addition
creates another path that must be classified into a permission and a scope.
DarkFactory should prefer fewer, coarser boundaries over a large custom
permission lattice.

## 5. DarkFactory vs Orca

| Boundary | Orca | DarkFactory today | Action |
|---|---|---|---|
| Local control | `0600` Unix socket + owner token | `0600` Unix socket, **no token** | Add a per-process token and a session-scoped capability; keep it labelled same-UID. |
| Remote devices | Per-device token, scope/grants, revocation | Per-device hashed session, `full`/`read`, device keys, E2EE | Add per-method permissions and pairing-time grants. |
| Network exposure | Loopback default; source classes; public refusal | Loopback default; `--allow-remote` + `--insecure-remote`; host allowlist | Add source classification and pre-auth caps on non-loopback. |
| Method authorization | 8 declared permissions, transport-stamped scope | Gateway verbs + ledger, only `full`/`read` scopes | Reuse the gateway to enforce a small permission set. |
| Agent credential | Session/spawn token for identity; owner token not given to the agent | Pane capability exists; sessions inherit env | Never give the master token to an agent; extend the pane/session capability. |
| Renderer | Sandbox, context isolation, no Node, CSP, pinned guests | CSP, origin checks, no remote UI by default | Keep; add the same allowlist discipline if we embed guests. |
| Secrets at rest | `0600`/ACL + OS keyring + gap report | `0600` + atomic writes | Add the gap report; hash device bearer tokens. |
| Agent containment | None (same UID, network allowed) | Opt-in `jail` scrub/mask; still same UID | Stop growing it; label it privacy, not containment. |

## 6. What this means for the recent DarkFactory work

The opt-in `jailScrubEnv`, `jailHideCredentials`, `jailEnvAllow`,
`jailPathAllow`, and `jailHiddenPaths` are reasonable **data-exposure
reduction** for a cooperative agent that follows a malicious instruction. They
are not a boundary against an agent that is trying, because the agent is the
same UID and can read anything the mask list missed. Orca's code makes the same
bet, but with fewer moving parts: it does not offer a deny list at all, and it
tells the user that `workspace` is shell access.

Recommendation:

- Keep the static options. They are already implemented, tested, and useful
  against accidental exposure.
- Stop calling them containment. Use “privacy filter” or “accident reduction”
  in README, `DESIGN.md`, `status`, and the UI. `strict` remains the only
  containment target.
- **Do not build the agent-request grant API now.** It adds an approval UI, a
  session capability, an audit path, expiry, and apply semantics for env/path
  grants — a second security product — while the same-UID agent can bypass it
  with `find`, `env`, or a direct socket connection. If we later need it, use
  Orca's shape: session-scoped capability, human approval on a full-scope
  device, never self-approval, exact names/paths, expiry.
- **Do not build Landlock/minimal-rootfs now** unless the threat model changes
  to “hostile agent.” If it does, do it as `strict`: separate principal or
  container, no network, minimal mounts, and a test that the session cannot
  read the token, `state.db`, `~/.ssh`, or the OpenCode credential. A pile of
  flags on `jail` will not get there.

## 7. Recommended next work, in order

### P0 — close the cheap control-plane gaps

1. **Give the daemon a session-scoped host capability.** When the daemon
   creates a session, the host mints a capability bound to that session and
   passes it to the session (env or a projected file). A pane/agent can act on
   its own session, not on every session. We already have a native pane
   capability; reuse the idea for host control. **This is the item with real
   security value.**
2. **Add a small method-permission map to the gateway.** Start with
   `workspace`, `terminal`, `settings`, `devices`, `host-admin`, and
   `device-self`. Keep `full`/`read` for compatibility, but make the handler
   declare what it needs. A read-scope device still cannot do anything new.
3. **Source-classify a non-loopback listener.** Admit loopback, LAN, and
   Tailscale; refuse public unless explicitly allowed; add per-source pre-auth
   caps and a failed-bearer throttle. This is defense in depth on top of the
   existing `--allow-remote`/`--insecure-remote` gate.

**Landed in the working tree (2026-11-07), not deployed.** Item 2 is a narrow
first slice: the gateway stamps a transport-derived `Local` flag and puts
`pair.code`, `device.revoke`, and `devices.revoke_all` in a `host-credential`
tier (`403 host_admin` for a remote paired device). `device.key.create` stays
workspace so a remote native client can finish first login. Item 3 is in:
`internal/httpapi/source.go` classifies the TCP peer (loopback / private /
tailnet / public), treats a globally addressed on-link LAN peer as private,
ignores forwarding headers, and refuses a public peer before routing
(`403 source_denied`) unless `allowPublicSources`/`--allow-public`. The default
loopback bind is untouched. Item 1 (session-scoped host capability) remains
deferred for the reattach/orphan risk described below; per-source
pre-auth connection caps and a failed-bearer throttle are not yet implemented.

### Host auth: optional hygiene, not a boundary

A per-host token on the session socket sounds like the obvious first fix, but
it is the weakest of the four and the easiest to get wrong operationally.

| Adversary | Host token | Session capability |
|---|---|---|
| Same-UID rogue agent | No — it reads the `0600` token | No — but it cannot use another session's capability |
| Another local user | Already blocked by `0600` | Already blocked |
| Accidental script that knows the socket path | Yes, if it cannot find the token | Yes for cross-session actions |
| Sandboxed local app with socket access but no file reads | Maybe | Maybe |
| Browser / remote peer | No — cannot reach a Unix socket | No |
| One agent reading or typing into another session | No | **Yes** |

So a host token is accident prevention and hygiene. It does not stop a
same-UID agent, which is the threat we were worried about. It also has a real
UX/reliability cost if added alone:

- the token must survive a daemon restart, or every session becomes orphaned;
- a new host with auth and an old daemon (or the reverse) cannot attach, which
  is exactly the three-generation mismatch that has burned us before;
- both the desktop and `serve` may attach to the same host, so both need to
  read the same token;
- if passed only by inherited fd, a later daemon cannot reattach; if persisted,
  it is readable by the same-UID agent anyway.

If we do it, do it with the capability work, not before it: host writes the
token atomically before listening, daemon re-reads it on every attempt, the
token persists across host restarts, and the protocol carries a version so the
old/new generation can refuse or dual-accept during an upgrade. No user-visible
prompt; the only UX risk is a failed reattach, so test that path explicitly.
**Recommendation: fold it into item 1 or skip it; do not make it the headline
fix.**

### P1 — credential hygiene

5. Hash device bearer tokens at rest (or finish the move to device keys) and
   keep revocation checks per request.
6. Add an `Origin`/`Host` check to any WebSocket transport.
7. Report secret-protection gaps (no keyring, Linux `basic_text`) in `status`
   and the UI.
8. Make pairing a clearly labelled full-workspace/RCE grant; show grants at
   pairing and never let a device widen them.

### P2 — honesty and simplification

9. Replace “jail contains” language with “jail filters data it would otherwise
   inherit; same UID; network allowed.”
10. Add a `strict` mode that **fails closed** when requested but unsupported,
    instead of silently falling back. Until it exists, `strict` should refuse
    to start a session.
11. Delete the deferred agent-request API from the roadmap until the substrate
    has an actual separate principal.

### Deferred — real containment

Only when the product requirement is “a prompt-injected agent must not read
the owner's secrets,” not “an agent should not accidentally see them”:

- separate UID per session with worktree ownership/ACLs, or
- a rootless container/microVM with a minimal rootfs, no host network, and a
  read-only bind of system toolchains.

Acceptance is behavioral: a session cannot read the master token, `state.db`,
`policy.json`, `~/.ssh`, the OpenCode password, or the host socket; it cannot
reach loopback services; and it still runs the agent with the tools it needs.
Until that test passes, no label may say “contained.”

## 8. Verification plan for the recommendations

- **Host auth:** a fake agent process with the socket path but no token gets
  `401` on health, list, create, input, resize, and delete. The daemon still
  works with the token. `-race` on the host client/server.
- **Session capability:** session A's capability cannot list or input into
  session B; revocation invalidates it immediately.
- **Method permissions:** table-driven tests over every registered route:
  read-scope against a mutation, paired client without a grant, mobile
  allowlist miss, owner pass.
- **Network policy:** table tests for `127.0.0.1`, RFC1918, `100.64/10`,
  `fd7a:115c:a1e0::/48`, and a public address; a public peer is closed at
  accept before HTTP parsing; unpaired bursts are throttled.
- **Credential hygiene:** a device token row is not readable as a bearer after
  revocation; a pairing code expires; a reused pairing offer with different
  grants is refused.
- **Honesty:** `status --json` and the README both show the actual mode, the
  same-UID/network caveat, and the secret-protection gap.

## 9. Limits of this review

- No dependency or supply-chain audit; no `pnpm audit`, no SBOM.
- No runtime testing of Orca; no attempt to exploit it.
- No full read of the 100+ RPC handlers, the browser implementation, or the
  cloud relay. Findings are based on the trust-boundary code and its comments.
- Orca's own `SECURITY.md` is a disclosure policy, not a threat model; the
  threat model here is inferred from code and docs.

The useful conclusion is not “Orca is secure” or “Orca is insecure.” It is that
Orca spends its security budget on **who may call the app**, not on **what an
agent may read**. DarkFactory has the same product shape, so it should spend
its budget the same way — and stop treating a deny list as a boundary.
