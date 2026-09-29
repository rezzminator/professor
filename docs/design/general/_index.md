# General design

| Topic | File | Covers |
| --- | --- | --- |
| general-orchestrator | [general-orchestrator.md](general-orchestrator.md) | The family and its manual: why it exists, the boundary against a flight and against one agent, what the caller hands it, the run (survey, cut, dispatch, verify, react, close), the brief, reactions, the 45-call law and the batch size it sets, what it does not do, the return |
| general-executors | [general-executors.md](general-executors.md) | The hands, one body per tier: what the inline brief carries, what the body holds, tests, the cap and the handoff, the return, the tiers shared with the flight executors |
| mechanical-executor | [../flights/mechanical-executor.md](../flights/mechanical-executor.md) | The `mechanical` tier, flights and general twins |
| precise-executor | [../flights/precise-executor.md](../flights/precise-executor.md) | The `precise` tier, flights and general twins |
| smart-executor | [../flights/smart-executor.md](../flights/smart-executor.md) | The `smart` tier, flights and general twins |
