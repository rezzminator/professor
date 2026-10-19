# 2026-10-19 — pfm's internal statusline burns about 98% CPU per refresh

Observed: the pfm statusline process sat near 98% of a core on its 1-second refresh, adding to host load during gate runs.

Amend: pfm statusline renderer — profile the refresh path and cache what does not change between ticks.
