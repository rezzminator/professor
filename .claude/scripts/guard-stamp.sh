#!/usr/bin/env bash
set -euo pipefail

# Session-keyed guard-marker maintenance (pfm gate).
#   read (default) — PostToolUse(Read): stamps this session's quality marker when
#                    .claude/commands/quality/prompt.md is read, making the
#                    mandatory /quality:prompt load deterministic, not advisory.
#   stop           — Stop: reaps abandoned markers only. Turn-end clearing was
#                    removed: markers survive turn ends and die by TTL alone
#                    (pfm-guard's sliding 1500s freshness + the 1h reap here), so a
#                    live multi-turn session stamps once, not once per turn.
# Both modes reap abandoned markers (age > 1h) so tmp/ never accumulates stale keys.

MODE="${1:-read}"
INPUT=$(cat 2>/dev/null || true)
SID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)

reap() {
  local root="$1" now
  now=$(date +%s)
  for m in "$root"/tmp/professor_pfm_active* "$root"/tmp/professor_quality_loaded*; do
    [[ -f "$m" ]] || continue
    local age=$(( now - $(cat "$m" 2>/dev/null || echo 0) ))
    (( age > 3600 )) && rm -f "$m"
  done
  return 0
}

case "$MODE" in
  read)
    FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null || true)
    [[ -z "$FILE_PATH" ]] && exit 0
    case "$FILE_PATH" in
      */.claude/commands/quality/prompt.md) ;;
      *) exit 0 ;;
    esac
    # The quality law is machine-global — one file, reachable from every
    # install — so the READ file's own repo is not necessarily the repo being
    # edited. Anchoring only there stamps the framework repo while pfm-guard
    # reads the EDITED file's repo (its own comment explains why), and the
    # gate then can never open: the agent reads the law, is denied anyway, and
    # follows a message that cannot help it. Stamp the law file's repo AND
    # this session's own project root.
    ROOTS=()
    FILE_ROOT=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-toplevel 2>/dev/null) || FILE_ROOT=""
    if [[ -n "$FILE_ROOT" ]]; then
      ROOTS+=("$FILE_ROOT")
    fi
    HOOK_CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // empty' 2>/dev/null || true)
    if [[ -n "$HOOK_CWD" ]]; then
      CWD_ROOT=$(git -C "$HOOK_CWD" rev-parse --show-toplevel 2>/dev/null) || CWD_ROOT=""
      if [[ -n "$CWD_ROOT" && "$CWD_ROOT" != "$FILE_ROOT" ]]; then
        ROOTS+=("$CWD_ROOT")
      fi
    fi
    if [[ ${#ROOTS[@]} -eq 0 ]]; then
      exit 0
    fi
    for root in "${ROOTS[@]}"; do
      mkdir -p "$root/tmp"
      date +%s > "$root/tmp/professor_quality_loaded${SID:+.$SID}"
      reap "$root"
    done
    ;;
  stop)
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
    reap "$REPO_ROOT"
    ;;
esac
exit 0
