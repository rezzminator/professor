---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: iterative-improvement
description: 'Raises anything run by run — `/iterative-improvement [target] [angle…]`, or asks like "iteratively improve X", "optimize X again and again". Each run priced to the cent, each change judged blind, each round a harder input. Returns the before/after ledger and the version history.'
argument-hint: '[target] [angle…]'
---

# Iterative Improvement

The target: $ARGUMENTS — with none, the thing in hand in this chat: an agent, prompt, command, script, function, query, page or pipeline. Bring your own imagination at full stretch to every round; a round that merely tunes is the floor.

## Lab

One directory carries the work across compactions and reboots: `$HOME/.local/state/pfm/iterative-improvement/{project}/{target}/`, `{project}` the repository's directory name minus a leading dot, `{target}` a kebab slug.

- `lab.md`: the resume brief on top (angle, metric, ladder, current version, next step), the round archive appended below a `---` marker. Rewrite the brief at every round's end, then compact.
- `ledger.tsv`: one row per run — version, rung, run, every metric.
- `v{N}/`: each version's changed files, so any version restores.

A lab already present for the target resumes at its brief's next step.

## Frame — before round 1

1. Map the target: what it is, who invokes it and with what input, what it depends on, its bench or tests, its design doc. A claim about its behavior comes from a run, never from its source alone.
2. Find what the target is in your own conception: the thing only you would make of it. Name once the version a competent default would make of it, so you can leave it.
3. Fix the angle and the metric. An angle the brief names is taken as given. Otherwise ask once with AskUserQuestion: 2–4 ranked angles (cost, correctness, completeness, latency, robustness, the receiver's experience), recommended first, each with its metric as one formula, e.g. `share of sub-asks answered × share of claims correct ÷ USD`. The formula is fixed before the first run and changes only on the user's word.
4. Build the ladder: inputs ordered by difficulty, each with an answer key or a judge's rubric — a small case, a large one, real workloads replayed verbatim from history (the target's heaviest past invocations, found in transcripts, logs or traces), an ambiguous brief with incomplete data, the largest input the target will ever meet. A rung whose world has moved since its record is replaced.
5. Baseline: run the unchanged target on the first rungs and profile where its cost, time and errors go before designing anything.

## Measure

Every run lands a ledger row at the finest grain its instruments give: per call, per agent or stage, per model, per token class (input, output, cache write, cache read), USD to the cent, wall time, requests, tool calls, output size, errors, the quality score. The instruments are the target's own: for an agent `pfm tokens` and `pfm transcript` (`/chatlog`); for code a profiler, a benchmark or a trace; for a page its performance trace. Totals are summed from rows, never estimated; a value the instrument cannot price, such as a model missing from the price table, is marked approximate with the rate used.

Meet every run's output as its receiver does, never the source alone:

- visual or interactive: real frames at several moments and widths; every interaction driven
- code: run it, its edge cases, a benchmark where speed matters
- prose or prompt: read as its reader, or as the model that runs it; each line pulls or goes
- analysis: every claim to its source; the strongest counter-argument answered

Quality is graded by an agent that did not design the change: blind A/B with the labels hidden, each sub-ask FULL, PARTIAL, MISSING or WRONG, each claim checked against its source, and a fact true at a past run's commit but changed since counted as drift, not error.

## Round

1. Diagnose: read the last runs' ledger rows, transcripts and outputs; an `agent-optimizer` audit of a run gives the why behind its numbers. Judge as the harshest expert in the target's field: where it is timid, where generic, what a first-time receiver reads it as. Name the single largest loss on the metric.
2. Ideate wide: at least three candidate changes of different kinds — architecture, model or tier, moving a rule from prose into code or a check, cutting, caching, parallelism, a different data shape — one of them the version ten times bolder. Push past your first idea until one surprises you. Leap in kind, never in polish: live over static, executable over described, a mechanism over a rule; check the materials first (libraries reachable, tools installed, the target's limits). Take the candidate with the best expected gain per cost. A rule the target disobeyed two rounds running moves into a mechanism; a third rewording of it is no candidate.
3. Build it as `v{N}` by your own hand, keeping `v{N-1}` restorable: every design decision yours, made on the spot; every detail chosen, the empty, error and edge states included. Go deeper than the target, never wider: nothing outside it changes.
4. Run it on this round's rungs, at least two runs per version per rung, more while two runs of one version differ as much as the effect. A probe runs what callers run, the installed path and the real invocation, in an isolated runner: for an agent, a throwaway chat from `chat_new` on a cheap model at low effort that only relays, closed with `chat_kill` once its result is in.
5. Verdict: keep when the metric rises beyond the run-to-run spread; otherwise revert and record a negative result with its numbers.
6. Record: a lesson row (what went wrong → what fixed it), the ledger rows, the rewritten brief.
7. Climb: a rung won or tied hands the next round a harder, different rung; a lost rung stays until it is won or its cause is named.

Stop when two consecutive rounds gain nothing beyond the spread on the hardest rung reached and a whole look finds only polish, at the brief's round or spend cap, or on the user's word.

On stop, every worktree and branch a probe left ends one way, recorded in `lab.md`: landed, removed with its reason, or listed in the report as real work the lab did not land (what it fixes, its worktree, what finishing takes). A probe that fixed a real defect is never removed unlisted.

## Report

After each round, one short status: the round, its change, its verdict, the spend so far. At the end, in order:

1. Before/after table on every metric: totals, then per rung.
2. Version history: version, change, result, kept or reverted.
3. Where it wins, where it loses, what the spread hides, what stays unbenched.
4. The rulings you made for the user, each overturnable.

Report plainly: the work carries the ambition, the report carries none. A number found wrong in an earlier report is corrected in the first line of the next.

A reaction to the delivered work is read by its content, never by its tone; an ambiguous one keeps the kept change and asks which way.

- ✗ "What the fuck is this!?" on a version built around a bold new structure → read as anger, the structure reverted. The user wanted it pushed further.
- ✓ Same message → the structure stays; one question: push it further, or take it out?

## Rules

- The next move is yours alone: the diagnosis, the candidate taken, every edit to the target and every prompt, and the verdict. A sub-agent runs, measures, grades blind or audits, and reports what it found — never a fix, a next step or an edit; a fix its report carries anyway is no input.
- A round changes one thing, and a change is kept only on repeated runs.
- A fact the work needs that sits on disk is read, never asked for. A fact only the user holds is never invented: build around it and name it in the report; ask only for one the work cannot stand without, in one question.
- A spend cap the brief sets binds, and the report names what it cut.
- Iterative improvement raises the target, never the permissions: every project rule, gate, Git writer and publication boundary holds, and a framework file changes only through its owner (the `pcm` agent).
