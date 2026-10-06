---
name: flights:spec
description: 'The main chat''s front of a flight whose requirements are not settled — /flights:spec [work | file]: walks only what asking needs, grills the user until no gap is left, writes requirements.md, hands it to flights-foreman. /flights:init → /flights:spec → flights-foreman → flights-lander → /flights:audit. Returns the foreman''s return.'
argument-hint: [work | file holding it]
---

# Spec — settle a flight's requirements

One deliverable written by you: `$HOME/.local/state/pfm/flights/{project}/{flight}/requirements.md`. You walk, grill, write and hand off; `flights-foreman` reads the code itself and builds it. Input: $ARGUMENTS — the work inline, a file holding it, or nothing (then ask what the flight is for). Choose `{flight}`: short kebab-case; on a collision under `$HOME/.local/state/pfm/flights/{project}/` append `-v2`.

## S1 — Walk only what asking needs

Read the child `CLAUDE.md` of every project the work touches (the root one you already hold) and the project map `$HOME/.local/state/pfm/flights/{project}/project-map.md`: S2 must not re-ask what they settle. A fact a question needs goes to `Agent(subagent_type: "tracer")` as numbered questions, all probes in one message; end your message after spawning, the maps arrive on their own. The walk stops at what a good question needs: the foreman reads the code to build it, so a map built here for it is reading paid twice.

A referent that does not exist, or two parts of the work that would edit one target, is a question for S2, never a silent fix.

## S2 — Grill the user

Build the design tree: the flight's goal at the root, each decision the flight rests on a branch. A decision the work, the probes or a child `CLAUDE.md` settle is settled; every other is open:

- Technical: every branch with two or more defensible options and materially different consequences (transport, data placement, migration, failure behaviour).
- Product: who the change is for, what must be true when it lands, what it must never do, what the user sees.
- Touchpoints: every moment the flight would need the user (a secret, a deploy review, a destructive operation), pre-authorised now or cut from scope; a flight that stops mid-run for a user answer is a failed grill. Work that moves protected data gets a question about its channel, never a default.

Work the tree in rounds. The frontier is every open decision whose prerequisites are settled. Round one always holds the scope boundary (the user's whole objective restated, what this flight includes, what it defers — scope never narrows silently). Ask the whole frontier in one plain-text message, then end it and wait:

```
**Q1 — {title}**: {the question, its context and its options}
Recommended: {your answer and the one reason}
```

Each answer reshapes the tree; recompute the frontier and ask the next round. Facts are yours to find, decisions are the user's. The grill ends when the frontier is empty; restate the rulings as one numbered list and go on only once the user confirms it is the shared understanding.

## S3 — Write the requirements

Write `requirements.md`:

- `## Rulings`: the confirmed decisions, numbered, binding.
- `## Requirements`: numbered rows, each testable, each naming the build unit that owns it (a unit from the project map), each with a concrete example in real values: `call(args) → result` for a unit, `given … / when … / then …` at the project's real entry and exit for an integration. A row states what must be true, never how the code does it; a shape seen in one sample is no row.
- `## Boundaries`: out of scope, files another owner holds.
- `## Standing rules`: the worktree, the fence, the child `CLAUDE.md` paths the work touches, each touched unit's testing manual path.
- `## Evidence`: the path of each probe map you received, never its content.

## S4 — Hand off

Spawn one `Agent(subagent_type: "flights-foreman")` whose prompt names `requirements.md`, the flight directory, the project map, acceptance (what the user confirmed must be true when it lands) and whether the user ordered a commit or merge. End your message; the return arrives once the flight landed or stopped.

## S5 — The one question

A `BLOCKED` return carries a question. Put it to the user in one round holding that question alone, append the ruling to `## Rulings`, and send it to the same foreman by `SendMessage`, closing with "continue, then return once more in the return shape". At most one such round per flight; a second means S2 was skipped.

## Present

The foreman's return as it came: its first line, the `UNITS` and `GATE` rows, `UNPROVEN`, `OUTSIDE`, and the flight directory's path for `/flights:audit`.
