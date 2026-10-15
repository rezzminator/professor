# 2026-10-12 — a one-file pfm change sent through a flight worktree the user did not want

Observed: for the tracer-intake doctor check the main chat had gitter set up `.worktrees/tracer-intake-doctor` per § Process ("Code flights build inside the fence: a git worktree under .worktrees/{flight}/"); the user stopped it: "Doesn't need a worktree, just do it damn on develop". The flight built on develop in the main checkout, tests still in the fence, gitter committing by explicit pathspec and by hunk where another session's edit shared a file.

Amend: CLAUDE.md#Process — name when a code flight needs a worktree (parallel flights, an overlap with another session's dirty files) and that otherwise it builds on develop in the main checkout, fence tests unchanged.
