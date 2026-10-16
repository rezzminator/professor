#!/usr/bin/env bash
# The settings.json command hooks both twins ship read as rooted at $CLAUDE_PROJECT_DIR, the form lane
# A.11's static half and the demo's express-hooks beat require; a resolver naming two scripts does not.
set -uo pipefail
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
ROOT="$(cd -- "$LANES/../../.." && pwd -P)"
SUT="${HOOK_COMMAND_SUT:-$LANES/hook-command.sh}"
# shellcheck disable=SC2034 # read by scripts/shtest.sh
SHTEST_TAG=lane-hook-command-test
# shellcheck source=/dev/null
source "$ROOT/scripts/shtest.sh"
[ -f "$SUT" ] || { echo "hook-command_test: missing reader $SUT" >&2; exit 2; }
# shellcheck source=/dev/null
source "$SUT"

for settings in "$ROOT/templates/project/settings.json" "$ROOT/.claude/settings.json"; do
  name="${settings#"$ROOT"/}"
  cmds="$(jq -er '.hooks | to_entries[] | .value[] | .hooks[] | select(.type == "command") | .command' "$settings")" ||
    { echo "hook-command_test: $name enumerates no command hook" >&2; exit 2; }
  while IFS= read -r cmd; do
    plain="$(hook_command_plain "$cmd")"
    if [[ $plain == '$CLAUDE_PROJECT_DIR/.claude/scripts/'* && $plain != *' && '* && $plain != *';'* ]]; then
      ok "$name hook reads rooted: $plain"
    else bad "$name hook not read rooted" "command: $cmd" "plain: $plain"; fi
  done <<<"$cmds"
done

resolver='top="$(git rev-parse --show-toplevel 2>/dev/null)" && [ -x "$top/.claude/scripts/a.sh" ] && common="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" && [ "$common" = "$(git -C "$CLAUDE_PROJECT_DIR" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" ] && export CLAUDE_PROJECT_DIR="$top"; "$CLAUDE_PROJECT_DIR/.claude/scripts/a.sh"'
plain="$(hook_command_plain "$resolver mark --fast")"
if [[ $plain == '$CLAUDE_PROJECT_DIR/.claude/scripts/a.sh mark --fast' ]]; then ok "a resolver's arguments stay on the plain form"
else bad "resolver arguments" "plain: $plain"; fi
mixed="${resolver%a.sh\"}b.sh\""
plain="$(hook_command_plain "$mixed")"
if [[ $plain == "$mixed" ]]; then ok "a resolver naming two scripts stays unrooted"
else bad "mixed resolver" "plain: $plain"; fi
plain="$(hook_command_plain 'bash -c "$CLAUDE_PROJECT_DIR/x.sh"')"
if [[ $plain == 'bash -c "$CLAUDE_PROJECT_DIR/x.sh"' ]]; then ok "any other command prints unchanged"
else bad "other command" "plain: $plain"; fi
shtest_end
