# 2026-10-18 — a lead re-invoked by its diggers' notifications is refused a second Write of its own file

Observed: the rr lead claimed its run directory by writing `{slug}.md` at PLAN, ended its turn while its background diggers ran, and was re-invoked by their notifications. At SAVE its second Write of the same path was refused with `File has not been read yet. Read it first before writing to it.`: after the re-invocation its own earlier Write no longer counts as a read. A same-turn probe had claimed the rewrite works, because it never crossed a re-invocation.

Amend: templates/global/agents/rr.md#2. PLAN and #8. SAVE — claim the directory with a separate `plan.md` and write the synthesis once, as a new file. Any agent that waits on background children and later writes again to a file it wrote before the wait hits the same refusal.

Resolved: 2026-10-18 — rr.md steps 2 and 8, commit 27c37fab
