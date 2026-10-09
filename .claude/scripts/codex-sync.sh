#!/usr/bin/env bash
set -uo pipefail

# Mirror auto-compile — a thin door to engine-sync, the single writer of the
# Codex and OpenCode mirrors compiled from the Claude sources
# (plugins/engine-sync/docs/interface.md § hook claude):
#   mark — PostToolUse(Edit|Write): `engine-sync hook claude mark` sets the
#          project's dirty flag when the edited file is a Claude source
#          (.claude/**, any CLAUDE.md, a .mcp.json, $HOME/.claude/commands/**).
#   sync — Stop: `engine-sync hook claude stop` runs codex build, codex check,
#          opencode build and opencode check when the flag is set; success
#          clears it silently. A failure blocks the stop ONCE naming each failed
#          stage with only its output, then lets the turn end with a
#          systemMessage warning and keeps the flag; it always exits 0.
# The binary: $ENGINE_SYNC_BIN when it names an executable regular file, else
# {engine-sync home}/bin/engine-sync ({engine-sync home}: $ENGINE_SYNC_HOME when
# absolute, else $XDG_DATA_HOME/engine-sync when absolute, else
# $HOME/.local/share/engine-sync). Neither: mark exits 0, and sync lets the turn
# end with a systemMessage naming the binary it could not find — never exit 1
# (a red stop-hook error the model never sees).
# Coverage (declared): sees Edit/Write TOOL calls only. A Bash-driven write (sed,
# redirect) to a Claude source does NOT set the flag — `engine-sync check` in the
# pfm `structure` audit scope remains the backstop for that shape.

case "${1:-mark}" in
  mark) STAGE=mark ;;
  sync) STAGE=stop ;;
  *) exit 0 ;;
esac
if [[ -n "${ENGINE_SYNC_BIN:-}" && -f "$ENGINE_SYNC_BIN" && -x "$ENGINE_SYNC_BIN" ]]; then
  BIN="$ENGINE_SYNC_BIN"
else
  ES_HOME="${ENGINE_SYNC_HOME:-}"
  if [[ "$ES_HOME" != /* ]]; then
    if [[ "${XDG_DATA_HOME:-}" == /* ]]; then ES_HOME="$XDG_DATA_HOME/engine-sync"; else ES_HOME="$HOME/.local/share/engine-sync"; fi
  fi
  BIN="$ES_HOME/bin/engine-sync"
fi
if [[ ! -x "$BIN" ]]; then
  if [[ "$STAGE" == stop ]]; then
    if [[ -e "$BIN" ]]; then CAUSE="$BIN: not executable"; else CAUSE="$BIN: no such file"; fi
    jq -n --arg m "engine-sync WARNING: binary unavailable — mirrors were not checked; dirty flag retained ($CAUSE)" '{systemMessage: $m}'
  fi
  exit 0
fi
exec "$BIN" hook claude "$STAGE"
