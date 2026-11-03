#!/usr/bin/env bash
# Compose the public dead-seat CLI journey over shared fault and carry doors.
_mesh_dead() {
  local scratch="$JOURNEY_ROOT/mesh-dead-$BRANCH" sid
  assert "$ID" new-from-cli "$(jq -r '.origin' "$scratch.chat")" "$scratch.chat"
  assert "$ID" mesh-dead "$(jq -r '.role' "$scratch.chat")" "$scratch.chat"
  assert "$ID" codex "$(jq -r '.engine' "$scratch.chat")" "$scratch.chat"
  sid="$(jq -er '.session_id' "$scratch.chat")" || return 2
  poll scratch-idle 15 mesh_state "$scratch.chat" idle || return 2
  python3 "$HERE/chat.py" inline "$scratch.chat.target" "$FIXTURES/turn.json" mesh-dead-transcript || return 2
  poll scratch-seed-working 10 mesh_state "$scratch.chat" working || return 2
  poll scratch-seed-idle 15 mesh_state "$scratch.chat" idle || return 2
  pfm chat read "$sid" --tail 1 --json >"$PREFIX.dead-transcript-before.json" || return 2
  assert "$ID" '{"role":"assistant","text":"pong"}' "$(jq -c '.entries[-1]|{role,text}' "$PREFIX.dead-transcript-before.json")" "$PREFIX.dead-transcript-before.json"
  _mesh_before || return 2
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$scratch.chat" "$scratch.birth" || return 2
  jq '.owner' "$JOURNEY_CARRY" >"$scratch.owner"
  python3 "$HERE/terminal.py" screen "$scratch.owner" >"$PREFIX.owner-screen" || return 2
  if rg -Fq 'New chat' "$PREFIX.owner-screen"; then python3 "$HERE/terminal.py" keys "$scratch.owner" escape || return 2; fi
  mesh_command "$scratch.owner" "$PREFIX.owner-ready" pwd || return 2
  assert "$ID" 0 "$(cat "$PREFIX.owner-ready.status")" "$PREFIX.owner-ready.status"
  assert "$ID" "$HOME" "$(cat "$PREFIX.owner-ready.out")" "$PREFIX.owner-ready.out"
  python3 "$HERE/faults.py" killed-process "$scratch.chat" "$scratch.birth" "$scratch.owner" -- \
    bash "$FIXTURES/dead-call.sh" "$HERE" "$CALLER" "$sid" "$PREFIX" "$ID" || return 2
  poll scratch-rebound 30 _mesh_rebind_dead "$scratch" || return 2
  assert "$ID" "$sid" "$(jq -r '.session_id' "$scratch.chat")" "$scratch.chat"
  poll scratch-restored 30 mesh_state "$scratch.chat" idle || return 2
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$scratch.chat" "$scratch.restored-birth" || return 2
  assert "$ID" replacement "$(if [ "$(jq -c '{pid,start}' "$scratch.birth")" = "$(jq -c '{pid,start}' "$scratch.restored-birth")" ]; then printf original; else printf replacement; fi)" "$scratch.restored-birth"
  mesh_shell "$CALLER" "$PREFIX.revived" pfm chat call "$sid" "mesh-revived-positive mock-engine: $(jq -c . "$FIXTURES/turn.json")" || return 2
  poll revived-cli-receipt 30 mesh_command_ready || return 2
  mesh_input_finish "$PREFIX.revived" || return 2
  assert "$ID" 0 "$(cat "$PREFIX.revived.status")" "$PREFIX.revived.status"
  poll revived-answer 15 _mesh_child_answer "$sid" pong || return 2
  _mesh_finish "replaced:$scratch.chat" chat.call cli || return 2
  pass
  return 0
}

_mesh_rebind_dead() {
  python3 "$HERE/chat.py" refresh "$(jq -r '.branch' "$1.chat")" mesh-dead >"$1.chat.new" || return "$?"
  mv "$1.chat.new" "$1.chat"
  jq '.terminal' "$1.chat" >"$1.chat.target"
}

_mesh_dead_ready() { test -s "$JOURNEY_ROOT/mesh-dead-$BRANCH.chat"; }
_mesh_dead_create() {
  local scratch="$JOURNEY_ROOT/mesh-dead-$BRANCH" terminal="${BRANCH#*+}"
  terminal="${terminal%%+*}"
  python3 "$HERE/chat.py" ensure-role "${BRANCH%+*}+codex" mesh-dead "mesh-dead-$PFM_RUN-$terminal" "$FIXTURES/seed.json" >"$scratch.chat" &&
    jq '.terminal' "$scratch.chat" >"$scratch.chat.target"
}

