#!/usr/bin/env bash
# Start an isolated daemon with a deterministic TUI session, so the browser
# probes can drive the real client against a real PTY. Source this file; it is
# a library, not a program.
#
# Everything is disposable: XDG directories, the state DB, the session host and
# the fixture live under one temporary root. The operator's daemon, token and
# sessions are never touched.
#
# shellcheck shell=bash

DFB_HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
DFB_REPO=$(cd "$DFB_HERE/../.." && pwd)
: "${DFB_PORT:=0}"
: "${DFB_KEEP:=0}"
: "${DFB_OPENCODE:=0}"
# DFB_OC_STUB=1 replaces the real OpenCode service with
# fixture-opencode-api.py, which serves one chat and a question form the probe
# controls through a JSON file.
: "${DFB_OC_STUB:=0}"
# DFB_FIXTURE selects the program behind the daemon's "shell" preset; the
# hostile probe points it at fixture-hostile.py. DFB_EXTRA_SESSIONS controls the
# filler tabs the layout probes need, and is 0 when one session must own the
# input log.
: "${DFB_FIXTURE:=$DFB_HERE/fixture-tui.py}"
: "${DFB_EXTRA_SESSIONS:=3}"
# DFB_BIN overrides the daemon under test. Point it at an installed binary to
# prove the probe fails on that build, not only on the current working tree.
: "${DFB_BIN:=$DFB_REPO/bin/darkfactory}"

# dfb_opencode_up starts a disposable OpenCode service under $DFB_ROOT and writes
# the daemon credentials file. env -i is required: the operator's shell (and this
# agent harness) may export OPENCODE_DB / OPENCODE_SESSION_ID, and an "isolated"
# service that inherits them reads the live session store instead of its own.
dfb_opencode_up() {
  local root="$DFB_ROOT/opencode" port deadline pw

  if ! command -v opencode >/dev/null 2>&1; then
    echo "opencode is not on PATH; the opencode probe needs the v2 CLI" >&2
    return 1
  fi

  mkdir -p "$root/home" "$root/config/opencode" "$root/data" "$root/state" "$root/cache"
  cat >"$root/config/opencode/opencode.json" <<'JSON'
{
  "$schema": "https://opencode.ai/config.json",
  "model": "opencode/beam-free",
  "agent": { "build": { "model": "opencode/beam-free" } }
}
JSON
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  env -i HOME="$root/home" PATH="$PATH" \
    XDG_CONFIG_HOME="$root/config" XDG_DATA_HOME="$root/data" \
    XDG_STATE_HOME="$root/state" XDG_CACHE_HOME="$root/cache" \
    opencode serve --hostname 127.0.0.1 --port "$port" >"$root/serve.log" 2>&1 &
  DFB_OC_PID=$!
  export DFB_OC_PID

  deadline=$((SECONDS + 30))
  pw=""
  while [ $SECONDS -lt $deadline ]; do
    pw=$(sed -n 's/^server password //p' "$root/serve.log" 2>/dev/null | tail -1)
    if [ -n "$pw" ] && curl -s -o /dev/null -u "opencode:$pw" \
      "http://127.0.0.1:$port/api/session?limit=1"; then
      break
    fi
    pw=""
    sleep 0.2
  done
  if [ -z "$pw" ]; then
    echo "the isolated OpenCode service did not start; see $root/serve.log" >&2
    return 1
  fi

  DFB_OC_URL="http://127.0.0.1:$port"
  DFB_OC_CREDS="$DFB_ROOT/opencode-credentials.json"
  python3 -c 'import json,sys; open(sys.argv[1], "w").write(json.dumps({"password": sys.argv[2]}))' \
    "$DFB_OC_CREDS" "$pw"
  chmod 600 "$DFB_OC_CREDS"
  export DFB_OC_URL DFB_OC_CREDS
}

# dfb_stub_up starts the fake OpenCode API and points the daemon config at it.
# It needs DFB_ROOT and the probe repo, so it runs inside dfb_up.
dfb_stub_up() {
  DFB_STUB_DIR="$DFB_ROOT/repo"
  DFB_STUB_FORMS="$DFB_ROOT/forms.json"
  DFB_STUB_IDLE="$DFB_ROOT/idle.txt"
  printf '0' >"$DFB_STUB_IDLE"
  cat >"$DFB_STUB_FORMS" <<'JSON'
{"data":[{"id":"form_1","title":"Which database should I use?","fields":[]}]}
JSON
  local port
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  DFB_STUB_DIR="$DFB_STUB_DIR" DFB_STUB_FORMS="$DFB_STUB_FORMS" DFB_STUB_IDLE="$DFB_STUB_IDLE" \
    python3 "$DFB_HERE/fixture-opencode-api.py" "$port" >"$DFB_ROOT/opencode-stub.log" 2>&1 &
  DFB_STUB_PID=$!
  export DFB_STUB_PID DFB_STUB_FORMS DFB_STUB_IDLE
  local deadline=$((SECONDS + 10))
  while [ $SECONDS -lt $deadline ]; do
    if curl -sf "http://127.0.0.1:$port/api/session?limit=1" >/dev/null; then
      break
    fi
    sleep 0.1
  done
  DFB_OC_URL="http://127.0.0.1:$port"
  DFB_OC_CREDS="$DFB_ROOT/opencode-credentials.json"
  printf '{"password":"stub"}' >"$DFB_OC_CREDS"
  chmod 600 "$DFB_OC_CREDS"
  export DFB_OC_URL DFB_OC_CREDS
}

dfb_up() {
  DFB_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/df-browser-probe.XXXXXX")
  export DFB_ROOT
  mkdir -p "$DFB_ROOT/xdg-config" "$DFB_ROOT/xdg-state" "$DFB_ROOT/xdg-data" "$DFB_ROOT/repo"
  DFB_LOG=$DFB_ROOT/input.log
  : >"$DFB_LOG"

  git -C "$DFB_ROOT/repo" init -q -b main .
  git -C "$DFB_ROOT/repo" -c user.email=probe@invalid -c user.name=probe \
    commit -q --allow-empty -m init

  if [ ! -x "$DFB_BIN" ]; then
    make -C "$DFB_REPO" build >/dev/null
  fi

  if [ "$DFB_PORT" = 0 ]; then
    DFB_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  fi
  DFB_BASE="http://127.0.0.1:$DFB_PORT"

  # The OpenCode probe needs a service before the daemon starts, because the
  # daemon reads opencodeAPIURL once at startup.
  if [ "$DFB_OC_STUB" = 1 ]; then
    dfb_stub_up || return 1
  elif [ "$DFB_OPENCODE" = 1 ]; then
    dfb_opencode_up || return 1
  fi
  if [ "$DFB_OC_STUB" = 1 ] || [ "$DFB_OPENCODE" = 1 ]; then
    mkdir -p "$DFB_ROOT/xdg-config/darkfactory"
    cat >"$DFB_ROOT/xdg-config/darkfactory/config.json" <<EOF
{
  "opencodeAPIURL": "$DFB_OC_URL",
  "opencodePasswordFile": "$DFB_OC_CREDS"
}
EOF
  fi

  # The shell preset uses $SHELL, so the daemon's environment decides what a
  # "shell" session runs. Nothing else is configured for the probe.
  XDG_CONFIG_HOME="$DFB_ROOT/xdg-config" \
  XDG_STATE_HOME="$DFB_ROOT/xdg-state" \
  XDG_DATA_HOME="$DFB_ROOT/xdg-data" \
  SHELL="$DFB_HERE/fixture-shell.sh" \
  DF_MOUSE_LOG="$DFB_LOG" \
  DF_FIXTURE_TUI="$DFB_FIXTURE" \
    "$DFB_BIN" serve --listen "127.0.0.1:$DFB_PORT" --debug \
    >"$DFB_ROOT/serve.out" 2>&1 &
  DFB_DAEMON=$!
  export DFB_DAEMON

  local deadline=$((SECONDS + 20))
  DFB_TOKEN=""
  while [ $SECONDS -lt $deadline ]; do
    DFB_TOKEN=$(cat "$DFB_ROOT/xdg-config/darkfactory/token" 2>/dev/null || true)
    if [ -n "$DFB_TOKEN" ] && \
      curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" >/dev/null; then
      break
    fi
    sleep 0.2
  done
  if [ -z "$DFB_TOKEN" ]; then
    echo "daemon did not start; see $DFB_ROOT/serve.out" >&2
    return 1
  fi
  export DFB_BASE DFB_TOKEN DFB_LOG

  if ! curl -sf -H "Authorization: Bearer $DFB_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"path\":\"$DFB_ROOT/repo\"}" "$DFB_BASE/api/v1/repos" >/dev/null; then
    echo "could not add the probe repo; see $DFB_ROOT/serve.out" >&2
    return 1
  fi
  # The repo response shape varies with whether the checkout was adopted; read
  # the authoritative list from the state route instead.
  DFB_WID=$(curl -sf -H "Authorization: Bearer $DFB_TOKEN" "$DFB_BASE/api/v1/state" | python3 -c \
    'import json,sys; print(json.load(sys.stdin)["worktrees"][0]["id"])') || return 1
  export DFB_WID

  DFB_SID1=$(dfb_session alpha k1)
  DFB_SID2=$(dfb_session beta k2)
  DFB_ALL_IDS="$DFB_SID1 $DFB_SID2"
  export DFB_SID1 DFB_SID2
  # Enough tabs to overflow a 396 px strip, so the pinned add control has to
  # stay reachable without the probe scrolling for it.
  local extra spec made=0
  for spec in "gamma k3" "delta k4" "epsilon k5"; do
    [ "$made" -lt "$DFB_EXTRA_SESSIONS" ] || break
    made=$((made + 1))
    # shellcheck disable=SC2086
    extra=$(dfb_session $spec)
    DFB_ALL_IDS="$DFB_ALL_IDS $extra"
  done
  export DFB_ALL_IDS
  sleep 1
}

# dfb_session prints the new session id, or fails loudly.
dfb_session() { # dfb_session TITLE KEY
  local body
  body=$(curl -sf -H "Authorization: Bearer $DFB_TOKEN" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: $2" \
    -d "{\"worktreeId\":\"$DFB_WID\",\"agent\":\"shell\",\"kind\":\"shell\",\"title\":\"$1\",\"cols\":40,\"rows\":10}" \
    "$DFB_BASE/api/v1/sessions") || return 1
  printf '%s' "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
}

dfb_down() {
  if [ -n "${DFB_TOKEN:-}" ]; then
    for sid in ${DFB_ALL_IDS:-} ${DFB_SID1:-} ${DFB_SID2:-}; do
      [ -n "$sid" ] || continue
      curl -sf -X DELETE -H "Authorization: Bearer $DFB_TOKEN" \
        "$DFB_BASE/api/v1/sessions/$sid" >/dev/null 2>&1 || true
    done
  fi
  if [ -n "${DFB_DAEMON:-}" ]; then
    kill "$DFB_DAEMON" 2>/dev/null || true
    wait "$DFB_DAEMON" 2>/dev/null || true
  fi
  if [ -n "${DFB_OC_PID:-}" ]; then
    kill "$DFB_OC_PID" 2>/dev/null || true
    wait "$DFB_OC_PID" 2>/dev/null || true
  fi
  if [ -n "${DFB_STUB_PID:-}" ]; then
    kill "$DFB_STUB_PID" 2>/dev/null || true
    wait "$DFB_STUB_PID" 2>/dev/null || true
  fi
  if [ -n "${DFB_ROOT:-}" ]; then
    "$DFB_BIN" session-host \
      --socket "$DFB_ROOT/xdg-state/darkfactory/session-host.sock" --stop >/dev/null 2>&1 || true
    if [ "${DFB_KEEP:-0}" = 1 ]; then
      echo "kept $DFB_ROOT" >&2
    else
      rm -rf "$DFB_ROOT"
    fi
  fi
}
