# 2026-10-15 — the templates gate is red on a time budget, and its summary names a step

Observed: every templates gate in the gate-history ledger since 2026-10-14 00:42 FAILs: `templates.skill-tests` runs the tracetree unit tests at 79.6–81.8 s against an 80 s step limit and a 40 s ×2 gate budget, at host loads 9–52. A run where every step PASSed still exited 1 on the budget, and its summary read "1 step(s) failed or could not run".

Amend: the tracetree tests get faster or `templates.skill-tests` gets its own measured limit; the gate summary names a budget failure as a budget failure, never as a step.
