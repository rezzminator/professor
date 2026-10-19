# 2026-10-19 — the Agent tool's effort field loses to an agent's pinned effort

Observed: a benchmark spawned `flights-foreman` with the Agent tool's `effort: medium`; the spawn record says medium, yet every call of the run carried effort high, the agent's frontmatter pin. A later run of the same agent whose brief opened with `[effort: medium]` ran at medium throughout.

Amend: pfm/harness-prompts/ § Model Selection — to run a pinned agent at another effort, open its brief with `[effort: {level}]`; the Agent tool's `effort` field does not override the agent's own pin.
