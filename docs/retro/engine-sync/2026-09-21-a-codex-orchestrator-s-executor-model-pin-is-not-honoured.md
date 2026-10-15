# 2026-09-21 — a Codex orchestrator's executor model pin is not honoured

Observed: flight `module-rename`, orchestrated by a Codex chat on `gpt-6-astra`, requested `gpt-5.6-sol` explicitly in every executor spawn; two executors reported running GPT-6 and the third could not tell (only the gitter role reported sol). Round-2 experiments had already shown a Codex role file honours `model_reasoning_effort` and left `model` unproven.

Amend: judgment until proven — before relying on per-executor models under a Codex orchestrator, prove by a run whether a role file's `model` key is honoured; if it is, `pfm/internal/codexgen/compiler.go` emits it (it writes a `# tier:` comment today) and the pin lives in the role, never in the spawn argument.

Resolved: 2026-09-22 — codexgen emits real `model` + `model_reasoning_effort` in role files (b030e015 task 1-a); every agent-role executor reported gpt-5.6-sol
