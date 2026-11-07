#!/usr/bin/env bash
# Run the browser client against an isolated daemon and a real PTY.
#
#   test/browser/probe.sh attach    # C1: clicks survive attach, switch, reload
#   test/browser/probe.sh session   # C2: session-tab "+" creates a live PTY
#   test/browser/probe.sh opencode  # UI: many chats, switch by click/picker
#   test/browser/probe.sh gate      # UI: desktop + phone layout contract
#   test/browser/probe.sh hostile   # TUI: hostile escape output is contained
#   test/browser/probe.sh echo      # measurement: keyboard-to-visible-echo
#   test/browser/probe.sh native-dialog  # native pane yields to a modal dialog
#   test/browser/probe.sh sidebar        # sidebar pin, copy path, session order
#   test/browser/probe.sh activity       # bell list: pending question, source, manual clear
#
# Needs a built daemon (make build), Node, Python 3, and Playwright with a
# Chromium build. Point DF_PLAYWRIGHT_MODULE / DF_CHROMIUM at them when they are
# not resolvable from the working directory. The opencode mode also needs the
# `opencode` v2 CLI on PATH; it starts a disposable service of its own. Not part
# of `make check`: it drives a browser and a PTY, and it is evidence, not a unit
# test.
#
# shellcheck source=lib.sh
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
MODE=${1:-}
case "$MODE" in
  attach|session|gate) ;;
  native-dialog)
    # The pane mode only needs one live session and no filler tabs.
    export DFB_EXTRA_SESSIONS=0
    ;;
  sidebar)
    # Two named sessions plus the fillers exercise the reorder.
    export DFB_EXTRA_SESSIONS=2
    ;;
  activity)
    # A fake OpenCode API serves one chat and a pending question form, so the
    # probe can check a prompt that was already waiting when the page loaded.
    export DFB_OC_STUB=1
    export DFB_EXTRA_SESSIONS=0
    ;;
  hostile)
    # The hostile fixture owns the input log, so do not add filler sessions.
    export DFB_FIXTURE="$HERE/fixture-hostile.py"
    export DFB_EXTRA_SESSIONS=0
    ;;
  echo)
    # A real shell echoes what is typed; the TUI fixture does not.
    export DFB_FIXTURE="${DFB_ECHO_SHELL:-/bin/bash}"
    export DFB_EXTRA_SESSIONS=0
    ;;
  opencode) export DFB_OPENCODE=1 ;;
  *)
    echo "usage: $0 attach|session|opencode|gate|hostile|echo|native-dialog|sidebar|activity" >&2
    exit 2
    ;;
esac

# shellcheck disable=SC1091
source "$HERE/lib.sh"

cleanup() { dfb_down; }
trap cleanup EXIT

dfb_up
if [ "$MODE" = opencode ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/probe-opencode.cjs"
elif [ "$MODE" = gate ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/gate.cjs"
elif [ "$MODE" = hostile ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/hostile.cjs"
elif [ "$MODE" = native-dialog ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/native-dialog.cjs"
elif [ "$MODE" = sidebar ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/sidebar.cjs"
elif [ "$MODE" = activity ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/activity.cjs"
elif [ "$MODE" = echo ]; then
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/echo.cjs"
else
  DFB_ROOT=$DFB_ROOT DF_PLAYWRIGHT_MODULE=${DF_PLAYWRIGHT_MODULE:-} \
    node "$HERE/probe.cjs" "$MODE"
fi
