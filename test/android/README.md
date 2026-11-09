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
