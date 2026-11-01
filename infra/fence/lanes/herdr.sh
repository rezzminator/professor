#!/usr/bin/env bash
# The owned real-Herdr server lifecycle lanes H and HELD share, under the named
# session H_SESSION; lib.sh supplies bounded wait_for.
H_SESSION="${H_SESSION:-lane-H}"
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
    "$H_HERDR_BIN --session $H_SESSION server"|"herdr --session $H_SESSION server") ;;
    *) printf 'H SERVER-OWNERSHIP-REFUSED pid=%s args=%s\n' "$H_SERVER" "$args" >&2; return 1 ;;
  esac
  if ! timeout 5 "$H_HERDR_BIN" --session "$H_SESSION" server stop; then
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
  export HERDR_SOCKET_PATH="$HOME/.config/herdr/sessions/$H_SESSION/herdr.sock" HERDR_ENV=1
  if [ -e "$HERDR_SOCKET_PATH" ] || [ -L "$HERDR_SOCKET_PATH" ]; then
    printf 'H SERVER-SOCKET-OCCUPIED %s\n' "$HERDR_SOCKET_PATH" >&2
    return 1
  fi
  H_SERVER=""
  trap 'h_server_cleanup || exit 1' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
  "$H_HERDR_BIN" --session "$H_SESSION" server >"$H_FIXTURE/server.log" 2>&1 &
  H_SERVER=$!
  if ! wait_for 10 'test -S "$HERDR_SOCKET_PATH"'; then
    printf 'H SERVER-NOT-READY pid=%s socket=%s: %s\n' "$H_SERVER" "$HERDR_SOCKET_PATH" "$LANE_WAIT_WHY" >&2
    cat "$H_FIXTURE/server.log" >&2
    return 1
  fi
}

# Run fixture commands beneath the real shell of a plain Herdr caller pane.
h_run_in_pane() {
  local pane="$1" run_dir command status
  shift
  run_dir="$(mktemp -d "$H_FIXTURE/shell.XXXXXX")" || return 1
  printf -v command '%q ' "$@"
  printf -v command 'PATH=%q %s >%q 2>&1; printf "%%s\\n" "$?" >%q' "$PATH" "$command" "$run_dir/out" "$run_dir/status"
  if ! python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" pane.send_text \
       "$(jq -nc --arg p "$pane" --arg text "$command" '{pane_id:$p,text:$text}')" >"$run_dir/transport.log" 2>&1 ||
     ! python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" pane.send_keys \
       "$(jq -nc --arg p "$pane" '{pane_id:$p,keys:["enter"]}')" >>"$run_dir/transport.log" 2>&1; then
    printf 'H SHELL-INPUT-FAILED pane=%s artifacts=%s\n' "$pane" "$run_dir" >&2
    cat "$run_dir/transport.log" >&2
    return 1
  fi
  if ! wait_for 10 "test -s '$run_dir/status'"; then
    printf 'H SHELL-TIMEOUT pane=%s artifacts=%s: %s\n' "$pane" "$run_dir" "$LANE_WAIT_WHY" >&2
    return 124
  fi
  status="$(cat "$run_dir/status")" || return 1
  cat "$run_dir/out" || return 1
  rm -rf -- "$run_dir" || return 1
  return "$status"
}

# One request on this server's API socket, its JSON response on stdout.
h_request() { python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" "$1" "$2"; }

# Type a line into a pane: its text, then Enter.
h_type() {
  h_request pane.send_text "$(jq -nc --arg p "$1" --arg text "$2" '{pane_id:$p,text:$text}')" >>"$LANE_LOG" &&
    h_request pane.send_keys "$(jq -nc --arg p "$1" '{pane_id:$p,keys:["enter"]}')" >>"$LANE_LOG"
}

# herdr-shield's own listing for this server (`herdr-shield list --json`), never cached.
h_shield() { HERDR_SOCKET_PATH="$HERDR_SOCKET_PATH" herdr-shield list --json; }

# h_held_holder <pane> — the one live, running, attached holder herdr-shield binds to
# <pane> on this server, as JSON; none or several is an error naming the count.
h_held_holder() {
  h_shield | jq -ec --arg p "$1" --arg s "$HERDR_SOCKET_PATH" '
    [.holders[] | select(.binding.pane == $p and .binding.socket == $s and .holder_live == true
      and .state == "running" and .orphan == false)]
    | if length == 1 then .[0] else error("\(length) live holders bound to \($p)") end'
}

# h_held_engine <pane> [name] — the pid of the first process named <name>
# (default claude) under <pane>'s holder child, breadth-first over
# /proc/<pid>/task/*/children: the engine pfm runs in the held shell.
h_held_engine() {
  local holder queue pid kid
  holder="$(h_held_holder "$1")" || return 1
  queue=("$(jq -er '.child.pid' <<<"$holder")") || return 1
  while [ "${#queue[@]}" -gt 0 ]; do
    pid="${queue[0]}" queue=("${queue[@]:1}")
    if [ "$(cat "/proc/$pid/comm" 2>/dev/null)" = "${2:-claude}" ]; then printf '%s\n' "$pid"; return 0; fi
    for kid in $(cat /proc/"$pid"/task/*/children 2>/dev/null); do queue+=("$kid"); done
  done
  return 1
}

# h_shell_prompt <pane> — the pane's last line is zsh's own prompt.
h_shell_prompt() {
  herdr --session "$H_SESSION" pane read "$1" | awk 'NF { last = $0 } END { exit !(last ~ /%[[:space:]]*$/) }'
}

# h_picker_dismiss <pane> — once the picker a new held pane opens shows its New
# chat row, Escape out of it, then poll until zsh's own prompt is back.
h_picker_dismiss() {
  wait_for 10 "herdr --session '$H_SESSION' pane read '$1' | grep -q 'New chat'" || return 1
  h_request pane.send_keys "$(jq -nc --arg p "$1" '{pane_id:$p,keys:["escape"]}')" >>"$LANE_LOG" || return 1
  wait_for 8 "h_shell_prompt '$1' && ! herdr --session '$H_SESSION' pane read '$1' | grep -q 'New chat'"
}
