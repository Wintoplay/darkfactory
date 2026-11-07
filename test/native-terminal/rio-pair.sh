#!/usr/bin/env bash
# Compare native terminals sequentially on one disposable hosted PTY.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${DFB_OUT:?DFB_OUT must name a new result directory}"
mkdir "$DFB_OUT"
export DFB_FIXTURE=/bin/bash DFB_EXTRA_SESSIONS=0
source "$HERE/../browser/lib.sh"
monitor_pid=""
cleanup() {
  [ -z "$monitor_pid" ] || kill "$monitor_pid" 2>/dev/null || true
  dfb_down
}
trap cleanup EXIT
dfb_up
export DFB_SID="$DFB_SID1" DFB_NATIVE_RESULT="$DFB_ROOT/ghostty-result.json"
curl -sN --unix-socket "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" \
  "http://unix/v1/sessions/$DFB_SID/stream" >"$DFB_ROOT/host-monitor.bin" &
monitor_pid=$!
sleep .2
host_state() {
  python3 - "$DFB_ROOT/host-monitor.bin" <<'PYMON'
import json, pathlib, struct, sys
b=pathlib.Path(sys.argv[1]).read_bytes(); out=b''; i=0
while i+5 <= len(b):
    size=struct.unpack('>I',b[i+1:i+5])[0]
    if i+5+size > len(b): break
    if b[i:i+1] in (b'S',b'D'): out += b[i+5:i+5+size]
    i += 5+size
print(json.dumps({'bytes':len(out), 'ghosttyMarker':b'DARKFLOODDONE' in out, 'rioMarker':b'DARKFLOODRIO' in out}))
PYMON
}
mkdir -p "$DFB_ROOT/ghostty/config" "$DFB_ROOT/ghostty/data" "$DFB_ROOT/ghostty/cache" "$DFB_ROOT/ghostty/home" \
  "$DFB_ROOT/rio/config" "$DFB_ROOT/rio/data" "$DFB_ROOT/rio/cache" "$DFB_ROOT/rio/home"
identity() {
  curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" |
    python3 -c 'import json,os,sys; s=next(x for x in json.load(sys.stdin)["sessions"] if x["id"]==os.environ["DFB_SID"]); print(json.dumps({k:s.get(k) for k in ("id","pid","startTicks","incarnationId","status")}))'
}
identity >"$DFB_OUT/before.json"
stat -c '%i' "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" >"$DFB_OUT/host-socket-before.txt"
native() {
  export DFB_NATIVE_RESULT="$DFB_ROOT/ghostty-result.json"
  HOME="$DFB_ROOT/ghostty/home" XDG_CONFIG_HOME="$DFB_ROOT/ghostty/config" \
  XDG_DATA_HOME="$DFB_ROOT/ghostty/data" XDG_CACHE_HOME="$DFB_ROOT/ghostty/cache" \
    timeout 45s ghostty --gtk-single-instance=false --confirm-close-surface=false \
    --quit-after-last-window-closed=true --clipboard-write=deny \
    -e /usr/bin/python3 "$HERE/rio-ghostty-bridge.py" >"$DFB_OUT/ghostty.log" 2>&1
  native_exit=$?
  if [ -f "$DFB_NATIVE_RESULT" ]; then cp "$DFB_NATIVE_RESULT" "$DFB_OUT/ghostty.json"; fi
}
rio_probe() {
  export DFB_NATIVE_RESULT="$DFB_ROOT/rio-result.json"
  mkdir -p "$DFB_ROOT/rio/config/rio"
  printf '[window]\ncolumns = 79\nrows = 26\n' >"$DFB_ROOT/rio/config/rio/config.toml"
  HOME="$DFB_ROOT/rio/home" XDG_CONFIG_HOME="$DFB_ROOT/rio/config" \
  XDG_DATA_HOME="$DFB_ROOT/rio/data" XDG_CACHE_HOME="$DFB_ROOT/rio/cache" \
    timeout 45s "${DFB_RIO_BIN:?DFB_RIO_BIN must name the Rio binary}" \
    -e /usr/bin/python3 "$HERE/rio-bridge.py" >"$DFB_OUT/rio.log" 2>&1
  rio_exit=$?
  if [ -f "$DFB_NATIVE_RESULT" ]; then cp "$DFB_NATIVE_RESULT" "$DFB_OUT/rio.json"; fi
}
native_exit=99
rio_exit=99
set +e
if [ "${DFB_ORDER:-native-first}" = rio-first ]; then
  rio_probe
  identity >"$DFB_OUT/between.json"
  host_state >"$DFB_OUT/host-between.json"
  native
else
  native
  identity >"$DFB_OUT/between.json"
  host_state >"$DFB_OUT/host-between.json"
  rio_probe
fi
identity >"$DFB_OUT/after.json"
host_state >"$DFB_OUT/host-after.json"
stat -c '%i' "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" >"$DFB_OUT/host-socket-after.txt"
set -e
python3 - "$DFB_OUT" "$native_exit" "$rio_exit" <<'PY'
import json, pathlib, sys
p=pathlib.Path(sys.argv[1])
ids=[json.loads((p / (name + '.json')).read_text()) for name in ('before','between','after')]
identity_ok=(ids[0] == ids[1] == ids[2] and ids[0]['status'] == 'running' and
             (p/'host-socket-before.txt').read_text() == (p/'host-socket-after.txt').read_text())
def load(name):
    path=p/(name+'.json')
    return json.loads(path.read_text()) if path.exists() else {}

host=load('host-after')
ghostty=load('ghostty')
candidate=load('rio')
result={'identityPreserved':identity_ok, 'ghosttyExit':int(sys.argv[2]), 'rioExit':int(sys.argv[3]),
        'ghosttyOk':bool(ghostty.get('ok')), 'rioOk':bool(candidate.get('ok')),
        'markersSeen':bool(host.get('ghosttyMarker') and host.get('rioMarker'))}
(p/'summary.json').write_text(json.dumps(result, indent=2)+'\n')
print(json.dumps(result))
sys.exit(0 if identity_ok and int(sys.argv[2]) == 0 and int(sys.argv[3]) == 0 and
         result['ghosttyOk'] and result['rioOk'] and result['markersSeen'] else 1)
PY
