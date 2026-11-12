# reviewer

`flights:reviewer` (`plugins/flights/plugins/flights/agents/reviewer.md`) reviews one diff of any size and writes its report. It is the lander's review without the landing: the same inventory script plans the work from the diff's size, the same three checker agents read it, and the reviewer verifies every candidate and judges. It fixes nothing and runs nothing: no edit, no git write, no build, test or program run; its only writes are the report and the inventory file beside it. A brief with no diff to review (compare two patches, grade work, review a plan) returns `BLOCKED: not a diff review` before any call. There is one tier: the script scales the spawn, so the 10,000+ code-line tier is gone.

## Contents

- [The run](#the-run)
- [What each seat checks](#what-each-seat-checks)
- [Report contract](#report-contract)
- [Rules and the runs they rest on](#rules-and-the-runs-they-rest-on)
- [Model and effort](#model-and-effort)
- [Open items](#open-items)

## The run

| Seat | Type, model | Takes | Returns | Cap |
| --- | --- | --- | --- | --- |
| lead | `flights:reviewer`, Opus, effort medium | TREE, BASE, optional HEAD (a sha, or absent for the working tree), PATHSPECS, MODE, INTENT, REPORT_PATH, BUDGET | the report file, one line | 40 calls |
| diff-checker | `flights:diff-checker`, Opus | one cluster of changed production files with their tests | candidates, each tied to a verbatim line | one per `PLAN DIFF-CHECKER` row, 12 at most |
| usage-checker | `flights:usage-checker`, Haiku | a range of inventory rows | three-line flags | one per `PLAN USAGE-CHECKER` row, 6 at most |
| spec-checker | `flights:spec-checker`, Sonnet | a range of a spec's numbered rows | candidates, rows held or not found | one per `PLAN SPEC-CHECKER` row, 4 at most |

1. Plan, one call: `lander-inventory.py {TREE} {BASE} {HEAD} --own --cluster-lines 750 --max-diff-checkers 12 --paths … --spec …` writes the inventory (LEFT, LITERAL and GONE rows: what the diff left behind) and the PLAN. A production diff of 400 lines or fewer plans no diff-checker and the whole diff is the lead's; above it, k = min(12, ceil(production lines / 750)) clusters in path order. `PLAN OWN` names the changed files no diff-checker reads: prose, prompts, tests no cluster took.
2. Spawn, one message: exactly the PLAN rows, all in the background; the lead never adds, drops, merges or re-cuts a row.
3. The lead's share, while the checkers work: the `PLAN OWN` files and every INTENT claim. Code and tests are read as their diff; a doc or prompt file as its added lines alone (`git diff -U0 … | grep -E '^(\+|@@)'`), each call's output under 28 KB.
4. Fold and judge: every candidate and flag is verified against its line ±15 on disk before it enters the report; one that does not hold is RULED OUT with the line it falls on; two candidates on one defect are one finding.
5. The report, then one return line: `{VERDICT} · {n} findings, {k} CRITICAL or HIGH · {COMPLETE|PARTIAL} · REPORT {path}`.

A sha HEAD reviews the commits BASE..HEAD and requires TREE to sit on it (else `BLOCKED: TREE is not at HEAD`); no HEAD reviews everything in TREE that BASE lacks, uncommitted and untracked work included.

## What each seat checks

The lead and each diff-checker read changed functions whole and tie a defect to a verbatim line with its concrete failure. The lead's nine checks: LINES (inverted condition, off-by-one, dropped error), PATHS (each return of a multi-field result against each field its consumer reads), GUARDS (fail open or closed), REMOVED (the invariant a deleted line held), CONCURRENCY and HOT PATH, INPUTS (absent, null, zero, empty), TESTS (drives the production path and would fail without the change), CLAIMS (each INTENT sentence and each doc line stating the changed behaviour, against the code), TWIN DOORS (another implementation of the operation the change leaves unfixed). The usage-checkers follow the inventory rows to their far side; the spec-checkers hold numbered spec rows against the code.

## Report contract

- The report is REPORT_PATH, default `/tmp/{project}/review/{BASE7}-{HEAD7|WORKTREE}.md`. Claude Code refuses a sub-agent's write to a `.md` named `REPORT*`, `SUMMARY*`, `FINDINGS*` or `ANALYSIS*`; such a path returns `BLOCKED: REPORT_PATH refused`.
- The layout is a literal skeleton the prompt carries: `# Review {range} · {MODE}`, then `## VERDICT`, `## FINDINGS`, `## RULED OUT`, `## TESTS`, `## COVERAGE`, with no numbering or bold on a heading or a finding line.
- A finding line: `F{n} · severity · path:line · IN-BODY | BETWEEN-HOPS | MISSING · source · CHECKED | UNCHECKED · status: open`, then the quoted line, `Fails:`, `Far side:` when there is one, and `Fix:`; a LOW finding is its line and `Fails:` alone.
- Verdict by MODE: pre-merge gives MERGE or DO NOT MERGE; post-merge gives KEEP, FIX-FORWARD or REVERT. Any CRITICAL or HIGH finding decides it.
- Merge-gating: findings carry `status: open`; a re-review of a REPORT_PATH that holds findings keeps every `F{n}`, marks a fixed one `status: resolved @{sha}` and numbers new ones on; `waived — {ruling}` is the caller's mark alone.
- Readers: `gitter` Phase MERGE refuses while the file is absent or a finding is neither `resolved` nor `waived`; `/pfm:release` dispatches one `flights:reviewer` over the `pfm-update` area's PATHSPECS when the cut changes that area. `flights:lander` dispatches no reviewer: it reviews its worktree itself ([flights-lander.md](../flights/flights-lander.md)).

## Rules and the runs they rest on

Every row cites [reviewer-bench.md](reviewer-bench.md).

| Rule | Why | Bench row |
| --- | --- | --- |
| A script computes the spawn; the lead spawns exactly its rows | two runs on one diff spawn alike, and the cut costs no model call | version 1, all rungs |
| The lead reads `PLAN OWN` only, never the whole diff | a cluster's files are its checker's; a lead that also read them paid twice | version 1, the 2,200- and 5,480-line rungs |
| No tracer | the tracer-led reviewer took 4 to 8 times longer and deadlocked on the 5,480-line rung without a report | the old reviewer, the 5,480-line rung |
| Diff-checkers explore their cluster themselves, on Opus | checkers fed a prepared pack found the planted defects and little else, and cost more | versions 2 and 3 |
| Clusters of 750 lines, 12 at most | the lander's 600 and 4 fit a landing; 12 covers the range the removed pro tier served | version 1; a diff past 9,000 production lines is unbenched |
| Doc and prompt files are read as added lines alone | a doc diff prints the old and the new long line; the lead's share fell from $1.46 to $0.92 on the 2,200-line rung | version 4 |
| The report is a literal skeleton | version 1 wrote finding lines in three forms and headings in two; version 4 held the skeleton in 3 of 3 reports | version 4 |
| Every candidate is verified on disk before it is a finding | 0 false findings in the 2 graded version 4 reports | version 4 |

## Model and effort

The lead is Opus at effort medium, as benched; the checkers keep the lander's pins (Opus, Sonnet, Haiku). Sonnet diff readers lost on the lander's bench (9.5 to 12 of 14 planted defects, never the blocking one), so Opus reads. The lead on Sonnet and the lead at another effort are unbenched.

## Open items

- Unbenched: a diff past 10,000 lines; a second repository; a commit range (a sha HEAD) and PATHSPECS, which the script's tests cover but no paid run did; the re-review of a report that holds findings; cluster sizes other than 750; the lead on Sonnet.
- The defects every version misses are seam defects whose proof is a line on the far side of a call (a wrong field read by a consumer in another package): the old reviewer, the lander's review and every version here missed the same one on the 648-line rung.
- Planted defects are saturated on the largest rung (12 to 14 of 14 in every shape); the unplanted real defects are not: nine graded runs of that rung found 16 distinct ones, half of them reported by one run alone, so a single review finds 2 to 5 of an estimated 21 to 26.
