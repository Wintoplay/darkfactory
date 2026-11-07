#!/usr/bin/env bash
# Disposable native terminal + hosted-PTY output probe; never use live sessions.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
export DFB_FIXTURE="${DFB_FIXTURE:-/bin/bash}" DFB_EXTRA_SESSIONS=0
source "$HERE/../browser/lib.sh"
trap dfb_down EXIT
dfb_up
export DFB_SID="$DFB_SID1" DFB_NATIVE_RESULT="$DFB_ROOT/native-result.json"
mkdir -p "$DFB_ROOT/ghostty/config" "$DFB_ROOT/ghostty/data" "$DFB_ROOT/ghostty/cache" "$DFB_ROOT/ghostty/home"
# An independent process and temporary XDG roots avoid the operator's window,
# settings, credentials and active Ghostty instance.
set +e
HOME="$DFB_ROOT/ghostty/home" XDG_CONFIG_HOME="$DFB_ROOT/ghostty/config" \
XDG_DATA_HOME="$DFB_ROOT/ghostty/data" XDG_CACHE_HOME="$DFB_ROOT/ghostty/cache" \
  timeout 45s ghostty --gtk-single-instance=false --confirm-close-surface=false \
    --quit-after-last-window-closed=true --clipboard-write=deny \
    -e /usr/bin/python3 "${DFB_BRIDGE:-$HERE/ghostty-bridge.py}" >"$DFB_ROOT/ghostty.log" 2>&1
exit_code=$?
set -e
if [ -f "$DFB_NATIVE_RESULT" ]; then cat "$DFB_NATIVE_RESULT"; else printf '{"ok":false,"error":"no result","exit":%s}\n' "$exit_code"; fi
exit "$exit_code"
