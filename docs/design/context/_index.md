# Context design

| Topic | File | Covers |
| --- | --- | --- |
| compaction | [compaction.md](compaction.md) | Separate auto-compact thresholds for the main chat and its sub-agents: what Claude Code offers and does not, the two config keys, the window and the `PreCompact` gate `pfm install` wires, how the gate tells the parties apart and sizes a context, the live measurements behind every number, what it does not do |
| statusline | [statusline.md](statusline.md) | The context meters: one agent-panel row per sub-agent through Claude Code's `subagentStatusLine`, and the main statusline's model block, effort emoji and palette; where each field comes from, how an unreadable fact renders, what the harness does not let us decorate |
