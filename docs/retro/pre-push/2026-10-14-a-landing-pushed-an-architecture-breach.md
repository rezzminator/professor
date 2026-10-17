# 2026-10-14 — a landing pushed an architecture breach

Observed: clean-sweep's `6f756a92` took `pfm/internal/installer/claude_plugins.go` to 812 lines, over the 800 ceiling; the fence gates ran on the worktree, the push's leak check passed it, and CI Verify went red after the push.

Amend: `.githooks/pre-push` runs `pfm/scripts/arch-check.sh` against an exported snapshot of every pushed develop tip, whether or not it is the checked-out HEAD; working, staged and untracked content cannot affect that verdict.
