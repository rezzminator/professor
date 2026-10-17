# 2026-09-14 — the installer's arm step compares `core.hooksPath` to the literal `.githooks`

- 2026-09-14 v0.77.1 host install: the new installer arm step compares `core.hooksPath` to the literal `.githooks`, so a clone armed with the absolute equivalent (`/…/.professor/.githooks`, which the doctor accepts as armed) is rewritten to the relative form — equivalent, harmless, but the two checks should share one comparison (resolve both against the repo root as `inspectPrePushGateWithRunner` does).

Resolved: 2026-09-15 — pfm/internal/installer/update_metadata.go
