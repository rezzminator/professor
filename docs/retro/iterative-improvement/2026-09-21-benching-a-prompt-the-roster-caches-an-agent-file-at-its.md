# 2026-09-21 — benching a prompt: the roster caches an agent file at its first write

Observed: an edited `~/.claude/agents/{name}.md` kept serving its first-written body for the whole session, so a benchmark run on a "fixed" prompt silently ran the old one; and deleting a bench agent file while a run that named it was alive made its lead fall back to `general-purpose` on the main model, voiding that run's cost.

Amend: judgment — when benchmarking a prompt, write each revision under a fresh agent name, launch it in a turn started after the write, and delete bench copies only after every run naming them has returned.
