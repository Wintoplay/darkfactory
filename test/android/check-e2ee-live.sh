#!/bin/sh
# Live acceptance for the Android client's v2 envelope.
#
# Starts a disposable daemon with an isolated XDG root and --e2ee-min-suite=2,
# then uses the app's own Java protocol classes to verify the signed bundle,
# pair with a one-time code, open the sealed token, and read /state through an
# encrypted envelope. It also checks that a v1 envelope is refused with
# e2ee_suite_downgrade and that a replayed envelope is not accepted.
#
# Nothing touches the operator's daemon or state. Needs a JDK and Go.
#
# Usage: sh test/android/check-e2ee-live.sh
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
    echo "check-e2ee-live: no javac found; set JAVA_HOME to a JDK" >&2
    return 1
}

jdk=$(resolve_javac)
bcprov=$(sh "$root/test/android/get-bcprov.sh")
work=$(mktemp -d "${TMPDIR:-/tmp}/df-e2ee-live.XXXXXX")
pid=
cleanup() {
    if [ -x "$work/darkfactory" ]; then
        "$work/darkfactory" session-host --stop >/dev/null 2>&1 || true
    fi
    if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
    rm -rf "$work"
}
trap cleanup EXIT INT TERM

cd "$root"
go build -o "$work/darkfactory" ./cmd/darkfactory
export XDG_CONFIG_HOME="$work/config"
export XDG_STATE_HOME="$work/state"
export XDG_DATA_HOME="$work/data"
export XDG_CACHE_HOME="$work/cache"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
base="http://127.0.0.1:$port"
"$work/darkfactory" serve --listen "127.0.0.1:$port" --e2ee-min-suite=2 --idle-exit 3m \
    >"$work/serve.log" 2>&1 &
pid=$!

ready=0
i=0
while [ "$i" -lt 100 ]; do
    if curl -fsS "$base/api/v1/e2ee/host-key" >"$work/hostkey.json" 2>/dev/null; then
        ready=1
        break
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
        break
    fi
    sleep 0.1
    i=$((i + 1))
done
if [ "$ready" -ne 1 ]; then
    echo "check-e2ee-live: daemon did not become ready" >&2
    sed -n '1,40p' "$work/serve.log" >&2 || true
    exit 1
fi

token=$("$work/darkfactory" token)
mint_code() {
    curl -fsS -X POST "$base/api/v1/pair/code" \
        -H "Authorization: Bearer $token" \
        -H 'Content-Type: application/json' \
        -H 'X-DF-Request: 1' \
        -d '{"scope":"full","label":"pq-live"}' |
        python3 -c 'import json, sys; print(json.load(sys.stdin)["code"])'
}

code=$(mint_code)

"$jdk/javac" -cp "$bcprov" -d "$work/classes" \
    android/app/src/main/java/dev/darkfactory/mobile/HostFingerprint.java \
    android/app/src/main/java/dev/darkfactory/mobile/HostKey.java \
    android/app/src/main/java/dev/darkfactory/mobile/HostPinPolicy.java \
    android/app/src/main/java/dev/darkfactory/mobile/EncryptedResponseReader.java \
    android/app/src/main/java/dev/darkfactory/mobile/V2Protocol.java \
    android/app/src/main/java/dev/darkfactory/mobile/Envelope.java \
    android/app/src/main/java/dev/darkfactory/mobile/crypto/*.java \
    test/android/pq/LivePair.java

"$jdk/java" -cp "$work/classes:$bcprov" dev.darkfactory.mobile.pq.LivePair "$base" "$code"

# A code minted under a v2 host cannot be spent by a client that offers only
# the legacy suite, even before any envelope exists.
legacyCode=$(mint_code)
status=$(curl -sS -o "$work/downgrade-pair.json" -w '%{http_code}' -X POST "$base/api/v1/pair" \
    -H 'Content-Type: application/json' \
    -H 'X-DF-Request: 1' \
    -d "{\"code\":\"$legacyCode\"}")
if [ "$status" != "403" ] || ! grep -q 'e2ee_suite_downgrade' "$work/downgrade-pair.json"; then
    echo "check-e2ee-live: legacy pairing was not refused as a downgrade (HTTP $status)" >&2
    cat "$work/downgrade-pair.json" >&2
    exit 1
fi

echo "check-e2ee-live: pair-time downgrade refused (HTTP 403)"
echo "check-e2ee-live: live daemon agrees with the Android protocol"
