# general-foreman

`general-foreman` owns one problem with no spec: it digs to the cause, decides the fix, builds it or briefs [`general-executor`](general-executor.md) to build it, proves it at the Goal and returns once. A main chat sends it one problem; [`general-orchestrator`](general-orchestrator.md) sends it one problem of a batch.

Decisions live in this file. The executable wording lives in `templates/global/agents/general-foreman.md`.

## Contents

- [Why it exists](#why-it-exists)
- [The run](#the-run)
- [Build or brief](#build-or-brief)
- [The executor's brief and reactions](#the-executors-brief-and-reactions)
- [Siblings and calls](#siblings-and-calls)
- [Tier, cap and concurrency](#tier-cap-and-concurrency)
- [The return](#the-return)
- [The bench](#the-bench)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Evidence](#evidence)

## Why it exists

The general family does live work without a speccer: a flight plans first and executes a spec, a general problem is worked out in place by a smart agent. That agent is the foreman. Judgment never delegates downward, so the foreman is the one that reads to the cause and decides; what it hands a sonnet executor is a change the decision already fixes. It is also the typed agent for one unsolved problem sent from a main chat, which otherwise goes to an untyped `general-purpose` with no cap, no return token and no watched-failing test (Evidence).

## The run

1. Goal: what is observably true once the problem is solved — what a person or a command sees — and the check that shows it. The first message opens the ranges the brief cites and searches the brief's area for the problem's symbols, all at once. The area bounds the changes and is never a reading list: a foreman told to open every file an orchestrator's area named read three whole files before its first search.
2. Dig: reproduce the failure or the gap before changing anything, then read to the cause — the line, the value, the code path. Live-only behaviour is probed live. A question spanning many files goes to `tracer`, exact text to `collector`, so the foreman's own reads stay on the unit it will change.
3. Decide: the cause and the fix, one line each, and one line per rejected alternative. An invariant held at several doors is enumerated before the build: the foreman names every symbol that yields the thing — each function, constant, env var and literal, never one function's callers — searches each one in one chained call, and builds or rules out every hit by its own `path:line` and reason. The return's `DOORS` lines, one per symbol searched, name every hit, so a caller sees a missing symbol or an unruled hit instead of trusting a count. With only a count to fill, a foreman searched `paths.DefaultStateDB`, got the installer's fallback back among the hits, and ruled the rest out as a group ("never reach a database"): the one door the symbol itself named went unexamined. A symbol whose value already carries the invariant is ruled out once, as a symbol: named hit by hit, a foreman sent a `tracer` through every consumer of the already-configured runtime paths, 197K characters read to rule out hits that could never be doors. A decision that belongs to the caller returns `BLOCKED` with nothing changed.
4. Build or brief (below).
5. Wait by ending the turn; the frontmatter hook `pfm internal orchestrator-wait` denies a Bash call that only waits.
6. Verify each executor return against the disk: the first-line token, `git diff --stat` inside its files, its check line as printed, the diff read against the decision.
7. Prove at the Goal: the step-2 reproduction passes and the covering test was watched failing. Live-only behaviour gets a live check; one the foreman could not run is returned as `UNPROVEN`, never as done.

## Build or brief

The foreman builds the change itself when what remains is about ten calls or less, when the deliverable is a document, prompt, spec or report (sonnet never writes prose someone acts on), or when the change and the diagnosis are one loop (a live probe rerun per edit). Otherwise it briefs `general-executor`: at most two, on disjoint files, in one message, with no model or effort override, so the executor runs at its pinned tier.

## The executor's brief and reactions

The brief is inline and leaves no judgment open: the change's `{id}` and goal; its files and what is not its; the exact change with every door by `path:line` and every interface pinned; the check and the testing manual path; the standing rules, the worktree and what already landed.

| Return | Reaction |
| --- | --- |
| `DONE` verified | Recorded |
| `DONE` not verified | Treated as `FAILED` with that cause |
| `FAILED` or `SPEC-DRIFT` | The brief was wrong until shown otherwise: read to the cause, one re-dispatch with a changed brief, or the foreman finishes it; a second red on one change returns `FAILED` with both causes |
| `BLOCKED` | Answered by `SendMessage` to the same executor |

## Siblings and calls

- A foreman under `general-orchestrator` shares its worktree with sibling foremen, which the brief names with their areas. A red whose cause sits in a sibling's files is never edited: the foreman reruns after its next change and reports the red if it stays. Without the line, a foreman reads a sibling's half-written file as its own break and either repairs another problem's code or burns calls on it.
- A call carries all the work it can: independent reads, searches and commands in one message; dependent steps that need no judgment (a history walk, a check and its run) chained into one shell call. A sub-agent never receives the fleet prompt's § Command execution, so the body carries it.

## Tier, cap and concurrency

- Tier: `opus` at effort `high` on Claude (the smart tier measured for the flight executors), `gpt-6-sol` at `high` on Codex. Inside a batch, `general-orchestrator` opens every brief with an effort line: `[effort: medium]` for a problem inside one package or module with no invariant held at several doors, `[effort: high]` otherwise ([general-orchestrator.md](general-orchestrator.md#the-brief)).
- Cap: 45 calls of its own; each executor has its own 45. The worst case is one opus run of 45 calls plus two sonnet runs of 45.
- Concurrency: a foreman with two executors is three agents. Claude Code admits about twenty sub-agents at once and refuses a spawn past its ceiling silently, so `general-orchestrator` keeps at most six foremen in flight.
- Registry: a running chat refreshes its agent types at its next turn, so a spawn in the same turn as the `pfm install` that linked the foreman fails with `not found`; from the next turn the foreman is there and the retired types are gone.

## The return

First line `DONE {id}`, `FAILED {id}: {why}` or `BLOCKED {id}: {question}`, then labelled lines: `CAUSE`, `DECISION` with the rejected alternatives, `DOORS` (one line per symbol searched, every hit named built or out with its reason), `FILES`, `CHECK` with the covering test's watched-failing proof, `HANDS` (one row per executor, or built itself), `UNPROVEN`, `OUTSIDE` (defects found beyond the problem, untouched), `RETRO`. The labels are what `general-orchestrator` and a main chat verify against.

## The bench

Two replayed pfm fixes, each from its parent commit in a detached worktree, briefed as the symptom only; the landed commit is the answer key. `general-purpose` got the identical prompt, effort prefix included. Judged on: correct (the landed regression test, dropped in afterwards, passes; every door built or ruled out), cause named, proof at the Goal, return shape, discipline (caps, no reads outside the worktree, no git writes), cost, wall time and peak context.

| Task | Parent | Shape |
| --- | --- | --- |
| `mcp-ledger-link` | `73048112^` | A ledger lookup that misses keys written through a symlink; one package |
| `state-db-config` | `b32201ec^` | Nine database openers ignoring the configured path; one resolver, many doors |

Cost is the run's whole tree priced from its transcripts, thinking counted as output and each message once; a fork's transcript repeats its parent's first messages, so per-file sums overcount. Two identical `general-purpose` runs differed by a third in cost, so a single run ranks nothing within about 20%.

| Task | Runner | Runs | Cost, mean | Wall, mean | Peak context, mean | Correct |
| --- | --- | --- | --- | --- | --- | --- |
| `mcp-ledger-link` | `general-purpose` | 1 | $1.05 | 165s | 94K | The landed test passes |
| `mcp-ledger-link` | `general-foreman` | 2 | $0.75 | 142s | 61K | The landed test passes, both runs |
| `state-db-config` | `general-purpose` | 2 | $3.01 | 821s | 158K | 8 of 9 doors, both runs, the ninth never examined; the second run took 71 requests in one context |
| `state-db-config` | `general-foreman` | 5 | $2.79 | 806s | 112K | 8, 9, 8 and 8 of 9 doors built; the fifth built 8 and ruled the ninth out with a reason that holds |

The counts are uneven, and the foreman's runs are not one prompt. Only `state-db-config`'s `general-purpose` baseline was replicated; `mcp-ledger-link`'s rests on one run, so its lead counts only because it clears that 20%. The foreman's runs span the prompt's revisions — its two `mcp-ledger-link` runs two wordings, its five `state-db-config` runs five — so the means describe the foreman as it was revised, not the final wording, which has not yet run against `general-purpose`.

The ninth door, the installer's fallback to `paths.DefaultStateDB`, is dead in production: the one constructor of the installer's options sets the path from the loaded runtime first. Only the per-hit `DOORS` line made a foreman say so.

Defects the bench found, each closed by wording recorded where it landed:

- The door search followed one function's callers → Decide names every symbol that yields the thing.
- The executor's brief carried no name → the brief's first item is the `{id}`.
- The foreman prefixed its spawns with `[effort: medium]`, below the executor's pinned `high` → no model or effort override.
- An area read whole before the first search → step 1 searches first (§ The run).
- A sibling's half-written file read as the foreman's own red → § Siblings and calls.
- About 1.1 tool uses per request, and an unquoted glob that failed under zsh → the calls law.
- A searched symbol's hit swallowed by a group ruling, then a `tracer` sent through an already-correct symbol's consumers → every hit ruled by its own `path:line`, a symbol that carries the invariant ruled once (§ The run, step 3).

Every run proved its covering tests red before green, reported what it could not run as `UNPROVEN`, made no git write and touched nothing outside its worktree.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/general-foreman.md` | The executable wording |
| The hand | [general-executor.md](general-executor.md), `templates/global/agents/general-executor.md` | What the brief carries, the return the foreman verifies |
| The orchestrator | [general-orchestrator.md](general-orchestrator.md), `templates/global/agents/general-orchestrator.md` | The brief to a foreman, the reactions, the in-flight ceiling |
| The wait guard | `pfm internal orchestrator-wait`, attached in the agent's frontmatter | A Bash call that only waits is denied |
| The Codex pin check | `pfm/internal/codexgen/globalagents_test.go` | The roles that carry a Codex role pin |
| The roster | `docs/BLUEPRINT.md`, `docs/SETUP.md`, `templates/project/commands/pcm.md`, `docs/design/flights/testing-manual.md` | The agent listed |

## Evidence

Fourteen days of transcripts across every account on the host that built the family:

- Main chats spawned `general-purpose` 657 times (330 on `opus`), `general-orchestrator` 16 times and `flights-speccer` 64 times. The build and fix slice of those `general-purpose` spawns is the foreman's work.
- Inside orchestrated batches, the three former general executors — one mechanical body rendered at three tiers — ran 118 times at the smart tier, 34 at mechanical and once at precise; 19 smart runs passed 45 calls. The smart tier was an `opus` model reading a body written for work that needs no judgment.
- One batch rated five of seven tasks smart; one returned `DONE` on green unit tests while its user-visible behaviour, checked live, had not changed.
