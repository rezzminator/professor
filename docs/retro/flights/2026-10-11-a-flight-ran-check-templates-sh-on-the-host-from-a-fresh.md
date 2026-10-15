# 2026-10-11 — a flight ran check-templates.sh on the host from a fresh worktree and reported GAP install

Observed: two flights ran `.claude/scripts/check-templates.sh` on the host inside `.worktrees/{flight}/`; it exited 3 naming PyYAML and jscpd. The fence image has both (python3-yaml in pfm-dev.Dockerfile, jscpd via tools.sh); the host's Homebrew python3 lacks PyYAML, and jscpd resolves only under the checkout's own tmp/tools, absent in a fresh worktree. Each lander then read the GAP as a residual FAIL. A fresh worktree also lacks scripts/leak-terms.txt, so the leak check needs LEAK_TERMS pointed at the main checkout's copy.

Amend: templates/global/agents/flights-foreman.md and flights-lander.md — a templates check in a flight runs through `.claude/scripts/dev.sh iso … templates`, never check-templates.sh on the host; the lander brief names LEAK_TERMS for a fresh worktree.
