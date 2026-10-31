---
paths:
  - "**/integration/**"
  - "**/e2e/**"
  - "**/hermetic/**"
  - "**/lanes/**"
  - "**/suites/**"
  - "**/*_integration_test.*"
  - "**/*.integration.*"
  - "**/*.e2e.*"
---

# Integration tests

Designing or restructuring a suite runs `/quality:integration-suite`; these laws bind every test written in one.

- A test above the unit tier covers only what a unit test cannot see (a process boundary, real input and output, a boot path, the built artifact): one success and one refusal per operation; input variations stay table-driven unit cases.
- Boot the production entry point from the build output as a separate process with production's configuration path, never in-process.
- Each external dependency gets one scripted mock speaking its real protocol from a scenario file; an unscripted request fails the test by name.
- The commit gate holds no credential and calls nothing paid or live.
- Live lanes run in sequence in one environment, each inheriting earlier state and opening with a prelude that creates missing preconditions and is a no-op otherwise.
- State builders run first, then readers, then destroyers.
- Every heavy step (a load, restart, migration or bulk change) is followed by a check that what earlier steps built still works.
- A probe reads state, never consumes it (a queue's attributes, never its message).
- Every observation is scoped by the test's own ids and log offset.
- A step passes on its asserted result plus a clean activity log; an expected error is declared per step.
- LLM output is asserted by its parsed structure and its deterministic post-processing, never by the model's own claim.
