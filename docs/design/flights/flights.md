# Flights

A flight is one spec directory, one orchestration and one landing. It is the unit of development work in this framework: a batch of tasks arrives, [`flights-speccer`](flights-speccer.md) turns it into task files and an index, [`flights-orchestrator`](flights-orchestrator.md) runs one fresh executor per task file, and the landing (checks, review, commit) closes it. This file holds the decisions shared by the whole family; each member's own decisions live in its file.

A change lands in the design doc first, then in the template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [The lifecycle](#the-lifecycle)
- [The flight directory](#the-flight-directory)
- [Verdict tokens](#verdict-tokens)
- [Three containers, one manual](#three-containers-one-manual)
- [Where each rule lives](#where-each-rule-lives)
- [Names](#names)
- [Harness settings the family needs](#harness-settings-the-family-needs)
- [What the family replaced](#what-the-family-replaced)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `flights-speccer` | agent | Turns one flight's work into task files and an index; rewrites the rest after a fault | apex (`fable`), effort `high`; a small flight's caller passes `model: "opus"` on the spawn |
| `flights-orchestrator` | agent | The one manual of running a flight: dispatch, wait, verify, react, land, return | spec-execution (`sonnet`), effort `high` |
| `flights-mechanical-executor`, `flights-hard-executor` | agents, one body | One task file each: the code and its covering tests in the project's test pattern; picked by the task's rating | `sonnet` and `opus`, effort `medium` |
| `flights-gater` | agent | The landing's first step, one per project: checks, one review of the whole diff, adversarial tests, its own fixes | frontier-judgment (`opus`), effort `high` |
| `/flights:spec` | command | The human front of specifying: maps the area, asks the user, hands `flights-speccer` the decisions, presents the index | the main chat |
| `/flights:orchestrate-nested` | command | Runs the flight in a `flights-orchestrator` sub-agent; the chat hears one return | the main chat spawns the agent |
| `/flights:orchestrate-live` | command | The main chat reads the manual and runs the flight itself, executors as sub-agents; the user watches and steers | the main chat |
| `/flights:orchestrate-cross-harness` | command | The main chat reads the manual and runs the flight with chat seats (Codex, OpenCode, Claude) as executors through the chat MCP | the main chat |
| `/flights:audit` | command | The skeptic over a flight, running or landed: every claim against its artifact | the main chat |

Five agents, five commands, nothing else. Project law reaches them through the project contract and the project's [testing manual](testing-manual.md); `gitter` is the fleet's own.

## The lifecycle

1. Specify. `/flights:spec` maps, asks, hands off; or a model caller with a batch hands the batch to `flights-speccer` without a human. Either way the output is a flight directory.
2. Orchestrate. One of the three `orchestrate-*` commands runs the manual over the directory: ready tasks dispatched together, each executor briefed with its task file, each return verified before it is recorded, faults sent back to `flights-speccer`, then the landing once: a `flights-gater` per project, the standing checks, the commit.
3. Audit. `/flights:audit` at any time, by the user: it believes `run.md`, git, the transcripts and the checks, never a message.

Specifying and running are two decisions. Approval of an index never starts a run; the user picks the container.

## The flight directory

```text
/tmp/{project}/flights/{flight}/
  index.md        one row per task              written by flights-speccer
  0-{topic}.md    shared content, when needed   written by flights-speccer
  {level}-{letter}.md  one task, one executor   written by flights-speccer
  run.md          the ledger of the run         written by flights-orchestrator
  agents.tsv      one row per spawn: task, type, agent id   appended by flights-orchestrator
  briefs/         one brief file per spawn      written by flights-orchestrator
  metrics.md      per-agent calls, context, tokens, price   written by token-audit.mjs at landing
  gate-{project}.md  the gate's attack map and findings  written by flights-gater
  audit.md        the last audit's report       written by /flights:audit
```

- The directory lives outside the repo, under `/tmp/{project}/` (`{project}` = the repo directory's basename, leading dot stripped). It is scratch with a reader: the run resumes from it, the audit reads it, and it dies with the branch.
- Four writers, one file each: `flights-speccer` writes the spec files and nothing else; the orchestrator writes `run.md`, appends `agents.tsv`, writes one file per spawn under `briefs/`, runs the script that writes `metrics.md`, and nothing else; each gater writes its `gate-{project}.md` and nothing else; the audit writes `audit.md` and nothing else. Nobody edits another writer's file. A task file changes only through a `flights-speccer` revising call.
- The `{flight}` name is short kebab-case chosen by whoever creates the directory: the user through `/flights:spec`, or the caller that hands a batch to `flights-speccer`.

## Verdict tokens

One vocabulary for the executor's return, the orchestrator's ledger and the audit, so a token is matched, never interpreted.

| Token | Written by | Means |
| --- | --- | --- |
| `CLAIMED` | orchestrator, at dispatch | An executor holds this task; a resume treats it as in flight until a verdict lands |
| `DONE` | executor → orchestrator, after verification | The Goal is reached and proven; the line names what was adapted, or `as specified` |
| `FAILED` | executor, or orchestrator after a second unproven return or a cap | The spec stands but the executor could not reach the Goal; the return names the cause or what was read; goes to `flights-speccer` with the executor's transcript, to be cut smaller or re-approached |
| `SPEC-DRIFT` | executor | The world moved or the spec contradicts itself; the executor changed nothing (or says what already landed) and names the cause or what it read; goes to `flights-speccer` with the executor's transcript |
| `BLOCKED` | executor or `flights-speccer` | A question only the user can answer; carried in the return, the other tasks continue |
| `STALE` | orchestrator, cross-harness only | A claimed seat silent past the bound; named, never auto-failed, never re-dispatched blind |

An executor's return opens with its token and id on the first line: `DONE 2-a`, `FAILED 2-a: {why}`, `SPEC-DRIFT 2-a: {what}`, `BLOCKED 2-a: {question}`. The orchestrator reads the first line; the rest is evidence.

The manual's `run.md` line names every token but `STALE`: only a seat can go stale, so the cross-harness container adds it as a substitution, and the manual never mentions it.

## Three containers, one manual

The manual is the `flights-orchestrator` agent body. It is written for the nested container and read by the other two, which substitute the transport and nothing else. The commands therefore stay thin: they name the substitutions and point at the agent file.

| Step in the manual | Nested (`/flights:orchestrate-nested`) | Live (`/flights:orchestrate-live`) | Cross-harness (`/flights:orchestrate-cross-harness`) |
| --- | --- | --- | --- |
| Who runs the loop | a `flights-orchestrator` sub-agent | the main chat, as the agent | the main chat, as the agent |
| Spawn an executor | `Agent(subagent_type, model)` | the same | a seat of the chosen engine, named `{flight}-{id}`, in the project directory or the worktree: `chat_new` with the engine and the directory; when the executor type is a registered role, the shell `pfm chat new … --agent-role {role}` instead, because the MCP verb carries no role |
| The model | `model:` from the executor type's pin, else the rating | the same | `chat_new`'s `model` and `effort`, in the engine's own names; unset, the engine's default |
| Deliver the brief | the spawn prompt | the same | `chat_inject` one message, the brief verbatim; the transport pastes any size. The brief closes with the way home: the seat writes its return to a file under `/tmp/` and sends it with `pfm chat inject {orchestrator} --file {path}`, because a seat's plain inject carries one line |
| Wait | end the message with one line and no tool call; the return arrives | the same | the same; the seat's report arrives as an inject into this chat |
| Verify a return | the return text plus `git diff {baseline} --stat -- {the index's files}` | the same | the same, plus `chat_last` when the inject was cut short |
| The gate | one `flights-gater` sub-agent per project at the landing; executors run no review | the same | the same: the gater is a sub-agent of the chat, never a seat |
| Liveness | the harness reports a stopped agent; a lost one is seen only when something else wakes the loop | the same; the user is the wake-up | `chat_status` once past the stale bound, on any wake-up; a full-screen pane capture judges from process evidence, never from rendered text |
| A spawn that does not happen | the harness reports nothing at its concurrency cap: the loop counts its in-flight executors and never exceeds the cap | the same | `chat_new` returns an error: no seat, no `CLAIMED` line; one retry, then the task holds and the return names it |
| The executor's transcript, sent with every `FAILED` and `SPEC-DRIFT` | `$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-{id}.jsonl` — the id is the spawn's task id, the file is flat whatever the depth | the same | the seat's name and its transcript id (`chat_find` by name; `pfm chat save` when the reader needs a file) |
| Question only the user can answer | `BLOCKED` in the return; the caller asks, sends the ruling to `flights-speccer` as a revising call, and re-runs the container naming the revised ids | `AskUserQuestion` now; the run continues on the answer | `AskUserQuestion` now |
| Stop an executor | not possible from inside; named in `DISPATCHED` | the same | `chat_kill` after the verdict is recorded |
| Cost profile | the loop stays out of the main chat | the main chat's context carries the loop; paid for the user's steering | seat cold starts on three engines; paid for engine choice |

The live container exists because a user sometimes wants to watch a flight land and rule on it as it goes; the nested container is the default for cost. The cross-harness container exists because an executor is sometimes a different engine on purpose (Codex for one task file, OpenCode for another); its seats are one-shot: born for one task file, killed after its verdict.

## Where each rule lives

A rule lives at the highest layer every reader who needs it reads, and nowhere else. Sub-agents never see the harness prompt; chat seats see their own engine's prompt plus the compiled `AGENTS.md`.

| Layer | Reader | Holds |
| --- | --- | --- |
| `pfm/harness-prompts/share/tail.md` § Orchestration | every main chat, chat seats included | The universal laws: cost = calls × context; a batch goes to `flights-orchestrator`, unspecified work to `flights-speccer`; report once, plus a real question or blocker, never a diff or a log in a message; waiting is one call or none; only `flights-speccer` changes a task file; the gater as the flight's one review; the hand's own laws, its return shape among them |
| `CLAUDE.md` / `AGENTS.md` | every sub-agent and seat | The executor's first move on a brief naming a task file: open it with the shared files named beside it, in the first message, and execute it |
| The agent file | the agent | The protocol of one role |
| The brief | one executor | The task file path, its `reads`, the `run.md` lines of its `needs`, the standing rules, the worktree, the testing manual's path; everything else an executor obeys lives in its agent |

Standing rules are what the project contract does not carry: the worktree, the fenced command that runs one package's affected tests (the full suite is the gate's alone), the checks by command, the cap, and anything the caller adds for this flight. The `CLAUDE.md` / `AGENTS.md` contract reaches every sub-agent and seat from the harness and is never pasted or named in a brief: pasted, it bills every executor twice for the same text.

The harness prompt lives in `pfm/harness-prompts/`; `share/tail.md` § Orchestration carries the laws for every engine, and the per-engine file carries only that engine's mechanics.

## Names

- The unit is a flight; the family is `flights`; a directory is `/tmp/{project}/flights/{flight}/`.
- Agent names carry no namespace (an agent name is lowercase letters, digits and hyphens), so the agents are `flights-speccer` and `flights-orchestrator`; commands carry the namespace as `/flights:{verb}`.

## Harness settings the family needs

Claude Code stops the Agent tool three levels below the main chat and caps concurrent sub-agents at twenty. A nested flight is main → orchestrator → executor → the executor's `tracer` → its walkers: four levels. `pfm` therefore carries two keys in its own settings (`claude.maxSubagentSpawnDepth`, default 8; `claude.maxConcurrentSubagents`, unset by default) and writes them onto every Claude Code launch line as `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` and `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS`, the way it already sets the web-search budget. Neither ceiling is reported when hit: a spawn past it does not happen, and nothing says so. The orchestrator therefore counts its own in-flight executors (`CLAIMED` lines without a verdict) and dispatches as many ready tasks at once as the cap the brief names admits, holding the rest for a free slot: the one legal hold. Absent from the brief, the cap is ten in flight at once, a first value.

## What the family replaced

| Retired | Replaced by |
| --- | --- |
| `/wave:refine` (R1 walk, R2 ask, R3 write, R4 architect passes + user gate) | `/flights:spec`: walk, ask, hand off to `flights-speccer`, present |
| `/wave:orchestrator` (train runner over chat seats) | `/flights:orchestrate-cross-harness` |
| `/wave:live` (batch on `main`) | `/flights:orchestrate-live` |
| `/wave:builder`, `/wave:walker`, the Codex `wave-builder` skill | the flight executor (or a seat born with its role) briefed with a task file |
| `/wave:ccc` | `/flights:audit` |
| `scheduler` agent, trains under `docs/dev/trains/` | none: a flight is one directory; several flights run one after another by the main chat |
| `architect` agent | `flights-speccer`'s reconcile phase |
| `speccer`, `speccer-orchestrator` | `flights-speccer`, `flights-orchestrator` |
| BUILD-GREEN handshakes, entry-point census, conformance pass, a worktree per wave | verification of each return, one `flights-gater` per project, the standing checks once |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/flights-speccer.md`, `flights-orchestrator.md`, `flights-mechanical-executor.md`, `flights-gater.md`, `variants.json` | The five protocols; `variants.json` renders the hard executor from the mechanical one |
| The commands | `templates/global/commands/flights/*.md` | The five commands, machine-global |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The universal laws, the family's names, the hand's laws for chat seats |
| The adopter contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The executor's first move; the pipeline paragraph under § Process |
| The executors' allowlist | `flights-mechanical-executor`, `flights-hard-executor` | `Read, Write, Edit, Bash, Glob, Grep`: no `Skill`, no `Agent`, no MCP tool; the gater alone adds `Skill` for `/code-review` |
| The engine | `pfm` settings and launcher | The two harness settings |
| This directory | `docs/design/flights/` | One design file per member with content of its own; this file for what they share |

## Open items

- The caps (80 tool calls per executor, 150 per gater, ten executors in flight at once) and the gater's review thresholds (5 files and 200 lines, 15 files and 800 lines) are first values; measure them against the next real flight.
- `pfm flights index` (compile and validate `index.md` from the task files' frontmatter) and a pfm-side audit of executor transcripts, so `/flights:audit` reads numbers instead of computing them.
- Whether `/code-review` runs inside a `flights-gater` sub-agent (the Skill tool at depth): verify on the next nested flight.
