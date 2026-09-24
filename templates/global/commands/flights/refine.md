---
name: flights:refine
description: 'Grill a written flight — /flights:refine [directory] [what to change], default the newest under $HOME/.local/state/pfm/flights/{project}/: numbered question rounds, a recommended answer each, until nothing is assumed; the rulings go to flights-speccer as a revising call. /flights:spec → here → /flights:orchestrate-{nested|live|cross-harness}. Returns the revised index.'
argument-hint: '[flight directory] [what to change]'
---

# Refine — grill a flight until nothing is assumed

One deliverable, written by `flights-speccer` and never by you: the flight directory revised so every decision in it is one the user made or confirmed. You read, grill, hand off and present. Input: $ARGUMENTS — a flight directory, what the user wants changed, both, or neither. Directory absent: the newest under `$HOME/.local/state/pfm/flights/{project}/`; it must hold `index.md`. Nothing to change named: the whole flight is the subject.

## F1 — Read the flight

Read `index.md`; extract the `Goal` and `Decisions` sections of every task file in one call, never the files opened whole. With a `run.md`: its `DONE` ids and what landed are settled ground, and a `CLAIMED` id without a verdict is out of reach (its executor has read the file); name both in round one. Build the design tree: the root is the flight's goal, each branch a decision the index or a task file rests on, each leaf a decision that hangs off another. A decision the user already ruled on is settled; one the speccer made on its own is open until the user confirms it.

## F2 — Grill

Work the tree in rounds. The frontier is every open decision whose prerequisites are settled: a question whose answer depends on another question still open in this round waits for a later round. Round one always holds the scope boundary (the user's objective restated, what the flight includes, what it defers) and what the user asked to change. Ask the whole frontier in one plain-text message, then end it and wait:

```
**Q1 — {title}**: {the question, its context and its options}
Recommended: {your answer and the one reason}

**Q2 — {title}**: …
```

Each answer reshapes the tree: settled decisions push the frontier outward. Recompute it and ask the next round. A task the user drops or defers is a ruling like any other.

Facts are yours to find, never the user's: a question that needs a fact from the code goes to a probe (`Agent(subagent_type: "tracer")` for a question, `mapper` for a whole area). A probe running is an unsettled prerequisite: only the questions downstream of it wait; ask the rest of the frontier now. Decisions are the user's: put each one and wait.

The grill ends when the frontier is empty: every branch visited, nothing silently assumed. Restate the rulings as one numbered list and hand off only once the user confirms it is the shared understanding.

## F3 — Hand off

Spawn `Agent(subagent_type: "flights-speccer", model: "opus")` as a revising call: the directory, the reason (`refinement`), the confirmed rulings as binding decisions, the maps the probes returned, and, with a `run.md`, the completed ids with what landed and every `CLAIMED` id. End your message; the return arrives with the revised index.

A `BLOCKED` item in the return: one more round holding its question alone, the answer sent to the same `flights-speccer` by `SendMessage`. A `BLOCKED` item still in that return stays `BLOCKED` in the presentation.

## F4 — Present

The index table as returned; one line per changed id (its `NOTES` line) naming the ruling it now carries; any `BLOCKED` item. Then the run: with a `run.md`, `/flights:orchestrate-nested {directory} revised {changed ids}`; without one, the three ways from `/flights:spec` S5. Approval is a reading, not a run; the run is the user's next command.
