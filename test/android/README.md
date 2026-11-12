# Android security checks

`check-pq.sh` compiles the production Java crypto and wire classes on a JVM,
checks their HKDF, X25519, ML-KEM-768 and bundle results against Go, and runs
negative checks for stored pins, suite replacement, bundle epochs, encrypted
response terminators, frame lengths and aggregate notification limits.

`check-e2ee-live.sh` builds a disposable daemon under isolated XDG directories.
It verifies a v2 host bundle, sends a deliberately modified ephemeral key with
the staged sealed code, confirms that the code remains reusable, then pairs
successfully, opens `/api/v1/state`, and checks downgrade and replay refusals.
The cleanup stops the isolated session host and daemon. Neither script touches
the operator service or requires a phone.

Both scripts use the test-time artifact `org.bouncycastle:bcprov-jdk18on:1.86`
for the same lightweight APIs used by the APK. `get-bcprov.sh` downloads the
jar only into the local test cache and checks this SHA-256 before use:

```text
2af190b300cbb0b35e248ccf5f4a06b6072030aeb3da7a98ec73abe5b4cb371f
```

The Android runtime has no dependency download path. The library's license is
in [`third_party/bouncycastle/LICENSE.md`](../../third_party/bouncycastle/LICENSE.md).

Run from the repository root:

```sh
sh test/android/check-pq.sh
sh test/android/check-e2ee-live.sh
```

## Android window insets

Use a disposable API 36 emulator for `WindowInsetsTest`. It clears the saved
host registry. Never run it against a daily-use install or a physical phone.
The CI security job uses API 36 and runs this class with the other
instrumentation tests.

```sh
./android/gradlew -p android lintDebug assembleDebug assembleDebugAndroidTest
ANDROID_SERIAL=emulator-5580 ./android/gradlew -p android connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=dev.darkfactory.mobile.WindowInsetsTest
```

The tests check real system-bar bounds on the pairing screen and a WebView in
the shared native root. Injected insets check portrait and landscape cutouts,
keyboard show/hide, repeat delivery, and zeroed insets reaching WebView.
Also inspect the paired Fleet screen with gesture and three-button navigation,
rotate it, and open and dismiss a real keyboard. Synthetic insets alone do not
prove those interactions or Samsung-specific behavior.
