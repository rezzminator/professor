# /flights:spec

`/flights:spec` is the human front of specifying a flight: it maps the area, asks the user what the code cannot answer, hands [`flights-speccer`](flights-speccer.md) the decisions, and presents the index. It writes nothing itself; the flight directory is `flights-speccer`'s. It descends from `/wave:refine`'s walk-ask-write shape with the writing removed: the spec is no longer authored in the chat, because the agent that writes it is cheaper to run fresh than a main chat is to keep.

Decisions live in this file. The executable wording lives in [`templates/global/commands/flights/spec.md`](../../../templates/global/commands/flights/spec.md).

## Contents

- [Input](#input)
- [S1 — Walk the code](#s1--walk-the-code)
- [S2 — Ask the user](#s2--ask-the-user)
- [S3 — Hand off](#s3--hand-off)
- [S4 — The one question](#s4--the-one-question)
- [S5 — Present](#s5--present)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Input

`/flights:spec {tasks | a file path}`: an inline task list, or a file holding one. Empty means the user is asked what the flight is for. The command chooses the flight name (short kebab-case) and the directory `tmp/flights/{flight}/`; a collision appends `-v2`.

## S1 — Walk the code

Read the child `CLAUDE.md` of every project the tasks touch (the root one the chat already holds): the specs must stay inside their rules, and S2 must not re-ask what they settle. Then one message of probes, one per area the tasks touch: `tracer` for a question ("who feeds X", "where does Y end"), `mapper` for a whole area ("everything about the users table"). Each probe returns a map of about one page with no file content quoted: where the area lives, who writes and reads it, which files would change, where the codebase already solves a similar problem, how the area is checked, and every referent the tasks name that does not exist. Probes retrieve; the command judges and asks. After spawning the round the command ends its message and lets the maps arrive.

A referent that does not exist, an edit two tasks would both make to one target, or a dependency nobody can order is a question for S2, never a silent fix.

## S2 — Ask the user

`AskUserQuestion` rounds, at most four questions per call, each round simpler and more concrete than the last, context inside the question text. The first round always holds:

- the scope boundary: the user's whole objective restated, what this flight includes, what it defers; scope never narrows silently;
- every task the maps could not ground (`NEEDS-USER-SPEC`): specify, defer or drop.

Then the technical questions: a branch with two or more defensible options and materially different consequences (transport, data placement, migration, failure behaviour). Then the product and business questions: who the change is for, what must be true when it lands, what it must never do, what the user sees. Then the touchpoint forecast: every moment the flight would need the user (a secret, a deploy review, a destructive operation, a merge nod) is named now and pre-authorised or removed from scope; a flight that stops mid-run for a user answer is a failed spec. A task that moves protected data gets a question about its channel, never a default.

Ask nothing derivable from code. Loop until every task is clear or disposed; proceeding unclear is not an option.

## S3 — Hand off

Spawn `Agent(subagent_type: "flights-speccer")` — adding `model: "opus"` for a small flight — with content, never a format: the numbered tasks, the rulings from S2 as binding decisions, the maps, the boundaries (out of scope, files another owner holds), the standing rules (the child `CLAUDE.md` paths the tasks touch, and the flight's own: worktree, fence, checks), and the directory. End the message; the return arrives with the index.

## S4 — The one question

A `BLOCKED` item in the return carries a question. One `AskUserQuestion` round puts it to the user, and the answer goes back to the same `flights-speccer` by message as a revising call with the ruling. A second `BLOCKED` in the revised return stays `BLOCKED` in the presentation: the flight runs without that task, and the user decides whether to specify it later. At most one question round per flight; more means S2 was skipped.

## S5 — Present

The index table as returned, one line per task naming its key decisions (the `Decisions` sections extracted from the task files in one call, never the files opened whole: the chat's context outlives the flight), the `NOTES` lines, and any `BLOCKED` item. Then the three ways to run it, by name: `/flights:orchestrate-nested`, `/flights:orchestrate-live`, `/flights:orchestrate-cross-harness`. Approval is a reading, not a run; the run is the user's next command.

## Not part of the design

| Left out | Reason |
| --- | --- |
| Writing the spec in the chat | `flights-speccer` writes it, fresh and cheap; the chat holds only the maps and the rulings |
| Architect passes over the written spec | `flights-speccer`'s reconcile phase is the review; a fault found later is a revising call |
| A refining pass by a nested `flights-speccer` | The six reconcile checks replaced it |
| `poc` and research modes | `/rnd` and the RND ledger own them; a task that needs a proof is rated `hard` |
| Merge mode over several specs | There is no scheduler; a flight is one directory |
| The legal fence and the officer pass | Project rules, carried by the project's `CLAUDE.md` into every brief |
| A staleness anchor in the spec header | The orchestrator records the baseline at run start; `Progress dependency` catches a moved world |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/spec.md` | The five steps; it never restates `flights-speccer`'s format |
| The spec writer | [`flights-speccer`](flights-speccer.md) | The input contract the hand-off fills, the `BLOCKED` question shape |
| The family | [`flights.md`](flights.md) | The directory, the lifecycle, the run commands named in S5 |
