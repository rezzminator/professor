# 2026-09-20 — headless exec: `--engine codex` must run the Codex CLI again (user ruling)

Observed: the headless-OpenCode port (89db9254) made `--engine codex` and `cx` aliases that route through OpenCode, so a caller asking for Codex silently gets a different engine.

Amend: `pfm/internal/headless/run/engine_selector.go` (+ `internal/headless/README.md`, `docs/dev/pfm-surface.md`) — RULED by the user: `--engine codex|cx` runs the Codex CLI. Every common control Codex cannot honour (`--system` replacement, `--json-schema` / `--schema`, `--tools`, `--sealed`, and any other flag the OpenCode plugin alone implements — enumerate them from the code, closed-world) is a named pre-launch refusal that says the flag is not supported for Codex and to use `--engine opencode`; never a silent drop, never a reroute. Tests watched red first.

Resolved: 2026-09-22 — still open; queued in the parity flights (broken guards first)
