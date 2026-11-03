# The macOS lane observes native detection; source routing is pinned at its Go door.
_attention_no_claude() {
  python3 "$HERE/terminal.py" read "$1" >"$2-terminal.json" || return 2
  python3 "$FIXTURE/state.py" engine-births "$2-terminal.json" "$2-births.json" || return 2
  [ "$(jq 'length' "$2-births.json")" -eq 0 ]
}
_attention_native_receipt() { test -s "$ARTIFACT/$1.status"; }
_attention_native() {
  [ "${NATIVE_OWNER_PREPARED:-0}" -eq 1 ] || {
    _attention_unavailable 'native restore owner shell prelude failed'
    return 3
  }
  python3 "$HERE/terminal.py" read "$JOURNEY_TARGET" >"$ARTIFACT/native-before-terminal.json" || return 2
  python3 "$FIXTURE/state.py" engine-births "$ARTIFACT/native-before-terminal.json" "$ARTIFACT/native-before-births.json" || return 2
  assert "$ID" 1 "$(jq 'length' "$ARTIFACT/native-before-births.json")" "$ARTIFACT/native-before-births.json"
  assert "$ID" 'n/a:native-pane' "$(jq -r '.shield' "$ARTIFACT/native-before-terminal.json")" "$ARTIFACT/native-before-terminal.json"
  assert "$ID" '' "$(jq -r '.[0].environment.TMUX // ""' "$ARTIFACT/native-before-births.json")" "$ARTIFACT/native-before-births.json"
  python3 "$FIXTURE/state.py" native "$JOURNEY_TARGET" "$ARTIFACT/native-before.json" || return 2
  jq -S 'del(.claude_working)' "$ARTIFACT/native-before.json" >"$ARTIFACT/native-before-detection.json" || return 2
  golden "$ID" chat-attention/native-idle-detection.json "$ARTIFACT/native-before-detection.json" || return "$?"
  _attention_run turn-works-then-idles || return "$?"
  cp "$ARTIFACT/idle-bundle.json" "$ARTIFACT/native-idle-before-end.json" || return 2
  python3 "$HERE/terminal.py" receipt "$JOURNEY_ROOT/attention-native-owner.json" \
    "$ARTIFACT/native-end" pfm chat end "$SID" || return 2
  poll native-end-receipt 10 _attention_native_receipt native-end || return "$?"
  assert "$ID" 0 "$(cat "$ARTIFACT/native-end.status")" "$ARTIFACT/native-end.status"
  poll native-engine-gone 10 _attention_no_claude "$JOURNEY_TARGET" "$ARTIFACT/native-ended" || return "$?"
  python3 "$FIXTURE/state.py" native "$JOURNEY_TARGET" "$ARTIFACT/native-ended.json" || return 2
  assert "$ID" false "$(jq -r '.claude_working' "$ARTIFACT/native-ended.json")" "$ARTIFACT/native-ended.json"
  python3 "$FIXTURE/state.py" ended-bundle "$CHAT" "$ARTIFACT/native-idle-before-end.json" \
    "$JOURNEY_TARGET" "$ARTIFACT/native-ended-bundle.json" || return 2
  assert "$ID" '[]' "$(jq -c '.errors' "$ARTIFACT/native-ended-bundle.json")" "$ARTIFACT/native-ended-bundle.json"
  python3 "$HERE/terminal.py" receipt "$JOURNEY_ROOT/attention-native-owner.json" \
    "$ARTIFACT/native-restore" pfm chat open --restored "$SID" || return 2
  poll native-restore-receipt 10 _attention_native_receipt native-restore || return "$?"
  assert "$ID" 0 "$(cat "$ARTIFACT/native-restore.status")" "$ARTIFACT/native-restore.status"
  poll native-restored-seat 20 lane_refresh || return "$?"
  ATTENTION_BEFORE=''
  _attention_wait idle || return "$?"
  python3 "$HERE/fixtures/chat-reload/projection.py" attention-invariants \
    "$ARTIFACT/native-idle-before-end.json" "$ARTIFACT/idle-bundle.json" "$SID" claude restore \
    >"$ARTIFACT/native-restored-invariants.json" || return 2
  lane_assert_checks "$ARTIFACT/native-restored-invariants.json" || return "$?"
  assert "$ID" "$(jq -r '.terminal.pane' "$CHAT")" \
    "$(jq -r '.terminal.identity.pane' "$ARTIFACT/idle-bundle.json")" "$ARTIFACT/idle-bundle.json"
  assert "$ID" "$(jq -r --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .status.session_id' "$ARTIFACT/native-idle-before-end.json")" \
    "$SID" "$CHAT"
  python3 "$HERE/terminal.py" read "$JOURNEY_TARGET" >"$ARTIFACT/native-restored-terminal.json" || return 2
  python3 "$FIXTURE/state.py" engine-births "$ARTIFACT/native-restored-terminal.json" \
    "$ARTIFACT/native-restored-births.json" || return 2
  assert "$ID" 1 "$(jq 'length' "$ARTIFACT/native-restored-births.json")" "$ARTIFACT/native-restored-births.json"
  assert "$ID" false "$(jq --slurpfile before "$ARTIFACT/native-before-births.json" \
    'map({pid,start}) == ($before[0] | map({pid,start}))' "$ARTIFACT/native-restored-births.json")" "$ARTIFACT/native-restored-births.json"
  python3 "$FIXTURE/state.py" native "$JOURNEY_TARGET" "$ARTIFACT/native-restored.json" || return 2
  jq -S 'del(.claude_working)' "$ARTIFACT/native-restored.json" >"$ARTIFACT/native-restored-detection.json" || return 2
  golden "$ID" chat-attention/native-idle-detection.json "$ARTIFACT/native-restored-detection.json"
}
