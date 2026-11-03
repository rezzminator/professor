#!/usr/bin/env bash
# Owner entries and assertions shared by each ending leaf.
_ending_state() {
  python3 "$HERE/fixtures/chat-ending/lifecycle.py" state "$CHAT" "$1" "$END_ROOT/state.json"
}
_ending_select() {
  local selector="$1" destination="${2:-$CHAT}" result=0
  python3 "$HERE/chat.py" select "$JOURNEY_BRANCH" "$selector" >"$destination" || result=$?
  if [ "$result" -eq 1 ]; then
    selector="ending-$selector"
    python3 "$HERE/chat.py" select "$JOURNEY_BRANCH" "$selector" >"$destination" || return "$?"
  elif [ "$result" -ne 0 ]; then return "$result"; fi
  ORIGIN="$selector"
  jq '.terminal' "$destination" >"$destination.target" || return 2
  if [ "$destination" = "$CHAT" ]; then cp "$destination.target" "$TARGET"; fi
}
_ending_existing() { _ending_select "$ORIGIN" && python3 "$HERE/chat.py" need "$JOURNEY_BRANCH" "$ORIGIN" >/dev/null; }
_ending_create() {
  local result=0
  _ending_select "$ORIGIN" || result=$?
  if [ "$result" -eq 0 ]; then
    python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" live || return 2
    python3 "$HERE/chat.py" reopen "$OWNER" "$JOURNEY_BRANCH" "$ORIGIN" || return 2
    poll ending-prelude-resumed 30 _ending_state idle || return 2
    return 0
  fi
  [ "$result" -eq 1 ] || return "$result"
  ORIGIN="ending-$ORIGIN"
  python3 "$HERE/chat.py" ensure-role "$JOURNEY_BRANCH" "$ORIGIN" "end-${JOURNEY_BRANCH//+/-}-$ORIGIN" "$HERE/fixtures/chat-birth/scenario.json" >"$CHAT"
}
_ending_picker() {
  python3 "$HERE/chat.py" pick "$OWNER" "$JOURNEY_BRANCH" "$ORIGIN"
}
_ending_resume() {
  if jq -e '.expect.visibility == "killed" or .expect.visibility == "hidden"' "$CHAT" >/dev/null; then
    ending_command "$OWNER" restore-visibility pfm chat unkill "$(jq -r '.session_id' "$CHAT")" || return 2
  fi
  python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" live || return 2
  ending_command "$OWNER" resume pfm chat open "$(jq -r '.session_id' "$CHAT")" &&
    poll ending-resumed 30 _ending_state idle &&
    python3 "$HERE/chat.py" refresh "$JOURNEY_BRANCH" "$ORIGIN" >"$CHAT" && jq '.terminal' "$CHAT" >"$TARGET"
}
_ending_mcp() {
  local prefix="$JOURNEY_OUT/beats/$PFM_BEAT/self-mcp"
  jq '.terminal' "$CHAT" >"$CHAT.target"
  if [ "$1" = exit ]; then
    mesh_mcp_start "$CHAT" "$1" "$2" "$prefix"
  else
    mesh_mcp "$CHAT" "$1" "$2" "$prefix"
  fi
}
_ending_peer_mcp() {
  local peer="$END_ROOT/peer.json" prefix="$JOURNEY_OUT/beats/$PFM_BEAT/peer-$1" origin="$ORIGIN"
  _ending_select new-from-cli "$peer" || return 2
  ORIGIN="$origin"
  mesh_mcp "$peer" "$1" "$2" "$prefix" || return 2
  assert "$id" 0 "$(jq '.code' "$prefix.result.json")" "$prefix.result.json"
}
_ending_exit() {
  python3 "$HERE/terminal.py" type "$TARGET" "$(jq -r --arg engine "$ENGINE" '.[$engine]' "$HERE/goldens/chat-ending/native-exit.json")" &&
    poll ending-native-exit 20 _ending_state dead
}
_ending_dead() {
  _ending_select "$1" || return 2
  local live="$END_ROOT/$(jq -r '.key' "$CHAT").live.json"
  if _ending_state idle; then
    python3 "$HERE/chat.py" snapshot "$OWNER" "$live" && _ending_exit &&
      python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" ended "$live"
  else _ending_state dead; fi
}
_ending_run() {
  local slug="$1" id="$2" mode="$3" action="$4" door="$5" entry="${7:-$1}" expected_state=dead sid visibility=visible live_before
  local ID="$id"
  ORIGIN="$6"
  if ! beat "$slug" "$id"; then return; fi
  if ! _ending_select "$ORIGIN"; then blocked "chat-birth/$ORIGIN@$JOURNEY_BRANCH"; return; fi
  sid="$(jq -r '.session_id' "$CHAT")"
  live_before="$END_ROOT/$(jq -r '.key' "$CHAT").live.json"
  case "$entry" in
    kill-from-cli|clear-hides-old-conversation|kill-self-refused|kill-lost-seat-from-picker)
      if ! _ending_state idle; then _ending_resume || { fail 'successive cycle could not resume its target'; return; }; fi ;;
    resume-from-picker|resume-after-mcp-kill-picker)
      if _ending_state idle; then
        python3 "$HERE/chat.py" snapshot "$OWNER" "$live_before" && _ending_exit || { fail 'resume cycle did not end its live target'; return; }
        python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" ended "$live_before" || { fail 'resume cycle death proof missing'; return; }
      fi ;;
  esac
  BEFORE="$JOURNEY_OUT/beats/$PFM_BEAT/before.json"
  if ! python3 "$HERE/chat.py" snapshot "$OWNER" "$BEFORE"; then fail 'pre-action bundle unreadable'; return; fi
  if _ending_state idle; then cp "$BEFORE" "$live_before"; fi
  case "$entry" in
    exit-typed) python3 "$HERE/terminal.py" type "$TARGET" /e ;;
    exit-native) _ending_exit ;;
    end-from-cli) ending_command "$OWNER" end pfm chat end "$sid" ;;
    exit-from-mcp) _ending_mcp exit '{}' ;;
    resume-from-picker|resume-after-mcp-kill-picker)
      _ending_picker || { fail 'inactive row was not selected'; return; }
      if [ "$TERMINAL" = tmux ]; then
        python3 "$HERE/chat.py" wizard "$OWNER" "$ENGINE" "$(jq -r '.account' "$CHAT")" "$(jq -r 'if .cache1h then "1h" else "5m" end' "$CHAT")" "$END_ROOT/wizard.json" || { fail 'resume wizard did not launch'; return; }
        if [ "$ENGINE" = claude ]; then
          assert "$id" "$(jq '.account' "$CHAT")" "$(jq '.account' "$END_ROOT/wizard.json")" "$END_ROOT/wizard.json"
          assert "$id" "$(jq -r 'if .cache1h then "1h" else "5m" end' "$CHAT")" "$(jq -r '.cache' "$END_ROOT/wizard.json")" "$END_ROOT/wizard.json"
        fi
      else
        python3 "$HERE/terminal.py" keys "$OWNER" enter || { fail 'native row did not open'; return; }
      fi
      expected_state=idle ;;
    resume-from-cli) _ending_exit && _ending_resume; expected_state=idle ;;
    resume-after-native-exit-cli|resume-after-picker-hide-cli)
      ending_command "$OWNER" unkill pfm chat unkill "$sid" && _ending_resume
      expected_state=idle ;;
    resume-after-cli-kill-mcp)
      _ending_peer_mcp unkill "$(jq -n --arg target "$sid" '{target:$target}')" &&
        _ending_peer_mcp open "$(jq -n --arg target "$sid" '{target:$target}')"
      expected_state=idle ;;
    kill-from-mcp|unkill-from-mcp)
      _ending_peer_mcp "${entry%%-from-*}" "$(jq -n --arg target "$sid" '{target:$target}')" ;;
    history-after-end) _ending_resume; expected_state=idle ;;
    kill-hides-from-picker|unkill-from-picker)
      _ending_picker && python3 "$HERE/terminal.py" keys "$OWNER" ctrl+x escape ;;
    clear-hides-old-conversation)
      python3 "$HERE/terminal.py" type "$TARGET" /clear
      expected_state=idle ;;
    kill-from-cli|kill-dead) ending_command "$OWNER" kill pfm chat kill "$sid" ;;
    unkill-from-cli) ending_command "$OWNER" unkill pfm chat unkill "$sid" ;;
    kill-self-refused)
      _ending_mcp kill '{"target":"self"}' || { fail 'self-kill request did not enter the engine'; return; }
      local refusal="$(jq -er '.records' "$CHAT")/mcp-call-kill.json" socket
      socket="$(jq -er --arg name "$(jq -r '.name' "$CHAT")" '.chats[] | select(.name == $name) | .picker[0].address.socket' "$BEFORE")" || { fail 'self-kill caller socket unreadable'; return; }
      poll self-kill-refusal 10 test -s "$refusal" || { fail 'self-kill refusal receipt missing'; return; }
      assert "$id" "$(jq -Rn --arg socket "$socket" 'input | gsub("\\{socket\\}";$socket)' <"$HERE/goldens/chat-ending/self-kill-refusal.txt")" \
        "$(python3 "$HERE/fixtures/chat-ending/mcp.py" "$refusal" message)" "$refusal"
      expected_state=idle ;;
    kill-lost-seat-from-picker)
      python3 "$HERE/fixtures/chat-ending/lost.py" "$CHAT" "$OWNER" "$BEFORE" || fail 'lost-seat fault, picker action or public restoration failed'
      return ;;
    resume-from-cli-over-ssh)
      RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/ssh-resume"
      local proof=()
      case "$slug" in forecast-*) proof=("$RECEIPT.ancestry.json") ;; esac
      python3 "$HERE/fixtures/chat-ending/ssh_session.py" "$OWNER" "$RECEIPT" "$sid" "${proof[@]}" &&
        poll ssh-resume-receipt 30 test -s "$RECEIPT.status"
      assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
      if [ "${#proof[@]}" -ne 0 ]; then
        assert "$id" true "$(jq '.ssh_pty and .foreign_environ_denied' "${proof[0]}")" "${proof[0]}"
      fi
      expected_state=idle ;;
    *) fail "unimplemented ending entry $slug"; return ;;
  esac || { fail "owner entry failed: $slug"; return; }
  if ! poll "ending-$slug-state" 30 _ending_state "$expected_state"; then fail "owner entry did not reach $expected_state"; return; fi
  case "$mode" in kill|lost) visibility=killed ;; esac
  if [ "$expected_state" = dead ]; then
    python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" ended "$live_before" "$visibility" || { fail 'ended expectation unreadable'; return; }
  else
    python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" live || { fail 'live expectation unreadable'; return; }
    python3 "$HERE/chat.py" refresh "$JOURNEY_BRANCH" "$ORIGIN" >"$END_ROOT/current.json" || { fail 'resumed identity unreadable'; return; }
  fi
  observe "$OWNER"
  if [ "$mode" = resume ]; then BEFORE="$live_before"; fi
  if python3 "$HERE/fixtures/chat-ending/projection.py" "$BEFORE" "$JOURNEY_OBSERVATION" "$CHAT" "$mode" "$END_ROOT/assertions.json"; then
    ending_assertions "$id" "$END_ROOT/assertions.json" "$mode"
    ending_action "$id" "$action" "$door"
    if [ "$TERMINAL" != tmux ] && [ "$mode" = resume ]; then
      python3 "$HERE/terminal.py" read "$OWNER" >"$END_ROOT/resumed-owner.json"
      assert "$id" '[]' "$(jq -c '[.text | scan("◖ (.+?) ◗")]' "$END_ROOT/resumed-owner.json")" "$END_ROOT/resumed-owner.json"
    fi
    ending_finish
  else fail 'lifecycle projection unreadable'; fi
}
