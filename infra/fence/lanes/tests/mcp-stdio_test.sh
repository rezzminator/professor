#!/usr/bin/env bash
# The stdio exchange holds a server's stdin until the requested JSON-RPC reply.
set -uo pipefail

SHTEST_TAG=lane-mcp-stdio-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${MCP_STDIO_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../mcp-stdio.sh}"
[ -f "$SUT" ] || { bad "mcp-stdio.sh exists" "missing $SUT"; shtest_end; exit 1; }
# shellcheck source=/dev/null
source "$SUT"

SERVER="$T/server.sh"
cat >"$SERVER" <<'SERVER'
#!/usr/bin/env bash
case "$1" in
  immediate) read -r frame; printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'; read -r frame ;;
  delayed) read -r frame; sleep 1; read -t 1 -r frame; rc=$?; [ "$rc" -gt 128 ] || exit 0; printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'; read -r frame ;;
  other) read -r frame; printf '%s\n' '{"jsonrpc":"2.0","id":20,"result":{}}' ;;
  empty) read -r frame ;;
  silent) read -r frame; sleep 30 ;;
  lingering) read -r frame; printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'; exec sleep 30 ;;
  stubborn) trap '' TERM; read -r frame; exec sleep 30 ;;
esac
SERVER
chmod +x "$SERVER"

exchange() {
  local started
  started="$(date +%s)"
  mcp_stdio_exchange 2 "$1" '{"id":2}' "$SERVER" "$2" >"$T/output"
  RC=$? WHY="$MCP_STDIO_WHY" WALL=$(( $(date +%s) - started ))
  OUT="$(cat "$T/output")"
}

exchange 3 immediate
if [ "$RC" -eq 0 ] && [ "$WALL" -lt 3 ] && grep -q '"id":2' <<<"$OUT"; then
  ok "immediate id-2 reply returns before the bound"
else bad "immediate reply" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 3 delayed
if [ "$RC" -eq 0 ] && [ "$WALL" -lt 3 ] && grep -q '"id":2' <<<"$OUT"; then
  ok "delayed reply arrives while stdin remains open"
else bad "delayed reply" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 3 other
if [ "$RC" -eq 1 ] && [ "$WALL" -lt 3 ] && grep -q '"id":20' <<<"$OUT" && [ "$WHY" = 'exited before answering' ]; then
  ok "id 20 cannot satisfy wanted id 2"
else bad "exact id" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 3 empty
if [ "$RC" -eq 1 ] && [ "$WALL" -lt 3 ] && [ -z "$OUT" ] && [ "$WHY" = 'exited before answering' ]; then
  ok "empty server exits unanswered at once"
else bad "empty reply" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 1 silent
if [ "$RC" -eq 2 ] && [ "$WALL" -le 2 ] && [ "$WHY" = 'no answer in 1s' ]; then
  ok "silent server is stopped at the bound"
else bad "bounded silence" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 3 lingering
if [ "$RC" -eq 0 ] && [ "$WALL" -le 5 ] && grep -q '"id":2' <<<"$OUT"; then
  ok "an answered server that never ends on EOF is stopped, its reply kept"
else bad "server lingering after EOF" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 1 stubborn
if [ "$RC" -eq 2 ] && [ "$WALL" -le 5 ] && [ "$WHY" = 'no answer in 1s' ]; then
  ok "a silent server that ignores TERM is killed after the bound"
else bad "TERM-ignoring server" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

mcp_stdio_exchange 2 1 '{"id":2}' /no/such/mcp-server >"$T/output"
RC=$?
if [ "$RC" -eq 3 ] && [ "$MCP_STDIO_WHY" = 'could not start' ]; then
  ok "missing command is a start failure"
else bad "missing command" "rc=$RC why=$MCP_STDIO_WHY"; fi

mkfifo() { return 1; }
mcp_stdio_exchange 2 1 '{"id":2}' "$SERVER" immediate >"$T/output"
RC=$?
unset -f mkfifo
if [ "$RC" -eq 3 ] && [ "$MCP_STDIO_WHY" = 'could not start' ]; then
  ok "FIFO creation failure is a start failure"
else bad "FIFO failure" "rc=$RC why=$MCP_STDIO_WHY"; fi

shtest_end
