---
paths:
  - "**/integration/**"
  - "**/e2e/**"
  - "**/hermetic/**"
  - "**/journey/**"
  - "**/lanes/**"
  - "**/suites/**"
  - "**/*_integration_test.*"
  - "**/*.integration.*"
  - "**/*.e2e.*"
---

# Integration tests walk the owner's real day and prove what no unit test sees

Designing or restructuring a suite runs `/design:test-suite`; these laws bind every test written in one, beside the `testing` rule.

- Above the unit tier, a test holds only what a unit call cannot see: a process boundary, real input and output, a boot path, the built artifact, the owner's path reaching a decision.
- Boot the production entry point from the build output as a separate process with production's configuration path; the live tier installs the product the way the owner does.
- Each external dependency gets one scripted mock speaking its real protocol both ways (its replies, and the events and hooks it sends) from a scenario file, recorded from the real dependency and re-recorded when a bug traces to a shape it lacked.
- An unscripted request fails the test by name.
- The commit gate holds no credential and calls nothing paid or live; the live tree runs on every pull request, on every OS the suite design lists.
- The live tier is one tree file plus one small file per step; a step chains under another only when it needs what that one left, else it forks.
- A failed step blocks only its own descendants.
- Branches running together each own what they create under a run-unique name, read only that, and stop what they started when their subtree ends, failed or not.
- Machine-wide checks and machine-wide steps (install, restart, uninstall) run only at the joins.
- A step acts through the owner's entry (keys typed into a real terminal, a click in a real browser, the public command); a direct command or internal subcommand only reads state, seeds or tears down.
- A step waits for a short literal the owner sees, then reads the state underneath (the product's own listing, its database, the process table) and asserts exact values.
- An error record in a step's log slice that the step did not declare fails it; the consistency check runs at the end of each branch and at each join.
- A step the product cannot play is left out and named in the suite design, never skipped.
- The live tree runs only on a disposable machine and refuses to start anywhere else.
- A probe reads state, never consumes it (a queue's attributes, never its message).
- A failed step saves every screen (text and raw bytes), the log slice and the process table; a passing step saves nothing.
- LLM output is asserted by its parsed structure and its deterministic post-processing, never by the model's own claim.
