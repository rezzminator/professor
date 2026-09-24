# /flights:refine

`/flights:refine` is the human front of revising a flight that is already written: it reads the flight directory, grills the user on the decisions in it until nothing is assumed, hands [`flights-speccer`](flights-speccer.md) the rulings as a revising call, and presents the revised index. It writes nothing itself; the flight directory stays `flights-speccer`'s.

Its interview follows the `grilling` skill of the public `mattpocock/skills` repository: the design tree, the rounds, the frontier, a recommended answer for every question, and facts found by the chat, never asked of the user. Its place in the family comes from `/wave:refine`, whose name it keeps. That command was the one-step way to reshape a spec, and when it was retired, revising a spec could only happen as a fault reaction inside the orchestrator.

Decisions live in this file. The executable wording lives in [`templates/global/commands/flights/refine.md`](../../../templates/global/commands/flights/refine.md).

## Contents

- [Input](#input)
- [F1 — Read the flight](#f1--read-the-flight)
- [F2 — Grill](#f2--grill)
- [F3 — Hand off](#f3--hand-off)
- [F4 — Present](#f4--present)
- [Why not /flights:spec](#why-not-flightsspec)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Input

`/flights:refine [directory] [what to change]`. With no directory it takes the newest under `$HOME/.local/state/pfm/flights/{project}/`, which must hold `index.md`. With no change named, the whole flight is the subject.

## F1 — Read the flight

The chat reads `index.md` and extracts the `Goal` and `Decisions` sections of every task file in one call. It never opens a task file whole, because the chat's context outlives the flight. With a `run.md`, the `DONE` ids and what landed are settled ground. A `CLAIMED` id without a verdict is out of reach, because its executor has already read the file; `flights-speccer`'s § Revising refuses to touch it. The design tree has the flight's goal at its root and the decisions it rests on as branches. A decision the user ruled on in `/flights:spec` is settled. A decision the speccer made on its own is open until the user confirms it.

## F2 — Grill

The rounds work like this:

- The frontier is every open decision whose prerequisites are settled. A question that depends on another question still open in the same round waits for a later round.
- A round asks the whole frontier in one plain-text message, numbered, each question with its recommended answer. The chat then ends its turn and waits.
- Each answer pushes the frontier outward. The chat recomputes it and asks the next round.
- Facts are the chat's job. A question that needs one goes to a `tracer` or `mapper` probe, and only the questions downstream of a running probe wait for it.
- The grill ends when the frontier is empty. The rulings are restated as one numbered list, and the hand-off waits for the user to confirm that list.

Round one always holds the scope boundary and what the user asked to change: scope never narrows silently, the same law as `/flights:spec` S2.

A round is a chat message, not an `AskUserQuestion` call. The frontier of a written flight can hold more than the four questions one call admits, and each question carries a recommended answer the user can accept in one word. `/flights:spec` keeps `AskUserQuestion`, because its rounds shape a flight that does not exist yet.

## F3 — Hand off

`flights-speccer` on opus runs a revising call with: the directory, the reason (`refinement`), the confirmed rulings as binding decisions, the probes' maps, and, with a `run.md`, the completed ids, what landed and every `CLAIMED` id. That is exactly the input `flights-speccer`'s § Revising takes, so the agent needs no change. A `BLOCKED` item gets one more round holding its question alone, sent back by `SendMessage`. A `BLOCKED` item still in that return stays `BLOCKED`.

## F4 — Present

The chat presents the index table, one line per changed id naming the ruling it now carries, and any `BLOCKED` item. What runs next depends on `run.md`. A flight with one resumes through `/flights:orchestrate-nested {directory} revised {changed ids}`, which dispatches only the rewritten tasks. A flight without one gets the three ways to run from `/flights:spec` S5. Approval is a reading, not a run.

## Why not /flights:spec

`/flights:spec` builds a flight from a task list: it probes areas nobody has mapped and asks the questions no spec has answered yet. `/flights:refine` starts from a flight that already exists: its tree is the decisions already written, and its questions are the ones the speccer settled without the user. Merging the two into one command would give it two inputs, two question shapes and two hand-offs.

## Not part of the design

| Left out | Reason |
| --- | --- |
| Editing a task file in the chat | Only `flights-speccer` changes a task file |
| Revising after a `FAILED` or `SPEC-DRIFT` | The orchestrator routes a fault to the speccer on its own; `/flights:refine` is the user's reason, not the run's |
| Touching a `CLAIMED` task | Its executor has read the file; a change reaches it only as a new task |
| Starting the run | Approval is a reading; the run is the user's next command |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/refine.md` | The four steps; it never restates `flights-speccer`'s format |
| The spec writer | [`flights-speccer`](flights-speccer.md) | § Revising, the input F3 fills |
| The entry command | [`/flights:spec`](flights-spec.md) | The family chain in its description, the pointer in S5 |
| The family | [`flights.md`](flights.md) | The member table, the lifecycle |
