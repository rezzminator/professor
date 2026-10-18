# 2026-10-18 — no hook can splice a file into a background agent's return

Observed: a `[[LOAD_FILE: {path}]]` middleware hook was proposed so a sub-agent could write its report and hand the same text to its parent in one move. A probe of Claude Code 2.1.302 found no hook event that rewrites a background agent's task-notification: `updatedToolOutput` exists only on PostToolUse and only in a narrow case, and SubagentStop sees `last_assistant_message` and the transcript path but feeds the sub-agent, not the parent.

Amend: judgment — the one-move handoff is the digger's last tool call writing the file and its final message returning the short findings plus the path (templates/global/agents/sub-rr.md#3 and #4); re-probe before designing on a hook-based splice.

Resolved: 2026-10-18 — sub-rr.md steps 3 and 4, commit 27c37fab
