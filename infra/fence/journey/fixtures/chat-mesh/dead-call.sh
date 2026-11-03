#!/usr/bin/env bash
# The caller's engine owns the public CLI while faults.py owns death/restoration.
set -uo pipefail
HERE="$1"; CALLER="$2"; TARGET_ID="$3"; PREFIX="$4"; ID="$5"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
export JOURNEY_TARGET="$CALLER.target"
mesh_shell "$CALLER" "$PREFIX.dead" pfm chat call "$TARGET_ID" mesh-dead-probe || exit 2
poll dead-cli-receipt 30 mesh_command_ready || exit 2
mesh_input_finish "$PREFIX.dead" || exit 2
assert "$ID" 3 "$(cat "$PREFIX.dead.status")" "$PREFIX.dead.status"
printf -v EXPECTED '"%s" is dead (engine cx, session %s); revive it with: pfm chat open %s' "$TARGET_ID" "$TARGET_ID" "$TARGET_ID"
assert "$ID" "$EXPECTED" "$(cat "$PREFIX.dead.out")" "$PREFIX.dead.out"
pfm chat read "$TARGET_ID" --tail 1 --json >"$PREFIX.dead-transcript-after.json" || exit 2
assert "$ID" "$(cat "$PREFIX.dead-transcript-before.json")" "$(cat "$PREFIX.dead-transcript-after.json")" "$PREFIX.dead-transcript-after.json"
