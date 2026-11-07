#!/usr/bin/env bash
# Regression probe for the OpenCode panel's chat round trip on a phone.
#
#   probe-chat.sh
#
# It drives the real UI, not the API: New chat, a prompt with a reply, a
# permission request answered with Allow once, and an interrupted turn. The page
# logs its own control boxes (`oc new box …`, `oc allow box …`), and lib.sh turns
# those into taps that account for the browser toolbar height, so no coordinate
# here is hard-coded from one screenshot.
#
# Preconditions (see test/android/README.md):
#   - daemon reachable at 127.0.0.1:8787 through adb reverse, started with
#     --debug, with DF_LOG pointing at its serve log;
#   - OpenCode configured for that daemon with a real test provider, and the
#     provider policy set to *ask* before the shell tool, so a permission card
#     appears (an auto-allow provider is a setup failure, not a pass);
#   - Firefox on the app, signed in, ?debug=1&panel=0&chat=1;
#   - DF_TOKEN holds that daemon's bearer token (used only for API assertions).
#
# Exit 0 both checks passed, 1 a precondition failed, 2 a setup gesture did not
# take effect, 3 a client regression.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
. "$here/lib.sh"

: "${DF_TOKEN:=}"
prompt_text='Run the shell command: echo df-chat-ok . Then reply with its output in one word.'
long_text='Count from 1 to 500 one number per line.'

usage() {
  awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
}
case "${1:-}" in
  -h | --help)
    usage
    exit 0
    ;;
  '') ;;
  *)
    echo "usage: $0" >&2
    exit 2
    ;;
esac

api() { # api METHOD PATH [BODY]
  local method=$1 path=$2 body=${3:-}
  if [ -n "$body" ]; then
    curl -sf -X "$method" -H "Authorization: Bearer $DF_TOKEN" \
      -H 'Content-Type: application/json' -d "$body" "http://127.0.0.1:${DF_PORT:-8787}$path"
  else
    curl -sf -X "$method" -H "Authorization: Bearer $DF_TOKEN" "http://127.0.0.1:${DF_PORT:-8787}$path"
  fi
}

# chat_ids prints the OpenCode chat ids in newest-first order.
chat_ids() {
  api GET '/api/v1/opencode/sessions?limit=100&order=desc' |
    python3 -c 'import json,sys; print(" ".join(c["id"] for c in json.load(sys.stdin)["data"]))'
}

# messages prints a compact view: type, outcome and assistant text.
messages() { # messages SESSION_ID
  api GET "/api/v1/opencode/sessions/$1/messages?limit=50&order=desc" |
    python3 -c '
import json,sys
for m in reversed(json.load(sys.stdin)["data"]):
    kind = m["type"]
    if kind == "assistant":
        text = " ".join(c.get("text","") for c in m.get("content",[]) if c.get("type")=="text")
        tools = ",".join(c.get("state",{}).get("status","") for c in m.get("content",[]) if c.get("type")=="tool")
        print("assistant finish=%s tools=[%s] text=%r" % (m.get("finish",""), tools, text[:120]))
    elif kind == "idle":
        print("idle outcome=%s" % m.get("outcome",""))
    else:
        print("%s %r" % (kind, m.get("text","")[:80]))
'
}

tap_box() { # tap_box NAME [TIMEOUT]
  local name=$1 deadline=$((SECONDS + ${2:-10})) rect
  while [ "$SECONDS" -lt "$deadline" ]; do
    if rect=$(oc_rect "$name") && [ -n "$rect" ]; then
      df_page_tap "$rect"
      return 0
    fi
    sleep 0.5
  done
  echo "the page never reported a '$name' box" >&2
  return 1
}

send_prompt() { # send_prompt TEXT
  local text=$1 rect
  df_hide_keyboard
  tap_box prompt 8 || return 1
  sleep 0.5
  df_type "$text"
  sleep 0.5
  tap_box send 8 || return 1
}

df_serial >/dev/null
df_reverse

# The probe needs the panel loaded and signed in; it does not sign in itself.
if ! oc_rect new >/dev/null; then
  echo "the OpenCode panel never reported its New chat box; open http://127.0.0.1:${DF_PORT:-8787}/?debug=1&panel=0&chat=1, sign in, and load the worktree chat" >&2
  exit 1
fi
if [ -z "$DF_TOKEN" ]; then
  echo "DF_TOKEN is unset: the probe needs it for API assertions" >&2
  exit 1
fi
if [ -z "$(chat_ids)" ]; then
  echo "the daemon lists no OpenCode chats; is the test provider configured?" >&2
  exit 1
fi

status=0
new_chat=''

new_chat_test() {
  local before after created
  before=" $(chat_ids) "
  oc_mark
  tap_box new 8 || return 2
  if ! oc_wait 'dialog open dlg-oc-new' 8 >/dev/null; then
    echo "New chat never opened its dialog" >&2
    return 2
  fi
  # The Start chat box is unchanged between dialogs, so the page does not log it
  # again; oc_rect already holds the last reported position.
  tap_box newstart 8 || return 2
  if ! oc_wait 'POST /api/v1/opencode/sessions' 15 >/dev/null; then
    echo "Start chat never reached the create route" >&2
    return 2
  fi
  sleep 2
  after=" $(chat_ids) "
  created=''
  for id in $after; do
    case "$before" in *" $id "*) continue ;; esac
    created=$id
    break
  done
  if [ -z "$created" ]; then
    echo "no new OpenCode chat after Start chat; chats: $after" >&2
    return 3
  fi
  new_chat=$created
  echo "ok: New chat created $created"
}

prompt_test() {
  local before after
  if [ -z "$new_chat" ]; then
    echo "no chat to prompt" >&2
    return 2
  fi
  oc_mark
  send_prompt "$prompt_text" || return 2
  if ! oc_wait "POST /api/v1/opencode/sessions/$new_chat/prompt" 15 >/dev/null; then
    echo "the composer never sent a prompt to $new_chat" >&2
    return 3
  fi
  if ! tap_box allow 40; then
    echo "no permission card: the provider is not configured to ask for the shell tool" >&2
    return 2
  fi
  if ! oc_wait "POST /api/v1/opencode/sessions/$new_chat/permissions/" 15 >/dev/null; then
    echo "Allow once never reached the permission reply route" >&2
    return 3
  fi
  # The reply is what the chat is for: an assistant text bubble after our prompt.
  local deadline=$((SECONDS + 40))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if messages "$new_chat" | grep -Eq "assistant .*text='[^']"; then
      echo "ok: prompt sent, permission answered, reply rendered"
      messages "$new_chat" | tail -3
      return 0
    fi
    sleep 2
  done
  echo "no assistant reply after the prompt and permission answer" >&2
  messages "$new_chat" | tail -6 >&2
  return 3
}

interrupt_test() {
  local result
  oc_mark
  send_prompt "$long_text" || return 2
  if ! oc_wait "POST /api/v1/opencode/sessions/$new_chat/prompt" 15 >/dev/null; then
    echo "the long prompt never reached $new_chat" >&2
    return 3
  fi
  # Stop is only reported while a turn is running, so a box is the signal.
  if ! tap_box stop 15; then
    echo "the Stop button never appeared; the turn finished before it could be interrupted" >&2
    return 2
  fi
  if ! oc_wait 'dialog confirm open' 8 >/dev/null; then
    echo "Stop skipped the confirmation dialog" >&2
    return 3
  fi
  tap_box confirmok 8 || return 3
  if ! oc_wait "POST /api/v1/opencode/sessions/$new_chat/interrupt" 15 >/dev/null; then
    echo "the confirmation never reached the interrupt route" >&2
    return 3
  fi
  local deadline=$((SECONDS + 20))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if result=$(messages "$new_chat") && printf '%s\n' "$result" | grep -q 'outcome=interrupted'; then
      echo "ok: turn interrupted from the phone"
      return 0
    fi
    sleep 1
  done
  echo "no interrupted turn in $new_chat" >&2
  messages "$new_chat" | tail -6 >&2
  return 3
}

if new_chat_test; then
  if prompt_test; then
    if interrupt_test; then status=0; else status=$?; fi
  else status=$?; fi
else status=$?; fi
echo "--- client log during probe"
oc_since | grep -E ' oc |click|dialog' || true
exit "$status"
