#!/usr/bin/env bash
set -uo pipefail
HERE="$1"; CALLER="$2"; CALLEE="$3"; PREFIX="$4"; ID="$5"; MODE="${6:-protected}"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
export JOURNEY_TARGET="$CALLEE.target"
assert "$ID" agent_not_found "$(jq -r '.code' "$PREFIX.agent-get.json")" "$PREFIX.agent-get.json"
if [ "$MODE" = delivery ]; then
  MESSAGE="agentless-orchestrator-$PFM_BEAT mock-engine: $(jq -c . "$HERE/fixtures/chat-mesh/turn.json")"
  mesh_mcp "$CALLER" call "$(jq -nc --arg target "$(jq -r '.session_id' "$CALLEE")" --arg message "$MESSAGE" '{target:$target,message:$message}')" "$PREFIX.call" || exit 2
  python3 "$HERE/fixtures/fleet-reader/projection.py" delivery "$PREFIX.call.result.json" >"$PREFIX.delivery.json" || exit 2
  golden "$ID" chat-mesh/delivered.json "$PREFIX.delivery.json"
  poll agentless-recipient-idle 15 mesh_state "$CALLEE" idle || exit 2
  observe || exit 2
  cp "$JOURNEY_OBSERVATION" "$PREFIX.held-observation.json"
  python3 "$HERE/fixtures/fleet-reader/projection.py" call "$JOURNEY_OBSERVATION" "$CALLER" "$CALLEE" "$MESSAGE" >"$PREFIX.call-checks.json" || exit 2
  mesh_checks "$ID" "$PREFIX.call-checks.json"
  exit 0
fi
python3 "$HERE/terminal.py" screen "$CALLEE.target" >"$PREFIX.chooser-before" || exit 2
python3 "$HERE/fixtures/chat-mesh/engine-birth.py" "$HERE" "$CALLEE" "$PREFIX.birth-before" || exit 2
mesh_mcp "$CALLER" call "$(jq -nc --arg target "$(jq -r '.session_id' "$CALLEE")" '{target:$target,message:"agentless-protected-probe"}')" "$PREFIX.call" || exit 2
assert "$ID" 6 "$(jq -r '.code' "$PREFIX.call.result.json")" "$PREFIX.call.result.json"
assert "$ID" false "$(jq -r '.typed//false' "$PREFIX.call.result.json")" "$PREFIX.call.result.json"
assert "$ID" 'ABORT: Herdr composer is not empty and ready; nothing was typed, existing draft/menu preserved' \
  "$(jq -r '.message' "$PREFIX.call.result.json")" "$PREFIX.call.result.json"
python3 "$HERE/terminal.py" screen "$CALLEE.target" >"$PREFIX.chooser-after" || exit 2
assert "$ID" "$(cat "$PREFIX.chooser-before")" "$(cat "$PREFIX.chooser-after")" "$PREFIX.chooser-after"
python3 "$HERE/fixtures/chat-mesh/engine-birth.py" "$HERE" "$CALLEE" "$PREFIX.birth-after" || exit 2
assert "$ID" "$(cat "$PREFIX.birth-before")" "$(cat "$PREFIX.birth-after")" "$PREFIX.birth-after"
observe || exit 2
cp "$JOURNEY_OBSERVATION" "$PREFIX.held-observation.json"
