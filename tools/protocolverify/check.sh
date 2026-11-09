#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ -n "${PROVERIF:-}" ]; then
  proverif_bin=$PROVERIF
else
  cache_root=${XDG_CACHE_HOME:-${HOME:-/tmp}/.cache}/darkfactory/protocolverify
  source_dir=$cache_root/proverif2.05
  archive=$cache_root/proverif2.05.tar.gz
  expected_sha=4871f53c32ab4a04669a060c4886ba5d9080496963fb980a9a62d2c429ceabc4
  mkdir -p "$cache_root"
  if [ ! -x "$source_dir/proverif" ]; then
    if [ ! -f "$archive" ]; then
      download_tmp=$cache_root/proverif2.05.tar.gz.tmp
      curl -fL --retry 2 https://proverif.inria.fr/proverif2.05.tar.gz -o "$download_tmp"
      mv "$download_tmp" "$archive"
    fi
    actual_sha=$(sha256sum "$archive" | awk '{print $1}')
    if [ "$actual_sha" != "$expected_sha" ]; then
      rm -f "$archive"
      echo "ProVerif 2.05 archive SHA-256 mismatch" >&2
      exit 1
    fi
    mkdir -p "$cache_root/src"
    tar -xzf "$archive" -C "$cache_root/src"
    (
      cd "$cache_root/src/proverif2.05"
      ./build -nointeract byte
    )
    mkdir -p "$source_dir"
    cp "$cache_root/src/proverif2.05/proverif" "$source_dir/proverif"
  fi
  proverif_bin=$source_dir/proverif
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

run_model() {
  model=$1
  output=$tmp_dir/$(basename "$model").out
  "$proverif_bin" -in pitype "$script_dir/$model" >"$output" 2>&1
  if [ -n "${PROVERIF_REPORT_DIR:-}" ]; then
    mkdir -p "$PROVERIF_REPORT_DIR"
    cp "$output" "$PROVERIF_REPORT_DIR/$(basename "$model").out"
  fi
  if ! grep -Fq 'Verification summary:' "$output"; then
    cat "$output" >&2
    echo "ProVerif did not produce a verification summary for $model" >&2
    exit 1
  fi
  printf '%s\n' "$output"
}

expect_line() {
  output=$1
  expected=$2
  if ! grep -Fq "$expected" "$output"; then
    cat "$output" >&2
    echo "Missing expected ProVerif result: $expected" >&2
    exit 1
  fi
}

expect_regex() {
  output=$1
  expected=$2
  if ! grep -Eq "$expected" "$output"; then
    cat "$output" >&2
    echo "Missing expected ProVerif result matching: $expected" >&2
    exit 1
  fi
}

pairing_out=$(run_model pairing.pv)
expect_line "$pairing_out" 'Query not attacker_bitstring(pairCode[]) is true.'
expect_line "$pairing_out" 'Query not attacker_bitstring(deviceToken[]) is true.'
expect_regex "$pairing_out" 'Query event\(ClientComplete\([^)]*\)\) ==> event\(HostPairComplete\([^)]*\)\) is true\.'
expect_regex "$pairing_out" 'Query not event\(ClientComplete\([^)]*\)\) is false\.'
expect_regex "$pairing_out" 'Query event\(HostAccept\([^)]*\)\) ==> event\(ClientSend\([^)]*\)\) is true\.'
expect_regex "$pairing_out" 'Query not event\(HostAccept\([^)]*\)\) is false\.'
expect_line "$pairing_out" 'Query not (event(HostMint(t)) && attacker_bitstring(t)) is true.'

frames_out=$(run_model response-terminator.pv)
expect_line "$frames_out" 'Query not attacker_bitstring(body[]) is true.'
expect_line "$frames_out" 'Query event(ClientComplete(r,b)) ==> event(ServerEnd(r,b)) is true.'
expect_line "$frames_out" 'Query not event(ClientComplete(req[],body[])) is false.'

for model in hybrid-dh-leak.pv hybrid-kem-leak.pv; do
  leak_out=$(run_model "$model")
  expect_line "$leak_out" 'Query not attacker_bitstring(deviceToken[]) is true.'
done

for model in broken-raw-code.pv broken-unpinned-bundle.pv; do
  attack_out=$(run_model "$model")
  expect_line "$attack_out" 'RESULT not attacker_bitstring(pairCode[]) is false.'
  expect_line "$attack_out" 'A trace has been found.'
done

truncation_out=$(run_model broken-no-terminator.pv)
expect_line "$truncation_out" 'Query event(ClientComplete(r,b)) ==> event(ServerEnd(r,b)) is false.'
expect_line "$truncation_out" 'A trace has been found.'

echo 'ProVerif models passed; negative controls found the expected attacks.'
