#!/usr/bin/env bash
# Readers consume the mesh carry before restart and ending change its lifecycle.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
. "$HERE/fixtures/fleet-reader/identity-readers.sh"
export JOURNEY_LANE=fleet-reader
FIXTURES="$HERE/fixtures/fleet-reader"
READER_OS="${JOURNEY_OS:?runner must select linux or macos}"
case "$READER_OS" in linux) HERDR=herdr-shield ;; macos) HERDR=herdr-native ;; *) exit 2 ;; esac

_reader_ready() { python3 "$HERE/chat.py" need "$READER_OS+$HERDR+project+claude" new-from-cli >/dev/null; }
_reader_create() { python3 "$HERE/chat.py" ensure "$READER_OS+$HERDR+project+claude" new-from-cli "$HERE/fixtures/chat-mesh/seed.json" >/dev/null; }
need herdr-claude-cli-caller _reader_ready _reader_create || exit 2
READER_SHELL="$JOURNEY_ROOT/reader-shell.json"
python3 "$HERE/chat.py" shell "$READER_OS+$HERDR+project+claude" "$READER_SHELL" '~' "$HOME" >/dev/null || exit 2
READER_OPERATOR="$JOURNEY_ROOT/reader-operator.json"
python3 "$HERE/terminal.py" create >"$READER_OPERATOR" || exit 2
_reader_close_operator() {
  local result=$?
  trap - EXIT
  python3 "$HERE/terminal.py" close "$READER_OPERATOR" || result=1
  exit "$result"
}
trap _reader_close_operator EXIT
python3 - "$HERE" "$READER_OPERATOR" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from bootstrap import wait
from chat import shell_ready
from runtime import read_json
owner = read_json(sys.argv[2])
wait("reader CLI operator shell", lambda: shell_ready(owner), 20)
PY
[ "$?" -eq 0 ] || exit 2

_reader_before() { python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$PREFIX.before.json"; }
_reader_finish() { mesh_bundle "$ID" "$PREFIX.before.json" "$(jq -r 'if .key then "key:"+.key else .name end' "$CHAT")" "$@"; }
_reader_invocation() {
  export JOURNEY_TARGET="$OWNER"
  assert "$ID" "$2" "$(jq -c '.argv' "$1.dispatch.json")" "owner-input:$1"
}
_reader_known() {
  if python3 "$HERE/machine.py" gap-only "$JOURNEY_OUT/$JOURNEY_LANE.state.json" "$1"; then
    known "$2"
  else
    fail 'the approved gap is not the sole failed assertion class'
  fi
  return 0
}

_reader_missing_actor() {
  local missing_id
  export JOURNEY_TARGET="$OWNER"
  mesh_command "$OWNER" "$PREFIX.missing-actor" pfm chat new --engine opencode --backend tmux --name "reader-missing-${JOURNEY_ROOT##*/}" --cwd "$HOME/work/atlas" || return 2
  _reader_invocation "$PREFIX.missing-actor" "$(jq -nc --arg name "reader-missing-${JOURNEY_ROOT##*/}" --arg cwd "$HOME/work/atlas" '["pfm","chat","new","--engine","opencode","--backend","tmux","--name",$name,"--cwd",$cwd]')"
  for missing_id in $IDS; do
    assert "$missing_id" 0 "$(cat "$PREFIX.missing-actor.status")" ruling:ruling-gap-opencode-tmux
  done
  assert "$ID" 2 "$(cat "$PREFIX.missing-actor.status")" "$PREFIX.missing-actor.status"
  assert "$ID" 'pfm chat new: OpenCode does not support headless chat' "$(cat "$PREFIX.missing-actor.out")" "$PREFIX.missing-actor.out"
  observe || return 2
  _reader_known ruling:ruling-gap-opencode-tmux 'ruling:ruling-gap-opencode-tmux; required actor has no supported birth entry'
  return 0
}

_reader_identity() {
  local door="$1" output
  _reader_before || return 2
  if [ "$door" = cli ]; then
    mesh_shell "$CHAT" "$PREFIX.whoami" pfm whoami --json || return 2
    poll engine-whoami-receipt 15 mesh_command_ready || return 2
    mesh_input_finish "$PREFIX.whoami"
    assert "$ID" 0 "$(cat "$PREFIX.whoami.status")" "$PREFIX.whoami.status"
    poll engine-idle-after-whoami 15 mesh_state "$CHAT" idle || return 2
    output="$PREFIX.whoami.out"
  else
    mesh_mcp "$CHAT" whoami '{}' "$PREFIX.whoami" || return 2
    output="$PREFIX.whoami.result.json"
  fi
  python3 "$FIXTURES/projection.py" identity "$output" "$CHAT" >"$PREFIX.identity-checks.json" || return 2
  mesh_checks "$ID" "$PREFIX.identity-checks.json"
  _reader_finish || return 2
  pass
  return 0
}

_reader_turn() {
  python3 "$HERE/chat.py" inline "$CHAT.target" "$FIXTURES/turn.json" reader-prompt &&
    poll reader-working 10 mesh_state "$CHAT" working && poll reader-idle 15 mesh_state "$CHAT" idle
}

_reader_status() {
  local sid
  sid="$(jq -er '.session_id' "$CHAT")" || return 2
  mesh_select "$BRANCH" reader-status-sibling "$PREFIX.sibling.json" || return 2
  assert chat.status-mcp distinct "$(jq -r --arg sid "$sid" 'if .session_id!=$sid then "distinct" else "self" end' "$PREFIX.sibling.json")" "$PREFIX.sibling.json"
  _reader_before && _reader_turn || return 2
  mesh_command "$OWNER" "$PREFIX.status" pfm chat status "$sid" --json || return 2
  _reader_invocation "$PREFIX.status" "$(jq -nc --arg sid "$sid" '["pfm","chat","status",$sid,"--json"]')"
  assert chat.status-cli 0 "$(cat "$PREFIX.status.status")" "$PREFIX.status.status"
  assert chat.status-cli idle "$(jq -r '.state' "$PREFIX.status.out")" "$PREFIX.status.out"
  assert chat.status-cli "$sid" "$(jq -r '.session_id' "$PREFIX.status.out")" "$PREFIX.status.out"
  mesh_mcp "$PREFIX.sibling.json" status "$(jq -nc --arg target "$sid" '{target:$target}')" "$PREFIX.status-mcp" || return 2
  assert chat.status-mcp idle "$(jq -r '.state' "$PREFIX.status-mcp.result.json")" "$PREFIX.status-mcp.result.json"
  mesh_mcp "$PREFIX.sibling.json" last "$(jq -nc --arg target "$sid" '{target:$target}')" "$PREFIX.last" || return 2
  assert chat.last-mcp reader-answer "$(jq -r '.text' "$PREFIX.last.result.json")" "$PREFIX.last.result.json"
  _reader_finish "$(jq -r '.name' "$PREFIX.sibling.json")" || return 2
  pass
  return 0
}

_reader_transcript() {
  _reader_before && _reader_turn || return 2
  mesh_command "$OWNER" "$PREFIX.transcript" pfm transcript show "$(jq -r '.session_id' "$CHAT")" || return 2
  _reader_invocation "$PREFIX.transcript" "$(jq -nc --arg sid "$(jq -r '.session_id' "$CHAT")" '["pfm","transcript","show",$sid]')"
  assert "$ID" 0 "$(cat "$PREFIX.transcript.status")" "$PREFIX.transcript.status"
  # The command must attribute the same submitted turn that the engine wrote.
  _reader_finish || return 2
  jq -c --arg name "$(jq -r '.name' "$CHAT")" \
    '.chats[]|select(.name==$name)|.stored.transcript.entries[-2:]|map({role,text})' "$JOURNEY_OBSERVATION" >"$PREFIX.turn.json"
  golden "$ID" fleet-reader/turn.json "$PREFIX.turn.json"
  assert "$ID" reader-answer "$(sed -n '/^reader-answer$/p' "$PREFIX.transcript.out")" "$PREFIX.transcript.out"
  pass
  return 0
}

_reader_ls() {
  local door="$1" output
  _reader_before || return 2
  if [ "$door" = cli ]; then
    if [ "$SLUG" = ls-from-cli ]; then
      mesh_command "$OWNER" "$PREFIX.ls" pfm chat ls --json || return 2
      _reader_invocation "$PREFIX.ls" '["pfm","chat","ls","--json"]'
      assert "$ID" 0 "$(cat "$PREFIX.ls.status")" product-gap:machine-readable-chat-list
      _reader_finish || return 2
      _reader_known product-gap:machine-readable-chat-list 'public machine-readable chat ls entry is absent; until-product-flight'
      return 0
    fi
    mesh_command "$OWNER" "$PREFIX.ls" pfm ls --all --tsv || return 2
    _reader_invocation "$PREFIX.ls" '["pfm","ls","--all","--tsv"]'
    assert "$ID" 0 "$(cat "$PREFIX.ls.status")" "$PREFIX.ls.status"
    output="$PREFIX.ls.out"
  else
    mesh_mcp "$CHAT" ls '{"all":true,"limit":1000}' "$PREFIX.ls" || return 2
    output="$PREFIX.ls.result.json"
  fi
  if [ "$door" = cli ]; then
    python3 "$FIXTURES/projection.py" text-fleet "$output" "$JOURNEY_CARRY" >"$PREFIX.fleet-checks.json" || return 2
  else
    python3 "$FIXTURES/projection.py" fleet "$output" "$JOURNEY_CARRY" >"$PREFIX.fleet-checks.json" || return 2
    mesh_command "$OWNER" "$PREFIX.text-ls" pfm ls --all --tsv || return 2
    _reader_invocation "$PREFIX.text-ls" '["pfm","ls","--all","--tsv"]'
    assert "$ID" 0 "$(cat "$PREFIX.text-ls.status")" "$PREFIX.text-ls.status"
    python3 "$FIXTURES/projection.py" fleet-drift "$output" "$PREFIX.text-ls.out" "$JOURNEY_CARRY" >"$PREFIX.drift-checks.json" || return 2
    mesh_checks "$ID" "$PREFIX.drift-checks.json"
  fi
  mesh_checks "$ID" "$PREFIX.fleet-checks.json"
  _reader_finish || return 2
  pass
  return 0
}

_reader_picker_ready() {
  python3 "$HERE/terminal.py" screen "$OWNER" >"$PREFIX.picker-screen" || return 2
  rg -q 'New chat' "$PREFIX.picker-screen"
}

_reader_picker() {
  _reader_before || return 2
  python3 "$HERE/terminal.py" command "$OWNER" pfm || return 2
  poll fleet-picker-ready 10 _reader_picker_ready || return 2
  mesh_picker_target "$OWNER" || return 2
  _reader_finish || return 2
  local name
  while IFS= read -r name; do
    assert "$ID" "$name" "$(jq -r --arg name "$name" '.chats[]|select(.name==$name)|.picker[]|select(.badge.state==.state)|.name' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  done < <(jq -r '.chats[].name' "$JOURNEY_CARRY")
  mesh_action "$ID" picker.open typed
  python3 "$HERE/terminal.py" keys "$OWNER" escape || return 2
  pass
  return 0
}

_reader_statusline() {
  _reader_before || return 2
  python3 "$FIXTURES/statusline.py" resources "$CHAT" "$PREFIX" || return 2
  mesh_checks "$ID" "$PREFIX.resources-checks.json"
  python3 "$FIXTURES/statusline.py" run "$CHAT" "$CHAT.target" "$PREFIX.resources.json" "$PREFIX" || return 2
  mesh_checks "$ID" "$PREFIX.checks.json"
  assert "$ID" "$(cat "$PREFIX.expected-input.txt")" "$(cat "$PREFIX.input.txt")" "engine-input:$PREFIX.input.txt"
  _reader_finish || return 2
  python3 "$FIXTURES/statusline.py" verify "$PREFIX" "$JOURNEY_OBSERVATION" || return 2
  mesh_checks "$ID" "$PREFIX.warning-checks.json"
  pass
}

_reader_move() {
  _reader_before || return 2
  mesh_command "$OWNER" "$PREFIX.keybindings" herdr --default-config || return 2
  _reader_invocation "$PREFIX.keybindings" '["herdr","--default-config"]'
  assert "$ID" 0 "$(cat "$PREFIX.keybindings.status")" "$PREFIX.keybindings.status"
  # The pinned terminal exposes reordering only; the requested cross-workspace entry stays red.
  assert "$ID" move_tab_to_workspace "$(sed -n 's/^\(move_tab_to_workspace\)[[:space:]]*=.*/\1/p' "$PREFIX.keybindings.out")" product-gap:move-tab-owner-key
  _reader_finish || return 2
  _reader_known product-gap:move-tab-owner-key 'pinned Herdr has no cross-workspace move-tab key entry; until-product-flight'
  return 0
}

_reader_theme() {
  _reader_before || return 2
  mesh_command "$OWNER" "$PREFIX.theme" pfm theme || return 2
  _reader_invocation "$PREFIX.theme" '["pfm","theme"]'
  assert "$ID" 0 "$(cat "$PREFIX.theme.status")" "$PREFIX.theme.status"
  assert "$ID" 'pfm theme: off — pfm theme on turns it on' "$(cat "$PREFIX.theme.out")" "$PREFIX.theme.out"
  _reader_finish || return 2
  pass
  return 0
}

. "$FIXTURES/fault-readers.sh"
. "$FIXTURES/setup-readers.sh"
. "$FIXTURES/partial-readers.sh"
. "$FIXTURES/process-readers.sh"
. "$FIXTURES/native-readers.sh"

_reader_row() {
  local row="$1" ids="$2" factors="$3" slug actor_rc native_rc=1
  export JOURNEY_BRANCH="${row#*@}"
  BRANCH="$JOURNEY_BRANCH"
  case "$BRANCH" in "$READER_OS"|"$READER_OS+$HERDR") BRANCH="$READER_OS+$HERDR+project+claude" ;; esac
  case "$factors" in
    engine=codex,multiplexer=herdr) BRANCH="$READER_OS+$HERDR+project+codex" ;;
    engine=opencode,multiplexer=herdr) BRANCH="$READER_OS+$HERDR+project+opencode" ;;
    multiplexer=tmux,seat_event=seat_dead) BRANCH="$READER_OS+tmux+project+claude" ;;
  esac
  slug="${row%%@*}"; slug="${slug#*/}"
  SLUG="$slug"
  ID="${ids%% *}"
  READER_STATUSLINE_ROLE="$(_reader_statusline_role)" || READER_STATUSLINE_ROLE=
  READER_NATIVE_PLAN="$JOURNEY_ROOT/reader-native-plan.json"
  if [ "$factors" != - ]; then
    python3 "$FIXTURES/native_forecasts.py" plan "$factors" >"$READER_NATIVE_PLAN" 2>"$READER_NATIVE_PLAN.error"; native_rc=$?
    case "$native_rc" in
      0) BRANCH="$READER_OS+$HERDR+project+$(jq -r '.engine' "$READER_NATIVE_PLAN")" ;;
      1) ;;
      *) return 2 ;;
    esac
  fi
  if [ -e "$JOURNEY_OUT/$JOURNEY_LANE.failed" ]; then beat "$slug" $ids; return 0; fi
  case "$BRANCH" in
    *+tmux+*+opencode)
      python3 "$HERE/chat.py" select "$BRANCH" new-typed >"$JOURNEY_ROOT/reader-existing-opencode.json"; actor_rc=$?
      case "$actor_rc" in
      0) ;;
      1)
        beat "$slug" $ids || return
        PREFIX="$JOURNEY_OUT/beats/$PFM_BEAT/reader"
        OWNER="$READER_SHELL"
        _reader_missing_actor || fail 'required OpenCode tmux actor refusal could not be verified'
        return 0
      ;;
      *) return 2 ;;
      esac ;;
  esac
  # Prelude creation preserves the real CLI origin when the lane runs alone.
  mesh_ensure "$BRANCH" new-typed "$JOURNEY_ROOT/reader-current.json" || return 2
  if [ "$native_rc" -eq 0 ]; then _reader_native_prelude || return 2; fi
  if [ -n "$READER_STATUSLINE_ROLE" ]; then _reader_statusline_prelude || return 2; fi
  case "$slug" in
    index-keeps-good-engine-sessions-after-bad-record|tokens-names-partial-transcript-read|name-sync-preserves-good-name-after-partial-read|headless-names-partial-usage|headless-retains-valid-answer-after-partial-read) _reader_partial_prelude || return 2 ;;
    watch-keeps-child-activity-working|reap-preview-retains-live-foreign-owner|theme-keeps-healthy-rows-beside-stopped-tmux) _reader_process_prelude || return 2 ;;
    status-and-last)
      python3 "$HERE/chat.py" ensure-role "$BRANCH" reader-status-sibling "reader-sibling-${BRANCH//+/-}-${PFM_RUN: -8}" "$HERE/fixtures/chat-mesh/seed.json" >"$JOURNEY_ROOT/reader-status-sibling.json" || return 2 ;;
    picker-keeps-deleted-cwd-chat)
      READER_GONE_PROJECT="atlas-scratch-${PFM_RUN//[^a-z0-9-]/-}"
      READER_GONE_DIR="$HOME/work/$READER_GONE_PROJECT"
      python3 - "$HERE" "$READER_GONE_DIR" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from runtime import write_text
write_text(sys.argv[2] + "/fixture.txt", "Owned plain-folder cwd for the reader journey.\n")
PY
      python3 "$HERE/chat.py" ensure-role "$BRANCH" reader-gone "reader-gone-${PFM_RUN: -8}" "$HERE/fixtures/chat-mesh/seed.json" "$READER_GONE_PROJECT" atlas >"$JOURNEY_ROOT/reader-gone.json" || return 2 ;;
    ls-lists-lost-seat|picker-refresh-with-dead-seat|forecast-multiplexer-tmux-seat-event-seat-dead|forecast-seat-event-seat-dead-door-list)
      python3 "$HERE/chat.py" ensure-role "$BRANCH" reader-lost "reader-lost-${BRANCH//+/-}-${PFM_RUN: -8}" "$HERE/fixtures/chat-mesh/seed.json" >"$JOURNEY_ROOT/reader-lost.json" || return 2 ;;
    forecast-tmux-server-sigstopped-probe-site-gather-probe-without-deadline-client-pipe-held-by-stopped-server)
      mesh_ensure "$READER_OS+tmux+project+claude" new-typed "$JOURNEY_ROOT/reader-stopped.json" || return 2 ;;
  esac
  beat "$slug" $ids || return
  PREFIX="$JOURNEY_OUT/beats/$PFM_BEAT/reader"
  CHAT="$PREFIX.chat.json" OWNER="$PREFIX.owner.json"
  cp "$JOURNEY_ROOT/reader-current.json" "$CHAT"
  if [ -n "$READER_STATUSLINE_ROLE" ]; then cp "$JOURNEY_ROOT/reader-statusline.json" "$CHAT"; fi
  case "$slug" in
    name-sync-preserves-good-name-after-partial-read) cp "$JOURNEY_ROOT/reader-partial-name.json" "$CHAT" ;;
  esac
  case "$slug" in
    watch-keeps-child-activity-working|reap-preview-retains-live-foreign-owner|theme-keeps-healthy-rows-beside-stopped-tmux) cp "$JOURNEY_ROOT/reader-process.json" "$CHAT" ;;
  esac
  jq '.terminal' "$CHAT" >"$CHAT.target"
  export JOURNEY_TARGET="$CHAT.target"
  case "$SLUG" in
    picker-lists-the-fleet|picker-keeps-deleted-cwd-chat|picker-refresh-with-dead-seat|ls-lists-lost-seat|forecast-multiplexer-tmux-seat-event-seat-dead|forecast-seat-event-seat-dead-door-list)
      cp "$READER_SHELL" "$OWNER" ;;
    *) cp "$READER_OPERATOR" "$OWNER" ;;
  esac
  if [ "$native_rc" -eq 0 ]; then
    _reader_native || fail "native owner entry or observation failed; see $PREFIX artifacts"
    return 0
  fi
  SCRATCH="$JOURNEY_ROOT/reader-lost.json"
  if [ "$slug" = picker-keeps-deleted-cwd-chat ]; then SCRATCH="$JOURNEY_ROOT/reader-gone.json"; fi
  if [ "$slug" = forecast-seat-owner-herdr-seat-row-process-table-empty-kinfo-or-eio-os-macos ]; then
    _reader_unavailable_kernel || fail 'native unsupported-fault evidence could not be verified'
    return 0
  fi
  case "$factors" in
    *pane_reused*|*recycled_occupant*)
      _reader_before && _reader_finish && _reader_herdr_gap isolated-pane-recycle || fail 'pinned external capability evidence failed'
      return 0 ;;
  esac
  if [ -n "$READER_STATUSLINE_ROLE" ] && [ "$slug" != statusline-warns-on-partial-account-read ]; then
    _reader_statusline || fail "native statusline owner entry or observation failed; see $PREFIX artifacts"
    return 0
  fi
  if [ "$factors" != - ]; then
    case "$factors" in
      engine=codex,multiplexer=herdr|engine=opencode,multiplexer=herdr|multiplexer=herdr,door=list|tmux_server=sigstopped,probe_site=gather_probe_without_deadline,client_pipe=held_by_stopped_server|multiplexer=tmux,seat_event=seat_dead|seat_event=seat_dead,door=list|harness_payload=omits_usage_window,shared_cache=present_fresh,engine=claude_code) ;;
      *) fail "forecast precondition needs shared fault setup: $factors"; return ;;
    esac
  fi
  case "$slug" in
    whoami-from-cli) _reader_identity cli ;;
    whoami-from-mcp) _reader_identity mcp ;;
    status-and-last) _reader_status ;;
    transcript-reads-engine-log) _reader_transcript ;;
    ls-from-cli) _reader_ls cli ;;
    forecast-engine-codex-multiplexer-herdr|forecast-engine-opencode-multiplexer-herdr|forecast-multiplexer-herdr-door-list|forecast-tmux-server-sigstopped-probe-site-gather-probe-without-deadline-client-pipe-held-by-stopped-server) _reader_forecast ;;
    ls-from-mcp) _reader_ls mcp ;;
    picker-lists-the-fleet) _reader_picker ;;
    watch-keeps-child-activity-working|reap-preview-retains-live-foreign-owner|theme-keeps-healthy-rows-beside-stopped-tmux) _reader_process ;;
    index-keeps-good-engine-sessions-after-bad-record|tokens-names-partial-transcript-read|name-sync-preserves-good-name-after-partial-read|statusline-warns-on-partial-account-read|headless-names-partial-usage|headless-retains-valid-answer-after-partial-read) _reader_partial ;;
    whoami-after-workspace-move) _reader_move ;;
    theme-carried-fleet) _reader_theme ;;
    ls-lists-lost-seat|forecast-multiplexer-tmux-seat-event-seat-dead|forecast-seat-event-seat-dead-door-list) _reader_lost ;;
    picker-refresh-with-dead-seat) _reader_lost ;;
    picker-keeps-deleted-cwd-chat) _reader_deleted ;;
    *) fail "owner fixture setup is not implemented for $slug" ;;
  esac || fail "owner entry or observation failed; see $PREFIX artifacts"
}

while IFS=$'\t' read -r ROW IDS FACTORS; do
  case "$ROW" in *@"$READER_OS"|*@"$READER_OS"+*) _reader_row "$ROW" "$IDS" "$FACTORS" ;; esac
done <"$FIXTURES/rows.tsv"
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"
