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

# Integration tests walk the user's real journey and prove what no unit test sees

Designing or restructuring a suite runs `/design:test-suite`; these laws bind every test written in one.

- A test above the unit tier asserts one success and one refusal per operation, entry and journey branch, across a process boundary, real input and output, a boot path or the built artifact.
- Another literal value of one input is a unit table row; a dimension (= an axis switching backend, engine or code path) is a journey fork, never a unit row.
- Boot the production entry point from the build output as a separate process with production's configuration path, never in-process.
- Each external dependency gets one scripted mock speaking its real protocol both ways (its replies, and the events and hooks it sends) from a scenario file, recorded from the real dependency and re-recorded when a bug traces to a shape it lacked.
- An unscripted request fails the test by name.
- The commit gate holds no credential and calls nothing paid or live.
- The full live sequence runs at every merge and in CI on every OS the suite design lists; a map or coverage check never stands in for running it.
- The live root replicates the user's machine: the shells and the folders they start in, nested projects, several open workspaces, the real launcher.
- Live lanes run in sequence in one environment, each inheriting earlier state, forking at every dimension and continuing in every branch.
- An action goes through every entry the user has for it (keys into a screen or menu, a command typed where the product's hook receives it, the public CLI, an integration's own call), all in one branch; an internal subcommand may seed or observe, never act.
- A lane opens with a prelude that creates missing preconditions and is a no-op otherwise.
- State builders run first, then readers, then destroyers.
- Every heavy step (a load, restart, migration or bulk change) is followed by a check that what earlier steps built still works.
- A probe reads state, never consumes it (a queue's attributes, never its message).
- A log read starts at the offset the test recorded before its first step.
- A step asserts every effect it can observe: what the user sees (screen text, names, labels, badges, placement), then the reports and stored state behind it.
- A step passes on its asserted result plus a clean activity log.
- An expected error is declared per step.
- LLM output is asserted by its parsed structure and its deterministic post-processing, never by the model's own claim.
