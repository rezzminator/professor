#!/usr/bin/env bash
# Reload and its attention after-effects use the same carried chat and public doors.
lane_check() {
  python3 "$HERE/chat.py" select "$CHAT_BRANCH" "$CHAT_ORIGIN" >"$CHAT.next" || return "$?"
  python3 "$HERE/chat.py" need "$CHAT_BRANCH" "$CHAT_ORIGIN" || return "$?"
  mv "$CHAT.next" "$CHAT"
}
lane_create() {
  python3 "$HERE/chat.py" ensure "$CHAT_BRANCH" "$CHAT_ORIGIN" >"$CHAT" || return 2
  CHAT_ORIGIN="$(jq -er '.role // .origin' "$CHAT")"
}
lane_prepare() {
  CHAT_BRANCH="$1" CHAT_ORIGIN=new-typed
  CHAT="$JOURNEY_ROOT/$JOURNEY_LANE-chat.json"
  export JOURNEY_TARGET="$JOURNEY_ROOT/$JOURNEY_LANE-target.json"
  need "$CHAT_BRANCH:idle-chat" lane_check lane_create || return 2
  jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
  SID="$(jq -er '.session_id' "$CHAT")" NAME="$(jq -er '.name' "$CHAT")"
  PICKER="$JOURNEY_ROOT/$JOURNEY_LANE-picker.json"
  jq '.picker' "$JOURNEY_CARRY" >"$PICKER" || return 2
  python3 "$HERE/chat.py" picker-ready "$PICKER" || return 2
  poll carried-chat-idle 10 lane_state idle "$JOURNEY_ROOT/$JOURNEY_LANE-idle.json" || return 2
  python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" >/dev/null
}
lane_refresh() {
  python3 "$HERE/chat.py" refresh "$CHAT_BRANCH" "$CHAT_ORIGIN" >"$CHAT.next" || return "$?"
  mv "$CHAT.next" "$CHAT" || return 2
  jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
  SID="$(jq -er '.session_id' "$CHAT")" NAME="$(jq -er '.name' "$CHAT")"
}
lane_state() {
  PYTHONPATH="$HERE" python3 - "$SID" "$1" "$2" <<'PY'
import json, sys
from observation import report
from runtime import write_json
try:
    value = json.loads(report(["pfm", "chat", "status", sys.argv[1], "--json"], allowed_codes=(0, 3)))
    write_json(sys.argv[3], value)
    sys.exit(0 if value["state"] == sys.argv[2] else 1)
except (OSError, ValueError, KeyError, RuntimeError) as error:
    print(f"CHAT-STATE-UNREADABLE: {error}", file=sys.stderr)
    sys.exit(2)
PY
}
lane_rows() {
  python3 - "$FIXTURE/rows.tsv" "$JOURNEY_BRANCH" <<'PY'
import csv, sys
with open(sys.argv[1]) as source:
    for row in csv.DictReader(source, delimiter="\t"):
        if row["row"].split("@", 1)[1] == sys.argv[2]:
            print("|".join(row[key] for key in ("row", "ids", "fork_of", "forecast", "factors")))
PY
}
lane_input() {
  local fixture="$1" prompt="$2"
  python3 "$HERE/chat.py" inline "$JOURNEY_TARGET" "$fixture" "$prompt"
}
lane_bind() {
  HERE="$HERE" PYTHONPATH="$HERE" python3 - "$1" "$2" "$CHAT" "$HOLD" "${MAIN_STOP_HOLD:-}" <<'PY'
import json, os, sys
from pathlib import Path
from observation import report
from runtime import read_json, write_json
chat = read_json(sys.argv[3])
values = {"$HOLD": sys.argv[4], "$MAIN_STOP_HOLD": sys.argv[5], "$SESSION": chat["session_id"]}
template = read_json(sys.argv[1])
if "$BACKGROUND_COMMAND" in json.dumps(template):
    sys.path.insert(0, str(Path(os.environ["HERE"]) / "fixtures/chat-reload"))
    from background import background_command
    values["$BACKGROUND_COMMAND"] = background_command(sys.argv[4], Path(sys.argv[4]).parent / "background.pid")
if "$TRANSCRIPT" in json.dumps(template):
    transcript = json.loads(report(["pfm", "chat", "read", chat["session_id"], "--tail", "1", "--json"]))
    status = json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"]))
    values.update({"$TRANSCRIPT": transcript["path"], "$CWD": status["cwd"],
                   "$SIBLING_TRANSCRIPT": str(Path(transcript["path"]).with_suffix("") / "subagents/journey-sibling.jsonl"),
                   "$AGENT_TRANSCRIPT": str(Path(transcript["path"]).with_suffix("") / "subagents/journey-researcher.jsonl")})
def bind(value):
    if isinstance(value, dict):
        return {key: bind(item) for key, item in value.items()}
    if isinstance(value, list):
        return [bind(item) for item in value]
    if isinstance(value, str) and " mock-engine: " in value:
        prompt, encoded = value.split(" mock-engine: ", 1)
        return prompt + " mock-engine: " + json.dumps(bind(json.loads(encoded)), separators=(",", ":"))
    if isinstance(value, str) and value.startswith("$"):
        if value not in values or not values[value]:
            raise ValueError(f"UNBOUND-ATTENTION-FIXTURE: {value}")
        return values[value]
    return value
write_json(sys.argv[2], bind(template))
PY
}
lane_action() {
  assert "$ID" "$1@$2" "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" "$1" "$2")" "$JOURNEY_OBSERVATION"
}
lane_assert_checks() {
  local pairs pair result=0 source="${2:-$1}"
  pairs="$(jq -ce 'if type == "array" and length > 0 and all(.[];
      type == "object" and (.field | type == "string" and length > 0) and has("expected") and has("actual"))
      then .[] else error("LANE-CHECKS: expected nonempty check array") end' "$1")" || return 2
  while IFS= read -r pair; do
    assert "$ID" "$(jq -c '.expected' <<<"$pair")" "$(jq -c '.actual' <<<"$pair")" \
      "$source:$(jq -r '.field' <<<"$pair")" || result=1
  done <<<"$pairs"
  return "$result"
}
lane_gap_only() {
  PYTHONPATH="$HERE" python3 - "$JOURNEY_OUT/$JOURNEY_LANE.state.json" "$1" <<'PY'
import sys
from runtime import read_json
state = read_json(sys.argv[1])
failures = [row for row in state["assertions"] if not row["pass"]]
sys.exit(0 if failures and not state["errors"] and state["observation"]
         and all(row["source"] == sys.argv[2] for row in failures) else 1)
PY
}
lane_walk() {
  local callback="$1" row ids parent forecast factors slug available=1 rc roster
  lane_prepare "${2:-$JOURNEY_BRANCH}" || available=0
  roster="$JOURNEY_ROOT/$JOURNEY_LANE-rows.txt"
  lane_rows >"$roster" || return 2
  if [ ! -s "$roster" ]; then
    printf 'LANE-ROWS-EMPTY: %s@%s\n' "$JOURNEY_LANE" "$JOURNEY_BRANCH" >&2
    return 2
  fi
  while IFS='|' read -r row ids parent forecast factors; do
    slug="${row#*/}"; slug="${slug%%@*}"
    if beat "$slug" $ids; then
      ID="${ids%% *}" ARTIFACT="$JOURNEY_OUT/beats/$PFM_BEAT" GAP_SOURCE='' GAP_REASON=''
      if [ "$available" -eq 0 ]; then LANE_BLOCKED=1; blocked "need:$CHAT_BRANCH"; continue; fi
      "$callback" "$slug" "$parent" "$forecast" "$factors"; rc=$?
      case "$rc" in
        0) pass ;;
        3) LANE_BLOCKED=1; blocked "shared-owner-entry-or-fault:$slug${forecast:+:$forecast}" ;;
        *)
          if [ -n "$GAP_SOURCE" ] && lane_gap_only "$GAP_SOURCE"; then known "$GAP_REASON"
          else observe; fail "$slug failed (exit $rc)"; fi ;;
      esac
    fi
  done <"$roster"
}
lane_run() {
  local result=0
  fork os "$1" "$2" || result=1
  test "$result" -eq 0 && test "${LANE_BLOCKED:-0}" -eq 0 && test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"
}
