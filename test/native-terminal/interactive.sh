#!/usr/bin/env bash
# Real PTY interaction with the native attach client and an isolated daemon.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
export DFB_FIXTURE="${DFB_FIXTURE:-/bin/bash}" DFB_EXTRA_SESSIONS=0
source "$HERE/../browser/lib.sh"
trap dfb_down EXIT
dfb_up
export DFB_SID="$DFB_SID1"
identity() {
  curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" |
    python3 -c 'import json,os,sys; s=next(x for x in json.load(sys.stdin)["sessions"] if x["id"]==os.environ["DFB_SID"]); print(json.dumps({k:s.get(k) for k in ("id","pid","startTicks","incarnationId","status")}))'
}
identity >"$DFB_ROOT/before.json"
stat -c '%i' "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" >"$DFB_ROOT/socket-before"
python3 "$HERE/interactive.py"
identity >"$DFB_ROOT/after.json"
stat -c '%i' "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" >"$DFB_ROOT/socket-after"
python3 - "$DFB_ROOT" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]); a=json.loads((p/'before.json').read_text()); b=json.loads((p/'after.json').read_text())
ok=a==b and a['status']=='running' and (p/'socket-before').read_text()==(p/'socket-after').read_text()
print(json.dumps({'identityPreserved':ok}))
sys.exit(0 if ok else 1)
PY
