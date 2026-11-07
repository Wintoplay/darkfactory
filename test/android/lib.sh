#!/usr/bin/env bash
# Helpers for driving the DarkFactory browser client on an Android emulator.
# Source this file; it is a library, not a program.
#
# The procedure and the traps are in docs/testing-android.md. This directory is
# the code for it: boot, reverse, tap, type, screenshot. Coordinates are device
# pixels; a screenshot shown in an image viewer is smaller, and a tap read off
# the image is ~15% off on both axes.
#
# shellcheck shell=bash

: "${ANDROID_SDK_ROOT:=$HOME/Android/Sdk}"
: "${DF_PORT:=8787}"
: "${DF_AVD:=dfphone}"

export ANDROID_SDK_ROOT
export ANDROID_HOME="$ANDROID_SDK_ROOT"
export PATH="$PATH:$ANDROID_SDK_ROOT/platform-tools:$ANDROID_SDK_ROOT/emulator"

DF_ANDROID_HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# df_serial prints the single attached device, or explains why there is none.
df_serial() {
  local serial
  serial=$(adb devices | awk '/\tdevice$/{print $1; exit}')
  if [ -z "$serial" ]; then
    echo "no adb device; start the emulator (df_boot in test/android/lib.sh)" >&2
    return 1
  fi
  printf '%s\n' "$serial"
}

# df_boot starts the AVD headless when nothing is attached and waits for boot.
# -no-window is fine: adb exec-out screencap is the display.
df_boot() {
  if adb devices | grep -q $'\tdevice$'; then
    return 0
  fi
  emulator -avd "$DF_AVD" -no-window -no-audio -no-snapshot -no-boot-anim \
    -gpu swiftshader_indirect -accel on -memory 3072 >/dev/null 2>&1 &
  disown
  df_wait_boot
}

df_wait_boot() {
  local tries=0
  until [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; do
    tries=$((tries + 1))
    if [ "$tries" -gt 120 ]; then
      echo "emulator did not report boot_completed" >&2
      return 1
    fi
    sleep 5
  done
  sleep 2
}

# df_reverse is why the phone needs no --allow-remote, no TLS and no pairing:
# the page is served from 127.0.0.1 from the device's point of view.
df_reverse() {
  adb reverse "tcp:${DF_PORT}" "tcp:${DF_PORT}" >/dev/null
}

df_screen() {
  adb shell wm size 2>/dev/null | sed 's/.*: //' | head -1
}

df_shot() { # df_shot out.png
  adb exec-out screencap -p >"$1"
}

df_tap() { # df_tap X Y   (device pixels)
  adb shell input tap "$1" "$2"
}

df_swipe() { # df_swipe X1 Y1 X2 Y2 [duration_ms] — finger down = scroll up
  adb shell input swipe "$1" "$2" "$3" "$4" "${5:-300}"
}

df_type() { # df_type 'echo hello' — input text drops a bare space
  adb shell input text "${1// /%s}"
}

df_key() { # df_key 66 (Enter) / 4 (Back)
  adb shell input keyevent "$1"
}

# df_open opens a URL in Firefox. Fenix receives external VIEW intents in
# org.mozilla.fenix.IntentReceiverActivity; the old `.App` alias does not exist
# in current builds, and an unresolved explicit intent silently falls through to
# Chrome. The inner quotes matter: a bare `&` in the query would be read by the
# device shell as a command separator and the later parameters would be lost.
df_open() { # df_open 'http://127.0.0.1:8787/?debug=1'
  adb shell "am start -a android.intent.action.VIEW -d '$1' -n org.mozilla.firefox/org.mozilla.fenix.IntentReceiverActivity" >/dev/null 2>&1
}

# The browser client reports control boxes from getBoundingClientRect() scaled
# by devicePixelRatio. That is screen pixels in x, but in y it is measured from
# the layout viewport, which begins below Firefox's toolbar (312 px on the Pixel
# 5 AVD). df_page_offset measures that gap off a screenshot; df_page_tap and
# df_page_center apply it so a probe can tap what the page reported.
df_page_offset() {
  local shot offset
  shot=$(mktemp "${TMPDIR:-/tmp}/df-page-offset.XXXXXX.png")
  df_shot "$shot" >/dev/null 2>&1 || { rm -f "$shot"; echo 0; return 0; }
  offset=$(python3 "$DF_ANDROID_HERE/page-offset.py" "$shot" 2>/dev/null) || offset=0
  rm -f "$shot"
  echo "${offset:-0}"
}

df_page_center() { # df_page_center "left,top,width,height" -> "x y"
  local l t w h
  IFS=, read -r l t w h <<<"$1"
  echo "$(( l + w / 2 )) $(( t + h / 2 + $(df_page_offset) ))"
}

df_page_tap() { # df_page_tap "left,top,width,height"
  local x y
  read -r x y <<<"$(df_page_center "$1")"
  df_tap "$x" "$y"
}

# A focused terminal raises the soft keyboard, which shrinks the layout viewport
# and moves the bottom nav up by ~780px on a Pixel 5. A nav tap computed from
# the device screen then lands in the keyboard. Hide it first; Back with no
# keyboard open would navigate the browser, so only send it when one is shown.
# `mInputShown=true` alone is not proof: it can survive a dismissed IME. Also
# require that the focused IME client is Firefox itself.
df_keyboard_shown() {
  local uid dump
  uid=$(adb shell pm list packages -U 2>/dev/null |
    sed -n 's/^package:org\.mozilla\.firefox uid:\([0-9]*\).*/\1/p' | head -1)
  [ -n "$uid" ] || return 1
  dump=$(adb shell dumpsys input_method 2>/dev/null)
  printf '%s' "$dump" | grep -q 'mInputShown=true' || return 1
  printf '%s' "$dump" | grep -q "mCurClient=ClientState{[^}]*mUid=$uid"
}

df_hide_keyboard() {
  if df_keyboard_shown; then
    df_key 4
    sleep 1
  fi
}

# Page-log helpers. The client posts its debug lines to the daemon, which
# mirrors them into the serve log; oc_lines strips the slog wrapper. These are
# shared by the OpenCode probes, which need to read state the UI only reports
# in its own log (picker, message list, control boxes).
oc_log() {
  printf '%s\n' "${DF_LOG:-${XDG_STATE_HOME:-$HOME/.local/state}/darkfactory/serve.log}"
}

oc_lines() {
  grep 'client-debug' "$(oc_log)" 2>/dev/null | sed 's/.*line="//; s/"$//'
}

# oc_mark remembers the current line count; oc_since prints lines after it.
oc_mark() { oc_mark_line=$(oc_lines | wc -l); }
oc_since() { oc_lines | tail -n "+$((oc_mark_line + 1))"; }

# oc_rect NAME prints "left,top,width,height" for the last box the page logged.
oc_rect() {
  local line
  line=$(oc_lines | grep -E " oc +$1 box [0-9]+,[0-9]+,[0-9]+,[0-9]+$" | tail -1 || true)
  [ -n "$line" ] || return 1
  printf '%s\n' "$line" | sed -nE 's/.*box ([0-9]+,[0-9]+,[0-9]+,[0-9]+)$/\1/p'
}

# oc_wait PATTERN TIMEOUT prints the first fresh line matching an extended regex.
oc_wait() {
  local pattern=$1 deadline=$((SECONDS + ${2:-8})) line
  while [ "$SECONDS" -lt "$deadline" ]; do
    line=$(oc_since | grep -E "$pattern" | tail -1 || true)
    if [ -n "$line" ]; then
      printf '%s\n' "$line"
      return 0
    fi
    sleep 0.5
  done
  return 1
}

oc_wait_rect() {
  local name=$1 deadline=$((SECONDS + ${2:-8}))
  while [ "$SECONDS" -lt "$deadline" ]; do
    oc_rect "$name" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  return 1
}

# df_nav taps a bottom-nav button by fraction of the device screen, so it does
# not inherit the screenshot scaling trap.
df_nav() { # fleet|session|runs  (chat is opened from a worktree, not the nav)
  df_hide_keyboard
  local size w h x y
  size=$(df_screen)
  w=${size%x*}
  h=${size#*x}
  y=$((h - h * 122 / 2340))
  case "$1" in
    fleet) x=$((w * 166 / 1000)) ;;
    session) x=$((w * 500 / 1000)) ;;
    runs) x=$((w * 833 / 1000)) ;;
    *)
      echo "unknown nav button: $1" >&2
      return 2
      ;;
  esac
  df_tap "$x" "$y"
}

# df_token types the daemon token into the focused field without printing it.
df_token() {
  local token
  token=$(cat "$HOME/.config/darkfactory/token")
  df_type "$token" >/dev/null 2>&1
}
