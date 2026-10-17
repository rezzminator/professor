# 2026-10-11 — a guarded patch applied by Bash left the flight worktree's engine mirrors stale

Observed: in the price-gap flight the main chat applied `patches/guarded.patch` (`.claude/scripts/check-pfm.sh`, `.claude/commands/pfm-testing-manual.md`) with `git apply`, which the Stop hook's mirror compile never sees, then told the foreman to skip the worktree mirrors; the flight's first `iso gate` failed only on `templates.mirrors` and the lander rebuilt them.

Amend: judgment — the rule exists (root `CLAUDE.md` § Runtime, Claude: after a Bash-driven write, `pfm codex build . && pfm codex check .` then `pfm opencode build . && pfm opencode doctor .`); the main chat applying a guarded patch runs it in the worktree before resuming the foreman.

Resolved: 2026-10-11 — judgment; root CLAUDE.md § Runtime already holds the rule
