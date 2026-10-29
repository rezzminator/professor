# Flights: one worktree, one lander

Binds the main chat, owner of the place, the landing and git; a sub-agent follows its brief. The lander is the costliest step, its review scaling with the diff, so related work shares one.

1. Settle open requirements with the user before any dispatch.
2. Small = one bounded change you can finish by hand: a few files, no open design question, no other agent writing those files. Small work needs no worktree, foreman or lander: change it by hand where you are, run its affected tests, have `gitter` commit. A project whose CLAUDE.md requires an isolated build place for it (a fence, a code worktree) keeps its law. Anything bigger: `gitter` makes `.worktrees/{flight}/` (Phase SETUP) and every brief carries that absolute path.
3. Build: `flights-foreman` for judgment work, `flights-foreman-sonnet` for clear work with a pattern, by difficulty, never size. Several foremen may share the worktree on separate areas; each runs only its affected tests.
4. Related work joins the open flight. Related = same feature, same files or modules, or a fix the open change needs or caused. Before its lander starts, it goes into that worktree (a new foreman given the same path, or by hand), never a second one. Once the lander has started, it waits for that merge and opens the next flight. Unrelated work gets its own flight.
5. Land: once every foreman on the worktree has returned and no related work is pending, spawn ONE `flights-lander` for the whole worktree; never a lander per change, a second lander or a separate reviewer.
6. Merge and tear down: on PASS or FIXED, `gitter` commits and merges with the lander's report path and removes the worktree in the same dispatch. A needed keep is `KEEP: {reason}` in that brief (a lock), carried in the Verdict until released.
7. Close without a merge (lander FAIL not retried, abandoned PARTIAL, dropped flight): once its writers stop, `gitter` removes the clean worktree and keeps any unmerged branch; dirty work is locked with its reason and the user decides.
