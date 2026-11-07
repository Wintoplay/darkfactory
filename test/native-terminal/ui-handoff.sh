#!/usr/bin/env bash
# Isolated browser -> native -> browser handoff using the actual UI button.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
export DFB_FIXTURE=/bin/bash DFB_EXTRA_SESSIONS=0
source "$HERE/../browser/lib.sh"
trap dfb_down EXIT
dfb_up
export DFB_SID="$DFB_SID1"
identity() {
  curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" |
    python3 -c 'import json,os,sys; s=next(x for x in json.load(sys.stdin)["sessions"] if x["id"]==os.environ["DFB_SID"]); print(json.dumps({k:s.get(k) for k in ("id","pid","startTicks","incarnationId","status")}))'
}
identity >"$DFB_ROOT/before.json"
DF_PLAYWRIGHT_MODULE="${DF_PLAYWRIGHT_MODULE:-}" node "$HERE/ui-handoff.cjs"
identity >"$DFB_ROOT/after.json"
cmp "$DFB_ROOT/before.json" "$DFB_ROOT/after.json"
