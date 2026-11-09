#!/bin/sh
# Cross-checks the Android client's v2 crypto against the daemon's Go code.
#
# Compiles the app's Bouncy Castle crypto adapters and protocol classes for a
# plain JVM, runs them against vectors from tools/pqcheck, then has Go re-derive
# the schedule and decapsulate the ciphertexts. It needs a JDK and the pinned
# test-time Bouncy Castle jar, not the Android SDK or a device.
#
# Usage: sh test/android/check-pq.sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

resolve_javac() {
    if [ -n "${JAVA_HOME:-}" ] && [ -x "$JAVA_HOME/bin/javac" ]; then
        echo "$JAVA_HOME/bin"
        return 0
    fi
    if command -v javac >/dev/null 2>&1; then
        dirname "$(command -v javac)"
        return 0
    fi
    for candidate in "$HOME"/.local/opt/jdk-*/bin "$HOME"/.sdkman/candidates/java/*/bin; do
        if [ -x "$candidate/javac" ]; then
            echo "$candidate"
            return 0
        fi
    done
    echo "check-pq: no javac found; set JAVA_HOME to a JDK" >&2
    return 1
}

jdk=$(resolve_javac)
bcprov=$(sh "$root/test/android/get-bcprov.sh")
work=$(mktemp -d "${TMPDIR:-/tmp}/df-pq.XXXXXX")
trap 'rm -rf "$work"' EXIT

cd "$root"
go run ./tools/pqcheck gen -dir "$work"

"$jdk/javac" -cp "$bcprov" -d "$work/classes" \
    android/app/src/main/java/dev/darkfactory/mobile/HostFingerprint.java \
    android/app/src/main/java/dev/darkfactory/mobile/HostKey.java \
    android/app/src/main/java/dev/darkfactory/mobile/HostPinPolicy.java \
    android/app/src/main/java/dev/darkfactory/mobile/EncryptedResponseReader.java \
    android/app/src/main/java/dev/darkfactory/mobile/Envelope.java \
    android/app/src/main/java/dev/darkfactory/mobile/V2Protocol.java \
    android/app/src/main/java/dev/darkfactory/mobile/crypto/*.java \
    test/android/pq/PqCheck.java

"$jdk/java" -cp "$work/classes:$bcprov" dev.darkfactory.mobile.pq.PqCheck "$work"
go run ./tools/pqcheck check -dir "$work"
