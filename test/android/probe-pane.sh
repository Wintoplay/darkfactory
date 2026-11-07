#!/usr/bin/env bash
# Regression probe: the phone terminal must still be painted after the gesture
# that switches what the pane shows.
#
# Default gesture: Fleet, then Session (the bottom-nav round trip). With -t it
# taps one device pixel instead, which is how you probe a session-tab switch:
# read the tab's centre off a screenshot, pass it.
#
# Preconditions, because the probe drives the UI and does not sign in:
#   - the daemon is running and reachable at 127.0.0.1:8787 (adb reverse)
#   - Firefox is showing the app, signed in, with the session pane open
#
# Exit 0 painted before and after, 1 precondition failed, 2 the gesture did not
# attach a session, 3 blank pane.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
. "$here/lib.sh"

tap_point=""
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -t)
      tap_point=$2
      shift 2
      ;;
    -o)
      out=$2
      shift 2
      ;;
    -h|--help)
      awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

check=$here/check-blank.py
: "${out:=$(mktemp -d)}"
mkdir -p "$out"

log=${DF_LOG:-${XDG_STATE_HOME:-$HOME/.local/state}/darkfactory/serve.log}
log_start=$(wc -l <"$log" 2>/dev/null || echo 0)

df_serial >/dev/null
df_reverse

fail_blank() {
  echo "$1" >&2
  echo "screenshots: $out" >&2
  exit 3
}

df_shot "$out/1-before.png"
before=$(python3 "$check" "$out/1-before.png" || true)
echo "before: $before"
case "$before" in
  *verdict=painted*) ;;
  *)
    echo "the pane is not painted before the gesture; open a session on the phone first" >&2
    echo "screenshots: $out" >&2
    exit 1
    ;;
esac

if [ -n "$tap_point" ]; then
  x=${tap_point%,*}
  y=${tap_point#*,}
  df_hide_keyboard
  df_tap "$x" "$y"
  sleep 2
else
  df_nav fleet
  sleep 1
  df_nav session
  sleep 2
fi

df_shot "$out/2-after.png"
after=$(python3 "$check" "$out/2-after.png" || true)
echo "after:  $after"

# The client's own decisions are the only remote console there is. A switch that
# never attached a stream (a stale tap point, a dialog eating the tap) changes
# the screenshot without exercising the pane, so require an attach in the log
# window rather than trusting the ink alone.
log_lines=""
if [ -f "$log" ]; then
  for _ in 1 2 3 4 5 6; do
    log_lines=$(tail -n "+$((log_start + 1))" "$log" | grep 'client-debug' |
      grep -Ei 'attach|redraw|term' || true)
    [ -n "$log_lines" ] && break
    sleep 0.5
  done
fi

case "$after" in
  *verdict=painted*) ;;
  *) fail_blank "the pane went blank after the gesture (see $out/2-after.png)" ;;
esac

case "$log_lines" in
  *attach*) ;;
  *)
    echo "the gesture did not attach a session; the tap point is stale or another control took it" >&2
    echo "screenshots: $out" >&2
    exit 2
    ;;
esac

echo "--- client log during probe"
printf '%s\n' "$log_lines"

echo "ok: pane painted before and after ($out)"
