---
visible-to:
  include: ["main", "flights:speccer"]
---

# Flights: one worktree, one lander

Binds the main chat, owner of the place, the landing and git; `flights:speccer` shapes its `## Phases` by rules 2, 4, 6 and 7 and places, dispatches and lands nothing; any other sub-agent follows its brief.

1. Settle open requirements with the user before any dispatch.
2. A flight builds code: program source and its tests. Docs and framework files (prompts, rules, commands, agents, config, scripts) never make one, whatever judgment they hold. Size = judgment left in the code edits, never file count; small = every edit fixed in advance (rename, move, known fix, a spec fixing every line), no other agent writing its files. Outside rule 6, make non-code and small work on the current branch, no worktree or lander, by hand or by the agent a guard reserves its files to (`pcm`; for code, one foreman), small code in a worktree only where the project's CLAUDE.md demands an isolated build; run its affected tests, the full gate when it spans layers or the project's done-gate requires it, and hand `gitter` the passing check. Judgment left: `.claude/scripts/worktree.sh create {flight}` (the script); refused or absent, `gitter` SETUP; each brief carries the worktree's absolute path.
3. Brief from the request, never from the code: a foreman is its piece's only reader. ONE `flights:tracer` (per `/flights:trace-guide`) only for a question the cut hinges on; each brief then carries its nodes' `TRACE` line.
4. Cut by tier (difficulty, never size) into the fewest foremen, the rest maximal: core = what the rest depends on or copies (contract, schema, shared function, each pattern's first instance) and every open decision → one `flights:foreman`; rest = edits a pattern fixes (existing code, a rule, the finished core) → `flights:foreman-sonnet`, its brief naming that pattern, a large rest split by area. All judgment or all pattern → one foreman.
5. Build core first in the one worktree: the rest starts once the core it copies is finished, unblocked work at once; foremen starting together spawn in one message, each running only its affected tests.
6. Related work (same feature, files or modules, a fix the open flight needs or caused, work from the same request) joins the open flight, code or not, small or not: non-code work's only way into a worktree. Before its lander starts it goes into that worktree (by hand or a new foreman given its path), never a second one; after, it waits for that merge, then goes by rule 2 like unrelated work.
7. Land: once every foreman has returned and no related work is pending, ONE `flights:lander` for the whole worktree; never a lander per change, a second one or a separate reviewer.
8. Merge: on PASS or FIXED, every finding resolved or waived, the script's `land {flight} -m "{message}"`, else `gitter` commits and merges with the lander's report path. A needed keep is `gitter`'s, `KEEP: {reason}` (a lock), carried in the Verdict until released.
9. Close without a merge (lander FAIL not retried, abandoned PARTIAL, dropped flight): once its writers stop, the script's `remove {flight}`, else `gitter`; an unmerged branch stays, dirty work locked with its reason, the user deciding.
