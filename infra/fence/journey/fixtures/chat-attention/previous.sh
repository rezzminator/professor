# The previous account input is frozen at launch; the current inline layer remains explicit.
_attention_previous() {
  [ "${PREVIOUS_SETTINGS_PREPARED:-0}" -eq 1 ] || {
    _attention_unavailable 'actual previous-install account settings launch failed; see named prelude error'
    return 3
  }
  local previous_origin="$CHAT_ORIGIN" rc
  cp "$CHAT" "$ARTIFACT/selected-before-previous.json" || return 2
  CHAT_ORIGIN=forecast-previous-settings
  lane_refresh || return 2
  cp "$JOURNEY_ROOT/attention-previous-settings-chat.json.launch-input.json" \
    "$ARTIFACT/previous-launch-input.json" || return 2
  assert "$ID" "$SID" "$(jq -r '.session_id' "$ARTIFACT/previous-launch-input.json")" "$ARTIFACT/previous-launch-input.json"
  assert "$ID" "$(jq -r '.accounts["1"].sha256' "$JOURNEY_ROOT/previous-install/provenance.json")" \
    "$(jq -r '.sha256' "$ARTIFACT/previous-launch-input.json")" "$ARTIFACT/previous-launch-input.json"
  assert "$ID" 1 "$(jq -r '.account' "$ARTIFACT/previous-launch-input.json")" "$ARTIFACT/previous-launch-input.json"
  assert "$ID" "$(jq -r '.pfm_version' "$JOURNEY_ROOT/previous-install/provenance.json")" \
    "$(jq -r '.pfm_version' "$ARTIFACT/previous-launch-input.json")" "$ARTIFACT/previous-launch-input.json"
  _attention_previous_agent; rc=$?
  CHAT_ORIGIN="$previous_origin"
  cp "$ARTIFACT/selected-before-previous.json" "$CHAT" || return 2
  lane_refresh || return 2
  return "$rc"
}
_attention_previous_agent() {
  MAIN_STOP_HOLD="$ARTIFACT/previous-main-stop.hold"
  printf '%s\n' held >"$MAIN_STOP_HOLD" || return 2
  python3 "$FIXTURE/state.py" sidechains "$CHAT" "$ARTIFACT/previous-sidechains-before.json" || return 2
  _attention_begin previous-agent 'research it' && _attention_wait working || return "$?"
  poll previous-child-after-main-stop 10 _attention_activity 1 "$ARTIFACT/previous-working.json" || return "$?"
  assert "$ID" true "$(jq '.main_stopped_at | type=="number" and .>0' "$ARTIFACT/previous-working.json")" "$ARTIFACT/previous-working.json"
  _attention_generated_child "$ARTIFACT/previous-sidechains-before.json" "$ARTIFACT/previous-working.json" previous || return "$?"
  rm "$MAIN_STOP_HOLD" || return 2
  _attention_wait blocked || return "$?"
  _attention_activity 1 "$ARTIFACT/previous-blocked.json" || return "$?"
  jq -S '.blocked_owner | del(.agent_id)' "$ARTIFACT/previous-blocked.json" >"$ARTIFACT/previous-blocked-tool.json" || return 2
  golden "$ID" chat-attention/blocked-tool.json "$ARTIFACT/previous-blocked-tool.json" || return "$?"
  assert "$ID" "$(jq -r '.[0]' "$ARTIFACT/previous-agent-ids.json")" \
    "$(jq -r '.blocked_owner.agent_id' "$ARTIFACT/previous-blocked.json")" "$ARTIFACT/previous-blocked.json"
  python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" enter && _attention_finish || return "$?"
  poll previous-child-stopped 10 _attention_activity 0 "$ARTIFACT/previous-idle.json" || return "$?"
  assert "$ID" null "$(jq -c '.blocked_owner' "$ARTIFACT/previous-idle.json")" "$ARTIFACT/previous-idle.json"
  assert "$ID" '[]' "$(jq -c '.active_child_ids' "$ARTIFACT/previous-idle.json")" "$ARTIFACT/previous-idle.json"
}
