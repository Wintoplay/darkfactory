# Dependency audit, 2026-11-12

This is a version and advisory check. No dependency pins, toolchains, APKs,
or running services were changed by this audit.

Release follow-up: the rc.10 candidate raises both Go module baselines to
1.27.4. Both module vulnerability scans then pass. The comparison below records
the earlier audit snapshot; the other suggested updates remain unapplied.

## Recommended updates

1. Go 1.27.3 to 1.27.4 in both modules and the build environment. Both module
   scans report GO-2026-6859, CVE-2026-97054, in the current standard library.
   The advisory concerns an HTTP/1 header race when a handler reads a limited
   request body concurrently with writing response headers. Static reachability
   is a reason to update, not proof that an attacker can trigger this app.
   [Advisory](https://pkg.go.dev/vuln/GO-2026-6859) and
   [release history](https://go.dev/doc/devel/release).
2. Gradle wrapper 9.9.0 to 9.9.1, including the published distribution checksum.
   It fixes three security issues. This changes the build tool, not targetSdk.
   [Release notes](https://docs.gradle.org/9.9.1/release-notes.html).
3. Bouncy Castle 1.86 to 1.87 after the crypto interoperability and live pairing
   checks. Maven Central lists 1.87 as its release, with metadata updated on
   November 11, and the vendor download page now links 1.87. Update the pinned
   artifact checksum and test helper versions together. The cached search
   snippet still described 1.86; current vendor and registry data supersede it.
   [Vendor downloads](https://www.bouncycastle.org/download/bouncy-castle-java/).

Other Go library and Playwright updates below are available maintenance
updates. Check compatibility before changing desktop indirect requirements.
No additional advisory was reported for those current packages by the scans.

## Pinned version comparison

| Component | Current | Latest stable | Assessment |
|---|---|---|---|
| [Gradle](https://services.gradle.org/versions/current) | 9.9.0 | 9.9.1 | Prioritize security update |
| [AGP](https://dl.google.com/dl/android/maven2/com/android/tools/build/gradle/maven-metadata.xml) | 9.5.1 | 9.5.1 | Current |
| [Kotlin](https://repo.maven.apache.org/maven2/org/jetbrains/kotlin/kotlin-gradle-plugin/maven-metadata.xml) | 2.4.21 | 2.4.21 | Current |
| [Bouncy Castle](https://repo.maven.apache.org/maven2/org/bouncycastle/bcprov-jdk18on/maven-metadata.xml) | 1.86 | 1.87 | Update with crypto gates |
| [Go](https://go.dev/dl/?mode=json) | go1.27.3 | go1.27.4 | Prioritize security update |
| [github.com/creack/pty](https://proxy.golang.org/github.com/creack/pty/@latest) | v1.1.24 | v1.1.24 | Current |
| [go.etcd.io/bbolt](https://proxy.golang.org/go.etcd.io/bbolt/@latest) | v1.5.0 | v1.5.0 | Current |
| [golang.org/x/sys](https://proxy.golang.org/golang.org/x/sys/@latest) | v0.49.0 | v0.50.0 | Available maintenance update |
| [github.com/wailsapp/wails/v3](https://proxy.golang.org/github.com/wailsapp/wails/v3/@latest) | v3.1.0 | v3.1.0 | Current |
| [github.com/coder/websocket](https://proxy.golang.org/github.com/coder/websocket/@latest) | v1.8.14 | v1.8.15 | Available maintenance update |
| [github.com/ebitengine/purego](https://proxy.golang.org/github.com/ebitengine/purego/@latest) | v0.11.1 | v0.11.3 | Available maintenance update |
| [github.com/go-ole/go-ole](https://proxy.golang.org/github.com/go-ole/go-ole/@latest) | v1.3.0 | v1.3.0 | Current |
| [github.com/godbus/dbus/v5](https://proxy.golang.org/github.com/godbus/dbus/v5/@latest) | v5.2.2 | v5.2.4 | Available maintenance update |
| [golang.org/x/term](https://proxy.golang.org/golang.org/x/term/@latest) | v0.46.0 | v0.48.0 | Available maintenance update |
| [golang.org/x/vuln](https://proxy.golang.org/golang.org/x/vuln/@latest) | v1.9.0 | v1.9.0 | Current |
| [@xterm/xterm](https://registry.npmjs.org/@xterm%2fxterm/latest) | 6.0.0 | 6.0.0 | Current |
| [@xterm/addon-fit](https://registry.npmjs.org/@xterm%2faddon-fit/latest) | 0.11.0 | 0.11.0 | Current |
| [@xterm/addon-webgl](https://registry.npmjs.org/@xterm%2faddon-webgl/latest) | 0.19.0 | 0.19.0 | Current |
| [playwright](https://registry.npmjs.org/playwright/latest) | 1.64.1 | 1.65.0 | Available maintenance update |
| [actions/checkout](https://api.github.com/repos/actions/checkout/releases/latest) | v7 | v7.0.1 | Already follows this major tag |
| [actions/setup-java](https://api.github.com/repos/actions/setup-java/releases/latest) | v6 | v6.2.0 | Already follows this major tag |
| [actions/setup-go](https://api.github.com/repos/actions/setup-go/releases/latest) | v7 | v7.0.0 | Already follows this major tag |
| [actions/upload-artifact](https://api.github.com/repos/actions/upload-artifact/releases/latest) | v7 | v7.0.2 | Already follows this major tag |
| [actions/attest](https://api.github.com/repos/actions/attest/releases/latest) | v4 | v4.2.2 | Already follows this major tag |
| [gradle/actions](https://api.github.com/repos/gradle/actions/releases/latest) | v6 | v6.4.1 | Already follows this major tag |

GitHub Actions use floating major tags such as `@v7`, so the patch/minor
release differences above do not require workflow version edits.

## Android SDK and JDK

- Installed Temurin 21.0.13+9 is current on the supported JDK 21 line.
  A JDK major upgrade is not required.
- AGP 9.5.1 and Kotlin 2.4.21 match current stable Maven metadata.
- The SDK stable channel offers the Android 17/API 37 family, including a
  `platforms;android-37.2` package, and Build-Tools 37.0.0. The app uses
  compileSdk/targetSdk 36 and AGP-default Build-Tools 36.0.0.
- Treat targetSdk 37 as a separate runtime migration. Keep 36 for the A55
  overlap-fix release until physical-phone acceptance. A higher target is not
  needed for the Go or Gradle security patches. The SDK update was not tested.

## Advisory checks and limits

- `python3 test/security/dependencies.py --android-runtime
  android/app/build/reports/security-runtime-dependencies.json` reports no known
  OSV advisories for eight pinned Java and npm packages.
- `/home/win/go/bin/govulncheck ./...` exits 3 in both the root and desktop
  modules, reporting the same Go standard-library advisory above and no others.
- Existing APK versionCode 163 already increases over published rc.9 code 162.
  The inset fix is not published; no new release tag was created.
- The version comparison covers manifest dependencies, desktop indirect
  dependencies, vendored xterm packages, Playwright, Actions, and the Go
  vulnerability scanner. QRCode is a vendored adaptation without a pinned
  package version, so it has no registry version comparison here.
- This is not a full vulnerability audit, compatibility run, or inspection of
  the operator stack. GitHub alert state was not queried.

Registry snapshots and the rerunnable check are in
`test/artifacts/dependency-audit-2026-11-12/` (ignored). The version script only
reads public metadata and writes its snapshots under `/tmp`.
