# 2026-10-15 — flight plugin-logs: the spec carried a self-contradicting fixture

Observed: A spec fixture paired an epoch in 2025 with an expected timestamp in 2026, and five units blocked on it.

Amend: templates/global/agents/flights-speccer.md — the speccer computes every fixture value it writes (run the conversion, paste its output), never types one by hand.
