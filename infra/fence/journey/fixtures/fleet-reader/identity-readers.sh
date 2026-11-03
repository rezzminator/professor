#!/usr/bin/env bash
# The same native caller supplies the CLI corroboration and its own MCP identity.
_reader_unnamed() {
  export JOURNEY_TARGET="$CHAT.target"
  _reader_before || return 2
  python3 - "$FIXTURES" "$CHAT" "$PREFIX.cli" "$ID" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from native_forecasts import engine_action
engine_action(sys.argv[2], ["pfm", "whoami", "--json"], sys.argv[3], sys.argv[4])
PY
  [ "$?" -eq 0 ] || return 2
  assert "$ID" 0 "$(cat "$PREFIX.cli.status")" "$PREFIX.cli.status"
  assert "$ID" "$(cat "$PREFIX.cli.expected-input.txt")" "$(cat "$PREFIX.cli.input.txt")" "engine-input:$PREFIX.cli.input.txt"
  mesh_mcp "$CHAT" whoami '{}' "$PREFIX.mcp" || return 2
  assert "$ID" "$MESH_INPUT_EXPECTED" "$(cat "$PREFIX.mcp.input.txt")" "engine-input:$PREFIX.mcp.input.txt"
  _reader_finish || return 2
  python3 "$FIXTURES/native_identity.py" "$CHAT" "$PREFIX.before.json" "$JOURNEY_OBSERVATION" "$PREFIX.cli.out" "$PREFIX.mcp.result.json" "$PREFIX.identity-checks.json" || return 2
  mesh_checks "$ID" "$PREFIX.identity-checks.json"
  pass
}
