---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: law:agent
description: "MANDATORY — load before writing or editing an agent definition; `build <name>` grows one from a fixed bench, `improve <agent>` raises one run by run, `check [path…]` certifies each (APPROVED/REJECTED). Prose → /law:prompt; a `description:` → /law:description."
argument-hint: "[build <name> | improve <agent> | check [path…]]"
---

# Agent Law

An agent definition is a whole system prompt: the spawned agent sees its own file, its brief and the environment, nothing else, and its `description:` rides every session that can spawn it. Every line earns its place on a fixed bench, run by run, and the run that earned it stays on record. `/law:prompt` governs each line's prose, `/law:description` the `description:`; this law governs what the file carries and how it is grown. The evidence behind each rule: `docs/design/law/agent.md` in the Professor clone (`readlink -f ~/.claude/commands/law/agent.md`, then its directory's `../../../..`).

## Modes

- No argument: § The file, applied while writing.
- `build <name>`: § Settle first, then § Grow it from a greenfield.
- `improve <agent>`: § Settle first, then § Grow it from the agent as it ships.
- `check [path…]`: § Approval, read-only.

`build` and `improve` end by running `check` on the result.

## Settle first

Before any read, run or edit, one AskUserQuestion round settles four things:

1. Axes, multi-select, all four recommended:
   - task accuracy: facets of a key built before any run, scored blind
   - cost per run: USD, requests and seconds, priced from the run's transcripts
   - zero wrong claims: wrong statements and broken return contracts
   - prompt leanness: bytes of the definition and of everything it preloads
2. Target and goal: the agent's name and the one job it does for its caller; for `improve`, where it loses most today.
3. Bench input: a real past brief its callers sent (default: the heaviest, found in transcripts) or one the user names.
4. Budget: the USD cap and the run cap.

Under god speed nothing is asked: all four axes, the goal read from the request, the heaviest real brief as the input, the request's caps. Each lands as one ruling line on top of the lab brief, e.g. `Ruling: axes = all four (god speed) — flip: delete this line and ask`.

The axes fix the metric before the first run: value = (facets delivered ÷ key) × (claims right ÷ claims checked) ÷ USD, an axis left out dropping its term; a broken return contract blocks shipping at any value; at equal value within the spread, the smaller file wins.

## Grow it

Run the loop as an `/improve:iterative` lab on the agent, the axes as its metric, and load `/ultimate` for every design round; the two own the loop's mechanics. An agent adds these steps:

1. Greenfield: `build`, and an `improve` whose agent two rounds could not move, start from the goal and the axes alone. The first version is the boldest design the goal admits (a lead over cheaper readers, a script holding what prose kept losing, a different split of the work), never the old prompt reworded.
2. Bench, fixed before any run:
   - input: the settled brief, verbatim, over a source whose facts can be checked
   - key: an independent judge (a fresh top-tier agent that designs nothing) builds a facet key from the source before any return exists; a facet later proved wrong against the source is corrected for every later judge
   - scoring: each return under a neutral letter, each facet DELIVERED 1, PARTIAL 0.5, MISSING or WRONG 0, a declared NOT READ never WRONG; a script scores what anchors can match (a facet by pattern, a `path:line` cite against its line)
   - cost: every request of every agent the run spawned, from its transcripts (`/chatlog`, `pfm tokens`), at the price table's rates, with the lead's share
   - contract: the return read line by line against the agent's own return and failure shapes
3. Theories: each version `v{N}` is one named theory, written in the ledger before it runs: the one change and the axis it should move. Two runs at least per version, since two runs of one agent on one brief can differ as much as any prompt change. A loser is reverted, its row kept as the reason the rule is absent.
4. Model and effort: once a body stands, run it on each candidate model × effort, tabled as shares of the strongest setup on price, accuracy and the agent's own quality axis; ship the cheapest setup inside the accuracy spread. Effort is not monotonic: bench both neighbours of the pick.
5. Tiers: a tier ships only where the bench separates a caller's need it alone serves (a deeper cap, a lane, a model); one no caller's need separates is retired. A rival agent doing the same job is benched head-to-head on one key, and the loser deleted.
6. Harder inputs: each won round climbs to the heaviest past briefs replayed verbatim, an ambiguous brief, the largest input the agent will meet, and for a coordinator the input that must trigger its rare decision (a split, a refusal). A rung every version ties on cannot discriminate and is replaced. An agent meant for many worlds climbs across them (`/improve:iterative` § Frame — before round 1, step 4).
7. Callers: once a round, a cheap chat given a vague task and told to use the agent runs it, and `agent-investigator` audits that whole session. What callers misread changes the return shape or becomes a caller manual preloaded through the callers' `skills:`.
8. Field runs: after it ships, sweep its real runs over a window, re-check each output with the agent's own instrument, join each to its transcript, count contract breaks per run and audit the outliers; each finding becomes a rule or a mechanism with its count.
9. Record: the design doc holds each rule with its why and the bench row it rests on, a model section with its bench, and an open item for everything unbenched; the bench ledger beside it holds the input, the key's size, the judge, the price table and every run row.

## The file

### Frontmatter

- `name`: kebab-case, the file's stem, a role noun (`tracer`, `collector`).
- `description`: per `/law:description`, naming the brief's fields as `Pass {fields}`.
- `model`, `effort`: the setup step 4 picked, on the fleet's Model Selection tiers. A lead whose output someone acts on runs smart; a reader that returns raw material runs collector (`haiku`) at `low` or `medium`.
- `tools`: exactly what the body calls. An omission can be the guard: the harness refuses a `Write` over a file an agent without `Read` has not read, so a taken path fails loud instead of being overwritten; a design doc names every such omission. A tool name the harness no longer serves fails silently: the agent never calls it.
- `visibility.visible-to`: an agent spawned by one caller alone lists that caller, its description opening with the `{CALLER}-ONLY` class.
- `skills:` preloads a manual the agent must follow without a Skill call; `hooks:` holds an invariant prose kept losing. Both load only when the agent spawns as its registered `subagent_type`, never for a `general-purpose` agent reading the file.
- `engines.codex.sandbox: workspace-write` where a tools list without a write tool still runs a script that writes (a kit's run directory): Codex compiles such a list read-only. Every key compiles for Codex and OpenCode: `engine-sync codex check .` and `engine-sync opencode doctor .` stay clean for a project agent, `engine-sync doctor --machine` for a machine one.
- The compaction tools: per the `pcm` agent's new-agent rule.

### Body, in order

1. Role: one sentence, the input it turns into which output (`You turn a caller's numbered orders into ONE run of …`).
2. Brief contract: every field a caller passes, spelled as the brief writes it (`RECALL: {run}`, `BUDGET {n}`, `Report: {path}`), its default when absent, and what a brief missing a required field returns.
3. Procedure: numbered steps, each naming its tool call; every costly step counted (`exactly one Bash call before spawning`, `at most two drills after each wait`, `one gap round`). A spawning agent waits inside one blocking call or by ending its message, never by polling.
4. Budgets: a default cap the brief may override and a cost formula a run is checked against (`r(d + 1) + 6` lead calls); a cap that cuts work names what it cut under NOT READ.
5. Return: a fixed first line the parent can match (a path, a verdict word), fixed sections in order, a size bound; detail goes to a file whose path the return carries.
6. Failure: one mark per failure kind, one vocabulary across the agent and its callers; an error never shares a mark with an absence (`SEARCH FAILED` against `NOTHING FOUND`); a failed write returns `NOT SAVED — {reason}` with the content inline.
7. NOT READ: every asked item not read, each with its next step; a list claim names each item, or counts the matches and names the exceptions.

A contrastive example enters only where a bench run shows its rule leaking, written per `/law:prompt` § Teaching by example; where the model copied a ✗ line into its output, the plain rule stays alone.

### Derived tiers

- A tier differing from its base only in model, effort, caps, tools or one lane is a `from: {base}` declaration in the machine-global agents source, never a copy: its frontmatter keys override, `add:` appends, `unset:` drops, `extends:` layers a `shared/` file, and each `## {Heading}` in its body replaces the base's section of that name under a `pins:` hash (engine-sync's `docs/interface.md` § Declarations).
- The base's steps cite the sections a tier replaces by name (`§ Budget`), so a tier changes a cap without touching a step.
- A tier picked by name alone carries `{base} tier {n} on {model} {effort}`, the base's description holding the chain (`rr → rr-pro → rr-pro-max`); one a caller picks by the work gets a full routing description.
- After a base edit, review each override, then `engine-sync variants pin {tier}` and render.
- Declarations are machine scope: a project `.claude/agents/*.md` carrying `from:` is refused.

### Load

An agent type registers at session start: an edited file runs in the next session, and a new one answers `Agent type '{name}' not found` until the harness reloads, so every bench run starts a fresh chat.

## Approval

`check [path…]`: no path means every `.md` under the project's `.claude/agents/` and `~/.claude/agents/`, symlinks resolved, `shared/` skipped; a declaration is checked on its render. Emit `APPROVED: {path}`, `REJECTED: {path} — checks {n,…}` with each failure in one line, or `UNREAD: {path} — {error}`; only a file read earns a verdict.

1. Frontmatter: YAML that does not parse; a `name` other than the stem; `model` or `effort` neither set nor inherited through `from:`.
2. Description: `/law:description` § Approval fails.
3. Tools: a tool the body calls missing; one listed the body never calls and no design doc names as a guard; a `{CALLER}-ONLY` agent without `visibility.visible-to`; a writing script under a read-only list without `engines.codex.sandbox: workspace-write`.
4. Brief: a field the body reads that no line names; a required field with no missing-field behaviour.
5. Procedure: steps not numbered; a fan-out, re-read, retry or wait loop without a count.
6. Budget: a cap without a default; a cut that leaves no NOT READ line.
7. Return: no fixed first line; sections not fixed in order; no size bound where the return grows with the input.
8. Failure: a failure kind without its mark; an error and an absence sharing one; a failed write that returns quietly.
9. NOT READ: absent, or an item without its next step.
10. Evidence: the model, effort or a cap rests on no recorded run, and no open item names it unbenched.
11. Declaration: a `from:` file restating base text it does not override; a replaced section without its pin; a render that differs from a fresh one.
12. Size: over the `pcm` agent's agent cap, where it sets one.
13. Prose: the `/law:prompt` pre-commit self-check fails.

The key exists before the first run, and a line stays only on the runs that earned it.
