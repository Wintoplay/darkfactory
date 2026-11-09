# Releasing

A release is one tag. `git tag v0.1.0 && git push origin v0.1.0` runs
`.github/workflows/release.yml`, which calls `make dist`, verifies the checksums,
and uploads the artifacts to the GitHub release for that tag. Nothing is built on
a workstation and uploaded by hand, so the artifacts always come from a commit
CI checked. A hyphenated tag such as `v0.1.0-rc.1` creates a **prerelease**;
GitHub's `/releases/latest` ignores it, so the default `install.sh` path keeps
pointing at the last stable release.

## What a release contains

| File | What it is |
|---|---|
| `darkfactory_linux_amd64.tar.gz` | binary + README + systemd unit + desktop entry + icon |
| `darkfactory_linux_arm64.tar.gz` | the same for arm64 |
| `darkfactory_<version>_amd64.deb` | Debian/Ubuntu package, amd64 |
| `darkfactory_<version>_arm64.deb` | Debian/Ubuntu package, arm64 |
| `SHA256SUMS` | one line per artifact; `install.sh` checks it before installing |

The tarball and package names are a contract: `install.sh` builds its download
URL from `<artifact base>/darkfactory_linux_<arch>.tar.gz`, and
`<artifact base>/SHA256SUMS`. Renaming either breaks every install.

## Build one locally

```bash
make dist VERSION=0.1.0
cd dist && sha256sum -c SHA256SUMS
```

`make dist` is the whole release recipe: two cross-compiled static binaries, the
tarballs, the `.deb`s, and the checksums. The same target runs in CI, so a
release is reproducible on any Linux box with Go, `tar`, `sha256sum` and
`dpkg-deb`.

Two runs from the same commit produce byte-identical artifacts, which
`.github/workflows/ci.yml` checks on every push:

- `-trimpath -buildvcs=false`, so no build path or VCS stamp enters the binary;
- `SOURCE_DATE_EPOCH` pinned to the commit time for both `tar` and `dpkg-deb`;
- tar members sorted, owned by root, with one mtime;
- tar member modes pinned with `--mode`, and the `.deb` staging tree
  chmod-normalized, so the build is also reproducible across different umasks.
  Package C found the umask leaking into the artifacts before this was added.

A change that embeds a wall-clock time, a hostname, a build path, a file mode
or the current user will break that check, which is the point of having it.
CI's two builds share one runner, so also spot-check `umask 002` vs `umask 022`
when touching `make dist`.

Test the installer against local artifacts without publishing anything:

```bash
make dist VERSION=0.1.0
python3 -m http.server 8899 --directory dist &
DARKFACTORY_BASE_URL=http://127.0.0.1:8899 PREFIX=/tmp/df-test ./install.sh --dry-run
DARKFACTORY_BASE_URL=http://127.0.0.1:8899 PREFIX=/tmp/df-test ./install.sh
./install.sh --uninstall --prefix /tmp/df-test
```

## Android APK

The release workflow builds only the Go daemon. The Android client is a separate
Gradle project, and its signed release APK is currently built on a workstation
and attached to the GitHub release by hand:

```sh
./android/gradlew -p android assembleRelease
# android/app/build/outputs/apk/release/app-release.apk
```

Signing reads `android/keystore.properties`, which is gitignored and points at a
keystore outside the repository (for the first prerelease,
`~/.config/darkfactory/android-release.jks`). Back up both; the APK signature is
the update identity, so a lost key means uninstalling before the next install.
Attach the APK and its `.sha256` file to the release. CI signing needs the
keystore as a repository secret and an Android SDK job, and is not implemented.

## What is and is not signed

`SHA256SUMS` proves the download arrived intact; it does not prove who built it,
because it is served from the same place as the artifacts. Anyone who can
publish a release can publish a matching checksum.

The workflow now creates a [GitHub artifact attestation][attest] for every
tarball, `.deb` and `SHA256SUMS`. The action mints a short-lived Sigstore
certificate from the workflow's OIDC identity and stores the signed SLSA build
provenance on this repository. That binds the artifact digest to the workflow,
commit, and repository that produced it, independently of the release page.

Verify a downloaded artifact with the GitHub CLI:

```sh
gh attestation verify darkfactory_linux_amd64.tar.gz --repo Wintoplay/darkfactory
```

The Android APK is signed with the project's release key but is not attested:
it is built on a maintainer workstation, not in Actions. Verify it against the
certificate fingerprint published in the release notes, or build it yourself.

[attest]: https://docs.github.com/en/actions/security-guides/using-artifact-attestations-to-establish-provenance-for-builds


## Packaging notes

- `packaging/systemd/darkfactory.service` is a **user** unit; the `.deb` installs
  it to `/usr/lib/systemd/user/`. The tarball ships it under `share/`, and
  `install.sh --desktop` copies it to `~/.config/systemd/user/` with `ExecStart`
  rewritten to wherever the binary landed.
- `packaging/darkfactory.desktop` runs `darkfactory launch --desktop`, so the
  icon works whether or not the daemon is already running. `--desktop` uses
  `darkfactory-desktop` when that optional shell is installed beside the daemon
  or on `PATH`, and falls back to the browser when it is not.
- `packaging/debian/control.in` is a template: `@VERSION@`, `@ARCH@`,
  `@MAINTAINER@` and `@INSTALLED@` are filled by `make dist`.
- Windows is not packaged: `internal/session` needs a POSIX PTY and does not
  build there. macOS compiles in CI but ships no artifact yet.
