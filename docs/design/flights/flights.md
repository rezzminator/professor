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
- [What the build/land lifecycle never carries](#what-the-buildland-lifecycle-never-carries)
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

- **One builder reads once.** The foreman reads what the build needs and writes the change itself; it spawns nothing, and the main chat splits work that divides by area.
- **A big reading may go to a tracer once.** The answer is reused while its files are unchanged.
- **Review and the full gate run once per worktree.** They happen at the very end, inside one lander, whose cheap children read what it spawns them for. Small code work, every edit fixed in advance, has no lander; docs and framework files (prompts, rules, commands, agent definitions, config, scripts) are never a flight.

## Why the foreman builds by its own hand

One brief (`pfm restart`, base `e4e4fa09`) was built three ways on 2026-10-18 and 2026-10-19:

| Build | Cost | Wall time | Requests | Notes |
| --- | --- | --- | --- | --- |
| native `general-purpose`, opus medium | $8.04 | 34 min | 134 | one agent |
| `flights:foreman` tree | $19.03 | 59 min | 550 | 24 agents; 6 workers on one package, 18 of 21 test runs red from siblings' half-written files; the foreman idle about 37 of the 59 min; workers re-read 15 of the 27 files their briefs summarized from the tracer |
| one-builder prototype | $6.87 | 39 min | 133 | one agent |

Flight merges over the prior 60 days averaged 1.42 Go test lines per code line, against 0.92 and 0.78 for the two single agents. So the foreman is the builder: it reads what the build needs, writes the whole change, and tests once it is whole. Splitting costs reading (each child re-reads what its brief summarized) and wall time (siblings break each other's runs), so a foreman spawns no child; the main chat splits work across several foremen on separate files of one worktree.

## The family

| Member | Kind | Model · effort | Does |
| --- | --- | --- | --- |
| `flights:foreman-sonnet` | agent | sonnet · high | Foreman for a clear path: the pattern is in the code; the same protocol as `flights:foreman`, in its own file |
| `flights:foreman` | agent | opus · medium | Foreman for design choices, ambiguity, unfamiliar code, an unknown cause, several modules |
| `flights:lander` | agent | opus · medium | Reviews, fixes and gates a worktree change, then returns it for the merge |
| `flights:speccer` | agent | opus · xhigh | On-demand specification outside the build/land lifecycle; returns a `spec.md` path and token price to its caller |
| `flights:diff-checker`, `flights:spec-checker`, `flights:usage-checker` | agents | opus · sonnet · haiku | The lander's read-only children ([flights-lander.md](flights-lander.md#the-children)) |
| `/flights:spec` | command | the main chat | Grills the user until no decision is open, writes `requirements.md`, hands it to `flights:speccer`; the spec waits for the user's go to build |
| `/flights:doctor` | command | the main chat | Measures a flight: the measure first, then where the reading went |

- **The speccer is user-invoked:** the user, or the main chat on the user's request, calls it, most often through `/flights:spec`, which hands it the grilled `requirements.md`. No foreman or lander hands work to it; it returns to its caller and hands off to nobody. Its spec, research and calibration files live in its flight directory, outside the message-only build/land protocol.
- **Landers are spawned only by the main chat.**
- **`flights:tracer` and `gitter`** are fleet agents the family uses; they are not its members.

## The life cycle

```text
main chat -> worktree.sh create (gitter without the script; or develop) -> foreman tier, worktree path in its brief
foreman -> reads, writes the whole change, runs the affected tests once -> one return message
every foreman on the worktree returned -> main chat -> flights:lander -> its own review, children in the background
lander fixes all findings -> affected tests until green -> full gate once -> lander returns
main chat -> worktree.sh land (gitter commits / merges on a refusal or without the script) -> verified teardown (or KEEP lock, gitter's)
flight abandoned without merge -> writers stopped -> main chat -> worktree.sh remove (gitter on a refusal or without the script)
```

1. **Settle.** The main chat settles open requirements with the user before any dispatch.
2. **Place.** The main chat makes the worktree with the repo's `worktree.sh create` where it has one (gitter makes it on a refusal and in a repo without the script), or names `develop` or the checkout, and passes the path in the brief. Several foremen may share one worktree, cut by tier into the fewest foremen and built core first (`templates/global/rules/flights-life-cycle.md` rules 3–5). A flight builds software, program source and its tests; docs and framework files (prompts, rules, commands, agent definitions, config, scripts) are never a flight, whatever judgment they hold: the main chat edits them on the current branch with no worktree, foreman or lander (through the agent a guard reserves the files to, such as `pcm`, else by hand), and gitter commits on the passing check. Size counts the code edits alone, never the file count: code work whose every edit is fixed in advance (a rename, a move, a known fix, a spec fixing every line), with no other agent writing its files, is small at any file count, made by hand on the current branch with no worktree and no lander; code work with judgment left is a flight in `.worktrees/{flight}/` (`templates/global/rules/flights-life-cycle.md` rule 2). Related work joins the open flight's worktree, code or not, small or not; that is the only way non-code work enters a worktree.
3. **Build.** A foreman builds the request by its own hand.
   - **Reads:** what the build needs, in three rounds; see [reading](#reading-and-the-trace-store).
   - **Writes:** the tests and the change in one round, the new tests run on the unchanged code first, then one foreground run of the affected tests and static checks, then one probe script.
   - **Children:** none; the foreman spawns no agent.
   - **Returns:** one message; every item the brief names is built, or the return is `PARTIAL` naming it.
4. **Land.** Once every foreman on a worktree has returned, the main chat spawns one `flights:lander`. The lander:
   1. syncs the worktree to TARGET's tip;
   2. reviews the whole change itself, spawning the children its inventory's PLAN computes;
   3. fixes every finding;
   4. runs the affected tests until green;
   5. runs the full gate once;
   6. returns.

   One worktree gets one lander: no separate reviewer, no second round, no second lander.
5. **Merge and tear down.** On `PASS` or `FIXED`, the main chat runs `worktree.sh land {name}` where the repo has it: it commits a dirty worktree (`-m`), fast-forwards the base and removes the worktree and branch, one stderr line and a non-zero exit on every refusal. Gitter takes every refusal and every repo without the script: it commits and merges with the lander's report path as its REPORT_PATH, and teardown belongs to that same MERGE dispatch. Either way `git worktree list` verifies it. The return ends `worktree removed: {path} · branch {deleted|kept}` or `worktree kept (locked): {path} — {reason}`. A needed keep is `KEEP: {reason}` in the brief, never a teardown ban; gitter keeps the branch and locks the worktree with that reason. A reasonless removal ban becomes a lock with reason `kept by caller, no reason given`. The main chat that kept it carries the `kept (locked)` line in its Verdict until it orders gitter to unlock and remove it.
6. **Close without a merge.** A lander `FAIL` not retried, an abandoned foreman `PARTIAL`, a dropped flight or an abandoned dead agent's flight still closes, after its writers stop. The main chat removes the clean linked worktree with `worktree.sh remove` where the repo has it (gitter on a refusal and without the script), keeping and naming any unmerged branch (`-d` only, never `-D`). Dirty work is locked with the reason it waits and the user is asked what to do with its changes; the Verdict names what stayed. The main checkout is never a close target.
7. **Measure.** `/flights:doctor`, run by the user, reports the measure for the tree.

## Choosing a tier

The main chat chooses at dispatch; a foreman spawns nothing.

| Choice | Decided by | Rule |
| --- | --- | --- |
| Foreman tier | difficulty and ambiguity, never size | `flights:foreman-sonnet`: the rest, edits a pattern fixes (existing code, a rule, the finished core), its brief naming that pattern, a large rest split by area · `flights:foreman`: the core, what the rest depends on or copies and all judgment left (design choices, ambiguity, unfamiliar code, an unknown cause, several modules) · all judgment or all pattern: one foreman |
| Lander children | the lander's inventory PLAN, computed from the diff and the spec files | diff-checkers k = min(4, ceil(production diff lines / 600)) · spec-checkers per spec with 10+ numbered rows · usage-checkers by inventory row ranges |

The lander itself has no tiers: `flights:lander` lands every worktree.

## What passes between agents

In the build/land lifecycle:

- **Messages only.** A brief is the spawn prompt, and a return is the final message.
- **No build/land files.** The build/land protocol creates no flight directory, run log, brief file, return file, gate file or spec file.
- **The one path that travels** is a tracer run, when one exists, named in a brief so the receiver may reuse it (a foreman spawns no tracer).
- **A foreman's brief carries:**
  - the work and its acceptance;
  - where to work;
  - the testing manual path, if one exists;
  - a tracer run path, when one exists.
- **A foreman's return:**
  - first line `DONE {name}` or `PARTIAL {name}: {what is left}`;
  - the fixed block of 25 lines at most: `ACCEPTANCE`, `DECISIONS`, `CRITICAL`, `FILES`, `TESTS`, `PROBES`, `UNTESTED`, `OUTSIDE` ([flights-foreman.md](flights-foreman.md#the-return)).
- **A lander's return:**
  - first line `PASS`, `FIXED` or `FAIL {worktree}`;
  - then each finding, fixed or listed;
  - the sync outcome;
  - each project's last full-gate verdict line;
  - the residuals and decisions;
  - the lander's report path.

## Reading and the trace store

- **The foreman reads what the build needs:** the files it changes, the code it calls at its signatures and contracts, and the closest pattern once.
- **A foreman calls no tracer.** It reads in three Bash-batched rounds; a tracer run path in its brief may be reused.
- **The store is per repo.** Every tracer run lives under `/tmp/{project}/tracer/`, and each answer records the sha256 of every file it cited.
- **The tracer checks the store first.** Recall searches every earlier run of the same repo root, with no age limit.
  - An answer whose cited files all match their recorded hashes is fresh: it is returned with no walk.
  - An answer with changed files re-walks only the threads that cite them.
- **No hook prepares, forces or gates a tracer.** The tracer opens its own run and recalls as its first command.

## Waiting

- **Agents** are waited on by ending the turn. Their return arrives as a notification.
- **Commands** run in the foreground with a timeout of at most 270 s, in `flights:lander`, `flights:foreman` and `flights:foreman-sonnet`. One that can run longer runs in the background (`{command} > {log} 2>&1; echo $? > {log}.rc`) and is awaited by repeating `timeout 270 bash -c 'until [ -s "$1" ]; do sleep 15; done; cat "$1"' _ {log}.rc` until it prints. Why: past 300 s the prompt cache expires and the next request re-pays the whole context; over 239 past landings the requests that followed one wait of 290 s or more cost $77, against $9 in 270 s slices.
- **Never end a turn while your own command runs.** An agent woken by its own finished background command can have its final notice delivered to the main chat instead of its parent.

## Tests

- **Who runs what:**
  - a foreman runs the new tests red on the unchanged code, then the affected tests and static checks once after the change, then one probe script;
  - on a flight, only the lander runs the full gate, once per worktree right before its merge, after every review finding is fixed and the affected tests are green;
  - on small code work, the main chat runs the full gate when the change spans layers or the project's done-gate requires it, then hands gitter the passing check.
- **A red full gate:** the lander fixes it, gets the failing tests green with the affected-test command, never a gate rerun, then runs that gate once more. That is the only repeat.
- **The testing manual:** a project's testing manual, where one exists (this repo's `.claude/commands/pfm-testing-manual.md`), names the affected-test command and the full gate. Without one, agents use the project's documented test commands.
- **Where tests run:** this repo runs every test in the fence through `.claude/scripts/test.sh`.

## Effort buttons

The `auto-effort` plugin gives an agent whose frontmatter carries `effortControl` three tools, `effort_up`, `effort_down` and `effort_auto` (`mcp__auto-effort__…`), and steers its effort while it works (`plugins/auto-effort/README.md`). `effortControl: on` takes the plugin's default band, floor medium and ceiling xhigh.

| Agent | `effortControl` |
| --- | --- |
| `flights:foreman`, `flights:foreman-sonnet` | floor medium, ceiling xhigh; `flights:foreman` starts at medium, `flights:foreman-sonnet` at high |
| `documenter` | `on` |

`pcm` does not carry `effortControl`. `flights:lander` and its children carry no `effortControl` and no buttons. Every steered agent but the two foremen lists the three tools in its own `tools:` line under the `mcp__auto-effort__` prefix; the two foremen carry no effort buttons and a `tools:` line without them. The plugin steers a foreman: one step up at a failing test run, home at the next green ([flights-foreman.md](flights-foreman.md#effort)).

## Decisions

- **A failing gate is fixed in the code.** A foreman raises a limit only through the project's documented exception route, the reason recorded there and in its `DECISIONS`; the lander judges it.
- **Never ask.** A foreman or lander never asks the user. It decides, and lists every decision first in its return.
- **Critical decisions go to the main chat, without stopping.** A critical decision is one where being wrong costs data, privacy or patient data, money, security, or anything irreversible. The agent sends it to the main chat (`SendMessage(to: "main")`) with the choice it is proceeding with, then keeps working. If an answer arrives that differs, it adopts it.
- **The place is the parent's.** A foreman's prompt names no worktree: it works where its parent points it, and the main chat owns the place, the landing and git.

## What the build/land lifecycle never carries

| Absent | Instead |
| --- | --- |
| Agent hooks (cut gate, call cap, stop hooks, wait denial) | the protocol in the agent file |
| A call or round cap | the foreman's three reading rounds and one write round are its protocol |
| A foreman's child | the main chat splits work across foremen |
| Flight files and a run log | messages |
| Review lenses (a security seat, fixed tracers) | the lander's hunt and the children its computed PLAN spawns |
| A second gate round | fixes inside the one lander |
| A required specification or requirements interview step | the main chat settles requirements; the user may independently request `flights:speccer` |

## Harness settings

The deepest chain is main → `flights:speccer` → `flights:tracer` → `flights:sub-tracer` → its child `flights:sub-tracer`: four levels below the main chat; a foreman spawns nothing and sits at one. pfm writes `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` and `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` onto every Claude Code launch line (`pfm/internal/claudelaunch/knobs.go`). A spawn refused at the concurrency limit never started, and goes out again once a slot frees.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `plugins/flights/plugins/flights/agents/*.md`, shipped by the `flights` plugin as `flights:{name}`; Codex and OpenCode keep the hyphen names `flights-{name}` | The protocols |
| The lander's kit | `plugins/flights/plugins/flights/skills/lander-inventory/` | The inventory and the computed spawn |
| The command | `plugins/flights/plugins/flights/commands/doctor.md` | The meter |
| The measurement | `pfm tokens --tree` in `pfm/internal/tokens/` | The measure's numbers |
| Dispatch routing | `plugins/flights/plugins/flights/agents/foreman.md`, `plugins/flights/plugins/flights/agents/foreman-sonnet.md` — `description:` | Foreman suitability; main chat → foreman → main chat → flights:lander → gitter |
| Landing | `plugins/flights/plugins/flights/agents/lander.md` | Main-only landing after every foreman returns: sync, review, fix, affected tests, full gate, report |
| The tracer | `plugins/flights/plugins/flights/agents/tracer.md`, `plugins/flights/plugins/flights/skills/tracetree/`, `docs/design/tracing/` | The store and its reuse |
| This directory | `docs/design/flights/` | One file per member, this file for what they share |

## Open items

- The baseline before this design: a median of 41 landed lines per million input tokens over 166 flights in the 14 days to 2026-10-18. Agents that wrote no code took 61% of the input. The first flights on this design are measured against it with `/flights:doctor`.
- The lander's PLAN constants (600 lines per diff-checker, 10 rows per spec-checker, the caps) are first values; tune them from `/flights:doctor` reports.
