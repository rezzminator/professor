# The general executors

`general-mechanical-executor` and `general-smart-executor` are the hands of a [`general-orchestrator`](general-orchestrator.md): one fresh agent per task, briefed inline, which makes the change, proves it and returns once. One body, two tiers, on the same pattern as the [flight executors](../flights/flights-executors.md).

Decisions live in this file. The executable wording lives in `templates/global/agents/general-mechanical-executor.md`.

## Contents

- [Why not the flight executors](#why-not-the-flight-executors)
- [Two tiers, one source](#two-tiers-one-source)
- [What the brief carries](#what-the-brief-carries)
- [What the body holds](#what-the-body-holds)
- [Tests](#tests)
- [The cap and the handoff](#the-cap-and-the-handoff)
- [The return](#the-return)
- [Codex and OpenCode](#codex-and-opencode)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Why not the flight executors

A flight executor is bound to a flight's contract: a brief file, a task file with `Done when` rows, the `run.md` lines of its needs. A general task has none of them; its whole spec is the inline brief. Reusing the flight body would carry that contract as dead text into every general spawn, and every general brief would have to override it. The law both share (the Goal wins, a red is read to its cause, stay inside the files, report once) is carried by each body: a sub-agent never receives the fleet prompt — it runs on its own agent body plus the project's `CLAUDE.md`.

## Two tiers, one source

| Agent | Model | Effort | Runs |
| --- | --- | --- | --- |
| `general-mechanical-executor` | `sonnet` | `medium` | a task rated `mechanical` |
| `general-smart-executor` | `opus` | `medium` | a task rated `smart` |

The body exists once, in `general-mechanical-executor.md`. `templates/global/agents/variants.json` declares `general-smart-executor` as a variant `from` it, overriding `model` and `description`; `pfm install` renders it and links it into the engine registries, the road `flights-smart-executor` and `super-rr` take. The orchestrator picks the agent type by the task's rating and passes no model override.

## What the brief carries

The task's goal and returned artifact, its files and what is not its, the exact change (for a `smart` task, the judgment it owns and its bounds), the check that proves it, the testing manual path when code changes, the standing rules, and what already landed that it needs. Nothing the body holds.

## What the body holds

Everything true for every task:

- the first move: open every file the brief names in one message;
- the hand law: the Goal wins over a detail; a premise that does not hold returns `SPEC-DRIFT` with nothing changed; a decision it cannot make is asked for as `BLOCKED`; a red it did not foresee is read until its cause is named, never rerun and never fixed outside its files;
- read discipline: find the lines with a search and read that range; never a whole file to find a place, never a file still in context; a log through `tail` or a search;
- stay inside the brief's files; a needed change outside them is reported, never made; git is read-only;
- run the brief's check and the affected tests only; the full suite, a review and a format sweep are never an executor's;
- waiting is one call sized to the command, never a poll chain;
- the 45-call cap and the handoff;
- the return format, and no progress message, diff or log in a message.

## Tests

When the task changes behavior, the executor writes the covering test in the project's pattern, per the testing manual the brief names, and accepts it only after watching it fail against the unfixed code or a deliberate re-break. A task that changes no behavior (a doc, a rename a build proves, a moved file) proves itself with the brief's check alone.

## The cap and the handoff

45 calls, one model request each, however many tools it carries at once. Past the cap the executor stops and returns `FAILED {id}: cap` with a handoff: the files changed, what landed and was proven, what is left, and the next step. The orchestrator gives the handoff to a fresh executor of the same rating; a second cap on one task sends the task back to be re-cut. An executor never dispatches: it holds no `Agent` tool.

## The return

First line `DONE {id}`, `FAILED {id}: {why}`, `SPEC-DRIFT {id}: {what}` or `BLOCKED {id}: {question}`. Then the files changed, the check's verdict line as printed, the test and its watched-failing proof when one was written, what it adapted, what it found outside its files, and last `RETRO {lesson}` or `RETRO none`: one line of at most 200 characters naming a fact about the environment, the tooling or the project law that cost it calls, with the working alternative.

## Codex and OpenCode

`pfm codex agents` and `pfm opencode build` compile both agents like every global role; the variant renders on the same road as the flight executors'. On Codex the allowlist is not enforced per role, so the cap and the read discipline are the savings there.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/general-mechanical-executor.md`, `templates/global/agents/variants.json` | The executable wording; the smart tier's frontmatter |
| The orchestrator | [`general-orchestrator`](general-orchestrator.md) | The brief, the verification, the reaction to a cap |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` | The 45-call law and the ladder, for a main chat acting as a hand |
| The project contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The ladder and the 45-call cap, for every sub-agent |
