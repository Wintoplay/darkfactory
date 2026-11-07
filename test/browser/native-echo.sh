#!/usr/bin/env bash
# Run only against a disposable daemon, PTY, token, and WebKit profile.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
export DFB_FIXTURE="${DFB_FIXTURE:-/bin/bash}" DFB_EXTRA_SESSIONS=0
source "$HERE/lib.sh"
monitor_pid=""
watch_pid=""
cleanup() {
  [ -z "$monitor_pid" ] || kill "$monitor_pid" 2>/dev/null || true
  [ -z "$watch_pid" ] || kill "$watch_pid" 2>/dev/null || true
  dfb_down
}
trap cleanup EXIT
dfb_up
export DFB_SID="$DFB_SID1"
if [ "${DFB_HOST_MONITOR:-0}" = 1 ]; then
  curl -sN --unix-socket "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" \
    "http://unix/v1/sessions/$DFB_SID/stream" >"$DFB_ROOT/host-monitor.bin" &
  monitor_pid=$!
  (
    while ! LC_ALL=C grep -aq DARKFLOODDONE "$DFB_ROOT/host-monitor.bin" 2>/dev/null; do sleep .05; done
    date +%s%3N >"$DFB_ROOT/host-marker-ms"
  ) &
  watch_pid=$!
fi
mkdir -p "$DFB_ROOT/webkit-config" "$DFB_ROOT/webkit-data" "$DFB_ROOT/webkit-cache"
XDG_CONFIG_HOME="$DFB_ROOT/webkit-config" XDG_DATA_HOME="$DFB_ROOT/webkit-data" XDG_CACHE_HOME="$DFB_ROOT/webkit-cache" \
  /usr/bin/python3 "$HERE/native-echo.py"
