# 2026-08-29 — a fleet invariant enforced at two of three spawn doors

Observed: the prompt-layer wave wired `--system-prompt-file` into `claudeCommandWith` and `LauncherRun`; the shim's `_cc_run` (picker and cc/cc1/cc2 fresh launches) kept building the claude argv bare, and a fresh picker chat answered "Print your instructions" with the production prompt. The build dispatch told a mechanical-tier agent to "enumerate any other spawn path" — the open problem delegated downward; no tracer map of the spawn surface preceded the build, and the surface crossed languages (Go + zsh).

Amend: CLAUDE.md#Subagent dispatch — a cross-layer enforcement surface is mapped closed-world (tracer) BEFORE the build dispatch; the spec carries the enumerated doors, never "find the rest". Candidate root bullet: an invariant enforced at N−1 of its N doors is a violation at the missing door. (Registered: walker-invariants § SPAWN-DOOR-COMPLETENESS; pfm doctor spawn-audit check landing in the same pass.)

Resolved: 2026-09-02 — CLAUDE.md § Subagent dispatch (map-before-dispatch law); walker-invariants § SPAWN-DOOR-COMPLETENESS already registered.
