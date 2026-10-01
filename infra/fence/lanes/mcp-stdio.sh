#!/usr/bin/env bash
# mcp_stdio_exchange <want-id> <bound-s> <frames> <command...> — hold the
# server's stdin open until its wanted JSON-RPC response arrives.

mcp_stdio_exchange() {
  local want="$1" bound="$2" frames="$3" dir fifo out pid started rc pipe_trap i
  shift 3
  MCP_STDIO_WHY=""
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

  started="$(date +%s)"
  rc=1
  while :; do
    if jq -e --argjson wanted "$want" 'select(.id == $wanted)' "$out" >/dev/null 2>&1; then
      rc=0
      break
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      MCP_STDIO_WHY="exited before answering"
      break
    fi
    if [ $(( $(date +%s) - started )) -ge "$bound" ]; then
      rc=2
      MCP_STDIO_WHY="no answer in ${bound}s"
      break
    fi
    # POLL-STEP: read the wanted JSON-RPC id and server process state.
    sleep 0.1
  done
  exec 3>&-
  if [ "$rc" -eq 2 ]; then kill "$pid" 2>/dev/null || true; fi
  # POLL-STEP: the server ends on EOF, or on the TERM above. One still running
  # 2 s later is killed: the exchange never waits on its end without limit.
  for i in {1..20}; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  kill -KILL "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  cat "$out"
  rm -rf -- "$dir"
  return "$rc"
}
