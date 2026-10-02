# The flight executors

`flights-mechanical-executor`, `flights-precise-executor` and `flights-smart-executor` are the hands of a flight: one fresh agent per task file, picked by the task's rating, which writes the code and its covering tests and returns once. One body per tier: this file is the base every tier holds, and each tier's body adds the rules its work needs. They replace the per-project `developer` and `qa` agents inside a flight, and they carry the instructions that task files and `0-` shared files used to restate for every executor.

Decisions live in this file. The executable wording lives in the three bodies, [`flights-mechanical-executor.md`](../../../templates/global/agents/flights-mechanical-executor.md), [`flights-precise-executor.md`](../../../templates/global/agents/flights-precise-executor.md) and [`flights-smart-executor.md`](../../../templates/global/agents/flights-smart-executor.md); what each tier adds, and why, is in its tier doc: [mechanical](mechanical-executor.md), [precise](precise-executor.md), [smart](smart-executor.md).

## Contents

- [Why it exists](#why-it-exists)
- [The tiers](#the-tiers)
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

## The tiers

| Rating | Agent | Claude | Codex role pin |
| --- | --- | --- | --- |
| `mechanical` | `flights-mechanical-executor` | `claude-sonnet-5-5` at `high` | `gpt-6-luna` at `xhigh` |
| `precise` | `flights-precise-executor` | `claude-sonnet-5-5` at `xhigh` | `gpt-6.1-sol` at `high` |
| `smart` | `flights-smart-executor` | `opus` at `high` | `gpt-6.1-sol` at `high` |

A seat under [`/flights:orchestrate-cross-harness`](../../../templates/global/commands/flights/orchestrate-cross-harness.md) sets the same Codex model and effort by rating on its launch line; a smart seat always runs `gpt-6.1-sol` at `high`.

The Sonnet tiers pin the full model ID: the `sonnet` alias resolved to different models on different accounts of one host. Each Claude pick was measured against its neighbours on real landed tasks, two seats per configuration, blind-judged:

- `mechanical`: Sonnet 5.5 at `high` scored 83.5 against `medium`'s 74.5.
- `precise` is a lateral tier, not a cheaper one. On two pinned-but-hard tasks (concurrency and failure paths; a bounded retry with many stop rows), Sonnet 5.5 at `xhigh` averaged 92.5 against Opus 5.5 at `high`'s 84 and Sonnet 5.5 at `high`'s 78.5, whose seats ranged from 64 to 95.
- `smart`: Opus 5.5 at `high` scored 75 against `medium`'s 65. On a goal-only task that left the design open, Opus at `high` scored 88 against Sonnet 5.5 at `xhigh`'s 66. So the spec's pinning, not the task's size, decides between `precise` and `smart`.

The Codex pins were measured the same way, on the same three tasks, with the Claude seats blinded in as anchors:

- `mechanical`: `gpt-6-luna` at `xhigh` scored 87 against `high`'s 85 and `medium`'s 76, at under $0.10 a task; Sonnet 5.5 at `high` scored 86 and 87.
- `precise`: `gpt-6-sol` at `high` scored 94, beside Sonnet 5.5 at `xhigh`'s 95 and 97; `gpt-6-luna` scored 81 at `xhigh` and 67 at `high`, both shipping log keys the scrubber redacts.
- `smart`: `gpt-6-sol` at `high` scored 87 against `xhigh`'s 85, at about 70% of the cost; Opus 5.5 at `high` scored 86.

Those runs used the one shared body. A later round ran the three tier bodies on the same tasks, fenced-graded, two seats per configuration, and moved `precise` and `smart` to `gpt-6.1-sol`:

- `precise`: `gpt-6.1-sol` at `high` passed every gate and 19 of 19 hidden tests in both seats, at $0.61 and $0.69; `gpt-6-sol` at `high` on the same body also passed every gate and 19 of 19, at $0.77.
- `smart`: `gpt-6.1-sol` at `high` passed every gate in both seats, the full suite included, and they were the first smart seats to pass the architecture ratchet, at $1.46 and $1.53; `gpt-6-sol` at `high` failed the ratchet in 3 of 3 runs, at $1.88 to $2.14.
- Price per 1M tokens, from OpenAI's API pricing page on 2026-09-29: `gpt-6.1-sol` $2 in, $0.10 cached, $10 out; `gpt-6-sol`'s cached input is $0.20.

Blind-judged together with the Claude anchors: `precise` `gpt-6.1-sol` scored 94 and 95 against `gpt-6-sol`'s 95 on the same body (84 on the old shared body) and Sonnet 5.5 at `xhigh`'s 97, a tie at about 15% less cost; `smart` `gpt-6.1-sol` scored 96 and 91 against `gpt-6-sol`'s 90 and 78 on the specialized bodies (72 on the old one) and Opus 5.5 at `high`'s 89, the best smart seats of every round. `gpt-6.1-sol` is the Codex pin for both tiers.

Each tier is its own body — `flights-mechanical-executor.md`, `flights-precise-executor.md`, `flights-smart-executor.md` — with its model, effort and Codex pin in its own frontmatter; no tier is a variant of another. The orchestrator picks the agent type by the index row's `rating` and passes no model override, so the tier is a registry fact, visible in a transcript's `agentType`.

## What it holds

Everything that is true for every task of every flight:

- the first move: open the brief file, the task file and its `reads` together, in one message; the brief pastes the `DONE` lines of the task's needs, which are what landed before it;
- a `Progress dependency` that does not hold returns `SPEC-DRIFT` with nothing changed; a decision it cannot make returns `BLOCKED {id}: {question}`;
- the hand is per tier. `precise` and `smart` reach the Goal their own way inside `Files` and say what they changed. `mechanical` applies the Steps and makes only its listed adaptations — a quoted line found at another place, an import the edit needs, the formatter's output, a fix for its own red; anything else returns `SPEC-DRIFT`;
- a red its own edit caused inside `Files` is iteration: fix it, rerun. Any other red it did not foresee is returned as `FAILED` or `SPEC-DRIFT` with its cause, or with what was read and "cause unknown": `precise` and `smart` read until the cause is named — the line, the value, the code path — while `mechanical` reads the error and the lines it names, deeper diagnosis being `smart`'s ([mechanical-executor](mechanical-executor.md#reds-are-bounded)); on every tier an unchanged rerun and a fix outside `Files` are forbidden; a cause outside `Files` that stops its own tests stops nothing early: what can still run past it runs, and every outside cause returns at once, first line `FAILED {id}: blocked by {file}, {file}…` naming every file, then one `{file}: {error line}` line per cause. The earlier wording, "a rerun and a fix outside the spec are forbidden", was read as a ban on fixing its own test, fixture or lint line and on looking past the first foreign red: in hermetic-fence, 26 red returns, one executor citing the rule for leaving its own reds, two tasks finding one layer of a broken foundation per round; the ten briefs that carried "fix every red of your own, an outside cause is never a reason to stop early" left no red of their own;
- a red in a test its diff does not reach, or a check rejecting what was there before its edit, is an outside defect, named and never fixed; the task finishes `DONE`;
- a test the task's Decisions list under `Temporary reds` (left red by this task, turned green by a named later task) is neither a `SPEC-DRIFT` nor a `FAILED` cause: named in the return, the task continues; a red outside `Files` it does not list stays `SPEC-DRIFT`;
- read discipline: find the lines with a search, read that range; never a whole file to find a place, never again a file still in context; a log through `tail` or a search, never whole; the project contract is already in context and is never read;
- waiting is one call: `mechanical` runs every command in the foreground as one call at the tool's longest timeout; the other tiers size the timeout to the command; never a poll chain;
- scratch files (logs, scripts) sit in a directory named for the task id; siblings share the scratch root;
- it stays inside the task's `Files`, a return file the brief names aside; git is read-only;
- the return format and the ban on progress messages, diffs and logs in a message;
- the rules a task file used to carry as fixed lines: how a `Done when` row is proven, a row read two ways, and the `Progress dependency` check before step 1.

What it does not hold: anything about one project. Project law reaches it through the project contract the harness injects and through the project's [testing manual](testing-manual.md).

## Tests

The executor writes the covering tests itself, one per `Done when` row, in the project's pattern. A row is a matrix row or a `Given` line, on every tier:

- before the first test it opens the project's testing manual, the path named in the orchestrator's brief, and follows its tiers, test home, lane or registry duty, mock boundary, run commands and traps;
- red once per task: every row's test is written first, each name the task creates stubbed so it compiles; all the new tests run in one command against the unfixed tree, each failing on its assertion (a build error proves nothing), the log kept; the fix lands and the same command runs green once. A row whose behaviour was in the tree before that red run (a previous round's code) gets no red proof: the return marks it `pre-existing, no red proof`, and the lander's adversarial pass covers it. No executor re-breaks, stashes, reverts or mutates finished or landed code to watch a test fail;
- a row with no behaviour change (a rename, a move, a deletion, the doc references one carries) or one a written deliverable meets is proven by its check line, plus the quoted line that meets it for a written deliverable; the orchestrator accepts that in place of a red-log line, and every other row needs one or the `pre-existing, no red proof` mark;
- it runs only the affected tests as it goes; the formatter, lint and type check of its own files and the static check the testing manual names (its architecture ratchet included) run once, after its last edit, as one command, a red there fixed and only that check rerun; a ratchet its diff pushes over is its to bring back under — the full suite is the lander's;
- a test that exists but did not run is missing; when a test and a row disagree the code is wrong, never the row;
- a row read two ways takes the reading today's code supports, named in the return; `SPEC-DRIFT` only when neither reading settles it and they build different code.

Bias of an author testing its own code is real and accepted here: the executor's tests prove the rows, and the independent attack is [`flights-lander`](flights-lander.md)'s.

## Layout laws at write time

`/quality:llm-codebase` is a design command; the laws of it that bind at the moment of writing a file live in the `precise` and `smart` bodies, so the speccer does not carry them for those tiers. A `mechanical` task makes no design choice: its speccer quotes the façade and the reuse targets as `EXISTING` shapes, and a new name the task does not give returns `SPEC-DRIFT`. The laws:

- search for the concept before creating a file or a function; reuse what exists, never a second implementation under another name;
- one canonical term per concept, the one the code already uses, identical in file name, identifier, wire key, environment variable and test name;
- call the project's façade for a cross-cutting mechanism (process execution, database open, file write, environment, clock, LLM invoke, logging, the test scratch root), never the primitive;
- no directory named by negation (`utils`, `helpers`, `common`, `misc`, `shared`): a new file sits with the unit that changes with it;
- a source file over the project's size ceiling is split before logic is added to it, per tier: on `mechanical` an edit pushing a file over the ceiling returns `SPEC-DRIFT`; on `precise` and `smart` a split's new file beside a `Files` entry, in the same unit, is in scope, and a split needing an existing file outside `Files` returns `SPEC-DRIFT {id}` naming it;
- a runner, parser or census script the task needs is a versioned script under the project's `scripts/`, never a private copy;
- one test home per source file, every temp path through the project's scratch-root helper.

The design-time laws (unit of change, anatomy, registries, cross-project alignment) stay with [`flights-speccer`](flights-speccer.md).

## Context budget

Cost is calls times context, and an executor's starting context is re-sent on every call.

- `tools: Read, Write, Edit, Bash, Glob, Grep` — no `Skill`, no `Agent`, no MCP tool. Listing `Skill` injects the skills listing, a measured 7.4K tokens per call; an MCP tool outside the allowlist costs nothing.
- The bodies are 6.5 KB (`mechanical`), 8.1 KB (`precise`) and 9.1 KB (`smart`). They exceed the earlier 5 KB ceiling by the tier-specific rules the transcript audits measured as stopping over-stops and misses; a rule that stops no measured failure is cut.
- The brief is a file carrying the task file path, its `reads`, the pasted `DONE` lines of its `needs`, the `RETRO` lines so far, the standing rules, the testing-manual path and the worktree. Everything else the old brief restated is in the agent.
- The project contract is injected by the harness into every sub-agent and no setting stops it; its size is the project's to keep small.

## The cap

80 tool calls. Past it the executor stops and returns `FAILED {id}: cap` with the handoff: what landed, what is left, the next step. The number is in the agent, not in the brief; the speccer sizes tasks to it (a task that needs 150 calls is three tasks). A `smart` executor estimates its calls once its design is stated and returns `SPEC-DRIFT {id}: too large` with the split it would make, nothing changed; the orchestrator records it as `{id} TOO-LARGE · {the split it proposes}` — no round, no red, no transcript — and a revising `flights-speccer` call cuts the task. The measured healthy band is 40 to 80 calls; the runaway executors of the audited flights ran 135 to 254.

## What it no longer does

- No review and no `Skill` tool: `/code-review` runs once per flight, in the lander, over the flight's whole diff. The per-executor review was the largest single defect of the audited flights: 67 review sessions, 182M tokens.
- No full-suite run, no format or lint sweep beyond its own files.
- No conformance report: a drift from the spec surfaces as the next task's `SPEC-DRIFT`.

## The return

First line `DONE {id}`, `FAILED {id}: {why}`, `SPEC-DRIFT {id}: {what}` or `BLOCKED {id}: {question}`. Then: files changed; the test that covers each `Done when` row with its failing line in the one red log, or `pre-existing, no red proof`, and the green log; what it adapted; defects found outside its files; what it could not reach. Last, `RETRO {lesson}` or `RETRO none`: one line, at most 200 characters, a fact about the environment, the tooling, the project law or the testing manual that cost it calls and would cost the next agent the same, with the working alternative; the orchestrator records it, deduplicates it and carries it to later briefs and to the user ([Retro lines](flights-orchestrator.md#retro-lines)).

## Codex and OpenCode

`pfm codex agents` and `pfm opencode build` compile the three agents like every global role. On Codex a role cannot restrict tools or MCP servers — those settings are global — so the allowlist is a Claude saving only; the cap, the read discipline and the single-wait rule are the Codex savings, and the wait recipe itself lives in the Codex part of the fleet prompt.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/flights-{mechanical,precise,smart}-executor.md` | The executable wording, one body per tier, each with its own frontmatter pins |
| The tier docs | [mechanical](mechanical-executor.md), [precise](precise-executor.md), [smart](smart-executor.md) | What each tier's body adds, and the measurements behind it |
| The orchestrator | [`flights-orchestrator`](flights-orchestrator.md) | The brief, the verification of a `DONE`, the agent type by rating |
| The speccer | [`flights-speccer`](flights-speccer.md) | Task size against the cap; test tier and home in `Done when` and `Files` |
| The testing manual | [`testing-manual`](testing-manual.md) | The project's test law the executor follows |
| The lander | [`flights-lander`](flights-lander.md) | The review and the full suite the executor no longer runs |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | A review inside an executor is a caller rule the manual refuses; the lander is the flight's one review. It carries no executor law: executors and executor seats run on the agent body |

## Evidence

- Spend audit of four flights: 94% of 770M tokens in executors; poll calls 28%, review sessions 182M, re-reads 2,214 of 6,653 read commands, 427 reads of the project contract already in context.
- Starting context by agent type, from 300 transcripts: the skills listing arrives only with `Skill` in the allowlist.
- Of 17 lifecycle frameworks surveyed, 13 have the implementer write its own tests or nobody.
