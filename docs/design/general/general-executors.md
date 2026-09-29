# The general executors

`general-mechanical-executor`, `general-precise-executor` and `general-smart-executor` are the hands of a [`general-orchestrator`](general-orchestrator.md): one fresh agent per task, briefed inline, which makes the change, proves it and returns once. One body per tier, each the general twin of its [flight executor](../flights/flights-executors.md).

Decisions live in this file. The executable wording lives in `templates/global/agents/general-mechanical-executor.md`, `general-precise-executor.md` and `general-smart-executor.md`.

## Contents

- [Why not the flight executors](#why-not-the-flight-executors)
- [The tiers](#the-tiers)
- [What the brief carries](#what-the-brief-carries)
- [What the body holds](#what-the-body-holds)
- [Tests](#tests)
- [The cap and the handoff](#the-cap-and-the-handoff)
- [The return](#the-return)
- [Codex and OpenCode](#codex-and-opencode)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Why not the flight executors

A flight executor is bound to a flight's contract: a brief file, a task file with `Done when` rows, the `run.md` lines of its needs. A general task has none of them; its whole spec is the inline brief. Reusing the flight body would carry that contract as dead text into every general spawn, and every general brief would have to override it. The law both share (a red is read to its cause, stay inside the files, report once, and the tier's hand — the Goal wins on `precise` and `smart`, the listed adaptations only on `mechanical`) is carried by each body: a sub-agent never receives the fleet prompt — it runs on its own agent body plus the project's `CLAUDE.md`.

## The tiers

The three tiers, their models and efforts on Claude and Codex, and the measurements behind them are the one tier table in [`flights-executors`](../flights/flights-executors.md#the-tiers): a general task rated `mechanical`, `precise` or `smart` runs the same model and effort as a flight task of that rating.

Each tier has its own body — `general-mechanical-executor.md`, `general-precise-executor.md`, `general-smart-executor.md` — the general twin of its flights tier: the same rules, with the inline brief in place of the task file, the 45-call cap in place of 80, and the brief's check line in the return. What each tier's body adds, and the measurements behind it, live in the tier docs: [mechanical](../flights/mechanical-executor.md), [precise](../flights/precise-executor.md), [smart](../flights/smart-executor.md). A rule change in a flights body lands in its general twin in the same pass. The orchestrator picks the agent type by the task's rating and passes no model override.

## What the brief carries

The task's goal and returned artifact, its files and what is not its, the exact change (for a `precise` or `smart` task, the judgment it owns and its bounds), the check that proves it, the testing manual path when code changes, the standing rules, and what already landed that it needs. Nothing the body holds.

## What the body holds

Everything true for every task:

- the first move: open every file the brief names in one message;
- the hand law, per tier: on `precise` and `smart` the Goal wins over a detail; on `mechanical` only the listed adaptations (a quoted line found at another place, an import the edit needs, the formatter's output, a fix for its own red), anything else `SPEC-DRIFT`. On every tier a premise that does not hold returns `SPEC-DRIFT` with nothing changed; a decision it cannot make is asked for as `BLOCKED`; a red its own edit caused inside its files is iteration, fixed and rerun; any other red it did not foresee is read until its cause is named, never rerun unchanged and never fixed outside its files;
- read discipline: find the lines with a search and read that range; never a whole file to find a place, never a file still in context; a log through `tail` or a search;
- stay inside the brief's files; a needed change outside them is reported, never made; git is read-only;
- run the brief's check, the affected tests, the formatter, lint and type check of its own files, and the testing manual's static check when the brief names one; the full suite, a review and a whole-tree format sweep are never an executor's;
- waiting is one call sized to the command, never a poll chain;
- the 45-call cap and the handoff;
- the return format, and no progress message, diff or log in a message.

## Tests

When the task changes behavior, the executor writes the covering test in the project's pattern, per the testing manual the brief names, and accepts it only after watching it fail against the unfixed code or a deliberate re-break. A task that changes no behavior (a doc, a rename a build proves, a moved file) proves itself with the brief's check alone.

## The cap and the handoff

45 calls, one model request each, however many tools it carries at once. Past the cap the executor stops and returns `FAILED {id}: cap` with a handoff: the files changed, what landed and was proven, what is left, and the next step. The orchestrator gives the handoff to a fresh executor of the same rating; a second cap on one task sends the task back to be re-cut. A `smart` executor that estimates, once its design is stated, that the task passes the cap returns `SPEC-DRIFT {id}: too large` with the split it would make, nothing changed: the orchestrator cuts the task, and that return counts as no red toward the task's red limit. An executor never dispatches: it holds no `Agent` tool.

## The return

First line `DONE {id}`, `FAILED {id}: {why}`, `SPEC-DRIFT {id}: {what}` or `BLOCKED {id}: {question}`; nothing before it. Every message the orchestrator sends an executor after dispatch — a re-brief, a malformed-return bounce — ends with "continue, then return once more in the return shape". Then the files changed, the check's verdict line as printed, the test and its watched-failing proof when one was written, what it adapted, what it found outside its files, and last `RETRO {lesson}` or `RETRO none`: one line of at most 200 characters naming a fact about the environment, the tooling or the project law that cost it calls, with the working alternative.

## Codex and OpenCode

`pfm codex agents` and `pfm opencode build` compile the three agents like every global role. On Codex the allowlist is not enforced per role, so the cap and the read discipline are the savings there.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/general-{mechanical,precise,smart}-executor.md` | The executable wording, one body per tier, each the twin of its flights tier |
| The orchestrator | [`general-orchestrator`](general-orchestrator.md) | The brief, the verification, the reaction to a cap |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` | The 45-call law and the ladder, for a main chat acting as a hand |
| The project contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The ladder and the 45-call cap, for every sub-agent |
