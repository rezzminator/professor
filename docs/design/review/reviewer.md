# reviewer and reviewer-pro

`reviewer` (`templates/global/agents/reviewer.md`, machine-global) reviews one diff range and proves the change safe on both sides of every thread it touches: its own lines, the callers feeding it, the readers of what it writes, the artifacts it installs and the claims it makes. The lead accepts only a BASE..HEAD range — any other job (comparing two diffs, an A/B or blind grade, scoring against requirements with no range) returns `BLOCKED: not a diff review` before any read or spawn. It reads the diff, cuts threads, puts every question that leaves its own reading into one `tracer` run and hunts the changed bodies itself while that run walks. Nothing in the run executes code: the lead and the tracer read and grep, and tests are judged by reading.

`reviewer-pro` (`templates/global/agents/reviewer-pro.md`) is the tier for diffs of 10,000+ code lines: the same manual, with the questions spread over at most three `tracer` runs, one per area (a package or subsystem, so a run's reads overlap), all in one message, each on `BUDGET 16` where `reviewer`'s one run has `BUDGET 24`. Both tiers read an uncommitted tree (HEAD `WORKTREE`) and carry no fixed lenses or security seats: every thread kind below applies to every review.

`reviewer-nt` (a `templates/global/agents/reviewer-nt.md` declaration with `from: reviewer`) runs without tracers: the lead walks every thread and every changed body itself, on a 140-call budget, without the `Agent` tool or the `tracer:use` skill. Five hash-pinned section overrides of `reviewer.md` carry the difference — the opening, Phase 0, Phase 1 (retitled `Walk, then hunt`), Phase 2 and the report — so a later edit to any of them fails its render with `SECTION-DRIFT` until the override is reviewed.

## Contents

- [The run](#the-run)
- [Thread kinds](#thread-kinds)
- [Report contract](#report-contract)
- [Benchmark](#benchmark)
- [Open items](#open-items)

## The run

| Seat | Type, model | Takes | Returns | Cap |
| --- | --- | --- | --- | --- |
| lead | `reviewer` or `reviewer-pro`, opus, effort high | the brief: TREE, BASE..HEAD, PATHSPECS, MODE, SANDBOX, claims, `RECALL: {run}` lines; HEAD `WORKTREE` with UNTRACKED | `review.md`, one line | 70 calls |
| tracer | `tracer` | the thread questions one lead grep did not close, and the body questions past the lead's share | facts with quoted `path:line`, no verdicts | `BUDGET 24` sub-tracers; `reviewer-pro`: up to three runs at `BUDGET 16` |

- Phase 0 (≤10 calls): the stat, the claims and the production diff (a new file by its surface only — declarations and I/O lines), `threads.md`, and L, the changed production lines plus each UNTRACKED file's line count. The lead hunts the changed functions of up to about 2500 L itself, the files carrying the most CONTRACT, CONCURRENT and HOT PATH threads first; every changed file past that becomes a body question. Every in-diff CONTRACT and multi-field result is the lead's.
- Phase 1: the thread and body questions go into the tracer run (`reviewer-pro`: the runs, one per area, in one message), each brief carrying the READ-ONLY line, the numbered questions with exact files, symbols and diff lines, a `RECALL: {run}` line per tracer run the brief names (the store answers every question whose cited files are unchanged), and the order to skip CHECK, TESTS and lines to paste; no open thread and no body question means no tracer. The lead runs the § Hunter procedure over its share while the run walks and ends its turn when its hunt is done and a run is still out; each return re-invokes it.
- Phase 2 (≤25 calls): reconcile runs dispatched against returns and questions against answers, by number; close every thread SAFE, UNSAFE or OPEN; break its own SAFEs by re-reading each in-diff CONTRACT, HOT PATH and CONCURRENT thread aimed at what would break it — a fact that contradicts a SAFE reopens it, never a re-argued SAFE; verify every candidate with about 15 lines around it in one `tt show {RUN} {path}:{A}-{B} …` call; rank CRITICAL, HIGH, MEDIUM, LOW.
- `WORKTREE`: every diff drops HEAD, so tracked edits are read against BASE in the working tree; the claims come from the brief, since there is no commit message; each UNTRACKED file the brief lists is reviewed as a new file, and its line count joins L.

## Thread kinds

| Kind | The question | Caught on the benchmark |
| --- | --- | --- |
| CALLERS | every caller outside the diff of a changed signature, return, error or meaning | installer wrapper passing a zero window |
| READERS | every reader, scanner, cleaner and strict decoder of what the change writes into shared space | a new per-session file every `pfm doctor` run counts as invalid |
| PARITY | the check that proves an installed or derived value present AND equal to its source | no probe compares the compact window env to its thresholds |
| CONTRACT | producer ↔ consumer across packages and between two functions of the diff, every return path × field × unset handling | an early return leaves the role empty, rendering `name·` instead of `role ?`; a self-hosted item reported with no diff, no error and no reason |
| HOT PATH | who invokes an entry point, how often, and what one call costs | a 1–3 s settle wait paid on every model request past the window |
| EXTERNAL | the payload fields the code assumes vs what the source provides, labelled when unprovable | a zero-usage transcript line resetting the estimate |
| CONCURRENT | every actor pair, two of the same kind included | the newest-writer guess naming the wrong sub-agent; a link removed by path after the updater renamed a real file onto it |
| REMOVED | the invariant a deleted line held and where it is re-established | — |
| CLAIM | each claim sentence and each doc, comment or test name vs the code | a command-line budget raised while the measured count fell |

## Report contract

- The report is `SANDBOX/review.md`: Claude Code refuses a sub-agent's write to any `.md` whose name matches `^(REPORT|SUMMARY|FINDINGS|ANALYSIS).*\.md$` and answers "Subagents should return findings as text".
- Sections: VERDICT, FINDINGS (`F{n} · severity · path:line · IN-BODY | BETWEEN-HOPS | MISSING · source · CHECKED | UNCHECKED`, a LOW finding one line), THREADS, RULED OUT, TESTS (by reading), COVERAGE and TELEMETRY.
- Merge-gating: the brief names REPORT_PATH; findings carry `status: open`, a re-review flips a verified fix to `status: resolved @{sha}` and appends new ones; `waived — {ruling}` is the orchestrator's mark.
- Readers: `gitter` Phase MERGE (project tier) refuses while the file is absent or a finding is open; `releaser` Phase REVIEW dispatches one `reviewer` per area with PATHSPECS and a REPORT_PATH per area. `flights-lander` dispatches no reviewer: it reviews the worktree itself and writes its own report in the gated shape, each finding `status: open` until its fix marks it `status: resolved in {file:line}` ([flights-lander.md](../flights/flights-lander.md)).

## Benchmark

Each rung is a past commit whose shipped defects later fix commits proved; a blind `opus` grader with hidden labels scores a review against the rung's key (FULL 1, PARTIAL 0.5) and checks every finding against the tree. Score = (0.6 × keys/K + 0.4 × min(1, valid/10)) ÷ USD per run, USD being the whole run, tracers included, from `pfm tokens`. Run-to-run spread of one version reaches 0.11 on a rung; a gain under it counts as a tie, and context tokens vary about ±30% between runs of one prompt.

- R1: a 40-file Go commit (+2820/−177: per-party auto-compact thresholds, a PreCompact gate, a sub-agent statusline), reviewed pre-merge. Key: D1 the gate judges the wrong party, D2 no check proves the window env equals its thresholds, D3 a settle wait paid per request, D4 an empty role on an early return, D5 doctor warning on a new per-session file. A version replacing `reviewer` must keep an R1 mean of at least 4/5.
- R2: a 503-line installer change that re-clones source-fetched skill stores; ten defects, three of them hostile input (a symlinked commit record in the clone, a `Stat` that follows a symlinked `SKILL.md`, git discovering a repo past the store).
- R3: a 42-file commit that made project files track upstream (render plus an upstream-diff report); five defects — three hostile input (a render through a symlinked path, an inherited `GIT_DIR` steering the diff, the user's textconv drivers), one early return (an item reported with no diff, no error and no reason), one other.
- R4: a 78-file commit (account links surviving Claude's updater, a login default) with distractor hunks; seven defects — two hostile input (a write through a symlinked startup file, a remove by path racing the updater), one early return, four others.
- WT: R3's change applied uncommitted, six of its 42 files untracked, reviewed with HEAD `WORKTREE`.

## Open items

- D4 — a contract broken by an early return that leaves a field empty — is the key the lead most often clears: it reads the empty value and judges it harmless.
- A Haiku sub-tracer once created an empty `.hops-tmp-none` inside TREE (`cat > {TREE}/.hops-tmp-none; rm -f …` before its `tracetree write`), a write the tracer brief forbids; in a lander's worktree it surfaces as an untracked stray in the change. The fix belongs to `tracer`'s sub-tracer, not to the reviewer prompt.
- The lead is about 60% of a run's cost; every cheaper lead tried (Sonnet, effort medium) lost R1 recall.
- `tracer` carries test homes and the check command by default; a review brief asks it to skip them.
