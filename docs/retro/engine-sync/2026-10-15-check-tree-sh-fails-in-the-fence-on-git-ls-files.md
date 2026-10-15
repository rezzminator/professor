# 2026-10-15 — engine-sync's `check-tree.sh` fails in the fence on `git ls-files`

Observed: engine-sync-tidy phase 2b ran the engine-sync tree gate through `dev.sh iso run` against a flight worktree; `scripts/check-tree.py` enumerates the tree with `git ls-files`, which fails inside the fence because the worktree's `.git` file names a gitdir outside the mount, and it stops with `git ls-files failed`. The builder ran `go test` in the fence and `scripts/check-tree.sh` on the host.

Amend: plugins/engine-sync/CLAUDE.md#Local (the Tree bullet) — the tree gate needs a working Git repository: run it on the host or in a fence container where the gitdir is mounted; `go test` runs in the fence.
