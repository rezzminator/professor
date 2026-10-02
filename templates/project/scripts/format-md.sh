#!/usr/bin/env bash
set -uo pipefail

# PostToolUse hook — formats the one Professor-owned .md file just written, under
# the repo-root `.rumdl.toml` policy (see /quality:md-forlint). Receives hook JSON
# on stdin.
#
# What rumdl cannot fix goes back to the agent that wrote the file: exit 2 with,
# on stderr, one header line, one `UNFIXED <file>:<line>:<col> <rule> <message>`
# line per issue, and the re-check command (Claude Code shows a PostToolUse exit-2
# stderr to the model; the write itself stands). A clean file exits 0, silent.
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

REPO_ROOT=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-toplevel 2>/dev/null) || exit 0
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
if ! (cd "$REPO_ROOT" && rumdl fmt "$REL_PATH" >/dev/null 2>&1); then
  echo "format-md: FAILED rumdl fmt on ${REL_PATH} — the file is unformatted; run \`rumdl fmt ${REL_PATH}\` from ${REPO_ROOT} to see why" >&2
  exit 2
fi

# After the format, `rumdl check` lists only what is left: exit 0 clean, 1 issues
# found, anything else a failed check.
LEFT=$(cd "$REPO_ROOT" && rumdl check --output-format concise "$REL_PATH" 2>&1)
CHECK_RC=$?
if ((CHECK_RC == 0)); then
  exit 0
fi
if ((CHECK_RC != 1)); then
  echo "format-md: FAILED rumdl check on ${REL_PATH} (exit ${CHECK_RC}) — unfixed issues could not be listed: ${LEFT}" >&2
  exit 2
fi

ISSUES=()
while IFS= read -r line; do
  # concise: `<file>:<line>:<col>: [<rule>] <message>`; anything else is rumdl's summary
  [[ $line == "$REL_PATH:"*": ["*"] "* ]] || continue
  loc=${line%%: \[*}
  rest=${line#*: \[}
  ISSUES+=("UNFIXED ${loc} ${rest%%]*} ${rest#*] }")
done <<< "$LEFT"
if ((${#ISSUES[@]} == 0)); then
  echo "format-md: FAILED rumdl check on ${REL_PATH} reported issues in a shape this hook cannot read: ${LEFT}" >&2
  exit 2
fi

{
  echo "format-md: ${REL_PATH} formatted; ${#ISSUES[@]} issue(s) rumdl cannot fix, left for you:"
  printf '  %s\n' "${ISSUES[@]}"
  echo "format-md: fix them in ${REL_PATH}; re-check from the repo root: rumdl check ${REL_PATH}"
} >&2
exit 2
