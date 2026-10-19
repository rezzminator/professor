# 2026-10-19 — the gate's contention judge could not see I/O pressure

Observed: full-gate runs on a busy host failed with TIMEOUT rows attributed to slowness, while the real cause was disk I/O contention; the judge read CPU pressure only, so I/O-bound stalls looked like regressions.

Amend: pfm/internal test-gate contention judge — read I/O PSI alongside CPU PSI before attributing a TIMEOUT.

Resolved: 2026-10-19 — 49ce55d7 fix(test): the gate holds on a busy host and still catches real slowness.
