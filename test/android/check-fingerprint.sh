#!/bin/sh
# Compiles HostFingerprint and its JVM test to pin DP-19 without a device.
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
    echo "check-fingerprint: no javac found; set JAVA_HOME to a JDK" >&2
    return 1
}

jdk=$(resolve_javac)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
"$jdk/javac" -d "$tmp" "$root/android/app/src/main/java/dev/darkfactory/mobile/HostFingerprint.java" "$root/test/android/HostFingerprintTest.java"
"$jdk/java" -cp "$tmp" dev.darkfactory.mobile.HostFingerprintTest
