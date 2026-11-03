# Native tmux ownership and the independently observed Herdr display stay distinct.
_attention_capture_legacy() {
  python3 "$HERE/chat.py" capture "$JOURNEY_ROOT/attention-legacy-display.json" \
    "$LEGACY_BRANCH" forecast-legacy-codex "$JOURNEY_ROOT/attention-legacy-before.json" \
    >"$JOURNEY_ROOT/attention-legacy-chat.next" || return "$?"
  mv "$JOURNEY_ROOT/attention-legacy-chat.next" "$JOURNEY_ROOT/attention-legacy-chat.json"
}
_attention_prepare_legacy() {
  local rc diagnostic diagnostic_rc=0
  LEGACY_BRANCH="$JOURNEY_OS+tmux+project+codex"
  python3 "$HERE/chat.py" shell "$JOURNEY_BRANCH" "$JOURNEY_ROOT/attention-legacy-display.json" >/dev/null || return 2
  python3 "$HERE/chat.py" scenario "$JOURNEY_ROOT/attention-legacy-display.json" \
    "$LEGACY_BRANCH" forecast-legacy-codex "$FIXTURE/legacy-codex.json" >/dev/null || return 2
  python3 "$HERE/chat.py" before "$JOURNEY_ROOT/attention-legacy-display.json" \
    "$JOURNEY_ROOT/attention-legacy-before.json" >/dev/null || return 2
  python3 "$HERE/terminal.py" command "$JOURNEY_ROOT/attention-legacy-display.json" \
    pfm chat new --engine cx --backend tmux --attach \
    --account 1 --cwd "$HOME/work/atlas" --name "journey-legacy-codex-$PFM_RUN" || return 2
  poll legacy-codex-native-seat 30 _attention_capture_legacy || {
    rc=$?
    diagnostic="$JOURNEY_ROOT/branches/$LEGACY_BRANCH/forecast-legacy-codex/launch-display.json"
    python3 "$HERE/terminal.py" read "$JOURNEY_ROOT/attention-legacy-display.json" \
      >"$diagnostic" || diagnostic_rc=$?
    if [ "$diagnostic_rc" -ne 0 ]; then
      printf 'ATTENTION-CAPTURE-DIAGNOSTIC-UNREADABLE: exit=%s artifact=%s\n' "$diagnostic_rc" "$diagnostic" >&2
    fi
    return "$rc"
  }
  python3 "$HERE/chat.py" shell "$JOURNEY_BRANCH" "$JOURNEY_ROOT/attention-legacy-owner.json" >/dev/null
}
_attention_legacy_end_receipt() { test -s "$ARTIFACT/legacy-end.status"; }
_attention_legacy_tmux() {
  [ "${LEGACY_PREPARED:-0}" -eq 1 ] || {
    _attention_unavailable 'real tmux-backed Codex prelude failed; see named setup error'
    return 3
  }
  local previous_branch="$CHAT_BRANCH" previous_origin="$CHAT_ORIGIN" rc=0
  cp "$CHAT" "$ARTIFACT/selected-before-legacy.json" || return 2
  CHAT_BRANCH="$LEGACY_BRANCH" CHAT_ORIGIN=forecast-legacy-codex
  ATTENTION_DISPLAY="$JOURNEY_ROOT/attention-legacy-display.json"
  lane_refresh || return 2
  python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" >/dev/null || return 2
  _attention_denied_begin 'deny the legacy Codex tool' || return "$?"
  assert "$ID" tmux "$(jq -r '.terminal.backend' "$CHAT")" "$CHAT"
  assert "$ID" herdr "$(jq -r '.terminal.backend' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  assert "$ID" codex "$(jq -r '.terminal.pane.agent' "$ARTIFACT/blocked-bundle.json")" "$ARTIFACT/blocked-bundle.json"
  assert "$ID" "$(jq -r '.pane' "$ATTENTION_DISPLAY")" \
    "$(jq -r '.terminal.identity.pane' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" escape || return 2
  rm "$HOLD" || return 2
  _attention_wait idle || return "$?"
  python3 "$HERE/terminal.py" receipt "$JOURNEY_ROOT/attention-legacy-owner.json" \
    "$ARTIFACT/legacy-end" pfm chat end "$SID" || return 2
  poll legacy-chat-ended 10 _attention_legacy_end_receipt || return "$?"
  assert "$ID" 0 "$(cat "$ARTIFACT/legacy-end.status")" "$ARTIFACT/legacy-end.status"
  poll legacy-native-engine-dead 10 lane_state dead "$ARTIFACT/legacy-dead.json" || return "$?"
  python3 "$HERE/chat.py" expect-ended "$CHAT_BRANCH" "$CHAT_ORIGIN" \
    "$ARTIFACT/idle-bundle.json" >"$JOURNEY_ROOT/attention-legacy-chat.json" || return 2
  python3 "$HERE/terminal.py" read "$ATTENTION_DISPLAY" >"$ARTIFACT/legacy-display-ended.json" || return 2
  assert "$ID" false "$(jq '.pane | has("agent")' "$ARTIFACT/legacy-display-ended.json")" "$ARTIFACT/legacy-display-ended.json"
  assert "$ID" unknown "$(jq -r '.pane.agent_status' "$ARTIFACT/legacy-display-ended.json")" "$ARTIFACT/legacy-display-ended.json"
  observe "$ATTENTION_DISPLAY" || return 2
  assert "$ID" "$(jq -c '.foreign' "$ARTIFACT/idle-bundle.json")" \
    "$(jq -c '.foreign' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  CHAT_BRANCH="$previous_branch" CHAT_ORIGIN="$previous_origin" ATTENTION_DISPLAY=''
  cp "$ARTIFACT/selected-before-legacy.json" "$CHAT" || return 2
  lane_refresh || rc=2
  return "$rc"
}
