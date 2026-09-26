# reviewer

`reviewer` (`templates/global/agents/reviewer.md`, machine-global) reviews one diff range and proves the change safe on both sides of every thread it touches: its own lines, the callers feeding it, the readers of what it writes, the artifacts it installs and the claims it makes. The lead reads the diff and cuts threads; `tracer` seats follow each thread out of the diff, hunter seats read every changed body, a test seat runs the affected packages; the lead verifies every candidate against the tree and writes one report.

## Contents

- [The run](#the-run)
- [Thread kinds](#thread-kinds)
- [Report contract](#report-contract)
- [Measured](#measured)
- [Open items](#open-items)

## The run

| Seat | Type, model | Takes | Returns | Cap |
| --- | --- | --- | --- | --- |
| lead | `reviewer`, opus, effort high | the brief: TREE, BASE..HEAD, PATHSPECS, MODE, SANDBOX, claims | `review.md`, one line | 40 calls |
| tracer | `tracer` (opus, medium) | 3–6 threads as numbered questions, at most 5 tracers | facts with quoted `path:line`, no verdicts | its own 25 |
| hunter | `general-purpose`, opus | ~100 hunks or ~1200 changed lines, at most 6, every changed file to one hunter; the § Hunter procedure verbatim | findings, ruled out, coverage | 35 calls |
| test | `general-purpose`, sonnet | build, vet, `-count=1` tests of changed packages and their direct importers, the diff's own gates, budget and count claims measured at BASE and HEAD | commands, exit codes, NEW vs FAILS-AT-BASE | 40 calls |

- Phase 0: the lead reads the stat, the claims and the production diff (a new file by its surface only — declarations and I/O lines), writes `threads.md`, assigns seats. A run holds about 600 hunks, the size `releaser` packs its review areas to.
- Phase 1: every seat in one message, the test seat first; the lead ends its turn and is re-invoked per return.
- Phase 2: every thread closed SAFE, UNSAFE or OPEN; every candidate re-read in TREE with ~15 lines around it before it enters the report; rank CRITICAL, HIGH, MEDIUM, LOW.
- BASE is exported with `git archive` into SANDBOX and run there without git: the fleet's git guard refuses `git init`, commits and checkouts from any seat but `gitter`.

## Thread kinds

| Kind | The question | Caught on the benchmark donor |
| --- | --- | --- |
| CALLERS | every caller outside the diff of a changed signature, return, error or meaning | installer wrapper passing a zero window |
| READERS | every reader, scanner, cleaner and strict decoder of what the change writes into shared space | a new per-session file every `pfm doctor` run counts as invalid |
| PARITY | the check that proves an installed or derived value present AND equal to its source | no probe compares the compact window env to its thresholds |
| CONTRACT | producer ↔ consumer across packages and between two functions of the diff, every return path × field × unset handling | an early return leaves the role empty, rendering `name·` instead of `role ?` |
| HOT PATH | who invokes an entry point, how often, and what one call costs | a 1–3 s settle wait paid on every model request past the window |
| EXTERNAL | the payload fields the code assumes vs what the source provides, labelled when unprovable | a zero-usage transcript line resetting the estimate |
| CONCURRENT | every actor pair, two of the same kind included | the newest-writer guess naming the wrong sub-agent |
| REMOVED | the invariant a deleted line held and where it is re-established | — |
| CLAIM | each claim sentence and each doc, comment or test name vs the code | a command-line budget raised while the measured count fell |

## Report contract

- The report is `SANDBOX/review.md`: Claude Code refuses a sub-agent's write to any `.md` whose name matches `^(REPORT|SUMMARY|FINDINGS|ANALYSIS).*\.md$` and answers "Subagents should return findings as text".
- Sections: VERDICT, FINDINGS (`F{n} · severity · path:line · IN-BODY | BETWEEN-HOPS | MISSING · source · CHECKED | UNCHECKED`, a LOW finding one line), THREADS, RULED OUT, TESTS, COVERAGE and TELEMETRY.
- Merge-gating: the brief names REPORT_PATH; findings carry `status: open`, a re-review flips a verified fix to `status: resolved @{sha}` and appends new ones; `waived — {ruling}` is the orchestrator's mark. Readers: `gitter` Phase MERGE (project tier) refuses while the file is absent or a finding is open; `releaser` Phase REVIEW dispatches one `reviewer` per area with PATHSPECS and a REPORT_PATH per area.

## Measured

Donor: a 40-file Go commit (+2820/−177: per-party auto-compact thresholds, a PreCompact gate, a sub-agent statusline), reviewed pre-merge. Answer key: five defects the commit shipped, each proven by a later fix commit — D1 the gate judges the wrong party, D2 no check proves the window env equals its thresholds, D3 a settle wait paid per request, D4 an empty role on an early return, D5 doctor warning on a new per-session file. Findings were ruled against the tree by an independent `opus` validator. Weighted score /100: recall 20, up/downstream threads 20, thread coverage 10, precision 10, valid yield 10, evidence 5, honest coverage 5, context tokens 10, wall time 5, ran as designed 5.

| Run | Key hits | Findings (valid + minor / invalid) | Context tokens | Wall | Score |
| --- | --- | --- | --- | --- | --- |
| `/code-review high` | 2/5 (D1, D5) | 10 (7 / 0) | 3.7M | 5.3 min | 60.8 |
| hunk-ledger lanes (opus lead, sonnet seats) | 1/5 (D1) | 5 (4 / 1) | 18.0M | 23.7 min | 34.0 |
| thread-first, 2 hunters | 4/5 | 26 (19 / 0) | 14.2M | 17.6 min | 80.5 |
| + in-diff CONTRACT threads | 5/5 | 32 | 19.7M | 20.0 min | 80.2 |
| + hunters ≤1200 lines, new files by surface (live) | 5/5 | 35 | 17.6M | 17.2 min | 82.6 |

The hunk-ledger design saw D2, D3 and D4 and cleared each: it asked whether the settle wait had a deadline, not what it cost per call; it read the probe's skip of the env row as correct; it checked the error path of the role and not the early return. Each thread kind above exists to ask the question that clearing answered wrongly. Context tokens vary about ±30% between runs of one prompt.

## Open items

- Cost: about 4.7× `/code-review high` in context tokens for 5/5 against 2/5 recall; hunters are about half of it.
- `tracer` always carries test homes and the check command for its spec-writer caller; a review brief asks it to skip them.
- A registered hunter role would carry the § Hunter procedure as its own prompt instead of a verbatim copy in every brief.
