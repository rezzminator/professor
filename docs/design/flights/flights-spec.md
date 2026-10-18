# /flights:spec

`/flights:spec` is the human front of a flight: it maps only what asking needs, grills the user until no technical or product gap is left, writes the confirmed rulings and requirement rows to `requirements.md` in the flight directory, and hands that file to [`flights-foreman`](flights-foreman.md). It designs nothing the user did not rule and builds nothing.

## Contents

- [S1 — Walk only what asking needs](#s1--walk-only-what-asking-needs)
- [S2 — Grill the user](#s2--grill-the-user)
- [S3 — Write the requirements](#s3--write-the-requirements)
- [Why the objective comes first](#why-the-objective-comes-first)
- [S4 — Hand off](#s4--hand-off)
- [S5 — The one question](#s5--the-one-question)
- [What it no longer does](#what-it-no-longer-does)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## S1 — Walk only what asking needs

The chat reads the child `CLAUDE.md` of every project the work touches, so S2 never re-asks what they settle, and sends `tracer` probes for the facts a question needs (who feeds X, where Y ends). The foreman reads the code itself to build it; a map the chat builds for the foreman is reading paid twice, so the walk stops at what a good question needs. A map file it wrote travels to the foreman by path, as evidence, never as the foreman's door list.

## S2 — Grill the user

The design tree: the flight's goal at the root, each decision the flight rests on a branch. Technical branches (transport, data placement, migration, failure behaviour), product branches (who it is for, what must be true when it lands, what it must never do, what the user sees) and touchpoints (every moment the flight would need the user, pre-authorised now or cut). Rounds over the frontier: every open decision whose prerequisites are settled, asked in one message with a recommendation each; round one always holds the objective (the goal behind the request: what the user ultimately wants, what should happen once the flight lands), restated for the user to correct, and the scope boundary, which never narrows or widens silently. Facts go to probes, decisions to the user. The grill ends when the frontier is empty and the user confirms the restated rulings as the shared understanding.

A flight that stops mid-run for a user answer is a failed grill: the foreman returns `BLOCKED` on a contradicted ruling, and the cost is a second run.

## S3 — Write the requirements

`{flight directory}/requirements.md`, written by the chat:

- `Objective`: the goal behind the request as the user confirmed it, in their words where they gave them. It reaches `flights-speccer` inside the request the foreman passes on, and steers the design without bounding it.
- `Rulings`: the confirmed decisions, numbered, binding.
- `Requirements`: numbered rows, each testable, each with a concrete example in real values (`call(args) → result` for a unit, `given / when / then` at the project's real entry and exit for an integration), each naming the build unit that owns it. The foreman passes each child its own unit's rows verbatim.
- `Boundaries`: out of scope, files another owner holds.
- `Standing rules`: the worktree, the fence, the child `CLAUDE.md` paths, each touched unit's testing manual.

A requirement row states what must be true, never how the code does it: an example's values are the requirement; a shape guessed from one sample is not.

## Why the objective comes first

A request is the user's chosen route to an outcome, and the outcome is what they are after. Felix Rieseberg's essay *How to be more ambitious: A tutorial for humans with models* names this altitude: "Going up in abstraction and considering the outcome rather than the individual task." The request alone falls short because "When you give the model a task, it's executing your best guess at how to get the outcome you want." The essay asks instead: "what do you want to happen next? What's the goal beyond the goal?" Its example: "At the beginning of last year, I still asked Claude to fix individual crashes for me. Eventually I asked Claude to simply keep our apps crash-free."

So the grill asks for the objective in round one, beside the scope boundary, before any later decision rests on it, and the user corrects the restatement rather than writing it from scratch. `requirements.md` records it as its own field, so the speccer designs toward it instead of reconstructing it from the rows. The request still bounds the scope: the objective steers the choices the rulings leave open, and a place where the request falls short of it or works against it comes back as a question, never as a silently wider flight.

## S4 — Hand off

Spawn one `flights-foreman` with the path of `requirements.md`, the flight directory, the project map's path, acceptance (what the user confirmed must be true when it lands) and whether the user ordered a commit or merge. End the message; the return arrives once the flight landed or stopped. Work with no pattern in the codebase reaches `flights-speccer` through the foreman, the rulings binding there too; a fork only the user can settle comes back as the foreman's `BLOCKED` question unless the user gave the authority to decide forks.

## S5 — The one question

A `BLOCKED` return carries a question: one more round holding that question alone, or under god speed the main chat's own ruling, never a question to the user; the ruling appended to `requirements.md`, and the same foreman continued by `SendMessage`. At most one such round per flight; a second means S2 was skipped.

## What it no longer does

| Retired | Why |
| --- | --- |
| Handing the rulings to a task-file planner and presenting an index | No task files exist: the foreman reads and builds ([why](flights-foreman.md#why-one-agent-reads-and-builds)) |
| Choosing a container (`orchestrate-nested`, `orchestrate-live`) | Nested is the only Claude container; `/flights:orchestrate-cross-harness` stays for seats of another engine |
| A full area map for a spec writer | The walk stops at what asking needs |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/spec.md` | The five steps |
| The builder | [`flights-foreman`](flights-foreman.md) | What it reads from `requirements.md`; the `BLOCKED` question shape |
| The speccer | [`flights-speccer`](flights-speccer.md) | How it designs against `Objective`, and infers one when the brief has none |
