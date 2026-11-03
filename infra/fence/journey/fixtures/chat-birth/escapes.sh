#!/usr/bin/env bash
_birth_escape() {
  case "$1:$JOURNEY_BRANCH:$SLUG" in
    alias:linux+herdr-shield+project+codex:new-from-cli|title:linux+herdr-shield+project+codex:new-from-cli|first-paint:linux+herdr-shield+project+opencode:new-from-cli|permission:linux+herdr-shield+project+claude:new-from-cli|agentless:linux+herdr-shield+project+claude:rename-from-cli) return 0 ;;
    *) return 1 ;;
  esac
}
_birth_escape_compare() {
  assert "$1" "$(jq -cS '.expected' "$2")" "$(jq -cS '.projection' "$2")" "$2"
}
_birth_alias() {
  local directory="$JOURNEY_OUT/beats/$PFM_BEAT"
  python3 "$FIXTURE/escapes.py" unknown-alias "$directory" "$SHELL_TARGET" "atlas-birth-$INDEX" >"$directory/alias-projection.json" || return 2
  _birth_escape_compare "$1" "$directory/alias-evidence.json"
}
_birth_title() {
  local directory="$JOURNEY_OUT/beats/$PFM_BEAT"
  python3 "$FIXTURE/escapes.py" codex-title "$directory" "$CHAT" "atlas-birth-$INDEX" >"$directory/title-projection.json" || return 2
  _birth_escape_compare "$1" "$directory/codex-title-evidence.json"
}
_birth_first_paint() {
  local observed="$JOURNEY_ROOT/birth-settled-chat.json"
  assert "$1" atlas-first-paint "$(jq -r '.name' "$CHAT")" "$CHAT"
  assert "$1" 'OC | atlas-first-paint' "$(jq -r '.terminal.pane.terminal_title_stripped' "$observed")" "$observed"
  assert "$1" atlas-first-paint "$(jq -r '.terminal.tab.tab.label' "$observed")" "$observed"
}
_birth_agentless() {
  local directory="$JOURNEY_OUT/beats/$PFM_BEAT"
  python3 "$FIXTURE/escapes.py" agentless-rename "$directory" "$CHAT" "$OWNER" >"$directory/agentless-projection.json" || return 2
  assert "$1" '{"agent_get_refusal":"agent_not_found","name":"atlas-1","exit_status":"0","birth_preserved":true}' \
    "$(jq -c '{agent_get_refusal,name,exit_status,birth_preserved}' "$directory/agentless-projection.json")" "$directory/agentless-evidence.json"
}
