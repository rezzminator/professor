# Writing code

- One door per operation: each operation (a decision, a lookup, a built command, a write) and each mechanism (process exec, file write, env read, clock, logging, an LLM call) lives in one function, its door, and every caller uses it. Grep for it before writing and call or extend it; never a second copy, a scheduled or remote variant included.
- A copy you meet in the code your change touches is folded into its door before your change builds on it — fixing a bug, adding a second use, or passing through. Grep the expression itself (the arithmetic, the format string, the threshold, the query), list every copy, switch them all; a copy outside your task's files is reported, never edited, and one you leave is named with why.
- A fold keeps every caller's exact result — guards, rounding, clamps, error text; copies that disagree are never merged into the majority: the difference becomes a parameter, or, where it is a bug, a behavior change named in your report. The affected tests pass after the fold, before the change that needed it.
- Fold one concept, never a coincidence: code that looks alike but changes for different reasons stays apart.
- A file over the project's size ceiling is split before logic is added to it.
- Presentation — templates, UI scripts, routes, view builders, CLI printers — displays values; a computation or rule on domain data found there moves to its door.
- One name per concept, identical in file name, identifier, wire key, env var and test; reuse the spelling the code already has, never `handler`, `process`, `data`, `helpers`.
- Code lives with what changes with it, one change in one directory; never a `utils`, `helpers`, `common`, `misc` or `shared` directory.
- A list (registry, usage text, allowlist) is derived from the code or generated from one source, never a hand-kept twin.
- A script you would run twice goes in the project's `scripts/`.
- A failure never reads as absence: "found nothing" and "could not look" print differently.
- Every check names what its broken state reports.
