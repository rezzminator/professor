# 2026-10-15 — a merge --abort destroyed another chat's in-progress conflict resolution

Observed: two chats both believed they owned one MERGE_HEAD in the shared main checkout; one ran `git merge --abort` and destroyed the other chat's in-progress conflict resolution. Same incident family as `2026-10-15-parallel-gitters-share-checkout.md`.

Amend: .claude/agents/gitter.md#Conflicts — before a `merge --abort` gitter proves ownership: the merge was started by its own phase in this session; otherwise it stops and reports. A merge runs in a worktree, never the shared main checkout.
