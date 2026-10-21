# 2026-10-19 — a merge to develop invalidated a sibling flight's gate mid-landing

Observed: two flights were landing at once. The main chat merged the Go-modules flight into develop while the login-shell flight's lander was still gating on the older develop tip. The lander returned FIXED with a green full gate, but the land refused to fast-forward, and the sync brought in `pfm/go.mod` and `pfm/go.sum`, which that gate never covered. The full gate had to run a second time on the synced tree.

Amend: pfm/harness-prompts § You hold a flight — when several flights land toward the same TARGET, merge them one at a time: a merge to TARGET while another lander is gating makes that lander's gate stale, so either hold the merge until the other lander returns, or re-gate the synced tree before its merge.
