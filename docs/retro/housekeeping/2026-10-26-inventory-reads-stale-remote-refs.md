# 2026-10-26 — the inventory classified against stale remote-tracking refs

Observed: `/housekeeping` ran its inventory without a fetch. Local `develop` and `origin/develop` both sat at a commit 61 behind GitHub's `develop`, so a worktree branch read MERGED against a base that was no longer the tip, two pruned Dependabot branches were still listed, and a remote-only branch holding unported work (`distill/host-install-2026-10-21`) was missing from `git branch -r`. Only a `gh api` compare exposed the gap; the lander that followed gated on the stale base and had to re-sync and re-gate.

Amend: templates/global/commands/housekeeping.md#1. Inventory — compare `{base}` with the remote before classifying (`git ls-remote origin {base}` against `git rev-parse {base}`, read-only); a mismatch sends `gitter` a PULL (`git fetch --prune origin`, `--ff-only`) first, so every class and every remote branch is judged on the live tip.
