# /flights:spec

`/flights:spec` is the human front of specifying a flight: it maps the area, grills the user until no technical or product gap is left, hands [`flights-speccer`](flights-speccer.md) the decisions, and presents the index. It writes nothing itself; the flight directory is `flights-speccer`'s. It descends from `/wave:refine`'s walk-ask-write shape with the writing removed: the spec is no longer authored in the chat, because the agent that writes it is cheaper to run fresh than a main chat is to keep. Its interview follows the `grilling` skill of the public `mattpocock/skills` repository: the design tree, the rounds, the frontier, a recommended answer for every question, and facts found by the chat, never asked of the user. It is the main chat's only way to start `flights-speccer`; the revising calls of the orchestrators and the sub-agent ladder's third rung keep their own.

Decisions live in this file. The executable wording lives in [`templates/global/commands/flights/spec.md`](../../../templates/global/commands/flights/spec.md).

## Contents

- [Input](#input)
- [S1 — Walk the code](#s1--walk-the-code)
- [S2 — Grill the user](#s2--grill-the-user)
- [S3 — Hand off](#s3--hand-off)
- [S4 — The one question](#s4--the-one-question)
- [S5 — Present](#s5--present)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Input

`/flights:spec {tasks | a file path}`: an inline task list, or a file holding one. Empty means the user is asked what the flight is for. The command chooses the flight name (short kebab-case) and the directory `$HOME/.local/state/pfm/flights/{project}/{flight}/`; a collision appends `-v2`.

## S1 — Walk the code

Read the child `CLAUDE.md` of every project the tasks touch (the root one the chat already holds): the specs must stay inside their rules, and S2 must not re-ask what they settle. Then one message of probes, one per area the tasks touch: `tracer` for a question ("who feeds X", "where does Y end"), `mapper` for a whole area ("everything about the users table"). Each probe returns a map of about one page with no file content quoted: where the area lives, who writes and reads it, which files would change, where the codebase already solves a similar problem, how the area is checked, and every referent the tasks name that does not exist. Probes retrieve; the command judges and asks. After spawning the round the command ends its message and lets the maps arrive.

A referent that does not exist, an edit two tasks would both make to one target, or a dependency nobody can order is a question for S2, never a silent fix.

## S2 — Grill the user

The chat builds a design tree from the tasks and the maps: the flight's goal at the root, the decisions it rests on as branches, dependent decisions as leaves. A decision the tasks, the maps or a child `CLAUDE.md` settle is settled; every other is open. The tree holds three kinds of decision:

- technical: a branch with two or more defensible options and materially different consequences (transport, data placement, migration, failure behaviour);
- product: who the change is for, what must be true when it lands, what it must never do, what the user sees;
- touchpoints: every moment the flight would need the user (a secret, a deploy review, a destructive operation, a merge nod), pre-authorised now or removed from scope; a flight that stops mid-run for a user answer is a failed spec. A task that moves protected data gets a question about its channel, never a default.

The rounds work like this:

- The frontier is every open decision whose prerequisites are settled. A question that depends on another question still open waits for a later round.
- Round one always holds the scope boundary (the user's whole objective restated, what this flight includes, what it defers; scope never narrows silently) and every task the maps could not ground (`NEEDS-USER-SPEC`): specify, defer or drop.
- A round asks the whole frontier in one plain-text message, numbered, each question with its recommended answer. The chat then ends its turn and waits.
- Each answer pushes the frontier outward. The chat recomputes it and asks the next round.
- Facts are the chat's job. A question that needs one goes to a `tracer` or `mapper` probe, and only the questions downstream of a running probe wait for it.
- The grill ends when the frontier is empty. The rulings are restated as one numbered list, and the hand-off waits for the user to confirm that list.

A round is a chat message, not an `AskUserQuestion` call: the frontier can hold more than the four questions one call admits, and each question carries a recommended answer the user can accept in one word.

## S3 — Hand off

Spawn `Agent(subagent_type: "flights-speccer")` with content, never a format: the numbered tasks, the confirmed rulings from S2 as binding decisions, the maps, the boundaries (out of scope, files another owner holds), the standing rules (the child `CLAUDE.md` paths the tasks touch, and the flight's own: worktree, fence, checks), the testing manual of each project touched, and the directory. End the message; the return arrives with the index.

## S4 — The one question

A `BLOCKED` item in the return carries a question. One more round, holding that question alone, puts it to the user, and the answer goes back to the same `flights-speccer` by message as a revising call with the ruling. A second `BLOCKED` in the revised return stays `BLOCKED` in the presentation: the flight runs without that task, and the user decides whether to specify it later. At most one question round per flight; more means S2 was skipped.

## S5 — Present

The index table as returned, one line per task naming its key decisions (the `Decisions` sections extracted from the task files in one call, never the files opened whole: the chat's context outlives the flight), the `NOTES` lines, and any `BLOCKED` item. Then the three ways to run it, by name: `/flights:orchestrate-nested`, `/flights:orchestrate-live`, `/flights:orchestrate-cross-harness`. Approval is a reading, not a run; the run is the user's next command.

## Not part of the design

| Left out | Reason |
| --- | --- |
| Writing the spec in the chat | `flights-speccer` writes it, fresh and cheap; the chat holds only the maps and the rulings |
| Architect passes over the written spec | `flights-speccer`'s reconcile phase is the review; a fault found later is a revising call |
| A refining pass by a nested `flights-speccer` | The six reconcile checks replaced it |
| `poc` and research modes | `/rnd` and the RND ledger own them; a task that needs a proof is rated `smart` |
| Merge mode over several specs | There is no scheduler; a flight is one directory |
| The legal fence and the officer pass | Project rules, carried by the project's `CLAUDE.md`, which the harness gives every agent |
| A staleness anchor in the spec header | The orchestrator records the baseline at run start; `Progress dependency` catches a moved world |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/spec.md` | The five steps; it never restates `flights-speccer`'s format |
| The spec writer | [`flights-speccer`](flights-speccer.md) | The input contract the hand-off fills, the `BLOCKED` question shape |
| The family | [`flights.md`](flights.md) | The directory, the lifecycle, the run commands named in S5 |
