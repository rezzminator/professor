#!/usr/bin/env bash
# Both the observation and birth inherit this same actual Codex tool shell.
set -euo pipefail
HERE="$1" PREFIX="$2" NAME="$3"
if [ "${4:-}" = user-colour ]; then export NO_COLOR=journey-user-colour; fi
python3 "$HERE/fixtures/chat-mesh/tool_environment.py" capture "$PREFIX.tool-environment.json"
exec pfm chat new --backend tmux --engine claude --account 3 --name "$NAME" --cwd "$HOME/work/atlas"
