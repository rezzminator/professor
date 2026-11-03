#!/usr/bin/env bash
# Lane composition over the public terminal/chat doors; no transport lives here.
mesh_select() {
  python3 "$HERE/chat.py" select "$1" "$2" >"$3" || return "$?"
  jq '.terminal' "$3" >"$3.target"
}

mesh_ensure() {
  python3 "$HERE/chat.py" ensure "$1" "$2" "$HERE/fixtures/chat-mesh/seed.json" >"$3" || return "$?"
  jq '.terminal' "$3" >"$3.target"
}

mesh_selector() {
  jq -er '.key|select(type=="string" and length>0)|"key:"+.' "$1" && return 0
  printf 'MESH-ACTOR-KEY: expected a non-empty stable key: %s\n' "$1" >&2
  return 2
}
mesh_subject() { python3 "$HERE/fixtures/fleet-reader/projection.py" selected "$1" "$2" >"$3"; }

mesh_bundle_actors() {
  local id="$1" before="$2" argument path modifier selector
  local -a actors=()
  shift 2
  for argument in "$@"; do
    path="$argument" modifier=''
    case "$argument" in replaced:*|moved:*) modifier="${argument%%:*}:"; path="${argument#*:}" ;; esac
    selector="$(mesh_selector "$path")" || return 2
    actors+=("$modifier$selector")
  done
  mesh_bundle "$id" "$before" "${actors[@]}"
}

mesh_registered_status() {
  # This is a public lookup handle; adopt-child owns native readiness and the final SID.
  local result
  pfm chat status "$1" --json >"$2"; result=$?
  case "$result" in 0|3) ;; 4) return 1 ;; *) return 2 ;; esac
  jq -e '.session_id|type=="string" and length>0' "$2" >/dev/null
}

mesh_adopt_new() {
  local caller="$1" name="$2" result="$3" prefix="$4" role="$5" sid
  poll child-session-registered 20 mesh_registered_status "$name" "$prefix.status.json" || return 2
  sid="$(jq -er '.session_id' "$prefix.status.json")" || return 2
  python3 "$HERE/chat.py" adopt-child "$caller" "$sid" "$result" "$role" >"$prefix.child.json" || return 2
  jq '.terminal' "$prefix.child.json" >"$prefix.child.json.target"
}

mesh_checks() {
  local id="$1" path="$2" row
  while IFS= read -r row; do
    assert "$id" "$(jq -c '.expected' <<<"$row")" "$(jq -c '.actual' <<<"$row")" "$path:$(jq -r '.field' <<<"$row")"
  done < <(jq -c '.[]' "$path")
}

mesh_state() {
  local actual result
  actual="$(pfm chat status "$(jq -er '.session_id' "$1")" --json)"; result=$?
  case "$result" in 0|3) ;; *) return 2 ;; esac
  [ "$(jq -r '.state' <<<"$actual")" = "$2" ]
}

mesh_picker_target() {
  python3 - "$HERE" "$JOURNEY_CARRY" "$1" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from runtime import read_json, write_json
carry = read_json(sys.argv[2])
carry["picker"] = read_json(sys.argv[3])
write_json(sys.argv[2], carry)
PY
}

mesh_receipt_ready() { [ -f "$MESH_RECORD" ] && [ "$(wc -l <"$MESH_RECORD")" -gt "$MESH_OFFSET" ]; }

mesh_input_start() {
  MESH_PROMPTS="$(jq -er '.records' "$1")/prompts" MESH_INPUT_OFFSET=0
  if [ -f "$MESH_PROMPTS" ]; then MESH_INPUT_OFFSET="$(wc -c <"$MESH_PROMPTS")"; fi
  MESH_INPUT_EXPECTED="$3 mock-engine: $(jq -c . "$2")"
  case "$MESH_INPUT_EXPECTED" in *$'\n'*) printf 'MESH-INPUT: multiline caller input refused\n' >&2; return 2 ;; esac
}

mesh_input_finish() {
  local prefix="$1"
  mesh_input_tail "$MESH_PROMPTS" "$MESH_INPUT_OFFSET" "$prefix.input-tail.txt" || return 2
  if [ "${MESH_INPUT_FIRST_FRAME:-}" = keys-child ]; then
    python3 "$HERE/fixtures/chat-mesh/caller_frame.py" "$prefix.input-tail.txt" "$prefix.input.txt" || return 2
  else
    cp "$prefix.input-tail.txt" "$prefix.input.txt" || return 2
  fi
  assert "$ID" "$MESH_INPUT_EXPECTED" "$(cat "$prefix.input.txt")" "engine-input:$prefix.input.txt"
}

mesh_input_tail() { tail -c "+$(($2 + 1))" "$1" >"$3"; }

mesh_mcp_start() {
  local chat="$1" tool="$2" input="$3" prefix="$4" records
  records="$(jq -er '.records' "$chat")" || return 2
  MESH_RECORD="$records/mcp-call-$tool.json" MESH_OFFSET=0
  if [ -f "$MESH_RECORD" ]; then MESH_OFFSET="$(wc -l <"$MESH_RECORD")"; fi
  jq -nc --arg tool "$tool" --argjson input "$input" \
    '[{type:"mcp",tool:$tool,input:$input},{type:"turn",reply:"mesh-owner-finished",busy_ms:200}]' >"$prefix.steps.json" || return 2
  mesh_input_start "$chat" "$prefix.steps.json" "mesh-$tool" || return 2
  python3 "$HERE/chat.py" inline "$chat.target" "$prefix.steps.json" "mesh-$tool"
}

mesh_mcp_finish() {
  local chat="$1" prefix="$2" bound="${3:-30}" result
  poll engine-mcp-receipt "$bound" mesh_receipt_ready; result=$?
  [ "$result" -eq 0 ] || return "$result"
  tail -n 1 "$MESH_RECORD" >"$prefix.receipt.json" || return 2
  python3 "$HERE/fixtures/fleet-reader/projection.py" mcp "$prefix.receipt.json" >"$prefix.result.json" || return 2
  poll caller-idle-after-mcp 15 mesh_state "$chat" idle || return 2
  mesh_input_finish "$prefix" || return 2
}

mesh_mcp() {
  mesh_mcp_start "$@" && mesh_mcp_finish "$1" "$4"
}

mesh_command_ready() { test -s "$MESH_COMMAND.status"; }

mesh_command() {
  local target="$1"
  MESH_COMMAND="$2"
  shift 2
  python3 "$HERE/terminal.py" receipt "$target" "$MESH_COMMAND" "$@" &&
    poll owner-command-receipt 30 mesh_command_ready
}

mesh_shell() {
  local chat="$1" prefix="$2" command tool=Bash input_key=command
  shift 2
  command="$(python3 - "$HERE" "$@" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from terminal import cli_command
print(cli_command(sys.argv[2:]))
PY
)" || return 2
  printf -v command '%s >%q 2>&1; printf "%%s\\n" "$?" >%q' "$command" "$prefix.out" "$prefix.status"
  case "$(jq -r '.engine' "$chat")" in codex) tool=exec_command; input_key=cmd ;; opencode) tool=bash ;; esac
  jq -nc --arg command "$command" --arg tool "$tool" --arg key "$input_key" '[{type:"tool_call",tool:$tool,input:{($key):$command}},{type:"turn",reply:"shell-finished",busy_ms:200}]' >"$prefix.steps.json" || return 2
  mesh_input_start "$chat" "$prefix.steps.json" engine-shell || return 2
  python3 "$HERE/chat.py" inline "$chat.target" "$prefix.steps.json" engine-shell || return 2
  MESH_COMMAND="$prefix"
  # The caller chooses whether to wait, so the receiver's working phase is observable.
  return 0
}

mesh_bundle() {
  local id="$1" before="$2"
  shift 2
  observe || return 2
  python3 "$HERE/fixtures/fleet-reader/projection.py" bundle "$JOURNEY_OBSERVATION" "$JOURNEY_CARRY" "$before" "$@" \
    >"$JOURNEY_OUT/beats/$PFM_BEAT/bundle-checks.json" || return 2
  mesh_checks "$id" "$JOURNEY_OUT/beats/$PFM_BEAT/bundle-checks.json"
}

mesh_action() {
  assert "$1" "$2@$3" "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" "$2" "$3")" "$JOURNEY_OBSERVATION"
}
