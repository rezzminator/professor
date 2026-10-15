# 2026-10-15 — a merge into develop was dispatched to a foreman instead of gitter

Observed: the main chat sent a foreman-pro to merge flight compaction-fixes into develop; the user corrected it to gitter.

Amend: templates/global/agents/flights-foreman.md#Landing — a merge into develop is gitter's from the first dispatch, never a foreman's; a foreman only lands its own flight through flights-lander and hands the merge to gitter.
