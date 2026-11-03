# Native quota and continuation entries, composed by the reload lane.
_reload_current_birth() {
  PYTHONPATH="$HERE:$FIXTURE" python3 - "$1" "$2" <<'PY'
import json, sys
from observation_process import engine_births
from projection import carried
from runtime import read_json
births = engine_births(carried(read_json(sys.argv[1]), sys.argv[2]), "claude")
if len(births) != 1:
    raise ValueError(f"CONTINUED-ENGINE: expected one actual engine birth; actual={births}")
print(json.dumps({key: births[0][key] for key in ("pid", "start")}, separators=(",", ":")))
PY
}
_reload_quota_ready() {
  python3 "$HERE/terminal.py" screen "$1" >"$3.screen" || return 2
  python3 - "$3.screen" "$2" <<'PY' >"$3"
import json, re, sys
from pathlib import Path
lines = [line for line in Path(sys.argv[1]).read_text().splitlines() if "7d-used:" in line]
actual = sorted(re.findall(r"(?:5h|7d)-used:(?:[0-9]+%|ERR|—)", lines[-1])) if lines else []
print(json.dumps(actual, separators=(",", ":")))
sys.exit(0 if actual == json.loads(sys.argv[2]) else 1)
PY
}
_reload_quota_turn() {
  PYTHONPATH="$HERE" python3 - "$1" "$2" <<'PY' || return 2
import sys
from runtime import read_json, write_json
write_json(sys.argv[2], [{"type": "turn", "reply": "recorded quota rendered", "busy_ms": 1,
                         "rate_limits": read_json(sys.argv[1])}])
PY
}
_reload_quotas() {
  local peer="$ARTIFACT/quota-peer.json" target="$ARTIFACT/quota-peer-target.json"
  local native="$HERE/../../../pfm/internal/mockengine/testdata/statusline"
  local CHAT="$ARTIFACT/quota-switch-chat.json" CHAT_ORIGIN="quota-switch-source-$PFM_RUN" SID NAME
  local JOURNEY_TARGET="$ARTIFACT/quota-switch-target.json"
  python3 "$HERE/chat.py" ensure-role "$CHAT_BRANCH" quota-account1 "quota-account1-${CHAT_BRANCH//+/-}" >"$peer" || return 2
  jq '.terminal' "$peer" >"$target" || return 2
  python3 "$HERE/terminal.py" type "$target" /stats || return 2
  _reload_quota_turn "$native/quota89.json" "$ARTIFACT/quota-one.json" || return 2
  python3 "$HERE/chat.py" inline "$target" "$ARTIFACT/quota-one.json" 'render account one quota' || return 2
  poll account-one-quota 20 _reload_quota_ready "$target" '["5h-used:89%","7d-used:34%"]' "$ARTIFACT/quota-one.actual" || return "$?"
  assert report.statusline '["5h-used:89%","7d-used:34%"]' "$(cat "$ARTIFACT/quota-one.actual")" "$ARTIFACT/quota-one.actual.screen"
  python3 "$HERE/chat.py" ensure-role "$CHAT_BRANCH" "$CHAT_ORIGIN" quota-switch-source "$FIXTURE/continued.json" >"$CHAT" || return 2
  jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
  SID="$(jq -er '.session_id' "$CHAT")" NAME="$(jq -er '.name' "$CHAT")"
  assert "$ID" 1 "$(jq -r '.account' "$CHAT")" "$CHAT:account"
  python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" /stats || return 2
  lane_input "$ARTIFACT/quota-one.json" 'render source account one quota' || return 2
  poll source-account-one-quota 20 _reload_quota_ready "$JOURNEY_TARGET" '["5h-used:89%","7d-used:34%"]' "$ARTIFACT/quota-source.actual" || return "$?"
  assert report.statusline '["5h-used:89%","7d-used:34%"]' "$(cat "$ARTIFACT/quota-source.actual")" "$ARTIFACT/quota-source.actual.screen"
  _reload_run reload-into-account || return "$?"
  python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" /stats || return 2
  _reload_quota_turn "$native/weekly-only.json" "$ARTIFACT/quota-two.json" || return 2
  lane_input "$ARTIFACT/quota-two.json" 'render account two quota' || return 2
  poll account-two-quota 20 _reload_quota_ready "$JOURNEY_TARGET" '["7d-used:34%"]' "$ARTIFACT/quota-two.actual" || return "$?"
  assert report.statusline '["7d-used:34%"]' "$(cat "$ARTIFACT/quota-two.actual")" "$ARTIFACT/quota-two.actual.screen"
  poll original-account-quota-preserved 20 _reload_quota_ready "$target" '["5h-used:89%","7d-used:34%"]' "$ARTIFACT/quota-one-after.actual" || return "$?"
  assert report.statusline '["5h-used:89%","7d-used:34%"]' "$(cat "$ARTIFACT/quota-one-after.actual")" "$ARTIFACT/quota-one-after.actual.screen"
  observe || return 2
  local caches
  caches="$(PYTHONPATH="$HERE:$FIXTURE" python3 - "$peer" "$CHAT" <<'PY'
import json, sys
from observation import account_stats_cache
from projection import account_caches
from runtime import read_json
one = account_stats_cache(read_json(sys.argv[1]))["1"]
two = account_stats_cache(read_json(sys.argv[2]))["2"]
print(json.dumps(account_caches(one, two), separators=(",", ":")))
PY
)" || return 2
  assert "$ID" '{"one":["regular-file",true,false],"two":["regular-file",true,false],"identity":"distinct"}' "$caches" "$JOURNEY_OBSERVATION:account_stats_cache"
}
_reload_continuation_ready() {
  python3 "$HERE/chat.py" adopt-continuation "$CONTINUED_TARGET" "$CHAT_BRANCH" "$CHAT_ORIGIN" "$CONTINUED_ORIGIN" "$ARTIFACT/continued-before.json" "$CONTINUED_PROJECT" "$CONTINUED_WORKSPACE" >"$CHAT.next" || return "$?"
  mv "$CHAT.next" "$CHAT" || return 2
  lane_refresh
}
_reload_native_session_read() {
  PYTHONPATH="$HERE" python3 - "$CHAT" "$ARTIFACT/continued-presence.json" <<'PYCODE'
import sys
from continuation import native
from runtime import read_json, write_json
value = native(read_json(sys.argv[1]))
write_json(sys.argv[2], value)
if value == "n/a:native-session-absent":
    sys.exit(1)
if not isinstance(value, dict):
    raise ValueError(f"NATIVE-SESSION-SHAPE: {value!r}")
PYCODE
}
_reload_native_identity() {
  local expected_id="$1" cwd="$2" birth="${3:-}" pid
  if [ -z "$birth" ]; then
    python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/continued-native.json" >/dev/null || return 2
    birth="$(_reload_current_birth "$ARTIFACT/continued-native.json" "$SID")" || return 2
  fi
  pid="$(jq -r .pid <<<"$birth")" || return 2
  poll actual-native-session-file 20 _reload_native_session_read || return "$?"
  assert "$ID" "$pid" "$(jq -r '.pid' "$ARTIFACT/continued-presence.json")" "$ARTIFACT/continued-presence.json"
  assert "$ID" "$expected_id" "$(jq -r '.sessionId' "$ARTIFACT/continued-presence.json")" "$ARTIFACT/continued-presence.json"
  assert "$ID" "$cwd" "$(jq -r '.cwd' "$ARTIFACT/continued-presence.json")" "$ARTIFACT/continued-presence.json"
}
_reload_continue() {
  local previous_id="$SID" cwd home
  lane_state idle "$ARTIFACT/continued-status.json" || return "$?"
  cwd="$(jq -er '.cwd' "$ARTIFACT/continued-status.json")" || return 2
  python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/continued-parent.json" >/dev/null || return 2
  home="$(jq -er --arg sid "$SID" '[.chats[] | select(.status.session_id==$sid) | .process_births[].environment.CLAUDE_CONFIG_DIR | select(.!=null)] | unique | if length==1 then .[0] else error("ambiguous continued engine home") end' "$ARTIFACT/continued-parent.json")" || return 2
  python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" /exit || return 2
  poll previous-continued-seat-ended 20 lane_state dead "$ARTIFACT/continued-ended.json" || return "$?"
  CONTINUED_TARGET="$ARTIFACT/continued-target.json"
  CONTINUED_PROJECT="$(jq -er '.project' "$CHAT")" CONTINUED_WORKSPACE="$(jq -er '.workspace // .project' "$CHAT")"
  CONTINUED_ORIGIN="continued-${PFM_BEAT##*/}"
  CONTINUED_ORIGIN="${CONTINUED_ORIGIN//@/-}" CONTINUED_ORIGIN="${CONTINUED_ORIGIN//+/-}"
  python3 "$HERE/chat.py" shell "$CHAT_BRANCH" "$CONTINUED_TARGET" "$CONTINUED_WORKSPACE" "$cwd" >/dev/null || return 2
  python3 "$HERE/chat.py" scenario "$CONTINUED_TARGET" "$CHAT_BRANCH" "$CONTINUED_ORIGIN" "$FIXTURE/continued.json" "$CONTINUED_PROJECT" "$CONTINUED_WORKSPACE" >/dev/null || return 2
  python3 "$HERE/chat.py" before "$CONTINUED_TARGET" "$ARTIFACT/continued-before.json" || return 2
  python3 "$HERE/terminal.py" command "$CONTINUED_TARGET" env "CLAUDE_CONFIG_DIR=$home" claude --continue || return 2
  poll native-continued-seat 30 _reload_continuation_ready || return "$?"
  assert "$ID" "$previous_id" "$SID" "$CHAT:session_id"
  lane_state idle "$ARTIFACT/continued-native-status.json" || return "$?"
  assert "$ID" "$(jq -c '{cwd,model,engine}' "$ARTIFACT/continued-status.json")" "$(jq -c '{cwd,model,engine}' "$ARTIFACT/continued-native-status.json")" "$ARTIFACT/continued-native-status.json"
  _reload_native_identity "$previous_id" "$cwd" || return "$?"
  python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" >/dev/null || return 2
  _reload_run reload-continued-chat
}
_reload_launch_id() {
  PYTHONPATH="$HERE" python3 - "$CHAT" <<'PYCODE'
import sys
from chat import seats
from runtime import read_json
chat = read_json(sys.argv[1])
found = [row for row in seats() if row["seat_id"] == chat["seat_id"] and row["state"] == "live"]
if len(found) != 1:
    raise ValueError(f"HANDOFF-LAUNCH-SEAT: expected one actual live seat; actual={found}")
print(found[0]["session_id"])
PYCODE
}
_reload_indexed() {
  PYTHONPATH="$HERE" python3 - "$CHAT" "$1" <<'PY'
import sys
from continuation import indexed
from runtime import read_json, write_json
write_json(sys.argv[2], indexed(read_json(sys.argv[1])))
PY
}
_reload_handoff_ready() {
  _reload_indexed "$ARTIFACT/handoff-index-after.json" || return 2
  [ "$(jq -er '.tail_id' "$ARTIFACT/handoff-index-after.json")" != "$HANDOFF_PREDECESSOR" ]
}
_reload_handoff_turn_ready() {
  local SID="$HANDOFF_SUCCESSOR"
  lane_state idle "$ARTIFACT/handoff-status-after.json" || return "$?"
  jq -e '.last == "handoff segment ready"' "$ARTIFACT/handoff-status-after.json" >/dev/null
}
_reload_handoff_record() {
  local actual expected
  _reload_shell cat "$HANDOFF_PREDECESSOR_PATH" || return 2
  poll native-handoff-record-read 20 _reload_receipt || return "$?"
  assert "$ID" 0 "$(cat "$ARTIFACT/command.status")" "$ARTIFACT/command.status"
  cp "$ARTIFACT/command.out" "$ARTIFACT/handoff-predecessor.jsonl" || return 2
  actual="$(PYTHONPATH="$FIXTURE" python3 - "$ARTIFACT/handoff-predecessor.jsonl" "$HANDOFF_PREDECESSOR" <<'PY'
import json, sys
from pathlib import Path
from handoff import continuation_records
print(json.dumps(continuation_records(Path(sys.argv[1]).read_text(), sys.argv[2]), separators=(",", ":")))
PY
)" || return 2
  expected="$(jq -nc --arg old "$HANDOFF_PREDECESSOR" --arg next "$HANDOFF_SUCCESSOR" '[{type:"continued-in",sessionId:$old,continuedInSessionId:$next}]')" || return 2
  assert "$ID" "$expected" "$actual" "$ARTIFACT/handoff-predecessor.jsonl"
}
_reload_handoff() {
  local mode="${1:-resume}" before after cwd home successor
  _reload_indexed "$ARTIFACT/handoff-index-before.json" || return 2
  HANDOFF_PREDECESSOR="$(jq -er '.tail_id' "$ARTIFACT/handoff-index-before.json")" || return 2
  python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" >/dev/null || return 2
  python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/handoff-before.json" >/dev/null || return 2
  HANDOFF_LAUNCH_ID="$(_reload_launch_id)" || return 2
  HANDOFF_PREDECESSOR_PATH="$(jq -er --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .stored.transcript.path' "$ARTIFACT/handoff-before.json")" || return 2
  before="$(_reload_current_birth "$ARTIFACT/handoff-before.json" "$SID")" || return 2
  lane_state idle "$ARTIFACT/handoff-status-before.json" || return "$?"
  cwd="$(jq -er '.cwd' "$ARTIFACT/handoff-status-before.json")" || return 2
  home="$(jq -er --arg sid "$SID" '[.chats[] | select(.status.session_id==$sid) | .process_births[].environment.CLAUDE_CONFIG_DIR | select(.!=null)] | unique | if length==1 then .[0] else error("ambiguous handoff engine home") end' "$ARTIFACT/handoff-before.json")" || return 2
  successor="$(PYTHONPATH="$HERE" python3 - "$ARTIFACT/handoff.json" <<'PY'
import sys, uuid
from runtime import write_json
session_id = str(uuid.uuid4())
write_json(sys.argv[1], [{"type": "background_handoff", "session_id": session_id},
                         {"type": "turn", "reply": "handoff segment ready", "busy_ms": 1}])
print(session_id)
PY
)" || return 2
  lane_input "$ARTIFACT/handoff.json" 'continue research in background' || return 2
  poll native-background-handoff 30 _reload_handoff_ready || return "$?"
  HANDOFF_SUCCESSOR="$(jq -er '.tail_id' "$ARTIFACT/handoff-index-after.json")" || return 2
  poll native-handoff-turn-idle 20 _reload_handoff_turn_ready || return "$?"
  assert "$ID" "$successor" "$HANDOFF_SUCCESSOR" "$ARTIFACT/handoff-index-after.json:tail_id"
  assert "$ID" "$HANDOFF_LAUNCH_ID" "$(jq -r '.launch_id' "$ARTIFACT/handoff-index-after.json")" "$ARTIFACT/handoff-index-after.json:launch_id"
  _reload_handoff_record || return "$?"
  poll native-handoff-record-probe-idle 10 lane_state idle "$ARTIFACT/handoff-record-probe-status.json" || return "$?"
  python3 "$HERE/terminal.py" read "$JOURNEY_TARGET" >"$ARTIFACT/handoff-terminal.json" || return 2
  after="$(PYTHONPATH="$HERE:$FIXTURE" python3 - "$before" <<'PYCODE'
import json, sys
from observation import process_snapshot
from projection import birth_presence
print(birth_presence(json.loads(sys.argv[1]), process_snapshot()))
PYCODE
)" || return 2
  assert "$ID" present "$after" "$ARTIFACT/handoff-terminal.json:process_births"
  assert "$ID" "$HANDOFF_LAUNCH_ID" "$(_reload_launch_id)" "actual-store-seat:$CHAT"
  assert "$ID" 'handoff segment ready' "$(jq -r '.last' "$ARTIFACT/handoff-status-after.json")" "$ARTIFACT/handoff-status-after.json"
  assert "$ID" "$(jq -c '{cwd,model,engine}' "$ARTIFACT/handoff-status-before.json")" "$(jq -c '{cwd,model,engine}' "$ARTIFACT/handoff-status-after.json")" "$ARTIFACT/handoff-status-after.json"
  _reload_native_identity "$HANDOFF_SUCCESSOR" "$cwd" "$before" || return "$?"
  if [ "$mode" = lost-index ]; then _reload_lost_index "$before"; return "$?"; fi
  _reload_run reload-continued-chat "$HANDOFF_LAUNCH_ID" continuation
}
_reload_lost_index_role_create() {
  python3 "$HERE/chat.py" ensure-role "$CHAT_BRANCH" "$CHAT_ORIGIN" lost-index-retry "$FIXTURE/continued.json" "$LOST_INDEX_PROJECT" "$LOST_INDEX_WORKSPACE" >"$CHAT" || return 2
  LOST_INDEX_CREATED=1
}
_reload_control_receipt_ready() { test -s "$1.status"; }
_reload_lost_index_isolated() {
  local CHAT="$ARTIFACT/lost-index-chat.json" CHAT_ORIGIN=lost-index-retry SID NAME LOST_INDEX_CREATED=0 LOST_INDEX_PROJECT LOST_INDEX_WORKSPACE
  local JOURNEY_TARGET="$ARTIFACT/lost-index-native-target.json" owner_workspace
  python3 "$HERE/workspace.py" need atlas "lost-index-retry:$PFM_BEAT" named >"$ARTIFACT/lost-index-workspace.json" || return 2
  LOST_INDEX_PROJECT="$(jq -er '.project' "$ARTIFACT/lost-index-workspace.json")" LOST_INDEX_WORKSPACE="$(jq -er '.id' "$ARTIFACT/lost-index-workspace.json")"
  need lost-index-isolated-native-role lane_check _reload_lost_index_role_create || return 2
  jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
  SID="$(jq -er '.session_id' "$CHAT")" NAME="$(jq -er '.name' "$CHAT")"
  local initializer initializer_refs initializers
  python3 "$HERE/workspace.py" read atlas "lost-index-retry:$PFM_BEAT" named >"$ARTIFACT/lost-index-workspace-current.json" || return 2
  initializers="$(jq -er --slurpfile chat "$CHAT" 'if $chat[0].launch_target.backend!="herdr" then error("lost-index launch target must be Herdr") else [.target.pane,$chat[0].launch_target.pane] | unique[] end' "$ARTIFACT/lost-index-workspace-current.json")" || return 2
  while IFS= read -r initializer; do
    if jq -e --arg pane "$initializer" '.panes | any(.pane_id==$pane)' "$ARTIFACT/lost-index-workspace-current.json" >/dev/null; then
      initializer_refs="$(jq --arg pane "$initializer" '[.chats[].terminal,.foreign.terminal,.owner,.client,.picker] | map(select(type=="object" and .backend=="herdr" and .pane==$pane)) | length' "$JOURNEY_CARRY")" || return 2
      if [ "$initializer_refs" -ne 0 ]; then printf 'LOST-INDEX-INITIALIZER: cannot close a carried terminal\n' >&2; return 2; fi
      # Both unused setup shells must end before their isolated project cwd is removed.
      python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" pane.close "$(jq -nc --arg pane "$initializer" '{pane_id:$pane}')" >>"$ARTIFACT/lost-index-initializer-close.jsonl" || return 2
    fi
  done <<<"$initializers"
  if [ "$LOST_INDEX_CREATED" -eq 1 ]; then lane_input "$FIXTURE/lost-index-ready.json" 'prepare isolated indexed-tail loss chat' || return 2; fi
  poll isolated-lost-index-chat-idle 20 lane_state idle "$ARTIFACT/lost-index-status.json" || return "$?"
  python3 "$HERE/terminal.py" read "$PICKER" >"$ARTIFACT/lost-index-picker-terminal.json" || return 2
  owner_workspace="$(jq -er '.identity.workspace' "$ARTIFACT/lost-index-picker-terminal.json")" || return 2
  python3 "$HERE/chat.py" shell "$CHAT_BRANCH" "$ARTIFACT/lost-owner.json" "$owner_workspace" "$HOME" >/dev/null || return 2
  python3 "$HERE/terminal.py" receipt "$ARTIFACT/lost-owner.json" "$ARTIFACT/owner-cwd" pwd -P || return 2
  poll lost-index-control-shell-cwd 10 _reload_control_receipt_ready "$ARTIFACT/owner-cwd" || return "$?"
  assert "$ID" 0 "$(cat "$ARTIFACT/owner-cwd.status")" "$ARTIFACT/owner-cwd.status"
  _reload_handoff lost-index
}
_reload_lost_index() {
  local before="$1" cwd expression held actual argument expected
  cwd="$(jq -er '.cwd' "$ARTIFACT/continued-presence.json")" || return 2
  expression="$(python3 - "$cwd" <<'PY'
import re, sys
print(re.escape("chdir " + sys.argv[1] + ": no such file or directory"))
PY
)" || return 2
  expect-log "$expression"
  _reload_before "$ARTIFACT/handoff-before.json" || return "$?"
  DOOR=typed
  PYTHONPATH="$HERE:$FIXTURE" python3 "$FIXTURE/lost_index.py" "$CHAT" "$ARTIFACT/lost-owner.json" "$ARTIFACT/handoff-index-after.json" "$before" "$ARTIFACT" || return "$?"
  expected="$(jq -c '["pfm","chat","reload",.launch_id]' "$ARTIFACT/handoff-index-after.json")" || return 2
  for argument in prepare-command retry-command; do
    poll "lost-index-$argument-complete" 20 _reload_control_receipt_ready "$ARTIFACT/$argument" || return "$?"
    assert "$ID" 0 "$(cat "$ARTIFACT/$argument.status")" "$ARTIFACT/$argument.status"
    assert "$ID" "$expected" "$expected" "owner-input:$ARTIFACT/$argument"
  done
  assert "$ID" 'n/a:not-started' "$(jq -r '.replacement_birth' "$ARTIFACT/failed-seat-preparation.json")" "$ARTIFACT/failed-seat-preparation.json"
  assert "$ID" true "$(jq -r '.cwd_restored and .host_continued' "$ARTIFACT/failed-seat-preparation.json")" "$ARTIFACT/failed-seat-preparation.json"
  _reload_complete continuation reload-from-cli || return "$?"
  held="$(jq -c . "$ARTIFACT/held-replacement.json")" || return 2
  actual="$(_reload_current_birth "$JOURNEY_OBSERVATION" "$SID")" || return 2
  assert "$ID" "$held" "$actual" "$ARTIFACT/held-replacement.json"
  assert "$ID" 1 "$(jq '[.activity.records[] | select(.msg=="reload.failed")]|length' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION:activity"
}
