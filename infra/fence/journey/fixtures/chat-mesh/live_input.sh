#!/usr/bin/env bash
# Native recipient input proves the real pane's engine, independently of its transcript.
mesh_callee_input_start() {
  MESH_CALLEE_PROMPTS="$(jq -er '.records' "$CALLEE")/prompts" MESH_CALLEE_OFFSET=0
  if [ "$(jq -r '.records' "$CALLER")" = "$(jq -r '.records' "$CALLEE")" ]; then
    printf 'MESH-CALLEE-INPUT: shared recorder cannot identify the recipient process\n' >&2
    return 2
  fi
  if [ -f "$MESH_CALLEE_PROMPTS" ]; then MESH_CALLEE_OFFSET="$(wc -c <"$MESH_CALLEE_PROMPTS")"; fi
  local sender
  sender="$(python3 "$HERE/fixtures/fleet-reader/projection.py" sender "$CALLER")" || return 2
  MESH_CALLEE_EXPECTED="$MESSAGE  — $sender"
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$CALLEE" "$PREFIX.callee-birth-before.json" || return 2
  pfm chat read "$(jq -r '.session_id' "$CALLEE")" --tail 1000000 --json >"$PREFIX.callee-public-before.json"
}

mesh_callee_input_finish() {
  mesh_input_tail "$MESH_CALLEE_PROMPTS" "$MESH_CALLEE_OFFSET" "$PREFIX.callee-native-input.txt" || return 2
  assert "$ID" "$MESH_CALLEE_EXPECTED" "$(cat "$PREFIX.callee-native-input.txt")" "$PREFIX.callee-native-input.txt"
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$CALLEE" "$PREFIX.callee-birth-after.json" || return 2
  assert "$ID" "$(cat "$PREFIX.callee-birth-before.json")" "$(cat "$PREFIX.callee-birth-after.json")" "$PREFIX.callee-birth-after.json"
  pfm chat read "$(jq -r '.session_id' "$CALLEE")" --tail 1000000 --json >"$PREFIX.callee-public-after.json" || return 2
  python3 "$FIXTURES/live_input.py" "$PREFIX.callee-public-before.json" "$PREFIX.callee-public-after.json" \
    "$MESH_CALLEE_EXPECTED" "$PREFIX.callee-public-checks.json" || return 2
  mesh_checks "$ID" "$PREFIX.callee-public-checks.json"
}
