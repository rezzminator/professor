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

# Integration tests prove only what a unit test cannot see

Designing or restructuring a suite runs `/integration-suite:use`; these laws bind every test written in one.

- A test above the unit tier asserts one success and one refusal per operation, across a process boundary, real input and output, a boot path or the built artifact.
- Boot the production entry point from the build output as a separate process with production's configuration path, never in-process.
- Each external dependency gets one scripted mock speaking its real protocol from a scenario file.
- An unscripted request fails the test by name.
- The commit gate holds no credential and calls nothing paid or live.
- Live lanes run in sequence in one environment, each inheriting earlier state.
- A lane opens with a prelude that creates missing preconditions and is a no-op otherwise.
- State builders run first, then readers, then destroyers.
- Every heavy step (a load, restart, migration or bulk change) is followed by a check that what earlier steps built still works.
- A probe reads state, never consumes it (a queue's attributes, never its message).
- A log read starts at the offset the test recorded before its first step.
- A step passes on its asserted result plus a clean activity log.
- An expected error is declared per step.
- LLM output is asserted by its parsed structure and its deterministic post-processing, never by the model's own claim.
