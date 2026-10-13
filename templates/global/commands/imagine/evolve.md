---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:evolve
description: 'Charts a prompt''s whole lineage — `/imagine:evolve {agent | command | skill}`, or asks like "how did X evolve", "X''s history and what comes next". /imagine:evolve → imagine-historian. Returns the visualized history and metrics, their coverage, the next version proposed.'
argument-hint: '{agent | command | skill}'
---

# Evolve

The target: $ARGUMENTS — an agent, command or skill of this repository; with none, ask which. Its whole life from its first ancestor, every version measured and seen, and the version it should become next, imagined past its ceiling and argued from evidence. The target's file is never edited: the next version is a proposal.

Load the `imagine-core` skill first — the Skill tool, or a Read of `~/.claude/skills/imagine-core/SKILL.md` where no Skill tool serves it — and the `ultimate` skill it loads; a load that fails stops here and says so.

## E1 — Resolve the target

- Find it among the repository's agents, commands and skills by `git ls-files`, originals included (`templates/**` where the repository ships them), never a guessed path.
- A name several entities share is one lineage: the file the others render from (`from:`) is the trunk, the rest its variants — `foreman` is `flights-foreman`, with `foreman-pro` and the `gpt-` twins as variants. Matches with no shared trunk: one AskUserQuestion.
- `{run}` is `$HOME/.local/state/imagine/evolve-{target}/`, `-v2` on a collision; write `{run}/salt` (`openssl rand -hex 16`) for anonymous project labels. Every artifact of the run lands in `{run}`; the checkout stays clean.

## E2 — Lineage and the user's factors

Spawn two `Agent(subagent_type: "imagine-historian")` in one message, each naming `{run}`, the repository root and the target's files and names: one in mode `LINEAGE`, one in mode `FACTORS`. End your message; the returns arrive. Read `{run}/lineage.md` and `{run}/factors.md` whole: the files are the evidence, the returns claims.

## E3 — Choose the metrics

Two sources choose them, never a fixed list:

- the factors the user has named as important, from `{run}/factors.md` — such as the ratio of reads to writes, or the hops between a read and the write it feeds
- what the target is for — its file, its return contract, what its callers do with its output: the metrics that tell a better version from a worse one, such as what a run costs, how long it takes, how often it gets the job right, what its callers had to redo; for `flights-foreman` that reads like tokens and calls per flight, wall time, peak context, DONE and BLOCKED rates, the lander's PASS, FIXED and FAIL, rework rounds

Write `{run}/metrics.md`: each metric's name, definition, unit and per-run field, whether the user named it, and the target's return vocabulary its verdicts are read from.

## E4 — Mine every source

Discover at run time every place that could hold a run of a lineage name; what follows are the kinds known, never the limit:

- callmeter's store, through `/callmeter:callmeter`; absent when the plugin is not installed
- Claude Code transcripts of every config dir on the host — each seat the callmeter store names, each account pfm knows, `~/.claude` — sub-agent transcripts included; Codex and OpenCode transcripts where a variant ran on another engine
- flight directories under `$HOME/.local/state/pfm/flights/`: run logs, verdicts, gate and lander results
- pfm's cost ledger
- the repository's reviewer and agent-optimizer reports

Spawn one `imagine-historian` per source found, mode `MINE {source}`, every one in one message, each naming the source's location, `{run}/lineage.json` and `{run}/metrics.md`. End your message; the returns arrive. Dispatched and returned are counted; a missing return is a source failed to read, named.

## E5 — Reconcile

Spawn one `imagine-historian`, mode `RECONCILE`, naming `{run}`. Read `{run}/versions.json` and `{run}/coverage.md` whole.

## E6 — See it

Make the work by your own hand under the core: its § Imagine on the evolution of the target as `{run}` holds it, its § Make, its § Look and raise, with § Evidence below binding. The work holds every version on the lineage with its live span and what changed and why, the target's own text at each version and the diff between them, every metric per version with its coverage beside the number, and E7's next version.

## E7 — The next evolution

Imagine the next version whole first, the core's § Imagine at full stretch on the target itself: the version that would move every metric the history says matters, past what any version has tried. Then every change carries its evidence — the versions and metric moves that argue for it, the confounders beside them — or reads `untested` where no evidence speaks. Write `{run}/next.md`: each change, its evidence, and the metric that will show next time whether it worked; and the full proposed text at `{run}/next/{file name}`.

## E8 — Deliver

The core's § Deliver, adding the lineage in a few lines (versions, eras, first ancestor and its date), coverage per source, and the paths of `{run}/next.md` and the proposed text.

## Evidence

- Every version and metric reads one coverage state at the visible surface: measured (sources, run count), no recorded runs (a source reaching its whole live span found none), unreached (no source's record reaches it; each source's earliest record named), failed to read (cause); partial reach names its share of the live span. A blank is never a zero, and no state renders as another.
- Where sources overlap, the reconciliation method shows on the surface.
- A metric move is credited to a version only beside the confounders that moved in the same span — model, effort, harness version, workload mix — and the run count behind it.
- The work carries the target's own text, its commit history, metric names and measured numbers; never transcript text, a user prompt or the words a factor came from, a machine path, or a project or client name — projects appear as anonymous labels.
- Every store is read only, callmeter's through its snapshot.
