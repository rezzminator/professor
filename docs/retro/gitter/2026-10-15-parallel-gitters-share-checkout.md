# 2026-10-15 — one chat's gitter aborted another chat's in-progress merge in the shared main checkout

Observed: two chats' gitters each treated an in-progress merge in the shared main checkout as their own; PLUGINS_LOGGING's gitter aborted flight compaction-fixes' professor merge.

Amend: .claude/agents/gitter.md#Conflicts — before aborting or continuing an in-progress merge, gitter checks MERGE_HEAD against its brief and stops when the merge is not its own; merges run in a worktree, never the shared checkout.
