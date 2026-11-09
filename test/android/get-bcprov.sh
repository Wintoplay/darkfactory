#!/bin/sh
set -eu

version=1.86
sha256=2af190b300cbb0b35e248ccf5f4a06b6072030aeb3da7a98ec73abe5b4cb371f
cache_root=${DF_ANDROID_CACHE:-${XDG_CACHE_HOME:-${TMPDIR:-/tmp}/df-cache}/darkfactory/android-deps}
jar="$cache_root/bcprov-jdk18on-$version.jar"
mkdir -p "$cache_root"

verify() {
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s  %s\n' "$sha256" "$1" | sha256sum -c - >/dev/null
    elif command -v shasum >/dev/null 2>&1; then
        printf '%s  %s\n' "$sha256" "$1" | shasum -a 256 -c - >/dev/null
    else
        echo "get-bcprov: sha256sum or shasum is required" >&2
        return 1
    fi
}

if [ -f "$jar" ] && verify "$jar"; then
    printf '%s\n' "$jar"
    exit 0
fi

tmp="$jar.$$.tmp"
trap 'rm -f "$tmp"' EXIT HUP INT TERM
url="https://repo.maven.apache.org/maven2/org/bouncycastle/bcprov-jdk18on/$version/bcprov-jdk18on-$version.jar"
if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$url" --output "$tmp"
elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$tmp"
else
    echo "get-bcprov: curl or wget is required to fetch the pinned test dependency" >&2
    exit 1
fi
if ! verify "$tmp"; then
    echo "get-bcprov: Bouncy Castle artifact SHA-256 mismatch" >&2
    exit 1
fi
mv "$tmp" "$jar"
printf '%s\n' "$jar"
