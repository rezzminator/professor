# Hooks design

| Topic | File | Covers |
| --- | --- | --- |
| hooks | [hooks.md](hooks.md) | Every hook Professor and pfm ship or install, per engine and tier: pfm's Claude hooks rendered into each launch's `--settings` payload from `claudelaunch.HookTemplates`, the project-tier template hooks, Codex, OpenCode and the Herdr reporter that reports every pfm chat to its Herdr pane; the ownership rule, including ownership by command shape for leftovers in account files; how spawn-audit, the `legacy` row and the Codex probe prove them; the discrepancies still open |
| git-guard | [git-guard.md](git-guard.md) | The machine-global `PreToolUse` `Bash` hook that denies every shared git write (worktrees, history, branches and tags, remotes, the index, whole-tree destruction, repository settings) to every agent but `gitter`: what it blocks and allows, the deny text, how it fails, its named gaps, its tests |
| pcm-guard | [pcm-guard.md](pcm-guard.md) | The machine-global `PreToolUse` hook on the edit tools and `Bash` that denies every write to a prompt file (any `CLAUDE.md`, a `.claude/` path, a config dir's `agents/`, `commands/`, `rules/`, `skills/`, the Professor clone's `templates/`) to every agent but `pcm`, and a theme to every agent but `themer`: what it protects, the Bash write forms it reads, the deny text, how it fails, its named gaps, its tests |
