# 2026-10-15 — flight plugin-logs: the speccer over-built a logging feature

Observed: The ask was "a bunch of logs"; the flight added about 8,200 lines across 10 repos. The report extras (heartbeat, coverage states, malformed-row and strays sections) and the per-site classification of about 190 call sites came from the spec, not the user.

Amend: templates/global/agents/flights-speccer.md — price the spec against the user's words; every element the user did not ask for is cut or named as a question, and the simplest thing that works is the default.

Resolved: 2026-10-18 — moot: flights-speccer is deleted; requirements settle with the user in the main chat before dispatch (`pfm/harness-prompts/share/tail.md` § Orchestration)
