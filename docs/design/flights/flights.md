# Flights

The foreman exists to raise the ratio of code written to tokens read. That ratio is the family's one measure: a rule, an agent or a step that does not raise it does not exist here.

## Contents

- [The measure](#the-measure)
- [Why the foreman builds by its own hand](#why-the-foreman-builds-by-its-own-hand)
- [The family](#the-family)
- [The life cycle](#the-life-cycle)
- [Choosing a tier](#choosing-a-tier)
- [What passes between agents](#what-passes-between-agents)
- [Reading and the trace store](#reading-and-the-trace-store)
- [Waiting](#waiting)
- [Tests](#tests)
- [Effort buttons](#effort-buttons)
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

- **One builder reads once.** The foreman reads what the build needs and writes the change itself; a child exists only for an area disjoint from its own.
- **A big reading may go to a tracer once.** The answer is reused while its files are unchanged.
- **Review and the full gate run once per worktree.** They happen at the very end, inside one lander.

## Why the foreman builds by its own hand

One brief (`pfm restart`, base `e4e4fa09`) was built three ways on 2026-10-18 and 2026-10-19:

| Build | Cost | Wall time | Requests | Notes |
| --- | --- | --- | --- | --- |
| native `general-purpose`, opus medium | $8.04 | 34 min | 134 | one agent |
| `flights-foreman` tree | $19.03 | 59 min | 550 | 24 agents; 6 workers on one package, 18 of 21 test runs red from siblings' half-written files; the foreman idle about 37 of the 59 min; workers re-read 15 of the 27 files their briefs summarized from the tracer |
| one-builder prototype | $6.87 | 39 min | 133 | one agent |

Flight merges over the prior 60 days averaged 1.42 Go test lines per code line, against 0.92 and 0.78 for the two single agents. So the foreman is the builder: it reads what the build needs, writes the whole change, and tests once it is whole. Splitting costs reading (each child re-reads what its brief summarized) and wall time (siblings break each other's runs), so it is the exception.

## The family

| Member | Kind | Model · effort | Does |
| --- | --- | --- | --- |
| `flights-foreman-sonnet` | agent | sonnet · high | Foreman for a clear path: the pattern is in the code; a `from:` render of `flights-foreman` |
| `flights-foreman` | agent | opus · high | Foreman for design choices, ambiguity, unfamiliar code, an unknown cause, several modules and the toughest work |
| `flights-lander-mini` | agent | sonnet · high | Lands a small worktree change |
| `flights-lander` | agent | opus · medium | Lands a worktree change |
| `flights-lander-pro` | agent | opus · high | Lands a large worktree change |
| `/flights:doctor` | command | the main chat | Measures a flight: the measure first, then where the reading went |

- **GPT twins:** `gpt-foreman` (sol, high, no effort buttons) and `gpt-flights-lander` (sol, high) run the same protocols.
  - `gpt-flights-lander` reviews through `gpt-reviewer` or `gpt-reviewer-pro`.
- **Landers are spawned only by the main chat.**
- **`reviewer`, `reviewer-pro`, `tracer` and `gitter`** are fleet agents the family uses; they are not its members.

## The life cycle

```text
main chat -> gitter worktree (or develop) -> foreman tier, worktree path in its brief
foreman -> reads, writes the whole change, runs the affected tests once -> one return message
(a child only for an area disjoint from the foreman's own, while the foreman is busy with its part)
every foreman on the worktree returned -> main chat -> lander tier -> one reviewer
lander fixes all findings -> affected tests until green -> full gate once -> lander returns
main chat -> gitter commits / merges
```

1. **Settle.** The main chat settles open requirements with the user before any dispatch.
2. **Place.** The main chat has gitter make the worktree, or names `develop` or the checkout, and passes the path in the brief. Several foremen may share one worktree on separate areas.
3. **Build.** A foreman builds the request by its own hand.
   - **Reads:** what the build needs; see [reading](#reading-and-the-trace-store).
   - **Writes:** the whole change, then runs the affected tests once, never after each edit; a fix's regression test runs red first.
   - **Children:** a child only when both hold: the foreman is busy with its own part, and the other area has no file in common with its own.
   - **Returns:** one message; every item the brief names is built, or the return is `PARTIAL` naming it.
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

The main chat chooses at dispatch; a foreman chooses for the rare child it spawns.

| Choice | Decided by | Rule |
| --- | --- | --- |
| Foreman tier | difficulty and ambiguity, never size | `flights-foreman-sonnet`: the path is clear, the pattern is in the code · `flights-foreman`: design choices, ambiguity, unfamiliar code, an unknown cause, several modules, the toughest work |
| Child tier | what the area needs | `flights-foreman-sonnet`: a clear path, the pattern is in the code · `flights-foreman`: design, debugging, judgment |
| Lander tier | size of the worktree change | mini: under 150 code lines in one module · pro: 1,000+ code lines or 4+ modules · base: everything else |
| Reviewer | size of the worktree change | `reviewer-pro` at 10,000+ code lines; `reviewer` otherwise |

- **Code lines** are counted from `git diff --numstat` of the worktree against its base, with tests, docs, generated and lock files excluded.
- **Modules** are the build units of the project map.

## What passes between agents

- **Messages only.** A brief is the spawn prompt, and a return is the final message.
- **No files.** There is no flight directory, run log, brief file, return file, gate file or spec file.
- **The one path that travels** is a tracer run, when one exists, named in a brief so the receiver may reuse it.
- **A foreman's brief carries:**
  - the work and its acceptance;
  - where to work;
  - the testing manual path, if one exists;
  - a tracer run path, when one exists;
  - for a child, the interface it must fit.
- **A foreman's return:**
  - first line `DONE {name}` or `PARTIAL {name}: {what is left}`;
  - then the decisions it made, the files it changed, and the tests it ran with their result lines;
  - a `TRACER` line naming any tracer run it made or used, so a lander can reuse it;
  - an `OUTSIDE` line naming any change beyond the request, which it never makes.
- **A child's return** has a foreman's shape. It is a claim. The parent checks it against the diff and the test lines.
- **A lander's return:**
  - first line `PASS`, `FIXED` or `FAIL {worktree}`;
  - then each finding, fixed or listed;
  - each project's last full-gate verdict line;
  - the residuals;
  - the reviewer's report path.

## Reading and the trace store

- **The foreman reads what the build needs:** the files it changes, the code it calls at its signatures and contracts, and the closest pattern once.
- **The tracer is optional.** A foreman may call one; it is not required to.
- **A run may travel.** When a tracer run exists, its path may go into a child's brief.
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
  - a foreman runs the affected tests once its change is whole, never after each edit; a fix's regression test runs red first;
  - only the lander runs the full gate, once per worktree right before its merge, after every review finding is fixed and the affected tests are green.
- **A red full gate:** the lander fixes it, gets the failing tests green with the affected-test command, never a gate rerun, then runs that gate once more. That is the only repeat.
- **The testing manual:** a project's testing manual, where one exists (this repo's `.claude/commands/pfm-testing-manual.md`), names the affected-test command and the full gate. Without one, agents use the project's documented test commands.
- **Where tests run:** this repo runs every test in the fence through `.claude/scripts/test.sh`.

## Effort buttons

The `auto-effort-control` plugin gives an agent whose frontmatter carries `effortControl` three tools, `effort_up`, `effort_down` and `effort_auto` (`mcp__auto-effort-control__…`), and steers its effort while it works (`plugins/auto-effort-control/README.md`). `effortControl: on` takes the plugin's default band, floor medium and ceiling xhigh.

| Agent | `effortControl` |
| --- | --- |
| `flights-foreman`, `flights-foreman-sonnet` | `on` (floor medium, ceiling xhigh), both starting at high |
| `flights-lander`, `flights-lander-mini`, `flights-lander-pro` | floor medium, ceiling xhigh, tripwires `redispatch` |
| `reviewer`, `reviewer-pro`, `reviewer-nt`, `documenter` | `on` |

A `from:` tier inherits its original's `effortControl` and `tools:`, and a `set` overrides only the keys it names. No `gpt-*` twin carries it: each GPT twin pins a `tools:` line without the buttons and unsets the key (see [gpt-twins.md](../gpt-twins/gpt-twins.md#declarations)); nor does `pcm` or `releaser`. Every steered agent but the two foremen lists the three tools in its own `tools:` line under the `mcp__auto-effort-control__` prefix ([flights-lander.md](flights-lander.md#declaration)); the two foremen carry every tool and have no `tools:` line. A foreman steers itself: effort up before a design choice the code leaves open or a red whose cause it cannot name, effort down for a stretch of mechanical edits, effort auto once that stretch is over.

## Decisions

- **A failing gate is fixed in the code.** A foreman raises a limit only through the project's documented exception route, the reason recorded there and in its `DECISIONS`; the lander judges it.
- **Never ask.** A foreman or lander never asks the user. It decides, and lists every decision first in its return.
- **Critical decisions go to the main chat, without stopping.** A critical decision is one where being wrong costs data, privacy or patient data, money, security, or anything irreversible. The agent sends it to the main chat (`SendMessage(to: "main")`) with the choice it is proceeding with, then keeps working. If an answer arrives that differs, it adopts it.
- **The place is the parent's.** A foreman's prompt names no worktree: it works where its parent points it, and the main chat owns the place, the landing and git.

## What the family never carries

| Absent | Instead |
| --- | --- |
| Agent hooks (cut gate, call cap, stop hooks, wait denial) | the protocol in the agent file |
| A call or round cap | the foreman decides when its change is whole |
| Flight files and a run log | messages |
| A lander or gitter spawned by a foreman | the main chat owns both |
| Review lenses (a security seat, fixed tracers) | one reviewer over the whole change |
| A second gate round | fixes inside the one lander |
| A spec writer or a requirements interview command | the main chat settles requirements; `flights-foreman` takes novel work |

## Harness settings

The deepest chain is main → foreman → child foreman → tracer → sub-tracer: four levels below the main chat; a foreman reaches it only when it spawns. pfm writes `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` and `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` onto every Claude Code launch line (`pfm/internal/claudelaunch/knobs.go`). A spawn refused at the concurrency limit never started, and goes out again once a slot frees.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/flights-{foreman,lander}.md` and their `from:` tiers, GPT twins in `templates/global/agents/gpt-flights-*.md`, rendered into `templates/generated/agents/` | The protocols |
| The command | `templates/global/commands/flights/doctor.md` | The meter |
| The measurement | `pfm tokens --tree` in `pfm/internal/tokens/` | The measure's numbers |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The ladder, the tier rubrics, main-chat ownership of worktree, lander and gitter |
| The project contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The first move of a sub-agent |
| The tracer | `templates/global/agents/tracer.md`, `templates/global/skills/tracetree/`, `docs/design/tracing/` | The store and its reuse |
| This directory | `docs/design/flights/` | One file per member, this file for what they share |

## Open items

- The baseline before this design: a median of 41 landed lines per million input tokens over 166 flights in the 14 days to 2026-10-18. Agents that wrote no code took 61% of the input. The first flights on this design are measured against it with `/flights:doctor`.
- The lander and reviewer size bars are first values; tune them from `/flights:doctor` reports.
