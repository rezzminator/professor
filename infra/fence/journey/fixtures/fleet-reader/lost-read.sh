#!/usr/bin/env bash
# Runs inside the shared killed-process scope; the parent owns restoration.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
ID="$1" PREFIX="$2" OWNER="$3" SCRATCH="$4" MODE="${5:-list}"
export JOURNEY_TARGET="$OWNER"
mesh_command "$OWNER" "$PREFIX.lost-list" pfm ls --all --plain || exit 2
assert "$ID" '["pfm","ls","--all","--plain"]' "$(jq -c '.argv' "$PREFIX.lost-list.dispatch.json")" "owner-input:$PREFIX.lost-list"
assert "$ID" 0 "$(cat "$PREFIX.lost-list.status")" "$PREFIX.lost-list.status"
if [ "$MODE" = dead ]; then
  python3 "$HERE/fixtures/fleet-reader/picker.py" "$OWNER" "$SCRATCH" "$PREFIX" dead || exit 2
  assert "$ID" retained-over-10s "$(jq -r 'if .elapsed_ms>=10000 and .elapsed_ms<13000 then "retained-over-10s" else .elapsed_ms end' "$PREFIX.picker.json")" "$PREFIX.picker.json"
  assert "$ID" "$(jq -c '["session",.session_id]' "$SCRATCH")" "$(jq -c '.selected' "$PREFIX.picker.json")" "$PREFIX.picker.json"
fi
observe || exit 2
cp "$JOURNEY_OBSERVATION" "$PREFIX.lost-observation.json"
python3 "$HERE/fixtures/fleet-reader/projection.py" lost "$PREFIX.lost-list.out" "$JOURNEY_OBSERVATION" "$SCRATCH" >"$PREFIX.lost-checks.json" || exit 2
mesh_checks "$ID" "$PREFIX.lost-checks.json"
if [ "$MODE" = dead ]; then
  mesh_action "$ID" picker.open typed
  python3 "$HERE/terminal.py" keys "$OWNER" escape || exit 2
fi
