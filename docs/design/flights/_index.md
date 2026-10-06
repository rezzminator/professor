# Development design

| Topic | File | Covers |
| --- | --- | --- |
| flights | [flights.md](flights.md) | The family: what a flight is, the six agents and six commands, the lifecycle, the flight directory and its five writers, the verdict tokens, the three containers of one manual, where each rule lives, the harness settings, and what the family replaced |
| flights-speccer | [flights-speccer.md](flights-speccer.md) | The spec-writing agent: the cost model, the two flows, what it takes, its manual, how it forms tasks, the spec directory, the task-file skeleton, altitude, the run, drift and `FAILED`, `BLOCKED` with one question, the return, and the surfaces in sync |
| flights-orchestrator | [flights-orchestrator.md](flights-orchestrator.md) | The manual of running a flight: input, the run, the executor brief, verdicts as evidence, every situation and its reaction, review, `run.md`, the landing and its merge, the return, the universal laws, and the evidence behind the rulings |
| flights-executors | [flights-executors.md](flights-executors.md) | The hands of a flight, one body per tier, and the base every tier holds: what it holds so specs stop restating it, tests in the project's pattern, the layout laws at write time, the tier table and its measurements, the context budget, the 150-call cap, compaction, what it no longer does, the return |
| mechanical-executor | [mechanical-executor.md](mechanical-executor.md) | The `mechanical` tier: apply the Steps, the listed adaptations, the pre-edit search, one red run and one green run, the literal command recipe, its situations, its general twin, its pins |
| precise-executor | [precise-executor.md](precise-executor.md) | The `precise` tier: the blast-radius search, judgments pinned by tests, a test per row and `Given` line, repository-wide reuse, no unasked guards, its situations, its general twin, its pins |
| smart-executor | [smart-executor.md](smart-executor.md) | The `smart` tier: understand then design, live-path tracing, the consumer list, written deliverables, the too-large estimate, `DONE` defined, its situations, its general twin, its pins |
| flights-lander | [flights-lander.md](flights-lander.md) | The gate of a flight before its landing, one per flight: every project's gate with the floors as its rows, one review of the whole diff, the attack map, adversarial tests, its own fixes, the review effort it sizes itself, no nesting, the bounds, the ledger, the return file, the accepted risk |
| testing-manual | [testing-manual.md](testing-manual.md) | One project's testing law: where it lives, the eleven sections, who reads which, how it reaches a flight, what stays out |
| /flights:init | [flights-init.md](flights-init.md) | Readies a repository for flights: maps build units inside-out, writes the speccer manual, gives each unit a testing manual, test and check commands with their tests; re-runnable |
| /flights:spec | [flights-spec.md](flights-spec.md) | The human front of specifying: walk, grill, hand off, the one question, present |
| /flights:audit | [flights-audit.md](flights-audit.md) | The skeptic over a flight: the anchors, the seven pieces, the report |
