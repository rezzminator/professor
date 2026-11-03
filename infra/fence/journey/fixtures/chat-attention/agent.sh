# Native generated identities are compared independently with the reporter's children.
_attention_generated_child() {
  local before="$1" active="$2" prefix="$ARTIFACT/$3"
  python3 "$FIXTURE/state.py" sidechains "$CHAT" "$prefix-sidechains-after.json" || return 2
  python3 "$FIXTURE/state.py" new-agents "$before" "$prefix-sidechains-after.json" \
    "$prefix-agent-ids.json" || return 2
  assert "$ID" 1 "$(jq 'length' "$prefix-agent-ids.json")" "$prefix-agent-ids.json"
  assert "$ID" "$(jq -c '.' "$prefix-agent-ids.json")" "$(jq -c '.active_child_ids' "$active")" "$active"
}
_attention_agent() {
  python3 "$FIXTURE/state.py" sidechains "$CHAT" "$ARTIFACT/sidechains-before.json" || return 2
  _attention_begin agent 'start the opted-in researcher' && _attention_wait working || return "$?"
  poll opted-in-child-live 10 _attention_activity 1 "$ARTIFACT/agent-working.json" || return "$?"
  _attention_generated_child "$ARTIFACT/sidechains-before.json" "$ARTIFACT/agent-working.json" opted-in || return "$?"
  assert "$ID" "$SID" "$(jq -r '.session_id' "$ARTIFACT/agent-working.json")" "$ARTIFACT/agent-working.json"
  rm "$HOLD" || return 2
  _attention_wait idle || return "$?"
  poll opted-in-child-finished 10 _attention_activity 0 "$ARTIFACT/agent-idle.json" || return "$?"
  assert "$ID" '[]' "$(jq -c '.active_child_ids' "$ARTIFACT/agent-idle.json")" "$ARTIFACT/agent-idle.json"
}
