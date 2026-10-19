# 2026-10-19 — a new agent version registers only at the next turn start

Observed: a lab agent written into `~/.claude/agents/` and spawned in the same turn failed twice with "Agent type not found", touching the file did not help, and it registered once the turn ended on a background job. A changed definition under an existing name likewise took effect only in a later turn.

Amend: templates/global/commands/iterative-improvement.md#Round — a new or changed agent version is spawned in a turn after the one that wrote it.
