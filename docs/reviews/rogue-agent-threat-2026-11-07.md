# Rogue-agent threat model — 2026-11-07

This is an internal source review, not a live exploit and not an external audit.
It assumes the question the owner actually asked: **any process or device on the
Tailscale network, and any agent launched by DarkFactory, may be hostile.** It
does not assume the local user, the tailnet, or a session is trusted.

Read this with [the threat model](../threat-model.md), [the security analysis
of 2026-11-03](security-analysis-2026-11-03.md), and [the E2EE protocol
review](e2ee-protocol-review-2026-11-02.md). Where they disagree, the code
citations here win.

## Verdict

The remote boundary is the part that has been reviewed. The **local** boundary
is the weak one:

- A session launched by DarkFactory runs as the same UID as the daemon by
  default (`containment: "off"`). It can read the master token, the instance
  secret, the state database, the policy file, and the OpenCode service
  password.
- The detached session host exposes an **intentionally unauthenticated** Unix
  socket to every same-UID process. That socket can list, stream, type into,
  create, resize, and delete **other agents' PTYs**.
- The jail is not a security boundary. It mounts the host filesystem read-only,
  keeps the host network namespace, inherits the daemon environment, and masks
  only the DarkFactory config and state directories.
- A full-scope paired device can mint another pairing code without any host
  re-authentication, so a stolen device token can survive revocation of the
  original device.

Consequences under the stated assumption:

- **Hijack agents:** yes. The host socket can inject input into any PTY, and the
  shared OpenCode password can prompt, interrupt, and answer permissions for
  every OpenCode chat.
- **Command agents:** yes. A same-UID session can start an arbitrary command
  through the host socket, bypassing the daemon's gateway, policy, and ledger.
- **Extract data:** yes. A same-UID session can read the token, state DB, host
  private key, OpenCode transcripts, and (with the jail) the rest of the home
  directory. A full-scope device can read state, audit, devices, filesystem
  paths, and every chat.
- **Persist:** yes. A same-UID process can edit the policy, replace a user-owned
  install, kill or restart the daemon, and use a stolen full token to enroll a
  new device.

This is not a new discovery so much as the consequence of the documented
assumption that same-user processes are not contained
(`threat-model.md:37-43`). That assumption is incompatible with "assume any
rogue agent may interact."

## Landed in this tree (2026-11-07)

Opt-in, additive, and not deployed:

- `jailScrubEnv` — a jailed session gets a minimal environment instead of
  `os.Environ()`. It keeps `PATH`, locale, `TERM`, the projected
  `HOME`/`TMPDIR`, the preset `env`, and only `jailEnvAllow` names. It drops
  desktop, D-Bus, SSH-agent, keyring, shell-hook and cloud variables.
- `jailHideCredentials` — adds the built-in `CredentialHiddenPaths` mask list
  (`~/.ssh`, `~/.aws`, `~/.config/opencode`, keyrings, browser profiles, cloud
  config, package credentials).
- `jailEnvAllow` and `jailPathAllow` — global and per-agent explicit grants.
  `jailPathAllow` exempts exact paths from the mask list; an allow inside a
  masked parent is refused at session start rather than silently left hidden.
- `jailHiddenPaths` — extra operator-chosen masks.
- If `bwrap` is unavailable, a session with `jailScrubEnv` fails to start
  instead of falling back to `off` unscrubbed.

What this does **not** fix: the default `off` mode still gives a same-UID agent
the master token, the state DB, and the host socket; `jail` still runs as the
same uid and still has network access; the host socket remains an unauthenticated
same-UID control plane; a full-scope device can still mint another device. R-1,
R-3, R-6, and R-9 remain open until the separate-principal `strict` work lands.
This is a data-exposure reduction for a jail, not a rogue-agent boundary.

## Allow design: static grants now, session requests later

The user needs a way to say "this agent may use this" without disabling the
jail. There are two layers, and they should not be confused:

### Static grants (landed)

```json
{
  "containment": "jail",
  "jailScrubEnv": true,
  "jailHideCredentials": true,
  "jailEnvAllow": ["SSH_AUTH_SOCK"],
  "jailHiddenPaths": ["~/.secrets"],
  "agents": {
    "opencode": {
      "command": "opencode",
      "env": { "OPENAI_API_KEY": "..." },
      "jailEnvAllow": ["GH_TOKEN"],
      "jailPathAllow": ["~/.config/opencode"]
    }
  }
}
```

- `env` carries values the user typed into config. `/state` exposes key names,
  never values (GV-16).
- `jailEnvAllow` copies named values from the daemon environment. It is not a
  place to put secrets; use `env` for those.
- `jailPathAllow` is an exact-path exemption, global or per agent. It cannot
  punch through a masked parent; the session refuses to start instead.
- All of this is visible in the config file, so the owner can review it before
  starting an agent. It has no approval UI because it is a pre-authorization.

### Agent-requested grants (not implemented)

An agent should not need the master token to ask. The shape:

1. At session start, the daemon mints a **session capability** (random, hashed
   at rest, scoped to that session, expires with it) and passes it to the
   session through the projected HOME or an explicit env name. It is not the
   owner token and cannot call the owner API.
2. The agent runs `darkfactory ask env SSH_AUTH_SOCK --reason "git push"` or
   `darkfactory ask path ~/.ssh/known_hosts --read --reason "host key"`.
   The CLI posts to a session-scoped endpoint, not the host control socket.
3. The daemon records a `session.request` ledger row and raises an attention
   event: "Session X asks for SSH_AUTH_SOCK — allow once / allow for session /
   deny." The user must approve on a full-scope device; a read-scope device
   cannot approve grants. Requests are rate-limited and expire.
4. Applying a grant is the hard part:
   - **env**: return the value once after approval, or write it to a `0600`
     file under the session HOME. The agent now has it; that is the grant.
   - **path**: a broker outside the jail opens the file and passes a read-only
     file descriptor over the session socket (SCM_RIGHTS), or the session is
     restarted with the path bound. A live bind mount is not portable.
   - **network**: a per-session egress proxy/allowlist; not a post-hoc change
     to an existing network namespace.
5. Grants are session-scoped, revocable from the UI, and expire. Never allow a
   wildcard name or path; show the exact string and the reason.

Do not let the agent self-approve. Do not accept a grant from a prompt, a
repository file, or the agent's own output. The approval belongs to the human
principal, and it must be a separate UI event from the agent's request.

**Status (2026-11-07): deferred.** The static grants above are landed. The
request API is not, and should not be the next work. The same-UID agent can
bypass any approved grant with `find`, `env`, or a direct socket; the approval
UI would be a second security product without a boundary underneath it. See
[`orca-security-review-2026-11-07.md`](orca-security-review-2026-11-07.md)
for the comparison and the recommended order: authenticate the control plane,
add method permissions and network source policy, and reserve `strict` for the
hostile-agent requirement.

## Adversaries

| ID | Adversary | Capabilities | Present boundary |
|---|---|---|---|
| A | Unpaired tailnet peer | TCP/HTTP to the listener or proxy, scanning, replay, DoS | App auth, rate limits, no remote UI |
| B | Paired device | Its own token; `read` or `full` scope | Scope checks, gateway, ledger |
| C | Agent launched by DarkFactory | Shell in a workspace, same UID by default, network | None beyond the optional jail |
| D | Same-host process, other UID | Local TCP, filesystem names | File modes, socket mode |
| E | Compromised tailnet control plane / subnet router | Can add or route nodes; sees metadata | WireGuard, app E2EE for native clients |
| F | Supply chain / update channel | Can alter binary, policy, dependencies | Vendoring, checksums, no signatures |

The rest of this document concentrates on **C**, because it is the adversary the
current model does not contain, and on **B** and **A** where they compound it.

## Findings

### R-1 (Critical) — the session host is an unauthenticated same-UID control plane

`internal/session/host.go:56-58` says the handler is "intentionally
unauthenticated: the socket is 0600 and only the same user can connect." The
socket is created at `internal/session/hostclient.go:29-31` under
`~/.local/state/darkfactory` and chmodded `0600`
(`cmd/darkfactory/main.go:763-770`). The directory is `0700`
(`internal/config/config.go:212-223`).

That protects against other users, not against a process running as the same
user. The host API includes:

| Route | Effect |
|---|---|
| `GET /v1/sessions` | Enumerate every session: id, pid, dir, agent, title, status |
| `GET /v1/sessions/{id}/stream` | Read the full PTY byte stream |
| `POST /v1/sessions/{id}/input` | Type into another agent's PTY |
| `POST /v1/sessions` | Start an arbitrary `Spec` (`Command`, `Args`, `Env`, `Dir`) |
| `DELETE /v1/sessions/{id}` | Stop another agent |
| `POST /v1/sessions/{id}/resize` | Change another client's terminal size |

See `internal/session/host.go:64-188`. `POST /v1/sessions` decodes the full
`Spec` and passes it to `manager.Start` without a principal, policy check, or
ledger row (`host.go:72-85`). `Spec` carries `Command`, `Args`, `Env`, and `Dir`
(`internal/session/session.go:35-55`).

**Impact:** a rogue agent started in default containment can read every other
agent's output, type prompts or commands into it, kill it, or start a new
process with arbitrary argv/env. It bypasses the daemon's resolve→decide→record→
act gateway entirely, so nothing appears in `darkfactory audit`.

**Why the 0600 socket does not help:** the attacker is the same UID. File mode is
not a principal.

### R-2 (Critical) — sessions inherit the daemon's full environment

`internal/session/session.go:185-193` builds the session environment with
`os.Environ()` and only overrides `HOME`/`TMPDIR` for a jail. The jail command
does not call `--clearenv` (`session.go:778-801`).

If the daemon is started from a desktop session or a user service, the inherited
environment can include:

- `SSH_AUTH_SOCK` — use the user's SSH agent to reach remote hosts
- `DBUS_SESSION_BUS_ADDRESS` — talk to the desktop, keyring, and Secret Service
- `DISPLAY`, `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR` — interact with the desktop
  session and user services
- `GPG_AGENT_INFO`, `KRB5CCNAME` — use signing/decryption or Kerberos tickets
- model and cloud credentials (`OPENAI_*`, `ANTHROPIC_*`, `AWS_*`, `GOOGLE_*`,
  `AZURE_*`) that were present when the daemon started

**Impact:** a rogue agent can use the user's existing authority without reading a
private key file. It can also reach the systemd user manager and other user
services. The per-worktree `HOME` is not a confidentiality boundary when the
absolute path and the inherited environment are still available.

### R-3 (Critical) — the master token and state are readable by any same-UID session

The master token is a file at `~/.config/darkfactory/token`, mode `0600`, in a
`0700` directory (`internal/config/config.go:192-223, 445`). The instance secret
is beside it. `state.db` is `0600`. The bearer token is full scope and terminal
authority (`internal/httpapi/server.go:776-780`).

A same-UID agent can:

- read the token and call every `/api/v1` route as the owner
- mint a pairing code and enroll another device
- read the state DB, including the host private key (plaintext, per
  `threat-model.md:39`), browser hashes, ledger, and session metadata
- edit `policy.json` and `config.json`, kill the daemon, and start it again
- replace a user-owned install path (for example `~/.local/bin/darkfactory`)

**Impact:** total DarkFactory compromise, including persistence. The token is the
difference between "an agent with shell access" and "the owner."

### R-4 (High) — the OpenCode password is shared and readable from a session

DarkFactory reads the OpenCode service password from
`opencodePasswordFile` (default `~/.config/opencode/service-local-install.json`;
`internal/config/config.go:376`, `internal/httpapi/opencode.go:141-142`). The
jail masks only the DarkFactory config and state directories
(`internal/session/session.go:791-798`), not `~/.config/opencode`.

Network is allowed in the jail (`threat-model.md:37`), and OpenCode listens on
loopback. A jailed agent can therefore read the password and call OpenCode
directly:

- list sessions and read messages
- prompt or interrupt any chat
- answer permission requests
- create chats

**Impact:** agent hijack and data extraction that bypasses DarkFactory's proxy
and audit. The "the password never reaches the browser" property is true, but it
does reach every same-UID process.

### R-5 (High) — the jail is not a security boundary

`jailCommand` uses `--ro-bind / /`, a new `/dev`, a new `/proc`, a tmpfs `/tmp`,
an optional per-worktree `HOME`, and tmpfs masks for `HiddenPaths`
(`internal/session/session.go:778-801`). It does **not**:

- clear the environment
- unshare the network namespace
- mask `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/opencode`, browser profiles,
  keyrings, cloud config, or shell history
- change the UID
- apply seccomp or cgroup limits
- make the host filesystem unreadable

**Impact:** a jailed agent can read most of the user's home directory, reach
every loopback service (including the daemon and OpenCode), and exfiltrate over
the network. It cannot see host PIDs or write outside the worktree, which is
useful, but it is not containment for secrets or agent control.

### R-6 (High) — a full-scope device can mint more devices without host re-auth

`handlePairCode` accepts a caller-chosen `scope` and defaults to `full`
(`internal/httpapi/server.go:1357-1397`). The route is a mutation, so a
read-scope principal is blocked by the generic read-only guard
(`server.go:696-699`); a full-scope device can call it. The default policy
allows `human*` every verb (`README.md:281-311`).

**Impact:** a stolen full token can enroll a new full-scope device. Revoking the
stolen device does not revoke the new one; only "revoke all" does. There is no
desktop confirmation or step-up authentication for minting a code.

### R-7 (High) — an unpaired tailnet peer still has a real attack surface

Unauthenticated routes are `/healthz`, `/api/v1/e2ee/host-key`, `/api/v1/pair`,
`/api/v1/e2ee`, and `/api/v1/device-keys/challenge|verify`
(`internal/httpapi/server.go:477-489, 539`). Rate limits exist: pairing 5/min per
source and 30/min global; E2EE 1,200/min per source and 3,600/min global; device
auth 20/min per source and 120/min global (`server.go:57-73`).

The remaining gaps are the ones in the public-exposure answer:

- no TLS listener (`cmd/darkfactory/main.go:925-945`)
- `isSecureRequest` trusts `X-Forwarded-Proto` (`server.go:1655-1657`)
- H-2/H-3/H-5/H-7 remain open (`security-analysis-2026-11-03.md:303-305`)
- no connection or concurrent-stream cap
- a flat tailnet ACL is treated as if it were app authentication

**Impact:** DoS, protocol attacks, information leaks, and a pairing-code
brute-force attempt that is bounded but not eliminated. Without TLS and trusted
proxy configuration, a direct or loopback-proxied peer can also influence the
"secure request" decision.

### R-8 (Medium) — read scope is fleet-wide and leaks more than its name

A read-scope device can enumerate directories (`GET /api/v1/fs/dirs`), read the
whole audit ledger (`/api/v1/audit`), list every device (`/api/v1/devices`), and
read every OpenCode chat and message
(`security-analysis-2026-11-03.md:190-206`). It cannot mutate or use a PTY, but
the data is still sensitive.

### R-9 (Medium) — same-UID signal, kill, and persistence

A same-UID process can signal the daemon, the session host, and every session;
write to a user-owned install path; edit policy and config; and replace the
binary before a restart. The daemon has no signed/immutable install and no
self-integrity check. This is a persistence path, not just a DoS.

### R-10 (Medium) — Tailscale is transport and network identity, not application auth

A compromised tailnet node is an authenticated network peer. On a flat tailnet,
it can reach any listener or proxy that the ACL allows. DarkFactory still
requires its own token/pairing, which is correct, but the operational guidance
must not imply that tailnet membership is an authorization boundary. Use tagged
devices, least-privilege ACLs, and treat every tailnet peer as unauthenticated.

### R-11 (Low/Medium) — the host control plane is outside the audit ledger

Daemon mutations go through the gateway and ledger. Host socket operations do
not. A session-to-session read or input leaves no `darkfactory audit` row,
because the host has no principal to record. That makes R-1 invisible after the
fact.

## What is already solid

These protect the API and remote boundary and should not regress:

- one `auth` → `mutate` → handler order and a hash-chained ledger
- deny-before-allow policy with a fail-closed parse
- one-time, hashed, short-lived open/pair codes
- `__Host-` `HttpOnly` `Secure` `SameSite=Strict` cookies with hashed rows
- constant-time master-token comparison
- remote browser UI refused on loopback-only checks
- E2EE with pinned host key, durable replay records, and device binding
- session-scoped pane capabilities
- argv-only child processes and text-node-only DOM rendering
- hostile terminal-output filtering and a CSP

None of these distinguish two processes running as the same user.

## Remediation

The ordering below is the **hostile-agent** programme: it assumes the product
must survive an agent that is actively trying to read the owner's secrets. It
is not the smallest next step for the current product. See the Orca review for
the smaller control-plane programme, which should land first. Do not claim
rogue-agent resistance until the P0 items here pass.

### P0 — only if hostile agents are in scope

1. **Give each session a different security principal.** Run agent sessions as a
   distinct UID (rootless podman/container, systemd `DynamicUser`, or a
   user-namespace mapping with subuid ranges), with the daemon in its own UID.
   Same-UID file modes cannot express a boundary.
2. **Separate the daemon control socket from the session attach socket.** The
   daemon-only control socket must require a capability that is not readable from
   the session (for example a random secret passed over an inherited fd at
   startup). The attach socket should be per-session and capability-scoped, like
   the existing native pane capability.
3. **Stop inheriting the daemon environment.** Build a minimal allowlist
   (`PATH`, `LANG`, `TERM`, `TMPDIR`, `HOME`) and pass only the secrets the
   intended agent needs. Add `--clearenv` to the bwrap command. (The opt-in
   `jailScrubEnv` already does the first half; it is not a boundary without
   items 1–2.)
4. **Mask secrets in the jail:** `~/.ssh`, `~/.aws`, `~/.config/opencode`,
   `~/.gnupg`, `~/.local/share/keyrings`, browser profiles, cloud config, shell
   history, and the DarkFactory config/state (already masked). A deny list is
   still not containment; item 1 is.
5. **Move the master token out of a same-UID-readable file.** Use a kernel
   keyring or a daemon-only UID. Issue short-lived per-session credentials for
   local clients instead of sharing the owner token.
6. **Give OpenCode per-session credentials or a per-session instance.** Do not
   share one service password across every workspace.

### P1 — remote and paired-device hardening

For the current product this is the high-value work; the Orca review recommends
doing the control-plane and source-policy items before any strict-containment
work.

7. Require step-up authentication (WebAuthn/device-key signature) and a desktop
   confirmation to mint a pairing code; bind the code to a scope and label;
   alert on new devices; make "revoke all" the obvious response.
8. Narrow read scope: no `/fs/dirs`, `/audit`, or `/devices`; scope OpenCode
   reads to one worktree.
9. Add TLS and a trusted-proxy allowlist; refuse `Forwarded`/`X-Forwarded-*`
   from anyone else; never run `--insecure-remote` in production.
10. Add connection, per-principal, and concurrent-stream caps; add a semaphore
    around ECDH/AEAD work; bound the replay table.
11. Land E2EE v2: authenticated terminator, frame ceiling, server time, uniform
    errors, re-pair revocation, and ephemeral keys for forward secrecy.
12. Audit host operations and session-to-session access; log peer credentials
    and alert on unexpected access.

**Landed in the working tree (2026-11-07), not deployed:** item 7's
control-plane half (`pair.code`, `device.revoke`, `devices.revoke_all` are
host-credential verbs; a remote full device gets `403 host_admin`) and item 9's
source half (a non-loopback listener refuses a direct public TCP peer with
`403 source_denied`, and forwarding headers never change the class). Step-up
authentication, read-scope narrowing, connection/stream caps, E2EE v2, and
host-operation audit remain open.

### P2 — platform and supply chain

13. Run the daemon as a dedicated system user with systemd hardening
    (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `PrivateDevices`,
    `RestrictAddressFamilies`, `SystemCallFilter`, `MemoryMax`, `TasksMax`).
14. Make the install path read-only to the session user; sign releases and add
    an explicit signed update path with rollback.
15. Publish the threat model honestly, add `SECURITY.md`, and obtain an external
    review before using "secure" in public.
16. Define and document support tiers: Linux host; macOS build-only; Windows out
    of scope until ConPTY exists.

## Acceptance tests to add

These are the tests that would have made the current gaps visible:

1. `TestAgentCannotOpenHostSocket` — an isolated session dials
   `session-host.sock`; expect refusal, not a session list.
2. `TestAgentCannotReadDaemonSecrets` — token, instance secret, state DB,
   policy, and OpenCode password are unreadable.
3. `TestAgentEnvironmentIsMinimal` — `DISPLAY`, `WAYLAND_DISPLAY`,
   `DBUS_SESSION_BUS_ADDRESS`, `SSH_AUTH_SOCK`, `XDG_RUNTIME_DIR`, and cloud
   variables are absent unless explicitly passed.
4. `TestAgentCannotReachLoopbackServices` — the daemon and OpenCode are
   unreachable from the session network namespace.
5. `TestHostControlSocketRequiresCapability` — an unauthenticated request is
   refused; a wrong capability is refused.
6. `TestReadScopeCannotMintFullPairCode` — read scope is refused by the mutation
   guard, and a full-scope mint is audited.
7. `TestPairCodeRequiresStepUp` — once implemented, a full token alone is not
   enough.
8. `TestSessionCannotStreamOrTypeIntoAnotherSession` — per-session capability
   isolation.
9. `TestRemotePeerCannotSpoofProxyHeaders` — direct and proxied requests are
   distinguished by the trusted-proxy allowlist.
10. `TestInternetProfileRejectsPlaintext` — a non-loopback bind without TLS or a
    trusted proxy refuses startup.
11. Fuzz the host frame protocol and the E2EE frame decoder with the same rigor
    as `FuzzBrowserSnapshot`.
12. Ledger assertions: every host control operation and every pair-code mint
    produces a row.

## Residual risk

Even after the above:

- A same-UID process that can ptrace the daemon (Yama disabled, `CAP_SYS_PTRACE`,
  or a kernel bug) defeats user-space isolation.
- A compromised kernel, a compromised host root, or a malicious binary
  replacement defeats everything in this document.
- A user who grants `full` scope to a compromised device has granted the
  attacker the same authority.
- The E2EE protocol has not had an external review.
- Tailscale ACLs can reduce exposure but are not application authentication.

The honest statement today is: **DarkFactory is suitable for a trusted local user
and trusted agents on a private network. It is not suitable for untrusted agents
or a hostile tailnet, because the same-UID boundary does not exist.**
