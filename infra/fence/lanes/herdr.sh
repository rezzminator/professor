#!/usr/bin/env bash
# H's owned real-Herdr server lifecycle; lib.sh supplies bounded wait_for.
h_server_cleanup() {
  [ -n "${H_SERVER:-}" ] || return 0
  local args probe_rc
  args="$(ps -ww -o args= -p "$H_SERVER" 2>"$H_FIXTURE/server-probe.log")"; probe_rc=$?
  if [ "$probe_rc" -eq 1 ] && [ ! -S "$HERDR_SOCKET_PATH" ]; then
    wait "$H_SERVER" 2>/dev/null || true
    H_SERVER=""
    return 0
  fi
  if [ "$probe_rc" -ne 0 ]; then
    printf 'H SERVER-CLEANUP-FAILED pid=%s socket=%s ps_rc=%s\n' "$H_SERVER" "$HERDR_SOCKET_PATH" "$probe_rc" >&2
    cat "$H_FIXTURE/server-probe.log" >&2
    return 1
  fi
  case "$args" in
    "$H_HERDR_BIN --session lane-H server"|"herdr --session lane-H server") ;;
    *) printf 'H SERVER-OWNERSHIP-REFUSED pid=%s args=%s\n' "$H_SERVER" "$args" >&2; return 1 ;;
  esac
  if ! timeout 5 "$H_HERDR_BIN" --session lane-H server stop; then
    printf 'H SERVER-STOP-FAILED pid=%s socket=%s\n' "$H_SERVER" "$HERDR_SOCKET_PATH" >&2
    return 1
  fi
  if ! wait_for 5 '! ps -p "$H_SERVER" -o pid= >/dev/null 2>&1'; then
    printf 'H SERVER-SURVIVOR pid=%s socket=%s: %s\n' "$H_SERVER" "$HERDR_SOCKET_PATH" "$LANE_WAIT_WHY" >&2
    return 1
  fi
  wait "$H_SERVER" 2>/dev/null || true
  H_SERVER=""
  if [ -S "$HERDR_SOCKET_PATH" ]; then
    printf 'H SERVER-SOCKET-SURVIVOR %s\n' "$HERDR_SOCKET_PATH" >&2
    return 1
  fi
}

h_server_start() {
  : "${H_FIXTURE:?H_FIXTURE is required}" "${HOME:?HOME is required}"
  H_HERDR_BIN="$(command -v herdr)" || { echo 'H SERVER-BINARY-MISSING herdr' >&2; return 1; }
  export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}" XDG_DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
  export XDG_STATE_HOME="${XDG_STATE_HOME:-$HOME/.local/state}" XDG_CACHE_HOME="${XDG_CACHE_HOME:-$HOME/.cache}"
  export HERDR_SOCKET_PATH="$HOME/.config/herdr/sessions/lane-H/herdr.sock" HERDR_ENV=1
  if [ -e "$HERDR_SOCKET_PATH" ] || [ -L "$HERDR_SOCKET_PATH" ]; then
    printf 'H SERVER-SOCKET-OCCUPIED %s\n' "$HERDR_SOCKET_PATH" >&2
    return 1
  fi
  H_SERVER=""
  trap 'h_server_cleanup || exit 1' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
  "$H_HERDR_BIN" --session lane-H server >"$H_FIXTURE/server.log" 2>&1 &
  H_SERVER=$!
  if ! wait_for 10 'test -S "$HERDR_SOCKET_PATH"'; then
    printf 'H SERVER-NOT-READY pid=%s socket=%s: %s\n' "$H_SERVER" "$HERDR_SOCKET_PATH" "$LANE_WAIT_WHY" >&2
    cat "$H_FIXTURE/server.log" >&2
    return 1
  fi
}
