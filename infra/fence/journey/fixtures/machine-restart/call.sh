#!/usr/bin/env bash
# Both restored and faulted routes use the same real engine MCP entry.
_restart_select() {
  local result=0
  RESTART_ROLE="$2"
  mesh_select "$1" "$RESTART_ROLE" "$3" || result=$?
  case "$result" in
    0) return 0 ;;
    1) RESTART_ROLE="restart-$2"; mesh_select "$1" "$RESTART_ROLE" "$3" ;;
    *) return "$result" ;;
  esac
}

_restart_call() {
  local caller_branch="$1" callee_branch="$2" label="$3" caller callee prefix callee_origin=new-from-picker
  local ID="${RESTART_ASSERT_ID:-chat.call-mcp}"
  [ "$caller_branch" != "$callee_branch" ] || callee_origin=new-from-cli
  prefix="$JOURNEY_OUT/beats/$PFM_BEAT/$label"
  caller="$prefix-caller.json" callee="$prefix-callee.json"
  _restart_select "$caller_branch" new-from-picker "$caller" && _restart_select "$callee_branch" "$callee_origin" "$callee" || return 2
  mesh_mcp "$caller" call "$(jq -nc --arg target "$(jq -r '.session_id' "$callee")" --arg message "restart-$label mock-engine: {\"type\":\"turn\",\"reply\":\"pong\",\"busy_ms\":1}" '{target:$target,message:$message}')" "$prefix-call" || return 2
  assert "$ID" "$(jq -cS . "$HERE/goldens/machine-restart/delivery.json")" \
    "$(jq -cS '{code,status,typed}' "$prefix-call.result.json")" "$prefix-call.result.json"
  poll "restart-$label-pong" 20 _restart_pong "$callee" || return 2
  assert "$ID" 1 "$(python3 "$HERE/fixtures/chat-ending/mcp.py" "$(jq -er '.records' "$callee")/prompts" input-count "restart-$label mock-engine:")" callee-engine-input-count
  mesh_mcp "$caller" last "$(jq -nc --arg target "$(jq -r '.session_id' "$callee")" '{target:$target}')" "$prefix-last" || return 2
  assert "$ID" '"pong"' "$(jq -c .text "$prefix-last.result.json")" "$prefix-last.result.json"
}

_restart_pong() {
  python3 "$HERE/fixtures/chat-ending/lifecycle.py" state "$1" idle "$RESTART_ROOT/callee-status.json" &&
    [ "$(jq -r '.last' "$RESTART_ROOT/callee-status.json")" = pong ]
}
