# reviewer and reviewer-pro

`reviewer` (`templates/global/agents/reviewer.md`, machine-global) reviews one diff range and proves the change safe on both sides of every thread it touches: its own lines, the callers feeding it, the readers of what it writes, the artifacts it installs and the claims it makes. The lead accepts only a BASE..HEAD range — any other job (comparing two diffs, an A/B or blind grade, scoring against requirements with no range) returns `BLOCKED: not a diff review` before any spawn. It reads the diff, cuts threads, sends `tracer` seats along the threads that leave the diff and hunts every changed body itself while they walk; past about 2500 changed production lines, body tracers carry the rest. Before it verifies, one more `tracer` tries to break the SAFEs the lead granted itself. Nothing in the run executes code: the lead and every seat read and grep, and tests are judged by reading.

`reviewer-pro` (`templates/global/agents/reviewer-pro.md`) is `reviewer` plus a trust tracer in every review, a TRUST thread kind in place of EXTERNAL, and a judging rule that treats the commit's own safety claims as the author's threat model. Both tiers read an uncommitted tree (HEAD `WORKTREE`). The pro costs about a third more per review and finds about a third more of the known defects, the hostile-input class above all, at the same price per defect found, while `reviewer` holds the R1 floor. `flights-lander` picks the tier per flight from the sinks the change adds (one hit or more: `reviewer-pro`); `releaser` and merge-gating stay on `reviewer`.

`reviewer-nt` (a `templates/global/agents/reviewer-nt.md` declaration with `from: reviewer`) runs without seats: the lead walks every thread and every changed body itself, on a 140-call budget, without the `Agent` tool or the `tracer:use` skill. Five hash-pinned section overrides of `reviewer.md` carry the difference — the opening, Phase 0, Phase 1 (retitled `Walk, then hunt`), Phase 2 and the report — so a later edit to any of them fails its render with `SECTION-DRIFT` until the override is reviewed.

## Contents

- [The run](#the-run)
- [Thread kinds](#thread-kinds)
- [Report contract](#report-contract)
- [Measured](#measured)
- [Benchmark](#benchmark)
- [Open items](#open-items)

## The run

| Seat | Type, model | Takes | Returns | Cap |
| --- | --- | --- | --- | --- |
| lead | `reviewer` or `reviewer-pro`, opus, effort high | the brief: TREE, BASE..HEAD, PATHSPECS, MODE, SANDBOX, claims; HEAD `WORKTREE` with UNTRACKED | `review.md`, one line | 70 calls |
| thread tracer | `tracer` (Sonnet lead, Haiku sub-tracers) | the CALLERS, READERS, PARITY, CONTRACT, REMOVED and HOT PATH invoker threads one lead grep did not close (and EXTERNAL in `reviewer`), about 6 per tracer, at most 3 | facts with quoted `path:line`, no verdicts | its own |
| body tracer | `tracer` | past about 2500 changed lines only: one per about 1200 lines of the files left, at most 3, split by package | per changed function: returns × fields, guard branches, unset inputs, per-call costs, consumers | its own |
| trust tracer | `tracer` | `reviewer-pro` only, every review whatever its size: every sink the diff adds (open, write, stat, join, walk, subprocess, environment, unbounded read) | per sink: who shapes its path or input, symlink handling at leaf and parents, containment, IsRegular, size cap, a subprocess's env, working directory, discovered repo and honoured config; then each hostile shape walked from source to sink | its own |
| SAFE audit | `tracer` | Phase 2: each in-diff CONTRACT, HOT PATH and CONCURRENT thread the lead closed SAFE, as a question aimed at what would break it | the producer's returns beside the comment, doc or sibling that states the contract; the invoker's rate; the actor pairs | its own |

- Phase 0 (≤10 calls): the stat, the claims and the production diff (a new file by its surface only — declarations and I/O lines), `threads.md`, the crew sized by L, the changed production lines. Every in-diff CONTRACT and multi-field result is the lead's.
- Phase 1: every seat in one message, then the lead runs the § Hunter procedure over its share; it ends its turn when its hunt is done and a seat is still out, and each return re-invokes it.
- Phase 2 (≤25 calls): reconcile seats against returns; close every thread SAFE, UNSAFE or OPEN; dispatch the SAFE audit, then verify every candidate with about 15 lines around it in TREE while the audit walks; a fact that contradicts a SAFE reopens it, never a re-argued SAFE; rank CRITICAL, HIGH, MEDIUM, LOW. In `reviewer-pro` a sink a hostile shape reaches unstopped is UNSAFE unless a quoted line proves its input trusted.
- Every seat is `tracer`: no `general-purpose` seat, no hunter, no test seat. Seat dispatch is the step an opus lead obeys where a prose rule is skipped, so the SAFE audit is a seat rather than an instruction to re-check — and the trust tracer is unconditional, because a lead allowed to judge whether it is needed pre-cleared it.
- `WORKTREE` (both tiers): every diff drops HEAD, so tracked edits are read against BASE in the working tree; the claims come from the brief, since there is no commit message; each UNTRACKED file the brief lists is reviewed as a new file, and its line count joins L, so a flight's new files size the crew as its tracked edits do (`scripts/test-flight-templates.sh` pins both halves).

## Thread kinds

| Kind | The question | Caught on the benchmark |
| --- | --- | --- |
| CALLERS | every caller outside the diff of a changed signature, return, error or meaning | installer wrapper passing a zero window |
| READERS | every reader, scanner, cleaner and strict decoder of what the change writes into shared space | a new per-session file every `pfm doctor` run counts as invalid |
| PARITY | the check that proves an installed or derived value present AND equal to its source | no probe compares the compact window env to its thresholds |
| CONTRACT | producer ↔ consumer across packages and between two functions of the diff, every return path × field × unset handling | an early return leaves the role empty, rendering `name·` instead of `role ?`; a self-hosted item reported with no diff, no error and no reason |
| HOT PATH | who invokes an entry point, how often, and what one call costs | a 1–3 s settle wait paid on every model request past the window |
| EXTERNAL (`reviewer`) | the payload fields the code assumes vs what the source provides, labelled when unprovable | a zero-usage transcript line resetting the estimate |
| TRUST (`reviewer-pro`) | what another party shapes — a payload, a cloned or fetched tree, a file in a shared or project directory, a link another program placed, the inherited env, a repo's own config — and what a hostile shape does at each sink it reaches | a commit-record write following a symlink the cloned tree supplies; an inherited `GIT_DIR` redirecting an upstream diff; the user's textconv drivers shaping a diff its comment says gitconfig never shapes |
| CONCURRENT | every actor pair, two of the same kind included | the newest-writer guess naming the wrong sub-agent; a link removed by path after the updater renamed a real file onto it |
| REMOVED | the invariant a deleted line held and where it is re-established | — |
| CLAIM | each claim sentence and each doc, comment or test name vs the code | a command-line budget raised while the measured count fell |

## Report contract

- The report is `SANDBOX/review.md`: Claude Code refuses a sub-agent's write to any `.md` whose name matches `^(REPORT|SUMMARY|FINDINGS|ANALYSIS).*\.md$` and answers "Subagents should return findings as text".
- Sections: VERDICT, FINDINGS (`F{n} · severity · path:line · IN-BODY | BETWEEN-HOPS | MISSING · source · CHECKED | UNCHECKED`, a LOW finding one line), THREADS, RULED OUT, TESTS (by reading), COVERAGE and TELEMETRY.
- Merge-gating: the brief names REPORT_PATH; findings carry `status: open`, a re-review flips a verified fix to `status: resolved @{sha}` and appends new ones; `waived — {ruling}` is the orchestrator's mark.
- Readers: `gitter` Phase MERGE (project tier) refuses while the file is absent or a finding is open; `releaser` Phase REVIEW dispatches one `reviewer` per area with PATHSPECS and a REPORT_PATH per area; `flights-lander` step 3 dispatches one `reviewer-pro` per worktree when the change adds a sink, one `reviewer` when it adds none, over the flight's uncommitted change (BASE the flight's baseline, HEAD `WORKTREE`, PATHSPECS the owned paths, UNTRACKED the owned untracked files, SANDBOX `{flight directory}/review-r{round}/`) and fixes every finding inside the flight's files.

## Measured

Donor: a 40-file Go commit (+2820/−177: per-party auto-compact thresholds, a PreCompact gate, a sub-agent statusline), reviewed pre-merge. Answer key: five defects the commit shipped, each proven by a later fix commit — D1 the gate judges the wrong party, D2 no check proves the window env equals its thresholds, D3 a settle wait paid per request, D4 an empty role on an early return, D5 doctor warning on a new per-session file. Findings were ruled against the tree by an independent `opus` validator. Weighted score /100: recall 20, up/downstream threads 20, thread coverage 10, precision 10, valid yield 10, evidence 5, honest coverage 5, context tokens 10, wall time 5, ran as designed 5.

| Run | Key hits | Findings (valid + minor / invalid) | Context tokens | Wall | Score |
| --- | --- | --- | --- | --- | --- |
| `/code-review high` | 2/5 (D1, D5) | 10 (7 / 0) | 3.7M | 5.3 min | 60.8 |
| hunk-ledger lanes (opus lead, sonnet seats) | 1/5 (D1) | 5 (4 / 1) | 18.0M | 23.7 min | 34.0 |
| thread-first, 2 hunters | 4/5 | 26 (19 / 0) | 14.2M | 17.6 min | 80.5 |
| + in-diff CONTRACT threads | 5/5 | 32 | 19.7M | 20.0 min | 80.2 |
| + hunters ≤1200 lines, new files by surface | 5/5 | 35 | 17.6M | 17.2 min | 82.6 |

The hunk-ledger design saw D2, D3 and D4 and cleared each: it asked whether the settle wait had a deadline, not what it cost per call; it read the probe's skip of the env row as correct; it checked the error path of the role and not the early return. Each thread kind above exists to ask the question that clearing answered wrongly. Context tokens vary about ±30% between runs of one prompt.

## Benchmark

Two iterative-improvement passes scored each version through the installed agent path, two runs per version per rung, each run launched by a throwaway relay chat. A blind `opus` grader with hidden labels scored each review against the rung's key (FULL 1, PARTIAL 0.5) and checked every finding against the tree. Score = (0.6 × keys/K + 0.4 × min(1, valid/10)) ÷ USD per run, USD being the whole run, seats included, from `pfm tokens`.

- R1: the donor above, key D1–D5. A version replacing `reviewer` must keep an R1 mean of at least 4/5.
- R2: a 503-line installer change that re-clones source-fetched skill stores; ten defects, three of them hostile input (a symlinked commit record in the clone, a `Stat` that follows a symlinked `SKILL.md`, git discovering a repo past the store).
- R3: a 42-file commit that made project files track upstream (render plus an upstream-diff report), mined from history; five defects later fix commits proved — three hostile input (a render through a symlinked path, an inherited `GIT_DIR` steering the diff, the user's textconv drivers), one early return (an item reported with no diff, no error and no reason), one other.
- R4: a 78-file commit (account links surviving Claude's updater, a login default) with distractor hunks; seven defects — two hostile input (a write through a symlinked startup file, a remove by path racing the updater), one early return, four others.
- WT: R3's change applied uncommitted, six of its 42 files untracked, reviewed with HEAD `WORKTREE`.

| Version | Change | R1 keys | R1 USD | R1 score | R2 keys | R2 USD | R2 score | Result |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| v0 | opus hunters + test seat (ran tests) | 4.5/5 | 4.71 | 0.196 | 3.0/10 | 1.19 | 0.323 | baseline |
| v1 | body tracers replace the hunters | 3.75 | 4.66 | 0.161 | — | — | — | reverted |
| v2 | the lead hunts every body, thread tracers walk | 3.5 | 2.39 | 0.334 | 3.5 | 1.65 | 0.236 | under the floor |
| v3 | + a hunt ledger the lead fills | 3.75 | 2.63 | 0.278 | — | — | — | reverted |
| v4 | + an awk listing of returns × fields | 3.5 | 2.42 | 0.315 | 4.0 | 1.50 | 0.330 | reverted |
| v5 | + the SAFE audit tracer | 4.0 | 3.54 | 0.243 | 5.0 | 2.25 | 0.258 | kept |
| v6 | + the audit reads the contract beside each render | 4.5 | 3.18 | 0.283 | 4.75 | 2.21 | 0.264 | **`reviewer`** |
| v7 | v5 with a Sonnet lead | 3.5 | 1.44 | 0.521 | 1.75 | 0.50 | 0.516 | reverted |
| v8 | v6 with the lead at effort medium | 3.5 | 3.25 | 0.231 | 5.25 | 1.64 | 0.337 | reverted |
| v9 | v6 + TRUST kind + a trust tracer when a TRUST thread reaches a sink + the threat-model rule | — | — | — | 6.0 | 2.89 | 0.221 | the lead pre-cleared the seat |
| v10 | v9 with the trust tracer in every review | 3.75 | 4.25 | 0.186 | 7.0 | 3.24 | 0.222 | kept |
| v12 | v10 + `WORKTREE`, trims | 3.75 | 4.34 | 0.190 | — | — | — | **`reviewer-pro`** |
| v13 | v12 + OPEN threads into the SAFE audit | 3.5 (1 run) | 4.20 | 0.186 | — | — | — | no gain, reverted |

| Version | R3 keys | R3 USD | R4 keys | R4 USD | WT keys | WT USD |
| --- | --- | --- | --- | --- | --- | --- |
| v6 `reviewer` | 0.5/5 | 2.83 | 2.0/7 | 4.59 | — | — |
| v9 | 1.0 | 2.14 | 1.0 | 5.35 | — | — |
| v10 | 3.0 | 4.05 | 2.25 | 5.47 | — | — |
| v11 (`WORKTREE` draft) | — | — | — | — | 4/5 (1 run) | 4.40 |
| v12 `reviewer-pro` | — | — | — | — | 3/5 (1 run) | 4.65 |

| Agent | Mean USD per review, R1–R4 | Keys found per four-rung set | USD per key found |
| --- | --- | --- | --- |
| `reviewer` (v6) | 3.20 | 11.75 of 27 | 1.09 |
| `reviewer-pro` (v10/v12) | 4.26 | 16.0 of 27 | 1.07 |

- v6 against v0: the same R1 recall at 32% less cost, R2 recall up from 3.0 to 4.75 of 10, and no code run.
- A rule the lead had to obey in prose (a PATHS walk, a ledger to fill, a command to run) was skipped in most runs; a seat it dispatches was not. That is why v5's gain is a seat.
- A seat the lead decides whether to dispatch is a prose rule again: v9's lead judged R3's baseline files "project-shipped", closed the trust threads SAFE in Phase 0 and never sent the tracer, in both runs. v10's one-line change — the trust tracer in every review — took R3 from 1.0 to 3.0 keys.
- Hostile-input keys over R2, R3 and R4, two runs each: `reviewer` hit 1 of 16, `reviewer-pro`'s mechanism 10.5 of 16 at v10. Early-return keys on R3 and R4: `reviewer` 1 of 4, v10 3 of 4.
- The R1 mean of the trust-tracer versions sat at 3.5–4 over five runs (D1 and D2 every time, D5 FULL in four, D3 FULL in three, D4 never), under the 4/5 floor `reviewer` holds; that is why the pro is a second tier rather than a replacement.
- A Sonnet lead costs a third to a fifth as much and dispatches few or no seats: on R2 it ran 12 calls, spawned none and skipped the audit.
- Effort medium cut R2 cost by a quarter and left R1 cost flat, losing a key there.
- Run-to-run spread of one version reaches 0.11 on a rung; a gain under it counts as a tie.

## Open items

- D4 — a contract broken by an early return that leaves a field empty — was caught in 2 of 23 R1 runs (v0 and v6), never by the trust-tracer versions. The audit returns the fact; the lead judges an empty value harmless.
- A Haiku sub-tracer once created an empty `.hops-tmp-none` inside TREE (`cat > {TREE}/.hops-tmp-none; rm -f …` before its `tracetree write`), a write the seat brief forbids; in a lander's worktree it surfaces as an untracked stray in the change inventory. The fix belongs to `tracer`'s sub-tracer, not to the reviewer prompt.
- The lead is about 60% of a run's cost; every cheaper lead tried (Sonnet, effort medium) lost R1 recall.
- `tracer` always carries test homes and the check command for its spec-writer caller; a review brief asks it to skip them.
