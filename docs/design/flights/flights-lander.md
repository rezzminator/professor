# flights-lander

The lander takes one worktree's whole change after every foreman on it has returned. It reviews the change itself with cheap children it spawns, fixes every finding, proves the fixes with the affected tests and runs the full gate once, right before the merge. One worktree gets one lander: there is no separate reviewer, no second round and no second lander.

## Contents

- [The cast](#the-cast)
- [The flow](#the-flow)
- [Input](#input)
- [Sync to the target](#sync-to-the-target)
- [The inventory and the computed spawn](#the-inventory-and-the-computed-spawn)
- [The children](#the-children)
- [The lander's own hunt](#the-landers-own-hunt)
- [Judge and report](#judge-and-report)
- [Fix, test, gate](#fix-test-gate)
- [The return](#the-return)
- [The bench](#the-bench)
- [Declaration](#declaration)

## The cast

| Agent | Model · effort | Visible to | Job |
| --- | --- | --- | --- |
| `flights-lander` | opus · medium | the main chat | reviews, judges, fixes, tests, gates |
| `flights-hunter` | opus | both landers | hunts defects inside one cluster of the production diff |
| `flights-spec-walker` | sonnet | both landers | holds a range of the request's numbered rows against the code |
| `flights-sub-lander` | haiku | both landers | follows inventory rows or threads past the diff and flags each |
| `gpt-flights-lander` | gpt-6.1-sol · high | the main chat | the same manual on GPT, spawning the same Claude children |

Only the main chat spawns a lander. The children are read-only (`Read, Grep, Glob, Bash`, Bash for `grep`, `sed -n` and read-only `git`), write nothing and judge nothing: each returns candidates or flags for the lander to verify.

## The flow

```text
lander -> sync to TARGET's tip -> inventory (call 1) -> one spawn message (call 2)
-> hunters / spec walkers / sub-landers in the background
-> lander hunts the seams -> every candidate verified on disk -> report
-> fix every finding -> affected tests until green -> full gate once -> return
```

The lander never waits on a child: it keeps hunting, verifying and writing the report while they work, and ends its turn only when its own work is done; each child's return re-invokes it.

## Input

- WORKTREE, BASE (the sha the change is measured against), TARGET (the branch or sha it lands on, `develop` when absent).
- MANUALS: each touched project with its testing manual path.
- INTENT: the request and the foremen's returns, their decisions included, read as claims to test.
- STOP_AT `review` (optional): write the report and return after the review; no fix, test or gate.
- REPORT_PATH (optional): default `/tmp/{project}/review/{worktree name}.md`. A path the harness refuses for a sub-agent's `.md` write (`REPORT*`, `SUMMARY*`, `FINDINGS*`, `ANALYSIS*`) returns `BLOCKED`.

## Sync to the target

When TARGET's tip is not an ancestor of the worktree's HEAD, the lander has `gitter` stash, rebase the branch onto the tip and pop, in the foreground. On a conflict gitter stops with the markers in place; the lander resolves each path, keeping the change's intent on top of the tip, and tells gitter to finish. BASE becomes the tip, and the sync outcome goes into the report's COVERAGE and the return's `SYNC` line.

## The inventory and the computed spawn

Call 1 runs `lander-inventory.py`, shipped as the machine-global kit skill `templates/global/skills/lander-inventory/` and invoked as `python3 -I $HOME/.claude/skills/lander-inventory/lander-inventory.py {WORKTREE} {BASE} --spec {FILES}`. It diffs BASE against the working tree, so a foreman's uncommitted and untracked work counts with its commits. Its output file sits beside REPORT_PATH (`.md` → `.inventory.txt`):

- `LEFT`: a name the diff removed everywhere it touched, still in the tree outside history files.
- `LITERAL`: a name a code or data file dropped from inside a string or key on a replaced line, so a matcher or a stored format there knows only the new spelling.
- `GONE`: a file the diff deleted or renamed away, still named in the tree.
- `PLAN`: the spawn, computed from the diff and the spec files.

| PLAN row | Rule | Cap |
| --- | --- | --- |
| `PLAN HUNTER` | k = min(4, ceil(production diff lines / 600)) clusters in path order, each with the tests beside its files; none at 400 production lines or fewer | 4 |
| `PLAN WALKER` | a spec file with 10 or more numbered rows gets 2 ranges, 3 past 60 rows; the largest spec first | 4 in all |
| `PLAN SUB-LANDER` | inventory rows by line number: `LEFT`+`GONE` in batches of about 12, `LITERAL` of about 25 | 6 |

Production lines exclude tests, docs, generated, lock and history files. Exit 2 prints `FAILED` with its cause; the lander then names it in COVERAGE, spawns hunters by module by hand and cuts the removed threads in its own hunt.

Call 2 is one message spawning exactly the PLAN rows in the background: no row added, dropped, merged or re-cut. The spawn is computed because a written spawn rule was disobeyed in 2 of 2 bench runs, while the computed PLAN was obeyed in 4 of 4; two runs on one change spawn alike.

## The children

- `flights-hunter`: one cluster's diff and the spec rows naming its files; reads every changed function whole and asks SPEC, LINES, PATHS, MANY, GUARDS, STATE, COMMENTS and TESTS at each. Budget 30 tool calls. Returns `C{n} · severity · path:line`, the line copied from its own `grep -n` output and the failure, then `GAPS`, `READ` and `NOT READ`.
- `flights-spec-walker`: one row range of a spec; finds the line keeping each row's promise in every state the row names. Budget 30 tool calls. Returns candidates tied to a row, then `HELD`, `NOT FOUND` and `NOT READ`; a row it cannot find is `NOT FOUND`, never held.
- `flights-sub-lander`: a range of inventory rows, or up to 3 threads the lander cut (CONTRACT, ABSENCE, HAND-OFF, CONCURRENT), each followed to its far side, 3 hops at most. Budget 25 tool calls, back within about four minutes. Returns three-line flags `UNSAFE | UNSURE` with `LOOK` and `WHY`, and one `SAFE` line naming the ranges it read; a line only grepped is `UNSURE`, never `SAFE`.

## The lander's own hunt

With the children out, the lander owns what no cluster owns: the seams between clusters, each INTENT claim, every changed file no hunter row lists (docs, prompts, configs, deleted files) and the untracked files, read whole. It reads every changed function with the helpers it relies on and checks LINES, PATHS, GUARDS, REMOVED, CONCURRENCY and HOT PATH, INPUTS, TESTS and CLAIMS. Two or more threads leaving the diff go to one more background sub-lander.

## Judge and report

- Every child candidate and every `UNSAFE` or `UNSURE` flag is verified on disk: its line ±15 lines, the quoted line at that number and the failure following from it; otherwise `RULED OUT`. A `SAFE` whose `WHY` names no read lines counts as `UNSURE`.
- Spawned children are reconciled against returns received; a missing flag leaves its thread `OPEN`.
- Severity: a boundary failing open, data loss or a wrong value shown as right > a crash > a cost paid on every call > what a person sees > a log line.
- The report at REPORT_PATH: VERDICT, ranked FINDINGS (`F{n} · severity · path:line · … · status: open`, the line copied from `grep -n`), THREADS, RULED OUT, TESTS, COVERAGE (files read, children spawned against returns, `LOOK read {n} of {m}`, HEAD first and last, the sync outcome). It is the file gitter's MERGE gate reads.

## Fix, test, gate

- Every finding inside the change is fixed, an unchecked one or an open thread read to its cause first, and marked `status: resolved in {file:line}`; a finding outside the change stays `status: open` and goes to the residuals. Fixes are not reviewed again.
- The affected tests of the change and the fixes run with the testing manual's command until green.
- The full gate runs once, right before the merge; a red gate is fixed, its failing tests run green, and that gate runs once more — the only repeat.
- No test, build or gate runs while a sub-lander is out or a finding inside the change is open. A command longer than 10 minutes runs in the background with one blocking wait.
- A critical decision goes to `SendMessage(to: "main")` with the choice it proceeds with; the lander never asks the user.

## The return

```text
PASS {worktree} | FIXED {worktree} | FAIL {worktree}
SYNC {up to date | synced {old base} → {TIP}, resolved {paths} | none}
FINDINGS F{n} {fixed — file:line | outside the change — why}, … | none
GATE {project} · {last full-gate verdict line as printed}, …
RESIDUALS … | none
DECISIONS … | none
REPORT {the report path}
```

The verdict is the message's first word. Any finding left unfixed, or a gate not green, makes it `FAIL`, whatever the prose around it says.

`STOP_AT review` returns one line instead: `REVIEWED {WORKTREE} · {n} findings · REPORT {REPORT_PATH}`.

## The bench

Per replayed landing, the lander it replaced (one reviewer over the change, then fixes and the gate):

| Flight | Full landing cost | Wall time |
| --- | --- | --- |
| pull-sync | $10.80 | 1,413 s |
| herdr-claude-sync | $12.56 | 1,976 s |
| fence-shared | $12.32 | 3,205 s |

Its review phase alone on pull-sync took about $6.37 and 1,001 s.

This lander's review phase (`STOP_AT review`), two runs per flight, scored against the old lander's findings:

| Flight | Old findings matched | Cost | Wall time |
| --- | --- | --- | --- |
| fence-shared | 6.5 and 5.5 of 10 | $4.57, $4.31 | 372 s, 320 s |
| herdr-claude-sync | 3 and 4 of 5 | $6.36, $5.89 | 365 s, 382 s |

- 0 of its 69 findings were invalid, and it found real defects the old lander missed.
- Bias: the answer keys are the old lander's own findings, so a defect only the new lander finds earns it nothing, and a miss is scored against the old lander's view of the change.
- Unbenched: a full landing (fix, tests, gate) on this version, and pull-sync on this version.

## Declaration

- `templates/global/agents/flights-lander.md`: opus at effort medium, no `effortControl` and no effort buttons; its `tools:` line holds `Agent` (the children and gitter) and `SendMessage` (gitter's conflict hand-back, a critical decision to main).
- `templates/global/agents/flights-{hunter,spec-walker,sub-lander}.md`: Claude originals, each `visible-to` `flights-lander` and `gpt-flights-lander`.
- `templates/global/skills/lander-inventory/`: the inventory kit, its `SKILL.md` visible to both landers.
- `gpt-flights-lander`: a `from: flights-lander` declaration, rendered to `templates/generated/agents/gpt-flights-lander.md`; it renames nothing, so its children stay Claude agents.
