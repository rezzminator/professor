#!/usr/bin/env bash
# Ordered caller/recipient pairs retain every origin for the later reader and end lanes.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
. "$HERE/fixtures/chat-mesh/env_scrub.sh"
. "$HERE/fixtures/chat-mesh/dead.sh"
. "$HERE/fixtures/chat-mesh/live_input.sh"
export JOURNEY_LANE=chat-mesh
FIXTURES="$HERE/fixtures/chat-mesh"
MESH_OS="${JOURNEY_OS:?runner must select linux or macos}"

_mesh_ready() { python3 "$HERE/chat.py" need "$MESH_OS+$MESH_HERDR+project+claude" new-from-cli >/dev/null; }
_mesh_create() { python3 "$HERE/chat.py" ensure "$MESH_OS+$MESH_HERDR+project+claude" new-from-cli "$FIXTURES/seed.json" >/dev/null; }
case "$MESH_OS" in linux) MESH_HERDR=herdr-shield ;; macos) MESH_HERDR=herdr-native ;; *) exit 2 ;; esac
need herdr-claude-cli-caller _mesh_ready _mesh_create || exit 2

_mesh_caller() {
  local origin="$1" result
  mesh_select "$BRANCH" "$origin" "$PREFIX.caller.json"; result=$?
  if [ "$result" -eq 1 ] && [ "$origin" = new-typed ]; then
    # Missing typed product entries use an honestly carried CLI prelude actor.
    mesh_select "$BRANCH" new-from-cli "$PREFIX.caller.json"; result=$?
  fi
  [ "$result" -eq 0 ] || return "$result"
  CALLER="$PREFIX.caller.json"
  export JOURNEY_TARGET="$CALLER.target"
}

_mesh_before() { python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$PREFIX.before.json"; }

_mesh_finish() {
  mesh_bundle_actors "$ID" "$PREFIX.before.json" "$CALLER" "$1" || return 2
  mesh_action "$ID" "$2" "$3"
}

_mesh_known_missing() {
  local reason expected argv name="mesh-missing-${JOURNEY_ROOT##*/}"
  reason="$(python3 - "$JOURNEY_DESIGN/gaps.tsv" "$PFM_BEAT" <<'PY'
import csv,fnmatch,sys
with open(sys.argv[1]) as source:
    rows=[row for row in csv.DictReader(source,delimiter='\t') if fnmatch.fnmatchcase(sys.argv[2],row['row']) and row['why']=='ruling:ruling-gap-opencode-tmux']
if not rows: sys.exit(1)
print(rows[0]['why'])
PY
)" || return 1
  export JOURNEY_TARGET="$PREFIX.missing-owner.json"
  jq '.owner' "$JOURNEY_CARRY" >"$JOURNEY_TARGET"
  python3 "$HERE/terminal.py" screen "$JOURNEY_TARGET" >"$PREFIX.missing-owner-screen" || return 2
  if rg -Fq 'New chat' "$PREFIX.missing-owner-screen"; then python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" escape || return 2; fi
  mesh_command "$JOURNEY_TARGET" "$PREFIX.prerequisite" pfm chat new --engine opencode --backend tmux --name "$name" --cwd "$HOME/work/atlas" || return 2
  argv="$(jq -nc --arg cwd "$HOME/work/atlas" --arg name "$name" '["pfm","chat","new","--engine","opencode","--backend","tmux","--name",$name,"--cwd",$cwd]')"
  assert "$ID" "$argv" "$(jq -c '.argv' "$PREFIX.prerequisite.dispatch.json")" "owner-input:$PREFIX.prerequisite" || return 2
  expected='pfm chat new: OpenCode does not support headless chat'
  assert "$ID" 2 "$(cat "$PREFIX.prerequisite.status")" "$PREFIX.prerequisite.status" || return 2
  assert "$ID" "$expected" "$(cat "$PREFIX.prerequisite.out")" "$PREFIX.prerequisite.out" || return 2
  assert "$ID" 0 "$(cat "$PREFIX.prerequisite.status")" inherited-product-gap
  observe || return 2
  known "$reason"
  return 0
}

_mesh_missing_or_failed() {
  local context="$1" result="$2" missing_branch="$3" gap_result
  if [ "$result" -ne 1 ]; then fail "could not inspect $context (exit $result)"; return 0; fi
  if [ "$missing_branch" != "$MESH_OS+tmux+project+opencode" ]; then blocked "$context"; return 0; fi
  _mesh_known_missing; gap_result=$?
  case "$gap_result" in
    0) ;;
    1) blocked "$context" ;;
    *) fail "inherited prerequisite proof failed for $context" ;;
  esac
  return 0
}

_mesh_peer() {
  local peer="${JOURNEY_BRANCH#*+to-}" peer_branch origin=new-from-picker
  peer_branch="$MESH_OS+${peer%-project-*}+project+${peer##*-}"
  MESH_PEER_BRANCH="$peer_branch"
  [ "$peer_branch" != "$BRANCH" ] || origin=new-from-cli
  if [ "$peer_branch" = "$BRANCH" ] && [ "$(jq -r '.origin' "$CALLER")" = new-from-cli ]; then origin=new-from-picker; fi
  [ "${CALLEE_ORIGIN:-}" = scout ] && origin=scout
  mesh_select "$peer_branch" "$origin" "$PREFIX.callee.json" || return "$?"
  CALLEE="$PREFIX.callee.json"
}

_mesh_composer_ready() {
  python3 "$HERE/terminal.py" screen "$CALLEE.target" >"$PREFIX.recipient-screen" || return 2
  rg -Fq "$1" "$PREFIX.recipient-screen"
}

_mesh_call() {
  local door="$1" state="${2:-idle}" target nonce result live_input=false
  _mesh_peer; result=$?
  if [ "$result" -ne 0 ]; then
    _mesh_missing_or_failed "chat-birth:recipient:$JOURNEY_BRANCH" "$result" "$MESH_PEER_BRANCH"; return 0
  fi
  target="$(jq -er '.session_id' "$CALLEE")" || return 2
  [ "$target" != "$(jq -r '.session_id' "$CALLER")" ] || { fail 'caller and recipient must be distinct carried chats'; return 0; }
  nonce="ping from $(jq -r '.name' "$CALLER") $PFM_BEAT"
  MESSAGE="$nonce mock-engine: $(jq -c . "$FIXTURES/turn.json")"
  if [ "$state" != idle ]; then
    python3 "$FIXTURES/recipient.py" "$state" "$CALLEE" "$PREFIX.recipient" || return 2
    if [ "$state" != draft ]; then
      python3 "$HERE/chat.py" inline "$CALLEE.target" "$PREFIX.recipient/recipient-steps.json" "recipient-$state" || return 2
    fi
    case "$state" in
      working|blocked) poll "recipient-$state" 10 mesh_state "$CALLEE" "$state" || return 2 ;;
      modal) poll recipient-modal 10 _mesh_composer_ready 'Choose target' || return 2 ;;
      draft) poll recipient-draft 10 _mesh_composer_ready owner-unsent-draft || return 2 ;;
    esac
  fi
  if [ "${slug:-}" = call-from-mcp ] && [ "$door" = mcp ] && [ "$state" = idle ] && [ "$(jq -r '.engine' "$CALLER")" = codex ] && \
    [ "$(jq -r '.engine' "$CALLEE")" = claude ] && [ "$(jq -r '.terminal.backend' "$CALLEE")" = herdr ]; then
    python3 "$FIXTURES/agentless.py" "$HERE" "$CALLER" "$CALLEE" "$PREFIX.agentless-delivery" "$ID" delivery || return 2
    poll raw-recipient-reregistered 15 mesh_state "$CALLEE" idle || return 2
  fi
  _mesh_before || return 2
  if [ "${slug:-}" = call-from-cli ] && [ "$BRANCH" = "$MESH_OS+tmux+project+claude" ] && \
    [ "$MESH_PEER_BRANCH" = "$BRANCH" ]; then
    mesh_callee_input_start || return 2
    live_input=true
  fi
  if [ "$door" = mcp ]; then
    mesh_mcp_start "$CALLER" call "$(jq -nc --arg target "$target" --arg message "$MESSAGE" '{target:$target,message:$message}')" "$PREFIX.call" || return 2
  else
    mesh_shell "$CALLER" "$PREFIX.call" pfm chat call "$target" "$MESSAGE" || return 2
  fi
  if [ "$state" = idle ]; then
    poll callee-working 10 mesh_state "$CALLEE" working || return 2
  fi
  if [ "$door" = mcp ]; then
    mesh_mcp_finish "$CALLER" "$PREFIX.call" || return 2
    python3 "$HERE/fixtures/fleet-reader/projection.py" delivery "$PREFIX.call.result.json" >"$PREFIX.delivery.json" || return 2
    case "$state" in
      idle|working) golden "$ID" chat-mesh/delivered.json "$PREFIX.delivery.json" ;;
      draft|modal|blocked) golden "$ID" chat-mesh/undelivered.json "$PREFIX.delivery.json" ;;
    esac
  else
    poll caller-shell-receipt 30 mesh_command_ready || return 2
    mesh_input_finish "$PREFIX.call"
    case "$state" in
      idle|working) assert "$ID" 0 "$(cat "$MESH_COMMAND.status")" "$MESH_COMMAND.status" ;;
      draft|modal|blocked) assert "$ID" 6 "$(cat "$MESH_COMMAND.status")" "$MESH_COMMAND.status" ;;
    esac
  fi
  if [ "$state" = working ]; then
    # The held turn is a run-owned mock input, not product state.
    python3 - "$PREFIX.recipient/recipient-working" <<'PY'
from pathlib import Path
import sys
Path(sys.argv[1]).unlink()
PY
  fi
  if [ "$state" = idle ] || [ "$state" = working ]; then
    poll callee-idle 15 mesh_state "$CALLEE" idle || return 2
    if [ "$live_input" = true ]; then mesh_callee_input_finish || return 2; fi
    mesh_mcp "$CALLER" last "$(jq -nc --arg target "$target" '{target:$target}')" "$PREFIX.last" || return 2
    assert "$ID" pong "$(jq -r '.text' "$PREFIX.last.result.json")" "$PREFIX.last.result.json"
    _mesh_finish "$CALLEE" chat.call "$door" || return 2
    python3 "$HERE/fixtures/fleet-reader/projection.py" call "$JOURNEY_OBSERVATION" "$CALLER" "$CALLEE" "$MESSAGE" >"$PREFIX.call-checks.json" || return 2
    mesh_checks "$ID" "$PREFIX.call-checks.json"
  else
    _mesh_finish "$CALLEE" chat.call "$door" || return 2
    mesh_subject "$PREFIX.before.json" "$CALLEE" "$PREFIX.callee-before-observed.json" || return 2
    mesh_subject "$JOURNEY_OBSERVATION" "$CALLEE" "$PREFIX.callee-after-observed.json" || return 2
    assert "$ID" "$(jq '.terminal.text' "$PREFIX.callee-before-observed.json")" \
      "$(jq '.terminal.text' "$PREFIX.callee-after-observed.json")" "$PREFIX.callee-after-observed.json"
    python3 "$HERE/terminal.py" keys "$CALLEE.target" escape || return 2
  fi
  pass
  return 0
}

_mesh_spawn() {
  local role="${1:-scout}" result
  if [ "$role" = readiness ]; then python3 "$FIXTURES/readiness.py" stage "$CALLER" "$PREFIX" || return 2; fi
  _mesh_spawn_action "$role"; result=$?
  if [ "$role" = readiness ]; then python3 "$FIXTURES/readiness.py" restore "$CALLER" "$PREFIX" || return 2; fi
  return "$result"
}

_mesh_spawn_lists() {
  local child="$PREFIX.child.json" receipt="$PREFIX.spawn-list"
  mesh_shell "$CALLER" "$receipt" pfm ls --all --tsv || return 2
  poll spawned-public-list 30 mesh_command_ready || return 2
  mesh_input_finish "$receipt" || return 2
  assert "$ID" 0 "$(cat "$receipt.status")" "$receipt.status"
  jq '{chats:[.]}' "$child" >"$receipt.carry.json"
  python3 "$HERE/fixtures/fleet-reader/projection.py" text-fleet "$receipt.out" "$receipt.carry.json" >"$receipt.checks.json" || return 2
  mesh_checks "$ID" "$receipt.checks.json"
  receipt="$PREFIX.spawn-json-list"
  mesh_shell "$CALLER" "$receipt" pfm chat ls --json || return 2
  poll spawned-machine-readable-list 30 mesh_command_ready || return 2
  mesh_input_finish "$receipt" || return 2
  assert "$ID" 0 "$(cat "$receipt.status")" product-gap:machine-readable-chat-list
  poll caller-idle-after-list 15 mesh_state "$CALLER" idle
}

_mesh_new_not_returned() {
  assert "$ID" 'MCP new returned before the child’s release file existed' \
    'MCP new did not return before the child’s release file existed' "$PREFIX.new"
  return 2
}

_mesh_spawn_action() {
  local role="$1" name="$1-$(jq -r '.terminal_kind' "$CALLER")-$(jq -r '.engine' "$CALLER")" input sid result
  _mesh_before || return 2
  input="$(jq -nc --arg name "$name" '{engine:"claude",account:3,name:$name}')"
  if [ "$role" = readiness ]; then
    input="$(jq -c '.+{await:false,timeout:8}' <<<"$input")"
    mesh_mcp_start "$CALLER" new "$input" "$PREFIX.new" || return 2
    mesh_mcp_finish "$CALLER" "$PREFIX.new" 10; result=$?
    if [ "$result" -eq 1 ] || { [ "$result" -eq 2 ] && [ -s "$PREFIX.new.receipt.json" ] && jq -e '.isError==true' "$PREFIX.new.receipt.json" >/dev/null; }; then
      _mesh_new_not_returned
      return 2
    fi
    [ "$result" -eq 0 ] || return 2
  else
    mesh_mcp "$CALLER" new "$input" "$PREFIX.new" || return 2
  fi
  assert "$ID" 0 "$(jq -r '.code' "$PREFIX.new.result.json")" "$PREFIX.new.result.json"
  if [ "$role" = readiness ] && [ "$(jq -r '.code' "$PREFIX.new.result.json")" != 0 ]; then
    _mesh_new_not_returned
    return 2
  fi
  if [ "$role" = readiness ]; then
    poll held-child-born 10 _mesh_held_birth_ready || return 2
    python3 "$FIXTURES/readiness.py" held "$CALLER" "$PREFIX" || return 2
    assert "$ID" held "$(if [ -e "$PREFIX.ready" ]; then printf released; else printf held; fi)" "$PREFIX.new.result.json"
    python3 "$FIXTURES/readiness.py" release "$CALLER" "$PREFIX" || return 2
  fi
  mesh_adopt_new "$CALLER" "$name" "$PREFIX.new.result.json" "$PREFIX" "$role" || return 2
  sid="$(jq -er '.session_id' "$PREFIX.child.json")" || return 2
  assert "$ID" "$name" "$(jq -r '.name' "$PREFIX.child.json")" "$PREFIX.child.json"
  assert "$ID" claude "$(jq -r '.engine' "$PREFIX.child.json")" "$PREFIX.child.json"
  assert "$ID" 3 "$(jq -r '.account' "$PREFIX.child.json")" "$PREFIX.child.json"
  assert "$ID" "$(jq -r '.terminal.backend' "$CALLER")" "$(jq -r '.terminal.backend' "$PREFIX.child.json")" "$PREFIX.child.json"
  assert "$ID" "$(jq -r '.session_id' "$CALLER")" "$(jq -r '.parent' "$PREFIX.child.json")" "$PREFIX.child.json"
  poll scout-idle 15 mesh_state "$PREFIX.child.json" idle || return 2
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$PREFIX.child.json" "$PREFIX.child-birth.json" || return 2
  assert "$ID" "$PFM_RUN" "$(jq -r '.environment.PFM_RUN' "$PREFIX.child-birth.json")" "$PREFIX.child-birth.json"
  assert "$ID" "$HOME/.cc/3" "$(jq -r '.environment.CLAUDE_CONFIG_DIR' "$PREFIX.child-birth.json")" "$PREFIX.child-birth.json"
  if [ "$(jq -r '.terminal.backend' "$PREFIX.child.json")" = herdr ]; then
    assert "$ID" "$(jq -r '.terminal.pane' "$PREFIX.child.json")" "$(jq -r '.environment.HERDR_PANE_ID' "$PREFIX.child-birth.json")" "$PREFIX.child-birth.json"
  else
    assert "$ID" null "$(jq -c '.environment.HERDR_PANE_ID' "$PREFIX.child-birth.json")" "$PREFIX.child-birth.json"
  fi
  if [ "$role" = readiness ]; then
    assert "$ID" "$(jq -c --arg cwd "$HOME/work/atlas" '{pid,cwd:$cwd,native_session_present:false}|[.]' "$PREFIX.child-birth.json")" \
      "$(jq -c --argjson pid "$(jq '.pid' "$PREFIX.child-birth.json")" '[.[]|select(.pid==$pid)|{pid,cwd,native_session_present}]' "$PREFIX.held-births.json")" "$PREFIX.held-births.json"
    assert "$ID" "$sid" "$(jq -r '.sessionId' "$HOME/.cc/3/sessions/$(jq -r '.pid' "$PREFIX.child-birth.json").json")" native-session-after-release
    python3 "$HERE/chat.py" inline "$PREFIX.child.json.target" "$FIXTURES/turn.json" mesh-first-nonce || return 2
    poll readiness-first-answer 15 _mesh_child_answer "$sid" pong || return 2
    assert "$ID" '{"role":"assistant","text":"pong"}' "$(jq -c '.entries[-1]|{role,text}' "$PREFIX.child-answer.json")" "$PREFIX.child-answer.json"
  fi
  if [ "$role" = scout ] && [ "$(jq -r '.engine' "$CALLER")" != opencode ]; then _mesh_tool_child || return 2; fi
  if [ "$role" = scout ]; then _mesh_spawn_lists || return 2; fi
  _mesh_finish "$PREFIX.child.json" chat.new mcp || return 2
  if [ "$(jq -r '.terminal.backend' "$CALLER")" = herdr ]; then
    mesh_subject "$JOURNEY_OBSERVATION" "$CALLER" "$PREFIX.caller-observed.json" || return 2
    mesh_subject "$JOURNEY_OBSERVATION" "$PREFIX.child.json" "$PREFIX.child-observed.json" || return 2
    assert "$ID" "$(jq '.terminal.identity.workspace' "$PREFIX.caller-observed.json")" \
      "$(jq '.terminal.identity.workspace' "$PREFIX.child-observed.json")" "$PREFIX.child-observed.json"
    assert "$ID" "🥉 $(printf '%s' "$name" | tr '[:lower:]' '[:upper:]')" "$(jq -r '.terminal.tab.tab.label' "$PREFIX.child-observed.json")" "$PREFIX.child-observed.json"
    if [ "$role" = scout ]; then
      assert "$ID" '["#5f86ff"]' "$(jq -c '.terminal.tab.tab|[to_entries[]|select(.key|test("colou?r";"i"))|.value]' "$PREFIX.child-observed.json")" external-gap:herdr-tabcolour
    fi
  fi
  if [ "$role" = scout ] && python3 "$HERE/machine.py" gap-only "$JOURNEY_OUT/$JOURNEY_LANE.state.json" \
    product-gap:machine-readable-chat-list external-gap:herdr-tabcolour; then
    known 'approved machine-readable chat list and scoped Herdr tab-colour gaps; all other spawn assertions passed'
    return 0
  fi
  pass
  return 0
}

_mesh_held_birth_ready() {
  test -s "$PREFIX.child-records/birth.json" && [ "$(wc -l <"$PREFIX.child-records/birth.json")" -gt 0 ]
}

_mesh_keys() {
  local target reply scenario
  # shortcut: the caller frame precedes its child's key-caused input; concurrent unrelated prompts need per-process receipts.
  local MESH_INPUT_FIRST_FRAME=keys-child
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$CALLER" "$PREFIX.caller-birth.json" || return 2
  mesh_select "$BRANCH" scout "$PREFIX.child.json" || { blocked "chat-mesh/spawn-child-from-mcp@$BRANCH"; return; }
  target="$(jq -er '.session_id' "$PREFIX.child.json")" || return 2
  scenario="$(jq -er '.scenario' "$PREFIX.child.json")" || return 2
  reply="$(jq -er '.steps[0]|select(.type=="turn")|.reply' "$scenario")" || return 2
  _mesh_before || return 2
  mesh_mcp "$CALLER" keys "$(jq -nc --arg target "$target" '{target:$target,keys:["status?"],literal:true}')" "$PREFIX.keys" || return 2
  assert chat.keys-mcp '["status?"]' "$(jq -c '.keys' "$PREFIX.keys.result.json")" "$PREFIX.keys.result.json"
  mesh_mcp "$CALLER" keys "$(jq -nc --arg target "$target" '{target:$target,keys:["Enter"]}')" "$PREFIX.enter" || return 2
  poll child-answer 10 _mesh_child_answer "$target" "$reply" || return 2
  poll child-answer-idle 10 mesh_state "$PREFIX.child.json" idle || return 2
  assert chat.keys-mcp "$reply" "$(jq -r '.entries[-1]|select(.role=="assistant")|.text' "$PREFIX.child-answer.json")" "$PREFIX.child-answer.json"
  mesh_mcp "$CALLER" capture "$(jq -nc --arg target "$target" '{target:$target}')" "$PREFIX.capture" || return 2
  python3 "$HERE/terminal.py" screen "$PREFIX.child.json.target" >"$PREFIX.screen" || return 2
  assert chat.capture-mcp "$(cat "$PREFIX.screen")" "$(jq -r '.text' "$PREFIX.capture.result.json")" "$PREFIX.capture.result.json"
  _mesh_finish "$PREFIX.child.json" chat.keys mcp || return 2
  pass
  return 0
}

_mesh_child_answer() {
  pfm chat read "$1" --tail 1 --json >"$PREFIX.child-answer.json" || return 2
  jq -e --arg reply "$2" '.entries[-1].role=="assistant" and .entries[-1].text==$reply' "$PREFIX.child-answer.json" >/dev/null
}

_mesh_agentless() {
  local target positive
  _mesh_peer || return 2
  python3 "$FIXTURES/recipient.py" modal "$CALLEE" "$PREFIX.recipient" || return 2
  python3 "$HERE/chat.py" inline "$CALLEE.target" "$PREFIX.recipient/recipient-steps.json" recipient-modal || return 2
  poll recipient-modal 10 _mesh_composer_ready 'Choose target' || return 2
  _mesh_before || return 2
  python3 "$FIXTURES/agentless.py" "$HERE" "$CALLER" "$CALLEE" "$PREFIX.agentless" "$ID" || return 2
  poll recipient-reregistered 15 mesh_state "$CALLEE" idle || return 2
  mesh_select "$BRANCH" new-from-cli "$PREFIX.positive.json" || return 2
  positive="$PREFIX.positive.json"
  target="$(jq -er '.session_id' "$positive")" || return 2
  mesh_mcp "$CALLER" call "$(jq -nc --arg target "$target" --arg message "agentless-positive mock-engine: $(jq -c . "$FIXTURES/turn.json")" '{target:$target,message:$message}')" "$PREFIX.positive-call" || return 2
  assert "$ID" 0 "$(jq -r '.code' "$PREFIX.positive-call.result.json")" "$PREFIX.positive-call.result.json"
  poll positive-peer-answer 15 _mesh_child_answer "$target" pong || return 2
  mesh_bundle_actors "$ID" "$PREFIX.before.json" "$CALLER" "$CALLEE" "$positive" || return 2
  mesh_action "$ID" chat.call mcp
  pass
  return 0
}

_mesh_row() {
  local row="$1" ids="$2" factors="$3" slug origin=new-typed result
  export JOURNEY_BRANCH="${row#*@}"
  BRANCH="${JOURNEY_BRANCH%%+to-*}"
  slug="${row%%@*}"; slug="${slug#*/}"
  ID="${ids%% *}"
  if [ "$slug" = call-dead-refused ] && [ ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed" ]; then
    need mesh-dead-shell _mesh_dead_ready _mesh_dead_create || return 2
  fi
  # Capability ids are space-separated registry keys, not shell input.
  beat "$slug" $ids || return
  PREFIX="$JOURNEY_OUT/beats/$PFM_BEAT/mesh"
  CALLEE_ORIGIN=''
  case "$slug" in call-from-cli-born-chat) origin=new-from-cli; CALLEE_ORIGIN=scout ;; call-from-picker-born-chat) origin=new-from-picker; CALLEE_ORIGIN=scout ;; esac
  _mesh_caller "$origin"; result=$?
  if [ "$result" -ne 0 ]; then
    _mesh_missing_or_failed "chat-birth:$BRANCH:$origin" "$result" "$BRANCH"; return 0
  fi
  if [ "$factors" != - ]; then
    case "$factors" in
      herdr_shield=on,door=call|multiplexer=herdr,door=call|pane_state=blocked,door=call) ;;
      process_table=sysctl_not_procfs,live_chat=claude_session_running,os=macos)
        assert "$ID" macos "$MESH_OS" runner-platform
        ;;
      *) fail "forecast precondition needs shared fault setup: $factors"; return ;;
    esac
  fi
  case "$slug" in
    spawn-child-from-mcp) _mesh_spawn ;;
    keys-and-capture-child) _mesh_keys ;;
    call-from-mcp|call-from-cli-born-chat|call-from-picker-born-chat|forecast-herdr-shield-on-door-call|forecast-multiplexer-herdr-door-call|forecast-process-table-sysctl-not-procfs-live-chat-claude-session-running-os-macos) _mesh_call mcp ;;
    call-from-cli) _mesh_call cli ;;
    call-*-recipient-*) _mesh_call cli "${slug##*-}" ;;
    call-dead-refused) _mesh_dead ;;
    mcp-new-returns-before-child-ready) _mesh_spawn readiness ;;
    agentless-call-preserves-modal-composer) _mesh_agentless ;;
    *) fail "no owner-entry implementation for $slug" ;;
  esac || fail "owner entry or observation failed; see $PREFIX artifacts"
}

while IFS=$'\t' read -r ROW IDS FACTORS; do
  case "$ROW" in *@"$MESH_OS"|*@"$MESH_OS"+*) _mesh_row "$ROW" "$IDS" "$FACTORS" ;; esac
done <"$FIXTURES/rows.tsv"
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"
