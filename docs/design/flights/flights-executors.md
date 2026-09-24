# The flight executors

`flights-mechanical-executor` and `flights-smart-executor` are the hands of a flight: one fresh agent per task file, picked by the task's rating, which writes the code and its covering tests and returns once. One body, two tiers. They replace the per-project `developer` and `qa` agents inside a flight, and they carry the instructions that task files and `0-` shared files used to restate for every executor.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-mechanical-executor.md`](../../../templates/global/agents/flights-mechanical-executor.md).

## Contents

- [Why it exists](#why-it-exists)
- [Two tiers, one source](#two-tiers-one-source)
- [What it holds](#what-it-holds)
- [Tests](#tests)
- [Layout laws at write time](#layout-laws-at-write-time)
- [Context budget](#context-budget)
- [The cap](#the-cap)
- [What it no longer does](#what-it-no-longer-does)
- [The return](#the-return)
- [Codex and OpenCode](#codex-and-opencode)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Evidence](#evidence)

## Why it exists

A flight had a speccer and an orchestrator and no executor of its own. The executor was whatever agent type the project had, and the instructions of a developer travelled in the spec: a measured 4.3 KB of generic instruction per executor in one flight and 12.3 KB in another, rewritten on every revising round. The orchestrator's brief also had to override the project's agent card ("write the covering tests yourself, whatever your agent card says"). One global body ends both: the generic instructions live once, in the agent, and a task file holds only the task.

## Two tiers, one source

| Agent | Model | Effort | Runs |
| --- | --- | --- | --- |
| `flights-mechanical-executor` | `sonnet` | `medium` | a task rated `mechanical` |
| `flights-smart-executor` | `opus` | `medium` | a task rated `smart` |

The body exists once, in `flights-mechanical-executor.md`. `templates/global/agents/variants.json` declares `flights-smart-executor` as a variant `from` it, overriding `model` and `description`; `pfm install` renders the variant into pfm's generated directory and links it into the engine registries, the same road `super-rr` takes. The orchestrator picks the agent type by the index row's `rating` and passes no model override, so the tier is a registry fact, visible in a transcript's `agentType`.

## What it holds

Everything that is true for every task of every flight:

- the first move: open the task file and its `reads` together, in one message;
- the Goal wins over a detail; a premise that does not hold returns `SPEC-DRIFT` with nothing changed; a decision it cannot make is asked for;
- a red it did not foresee is read until its cause is named — the line, the value, the code path — and returned as `FAILED` or `SPEC-DRIFT` with that cause, or with what was read and "cause unknown"; a rerun and a fix outside the spec are forbidden, reading never is;
- read discipline: find the lines with a search, read that range; never a whole file to find a place, never again a file still in context; a log through `tail` or a search, never whole; the project contract is already in context and is never read;
- waiting is one call sized to the command's duration, never a poll chain;
- it stays inside the task's `Files`; git is read-only;
- the return format and the ban on progress messages, diffs and logs in a message.
- the rules a task file used to carry as fixed lines: how a `Done when` row is proven, and the `Progress dependency` check before step 1.

What it does not hold: anything about one project. Project law reaches it through the project contract the harness injects and through the project's [testing manual](testing-manual.md).

## Tests

The executor writes the covering tests itself, one per `Done when` row, in the project's pattern:

- before the first test it opens the project's testing manual, the path named in the orchestrator's brief, and follows its tiers, test home, lane or registry duty, mock boundary, run commands and traps;
- a test is accepted only after it was watched failing against the unfixed code, or against a deliberate re-break when the fix already landed;
- it runs only the affected tests plus the type check and lint of its own files — the full suite is the lander's;
- a test that exists but did not run is missing; when a test and a row disagree the code is wrong, never the row.

Bias of an author testing its own code is real and accepted here: the executor's tests prove the rows, and the independent attack is [`flights-lander`](flights-lander.md)'s.

## Layout laws at write time

`/quality:llm-codebase` is a design command; the laws of it that bind at the moment of writing a file live in the executor, so the speccer does not carry them:

- search for the concept before creating a file or a function; reuse what exists, never a second implementation under another name;
- one canonical term per concept, the one the code already uses, identical in file name, identifier, wire key, environment variable and test name;
- call the project's façade for a cross-cutting mechanism (process execution, database open, file write, environment, clock, LLM invoke, logging, the test scratch root), never the primitive;
- no directory named by negation (`utils`, `helpers`, `common`, `misc`, `shared`): a new file sits with the unit that changes with it;
- a source file over the project's size ceiling is split before logic is added to it;
- a runner, parser or census script the task needs is a versioned script under the project's `scripts/`, never a private copy;
- one test home per source file, every temp path through the project's scratch-root helper.

The design-time laws (unit of change, anatomy, registries, cross-project alignment) stay with [`flights-speccer`](flights-speccer.md).

## Context budget

Cost is calls times context, and an executor's starting context is re-sent on every call.

- `tools: Read, Write, Edit, Bash, Glob, Grep` — no `Skill`, no `Agent`, no MCP tool. Listing `Skill` injects the skills listing, a measured 7.4K tokens per call; an MCP tool outside the allowlist costs nothing.
- The agent body stays under 5 KB.
- The brief shrinks to the task file path, its `reads`, the pasted `run.md` lines of its `needs`, the standing rules, the testing-manual path and the worktree. Everything else the old brief restated is in the agent.
- The project contract is injected by the harness into every sub-agent and no setting stops it; its size is the project's to keep small.

## The cap

80 tool calls. Past it the executor stops and returns `FAILED {id}: cap` with the handoff: what landed, what is left, the next step. The number is in the agent, not in the brief; the speccer sizes tasks to it (a task that needs 150 calls is three tasks). The measured healthy band is 40 to 80 calls; the runaway executors of the audited flights ran 135 to 254.

## What it no longer does

- No review and no `Skill` tool: `/code-review` runs once per flight, in the lander, over the flight's whole diff. The per-executor review was the largest single defect of the audited flights: 67 review sessions, 182M tokens.
- No full-suite run, no format or lint sweep beyond its own files.
- No conformance report: a drift from the spec surfaces as the next task's `SPEC-DRIFT`.

## The return

First line `DONE {id}`, `FAILED {id}: {why}`, `SPEC-DRIFT {id}: {what}` or `BLOCKED {id}: {question}`. Then: files changed; the test that covers each `Done when` row and the proof it ran and was watched failing; what it adapted; defects found outside its files; what it could not reach. Last, `RETRO {lesson}` or `RETRO none`: one line, at most 200 characters, a fact about the environment, the tooling, the project law or the testing manual that cost it calls and would cost the next agent the same, with the working alternative; the orchestrator records it, deduplicates it and carries it to later briefs and to the user ([Retro lines](flights-orchestrator.md#retro-lines)).

## Codex and OpenCode

`pfm codex agents` and `pfm opencode build` compile both agents like every global role. On Codex a role cannot restrict tools or MCP servers — those settings are global — so the allowlist is a Claude saving only; the cap, the read discipline and the single-wait rule are the Codex savings, and the wait recipe itself lives in the Codex part of the fleet prompt.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-mechanical-executor.md`, `templates/global/agents/variants.json` | The executable wording; the smart tier's frontmatter |
| The orchestrator | [`flights-orchestrator`](flights-orchestrator.md) | The brief, the verification of a `DONE`, the agent type by rating |
| The speccer | [`flights-speccer`](flights-speccer.md) | Task size against the cap; test tier and home in `Done when` and `Files` |
| The testing manual | [`testing-manual`](testing-manual.md) | The project's test law the executor follows |
| The lander | [`flights-lander`](flights-lander.md) | The review and the full suite the executor no longer runs |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | A review inside an executor is a caller rule the manual refuses; the lander is the flight's one review. It carries no executor law: executors and executor seats run on the agent body |

## Evidence

- Spend audit of four flights: 94% of 770M tokens in executors; poll calls 28%, review sessions 182M, re-reads 2,214 of 6,653 read commands, 427 reads of the project contract already in context.
- Starting context by agent type, from 300 transcripts: the skills listing arrives only with `Skill` in the allowlist.
- Of 17 lifecycle frameworks surveyed, 13 have the implementer write its own tests or nobody.
