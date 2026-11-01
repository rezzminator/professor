# Writing code

- One door per operation: each operation (a decision, a lookup, a built command, a write) and each mechanism (process exec, file write, env read, clock, logging, an LLM call) lives in one function, its door, and every caller uses it. Grep for it before writing and call or extend it; never a second copy, a scheduled or remote variant included.
- Before fixing a bug, find every caller and every copy; the fix lands in the door and folds every copy into it. Two copies drift, and a fix in one leaves the other broken.
- One name per concept, identical in file name, identifier, wire key, env var and test; reuse the spelling the code already has, never `handler`, `process`, `data`, `helpers`.
- Code lives with what changes with it, one change in one directory; never a `utils`, `helpers`, `common`, `misc` or `shared` directory.
- A list (registry, usage text, allowlist) is derived from the code or generated from one source, never a hand-kept twin.
- A script you would run twice goes in the project's `scripts/`.
- A failure never reads as absence: "found nothing" and "could not look" print differently.
- Every check names what its broken state reports.
