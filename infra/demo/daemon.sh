#!/usr/bin/env bash
# daemon.sh — runs INSIDE the demo fence container: keeps the pfm MCP HTTP daemon
# up. On a real host `pfm install` wires it as a systemd/launchd unit; the fence
# has no init system, so Codex chats (whose MCP registration is the loopback
# URL, not stdio) would start with "1 MCP startup issue" and no chat_* tools.
# Claude chats use the stdio registration and never need this.
#
# Idempotent: a daemon already answering on the port is left alone. Called by
# setup.sh install and by every spawning script before its first spawn.
#
# BROKEN STATE: exits 1 with the daemon's log tail when the port never answers.
set -euo pipefail
export PATH="$HOME/.local/bin:$PATH"
PORT="$(jq -r '.mcp.port // 18377' "$HOME/.config/pfm/pfm.config.json" 2>/dev/null || echo 18377)"
LOG=/tmp/pfm-mcp.log
up() { [ "$(curl -s -o /dev/null -w '%{http_code}' -m 2 "http://127.0.0.1:$PORT/mcp/chat" || true)" != 000 ]; }
if up; then echo "daemon: pfm mcp serve already answering on :$PORT"; exit 0; fi
nohup pfm mcp serve >"$LOG" 2>&1 < /dev/null &
disown
for _ in $(seq 1 20); do up && { echo "daemon: pfm mcp serve up on :$PORT (pid $!, log $LOG)"; exit 0; }; sleep 0.5; done
echo "daemon: pfm mcp serve never answered on :$PORT — log tail:" >&2
tail -20 "$LOG" >&2
exit 1
