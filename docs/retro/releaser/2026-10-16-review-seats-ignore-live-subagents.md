# 2026-10-16 — Review seats must include the caller's live subagents

Observed: The release REVIEW phase dispatched eight reviewers with one helper seat each using its SEATS formula. The caller already had other live subagents, which the formula did not count. The combined fan-out reached the harness cap of twenty subagents; one reviewer was refused and had to be sent again after a seat became free.

Amend: `.claude/agents/releaser.md#REVIEW` and `docs/design/release/releaser.md#REVIEW` — subtract the caller's other live subagents from the available seats before choosing reviewer rounds and helper budgets. Reserve headroom rather than assuming all twenty seats belong to this release; a refused spawn waits for a free seat and is never retried in a loop.
