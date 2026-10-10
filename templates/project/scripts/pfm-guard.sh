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
# worktrees and regardless of where the harness spawns the hook. Resolve from the
# nearest existing ancestor: a Write may create its parent directories, and a
# missing parent must not read as "outside any repo". The target's own symlinks
# are followed too: an alias of a guarded file edits the guarded file, so the
# path as written and every hop of its link chain are each classified.
fail_closed() {
  jq -cn --arg r "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
  exit 2
}

# physical_path <path>: <path> with every directory symlink above its last component resolved.
physical_path() {
  local ancestor parent physical
  ancestor="$(dirname "$1")"
  while [[ ! -d "$ancestor" ]]; do
    parent="$(dirname "$ancestor")"
    [[ "$parent" != "$ancestor" ]] || break
    ancestor="$parent"
  done
  physical="$(cd "$ancestor" && pwd -P)" || return 1
  printf '%s' "$physical${1#"$ancestor"}"
}

# in_git_checkout <dir>: succeeds when a .git entry (file, directory or a link, dangling included) sits at or above <dir>.
in_git_checkout() {
  local dir="$1"
  while :; do
    [[ -e "$dir/.git" || -L "$dir/.git" ]] && return 0
    [[ "$dir" != / && "$dir" != . ]] || return 1
    dir="$(dirname "$dir")"
  done
}

# guarded_rel <path relative to the repo root>: succeeds when it is /pcm territory.
guarded_rel() {
  # Any spelling: on a case-insensitive filesystem (APFS's default) claude.md IS CLAUDE.md.
  local rc=0
  shopt -s nocasematch
  case "$1" in
    CLAUDE.md|.claude/*) ;;              # root infrastructure
    */CLAUDE.md|*/.claude/*) ;;          # child-project infrastructure (any nesting)
    *) rc=1 ;;
  esac
  shopt -u nocasematch
  return "$rc"
}

# classify <physical path>: sets REPO_ROOT and REL_PATH, succeeds when the path is /pcm territory.
# Outside any repository (~/.claude plans, memory, settings) is not /pcm territory. A Git error
# INSIDE a checkout leaves the root unknown, never "outside": the path is then judged against the
# guarded patterns at every possible root, and a match is denied outright (no gate can open
# without a repository to key it by).
classify() {
  local dir rel
  dir="$(dirname "$1")"
  while [[ ! -d "$dir" ]]; do
    dir="$(dirname "$dir")"
  done
  case "$dir/" in */.git/*) return 1 ;; esac
  if ! REPO_ROOT="$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)" || [[ -z "$REPO_ROOT" ]]; then
    in_git_checkout "$dir" || return 1
    rel="${1#/}"
    while :; do
      if guarded_rel "$rel"; then
        fail_closed "Git could not resolve the repository root of $1, a guarded path; guarded-path classification failed closed."
      fi
      [[ "$rel" == */* ]] || return 1
      rel="${rel#*/}"
    done
  fi
  REL_PATH="${1#"$REPO_ROOT"/}"
  guarded_rel "$REL_PATH"
}

if [[ "$FILE_PATH" != /* ]]; then
  INPUT_CWD="$(printf '%s' "$INPUT" | jq -r '.cwd // empty')"
  FILE_PATH="${INPUT_CWD:-$PWD}/$FILE_PATH"
fi
CANDIDATE="$(physical_path "$FILE_PATH")" || fail_closed "Cannot resolve the target ancestor; guarded-path classification failed closed."
GUARDED=0
HOPS=0
while :; do
  if classify "$CANDIDATE"; then GUARDED=1; break; fi
  [[ -L "$CANDIDATE" ]] || break
  (( ++HOPS <= 40 )) || fail_closed "Symlink chain of $FILE_PATH is too deep; guarded-path classification failed closed."
  LINK="$(readlink "$CANDIDATE")" || fail_closed "Cannot read the symlink $CANDIDATE; guarded-path classification failed closed."
  [[ "$LINK" == /* ]] || LINK="$(dirname "$CANDIDATE")/$LINK"
  CANDIDATE="$(physical_path "$LINK")" || fail_closed "Cannot resolve the symlink target $LINK; guarded-path classification failed closed."
done
[[ "$GUARDED" == 1 ]] || exit 0

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

# The law read is session knowledge, not the edited repo's: guard-stamp.sh
# stamps the law file's repo and the session's own project, never a repo the
# session edits later (a sibling checkout, a plugin repo). Without this the
# gate for another repo can never open: the agent reads the law, is denied
# anyway, and follows a message that cannot help it.
if ! fresh "$QUALITY"; then
  HOOK_CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // empty')
  CWD_ROOT=$([[ -n "$HOOK_CWD" ]] && git -C "$HOOK_CWD" rev-parse --show-toplevel 2>/dev/null) || CWD_ROOT=""
  if [[ -n "$CWD_ROOT" && "$CWD_ROOT" != "$REPO_ROOT" ]]; then
    CWD_PROJECT="$(basename "$CWD_ROOT")"
    QUALITY="/tmp/${CWD_PROJECT#.}/guard/quality_loaded${SID:+.$SID}"
  fi
fi
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
