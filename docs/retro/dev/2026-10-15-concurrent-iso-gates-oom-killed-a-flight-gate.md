# 2026-10-15 — concurrent `iso gate templates` and `iso gate pfm` OOM-killed a flight gate

Observed: the engine-sync-tidy phase-1 gate ran `.claude/scripts/dev.sh iso gate templates` and `iso gate pfm` at the same time on one host: the pfm gate's `pfm.e2e` step died building its binary (`compile: signal: killed`, `STEPS FAIL 209.2`) and the templates gate went `BUDGET WARN` on contention; run one after the other, both passed. `cmd_iso`'s `gate` branch starts its container with no host-wide lock, so nothing keeps a second gate from starting beside the first, and each gate already fills `STEPS_HEAVY_JOBS` heavy slots of its own.

Amend: .claude/scripts/dev.sh#cmd_iso (gate) — hold a host-wide lock for the gate's container so a second `iso gate` waits for the first, printing that it waits; until a lock lands, `.claude/commands/pfm-testing-manual.md#Run commands` (the Full bullet) says gates run one at a time per host.
