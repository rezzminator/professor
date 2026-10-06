# Flights

A flight is one request, built and landed. `/flights:spec` settles the requirements with the user when they are not settled; [`flights-foreman`](flights-foreman.md) reads, designs what no ruling decided, builds, splits only at ownership boundaries, and lands the flight through one [`flights-lander`](flights-lander.md). This file holds the decisions shared by the whole family; each member's own decisions live in its file.

A change lands in the design doc first, then in the template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [The lifecycle](#the-lifecycle)
- [The flight directory](#the-flight-directory)
- [Verdict tokens](#verdict-tokens)
- [Two containers](#two-containers)
- [Where each rule lives](#where-each-rule-lives)
- [Names](#names)
- [Harness settings the family needs](#harness-settings-the-family-needs)
- [What the family replaced](#what-the-family-replaced)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `flights-foreman` | agent | Builds one request with or without a spec: base gate, doors, data shapes, decision, build; freezes the interface and spawns one child foreman per other build unit or unrelated problem; verifies their returns; lands through the lander | `opus`, effort `high` |
| `flights-lander` | agent | The landing's gate, one per flight: every project's gate, one review of the whole diff across every project, adversarial tests, its own fixes; the testing manuals' floors are its gate rows | `opus`, effort `high` |
| `flights-mechanical-executor` | agent | One bulk edit a foreman fully fixed (a rename across many files), from a task file the foreman writes | `sonnet` |
| `/flights:init` | command | Readies a project for flights, once: maps its build units inside-out, writes the project map, and gives each unit a testing manual, a test command and a static-check command | the main chat |
| `/flights:spec` | command | The human front: maps only what asking needs, grills the user until no gap is left, writes `requirements.md`, hands it to `flights-foreman` | the main chat |
| `/flights:orchestrate-cross-harness` | command | The main chat acts as the root foreman; child foremen run as chat seats of another engine | the main chat |
| `/flights:audit` | command | The skeptic over a flight, running or landed: every claim against its artifact | the main chat |

Three agents, four commands. Project law reaches them through the project contract and the project's [testing manual](testing-manual.md); `gitter` is the fleet's own.

## The lifecycle

1. Ready. `/flights:init`, once per project: the project map, and each unit's testing manual, test command and static-check command. Re-run, it builds only what is missing and aligns what is stale.
2. Specify, when the requirements are not settled. `/flights:spec` grills and writes `requirements.md`: the binding rulings and the requirement rows, each row testable. A model caller with settled work skips it and hands the foreman the request.
3. Build. One root `flights-foreman`: the base gate, the doors, the data shapes, the decision; the interface frozen in the innermost unit; the producer built by the foreman that read it; one child foreman per other unit, in parallel against the interface; every return verified; the owner repairs its own reds.
4. Land. One `flights-lander` for the whole flight, then, when the caller ordered it, the commit and, for a worktree flight, the merge through the repository's git writer, the lander's `PASS` or `FIXED` being the merge nod.
5. Audit. `/flights:audit` at any time, by the user: it believes `run.md`, git, the transcripts and the checks, never a message.

## The flight directory

```text
$HOME/.local/state/pfm/flights/{project}/{flight}/
  requirements.md the binding rulings and requirement rows   written by /flights:spec
  run.md          the ledger of the run                      written by the root flights-foreman
  agents.tsv      one row per spawn: unit, type, agent id, round, time, engine   appended by the root flights-foreman
  briefs/         one brief file per spawn                   written by the spawning foreman
  tasks/          one task file per mechanical bulk edit     written by the foreman that fixed it
  returns/        one return file per child foreman (sub-agent or seat) and per gate round   written by each child and by flights-lander
  metrics.md      per-agent calls, context, tokens, price    written by token-audit.mjs at landing
  gate.md         the gate's attack map and findings         written by flights-lander
  REVIEW.md       the merge-gating report, from gate.md      written by the root flights-foreman
  audit.md        the last audit's report                    written by /flights:audit
```

- The directory lives outside the repo, under `$HOME/.local/state/pfm/flights/{project}/` (`{project}` = the repo directory's basename, leading dot stripped), kept across reboots; the audit reads it after the landing.
- Beside the project's flight directories sits `project-map.md`: the project's static facts — its build units in inside-out dependency order, where the shared contract lives and what it generates, each unit's testing manual path and gates, test homes, hot files. `/flights:init` writes it; a foreman reads it at intake and reports a line it finds wrong. The code wins over it.
- Each writer owns its own files; nobody edits another writer's file.

## Verdict tokens

| Token | Written by | Means |
| --- | --- | --- |
| `CLAIMED` | the spawning foreman, at spawn | A child or executor holds this unit or task |
| `DONE` | child → parent, after verification | The unit's requirement rows are met and proven |
| `PARTIAL` | root foreman | Some units `DONE`, the rest named with their cause |
| `FAILED` | any builder | The goal was not reached; the return names the cause or what was read |
| `SPEC-DRIFT` | `flights-mechanical-executor` | Its task file contradicts the tree; nothing guessed |
| `BLOCKED` | any foreman | A question only the user can answer, or a ruling contradicted; both sides quoted |
| `COMA` | the root, cross-harness only | A seat whose turn a model-server error stopped, re-prompted |

A return opens with its token on the first line; the rest is evidence.

## Two containers

| | Nested (default) | Cross-harness (`/flights:orchestrate-cross-harness`) |
| --- | --- | --- |
| The root foreman | a `flights-foreman` sub-agent | the main chat, reading the foreman's body |
| A child foreman | `Agent(subagent_type: "flights-foreman")` | a seat of the chosen engine, named `{flight}-{unit}`, born with the `flights-foreman` role through `pfm chat new --agent-role`, its brief as the first turn via `--prompt-file` |
| Wait | end the message; each return wakes the parent | a Monitor on `returns/`; the seat's inject is a bonus |
| The lander | a sub-agent of the root | the same: a sub-agent of the chat, never a seat |

## Where each rule lives

| Layer | Reader | Holds |
| --- | --- | --- |
| `pfm/harness-prompts/share/tail.md` § Orchestration | every main chat, chat seats included | Cost = calls × context; one context per build unit with compaction for length; the ladder (direct, `flights-foreman`, `/flights:spec` first when requirements are open); a foreman reports once and waits for its children |
| `CLAUDE.md` / `AGENTS.md` | every sub-agent and seat | The project contract |
| The agent file | the agent | The protocol of one role |
| The brief | one child or executor | Its requirement rows, the frozen interface's paths, acceptance, the testing manual's path, the worktree, the flight directory |

## Names

- The unit of work is a flight; the family is `flights`; agent names carry no namespace (`flights-foreman`), commands carry it as `/flights:{verb}`.

## Harness settings the family needs

Claude Code stops the Agent tool three levels below the main chat by default and caps concurrent sub-agents. A flight is main → root foreman → child foreman → mechanical executor, or main → root foreman → lander → its review fork. `pfm` writes `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` and `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` onto every Claude Code launch line from its settings; neither ceiling is reported when hit, so the root counts its children in flight.

## What the family replaced

| Retired | Replaced by |
| --- | --- |
| `flights-speccer` (task files, index, nested planners) | the foreman reads and builds; `/flights:spec` writes `requirements.md` |
| `flights-orchestrator`, `/flights:orchestrate-nested`, `/flights:orchestrate-live` | the root foreman; nested is the default container |
| `flights-smart-executor`, `flights-precise-executor` | the foreman builds what it read |
| `general-orchestrator`, `general-foreman`, `general-executor` | `flights-foreman` with no spec; a batch of unrelated problems is one child per problem; bulk fixed edits go to `flights-mechanical-executor` |
| `flight-index.mjs` | none: no task-file index exists |
| `speccer-manual.md` | `project-map.md` |
| Earlier: `/wave:*`, `scheduler`, `architect`, `speccer` | the flights family |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/flights-foreman.md`, `flights-lander.md`, `flights-mechanical-executor.md` | The protocols |
| The commands | `templates/global/commands/flights/*.md` | The four commands, machine-global |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The universal laws, the family's names |
| The adopter contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The ladder |
| The engine | `pfm` settings and launcher | The two harness settings |
| This directory | `docs/design/flights/` | One design file per member; this file for what they share |

## Open items

- The multi-project form is unmeasured: the first cross-project flights measure calls per unit, the re-read share and the lander's findings.
- The caps (250 calls per foreman, 200 per lander) and the `autoCompact` points are first values; tune them from callmeter peaks.
