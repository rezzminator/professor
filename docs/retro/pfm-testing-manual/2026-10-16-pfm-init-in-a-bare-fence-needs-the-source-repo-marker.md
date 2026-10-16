# 2026-10-16 — `pfm init` in a bare fence container needs the source-repo marker and `~/.professor` pointed at the worktree

Observed: the engine-sync-tidy phase-4 lander ran `pfm init` and `pfm doctor --project-updates` in a bare fence container; with no `~/.local/share/pfm/install/source-repo` marker and no `~/.professor` link, `pfm init` or `pfm doctor --project-updates` exited rc 1 or 3 before printing any report. Recording `/worktree` in the marker and linking `~/.professor` to `/worktree` let both run.

Amend: .claude/commands/pfm-testing-manual.md#Tricks and traps — a fence probe of `pfm init` or `pfm doctor --project-updates` in a bare container first records `/worktree` in `~/.local/share/pfm/install/source-repo` and links `~/.professor` to `/worktree`; without them both exit 1 or 3 before any report, which reads as a product failure.
