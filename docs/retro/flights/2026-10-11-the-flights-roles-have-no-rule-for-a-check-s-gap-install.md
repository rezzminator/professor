# 2026-10-11 — the flights roles have no rule for a check's `GAP install`, exit 3

Observed: `check-templates.sh` (safety-repairs) and `check-pfm.sh` (price-gap, `8c663472`) now print only `GAP install ({unit} is missing: …)` and exit 3 when a tool is absent; the role prompts say only "a red fixed and the same command again", so a builder reading exit 3 as a red may chase a tool gap in code, and one reading it as non-FAIL may report a pass.

Amend: templates/global/agents/flights-foreman.md#Tests, templates/global/agents/flights-lander.md steps 2 and 8, templates/global/agents/flights-mechanical-executor.md step 7 — a `GAP install` exit 3 is no red to fix and no pass: the return names it as a gap with the missing tools, and the builder installs nothing.

Resolved: 2026-10-11 — flights-foreman.md § Tests, flights-lander.md step 2, flights-mechanical-executor.md step 7
