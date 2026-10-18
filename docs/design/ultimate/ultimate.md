# ultimate

`ultimate` is the skill that moves a piece of work past the model's standard. It is a stance, not a procedure: loaded, it makes the loader a perfectionist who ships nothing below the best it can make, and it names the four things the work must reach. It builds nothing itself. The work is made where the skill was loaded, by whoever loaded it.

## Contents

- [Identity](#identity)
- [The flows](#the-flows)
- [Raise the ask](#raise-the-ask)
- [The four pillars](#the-four-pillars)
- [Boundaries](#boundaries)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | skill, `templates/global/skills/ultimate/SKILL.md`, linked to `~/.claude/skills/ultimate/` by `pfm install` |
| Entry | `/ultimate [work]`, on the named work or the work in hand; also whenever the ultimate of anything is asked for |
| Loaded by | a user or chat directly; the `/imagine` command; the `imagination` agent, which takes it as its law |
| Writes | nothing of its own; it shapes how the loader works |

## The flows

- `/ultimate [work]` → the work is made where it stands, by whoever loaded it.
- `/imagine [work]` → loads `ultimate` → the chat writes `$HOME/.local/state/imagine/{slug}/brief.md` → one `imagination` agent, which also loads `ultimate`, builds it → the parent chat looks and raises → delivers to the user. The full flow is in `imagine.md` in this directory.

`ultimate` does not route visual work to `/imagine`. A user who wants a visual work built by the family runs `/imagine`; a chat that loaded `ultimate` makes its work itself, at the standard the skill sets.

## Raise the ask

The skill opens before the first edit: the request is rewritten, then built to the rewrite. Four moves, in order.

### Altitude

The goal beyond the goal: what the receiver wants to happen once the work lands. The work aims there, not at the literal request.

### Depth

The rawest material the field's best practitioner would start from, instead of what was digested for the maker, and the instruments they would use on it (a database, a statistical model, a profiler, a benchmark, never vocabulary alone). The idea lives in the material, so the maker touches it raw before designing. The raw-material rule has four parts:

- Read, never change: the maker goes into the data, files or system the work is about and reads it raw. A store reached through a snapshot is read only through it, and a script that walks files caps every read and never trusts `st_size`.
- Scope exactly as the user stated it: the host, the period, the subset they name. A wider record never fills the view.
- Never a summary: a digest hides the strange shape, the hidden clock, the number that should not be, and those are where the idea is.
- One belief the request takes for granted is tested against that material.

### Range

The work stated as verbs, not its noun ("getting busy people to decide under uncertainty", not "a deck"), and the fields whose methods those verbs call for, each brought in by what it does, never by playing a character.

### Limit

The goal set past what seems reasonable (200 ms, not 5 s) and treated as reachable: the limit is found by going through where it seemed to be.

## The four pillars

The work meets all four at once.

### Perfection

Every part a receiver can reach holds at the level of the best part: first second and hundredth visit, the empty case, the error, the slowest device, the narrowest screen. The weakest part is the work's level, so the next hour goes there. Perfection is judged on the delivered work as its receiver meets it, never on the source.

### Completeness and beyond

Everything the material holds that bears on the work is in it, every aggregate opens to its single real item, and a timeline spans the subject's whole life. Then the work goes past the material in two steps: join what was never joined to show what none shows alone, then create the data the material never recorded (a model, a simulation, a signal), shown to hold on data it was not built from. A combination names the sources it joins, and what a join cannot establish stays a visible gap, never a guess.

### Going crazy on something

One element is chosen and taken further than anyone would think reasonable, until it is the reason the work exists. The rest of the work serves it. The first delivery is already the bold one: a safe, minimal first pass is a miss, and a work that does not amaze is replaced by a new idea, never polished.

### Capability at its limit

The work shows what the running model and the machine can do at their limit: the hardest technique the work can use, the medium at full power, the computation a lesser maker would skip. Capability spent outside the work's purpose is display, not the ultimate; the receiver should be astonished at first sight that it is possible.

## Boundaries

- It is not a builder. Whether one agent or the chat itself makes the work is decided by the loader (`/imagine` dispatches one `imagination`; a bare `/ultimate` makes the work in the chat).
- The skill is the shared law of the family: the command and the agent both load it rather than restating it, so a change to a pillar lands in one file.

## Surfaces that stay in sync

| Surface | Holds |
| --- | --- |
| `templates/global/skills/ultimate/SKILL.md` | the skill itself |
| `templates/global/commands/imagine.md` | step 1 loads the skill |
| `templates/global/agents/imagination.md` | first line loads the skill; the Depth rule is cited from its § Before the first file |
| `templates/refresh-map.json` | the skill's refresh-map entry |
