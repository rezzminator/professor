# general-executor

`general-executor` is the hand of a [`general-foreman`](general-foreman.md), and of [`general-orchestrator`](general-orchestrator.md) for a change its caller already decided: one fresh agent per decided change, briefed inline, which builds the change, proves it and returns once. Only the general family spawns it (`GENERAL-ONLY`).

Decisions live in this file. The executable wording lives in `templates/global/agents/general-executor.md`.

## Contents

- [One tier](#one-tier)
- [Why not the flight executors](#why-not-the-flight-executors)
- [What the brief carries](#what-the-brief-carries)
- [What the body holds](#what-the-body-holds)
- [Tests](#tests)
- [The cap and the handoff](#the-cap-and-the-handoff)
- [The return](#the-return)
- [Codex and OpenCode](#codex-and-opencode)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## One tier

The general family has one executor tier, mechanical: `claude-sonnet-5-5` at effort `high` on Claude (Sonnet 5.5 at `high` scored 83.5 against `medium`'s 74.5 on real landed tasks, blind-judged), `gpt-6-sol` at `low` on Codex. It is a plain agent file, not a `variants.json` variant.

The foreman carries the judgment: it has dug to the cause and decided the change before it briefs, so what reaches the executor is code the decision fixes line by line. A second, judging tier would reopen a decision the foreman closed. The orchestrator sends it a change only when the orchestrator's caller already stated the files and the exact edit, so no judgment reaches it from there either; a change that proves undecided returns `SPEC-DRIFT` and goes to a foreman. Work that still holds a judgment stays with the foreman, which builds it itself or decides again.

## Why not the flight executors

A flight executor is bound to a flight's contract: a brief file, a task file with `Done when` rows, the `run.md` lines of its needs. A general change has none of them; its whole spec is the foreman's inline brief. The law both share (the Goal wins, a red is read to its cause, stay inside the files, report once) is carried by each body: a sub-agent never receives the fleet prompt — it runs on its own agent body plus the project's `CLAUDE.md`.

## What the brief carries

The change's goal in one sentence; its files and what is not its; the exact change — symbols, lines, every door by `path:line`, every interface pinned; the check that proves it and the testing manual path when code changes; the standing rules, the worktree and what already landed. Nothing the body holds.

## What the body holds

Everything true for every change:

- the first move: open every file the brief names in one message;
- the hand law: the decision is the foreman's; the Goal wins over a detail; a premise that does not hold returns `SPEC-DRIFT` with nothing changed; a decision it cannot make is asked for as `BLOCKED`; a red it did not foresee is read until its cause is named — a cause in its own files is fixed and rerun, a cause outside them is reported with every other outside cause at once;
- read discipline: find the lines with a search and read that range; never a whole file to find a place, never a file still in context; a log through `tail` or a search;
- a call carries all the work it can: independent reads, searches and commands in one message, dependent steps that need no judgment chained into one shell call, every glob quoted (an unquoted `--include=*.go` fails under zsh before the search runs). The fleet prompt's § Command execution never reaches a sub-agent, and without the line every family member measured about 1.1 tool uses per request on the bench;
- stay inside the brief's files; a needed change outside them is reported, never made; git is read-only;
- run the brief's check and the affected tests only; the full suite, a review and a format sweep are never an executor's;
- waiting is one call sized to the command, never a poll chain;
- the 45-call cap and the handoff;
- the return format, and no progress message, diff or log in a message.

## Tests

When the change alters behaviour, the executor writes the covering test in the project's pattern, per the testing manual the brief names, and accepts it only after watching it fail against the unfixed code or a deliberate re-break. A change that alters no behaviour (a doc, a rename a build proves, a moved file) proves itself with the brief's check alone.

## The cap and the handoff

45 calls, one model request each, however many tools one carries. Past the cap the executor stops and returns `FAILED {id}: cap` with a handoff: the files changed, what landed and was proven, what is left, and the next step. Its caller re-briefs from the handoff; a foreman may finish the change itself, and the orchestrator hands it to a foreman. An executor never dispatches: it holds no `Agent` tool.

## The return

First line `DONE {id}`, `FAILED {id}: {why}`, `SPEC-DRIFT {id}: {what}` or `BLOCKED {id}: {question}`. Then the files changed, the check's verdict line as printed, the test and its watched-failing proof when one was written, what it adapted, what it found outside its files, and last `RETRO {lesson}` or `RETRO none`: one line of at most 200 characters naming a fact about the environment, the tooling or the project law that cost it calls, with the working alternative.

## Codex and OpenCode

`pfm codex agents` and `pfm opencode build` compile it like every global role; its `codex-model` and `codex-effort` pin the Codex role. On Codex the tool allowlist is not enforced per role, so the cap and the read discipline are the savings there.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/general-executor.md` | The executable wording and the tier's frontmatter |
| The foreman | [`general-foreman`](general-foreman.md), `templates/global/agents/general-foreman.md` | The brief, the verification, the reaction to a red or a cap |
| The orchestrator | [`general-orchestrator`](general-orchestrator.md), `templates/global/agents/general-orchestrator.md` | A change its caller already decided, passed on as stated; the reaction to `SPEC-DRIFT` |
| The Codex pin check | `pfm/internal/codexgen/globalagents_test.go` | The roles that carry a Codex role pin |
| The roster | `docs/BLUEPRINT.md`, `docs/SETUP.md`, `templates/project/commands/pcm.md`, `docs/design/flights/testing-manual.md` | The agent listed |
