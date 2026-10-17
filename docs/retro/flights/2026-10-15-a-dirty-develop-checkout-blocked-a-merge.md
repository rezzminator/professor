# 2026-10-15 — flight plugin-logs: a dirty develop checkout of unknown ownership blocked a merge

Observed: Uncommitted work of unknown ownership on the `develop` checkout blocked the merge; it was preserved to a side branch and a patch, the merge ran, and the work was re-applied.

Amend: templates/global/agents/gitter.md (MERGE) — preserve-then-merge (side branch plus patch, re-apply after) is the standard move for orphaned uncommitted work.
