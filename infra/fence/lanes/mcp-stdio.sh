#!/usr/bin/env bash
# mcp_stdio_exchange <want-id> <bound-s> <frames> <command...> — hold the
# server's stdin open until its wanted JSON-RPC response arrives.
# MCP_STDIO_GRACE_SECS sets the end-wait grace; production defaults to 2 s.

mcp_stdio_ticks() {
  local seconds="$1" whole tenth=0
  [[ "$seconds" =~ ^[0-9]+(\.[0-9])?$ ]] || return 1
  whole="${seconds%%.*}"
  if [[ "$seconds" == *.* ]]; then tenth="${seconds#*.}"; fi
  MCP_STDIO_TICKS=$(( 10#$whole * 10 + 10#$tenth ))
}

mcp_stdio_grace_ticks() {
  mcp_stdio_ticks "${MCP_STDIO_GRACE_SECS-2}"
}

mcp_stdio_exchange() {
  local want="$1" bound="$2" frames="$3" dir fifo out pid rc pipe_trap i bound_ticks grace_ticks grace alive
  shift 3
  MCP_STDIO_WHY=""
  if ! mcp_stdio_ticks "$bound"; then
    MCP_STDIO_WHY="bad bound: $bound"
    return 3
  fi
  bound_ticks="$MCP_STDIO_TICKS"
  grace="${MCP_STDIO_GRACE_SECS-2}"
  if ! mcp_stdio_grace_ticks; then
    MCP_STDIO_WHY="bad grace: $grace"
    return 3
  fi
  grace_ticks="$MCP_STDIO_TICKS"
  if [ "$#" -eq 0 ] || ! command -v "$1" >/dev/null 2>&1; then
    MCP_STDIO_WHY="could not start"
    return 3
  fi
  dir="$(mktemp -d "${TMPDIR:-/tmp}/lane-mcp-stdio.XXXXXX")" || {
    MCP_STDIO_WHY="could not start"
    return 3
  }
  fifo="$dir/in" out="$dir/out"
  if ! mkfifo "$fifo" || ! : >"$out"; then
    MCP_STDIO_WHY="could not start"
    rm -rf -- "$dir"
    return 3
  fi

  "$@" <"$fifo" >"$out" &
  pid=$!
  # A server that exits before reading can close the pipe during these writes.
  pipe_trap="$(trap -p PIPE)"
  trap '' PIPE
  if ! exec 3>"$fifo"; then
    trap - PIPE
    [ -z "$pipe_trap" ] || eval "$pipe_trap"
    MCP_STDIO_WHY="could not start"
    wait "$pid" 2>/dev/null || true
    rm -rf -- "$dir"
    return 3
  fi
  printf '%s\n' "$frames" >&3 2>/dev/null || true
  trap - PIPE
  [ -z "$pipe_trap" ] || eval "$pipe_trap"

  rc=1 i=0
  while :; do
    if kill -0 "$pid" 2>/dev/null; then alive=1; else alive=0; fi
    if jq -e --argjson wanted "$want" 'select(.id == $wanted)' "$out" >/dev/null 2>&1; then
      rc=0
      break
    fi
    if [ "$alive" -eq 0 ]; then
      MCP_STDIO_WHY="exited before answering"
      break
    fi
    if [ "$i" -ge "$bound_ticks" ]; then
      rc=2
      MCP_STDIO_WHY="no answer in ${bound}s"
      break
    fi
    # POLL-STEP: read the wanted JSON-RPC id and server process state.
    sleep 0.1
    i=$(( i + 1 ))
  done
  exec 3>&-
  if [ "$rc" -eq 2 ]; then kill "$pid" 2>/dev/null || true; fi
  # POLL-STEP: the server ends on EOF, or on the TERM above. One still running
  # the configured grace later is killed: the end wait stays bounded.
  for ((i=0; i<grace_ticks; i++)); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  cat "$out"
  rm -rf -- "$dir"
  return "$rc"
}
