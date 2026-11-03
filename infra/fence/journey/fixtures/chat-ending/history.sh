#!/usr/bin/env bash
# Each history operation starts from the actual predecessor retained in carry.
_ending_branch_ready() {
  local session
  session="$(python3 "$HERE/fixtures/chat-ending/history.py" branch-session "$RECEIPT")" || return "$?"
  python3 "$HERE/chat.py" adopt-branch "$CHAT" "$HISTORY_ORIGIN" "$session" "$RECEIPT" >"$HISTORY_CHAT"
}

_ending_branch() {
  local slug="${1:-branch-after-end}" id=chat.branch-cli name before parent_history child_history cwd
  local arguments=()
  HISTORY_ORIGIN="$slug" HISTORY_CHAT="$END_ROOT/${JOURNEY_BRANCH//+/-}-$slug.json"
  if ! beat "$slug" "$id"; then return; fi
  _ending_select new-typed || { blocked "chat-ending/history-after-end@$JOURNEY_BRANCH"; return; }
  before="$JOURNEY_OUT/beats/$PFM_BEAT/before.json"
  parent_history="$JOURNEY_OUT/beats/$PFM_BEAT/parent-transcript.json"
  child_history="$JOURNEY_OUT/beats/$PFM_BEAT/child-transcript.json"
  python3 "$HERE/chat.py" snapshot "$OWNER" "$before" || { fail 'branch parent observation unreadable'; return; }
  if [ "$ENGINE" != opencode ]; then
    python3 "$HERE/fixtures/chat-ending/history.py" transcript "$CHAT" "$parent_history" || { fail 'branch parent transcript unreadable'; return; }
  fi
  name="ending-branch-${PFM_RUN}-${JOURNEY_BRANCH//+/-}-$slug"
  cwd="$(jq -er --arg sid "$(jq -r '.session_id' "$CHAT")" '.chats[] | select(.status.session_id == $sid) | .status.cwd' "$before")" || { fail 'branch parent cwd missing'; return; }
  arguments=(pfm chat branch --engine "$ENGINE" --session-id "$(jq -r '.session_id' "$CHAT")" --name "$name" --cwd "$cwd")
  case "$slug" in
    forecast-spawn-request-*)
      assert "$id" 2 "$(jq '.account' "$CHAT")" "$CHAT"
      ending_command "$OWNER" codex-home-unset unset CODEX_HOME || { fail 'owner shell could not unset inherited Codex home'; return; } ;;
  esac
  if ! ending_command "$OWNER" branch-environment export "MOCK_ENGINE_SCENARIO=$(jq -r '.scenario' "$CHAT")" ||
     ! ending_command "$OWNER" branch "${arguments[@]}"; then
    fail 'public branch receipt missing'; return
  fi
  ending_invocation "$id" "$RECEIPT" "${arguments[@]}"
  if [ "$ENGINE" = opencode ]; then
    assert "$id" 0 "$(cat "$RECEIPT.status")" product-gap:opencode-branch
    assert "$id" 'pfm chat branch: engine must be claude or codex: <nil>' "$(cat "$RECEIPT.out")" "$RECEIPT.out"
    observe
    ending_finish product-gap:opencode-branch 'public branch supports Claude and Codex only'
    return
  fi
  assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
  if poll branch-actual-session 30 _ending_branch_ready &&
     python3 "$HERE/fixtures/chat-ending/history.py" transcript "$HISTORY_CHAT" "$child_history"; then
    observe
    if python3 "$HERE/fixtures/chat-ending/history.py" branch-checks "$before" "$JOURNEY_OBSERVATION" "$CHAT" "$HISTORY_CHAT" "$parent_history" "$child_history" "$END_ROOT/branch-checks.json"; then
      ending_assertions "$id" "$END_ROOT/branch-checks.json"
      pass
    else fail 'branch lineage projection unreadable'; fi
  else fail 'successful branch did not publish its actual child identity and transcript'; fi
}

_ending_recover() {
  local id=chat.recover-cli before source sid
  if ! beat recover-after-end "$id"; then return; fi
  if [ "$ENGINE" = opencode ]; then
    _ending_select new-typed || { blocked "chat-birth/new-typed@$JOURNEY_BRANCH"; return; }
  else
    _ending_select branch-after-end || { blocked "chat-ending/branch-after-end@$JOURNEY_BRANCH"; return; }
  fi
  before="$JOURNEY_OUT/beats/$PFM_BEAT/before.json"
  python3 "$HERE/chat.py" snapshot "$OWNER" "$before" || { fail 'recovery predecessor observation unreadable'; return; }
  sid="$(jq -r '.session_id' "$CHAT")" source="$sid"
  if [ "$ENGINE" = codex ]; then
    python3 "$HERE/fixtures/chat-ending/history.py" transcript "$CHAT" "$END_ROOT/recover-transcript.json" || { fail 'Codex recovery transcript unreadable'; return; }
    source="$(jq -er '.path' "$END_ROOT/recover-transcript.json")"
  fi
  ending_command "$OWNER" recover pfm chat recover "$source" || { fail 'public recovery receipt missing'; return; }
  ending_invocation "$id" "$RECEIPT" pfm chat recover "$source"
  if [ "$ENGINE" = codex ]; then
    assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    python3 "$HERE/fixtures/chat-ending/history.py" recovery "$RECEIPT" "$END_ROOT/recovered.json" || { fail 'recovery files or native thread identity unreadable'; return; }
    assert "$id" "$sid" "$(jq -r '.session_id' "$END_ROOT/recovered.json")" "$END_ROOT/recovered.json"
  else
    assert "$id" 0 "$(cat "$RECEIPT.status")" product-gap:recover-process
  fi
  observe
  assert "$id" true "$(python3 "$HERE/fixtures/chat-ending/history.py" recovery-birth "$before" "$JOURNEY_OBSERVATION" "$CHAT")" product-gap:recover-process
  ending_finish product-gap:recover-process 'recover emits Codex recovery files; it does not launch a recovered process or support other engines'
}

_ending_archive() {
  local id=ops.archive before sid origin=branch-after-end
  [ "$ENGINE" != opencode ] || origin=new-typed
  _ending_select "$origin" || return 2
  before="$END_ROOT/archive-prelude.json"
  python3 "$HERE/chat.py" snapshot "$OWNER" "$before" &&
    python3 "$HERE/fixtures/chat-ending/archive.py" prepare "$CHAT" "$OWNER" "$before" "$END_ROOT/archive-policy.json" || return 2
  if ! beat archive-after-end "$id"; then return; fi
  before="$JOURNEY_OUT/beats/$PFM_BEAT/before.json"
  python3 "$HERE/chat.py" snapshot "$OWNER" "$before" || { fail 'archive baseline after policy prelude unreadable'; return; }
  sid="$(jq -r '.session_id' "$CHAT")"
  if [ "$ENGINE" != opencode ]; then
    python3 "$HERE/fixtures/chat-ending/history.py" transcript "$CHAT" "$END_ROOT/archive-transcript.json" || { fail 'archive candidate transcript unreadable'; return; }
  fi
  ending_command "$OWNER" kill pfm chat kill "$sid" && poll archive-candidate-ended 30 _ending_state dead &&
    python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" ended "$before" killed || { fail 'archive candidate did not end through public kill'; return; }
  ending_command "$OWNER" archive pfm archive --apply || { fail 'public archive receipt missing'; return; }
  ending_invocation "$id" "$RECEIPT" pfm archive --apply
  assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
  if [ "$ENGINE" = opencode ]; then
    observe
    assert "$id" '[]' "$(jq -c --arg sid "$sid" '[.chats[].stored.hidden[] | select(.uuid == $sid) | .uuid]' "$JOURNEY_OBSERVATION")" product-gap:opencode-archive
    ending_finish product-gap:opencode-archive 'OpenCode single-session file archive is unsupported'
    return
  fi
  python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" archived "$before" archived || { fail 'archived lifecycle evidence unavailable'; return; }
  observe
  if python3 "$HERE/fixtures/chat-ending/archive.py" checks "$before" "$JOURNEY_OBSERVATION" "$CHAT" "$END_ROOT/archive-transcript.json" "$END_ROOT/archive-checks.json"; then
    ending_assertions "$id" "$END_ROOT/archive-checks.json"
    pass
  else fail 'archive bytes and peer projection unreadable'; fi
}
