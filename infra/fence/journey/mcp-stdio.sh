#!/usr/bin/env bash
# mcp_stdio_exchange WANT-ID BOUND-SECONDS FRAMES COMMAND [ARG...]
# Emits response frames; sets MCP_STDIO_WHY and MCP_STDIO_END=eof|term|kill.
# Exit 0 wanted reply, 1 early EOF, 2 deadline, 3 invalid input/start/read failure.
mcp_stdio_exchange() {
  local directory metadata status
  directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)" || return 3
  metadata="$(mktemp "${TMPDIR:?run tmp directory required}/journey-mcp.XXXXXXXX")" || return 3
  if python3 "$directory/mcp_stdio.py" "$1" "$2" "$3" "$metadata" "${@:4}"; then status=0
  else status="$?"; fi
  { IFS= read -r MCP_STDIO_WHY; IFS= read -r MCP_STDIO_END; } <"$metadata"
  rm -f -- "$metadata"
  return "$status"
}
