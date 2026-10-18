# Flights

The foreman exists to raise the ratio of code written to tokens read. That ratio is the family's one measure: a rule, an agent or a step that does not raise it does not exist here.

## Contents

- [The measure](#the-measure)
- [The family](#the-family)
- [The life cycle](#the-life-cycle)
- [Choosing a tier](#choosing-a-tier)
- [What passes between agents](#what-passes-between-agents)
- [Reading and the trace store](#reading-and-the-trace-store)
- [Waiting](#waiting)
- [Tests](#tests)
- [Decisions](#decisions)
- [What the family never carries](#what-the-family-never-carries)
- [Harness settings](#harness-settings)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## The measure

| Number | Definition | Reported by |
| --- | --- | --- |
| Landed lines per million input tokens | code lines added plus removed in what landed (tests, docs, generated and lock files excluded) ÷ input tokens (input + cache read + cache write) of every agent in the tree, × 10⁶ | `pfm tokens --tree`, first line of `/flights:doctor` |
| Written / read | bytes an agent wrote through Edit and Write to repo files ÷ bytes of tool results it read | per agent and per agent type, same tools |
| Silent share | share of the tree's input tokens taken by agents that wrote no code | same tools |

Reading is the cost; writing is the product. The family raises the ratio three ways:

- **The writer is cheap.** Sonnet workers write the code.
- **Big reading happens once.** It goes to a tracer, and the answer is reused while its files are unchanged.
- **Review and the full gate run once per worktree.** They happen at the very end, inside one lander.

## The family

| Member | Kind | Model · effort | Does |
| --- | --- | --- | --- |
| `flights-foreman-mini` | agent | opus · medium | Foreman for clear work with a pattern to follow |
| `flights-foreman` | agent | opus · high | Foreman for design choices, ambiguity, unfamiliar code, cross-module work |
| `flights-foreman-pro` | agent | opus · xhigh | Foreman for the toughest challenges; the highest level of intelligence among the builders |
| `flights-worker` | agent | sonnet · high | Writes one clearly defined piece and runs its tests |
| `flights-worker-pro` | agent | sonnet · xhigh | Writes one piece that needs debugging or design inside it |
| `flights-lander-mini` | agent | sonnet · high | Lands a small worktree change |
| `flights-lander` | agent | opus · medium | Lands a worktree change |
| `flights-lander-pro` | agent | opus · high | Lands a large worktree change |
| `/flights:doctor` | command | the main chat | Measures a flight: the measure first, then where the reading went |

- **GPT twins:** `gpt-flights-foreman` (sol, high), `gpt-flights-foreman-pro` (sol, xhigh) and `gpt-flights-lander` (sol, high) run the same protocols.
  - GPT foremen spawn the Claude workers.
  - `gpt-flights-lander` reviews through `gpt-reviewer` or `gpt-reviewer-pro`.
- **Workers are visible only to the foremen.**
- **Landers are spawned only by the main chat.**
- **`reviewer`, `reviewer-pro`, `tracer` and `gitter`** are fleet agents the family uses; they are not its members.

## The life cycle

```text
main chat -> gitter worktree (or develop) -> foreman tier, worktree path in its brief
foreman -> workers / child foremen in parallel -> foreman integrates -> one return message
every foreman on the worktree returned -> main chat -> lander tier -> one reviewer
lander fixes all findings -> affected tests until green -> full gate once -> lander returns
main chat -> gitter commits / merges
```

1. **Settle.** The main chat settles open requirements with the user before any dispatch.
2. **Place.** The main chat has gitter make the worktree, or names `develop` or the checkout, and passes the path in the brief. Several foremen may share one worktree on separate areas.
3. **Build.** A foreman reads what it changes, traces what is too large to read, and splits the work.
   - **Who gets each piece:** workers get the defined pieces; child foremen get large areas that need their own split.
   - **What the foreman writes:** only the joints between pieces.
   - **Before returning:** it runs the affected tests across the pieces, then returns one message.
4. **Land.** Once every foreman on a worktree has returned, the main chat spawns one lander. The lander:
   1. spawns one reviewer over the whole change;
   2. fixes every finding;
   3. runs the affected tests until green;
   4. runs the full gate once;
   5. returns.

   One worktree gets one lander and one reviewer: no second round, no second lander.
5. **Merge.** On `PASS` or `FIXED`, the main chat sends gitter to commit and merge, with the reviewer's report path as its REPORT_PATH.
6. **Measure.** `/flights:doctor`, run by the user, reports the measure for the tree.

## Choosing a tier

The main chat chooses at dispatch; a foreman chooses for its own children.

| Choice | Decided by | Rule |
| --- | --- | --- |
| Foreman tier | difficulty and ambiguity, never size | mini: the path is clear, the codebase has the pattern, little is left to decide · base: real design choices, ambiguity, unfamiliar code, a known symptom with an unknown cause, several modules · pro: no pattern to follow, deep ambiguity, a root cause earlier attempts missed, concurrency, data integrity or security where being wrong is costly |
| Worker tier | what the piece needs | worker: the files, the change and the acceptance are fully stated · worker-pro: the piece needs debugging or design inside it, or code nobody has read |
| Lander tier | size of the worktree change | mini: under 150 code lines in one module · pro: 1,000+ code lines or 4+ modules · base: everything else |
| Reviewer | size of the worktree change | `reviewer-pro` at 10,000+ code lines; `reviewer` otherwise |

- **Code lines** are counted from `git diff --numstat` of the worktree against its base, with tests, docs, generated and lock files excluded.
- **Modules** are the build units of the project map.

## What passes between agents

- **Messages only.** A brief is the spawn prompt, and a return is the final message.
- **No files.** There is no flight directory, run log, brief file, return file, gate file or spec file.
- **The one path that travels** is a tracer run, named in a brief so the child reuses it.
- **A foreman's brief carries:**
  - the work and its acceptance;
  - where to work;
  - the testing manual path, if one exists;
  - the tracer run paths;
  - for a child, the interface it must fit.
- **A foreman's return:**
  - first line `DONE {name}` or `PARTIAL {name}: {what is left}`;
  - then the decisions it made, the files it changed, and the tests it ran with their result lines.
- **A worker's return:** first line `DONE {piece}` or `PARTIAL {piece}: {why}`, then the changed files and the test result lines.
- **A lander's return:**
  - first line `PASS`, `FIXED` or `FAIL {worktree}`;
  - then each finding, fixed or listed;
  - each project's last full-gate verdict line;
  - the residuals;
  - the reviewer's report path.

## Reading and the trace store

- **Read only what you change.** A foreman or worker reads the files it will change.
- **Too large means trace.** Understanding an area beyond about 10 files or 2,000 lines the agent will not edit requires `tracer`.
- **Answered files are not re-read.** After a trace, the agent reads only the lines it edits inside the files the answer covered.
- **Answers travel down.** A tracer run's path goes into every child's brief.
- **The store is per repo.** Every tracer run lives under `/tmp/{project}/tracer/`, and each answer records the sha256 of every file it cited.
- **The tracer checks the store first.** Recall searches every earlier run of the same repo root, with no age limit.
  - An answer whose cited files all match their recorded hashes is fresh: it is returned with no walk.
  - An answer with changed files re-walks only the threads that cite them.
- **No hook prepares, forces or gates a tracer.** The tracer opens its own run and recalls as its first command.
- **Reviewers batch their questions.** `reviewer` puts every question into one tracer run; `reviewer-pro` uses at most three, grouped by area.

## Waiting

- **Agents** are waited on by ending the turn. Their return arrives as a notification.
- **Commands** run in the foreground with a timeout. One longer than the Bash tool's 10-minute limit runs in the background and is waited on with one blocking wait (`timeout {s} bash -c 'until [ -s "$1" ]; do sleep 20; done; cat "$1"' _ {file}`).
- **Never end a turn while your own command runs.** An agent woken by its own finished background command can have its final notice delivered to the main chat instead of its parent.

## Tests

- **Who runs what:**
  - a worker runs its piece's test command;
  - a foreman runs the affected tests across its pieces after integrating;
  - only the lander runs a full suite, once per touched project at the very end, after every review finding is fixed and the affected tests are green.
- **A red full gate:** the lander fixes it, gets the failing tests green, then runs that project's gate once more. That is the only repeat.
- **The testing manual:** a project's testing manual, where one exists (this repo's `.claude/commands/pfm-testing-manual.md`), names the affected-test command and the full gate. Without one, agents use the project's documented test commands.
- **Where tests run:** this repo runs every test in the fence (`.claude/scripts/dev.sh iso`).

## Decisions

- **Never ask.** A foreman, worker or lander never asks the user. It decides, and lists every decision first in its return.
- **Critical decisions go to the main chat, without stopping.** A critical decision is one where being wrong costs data, privacy or patient data, money, security, or anything irreversible. The agent sends it to the main chat (`SendMessage(to: "main")`) with the choice it is proceeding with, then keeps working. If an answer arrives that differs, it adopts it.
- **No location named.** A foreman given no place to work first sends the main chat "No worktree named — working on {branch} at {path}", then proceeds.

## What the family never carries

| Absent | Instead |
| --- | --- |
| Agent hooks (cut gate, call cap, stop hooks, wait denial) | the protocol in the agent file |
| A call or round cap | a foreman splits the work across workers and child foremen rather than doing it all itself |
| A cutting tool that decides children | the foreman decides its split |
| Flight files and a run log | messages |
| A lander or gitter spawned by a foreman | the main chat owns both |
| Review lenses (a security seat, fixed tracers) | one reviewer over the whole change |
| A second gate round | fixes inside the one lander |
| A spec writer or a requirements interview command | the main chat settles requirements; `flights-foreman-pro` takes novel work |

## Harness settings

The deepest chain is main → foreman → child foreman → worker → tracer → sub-tracer: five levels below the main chat. pfm writes `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` and `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` onto every Claude Code launch line (`pfm/internal/claudelaunch/knobs.go`). A spawn refused at the concurrency limit never started, and goes out again once a slot frees.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/flights-{foreman,worker,lander}.md` and their `from:` tiers, GPT twins in `templates/global/agents/gpt-flights-*.md`, rendered into `templates/generated/agents/` | The protocols |
| The command | `templates/global/commands/flights/doctor.md` | The meter |
| The measurement | `pfm tokens --tree` in `pfm/internal/tokens/` | The measure's numbers |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The ladder, the tier rubrics, main-chat ownership of worktree, lander and gitter |
| The project contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The first move of a sub-agent |
| The tracer | `templates/global/agents/tracer.md`, `templates/global/skills/tracetree/`, `docs/design/tracing/` | The store and its reuse |
| This directory | `docs/design/flights/` | One file per member, this file for what they share |

## Open items

- The baseline before this design: a median of 41 landed lines per million input tokens over 166 flights in the 14 days to 2026-10-18. Agents that wrote no code took 61% of the input. The first flights on this design are measured against it with `/flights:doctor`.
- The tracer threshold (10 files or 2,000 lines) and the lander and reviewer size bars are first values; tune them from `/flights:doctor` reports.
