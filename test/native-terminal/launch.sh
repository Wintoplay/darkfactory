#!/usr/bin/env bash
# Actual local API launch; only the Ghostty child of this disposable daemon is stopped.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
export DFB_FIXTURE="${DFB_FIXTURE:-/bin/bash}" DFB_EXTRA_SESSIONS=0
source "$HERE/../browser/lib.sh"
ghostty_pid=""
cleanup() {
  [ -z "$ghostty_pid" ] || kill "$ghostty_pid" 2>/dev/null || true
  dfb_down
}
trap cleanup EXIT
dfb_up
export DFB_SID="$DFB_SID1"
identity() {
  curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" |
    python3 -c 'import json,os,sys; s=next(x for x in json.load(sys.stdin)["sessions"] if x["id"]==os.environ["DFB_SID"]); print(json.dumps({k:s.get(k) for k in ("id","pid","startTicks","incarnationId","status")}))'
}
identity >"$DFB_ROOT/before.json"
stat -c '%i' "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" >"$DFB_ROOT/socket-before"
status=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $DFB_TOKEN" \
  -H 'X-DF-Request: 1' "$DFB_BASE/api/v1/sessions/$DFB_SID/native")
[ "$status" = 204 ] || { echo "native launch returned $status" >&2; exit 1; }
for i in $(seq 1 80); do
  ghostty_pid=$(pgrep -P "$DFB_DAEMON" -x ghostty | head -1 || true)
  if [ -n "$ghostty_pid" ] && rg -q "path=/api/v1/sessions/$DFB_SID/resize status=204" "$DFB_ROOT/serve.out"; then break; fi
  sleep .1
done
[ -n "$ghostty_pid" ] && kill -0 "$ghostty_pid" 2>/dev/null || { echo 'Ghostty child did not remain running' >&2; exit 1; }
rg -q "path=/api/v1/sessions/$DFB_SID/resize status=204" "$DFB_ROOT/serve.out" || { echo 'native client did not attach and resize' >&2; exit 1; }
if [ "${DFB_HOLD_FOR_MENU:-0}" = 1 ]; then
  echo "isolated Ghostty test window is ready for a right-click (hold ${DFB_HOLD_SECONDS:-300}s)" >&2
  sleep "${DFB_HOLD_SECONDS:-300}"
fi
kill "$ghostty_pid"
for i in $(seq 1 50); do kill -0 "$ghostty_pid" 2>/dev/null || break; sleep .1; done
ghostty_pid=""
identity >"$DFB_ROOT/after.json"
stat -c '%i' "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" >"$DFB_ROOT/socket-after"
python3 - "$DFB_ROOT" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]); a=json.loads((p/'before.json').read_text()); b=json.loads((p/'after.json').read_text())
ok=a==b and a['status']=='running' and (p/'socket-before').read_text()==(p/'socket-after').read_text()
print(json.dumps({'apiLaunch':True,'nativeAttachAndResize':True,'identityPreserved':ok}))
sys.exit(0 if ok else 1)
PY
