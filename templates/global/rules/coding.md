---
visible-to:
  include: ["main", "flights:foreman", "flights:foreman-sonnet", "flights:lander", "flights:speccer"]
---

# Code: one home per thing

Binds every agent writing code.

- Skip an unasked feature, option or flexibility, named in one line; a vague request gets the smallest core-job version.
- Smallest complete diff, correct over small: fix every caller, test and fixture it breaks; keep trust-boundary validation, data-loss error handling, security, accessibility, all asked for.
- Door = one function every caller of an operation (decision, lookup, built command, write) or mechanism (exec, file write, env read, clock, logging, LLM call) uses, scheduled or remote too: grep, call or extend it, never copy.
- No new dependency for a few lines' work; a parser, codec, protocol or known algorithm (JSONC, semver, UUID, ANSI, shell words, HTML→Markdown, OAuth, archive extraction) comes from stdlib, else an existing dependency, else a maintained popular library; hand-written, it states why (runtime-less sandbox, byte-exact output, missing semantic) in a one-line comment at its door.
- A copy your change touches (bug fix, second use, pass-through) folds into its door first: grep the expression (arithmetic, format string, threshold, query), switch every copy; report, never edit, one outside your task's files; name why one stays.
- A fold keeps every caller's exact result (guards, rounding, clamps, error text); disagreeing copies never merge by majority: the difference becomes a parameter or, if a bug, a reported behavior change; tests pass after the fold, before the change needing it.
- Fold one concept, never a coincidence: lookalikes changing for different reasons stay apart.
- Front door = the file named for its package (`{package}.go`, `index.{ts,js}`, `__init__.py`, `lib.rs`/`mod.rs`): entry points under a top-comment menu, the package's job, then one line per other non-test file; read it first; a file added, split, renamed or removed updates its menu; a package gains one when a change touches it, never in a sweep.
- A file over the project's size ceiling (higher for a front door) splits before gaining logic.
- Presentation (templates, UI scripts, routes, view builders, CLI printers) displays; domain logic moves to its door.
- Name = literal owner chain, widest first, so grepping its start finds the family: `chat_mcp_branch_get_id`; in a Go/Python package the path carries it: `chat/mcp/branch.go`.
- One word per concept in file name, identifier, wire key, env var, test: grep, reuse the code's spelling, no metaphor (`satellite` for `branch`) or `handler`, `process`, `data`, `helpers`.
- A breaking name in code you touch is renamed end to end in that change, outside your task reported; a CLI verb, config key or env var only on the user's say.
- Code lives with what changes with it, one change, one directory; no `utils`, `helpers`, `common`, `misc`, `shared` directory.
- Lists (registry, usage text, allowlist) generate from code or one source.
- A script you'd rerun goes in the project's `scripts/`.
- A failure never reads as absence ("found nothing" ≠ "could not look").
- Every check names what its broken state reports.
