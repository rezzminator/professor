#!/usr/bin/env bash
set -uo pipefail

# PostToolUse hook — formats the one Professor-owned .md file just written, under
# the repo-root `.rumdl.toml` policy (see /quality:md-forlint). Receives hook JSON
# on stdin.
#
# What rumdl cannot fix goes back only when the write introduced it against the
# committed file on the first hook, then the previous hook in this session:
# match rule + message counts, ignoring shifted positions. Exit 2 with,
# on stderr, one header line, one `UNFIXED <file>:<line>:<col> <rule> <message>`
# line per issue, and the re-check command (Claude Code shows a PostToolUse exit-2
# stderr to the model; the write itself stands). Clean or standing-only issues
# exit 0, silent.
#
# What this reports when IT is broken: a failing `rumdl fmt` or `rumdl check`
# exits 2 naming the step; a missing `jq` or `rumdl` prints one stderr line and
# exits 0 (a tool to install, not an issue in the file) — never a silent skip
# that looks like a clean format.

INPUT=$(cat)

if ! command -v jq >/dev/null 2>&1; then
  echo "format-md: jq not found — markdown left unformatted" >&2
  exit 0
fi

FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty')

[[ -z "$FILE_PATH" ]] && exit 0
[[ "$FILE_PATH" != *.md ]] && exit 0
[[ ! -f "$FILE_PATH" ]] && exit 0

FILE_DIR=$(cd "$(dirname "$FILE_PATH")" && pwd -P) || exit 0
FILE_PATH="$FILE_DIR/${FILE_PATH##*/}"
LINK_HOPS=0
while [[ -L "$FILE_PATH" ]]; do
  LINK_HOPS=$((LINK_HOPS + 1))
  if ((LINK_HOPS > 40)) || ! LINK_TARGET=$(readlink "$FILE_PATH"); then
    echo "format-md: FAILED resolving file alias ${FILE_PATH}" >&2; exit 2
  fi
  case "$LINK_TARGET" in /*) FILE_PATH="$LINK_TARGET" ;; *) FILE_PATH="$FILE_DIR/$LINK_TARGET" ;; esac
  if ! FILE_DIR=$(cd "$(dirname "$FILE_PATH")" && pwd -P); then
    echo "format-md: FAILED resolving file alias ${FILE_PATH}" >&2; exit 2
  fi
  FILE_PATH="$FILE_DIR/${FILE_PATH##*/}"
done
REPO_ROOT=$(git -C "$FILE_DIR" rev-parse --show-toplevel 2>/dev/null) || exit 0
REL_PATH="${FILE_PATH#"$REPO_ROOT"/}"

# Only format Professor-owned files — not user source code. Generated mirrors
# (AGENTS.md, .codex/) are rebuilt by their compiler, never formatted here.
case "$REL_PATH" in
  CLAUDE.md) ;;
  .claude/*.md) ;;
  docs/commands/*.md) ;;
  docs/agents/*.md) ;;
  docs/references/*.md) ;;
  docs/features/*.md) ;;
  docs/runbooks/*.md) ;;
  docs/facts/*.md) ;;
  docs/epics/*.md) ;;
  docs/dev/*.md) ;;
  docs/business/*.md) ;;
  */CLAUDE.md|*/.claude/*.md) ;;
  *) exit 0 ;;
esac

if ! command -v rumdl >/dev/null 2>&1; then
  echo "format-md: rumdl not found — ${REL_PATH} left unformatted (\`pfm install\` provisions it)" >&2
  exit 0
fi

# rumdl resolves `[per-file-ignores]` globs against the CURRENT DIRECTORY, not
# against the config's own location: run it from the repo root or the whole
# category policy silently fails to match.
BASELINE_KEYS=()
BASE_HEAD=$(git -C "$REPO_ROOT" rev-parse --verify --quiet HEAD 2>&1)
GIT_RC=$?
if ((GIT_RC != 0 && GIT_RC != 1)); then
  echo "format-md: FAILED rumdl check of the committed ${REL_PATH} (git rev-parse exit ${GIT_RC}) — ${BASE_HEAD}" >&2
  exit 2
fi
if ((GIT_RC == 0)); then
  COMMITTED_PATH=$(git -C "$REPO_ROOT" ls-tree --name-only "$BASE_HEAD" -- "$REL_PATH" 2>&1)
  GIT_RC=$?
  if ((GIT_RC != 0)); then
    echo "format-md: FAILED rumdl check of the committed ${REL_PATH} (git ls-tree exit ${GIT_RC}) — ${COMMITTED_PATH}" >&2
    exit 2
  fi
  if [[ -n "$COMMITTED_PATH" ]]; then
    # The sentinel keeps command substitution from stripping the file's final newlines.
    COMMITTED=$(git -C "$REPO_ROOT" show "HEAD:$REL_PATH" 2>&1 && printf '.')
    GIT_RC=$?
    if ((GIT_RC != 0)); then
      echo "format-md: FAILED rumdl check of the committed ${REL_PATH} (git show exit ${GIT_RC}) — ${COMMITTED}" >&2
      exit 2
    fi
    BASELINE=$(cd "$REPO_ROOT" && printf '%s' "${COMMITTED%.}" | rumdl check --stdin --stdin-filename "$REL_PATH" --output-format concise 2>&1)
    BASELINE_RC=$?
    if ((BASELINE_RC != 0 && BASELINE_RC != 1)); then
      echo "format-md: FAILED rumdl check of the committed ${REL_PATH} (exit ${BASELINE_RC}) — ${BASELINE}" >&2
      exit 2
    fi
    while IFS= read -r line; do
      [[ $line == "$REL_PATH:"*": ["*"] "* ]] || continue
      rest=${line#*: \[}
      BASELINE_KEYS+=("${rest%%]*} ${rest#*] }")
    done <<< "$BASELINE"
  fi
fi

# Hook session_id is supplied by Claude Code. Keep the last observed diagnostics
# outside the checkout; separate repositories and sessions have separate snapshots.
STATE_FILE=""
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty')
if [[ -n "$SESSION_ID" ]]; then
  if ! STATE_KEY=$(printf '%s\n' "$REPO_ROOT" "$SESSION_ID" "$REL_PATH" | git -C "$REPO_ROOT" hash-object --stdin); then
    echo "format-md: FAILED diagnostic snapshot key for ${REL_PATH}" >&2; exit 2
  fi
  PROJECT_DIR="${REPO_ROOT##*/}"
  STATE_DIR="${TMPDIR:-/tmp}/${PROJECT_DIR#.}/format-md"
  if ! mkdir -p "$STATE_DIR"; then
    echo "format-md: FAILED diagnostic snapshot directory for ${REL_PATH}" >&2; exit 2
  fi
  STATE_FILE="$STATE_DIR/$STATE_KEY"
  STATE_LOCK="$STATE_FILE.lock"
  if ! mkdir "$STATE_LOCK" 2>/dev/null; then
    echo "format-md: diagnostic snapshot busy or lock unavailable for ${REL_PATH} at ${STATE_LOCK} — retry after the owner exits; inspect an abandoned lock before removing it" >&2
    exit 2
  fi
  # Ownership spans read, format, compare and save/cleanup. Never steal a lock.
  release_snapshot() {
    local rc=$?
    if ! rmdir "$STATE_LOCK"; then
      echo "format-md: FAILED releasing diagnostic snapshot lock for ${REL_PATH}" >&2
      rc=2
    fi
    exit "$rc"
  }
  trap release_snapshot EXIT
  trap 'exit 2' HUP INT TERM
  if [[ -f "$STATE_FILE" ]]; then
    BASELINE_KEYS=()
    if ! { while IFS= read -r key; do BASELINE_KEYS+=("$key"); done < "$STATE_FILE"; }; then
      echo "format-md: FAILED reading diagnostic snapshot for ${REL_PATH}" >&2; exit 2
    fi
  fi
fi

if ! (cd "$REPO_ROOT" && rumdl fmt "$REL_PATH" >/dev/null 2>&1); then
  echo "format-md: FAILED rumdl fmt on ${REL_PATH} — the file is unformatted; run \`rumdl fmt ${REL_PATH}\` from ${REPO_ROOT} to see why" >&2
  exit 2
fi

# After the format, `rumdl check` lists only what is left: exit 0 clean, 1 issues
# found, anything else a failed check.
LEFT=$(cd "$REPO_ROOT" && rumdl check --output-format concise "$REL_PATH" 2>&1)
CHECK_RC=$?
if ((CHECK_RC == 0)); then
  if [[ -n "$STATE_FILE" ]] && ! rm -f "$STATE_FILE"; then
    echo "format-md: FAILED clearing diagnostic snapshot for ${REL_PATH}" >&2; exit 2
  fi
  exit 0
fi
if ((CHECK_RC != 1)); then
  echo "format-md: FAILED rumdl check on ${REL_PATH} (exit ${CHECK_RC}) — unfixed issues could not be listed: ${LEFT}" >&2
  exit 2
fi

ISSUES=()
CURRENT_KEYS=()
PARSED=0
while IFS= read -r line; do
  # concise: `<file>:<line>:<col>: [<rule>] <message>`; anything else is rumdl's summary
  [[ $line == "$REL_PATH:"*": ["*"] "* ]] || continue
  loc=${line%%: \[*}
  rest=${line#*: \[}
  key="${rest%%]*} ${rest#*] }"
  CURRENT_KEYS+=("$key")
  PARSED=$((PARSED + 1))
  matched=0
  for ((i=0; i<${#BASELINE_KEYS[@]}; i++)); do
    if [[ ${BASELINE_KEYS[i]} == "$key" ]]; then
      BASELINE_KEYS[i]=''
      matched=1
      break
    fi
  done
  if ((matched == 0)); then
    ISSUES+=("UNFIXED ${loc} ${key}")
  fi
done <<< "$LEFT"
if ((PARSED == 0)); then
  echo "format-md: FAILED rumdl check on ${REL_PATH} reported issues in a shape this hook cannot read: ${LEFT}" >&2
  exit 2
fi
if [[ -n "$STATE_FILE" ]]; then
  if ! STATE_TMP=$(mktemp "$STATE_DIR/.snapshot.XXXXXX"); then
    echo "format-md: FAILED creating diagnostic snapshot for ${REL_PATH}" >&2; exit 2
  fi
  if ! printf '%s\n' "${CURRENT_KEYS[@]}" > "$STATE_TMP" || ! mv "$STATE_TMP" "$STATE_FILE"; then
    rm -f "$STATE_TMP"
    echo "format-md: FAILED saving diagnostic snapshot for ${REL_PATH}" >&2; exit 2
  fi
fi
if ((${#ISSUES[@]} == 0)); then
  exit 0
fi

{
  echo "format-md: ${REL_PATH} formatted; ${#ISSUES[@]} issue(s) rumdl cannot fix, left for you:"
  printf '  %s\n' "${ISSUES[@]}"
  echo "format-md: fix them in ${REL_PATH}; re-check from the repo root: rumdl check ${REL_PATH}"
} >&2
exit 2
