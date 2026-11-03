#!/usr/bin/env bash
# Each storm child retains its own scenario and actual carry identity.
_birth_storm_prepare() {
  local index origin JOURNEY_BRANCH="$BRANCH+project+claude"
  STORM_BRANCH="$JOURNEY_BRANCH"
  PROJECT=cobalt; WORKSPACE=cobalt; CWD="$HOME/work/cobalt"; START_CWD="$CWD"
  EXPECTED_WORKSPACE="$(jq -er '.workspaces.cobalt.id' "$JOURNEY_CARRY")" || return 2
  STORM_TARGETS=()
  for index in {1..8}; do
    origin="storm-of-births-$index"
    _birth_prepare "$origin" || return 2
    STORM_TARGETS+=("$SHELL_TARGET")
  done
}
_birth_storm() {
  local id="$1" index origin directory JOURNEY_BRANCH="$STORM_BRANCH" ENTRY=new-from-cli
  ENGINE=claude
  python3 "$FIXTURE/burst.py" "$JOURNEY_OUT/beats/$PFM_BEAT/burst.json" "${STORM_TARGETS[@]}" || {
    fail 'eight owner submissions did not complete'; return 1;
  }
  assert "$id" true "$(jq '.span_ms <= 1000' "$JOURNEY_OUT/beats/$PFM_BEAT/burst.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/burst.json"
  for index in {1..8}; do
    origin="storm-of-births-$index"
    SHELL_TARGET="${STORM_TARGETS[$((index - 1))]}"
    directory="${SHELL_TARGET%/*}"; RECEIPT="$directory/cli"
    poll "storm-chat-$index" 20 _birth_capture "$origin" || { fail "storm child $index did not register"; return 1; }
    poll "storm-receipt-$index" 5 _birth_receipt_ready || { fail "storm child $index did not return"; return 1; }
    assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    poll "storm-idle-$index" 15 _birth_snapshot_ready || { fail "storm child $index did not settle"; return 1; }
    observe || { fail "storm child $index bundle could not be read"; return 1; }
    _birth_assert "$id" "$origin" || { fail "storm child $index projection differs"; return 1; }
    cp "$CHAT" "$JOURNEY_OUT/beats/$PFM_BEAT/chat-$index.json" || return 2
  done
  assert "$id" '["storm-of-births-1","storm-of-births-2","storm-of-births-3","storm-of-births-4","storm-of-births-5","storm-of-births-6","storm-of-births-7","storm-of-births-8"]' \
    "$(jq -cs '[.[].origin] | sort' "$JOURNEY_OUT/beats/$PFM_BEAT"/chat-*.json)" "$JOURNEY_OUT/beats/$PFM_BEAT"
  for field in session_id terminal; do
    assert "$id" '[]' "$(jq -cs --arg field "$field" 'group_by(.[$field]) | map(select(length > 1) | .[0][$field])' \
      "$JOURNEY_OUT/beats/$PFM_BEAT"/chat-*.json)" "$JOURNEY_OUT/beats/$PFM_BEAT"
  done
  directory="${STORM_TARGETS[0]%/*}"
  python3 "$FIXTURE/projection.py" preserved "$JOURNEY_OBSERVATION" "$directory/preserved-ids.json" >"$directory/preserved-after.json" || return 2
  assert "$id" "$(cat "$directory/preserved-before-projection.json")" "$(cat "$directory/preserved-after.json")" "$directory/preserved-after.json"
  assert "$id" '["chat.new@typed","chat.new@typed","chat.new@typed","chat.new@typed","chat.new@typed","chat.new@typed","chat.new@typed","chat.new@typed"]' \
    "$(jq -c '[.activity.attributed[] | select(.action == "chat.new" and .door == "typed") | .action + "@" + .door]' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  pass
}
