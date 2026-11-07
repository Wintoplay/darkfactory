#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
javac -d "$tmp" "$root/android/app/src/main/java/dev/darkfactory/mobile/HostFingerprint.java" "$root/test/android/HostFingerprintTest.java"
java -cp "$tmp" dev.darkfactory.mobile.HostFingerprintTest
