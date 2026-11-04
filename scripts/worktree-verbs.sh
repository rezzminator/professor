#!/usr/bin/env bash
# Sourced by the worktree.sh suites after scripts/shtest.sh: the three verb sets that must agree —
# the ones a worktree.sh header declares (its `#   .claude/scripts/worktree.sh <verb> …` lines), the
# ones its top-level `case "${1:-…}"` dispatches, and the ones its rule's `.claude/scripts/worktree.sh`
# item names in backticks. The rule and the header tell the main chat one interface; drift misleads it.
#   worktree_verbs_agree <worktree.sh> <rule.md>  → 0 when all three sets are equal and non-empty
# BROKEN STATE: an unreadable file or an empty set returns 2 and WORKTREE_VERBS_REPORT names it, never
# a match; a disagreement returns 1 with every set in WORKTREE_VERBS_REPORT.
worktree_header_verbs() {
  awk '!/^#/ { exit } match($0, /^#   (\.\/)?\.claude\/scripts\/worktree\.sh [a-z]+/) {
    n = split(substr($0, RSTART, RLENGTH), word, " "); print word[n] }' "$1" | sort -u | tr '\n' ' '
}
worktree_case_verbs() {
  awk '/^case "\$\{1:-/ { on = 1; next } on && /^esac/ { exit } on && /^  [a-z][a-z|]*\)/ {
    sub(/^  /, ""); sub(/\).*/, ""); n = split($0, verb, "|"); for (i = 1; i <= n; i++) print verb[i] }' "$1" |
    sort -u | tr '\n' ' '
}
worktree_rule_verbs() {
  grep -F '`.claude/scripts/worktree.sh`' "$1" | grep -o '`[a-z]*`' | tr -d '`' | sort -u | tr '\n' ' '
}
worktree_verbs_agree() {
  local script="$1" rule="$2" header cases named f
  for f in "$script" "$rule"; do
    [[ -r "$f" ]] || { WORKTREE_VERBS_REPORT="unreadable: $f"; return 2; }
  done
  header="$(worktree_header_verbs "$script")" cases="$(worktree_case_verbs "$script")"
  named="$(worktree_rule_verbs "$rule")"
  WORKTREE_VERBS_REPORT="header($script)={$header} case={$cases} rule($rule)={$named}"
  [[ -n "$header" && -n "$cases" && -n "$named" ]] || { WORKTREE_VERBS_REPORT="empty set: $WORKTREE_VERBS_REPORT"; return 2; }
  [[ "$header" == "$cases" && "$cases" == "$named" ]] || return 1
}
