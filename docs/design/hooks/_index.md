# Hooks design

| Topic | File | Covers |
| --- | --- | --- |
| hooks | [hooks.md](hooks.md) | Every hook Professor and pfm ship or install, per engine and tier: event, matcher, command, source, where it is installed, who installs it, what it does, how it fails; the ownership rule between pfm's hooks and the operator's own; the `pfm doctor` check that proves each pfm hook is in place; the discrepancies found on a live host; the open items |
| callmeter | [callmeter.md](callmeter.md) | The async hooks (`PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `SubagentStart`, `SubagentStop`, `Stop`) that record every tool call, its bytes and its request's context size into SQLite; the command parsing; the reports on files, commands, context and repeated sequences |
| git-guard | [git-guard.md](git-guard.md) | The machine-global `PreToolUse` `Bash` hook that denies every shared git write (worktrees, history, branches and tags, remotes, the index, whole-tree destruction, repository settings) to every agent but `gitter`: what it blocks and allows, the deny text, how it fails, its named gaps, its tests |
