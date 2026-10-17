# 2026-10-11 — the picker parked forever on one quiet interval, so cosmos drew dead chats after a reboot

Observed: two root causes. `compose/cosmos.go` gave an event-matched node `Dead = Killed` only, so a resumable chat with comms in the 24h window drew live. `picker/pipeline.go` parked after one untouched 5s pass and ran no full pass until a keystroke, so births and deaths never reached the TUI. Fixed in 5fcc59d4 (liveness) and 2d38712b (CPU). Idle Limits under chat churn is still 0.87–1.49% of a core against the old 0.3%, because each presence-triggered pass is a full two-phase pass.

Amend: pfm/internal/picker/pipeline.go — move code out to get under the C1 ceiling, then make a presence-triggered pass lean (full load first, skip the first publish and the reload when the indexer touched nothing; ~40% of its CPU). Close R2 as well: `/tmp/cc-sid` crumb owner is never checked (gather/crumbs.go, statusline, doctor).
