# Security checks

Run the local protocol gate with a JDK, Go, OCaml or an installed ProVerif, and permission to bind disposable
loopback sockets. Set `PROVERIF=/path/to/proverif` to use an existing verifier.
Otherwise the check downloads checksum-pinned ProVerif source and builds it with
OCaml:

```sh
make security-check
```

This runs the production Java client's Go interoperability check, the live
pairing/envelope check with isolated XDG roots, and every Go fuzz target in the
HTTP protocol and terminal packages, plus the symbolic protocol models. `FUZZTIME=60s make fuzz` increases the
per-target exploration time. Ordinary `go test ./...` also executes the saved
fuzz seeds. Keep any failure corpus as a regression before fixing the defect.

Run known-advisory checks separately because they contact public databases:

```sh
make vuln
(cd desktop && govulncheck ./...)
python3 test/security/dependencies.py
```

The Python check derives its Java coordinate from the Android build, terminal
package versions from `third_party/README.md`, and browser test dependencies
from their lockfile. The Android build also writes
`android/app/build/reports/security-runtime-dependencies.json` from the resolved
debug and release runtime classpaths. Pass that fresh report with
`--android-runtime` to include transitive runtime dependencies. `--inventory`
prints the selected scope without networking.
The unversioned QR generator and operating-system WebKit/VTE libraries are not
covered by that inventory. Keep the host and Android system packages patched.
A clean scan means no advisory was found in the queried scope at that time.
It does not mean the application has no vulnerabilities.

The `protocol security` workflow runs the local gate, the Java/JavaScript
advisory inventory, and Android compile/lint on pull requests and pushes. The
`vuln` workflow scans both Go modules on pull requests, pushes, and a schedule.
Repository branch protection must require these jobs to prevent bypassing them.

For the actual Android pairing flow, build the current artifacts and select a
fresh disposable emulator that has no DarkFactory install:

```sh
make build
JAVA_HOME=/path/to/jdk make android-build
python3 test/security/android_acceptance.py --serial emulator-5580
```

The test installs only on that emulator, starts a separate daemon, verifies
that stripped hybrid metadata causes no pairing POST and no device appears
before confirmation. It cancels and reuses the same code, confirms a pairing, and relaunches the saved host. It uninstalls its test app and
stops its daemon and detached session host afterward. It refuses physical
phones and existing DarkFactory installs. The Android instrumentation tests also
exercise the actual activity with
unusable saved rows, and the actual proxy with complete and truncated encrypted
upstream responses. Run them on the disposable emulator with
`ANDROID_SERIAL=emulator-5580 ./android/gradlew -p android connectedDebugAndroidTest`.
These checks do not replace physical-device
acceptance for keyboard, notifications, vendor battery policies, or Tailscale.

See `tools/protocolverify/README.md` for symbolic protocol analysis. A symbolic
proof assumes ideal cryptography and trusted out-of-band fingerprint comparison.
It does not verify the Java provider, Go implementation, Android UI, entropy,
constant-time execution, or resistance to endpoint compromise.
