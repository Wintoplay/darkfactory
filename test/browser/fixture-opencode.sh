#!/bin/sh
# Disposable OpenCode UI only. Do not inherit the operator's session, database,
# credentials, providers, or config. No prompt is submitted.
set -eu
root="${XDG_STATE_HOME:?}/opencode-probe"
mkdir -p "$root/home" "$root/config" "$root/data" "$root/state" "$root/cache"
exec env -i HOME="$root/home" PATH="$PATH" TERM="${TERM:-xterm-256color}" \
  XDG_CONFIG_HOME="$root/config" XDG_DATA_HOME="$root/data" \
  XDG_STATE_HOME="$root/state" XDG_CACHE_HOME="$root/cache" \
  OPENCODE_DISABLE_AUTOUPDATE=1 OPENCODE_DISABLE_SHARE=1 \
  opencode --standalone
