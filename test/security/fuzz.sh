#!/bin/sh
# Run every checked-in protocol and terminal fuzz target for a bounded duration.
set -eu
cd "$(dirname "$0")/../.."
for package in ./internal/httpapi ./internal/session; do
    targets=$(go test "$package" -list '^Fuzz' | sed -n '/^Fuzz[A-Za-z0-9_]*$/p')
    test -n "$targets" || { echo "No fuzz targets in $package" >&2; exit 1; }
    for target in $targets; do
        go test "$package" -run '^$' -fuzz "^${target}$" \
            -fuzztime "${FUZZTIME:-10s}" -parallel "${FUZZWORKERS:-2}"
    done
done
