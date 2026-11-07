#!/usr/bin/env bash
# Standalone OpenCode in a disposable hosted PTY, rendered in native Ghostty.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${DFB_OUT:?DFB_OUT must name a new result directory}"
mkdir "$DFB_OUT"
export DFB_FIXTURE="$HERE/../browser/fixture-opencode.sh" DFB_EXTRA_SESSIONS=0
source "$HERE/../browser/lib.sh"
trap dfb_down EXIT
dfb_up
export DFB_SID="$DFB_SID1" DFB_NATIVE_RESULT="$DFB_ROOT/native-result.json"
identity() {
  curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" |
    python3 -c 'import json,os,sys; s=next(x for x in json.load(sys.stdin)["sessions"] if x["id"]==os.environ["DFB_SID"]); print(json.dumps({k:s.get(k) for k in ("id","pid","startTicks","incarnationId","status")}))'
}
identity >"$DFB_OUT/before.json"
mkdir -p "$DFB_ROOT/ghostty/config" "$DFB_ROOT/ghostty/data" "$DFB_ROOT/ghostty/cache" "$DFB_ROOT/ghostty/home"
set +e
HOME="$DFB_ROOT/ghostty/home" XDG_CONFIG_HOME="$DFB_ROOT/ghostty/config" \
XDG_DATA_HOME="$DFB_ROOT/ghostty/data" XDG_CACHE_HOME="$DFB_ROOT/ghostty/cache" \
  timeout 45s ghostty --gtk-single-instance=false --confirm-close-surface=false \
    --quit-after-last-window-closed=true --clipboard-write=deny \
    -e /usr/bin/python3 "$HERE/opencode-bridge.py" >"$DFB_OUT/ghostty.log" 2>&1
native_exit=$?
set -e
if [ -f "$DFB_NATIVE_RESULT" ]; then cp "$DFB_NATIVE_RESULT" "$DFB_OUT/ghostty.json"; fi
identity >"$DFB_OUT/after.json"
python3 - "$DFB_OUT" "$native_exit" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]); a=json.loads((p/'before.json').read_text()); b=json.loads((p/'after.json').read_text())
q=p/'ghostty.json'; result=json.loads(q.read_text()) if q.exists() else {}
ok=a==b and a['status']=='running' and result.get('ok') and int(sys.argv[2])==0
print(json.dumps({'ok':bool(ok),'identityPreserved':a==b,'native':result}))
sys.exit(0 if ok else 1)
PY
