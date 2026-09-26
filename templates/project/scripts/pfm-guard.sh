#!/usr/bin/env bash
set -euo pipefail

# PreToolUse(Edit|Write) — guards /pcm territory.
# Edits to .claude/** and any CLAUDE.md (root or child) are /pcm-exclusive: these
# files ARE the framework — agent graph, routing, hooks, conventions — loaded by
# the harness at runtime, so a careless edit breaks the pipeline. Allowed only
# when BOTH of THIS session's markers are fresh (session-keyed — concurrent
# sessions on one repo never share or clear each other's gate):
#   /tmp/<project>/guard/pfm_active.<sid>      — /pcm is active (stamped per the deny message)
#   /tmp/<project>/guard/quality_loaded.<sid>  — quality/prompt.md was READ this session
#                                                (stamped automatically by guard-stamp.sh)
# <project> is the repo directory's basename with any leading dot stripped, so
# gate state lives outside the working tree and never dirties a checkout.
# Sliding expiry: every ALLOWED edit re-touches both markers, so an active session
# never expires mid-batch; the TTL reaps only abandoned sessions (guard-stamp.sh's
# Stop pass reaps >1h leftovers; markers SURVIVE turn ends — stamp once per session).
# Silent no-op for every other path.

INPUT=$(cat)
FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty')
[[ -z "$FILE_PATH" ]] && exit 0

# Anchor to the EDITED FILE's repo root, not the hook's cwd — correct inside
# worktrees and regardless of where the harness spawns the hook.
REPO_ROOT=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-toplevel 2>/dev/null) || exit 0
REL_PATH="${FILE_PATH#"$REPO_ROOT"/}"

case "$REL_PATH" in
  CLAUDE.md|.claude/*) ;;              # root infrastructure
  */CLAUDE.md|*/.claude/*) ;;          # child-project infrastructure (any nesting)
  *) exit 0 ;;
esac

SID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty')
PROJECT="$(basename "$REPO_ROOT")"; PROJECT="${PROJECT#.}"
GUARD_DIR="/tmp/$PROJECT/guard"
ACTIVE="$GUARD_DIR/pfm_active${SID:+.$SID}"
QUALITY="$GUARD_DIR/quality_loaded${SID:+.$SID}"
TTL=1500
NOW=$(date +%s)

fresh() {
  [[ -f "$1" ]] || return 1
  local age=$(( NOW - $(cat "$1" 2>/dev/null || echo 0) ))
  (( age >= 0 && age < TTL ))
}

if fresh "$ACTIVE" && fresh "$QUALITY"; then
  # Sliding expiry — an active session never times out mid-batch.
  printf '%s\n' "$NOW" > "$ACTIVE"
  printf '%s\n' "$NOW" > "$QUALITY"
  exit 0
fi

REASON="This file is framework INFRASTRUCTURE — a .claude/ prompt or a CLAUDE.md that the harness loads at runtime; a careless edit ships straight into the agent pipeline. You ARE authorized as the infra owner via /pcm."
if ! fresh "$QUALITY"; then
  REASON+=" DENIED — prompt-file edits require /quality:prompt loaded this session: Read ~/.claude/commands/quality/prompt.md (the machine-global law; the Read auto-stamps your session), then retry."
fi
if ! fresh "$ACTIVE"; then
  REASON+=" DENIED — infra edits route through /pcm: open this session's gate: mkdir -p \"$GUARD_DIR\" && date +%s > \"$ACTIVE\" — run the stamp UNSANDBOXED (a sandboxed write never lands on the filesystem this hook reads; a denied retry after stamping means the stamp ran sandboxed), then retry."
fi
REASON+=" Markers slide on every allowed edit and expire by TTL only — no turn-end clearing; stamp once per session. Do NOT route around this by disabling the hook or editing infra outside /pcm."

jq -cn --arg r "$REASON" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
exit 2
