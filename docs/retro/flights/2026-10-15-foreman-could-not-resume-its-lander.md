# 2026-10-15 — flight plugin-logs: a foreman's message to its capped lander never arrived

Observed: Twice, a root foreman tried to resume its own lander for the remaining gate checks. Role visibility refused the resume, and the foreman stopped with PARTIAL "gate pending". Only the main chat could resume the lander, which it then did.

Amend: templates/global/agents/flights-foreman.md — on a refused child resume, return BLOCKED naming the child id and the exact resume message, so the caller resumes it, instead of stopping as PARTIAL.
