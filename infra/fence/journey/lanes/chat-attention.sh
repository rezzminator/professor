#!/usr/bin/env bash
# Attention is exercised on the exact chats carried through reload.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-reload/flow.sh"
export JOURNEY_LANE=chat-attention
FIXTURE="$HERE/fixtures/chat-attention"
. "$FIXTURE/legacy.sh"
. "$FIXTURE/native.sh"
. "$FIXTURE/previous.sh"
. "$FIXTURE/indicator.sh"
. "$FIXTURE/large.sh"
. "$FIXTURE/agent.sh"
. "$FIXTURE/denied.sh"
: "${JOURNEY_OS:?runner must select linux or macos}"

_attention_observe() {
  local state="$1" backend target="${ATTENTION_DISPLAY:-$JOURNEY_TARGET}" projection_args=()
  observe "$target" || return 2
  cp "$JOURNEY_OBSERVATION" "$ARTIFACT/$state-bundle.json" || return 2
  if [ -n "${ATTENTION_BEFORE:-}" ]; then
    python3 "$HERE/fixtures/chat-reload/projection.py" attention-invariants "$ATTENTION_BEFORE" \
      "$JOURNEY_OBSERVATION" "$SID" "$(jq -r '.engine' "$CHAT")" >"$ARTIFACT/$state-invariants.json" || return 2
    lane_assert_checks "$ARTIFACT/$state-invariants.json" || return "$?"
  fi
  backend="$(jq -er '.backend' "$target")" || return 2
  if [ -n "${ATTENTION_DISPLAY:-}" ]; then
    python3 "$HERE/terminal.py" read "$target" >"$ARTIFACT/$state-display.json" || return 2
    projection_args=("$ARTIFACT/$state-display.json")
  fi
  python3 "$HERE/fixtures/chat-reload/projection.py" attention "$JOURNEY_OBSERVATION" "$SID" "${projection_args[@]}" >"$ARTIFACT/$state.json" || return 2
  if [ "$backend" = herdr ] && { [ "$state" = working ] || [ "$state" = blocked ]; }; then
    python3 "$FIXTURE/state.py" sample "$target" "$state" "$ARTIFACT/$state-phases.json" || return 2
    assert "$ID" "[\"$state\"]" "$(jq -c '.states' "$ARTIFACT/$state-phases.json")" "$ARTIFACT/$state-phases.json"
    local source="$ARTIFACT/$state-phases.json"
    if [ "$state" = working ]; then
      GAP_SOURCE=product-gap:working-blink
      GAP_REASON='working indicator remains continuously present rather than blinking'
      source="$GAP_SOURCE"
    fi
    assert "$ID" "[\"none\",\"pfm_$state\"]" "$(jq -c '.phases' "$ARTIFACT/$state-phases.json")" "$source"
  elif [ "$backend" = tmux ]; then
    python3 "$FIXTURE/state.py" mark "$JOURNEY_TARGET" "$NAME" "$ARTIFACT/$state-mark.json" || return 2
    GAP_SOURCE=product-gap:tmux-state-mark
    GAP_REASON='tmux window name has no observable state mark'
    assert "$ID" "$state" "$(jq -r '.mark' "$ARTIFACT/$state-mark.json")" "$GAP_SOURCE"
  fi
  jq 'del(.indicator)' "$ARTIFACT/$state.json" >"$ARTIFACT/$state-stable.json" || return 2
  golden "$ID" "chat-attention/$backend-$state.json" "$ARTIFACT/$state-stable.json"
}
_attention_wait() {
  poll "attention-$1" "${2:-10}" lane_state "$1" "$ARTIFACT/status-$1.json" || return "$?"
  _attention_observe "$1"
}
_attention_begin() {
  local template
  case "$1" in /*) template="$1" ;; *) template="$FIXTURE/$1.json" ;; esac
  observe "${ATTENTION_DISPLAY:-$JOURNEY_TARGET}" || return 2
  ATTENTION_BEFORE="$ARTIFACT/attention-before.json"
  cp "$JOURNEY_OBSERVATION" "$ATTENTION_BEFORE" || return 2
  HOLD="$ARTIFACT/turn.hold"
  printf '%s\n' held >"$HOLD" || return 2
  lane_bind "$template" "$ARTIFACT/steps.json" || return 2
  INPUT_RECORD="$(jq -er '.records' "$CHAT")/prompts"
  INPUT_OFFSET=0
  if [ -f "$INPUT_RECORD" ]; then INPUT_OFFSET="$(wc -c <"$INPUT_RECORD")" || return 2; fi
  PYTHONPATH="$HERE" python3 - "$ARTIFACT/steps.json" "$2" <<'PY' >"$ARTIFACT/expected-input.txt" || return 2
import json, sys
from runtime import read_json
print(sys.argv[2] + " mock-engine: " + json.dumps(read_json(sys.argv[1]), separators=(",", ":")))
PY
  lane_input "$ARTIFACT/steps.json" "$2" || return 2
  poll native-attention-input 10 _attention_input_ready || return "$?"
  assert "$ID" "$(cat "$ARTIFACT/expected-input.txt")" "$(cat "$ARTIFACT/engine-input.txt")" "engine-input:$ARTIFACT/engine-input.txt"
}
_attention_input_ready() {
  [ -f "$INPUT_RECORD" ] || return 1
  tail -c "+$((INPUT_OFFSET + 1))" "$INPUT_RECORD" >"$ARTIFACT/engine-input.txt" || return 2
  cmp -s "$ARTIFACT/expected-input.txt" "$ARTIFACT/engine-input.txt"
}
_attention_activity() { python3 "$FIXTURE/state.py" activity "$CHAT" "$1" "$2"; }
_attention_unavailable() {
  printf 'ATTENTION-SETUP-UNAVAILABLE: %s: %s\n' "$PFM_BEAT" "$1" >&2
  return 3
}
_attention_sibling() {
  MAIN_STOP_HOLD="$ARTIFACT/sibling.hold"
  printf '%s\n' held >"$MAIN_STOP_HOLD" || return 2
  _attention_begin sibling 'research with a sibling' && _attention_wait blocked || return "$?"
  poll two-live-children 10 _attention_activity 2 "$ARTIFACT/owner-before.json" || return "$?"
  jq -S '.blocked_owner' "$ARTIFACT/owner-before.json" >"$ARTIFACT/blocked-owner.json" || return 2
  golden "$ID" chat-attention/blocked-owner.json "$ARTIFACT/blocked-owner.json" || return "$?"
  assert "$ID" '["journey-researcher","journey-sibling"]' "$(jq -c '.active_child_ids' "$ARTIFACT/owner-before.json")" "$ARTIFACT/owner-before.json"
  rm "$MAIN_STOP_HOLD" || return 2
  poll sibling-completed 10 _attention_activity 1 "$ARTIFACT/owner-after.json" || return "$?"
  assert "$ID" "$(jq -c '.blocked_owner' "$ARTIFACT/owner-before.json")" "$(jq -c '.blocked_owner' "$ARTIFACT/owner-after.json")" "$ARTIFACT/owner-after.json"
  assert "$ID" '["journey-researcher"]' "$(jq -c '.active_child_ids' "$ARTIFACT/owner-after.json")" "$ARTIFACT/owner-after.json"
  _attention_wait blocked || return "$?"
  python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" enter && _attention_finish || return "$?"
  poll owning-child-finished 10 _attention_activity 0 "$ARTIFACT/owner-finished.json"
}
_attention_ghost() {
  _attention_begin ghost 'deliver background research' && _attention_wait working || return "$?"
  poll notification-child-live 10 _attention_activity 1 "$ARTIFACT/notification-before.json" || return "$?"
  rm "$HOLD" || return 2
  _attention_wait idle || return "$?"
  poll notification-child-settled 10 _attention_activity 0 "$ARTIFACT/notification-after.json" || return "$?"
  assert "$ID" '[]' "$(jq -c '.active_child_ids' "$ARTIFACT/notification-after.json")" "$ARTIFACT/notification-after.json"
}
_attention_codex_child() {
  python3 "$FIXTURE/state.py" codex-children "$CHAT" "$ARTIFACT/codex-children-before.json" || return 2
  _attention_begin codex-child 'create native child rollout' && _attention_finish || return "$?"
  python3 "$FIXTURE/state.py" codex-children "$CHAT" "$ARTIFACT/codex-children-after.json" || return 2
  jq --slurpfile before "$ARTIFACT/codex-children-before.json" \
    '[.children[] | select(.id as $id | [$before[0].children[].id] | index($id) | not)]' \
    "$ARTIFACT/codex-children-after.json" >"$ARTIFACT/new-codex-children.json" || return 2
  assert "$ID" 1 "$(jq 'length' "$ARTIFACT/new-codex-children.json")" "$ARTIFACT/new-codex-children.json" || return "$?"
  jq '.[0]' "$ARTIFACT/new-codex-children.json" >"$ARTIFACT/codex-child.json" || return 2
  assert "$ID" "$SID" "$(jq -r '.parent_thread_id' "$ARTIFACT/codex-child.json")" "$ARTIFACT/codex-child.json"
  python3 "$FIXTURE/state.py" codex-hook "$ARTIFACT/codex-child.json" "$SID" \
    "$(jq -r '.cwd' "$ARTIFACT/status-idle.json")" "$FIXTURE/question.json" \
    "$ARTIFACT/codex-child-hook.json" || return 2
  _attention_begin "$ARTIFACT/codex-child-hook.json" 'deliver native child hook' && _attention_wait blocked || return "$?"
  assert "$ID" "[\"$SID\"]" "$(jq -c --arg parent "$SID" \
    --arg child "$(jq -r '.id' "$ARTIFACT/codex-child.json")" \
    '.picker.rows | map(select(.id==$parent or .id==$child)) | map(.id) | sort' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  _attention_run answer-unblocks
}
_attention_trust_receipt() { test -s "$ARTIFACT/trust-stream.status"; }
_attention_end_receipt() { test -s "$ARTIFACT/auxiliary-end.status"; }
_attention_cleanup_trust() {
  [ -n "${TRUST_BEFORE_END:-}" ] || return 0
  local ended_sid
  ended_sid="$(jq -er '.session_id' "$JOURNEY_ROOT/attention-trust-chat.json")" || return 2
  python3 "$HERE/terminal.py" receipt "$JOURNEY_ROOT/attention-reader-shell.json" \
    "$ARTIFACT/auxiliary-end" pfm chat end "$ended_sid" || return 2
  poll auxiliary-role-ended 10 _attention_end_receipt || return "$?"
  if [ "$(cat "$ARTIFACT/auxiliary-end.status")" != 0 ]; then
    printf 'ATTENTION-CLEANUP: expected=0 actual=%s artifact=%s\n' \
      "$(cat "$ARTIFACT/auxiliary-end.status")" "$ARTIFACT/auxiliary-end.out" >&2
    return 2
  fi
  jq '.terminal' "$JOURNEY_ROOT/attention-trust-chat.json" >"$ARTIFACT/trust-ended-target.json" || return 2
  poll trust-engine-gone 10 _attention_no_claude "$ARTIFACT/trust-ended-target.json" "$ARTIFACT/trust-ended" || return "$?"
  python3 "$HERE/chat.py" expect-ended "$JOURNEY_BRANCH" forecast-trust-reader \
    "$TRUST_BEFORE_END" >"$JOURNEY_ROOT/attention-trust-chat.json"
}
_attention_prepare_trust() {
  python3 "$HERE/chat.py" ensure-role "$JOURNEY_BRANCH" forecast-trust-reader \
    "journey-trust-reader-$PFM_RUN" "$FIXTURE/trust-reader.json" \
    >"$JOURNEY_ROOT/attention-trust-chat.json" || return 2
  python3 "$HERE/chat.py" shell "$JOURNEY_BRANCH" "$JOURNEY_ROOT/attention-reader-shell.json" >/dev/null
}
_attention_trust_reader() {
  local previous_origin="$CHAT_ORIGIN" rc=0
  cp "$CHAT" "$ARTIFACT/selected-before-trust.json" || return 2
  [ "${TRUST_PREPARED:-0}" -eq 1 ] || { _attention_unavailable 'actual trust role prelude failed; see named setup error'; return 3; }
  cp "$JOURNEY_ROOT/attention-trust-chat.json" "$CHAT" || return 2
  CHAT_ORIGIN=forecast-trust-reader
  lane_refresh || return 2
  python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" >/dev/null || return 2
  poll trust-frame-rendered 10 test -s "$(jq -r '.records' "$CHAT")/trust-frame.txt" || return "$?"
  python3 "$HERE/terminal.py" read "$JOURNEY_TARGET" >"$ARTIFACT/trust-before.json" || return 2
  python3 "$FIXTURE/state.py" engine-births "$ARTIFACT/trust-before.json" "$ARTIFACT/trust-birth-before.json" || return 2
  python3 "$HERE/terminal.py" receipt "$JOURNEY_ROOT/attention-reader-shell.json" "$ARTIFACT/trust-stream" \
    pfm chat stream "$SID" || return 2
  poll trust-stream-refused 10 _attention_trust_receipt || return "$?"
  assert "$ID" 6 "$(cat "$ARTIFACT/trust-stream.status")" "$ARTIFACT/trust-stream.status"
  assert "$ID" "[\"pfm\",\"chat\",\"stream\",\"$SID\"]" \
    "$(jq -c '.argv' "$ARTIFACT/trust-stream.dispatch.json")" "owner-input:$ARTIFACT/trust-stream"
  python3 - "$NAME" "$(jq -r '.pane' "$JOURNEY_TARGET")" "$HERDR_SOCKET_PATH" "$(jq -r '.seat_id' "$CHAT")" <<'PY' >"$ARTIFACT/trust-stream.expected" || return 2
import sys
name, pane, socket, seat = sys.argv[1:]
print(f'''pfm chat stream: blocked at Claude Code's folder-trust dialog: {name} is alive in pane {pane} ({socket}); open the chat with `pfm chat open 'seat:{seat}'` and answer the dialog yourself ("Yes, I trust this folder" is your decision), or start the chat in a trusted directory; pfm pressed nothing''')
PY
  assert "$ID" "$(cat "$ARTIFACT/trust-stream.expected")" \
    "$(cat "$ARTIFACT/trust-stream.out")" "$ARTIFACT/trust-stream.out"
  python3 "$HERE/terminal.py" read "$JOURNEY_TARGET" >"$ARTIFACT/trust-after.json" || return 2
  python3 "$FIXTURE/state.py" engine-births "$ARTIFACT/trust-after.json" "$ARTIFACT/trust-birth-after.json" || return 2
  assert "$ID" 1 "$(jq 'length' "$ARTIFACT/trust-birth-before.json")" "$ARTIFACT/trust-birth-before.json"
  assert "$ID" "$(jq -c '.' "$ARTIFACT/trust-birth-before.json")" \
    "$(jq -c '.' "$ARTIFACT/trust-birth-after.json")" "$ARTIFACT/trust-birth-after.json"
  assert "$ID" "$(jq -c '{identity,text}' "$ARTIFACT/trust-before.json")" \
    "$(jq -c '{identity,text}' "$ARTIFACT/trust-after.json")" "$ARTIFACT/trust-after.json"
  python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" 1 || return 2
  poll trust-accepted 10 lane_state idle "$ARTIFACT/trust-accepted.json" || return "$?"
  _attention_run question-blocks && _attention_run answer-unblocks || rc=$?
  if [ "$rc" -eq 0 ]; then TRUST_BEFORE_END="$ARTIFACT/idle-bundle.json"; fi
  CHAT_ORIGIN="$previous_origin"
  cp "$ARTIFACT/selected-before-trust.json" "$CHAT" || return 2
  lane_refresh || return 2
  return "$rc"
}
_attention_finish() {
  _attention_wait working || return "$?"
  rm "$HOLD" || return 2
  _attention_wait idle
}
_attention_dropped() {
  local fault="${1:-server}" command
  _attention_begin turn 'summarize the plan' && _attention_wait working || return "$?"
  case "$fault" in
    server) command=(stopped-herdr "$HERDR_SOCKET_PATH") ;;
    activity) command=(locked-reporter "$CHAT" activity) ;;
    *) printf 'ATTENTION-FAULT: %s\n' "$fault" >&2; return 2 ;;
  esac
  expect-log '(?=.*herdr)(?=.*(?:deadline|timeout|timed out))'
  # Three seconds is the specified injected outage, not a wait for the UI to settle.
  python3 "$HERE/faults.py" "${command[@]}" -- \
    bash -c 'rm "$1" && sleep 3' dropped-stop "$HOLD" || return 2
  _attention_wait idle || return "$?"
  assert "$ID" '[]' "$(jq -c '[.terminal.text | split("\n")[] | select(contains("1 agent running"))]' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  assert "$ID" 'plan summarized' "$(jq -r --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .status.last' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  lane_action chat.prompt typed
}
_attention_run() {
  local action="$1"
  case "$action" in
    turn-works-then-idles)
      _attention_begin turn 'summarize the plan' && _attention_finish || return "$?"
      assert "$ID" 'plan summarized' "$(jq -r --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .status.last' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
      lane_action chat.prompt typed ;;
    question-blocks)
      _attention_begin question 'pick a target' && _attention_wait blocked || return "$?"
      lane_action chat.prompt typed ;;
    answer-unblocks)
      python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" 2 && _attention_finish || return "$?"
      assert "$ID" 'target 2' "$(jq -r --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .status.last' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION" ;;
    permission-blocks)
      _attention_begin permission 'write the report' && _attention_wait blocked || return "$?" ;;
    permission-answered-unblocks)
      python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" enter && _attention_finish || return "$?"
      golden "$ID" chat-attention/report.txt "$(jq -r '.cwd' "$ARTIFACT/status-idle.json")/report.txt" ;;
    subagent-holds-working)
      MAIN_STOP_HOLD="$ARTIFACT/main-stop.hold"
      printf '%s\n' held >"$MAIN_STOP_HOLD" || return 2
      _attention_begin subagent 'research it' && _attention_wait working || return "$?"
      if [ "$(jq -r '.backend' "$JOURNEY_TARGET")" = herdr ]; then
        poll main-stop-keeps-child 10 _attention_activity 1 "$ARTIFACT/subagent-working.json" || return "$?"
        assert "$ID" '["journey-researcher"]' "$(jq -c '.active_child_ids' "$ARTIFACT/subagent-working.json")" "$ARTIFACT/subagent-working.json"
      fi
      rm "$MAIN_STOP_HOLD" || return 2
      _attention_wait blocked || return "$?"
      if [ "$(jq -r '.backend' "$JOURNEY_TARGET")" = herdr ]; then
        _attention_activity 1 "$ARTIFACT/subagent-blocked.json" || return "$?"
        jq -S '.blocked_owner' "$ARTIFACT/subagent-blocked.json" >"$ARTIFACT/blocked-owner.json" || return 2
        golden "$ID" chat-attention/blocked-owner.json "$ARTIFACT/blocked-owner.json" || return "$?"
      fi
      python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" enter && _attention_finish || return "$?"
      if [ "$(jq -r '.backend' "$JOURNEY_TARGET")" = herdr ]; then
        poll subagent-stop-clears-child 10 _attention_activity 0 "$ARTIFACT/subagent-idle.json" || return "$?"
        assert "$ID" null "$(jq -c '.blocked_owner' "$ARTIFACT/subagent-idle.json")" "$ARTIFACT/subagent-idle.json"
      fi ;;
    notification-turn-works-then-idles)
      _attention_begin notification 'research it in background' && _attention_finish ;;
    subagent-sibling-does-not-unblock) _attention_sibling ;;
    notification-settle-does-not-create-ghost-child) _attention_ghost ;;
    opted-in-agent-attaches-to-carried-parent) _attention_agent ;;
    turn-idles-after-dropped-report) _attention_dropped ;;
    turn-fails-shows-error)
      lane_input "$FIXTURE/failure.json" 'fail now' && _attention_wait error 1 ;;
    indicators-survive-worktree-removal) _attention_unavailable 'harness scratch-worktree creation/removal fixture is not published' ;;
    *) _attention_unavailable "no owner setup published for action $action" ;;
  esac
}
_attention_dispatch() {
  local parent="$2" action
  if [ -n "$3" ] || [ "$1" = forecast-codex-blocked ]; then
    case "$3:$1" in
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C06:*)
        _attention_begin plugin 'trigger plugin notification' && _attention_wait blocked || return "$?"
        rm "$HOLD" || return 2
        _attention_wait idle; return "$?" ;;
      pfm-herdr-tmux/C15:*)
        _attention_run turn-works-then-idles || return "$?"
        assert "$ID" running "$(jq -r '.terminal.shield.holder.state' "$ARTIFACT/working-bundle.json")" "$ARTIFACT/working-bundle.json"
        assert "$ID" "$(jq -r '.pane' "$JOURNEY_TARGET")" "$(jq -r '.terminal.shield.holder.binding.pane' "$ARTIFACT/working-bundle.json")" "$ARTIFACT/working-bundle.json"
        return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C09:*)
        _attention_denied_begin 'deny the report' || return "$?"
        python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" escape || return 2
        rm "$HOLD" || return 2
        _attention_wait idle || return "$?"
        _attention_activity 0 "$ARTIFACT/denied-idle.json" || return "$?"
        assert "$ID" null "$(jq -c '.blocked_owner' "$ARTIFACT/denied-idle.json")" "$ARTIFACT/denied-idle.json"
        return "$?" ;;
      pfm-herdr-tmux/C14:*) _attention_run subagent-holds-working; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C03:*)
        _attention_run notification-turn-works-then-idles || return "$?"
        poll task-notification-no-live-child 10 _attention_activity 0 "$ARTIFACT/notification-settled.json"; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C07:*)
        _attention_begin interrupted 'pick a target' && _attention_wait blocked || return "$?"
        _attention_run answer-unblocks; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C02:*) _attention_dropped; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C20:*) _attention_dropped activity; return "$?" ;;
      pfm-herdr-tmux/C26:*|pfm-herdr-tmux/C33:*|:forecast-codex-blocked) ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C33:*) _attention_indicator c33; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C10:*) _attention_large; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C115:*) _attention_previous; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C67:*) _attention_trust_reader; return "$?" ;;
      pfm-herdr-tmux/C12:*|darwin-linux-launchd-systemd-proc-sysctl-setsid/C48:*) _attention_codex_child; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C78:*) _attention_legacy_tmux; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C08:*) _attention_native; return "$?" ;;
      *) _attention_unavailable "no owner setup published for forecast $3"; return 3 ;;
    esac
    action="${parent#*/}"; action="${action%%@*}"
    _attention_run "$action" || return "$?"
    if [ "$1" = forecast-codex-blocked ]; then
      assert "$ID" cx "$(jq -r --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .status.engine' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
      _attention_run permission-answered-unblocks
    elif [ "$3" = pfm-herdr-tmux/C26 ]; then
      assert "$ID" cc "$(jq -r --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .status.engine' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
      _attention_run answer-unblocks
    else
      assert "$ID" herdr "$(jq -r '.terminal.backend' "$ARTIFACT/working-bundle.json")" "$ARTIFACT/working-bundle.json"
    fi
    return "$?"
  fi
  if [ "$1" = indicators-survive-worktree-removal ]; then _attention_indicator base; return "$?"; fi
  _attention_run "$1"
}
_attention_row() {
  local rc
  _attention_dispatch "$@"; rc=$?
  if [ "$rc" -ne 0 ]; then GAP_SOURCE=''; return "$rc"; fi
  if [ "$rc" -eq 0 ] && [ -n "$GAP_SOURCE" ] && lane_gap_only "$GAP_SOURCE"; then return 1; fi
  return "$rc"
}
_attention_leaf() {
  local rc
  if [ "$JOURNEY_BRANCH" = linux+herdr-shield+project+claude ]; then
    TRUST_PREPARED=0 TRUST_BEFORE_END=''
    if _attention_prepare_trust; then TRUST_PREPARED=1; fi
    PREVIOUS_SETTINGS_PREPARED=0
    if python3 "$FIXTURE/state.py" previous-settings "$JOURNEY_BRANCH" "$HERE/scenarios/default.json" \
      "$JOURNEY_ROOT/attention-previous-settings-chat.json"; then PREVIOUS_SETTINGS_PREPARED=1; fi
  elif [ "$JOURNEY_BRANCH" = linux+herdr-shield+project+codex ]; then
    LEGACY_PREPARED=0
    if _attention_prepare_legacy; then LEGACY_PREPARED=1; fi
  elif [ "$JOURNEY_BRANCH" = macos+herdr-native+project+claude ]; then
    NATIVE_OWNER_PREPARED=0
    if python3 "$HERE/chat.py" shell "$JOURNEY_BRANCH" "$JOURNEY_ROOT/attention-native-owner.json" >/dev/null; then
      NATIVE_OWNER_PREPARED=1
    fi
  fi
  lane_walk _attention_row; rc=$?
  if [ "$JOURNEY_BRANCH" = linux+herdr-shield+project+claude ]; then
    _attention_cleanup_trust || rc=2
  fi
  return "$rc"
}
_attention_place() { fork engine 'claude codex opencode' _attention_leaf; }
_attention_terminal() {
  local terminal="$1"
  fork place project _attention_place
  if [ "$terminal" != tmux ]; then
    _attention_prepare_indicators
    lane_walk _attention_row "$JOURNEY_BRANCH+project+claude"
  fi
}
_attention_os() {
  case "$1" in
    linux) fork terminal 'herdr-shield tmux' _attention_terminal ;;
    macos) fork terminal 'herdr-native tmux' _attention_terminal ;;
    *) printf 'ATTENTION-OS: unsupported %s\n' "$1" >&2; return 2 ;;
  esac
}
if [ "${BASH_SOURCE[0]}" = "$0" ]; then lane_run "$JOURNEY_OS" _attention_os; fi
