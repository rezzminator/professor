---
name: flights:spec
description: 'Grills until nothing stays open — /flights:spec [work | file], or asks like "grill me", "roast my plan", "settle the requirements" before a build. /flights:spec → flights-speccer → main chat → flights-foreman. Returns the requirements.md and spec.md paths, ready to build.'
argument-hint: '[work | file holding it]'
---

# Spec — settle the work, then spec it

Two deliverables in the flight directory `$HOME/.local/state/pfm/flights/{project}/{flight}/`: `requirements.md`, written by you once the grill leaves no decision open, and `spec.md`, written from it by `flights-speccer`. You walk, grill, write and hand off; you build nothing, and the spec waits for the user's go. Input: $ARGUMENTS — the work inline, a file holding it, or nothing (then ask what the work is for). `{project}` is the checkout's directory name minus any leading dot; `{flight}` is short kebab-case, with `-v2` appended on a collision under `$HOME/.local/state/pfm/flights/{project}/`.

## S1 — Walk only what asking needs

Read the child `CLAUDE.md` of every project the work touches (the root one you already hold): S2 must not re-ask what they settle. A fact a question needs goes to `Agent(subagent_type: "tracer")` as numbered questions, all probes in one message; end your message after spawning, the answers arrive on their own. The walk stops at what a good question needs: the speccer reads the material itself, so a map built here for it is reading paid twice.

A referent that does not exist, or two parts of the work that would edit one target, is a question for S2, never a silent fix.

## S2 — Grill the user

Build the design tree: the goal at the root, each decision the work rests on a branch. A decision the work, the probes or a child `CLAUDE.md` settle is settled; every other is open:

- Technical: every branch with two or more defensible options and materially different consequences (transport, data placement, migration, failure behaviour, naming, layout).
- Product: who the change is for, what must be true when it lands, what it must never do, what the user sees.
- Edges: the second writer, the extreme input, the failure nobody handles, the old path the work replaces and everything that dies with it.
- Touchpoints: every moment the build would need the user (a secret, a deploy review, a destructive operation), pre-authorised now or cut from scope; a build that stops mid-run for a user answer is a failed grill. Work that moves protected data gets a question about its channel, never a default.

Roast as you grill: a premise the probes contradict, a choice that costs more than it buys, or a requirement that works against the stated goal is named plainly in its question, with the better option as your recommendation. Push back once per point; the user's answer rules.

Work the tree in rounds. The frontier is every open decision whose prerequisites are settled. Round one always holds the objective and the scope boundary: the goal behind the request (what the user ultimately wants, what should happen once the work lands) restated for the user to correct, then what the work includes and what it defers — scope never narrows or widens silently. Ask the whole frontier in one plain-text message, then end it and wait:

```
**Q1 — {title}**: {the question, its context and its options}
Recommended: {your answer and the one reason}
```

Each answer reshapes the tree; recompute the frontier and ask the next round. Facts are yours to find, decisions are the user's; one the user hands back ("you pick") takes your recommendation as its ruling, marked `delegated`. The grill ends when the frontier is empty: no decision is left for the speccer that the user did not rule or delegate. Restate the rulings as one numbered list and go on only once the user confirms it is the shared understanding. Under god speed you rule every question yourself, each marked `delegated`, and skip the confirmation; a protected-data channel the user did not name still gets no default: take the option that moves no protected data, or cut that work from scope.

## S3 — Write the requirements

Write `requirements.md`:

- `## Objective`: the goal behind the request as the user confirmed it, in their words where they gave them.
- `## Rulings`: the confirmed decisions, numbered, binding; a delegated one marked `delegated`.
- `## Requirements`: numbered rows, each testable, each naming the build unit that owns it, each with a concrete example in real values: `call(args) → result` for a unit, `given … / when … / then …` at the project's real entry and exit for an integration. A row states what must be true, never how the code does it; a shape seen in one sample is no row.
- `## Boundaries`: out of scope, files another owner holds.
- `## Standing rules`: the fence, the child `CLAUDE.md` paths the work touches, each touched unit's testing manual path.
- `## Evidence`: the path of each tracer `RUN` you received, never its content.

## S4 — Hand off to the speccer

Spawn one `Agent(subagent_type: "flights-speccer")` whose prompt names `requirements.md`, the flight directory, the tracer `RUN` paths, each touched unit's testing manual path, and god speed when the user's prompt carried it. End your message; the return arrives on its own.

## S5 — Close every gap

A `BLOCKED` return, or a `SPEC` whose `OPEN` holds a question for the user, goes to the user in one round holding those questions alone, in S2's shape; under god speed, rule them yourself as S2 does. Append the rulings to `## Rulings`, then spawn a fresh `flights-speccer` with S4's brief plus the existing `spec.md` and the new rulings as its reason; it edits the spec in place. Repeat until the return is `SPEC` with no question for the user left in `OPEN`. Each such round is a decision S2 missed.

## Present

The speccer's last return as it came — its first line (a `PARTIAL` included), `BOLDEST`, `ROWS`, `COST`, `OPEN` — then the absolute paths of `requirements.md` and `spec.md`, the number of S5 rounds and the decision each one closed, and that the spec is ready to build. The build starts only on the user's go: the main chat then runs the flights life cycle (the `flights-life-cycle` rule) with `spec.md` as the request.
