---
visible-to:
  include: ["flights-foreman", "flights-foreman-sonnet", "flights-lander"]
---
# Code: one home per thing

Binds every agent writing code.

- One door per operation: each operation (decision, lookup, built command, write) and mechanism (process exec, file write, env read, clock, logging, LLM call) lives in one function, its door, used by every caller; grep first and call or extend it, never a second copy, scheduled or remote included.
- A parser, codec, protocol, format or well-known algorithm (JSONC, semver, UUID, ANSI, shell words, HTML→Markdown, OAuth, archive extraction) comes from the first having it: standard library, the module's dependencies, a maintained, widely used library. Hand-writing one needs a named reason (a sandbox with no runtime, byte-exact output, a semantic the library lacks) in a one-line comment at its door.
- A copy in code your change touches (fixing a bug, adding a second use or passing through) folds into its door first. Grep the expression itself (arithmetic, format string, threshold, query), list and switch every copy; a copy outside your task's files is reported, never edited; one left is named with why.
- A fold keeps every caller's exact result (guards, rounding, clamps, error text); disagreeing copies never merge to the majority: the difference becomes a parameter or, if a bug, a reported behavior change. Affected tests pass after the fold, before the change needing it.
- Fold one concept, never a coincidence: lookalike code changing for different reasons stays apart.
- A file over the project's size ceiling splits before gaining logic.
- Presentation (templates, UI scripts, routes, view builders, CLI printers) displays values; domain logic there moves to its door.
- One name per concept, identical in file name, identifier, wire key, env var and test; reuse the code's spelling, never `handler`, `process`, `data`, `helpers`.
- Code lives with what changes with it, one change in one directory; no `utils`, `helpers`, `common`, `misc` or `shared` directory.
- A list (registry, usage text, allowlist) is generated from the code or one source, never hand-kept.
- A script you would run twice goes in the project's `scripts/`.
- A failure never reads as absence: "found nothing" and "could not look" print differently.
- Every check names what its broken state reports.
