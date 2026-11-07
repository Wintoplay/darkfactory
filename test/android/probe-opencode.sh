#!/usr/bin/env bash
# Regression probe for the OpenCode panel on a phone.
#
#   probe-opencode.sh picker   # switch chats through the mobile picker
#   probe-opencode.sh latest   # scroll away from the newest message, then jump
#   probe-opencode.sh          # both
#
# The page logs the device-pixel box of its own picker, message list and Latest
# button (`oc picker box left,top,width,height`), so the probe taps what the page
# reports instead of hard-coded coordinates that rot with every layout change.
#
# Preconditions: daemon reachable at 127.0.0.1:8787 through adb reverse, Firefox
# on the app with ?debug=1&panel=0, signed in, OpenCode configured with at least
# one chat. The probe opens the panel itself.
#
# Exit 0 both checks passed, 1 precondition failed, 2 the gesture never took
# effect, 3 the control stayed stuck (a picker that never closes).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
. "$here/lib.sh"

log=$(oc_log)
mode=${1:-all}
# The picker tap point can be passed explicitly as X,Y read off a screenshot; the
# page-reported box is preferred because it survives layout changes.
picker_point=${2:-}
case "$mode" in
  picker | latest | all) ;;
  -h | --help)
    awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
    exit 0
    ;;
  *)
    echo "usage: $0 [picker|latest|all]" >&2
    exit 2
    ;;
esac

if [ ! -f "$log" ]; then
  echo "no daemon log at $log; start: ./bin/darkfactory serve --debug" >&2
  exit 1
fi

# oc_lines, oc_mark/oc_since, oc_rect, oc_wait and oc_wait_rect live in lib.sh:
# they read the page's own debug lines out of the daemon log.

oc_center() {
  local rect
  rect=$(oc_rect "$1") || return 1
  df_page_center "$rect"
}

picker_test() {
  local box x y opened before after selected closed
  if [ -n "$picker_point" ]; then
    x=${picker_point%,*}
    y=${picker_point#*,}
  else
    box=$(oc_center picker)
    set -- $box
    x=$1
    y=$2
  fi
  # No keyboard dismissal: tapping the select handles the IME, and a spurious
  # Back would navigate away from the panel (a real field failure).
  oc_mark
  df_tap "$x" "$y"
  if ! opened=$(oc_wait ' oc +picker open ' 6); then
    echo "the chat picker never opened after tapping $x,$y (the panel must show the select)" >&2
    exit 2
  fi
  before=$(printf '%s\n' "$opened" | sed -nE 's/.*current=([A-Za-z0-9_]*).*/\1/p')
  # DPAD moves inside the browser's picker, so the probe does not need the row
  # geometry: go to the next chat, then confirm.
  df_key 20
  sleep 0.4
  df_key 66
  if ! selected=$(oc_wait ' oc +select [A-Za-z0-9_]{6,}' 8); then
    echo "no chat switch after the picker keypress (no 'oc select' in the log)" >&2
    exit 3
  fi
  after=$(printf '%s\n' "$selected" | sed -nE 's/.*select ([A-Za-z0-9_]+).*/\1/p')
  if ! closed=$(oc_wait ' oc +picker closed' 6); then
    echo "the picker never closed after selecting $after: the browser popup is stuck over the page" >&2
    exit 3
  fi
  if [ -n "$before" ] && [ "$before" = "$after" ]; then
    echo "the picker re-selected the chat it started on ($after); this needs a second chat" >&2
    exit 2
  fi
  echo "ok: picker switched ${before:-none} -> $after and closed"
}

latest_test() {
  local rect l t w h cx from to max state off
  rect=$(oc_rect messages)
  IFS=, read -r l t w h <<<"$rect"
  if [ "$h" -lt 200 ]; then
    echo "the message list is ${h}px tall; the panel is not laid out" >&2
    exit 1
  fi
  off=$(df_page_offset)
  cx=$((l + w / 2))
  state=$(oc_lines | grep -E ' oc +latest (show|hide)' | tail -1 || true)
  if [[ "$state" != *" latest show "* ]]; then
    # The messages box already reflects the keyboard's inset, so the swipe is
    # valid either way; do not send Back to "hide" it (it can navigate).
    oc_mark
    # Finger down scrolls toward older messages; the Latest button appears when
    # the list is more than ~90px from the bottom.
    df_swipe "$cx" "$((t + h * 3 / 10 + off))" "$cx" "$((t + h * 8 / 10 + off))" 300
    sleep 0.5
    df_swipe "$cx" "$((t + h * 3 / 10 + off))" "$cx" "$((t + h * 8 / 10 + off))" 300
    if ! oc_wait ' oc +latest show ' 8 >/dev/null; then
      echo "the Latest button never appeared: the chat is too short to scroll, or the list snapped back" >&2
      exit 2
    fi
  fi
  if ! oc_wait_rect latest 8; then
    echo "the page never reported where the Latest button is" >&2
    exit 2
  fi
  set -- $(oc_center latest)
  oc_mark
  df_tap "$1" "$2"
  local jump
  if ! jump=$(oc_wait ' oc +latest jump ' 6); then
    echo "tapping Latest did not jump" >&2
    exit 2
  fi
  from=$(printf '%s\n' "$jump" | sed -nE 's/.*from=([0-9]+).*/\1/p')
  to=$(printf '%s\n' "$jump" | sed -nE 's/.*to=([0-9]+).*/\1/p')
  max=$(printf '%s\n' "$jump" | sed -nE 's/.*max=([0-9]+).*/\1/p')
  if [ "$to" -lt $((max - 4)) ]; then
    echo "the jump stopped at $to of $max" >&2
    exit 2
  fi
  if ! oc_wait ' oc +latest hide ' 6 >/dev/null; then
    echo "the Latest button stayed visible after jumping to $to" >&2
    exit 2
  fi
  echo "ok: latest jump $from -> $to/$max and the button hid"
}

df_serial >/dev/null
df_reverse

# Chat is scoped to a worktree, so it is opened by URL rather than a nav tab.
# ?chat=1 scopes to the newest worktree; the chat it needs must live there.
df_open 'http://127.0.0.1:8787/?debug=1&panel=0&chat=1'
sleep 2
oc_mark
sleep 4
if ! oc_since | grep -qE ' api +.*GET /api/v1/state'; then
  echo "the app is not logging state polls; load it with ?debug=1&panel=0 and sign in" >&2
  exit 1
fi
if ! oc_rect picker >/dev/null; then
  echo "the OpenCode panel never reported its picker; is OpenCode configured and the panel loaded?" >&2
  exit 1
fi

status=0
if [ "$mode" = picker ] || [ "$mode" = all ]; then
  picker_test || status=$?
fi
if [ "$status" -eq 0 ] && { [ "$mode" = latest ] || [ "$mode" = all ]; }; then
  latest_test || status=$?
fi
echo "--- client log during probe"
oc_since | grep -E ' oc |click' || true
exit "$status"
