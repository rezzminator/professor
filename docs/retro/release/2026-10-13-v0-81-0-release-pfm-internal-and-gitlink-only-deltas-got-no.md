# 2026-10-13 — v0.81.0 release: pfm/internal/** and gitlink-only deltas got no area reviewer

Observed: deltas under pfm/internal/** and plugin gitlink bumps matched no review area, so no reviewer read them.

Amend: .claude/agents/releaser.md — every changed path maps to a review area; a gitlink delta reviews the plugin's own range.
