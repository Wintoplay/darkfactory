# Dependency policy

Status: current. Dependabot watches the manifests; the gates below still decide
whether a bump lands. Nothing is auto-merged.

## What Dependabot covers

| Ecosystem | Path | Notes |
|---|---|---|
| `gomod` | `/` | Vendored modules are maintained in `vendor/` as well. |
| `gomod` | `/desktop` | Separate Wails module. |
| `gradle` | `/android` | Bouncy Castle and the buildscript classpath. |
| `github-actions` | `/` | Action versions only. |

Minor and patch updates arrive as one grouped PR per ecosystem. Major Go and
desktop updates get their own PR. AGP and Kotlin Gradle plugin majors are
ignored on purpose (see below). Dependabot **alerts** and **security updates**
are enabled in the repository settings rather than in `dependabot.yml`.

## What Dependabot cannot see

These are pinned in files Dependabot does not parse, so they are manual:

- **Gradle wrapper** — `android/gradle/wrapper/gradle-wrapper.properties`
  (`distributionUrl`, `distributionSha256Sum`, `networkTimeout`).
- **AGP's minimum Build-Tools** — currently `36.0.0`; AGP refuses an older one.
- **Android SDK platform** — `compileSdk`/`targetSdk` 36 needs
  `platforms;android-36` installed locally and in CI.
- **JDK** — the build uses JDK 21; it is a build tool, not a shipped runtime.

## Gates

A dependency PR is ready when the checks relevant to it pass:

- Go modules: `go mod tidy && go mod vendor`, then `make check`. A `bbolt` bump
  also needs the store tests, because it touches on-disk state.
- Wails: `make desktop-check` and `make desktop-build`.
- Bouncy Castle: `make android-pq-check` and `make android-pq-live`; the pinned
  artifact SHA-256 in `android/app/build.gradle` must be updated with the
  version.
- Actions: a green run of `ci`, `protocol security`, `android` and `vuln`.
- Anything under `web/`: bump `UI_VERSION` and the `?v=` queries, then
  `make ui-gate` (see `docs/ui-gate.md`).

## Android toolchain refreshes

AGP, Gradle, Kotlin, `compileSdk`/`targetSdk`, Build-Tools and the JDK move
together and are not a Dependabot merge. A refresh means:

1. Read the AGP release notes for the required Gradle, Build-Tools, JDK and KGP
   versions, and for behavior changes tied to a `targetSdk` bump.
2. Update `android/build.gradle` (AGP and KGP), the wrapper URL and its
   published SHA-256, `compileSdk`/`targetSdk`, and the documented Build-Tools.
3. `./android/gradlew -p android --warning-mode all lintDebug assembleDebug
   assembleDebugAndroidTest`; fix deprecations before they become Gradle 10
   errors.
4. `make android-build`, `make android-pq-check`, `make android-pq-live`, then
   install on the emulator and, for a `targetSdk` change, on a physical phone.
   A `targetSdk` bump changes runtime behavior (back gestures, edge-to-edge,
   foreground services) and the old evidence no longer covers it.
5. Record the versions and what was actually exercised in `HANDOFF.md`.

Keep the JDK on 21 unless the new Gradle/AGP needs a newer one: the app ships
bytecode for Java 17 and the JDK is not part of the release.
