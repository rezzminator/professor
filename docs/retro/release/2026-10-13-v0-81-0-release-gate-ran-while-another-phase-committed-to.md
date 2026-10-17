# 2026-10-13 — v0.81.0 release: GATE ran while another phase committed to CANDIDATE

Observed: a GATE ran against a CANDIDATE that another phase was still committing to, so its verdict described a superseded sha and the round repeated.

Amend: .claude/commands/pfm/release.md — GATE never runs while any phase commits to CANDIDATE.
