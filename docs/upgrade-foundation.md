# Upgrade foundation

Status: design for a future, explicitly invoked release upgrade. The checkout
has `make update-source` for local builds, but no release updater, signed
metadata, automatic check, or rollback transaction is implemented here. The
current release and evidence are in [HANDOFF.md](../HANDOFF.md). This proposal
does not authorize a restart, installation, network request, or publication.

## Goal and boundaries

An operator can inspect a specific candidate, verify its publisher and
compatibility, then explicitly install it while preserving hosted PTYs. The
daemon makes no unsolicited update checks or outbound calls. A check and an
apply are separate commands. Checking never changes the installation or state.
Applying never silently changes the selected candidate.

The first implementation targets Linux daemon tarball installs. Debian
packages remain owned by the package manager; source installations remain owned
by the operator. Android APK upgrades use Android signing and are a separate
workflow. A Wails executable and the daemon must not be treated as one artifact
until their compatibility and packaging are defined.

## Trust and candidate identity

The current installer downloads a tarball and `SHA256SUMS` from the same
release. That detects corruption but does not authenticate the publisher.
It has no signed update metadata, rollback journal, or compatibility check.
The installer writes directly to the destination and is not the transaction
described below.

A future update manifest must bind an immutable version, artifact digest,
platform, release channel, signing identity, and compatibility requirements.
Verify a signature against a locally trusted key before accepting a candidate;
a key downloaded alongside the candidate cannot establish its own trust. The
operator needs an independent way to establish and rotate that key. Define
metadata expiration, monotonic version policy, key rotation, recovery from key
loss, and explicit downgrade authorization before selecting a signature format.
Replay, freeze, compromised release accounts, and a compromised signing key
need separate treatments; signatures alone do not solve all of them.

The check result identifies the installed executable and running daemon
separately. It shows the candidate version, digest, platform, origin, signature
result, compatibility, and intended destination. The apply step verifies the
same digest again and refuses if the installed identity, destination, trust
configuration, or compatibility assumptions changed since the check.

## Compatibility and transaction

Define versioned contracts for the daemon, detached session-host protocol,
stored state schema, browser assets, and Android protocol before advertising
automatic compatibility. Unknown compatibility must refuse the upgrade. An old
binary must not be restarted against a state schema it cannot read; a binary
rollback alone is not a database rollback. State backup and recovery need a
separate documented plan and must not overwrite live state.

The intended apply sequence is:

1. Acquire an installation-specific lock. Identify package ownership, target,
   running daemon, and session host. Refuse ambiguous or package-managed paths.
2. Download only on explicit request, or accept an operator-supplied artifact.
   Verify signed metadata, artifact digest, archive paths, file types, size
   limits, ownership, and platform before using any extracted file.
3. Stage on the target filesystem, set restrictive modes, and verify the staged
   executable. Persist the previous binary and a transaction record without
   copying secrets or session data into the installation directory.
4. Atomically replace the executable and durably record each step. A crash or
   repeated invocation must produce a diagnosable state and a safe recovery
   path. Preserve the previous executable until acceptance completes.
5. Restart only with explicit operator authorization. Stop the daemon using its
   normal detach path; never stop or replace the session host as a side effect.
   Confirm the running build, existing session IDs, attach behavior, and state
   health before declaring success.
6. If verification fails, report the exact phase and recovery instructions.
   Restore the previous binary only when state and host compatibility are known;
   otherwise preserve evidence and require operator recovery.

File replacement, daemon restart, and database migration are separate failure
boundaries. Do not claim a whole transaction is atomic because one rename is
atomic. Do not install or restart the operator's service to test this design.

## Work packages and gates

| Owner | Deliverable | Acceptance evidence |
|---|---|---|
| Release maintainer | Signed metadata, trusted-key lifecycle, version and compatibility policy | Independent review; tests for wrong key, tampering, replay, expiry, rotation, downgrade and key loss. |
| Daemon maintainer | Versioned host, API and state compatibility contract | Old/new interoperability and migration tests, including explicit refusal where rollback is unsafe. |
| Installer maintainer | Explicit check, stage, apply and recovery commands | Isolated-root tests for interruption at every phase, retries, concurrent invocations, malicious archives, permissions and disk-full failures. |
| Integration maintainer | Running-session acceptance | Real hosted PTYs survive upgrade, daemon restart, reconnect and supported rollback; session IDs and first stream bytes are checked. |
| Release maintainer | Distribution ownership and documentation | Package-managed installs are refused or delegated; release provenance, license notices and offline operation are verified. |

All checks use isolated temporary XDG roots and test binaries. Record the exact
old and new revisions, versions, artifact digests, commands, results, session IDs,
and recovery outcome. `make check` does not prove upgrade recovery.
