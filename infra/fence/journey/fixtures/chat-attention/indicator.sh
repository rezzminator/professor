# Auxiliary linked worktrees keep birth's carried directories untouched.
_attention_prepare_indicators() {
  local row ids parent forecast factors label
  INDICATOR_BASE_PREPARED=0 INDICATOR_C33_PREPARED=0
  lane_rows >"$JOURNEY_ROOT/attention-indicator-setup-rows.txt" || return 2
  while IFS='|' read -r row ids parent forecast factors; do
    case "$ids" in
      herdr.indicators-worktree-removed)
        label=base
        if [ "$forecast" = darwin-linux-launchd-systemd-proc-sysctl-setsid/C33 ]; then label=c33; fi
        if python3 "$FIXTURE/state.py" indicator-setup "$JOURNEY_BRANCH" "$label" \
          "$JOURNEY_ROOT/attention-indicator-$label.json"; then
          case "$label" in base) INDICATOR_BASE_PREPARED=1 ;; c33) INDICATOR_C33_PREPARED=1 ;; esac
        fi ;;
    esac
  done <"$JOURNEY_ROOT/attention-indicator-setup-rows.txt"
}
_attention_indicator() {
  local label="$1" prepared=0 rc=0 setup="$JOURNEY_ROOT/attention-indicator-$1.json"
  case "$label" in base) prepared="$INDICATOR_BASE_PREPARED" ;; c33) prepared="$INDICATOR_C33_PREPARED" ;; esac
  [ "$prepared" -eq 1 ] || { _attention_unavailable "linked worktree prelude failed: $label"; return 3; }
  _attention_indicator_scratch "$setup" || rc=$?
  if [ "$rc" -eq 0 ]; then
    export ID ARTIFACT CHAT CHAT_BRANCH CHAT_ORIGIN PICKER SID NAME ATTENTION_BEFORE ATTENTION_DISPLAY
    GAP_SOURCE=product-gap:working-blink
    GAP_REASON='working indicator remains continuously present rather than blinking'
    python3 "$FIXTURE/state.py" indicator-live "$setup" || rc=$?
    if [ "$rc" -eq 0 ]; then
      assert "$ID" "$(jq -r '.worktree' "$setup")" \
        "$(jq -r '.cwd' "$ARTIFACT/indicator-startup.json")" "$ARTIFACT/indicator-startup.json"
      assert "$ID" '' "$(jq -r '.stderr' "$ARTIFACT/indicator-exit.json")" "$ARTIFACT/indicator-exit.json"
      assert "$ID" 0 "$(jq -r '.status' "$ARTIFACT/indicator-exit.json")" "$ARTIFACT/indicator-exit.json"
    fi
  fi
  _attention_indicator_cleanup "$setup" || return 2
  if [ "$rc" -eq 0 ]; then _attention_observe idle || return "$?"; fi
  return "$rc"
}
_attention_indicator_scratch() {
  local previous_branch="$CHAT_BRANCH" previous_origin="$CHAT_ORIGIN" rc=0
  cp "$CHAT" "$ARTIFACT/indicator-project-chat.json" || return 2
  CHAT_BRANCH="$(jq -r '.chat.branch' "$1")" CHAT_ORIGIN="$(jq -r '.chat.role // .chat.origin' "$1")"
  lane_refresh || return 2
  cp "$CHAT" "$ARTIFACT/indicator-scratch-chat.json" || return 2
  cp "$JOURNEY_TARGET" "$ARTIFACT/indicator-scratch-target.json" || return 2
  observe || return 2
  cp "$JOURNEY_OBSERVATION" "$ARTIFACT/indicator-scratch-before-end.json" || return 2
  _attention_run turn-works-then-idles && _attention_begin question 'keep the scratch indicator live' \
    && _attention_wait blocked || rc=$?
  CHAT_BRANCH="$previous_branch" CHAT_ORIGIN="$previous_origin"
  cp "$ARTIFACT/indicator-project-chat.json" "$CHAT" || return 2
  lane_refresh || return 2
  return "$rc"
}
_attention_indicator_project() {
  local metrics="$ARTIFACT/indicator-accounting.json" peer="${CHAT_BRANCH%+claude}+opencode"
  python3 "$FIXTURE/state.py" peer-idle "$peer" "$ARTIFACT/indicator-opencode-before.json" || return 2
  assert "$ID" idle "$(jq -r '.state' "$ARTIFACT/indicator-opencode-before.json")" "$ARTIFACT/indicator-opencode-before.json"
  assert "$ID" oc "$(jq -r '.engine' "$ARTIFACT/indicator-opencode-before.json")" "$ARTIFACT/indicator-opencode-before.json"
  python3 "$FIXTURE/state.py" indicator "$ARTIFACT/indicator-before.json" "$INDICATOR_PID" || return 2
  assert "$ID" "$(jq -c '.birth | {pid,start}' "$ARTIFACT/indicator-startup.json")" \
    "$(jq -c '.daemon | {pid,start}' "$ARTIFACT/indicator-before.json")" "$ARTIFACT/indicator-before.json"
  python3 "$FIXTURE/state.py" process-cwd "$INDICATOR_PID" "$ARTIFACT/indicator-running-cwd.json" || return 2
  assert "$ID" / "$(jq -r '.cwd' "$ARTIFACT/indicator-running-cwd.json")" "$ARTIFACT/indicator-running-cwd.json"
  _attention_run turn-works-then-idles || return "$?"
  poll project-has-no-ghost-child 10 _attention_activity 0 "$ARTIFACT/indicator-project-idle.json" || return "$?"
  assert "$ID" '[]' "$(jq -c '.active_child_ids' "$ARTIFACT/indicator-project-idle.json")" "$ARTIFACT/indicator-project-idle.json"
  assert "$ID" idle "$(jq -r '.state' "$ARTIFACT/indicator-project-idle.json")" "$ARTIFACT/indicator-project-idle.json"
  python3 "$FIXTURE/state.py" window "$ARTIFACT/indicator-before.json" || return 2
  python3 "$FIXTURE/state.py" indicator "$ARTIFACT/indicator-after.json" "$INDICATOR_PID" || return 2
  python3 "$FIXTURE/state.py" accounting "$ARTIFACT/indicator-before.json" \
    "$ARTIFACT/indicator-after.json" "$metrics" || return 2
  assert "$ID" true "$(jq '.checks.same_birth' "$metrics")" "$metrics"
  assert "$ID" true "$(jq '.checks.cpu_under_limit' "$metrics")" "$metrics"
  assert "$ID" true "$(jq '.checks.write_under_limit' "$metrics")" "$metrics"
  python3 "$FIXTURE/state.py" peer-idle "$peer" "$ARTIFACT/indicator-opencode-after.json" || return 2
  assert "$ID" idle "$(jq -r '.state' "$ARTIFACT/indicator-opencode-after.json")" "$ARTIFACT/indicator-opencode-after.json"
  assert "$ID" "$(jq -r '.session_id' "$ARTIFACT/indicator-opencode-before.json")" \
    "$(jq -r '.session_id' "$ARTIFACT/indicator-opencode-after.json")" "$ARTIFACT/indicator-opencode-after.json"
  _attention_activity 0 "$ARTIFACT/indicator-final-activity.json" || return "$?"
  assert "$ID" idle "$(jq -r '.state' "$ARTIFACT/indicator-final-activity.json")" "$ARTIFACT/indicator-final-activity.json"
  assert "$ID" null "$(jq -c '.blocked_owner' "$ARTIFACT/indicator-final-activity.json")" "$ARTIFACT/indicator-final-activity.json"
  assert "$ID" true "$(jq --slurpfile prior "$ARTIFACT/indicator-project-idle.json" \
    '.event_sequence >= $prior[0].event_sequence' "$ARTIFACT/indicator-final-activity.json")" "$ARTIFACT/indicator-final-activity.json"
  _attention_observe idle || return "$?"
  assert "$ID" '[]' "$(jq -c '[.activity.records[] | select(.level=="ERROR" and ((.op // "") | contains("indicator")))]' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
}
_attention_indicator_end_receipt() { test -s "$ARTIFACT/indicator-end.status"; }
_attention_indicator_cleanup() {
  local setup="$1" scratch="$ARTIFACT/indicator-scratch-chat.json"
  [ ! -s "$ARTIFACT/indicator-scratch-ended-chat.json" ] || return 0
  jq '.owner' "$setup" >"$ARTIFACT/indicator-owner.json" || return 2
  python3 "$HERE/terminal.py" receipt "$ARTIFACT/indicator-owner.json" \
    "$ARTIFACT/indicator-end" pfm chat end "$(jq -r '.session_id' "$scratch")" || return 2
  poll indicator-scratch-end-receipt 10 _attention_indicator_end_receipt || return "$?"
  assert "$ID" 0 "$(cat "$ARTIFACT/indicator-end.status")" "$ARTIFACT/indicator-end.status"
  poll indicator-scratch-engine-gone 10 _attention_no_claude "$ARTIFACT/indicator-scratch-target.json" "$ARTIFACT/indicator-ended" || return "$?"
  python3 "$HERE/chat.py" expect-ended "$(jq -r '.branch' "$scratch")" \
    "$(jq -r '.role // .origin' "$scratch")" "$ARTIFACT/indicator-scratch-before-end.json" \
    >"$ARTIFACT/indicator-scratch-ended-chat.json"
}
_attention_indicator_actions() {
  local rc
  _attention_indicator_project; rc=$?
  _attention_indicator_cleanup "$1" || return 2
  return "$rc"
}
