#!/usr/bin/env bash
# Exercise the historical tool-shell birth path in the same Codex producer context.
_mesh_tool_child() {
  local name="tool-scout-${BRANCH#*+}" receipt="$PREFIX.tool-child" child="$PREFIX.tool-child.json" sid colour=native
  name="${name//+/-}"
  if [ "$(jq -r '.engine' "$CALLER")" != codex ]; then colour=user-colour; fi
  mesh_shell "$CALLER" "$receipt" bash "$FIXTURES/tool-child.sh" "$HERE" "$receipt" "$name" "$colour" || return 2
  poll tool-child-cli-receipt 30 mesh_command_ready || return 2
  mesh_input_finish "$receipt" || return 2
  assert "$ID" 0 "$(cat "$receipt.status")" "$receipt.status"
  assert "$ID" "$PFM_RUN" "$(jq -r '.environment.PFM_RUN' "$receipt.tool-environment.json")" "$receipt.tool-environment.json"
  poll tool-child-registered 20 mesh_registered_status "$name" "$receipt.registered.json" || return 2
  sid="$(jq -er '.session_id' "$receipt.registered.json")" || return 2
  python3 "$HERE/chat.py" adopt-cli-child "$CALLER" "$sid" "$receipt" tool-shell-scout >"$child" || return 2
  jq '.terminal' "$child" >"$child.target"
  assert "$ID" "$name" "$(jq -r '.name' "$child")" "$child"
  assert "$ID" claude "$(jq -r '.engine' "$child")" "$child"
  assert "$ID" tmux "$(jq -r '.terminal.backend' "$child")" "$child"
  assert "$ID" 3 "$(jq -r '.account' "$child")" "$child"
  assert "$ID" "$(jq -r '.session_id' "$CALLER")" "$(jq -r '.parent' "$child")" "$child"
  poll tool-child-idle 20 mesh_state "$child" idle || return 2
  python3 "$FIXTURES/engine-birth.py" "$HERE" "$child" "$receipt.birth.json" || return 2
  python3 "$FIXTURES/tool_environment.py" checks "$CALLER" "$receipt.tool-environment.json" "$receipt.birth.json" "$child" "$receipt.environment-checks.json" || return 2
  mesh_checks "$ID" "$receipt.environment-checks.json"
  assert "$ID" "$PFM_RUN" "$(jq -r '.environment.PFM_RUN' "$receipt.birth.json")" "$receipt.birth.json"
  assert "$ID" "$HOME/.cc/3" "$(jq -r '.environment.CLAUDE_CONFIG_DIR' "$receipt.birth.json")" "$receipt.birth.json"
  poll tool-caller-idle 15 mesh_state "$CALLER" idle
}
