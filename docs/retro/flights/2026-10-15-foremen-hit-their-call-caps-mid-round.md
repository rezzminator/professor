# 2026-10-15 — flight plugin-logs: foremen hit their call caps mid-round

Observed: Foremen hit their call caps mid-round twice, and so did the continuation lander, forcing handoffs and fresh agents.

Amend: templates/global/agents/flights-foreman.md and flights-lander.md (cap) — before dispatching a repair, reserve the calls the final full check and the handoff need; a round that cannot afford both hands off first.

Resolved: 2026-10-18 — moot: no call or round cap exists; pfm's call-cap hook is deleted
