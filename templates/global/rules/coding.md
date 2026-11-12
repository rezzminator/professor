---
visible-to:
  include: ["main", "flights:foreman", "flights:foreman-sonnet", "flights:lander", "flights:speccer"]
---

# Code: one home per thing

Binds every agent writing code.

- An unasked feature, option or flexibility is skipped, named in one line; a vague request gets the smallest version doing the core job.
- Smallest diff that fully works, correct over small: fix every caller, test and fixture it breaks; keep trust-boundary validation, data-loss error handling, security, accessibility, all asked for.
- One door per operation: each operation (decision, lookup, built command, write) and mechanism (process exec, file write, env read, clock, logging, LLM call) is one function every caller uses; grep, then call or extend it, no second copy, scheduled or remote included.
- An OS difference lives inside its door (build-tagged files, a sourced shell helper); callers never branch on OS or exec a one-OS command.
- No new dependency for what a few lines do; a parser, codec, protocol, format or well-known algorithm (JSONC, semver, UUID, ANSI, shell words, HTML→Markdown, OAuth, archive extraction) comes from the first of: standard library, the module's dependencies, a maintained, widely used one; hand-writing one needs a reason (a runtime-less sandbox, byte-exact output, a semantic the library lacks) in a one-line comment at its door.
- A copy in code your change touches (a bug fix, a second use, a pass-through) folds into its door first: grep the expression itself (arithmetic, format string, threshold, query), list and switch every copy; one outside your task's files is reported, never edited; one left is named with why.
- A fold keeps every caller's exact result (guards, rounding, clamps, error text); disagreeing copies never merge by majority: their difference becomes a parameter or, if a bug, a reported behavior change; affected tests pass after the fold, before the change needing it.
- Fold one concept, never a coincidence: lookalike code changing for different reasons stays apart.
- Front door = the file named for its package (`{package}.go`, `main.go` for `package main`, `index.ts`/`index.js`, `__init__.py`, `lib.rs`/`mod.rs`): entry points under a top-comment menu, the package's job then one line per other non-test file.
- Read the front door first; a file added, split, renamed or removed updates its menu; a package gains one when a change touches it, never in a sweep.
- A file over the project's size ceiling (higher for a front door) splits before gaining logic.
- Presentation (templates, UI scripts, routes, view builders, CLI printers) displays values; domain logic there moves to its door.
- Name = literal owner chain, widest first, so grepping its start finds the family: `chat_mcp_branch_get_id`; in a Go/Python package the path carries it: `chat/mcp/branch.go`.
- One word per concept in file name, identifier, wire key, env var, test: grep, reuse the code's spelling, no metaphor (`satellite` for `branch`) or `handler`, `process`, `data`, `helpers`.
- A breaking name in code you touch is renamed end to end in that change, outside your task reported; a CLI verb, config key or env var only on the user's say.
- Code lives with what changes with it, one change in one directory; no `utils`, `helpers`, `common`, `misc`, `shared` directory.
- A list (registry, usage text, allowlist) is generated from the code or one source, never hand-kept.
- A script you would run twice goes in the project's `scripts/`.
- A failure never reads as absence: "found nothing" and "could not look" print differently.
- Every check names what its broken state reports.
