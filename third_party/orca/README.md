# Orca terminal reference and adaptation

The unchanged reference files under `upstream/` come from the local Orca
checkout's git objects at commit
`2e40b990b8a70f0d4bb987be2fb6b51a38e477bc`.
The checkout's origin is <https://github.com/stablyai/orca>.
`manifest.json` records the source paths and SHA-256 hashes.

Copyright (c) 2026 Lovecast Inc. The original MIT license is preserved at
[`upstream/LICENSE`](upstream/LICENSE).

## What is usable now

`clients/terminal/mouse-click.mjs` adapts the upstream `buildMouseClickInput`
algorithm into a pure function independent of React Native and Orca's scope.
`clients/terminal/mouse-click.test.mjs` runs with Node's built-in test runner.
It checks literal SGR, pixel and legacy press/release bytes, X10 press-only
behavior, disabled tracking, unknown encoding, and coordinate bounds.

Local changes are explicit:

- Pass proven tracking/encoding and resolved coordinates as input. A future
  client adapter owns viewport pan, scale, cell metrics, and mode observation.
- Keep output empty when encoding is unknown. Do not assume legacy encoding.
- Validate nonnegative integer cell coordinates in the pure function because
  the upstream viewport adapter's clamping is not part of this adaptation.
- Return bytes to the caller. No Orca notification bus or DarkFactory input
  queue is invoked here.

This code is not loaded by the shipped browser yet. Wiring it blindly into
the existing touch handler could duplicate native xterm clicks. C1/C2 in
`docs/client-foundation.md` own integration and real PTY/device tests.

The Android snapshots under
`upstream/mobile/modules/orca-mobile-web-shell/...` are reference only. They
record the problem Orca solved: a bundled document needs a stable app-owned
origin, an unrelaxable document CSP, a response-header set that does not leak
the session through image fetches, and a navigation policy that allows only the
served main document. DarkFactory's `android/` client implements those ideas in
its own Kotlin loopback origin (`LoopbackServer` and `MainActivity`); it does not
link Orca's Expo/React Native module.

The preserved TypeScript files are reference snapshots, not a standalone Orca
runtime. Their imports still name upstream modules. The Vitest and DOM tests
are preserved for migration, not claimed to pass inside DarkFactory. The local
Node tests cover the adapted encoder only, not all upstream behaviors.

## Verify or reimport

```sh
python3 tools/vendor-orca.py
node --test clients/terminal/mouse-click.test.mjs
```

To restore the pinned snapshot from the sibling checkout:

```sh
python3 tools/vendor-orca.py --import-from ../orca
```

Import reads the pinned commit, not the working tree or latest branch. It
never fetches or writes to Orca. To adopt a later version, review the source
diff, update the recipe's revision, migrate tests, regenerate the manifest, and
record local changes here. Do not edit the snapshots in place.
