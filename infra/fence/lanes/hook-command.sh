#!/usr/bin/env bash
# hook-command.sh — sourced by lane A.11's static half (A.sh) and the demo's express-hooks beat
# (infra/demo/verify.sh), the two readers that require a settings.json command hook rooted at
# $CLAUDE_PROJECT_DIR.
#   hook_command_plain <command>   prints the command's plain `$CLAUDE_PROJECT_DIR/{script}[ args]` form
# The scaffolded format-md.sh command (templates/project/settings.json) first points CLAUDE_PROJECT_DIR at the
# current repository when that repository holds the same script and shares the project's git common dir (the
# project itself or one of its worktrees); that exact resolver prints as its plain form.
# Every other command, a resolver naming two different scripts included, prints unchanged, so the reader's
# own rooted check judges it.
hook_command_plain() {
  local cmd=$1 script resolver
  if [[ $cmd == 'top="$(git rev-parse --show-toplevel 2>/dev/null)" && [ -x "$top/'* ]]; then
    script="${cmd#*\"\$top/}"
    script="${script%%\"*}"
    resolver="top=\"\$(git rev-parse --show-toplevel 2>/dev/null)\" && [ -x \"\$top/$script\" ] && common=\"\$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)\" && [ \"\$common\" = \"\$(git -C \"\$CLAUDE_PROJECT_DIR\" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)\" ] && export CLAUDE_PROJECT_DIR=\"\$top\"; \"\$CLAUDE_PROJECT_DIR/$script\""
    if [[ $cmd == "$resolver" || $cmd == "$resolver "* ]]; then cmd="\$CLAUDE_PROJECT_DIR/$script${cmd#"$resolver"}"; fi
  fi
  printf '%s\n' "$cmd"
}
