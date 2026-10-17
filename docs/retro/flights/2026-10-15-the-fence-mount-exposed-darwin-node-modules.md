# 2026-10-15 — flight plugin-logs: the fence's read-only mount exposed the host's darwin node_modules

Observed: Inside the fence the read-only mount showed the host's darwin `node_modules`; a child stalled on it instead of taking the spec's scratch copy plus `npm ci` route.

Amend: the fence route doc every child reads (`.claude/commands/pfm-testing-manual.md` or the unit's testing manual) — dependency setup for a Node unit: scratch copy, then `npm ci`, never the mounted `node_modules`.
