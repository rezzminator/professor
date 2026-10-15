# 2026-10-15 — flight plugin-logs: a 4096-byte session edge case nearly reintroduced a rejection path

Observed: During the simplification, a builder stopped over a 3,922-byte session id, an input that no real session produces. Building for it would have needed a special rejection path.

Amend: templates/global/agents/flights-foreman.md — an input that cannot occur in production gets the plainest uniform rule (truncate like every other field), never a dedicated branch.
