# DarkFactory threat model

This document sets a security target and records current limits. It is not a
security certification. The release scope and verified evidence are in
[HANDOFF.md](../HANDOFF.md). The protocol findings and their dispositions are
in the [internal protocol review](reviews/e2ee-protocol-review-2026-11-02.md).

## Security objective

Protect a user's workspaces, credentials, sessions, and devices against
automated probing and determined attackers, including attackers using AI tools.
Assume attackers can study the source, repeatedly send crafted network traffic,
and place hostile text, terminal sequences, or instructions in a repository.
No finite test suite proves immunity to unknown vulnerabilities. This target is
not yet met for hostile workloads or direct Internet exposure.

## Assets and authority

The daemon can start processes and access files with the operator's OS
permissions. A full-scope paired client can control workspaces and PTYs. A
read-scope client has a narrower grant, but its available data can still be
sensitive. Protect device tokens, pairing codes, private keys, repository
contents, prompts, terminal output, audit records, and release signing keys.

The daemon, session host, browser or desktop shell, Android app, transport,
local filesystem, agent processes, and dependency supply chain are distinct
trust boundaries. A worktree separates Git files; it is not a security sandbox.

## Attackers and boundaries

| Attacker | Entry point | Security objective and present limit |
|---|---|---|
| Unauthenticated remote attacker | Network listener, pairing, E2EE envelope | Deny authority and bound resource use. The envelope body is capped, but there is no per-source rate limit. Do not expose the daemon directly to the Internet. |
| On-path attacker or untrusted proxy | Android transport | Protect encrypted traffic and verify host identity. Compare the fingerprint during first pairing. The protocol has deferred truncation and forged outer-error weaknesses. |
| Malicious or compromised paired device | Authenticated API | Enforce the device scope and record mutations. Full access is equivalent to powerful local user authority, not a safe untrusted role. |
| Hostile repository, terminal output, or prompt injection | Agent, PTY stream, UI | Avoid interpreting data as authority and prevent terminal escape abuse. Current worktrees do not isolate processes or prevent exfiltration. |
| Rogue agent or same-user process | Local files, processes, daemon credentials | Prevent access to unrelated secrets and daemon authority. This is not guaranteed today. Default containment is `off`; `jail` is opt-in, same-UID, permits network, and falls back to `off` if unavailable. |
| Compromised dependency or release channel | Build, updates, artifacts | Detect tampering and verify provenance. Vendoring and checksums help, but a checksum delivered with an artifact is not an independent signature. |
| Local filesystem thief | State database and backups | Limit secret exposure. The host private key is plaintext in `state.db`; static ECDH does not provide forward secrecy. Protect the device and backups. |

An attacker controlling the operator's OS account, the daemon binary, the
kernel, or an unlocked full-scope device can generally exercise that account's
authority. This project does not currently claim to contain that attacker.

## Operating assumptions

- Keep the daemon on loopback and use an authenticated private tunnel for
  remote access. A browser connection relies on the transport for encryption;
  the native Android envelope has additional limitations in the protocol review.
- Pair from a trusted device and compare the host fingerprint out of band.
- Treat full-scope devices, agent tools, shell commands, and repositories as
  trusted with the authority they receive. Use separately managed OS isolation
  and restricted credentials for untrusted code. Do not assume `jail` provides
  complete containment.
- Protect the state directory, Android device, backups, and release signing key.
  Revoke a device on the daemon when it is lost; local Forget alone is not
  revocation.

## Security work before stronger claims

These are acceptance targets, not completed features or a promise of a
release date. An owner must attach revision-specific evidence to each item.

1. Obtain an independent review of authentication, pairing, session scope,
   browser and Android boundaries, and the encrypted protocol. Resolve findings
   or document their owners and explicit risk decisions.
2. Bound unauthenticated concurrency and request cost. Test slow, large, and
   repeated requests without allowing unrelated sessions to starve.
3. Resolve the deferred protocol issues, including authenticated completion,
   authenticated error handling, frame limits, device lifecycle, and clock
   skew. Test downgrade, replay, interruption, and key replacement.
4. Design and independently test fail-closed OS isolation for untrusted
   agents. Test access to daemon credentials, unrelated files, sockets, and
   network under hostile same-user workloads. Do not describe the current jail
   as satisfying this target.
5. Fuzz network and terminal parsing boundaries and test hostile terminal
   output, clipboard, links, resource exhaustion, and prompt injection.
6. Review dependencies and release provenance, scan source and history for
   secrets, and establish vulnerability reporting and a patch process.
7. Repeat real-device Android and native desktop acceptance at the release
   revision. Record environmental constraints and unresolved failures.

The [publication checklist](publication-checklist.md) covers privacy, licensing,
and repository history. The [terminal evaluation plan](terminal-evaluation.md)
defines the evidence needed before changing terminal engines.
