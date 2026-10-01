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
  delayed) read -r frame; sleep 0.2; read -t 0.2 -r frame; rc=$?; [ "$rc" -gt 128 ] || exit 0; printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'; read -r frame ;;
  other) read -r frame; printf '%s\n' '{"jsonrpc":"2.0","id":20,"result":{}}' ;;
  empty) read -r frame ;;
  silent) read -r frame; sleep 30 ;;
  lingering) read -r frame; printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'; exec sleep 30 ;;
  stubborn) trap '' TERM; read -r frame; exec sleep 30 ;;
esac
SERVER
chmod +x "$SERVER"

exchange() {
  local started="${EPOCHREALTIME/./}" ended
  if [ "$#" -eq 3 ]; then
    MCP_STDIO_GRACE_SECS="$3" mcp_stdio_exchange 2 "$1" '{"id":2}' "$SERVER" "$2" >"$T/output"
  else
    mcp_stdio_exchange 2 "$1" '{"id":2}' "$SERVER" "$2" >"$T/output"
  fi
  RC=$? WHY="$MCP_STDIO_WHY"
  ended="${EPOCHREALTIME/./}"
  WALL=$(( (ended - started) / 1000 ))
  OUT="$(cat "$T/output")"
}

exchange 1 immediate
if [ "$RC" -eq 0 ] && [ "$WALL" -lt 1000 ] && grep -q '"id":2' <<<"$OUT"; then
  ok "immediate id-2 reply returns before the bound"
else bad "immediate reply" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 1 delayed
if [ "$RC" -eq 0 ] && [ "$WALL" -lt 1000 ] && grep -q '"id":2' <<<"$OUT"; then
  ok "delayed reply arrives while stdin remains open"
else bad "delayed reply" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 1 other
if [ "$RC" -eq 1 ] && [ "$WALL" -lt 1000 ] && grep -q '"id":20' <<<"$OUT" && [ "$WHY" = 'exited before answering' ]; then
  ok "id 20 cannot satisfy wanted id 2"
else bad "exact id" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 1 empty
if [ "$RC" -eq 1 ] && [ "$WALL" -lt 1000 ] && [ -z "$OUT" ] && [ "$WHY" = 'exited before answering' ]; then
  ok "empty server exits unanswered at once"
else bad "empty reply" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

# Upper wall bounds sit below the 2 s production grace, never at a load-sensitive
# few hundred ms: they prove the 0.3 s bound and grace were honoured, not the
# servers' 30 s sleep or the default grace, and hold under a loaded gate.
exchange 0.3 silent
if [ "$RC" -eq 2 ] && [ "$WALL" -ge 300 ] && [ "$WALL" -lt 1900 ] && [ "$WHY" = 'no answer in 0.3s' ]; then
  ok "silent server is stopped at the bound"
else bad "bounded silence" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 1 lingering 0.3
if [ "$RC" -eq 0 ] && [ "$WALL" -ge 300 ] && [ "$WALL" -lt 1900 ] && grep -q '"id":2' <<<"$OUT"; then
  ok "an answered server that never ends on EOF is stopped, its reply kept"
else bad "server lingering after EOF" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

exchange 0.3 stubborn 0.3
if [ "$RC" -eq 2 ] && [ "$WALL" -ge 600 ] && [ "$WALL" -lt 2200 ] && [ "$WHY" = 'no answer in 0.3s' ]; then
  ok "a silent server that ignores TERM is killed after the bound"
else bad "TERM-ignoring server" "rc=$RC wall=$WALL why=$WHY out=$OUT"; fi

unset MCP_STDIO_GRACE_SECS
mcp_stdio_grace_ticks
if [ "$MCP_STDIO_TICKS" -eq 20 ]; then
  ok "production grace resolves to 2 s"
else bad "production grace" "ticks=$MCP_STDIO_TICKS"; fi

exchange 0.5 immediate
mcp_stdio_ticks 0.5
if [ "$RC" -eq 0 ] && [ "$WALL" -lt 1000 ] && [ "$MCP_STDIO_TICKS" -eq 5 ]; then
  ok "fractional bound is accepted as 5 ticks"
else bad "fractional bound" "rc=$RC wall=$WALL why=$WHY"; fi

for value in abc 1.25 -1; do
  mcp_stdio_exchange 2 "$value" '{"id":2}' "$SERVER" immediate >"$T/output"
  RC=$?
  if [ "$RC" -eq 3 ] && [ "$MCP_STDIO_WHY" = "bad bound: $value" ]; then
    ok "bad bound $value is rejected"
  else bad "bad bound $value" "rc=$RC why=$MCP_STDIO_WHY"; fi
done

for value in abc 1.25 -1; do
  MCP_STDIO_GRACE_SECS="$value" mcp_stdio_exchange 2 1 '{"id":2}' "$SERVER" immediate >"$T/output"
  RC=$?
  if [ "$RC" -eq 3 ] && [ "$MCP_STDIO_WHY" = "bad grace: $value" ]; then
    ok "bad grace $value is rejected"
  else bad "bad grace $value" "rc=$RC why=$MCP_STDIO_WHY"; fi
done

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
