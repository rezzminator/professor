---
name: flights:spec
description: 'MANDATORY — the main chat''s only way to start flights-speccer: /flights:spec [tasks | file] maps the area, grills the user until no gap is left, hands the rulings to flights-speccer. /flights:spec → flights-speccer → /flights:orchestrate-{nested|live|cross-harness} → flights-orchestrator → flights-*-executor → flights-lander → /flights:audit. Returns the index.'
argument-hint: [tasks | task file]
---

# Spec — specify a flight

One deliverable, written by `flights-speccer` and never by you: `$HOME/.local/state/pfm/flights/{project}/{flight}/` holding `index.md` and one task file per executor. You map, grill, hand off and present; running it is the user's next command. Input: $ARGUMENTS — an inline task list, a file holding one, or nothing (then ask what the flight is for). Choose `{flight}`: short kebab-case; on a collision under `$HOME/.local/state/pfm/flights/{project}/` append `-v2`.

## S1 — Walk the code

Read the child `CLAUDE.md` of every project the tasks touch (the root one you already hold): the specs must stay inside their rules, and S2 must not re-ask what they settle. Then one message of probes, one per area the tasks touch: `Agent(subagent_type: "tracer")` for a question (who feeds X, where Y ends), `Agent(subagent_type: "mapper")` for a whole area. Each returns a map of about one page with no file content quoted: where the area lives, who writes and reads it, which files would change, where the codebase already solves a similar problem, how the area is checked, and every referent the tasks name that does not exist. End your message after spawning; the maps arrive on their own. Probes retrieve; you judge and ask.

A referent that does not exist, an edit two tasks would both make to one target, or a dependency nobody can order is a question for S2, never a silent fix.

## S2 — Grill the user

Build the design tree from the tasks and the maps: the root is the flight's goal, each branch a decision the flight rests on, each leaf a decision that hangs off another. A decision the tasks, the maps or a child `CLAUDE.md` settle is settled; every other is open, and the tree holds all of them:

- Technical: every branch with two or more defensible options and materially different consequences (transport, data placement, migration, failure behaviour).
- Product: who the change is for, what must be true when it lands, what it must never do, what the user sees.
- Touchpoints: every moment the flight would need the user (a secret, a deploy review, a destructive operation, a merge nod), pre-authorised now or cut from scope; a flight that stops mid-run for a user answer is a failed spec. A task that moves protected data gets a question about its channel, never a default.

Work the tree in rounds. The frontier is every open decision whose prerequisites are settled: a question whose answer depends on another question still open waits for a later round. Round one always holds the scope boundary (the user's whole objective restated, what this flight includes, what it defers — scope never narrows silently) and every task the maps could not ground: specify, defer or drop. Ask the whole frontier in one plain-text message, then end it and wait:

```
**Q1 — {title}**: {the question, its context and its options}
Recommended: {your answer and the one reason}

**Q2 — {title}**: …
```

Each answer reshapes the tree: settled decisions push the frontier outward. Recompute it and ask the next round. A task the user drops or defers is a ruling like any other.

Facts are yours to find, never the user's: a question that needs a fact from the code goes to a probe (`tracer` for a question, `mapper` for a whole area), and only the questions downstream of a running probe wait for it. Decisions are the user's: put each one and wait.

The grill ends when the frontier is empty: every branch visited, nothing silently assumed. Restate the rulings as one numbered list and hand off only once the user confirms it is the shared understanding.

## S3 — Hand off

Spawn `Agent(subagent_type: "flights-speccer")` with content, never a format: the numbered tasks, the confirmed rulings from S2 as binding decisions, the maps, the boundaries (out of scope, files another owner holds), the standing rules (the child `CLAUDE.md` paths the tasks touch, and the flight's own: worktree, fence, checks), the testing manual of each project touched, and the directory `$HOME/.local/state/pfm/flights/{project}/{flight}/`. End your message; the return arrives with the index.

## S4 — The one question

A `BLOCKED` item in the return carries a question. Put it to the user in one more round holding that question alone, and send the answer back to the same `flights-speccer` by `SendMessage` as a revising call with the ruling. A `BLOCKED` item still in the revised return stays `BLOCKED` in the presentation: the flight runs without that task. At most one question round per flight; a second means S2 was skipped.

## S5 — Present

The index table as returned; one line per task naming its key decisions (the `Decisions` sections extracted from the task files in one call, never the files opened whole: your context outlives this flight); the `NOTES` lines; any `BLOCKED` item. Then the three ways to run it: `/flights:orchestrate-nested` (a `flights-orchestrator` sub-agent, one return), `/flights:orchestrate-live` (this chat runs it, the user watches), `/flights:orchestrate-cross-harness` (chat seats of another engine as executors). Approval is a reading, not a run.
