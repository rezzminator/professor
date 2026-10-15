# 2026-10-15 — flight plugin-logs: review findings became scope

Observed: The lander's hostile-local-user hardening (symlinked-ancestor containment, rotation locks, owner-only backup files, rejecting oversized cap notices before archive moves) was approved by the caller instead of ruled out of scope for a single-user local log folder. The user later removed every item.

Amend: templates/global/agents/flights-foreman.md (repairs after the gate) — a review finding outside the flight's threat model is ruled "out of scope" in `run.md`, never sent to a repair round.
