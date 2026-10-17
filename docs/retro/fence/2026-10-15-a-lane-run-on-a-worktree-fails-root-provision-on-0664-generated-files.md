# 2026-10-15 — a lane run on a worktree fails root provision on 0664 generated files

Observed: flight plugins-all-dev's lane O1 run failed at root provision: `engine-sync global` could not chmod the worktree's `templates/generated/` files, mode 0664 on the host, through the fence's read-only mount; a `chmod 644` on the 14 files let the next run provision.

Amend: infra/fence/lanes/run.sh — chmod 644 the worktree's `templates/generated/` files before the run, or let root provision accept a mode it cannot change on a read-only mount.
