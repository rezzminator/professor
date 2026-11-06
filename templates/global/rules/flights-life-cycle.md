---
visible-to:
  include: ["main"]
---

# Flights: one worktree, one lander

Binds the main chat, owner of the place, the landing and git; a sub-agent follows its brief. The lander is the costliest step, its review scaling with the diff, so related work shares one.

1. Settle open requirements with the user before any dispatch.
2. Size = judgment left in the edits, never file count. Small = every edit fixed in advance (a rename, a move, a known fix, a spec fixing every line), no other agent writing its files. Small work needs no lander: make it by hand where you are (one foreman or `pcm` where a guard reserves the files; a worktree only where the project's CLAUDE.md requires an isolated build place), run its affected tests, the full gate when it spans layers or the project's done-gate requires it, and hand `gitter` the passing check. Judgment left: `.claude/scripts/worktree.sh create {flight}` (the script); refused or absent, `gitter` SETUP; each brief carries the worktree's absolute path.
3. Build: `flights-foreman` for judgment work, `flights-foreman-sonnet` for clear work with a pattern, by difficulty, never size. Several foremen may share the worktree on separate areas; each runs only its affected tests.
4. Related work joins the open flight. Related = same feature, same files or modules, or a fix the open flight needs or caused. Before its lander starts, it goes into that worktree (a new foreman given the same path, or by hand), never a second one. Once the lander has started, it waits for that merge and opens the next flight. Unrelated work gets its own flight.
5. Land: once every foreman on the worktree has returned and no related work is pending, spawn ONE `flights-lander` for the whole worktree; never a lander per change, a second lander or a separate reviewer.
6. Merge and tear down: on PASS or FIXED, every lander finding resolved or waived, the script's `land {flight} -m "{message}"`, else `gitter` commits and merges with the lander's report path. A needed keep is `gitter`'s, `KEEP: {reason}` (a lock), carried in the Verdict until released.
7. Close without a merge (lander FAIL not retried, abandoned PARTIAL, dropped flight): once its writers stop, the script's `remove {flight}`, else `gitter`; an unmerged branch stays, dirty work locked with its reason, the user deciding.
