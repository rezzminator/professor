#!/usr/bin/env bash
set -uo pipefail

# Mirror auto-compile — a thin door to engine-sync, the single writer of the
# Codex and OpenCode mirrors compiled from the Claude sources:
#   mark — PostToolUse(Edit|Write): `engine-sync hook claude mark` sets the
#          project's dirty flag when the edited file is a Claude source
#          (.claude/**, any CLAUDE.md, a .mcp.json, $HOME/.claude/commands/**).
#   sync — Stop: `engine-sync hook claude stop` runs codex build, codex check,
#          opencode build and opencode check when the flag is set; success
#          clears it silently. A failure blocks the stop ONCE naming each failed
#          stage with only its output, then lets the turn end with a
#          systemMessage warning and keeps the flag; it always exits 0.
# The binary: $ENGINE_SYNC_BIN when set (one that is not an executable regular
# file is refused by name, never replaced by the default), else
# {engine-sync home}/bin/engine-sync ({engine-sync home}: $ENGINE_SYNC_HOME when
# absolute, else $XDG_DATA_HOME/engine-sync when absolute, else
# $HOME/.local/share/engine-sync). Without a usable binary mark exits 0 and
# records nothing, and sync lets the turn end — never exit 1 (a red stop-hook
# error the model never sees). It warns through a systemMessage when an edit is
# pending, read from engine-sync's dirty flag: the regular file
# {state}/dirty/{project directory name without one leading dot}-{first 16 hex of
# the sha256 of the project's physical root path} ({state}: $ENGINE_SYNC_STATE
# when absolute, else $XDG_STATE_HOME/engine-sync when absolute, else
# $HOME/.local/state/engine-sync; the project: $CLAUDE_PROJECT_DIR, else the
# nearest ancestor of the working directory holding .git or a CLAUDE.md). No flag
# is silence; "dirty flag retained" is claimed only for a flag that exists; a
# flag that cannot be looked up says so.
# Coverage (declared): sees Edit/Write TOOL calls only. A Bash-driven write (sed,
# redirect) to a Claude source does NOT set the flag, nor does an edit made while
# no binary exists — `engine-sync check` in the pfm `structure` audit scope
# remains the backstop for those shapes.

# edit_pending: 0 when engine-sync's dirty flag for this project is set, 1 when
# it is not, 2 when that cannot be told (no project root, no sha256 tool, no state
# directory, a flag lookup that failed for any cause but a missing path).
edit_pending() {
  local root name state digest flag look
  if [[ -n "${CLAUDE_PROJECT_DIR:-}" ]]; then
    root="$(cd -P -- "$CLAUDE_PROJECT_DIR" 2> /dev/null && pwd -P)" || return 2
  else
    root="$(cd -P -- . 2> /dev/null && pwd -P)" || return 2
    while [[ ! -e "$root/.git" && ! -f "$root/CLAUDE.md" ]]; do
      [[ -n "$root" && "$root" != / ]] || return 2
      root="${root%/*}"
      [[ -n "$root" ]] || root=/
    done
  fi
  name="${root##*/}"
  name="${name#.}"
  if [[ "${ENGINE_SYNC_STATE:-}" == /* ]]; then
    state="$ENGINE_SYNC_STATE"
  elif [[ "${XDG_STATE_HOME:-}" == /* ]]; then
    state="$XDG_STATE_HOME/engine-sync"
  elif [[ -n "${HOME:-}" ]]; then
    state="$HOME/.local/state/engine-sync"
  else
    return 2
  fi
  if command -v sha256sum > /dev/null 2>&1; then
    digest="$(printf '%s' "$root" | sha256sum)" || return 2
  elif command -v shasum > /dev/null 2>&1; then
    digest="$(printf '%s' "$root" | shasum -a 256)" || return 2
  else
    return 2
  fi
  digest="${digest:0:16}"
  [[ ${#digest} -eq 16 ]] || return 2
  flag="$state/dirty/$name-$digest"
  # Only a missing flag (ENOENT, ENOTDIR) means no edit is pending; any other lookup failure
  # (permission, a symlink loop) or a flag that is not a regular file cannot tell, and says so.
  if [[ -f "$flag" ]]; then
    [[ -L "$flag" ]] && return 1
    return 0
  fi
  [[ -L "$flag" ]] && return 1
  [[ -e "$flag" ]] && return 2
  look="$(LC_ALL=C; { : < "$flag"; } 2>&1)" && return 2
  case "$look" in
    *'No such file or directory'* | *'Not a directory'*) return 1 ;;
    *) return 2 ;;
  esac
}

case "${1:-mark}" in
  mark) STAGE=mark ;;
  sync) STAGE=stop ;;
  *) exit 0 ;;
esac
if [[ -n "${ENGINE_SYNC_BIN:-}" ]]; then
  BIN="$ENGINE_SYNC_BIN"
  if [[ ! -f "$BIN" || ! -x "$BIN" ]]; then
    if [[ "$STAGE" == stop ]]; then
      NOTE=""
      edit_pending && NOTE="; dirty flag retained"
      jq -n --arg m "engine-sync WARNING: ENGINE_SYNC_BIN=$BIN is not an executable file — mirrors were not checked$NOTE" '{systemMessage: $m}'
    fi
    exit 0
  fi
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
    edit_pending
    case $? in
      0) NOTE="dirty flag retained ($CAUSE)" ;;
      1) exit 0 ;;
      *) NOTE="whether an edit is pending could not be determined ($CAUSE)" ;;
    esac
    jq -n --arg m "engine-sync WARNING: binary unavailable — mirrors were not checked; $NOTE" '{systemMessage: $m}'
  fi
  exit 0
fi
exec "$BIN" hook claude "$STAGE"
