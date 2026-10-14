# flights-speccer

The agent that writes the ideal spec for work the codebase has no pattern for: the version past the competent default that buys the most worth per token spent building and keeping it. It reads, researches and decides; it writes one file, `spec.md` in the flight directory, and builds nothing. A `flights-foreman` builds from that file, cold.

## Contents

- [When it fires](#when-it-fires)
- [Why a context of its own](#why-a-context-of-its-own)
- [Cost per worth leads](#cost-per-worth-leads)
- [The flavour it carries](#the-flavour-it-carries)
- [How it learns what it decides on](#how-it-learns-what-it-decides-on)
- [spec.md and its readers](#specmd-and-its-readers)
- [Return](#return)
- [Settings](#settings)
- [The GPT twin](#the-gpt-twin)
- [Not a task-file planner](#not-a-task-file-planner)

## When it fires

- The root foreman's spawn before its base gate, when a closest-pattern search (its own grep or one `tracer` question) finds none to copy (a new subsystem, an integration with a system nothing here talks to, a layout or test suite the quality laws would design from scratch) and `run.md` holds no `SPEC` line, the recorded return of an earlier speccer; a `spec.md` file alone, which a cut or blocked run leaves behind, never skips it. The foreman writes `briefs/speccer-r{round}.md` and the speccer returns to `returns/speccer-r{round}.md`, as a child does. A child foreman never fires one: it builds against the root's frozen interface.
- Any chat, directly, for a spec without the build; the fleet prompt's ladder names it on the `flights-foreman` rung.
- With an existing `spec.md` and a reason (a foreman's finding, a ruling): it revises the reason's sections and every section whose facts depend on them, keeps the rest, and saves the whole document. A `D` the code refuses reaches it this way, by `SendMessage` from the foreman that spawned it.

Work with a pattern to copy skips it: a fifth door on an existing alerter copies the four, and an ideal spec of it costs more than it buys. Requirements the user has not settled go to `/flights:spec` first, which grills the user; the speccer decides only what the rulings leave open.

## Why a context of its own

Novel work needs research, a dependency map both ways, the quality laws loaded and a conception pushed past the default. Carried by the foreman, all of that rides every build call that follows; held by a separate context, it is paid once and handed over as one file. A wrong design of something new costs a rebuild, so its decisions are made before the first edit, with their rejected alternatives on record. The foreman still reads every line it changes: the spec carries decisions, never code.

## Cost per worth leads

Every open decision is weighed on every axis it touches, and the first axis is the tokens to build and keep it against the worth it buys. The rest follow: correctness, failure visibility, security and privacy, data lifecycle, operability, performance and cost, testability, maintainability by an LLM, reversibility, blast radius.

Each phase and element is priced: the calls and tokens to build, test and land it, what keeping it costs (maintenance, the runtime spend of any LLM call it adds), and its worth (the failure class it closes, the moment it creates, the old path it removes, the risk it retires). The price is calibrated against comparable landed flights through the `/chatlog` skill: `pfm tokens --flight {dir} --metrics-out {flight directory}/calibration/{that flight}.md` for a flight's tokens, so the landed flight's own `metrics.md` is never rewritten, and `pfm model-cost --all --json` for the prices. An element whose tokens outrun its worth is cut or cheapened, with its decision line; the boldest element earns its price or is replaced. A calibration that would not run is named and the estimate marked uncalibrated, never shown as measured; a price that could not be read is named, and no USD total is invented.

## The flavour it carries

The perfectionist's habits of the fleet's most ambitious specs, held to engineering:

- One boldest element, and the whole spec organised around it.
- The idea comes out of the raw material (the code, real data, the incident), never a summary of it.
- A leap in kind past the competent default, which is named once and left. Rigour is the floor, never the idea: a spec that is only safe and provable is the default.
- The finish counts: `## How it is met` walks the user's or operator's first second, first minute, second look and the bad day it was built for.
- Shown running early: a spec too big for one flight splits into phases, each its own flight, the first ending at something the user can see working.
- Every fork closed, the user's kept the user's: every open technical fork is decided; one only the user can settle (scope, money, an outward-facing or legal step) returns `BLOCKED` with its question, unless the brief carries the user's authority to decide forks (a "god speed" ruling), and only then is decided and marked `USER`.
- Ambition goes deeper, never wider: scope stays the request's, fixed values bind as given, and the target's real limits bind.
- Bold work, plain return: seven fixed lines.

## How it learns what it decides on

- Code it has not read goes to `tracer` as numbered questions in the build's terms; a `NOT READ` is re-sent or read, never a fact.
- Research goes out one question per part nobody here built, dependency choice or tricky edge, the tier rising with what a wrong answer would cost: `rr`, `rr-pro`, `rr-pro-max`; `clone-distiller` where a named project's code holds the prior art. Each `rr`-family brief carries `RR-DIR: {flight directory}/research` and each `clone-distiller` brief `OUT-DIR: {flight directory}/research`, so no report lands in the checkout. A candidate dependency is compared on maintenance, licence, security record, footprint and fit; one already in the tree wins a tie.
- The quality laws the spec puts in scope load through `Skill`: `quality:llm-codebase` for a new directory, file family or LLM call; `quality:integration-suite` for a new test tier, harness or live lane; the prompt, doc, description and orientation-file laws where those are touched. An owner input a loaded law asks for that the rulings leave open is a user fork, settled as above; a law's `DRAFT` or unruled status is kept, never promoted.
- A final pass reads the draft as its harshest reviewer and as the foreman who must build it cold, until a whole pass finds only polish.

## spec.md and its readers

| Section | Read by |
| --- | --- |
| `## Order`, `## Requirements`, `## Decisions` | the foreman, as rulings; its design file (`design.md`, a child's `design-{unit}.md`) cites the `D` ids, and each child's brief carries the `spec.md` path and the `D` ids its rows rest on |
| `## Phases` | the caller, which runs one foreman and landing per phase; the foreman builds the phase its brief names, else the first, and its return names the next |
| `## Layout`, `## Tests` | the foreman, for the homes and the tiers |
| `## Dependencies`, `## Connectivity`, `## What the material holds` | the foreman, as evidence for its door list |
| `## Removal` | the foreman, for what to remove and the searches that prove it gone |
| `## Boundaries`, `## Standing rules` | the foreman, and each child through its brief |
| `## The idea`, `## Boldest element`, `## How it is met` | the user, and the foreman for intent |
| `## Cost and worth`, `## Research`, `## Open` | the user and the caller deciding whether to build |

Requirement rows name their owning build unit and carry a real-valued example; a `requirements.md` row travels verbatim with its number, so a flight with a `spec.md` reads it, never `requirements.md`, as its statement of intent. `## Order` holds the request as received, the user's own words only where the brief quotes them. Exact text appears only where copied from code, the user or a run's output; a new contract (a name, key, signature, literal or example value) is proposed and marked new. `## Phases` exists only for work larger than one flight.

## Return

Seven lines, nothing around them: `SPEC {path}`, `PARTIAL {path}: {what is missing}` or `BLOCKED {flight}: {question}`; then `BOLDEST`, `ROWS` (requirements, decisions, the `USER` ids, phases), `COST` (build tokens, USD or the models whose price could not be read, cut and cheapened counts, calibrated or not), `RESEARCH`, `LAWS`, `OPEN`. The foreman builds from a `SPEC` return and carries the path, the `USER` ids, the next phase and the `COST` line up on its own return's `SPEC` line. A `PARTIAL` or `BLOCKED` return stops the build: the foreman returns `BLOCKED {flight}` as its first line, with the speccer's missing part or question, nothing built.

## Settings

| Setting | Value |
| --- | --- |
| Model | `opus`, effort `xhigh` |
| Tools | `Read`, `Write`, `Edit`, `Bash`, `Glob`, `Grep`, `Agent`, `Skill`, the compaction tool |
| `autoCompact` | forced at 300k, nudges from 120k every 60k |
| Hook | `orchestrator-wait` on `Bash`: a call that only waits is denied |
| Cap | 150 calls, a spawn's calls its own; past it, `PARTIAL` with the rest in `## Open` |

## The GPT twin

`gpt-flights-speccer` derives from `flights-speccer` on `gpt-6.1-sol[1m]` at effort `xhigh`, with a return law of its own prepended: it writes `spec.md` and the research and calibration files its manual names, never the builders' `run.md`, briefs, returns or gate files (see [GPT twins](../gpt-twins/gpt-twins.md)). It renames nothing, so its `tracer`, `rr` and `clone-distiller` spawns stay Claude agents, and it keeps its original's visibility, so any chat may spawn it. `gpt-flights-foreman` reaches it through its rename of `flights-speccer`.

## Not a task-file planner

The name once belonged to a planner that wrote executor task files and an index; that role was removed when the reader became the builder ([why](flights-foreman.md#why-one-agent-reads-and-builds)). This one writes no task file, briefs no executor, and fires only where there is nothing in the codebase to read a pattern from.
