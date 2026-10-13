---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:callmeter
description: 'USER-ONLY — `/imagine:callmeter [focus]`: one striking, animated Artifact of your Claude Code history from callmeter''s store, every chat, prompt, agent and tool call open to its record. /imagine:callmeter → /imagine:build.'
argument-hint: "[focus]"
disable-model-invocation: true
---

# Imagine callmeter

Visualize what happened in the past, from the data callmeter recorded: the work Claude Code did on this machine, seen as a whole and as it flowed. One Artifact, in whatever form the build finds most striking. An open greenfield: no template, no menu of chart types, no layout handed down.

The user's focus: `$ARGUMENTS`

With none, the whole history and the maker's own angle.

Load the `imagine-core` skill first — the Skill tool, or a Read of `~/.claude/skills/imagine-core/SKILL.md` where no Skill tool serves it — and the `ultimate` skill it loads; a load that fails stops here and says so.

## Before the build

1. The core's § Live and visual work target proof for an Artifact. One that fails is said now, before anything is built, with one question: build a self-contained HTML file instead?
2. Load the `callmeter` skill (`/callmeter:callmeter`, from the callmeter plugin) for how to open the store: a snapshot copy, read-only, never the live file, never a write. Take the snapshot. If the skill will not load, say so and stop before reading the store.
3. Explore the snapshot and look at it. Find out what it holds, how the pieces connect, how it changed over the days, what is unusual. Read real rows and real distributions, print samples of structure, and look at the numbers themselves, not only their totals. The idea comes from what the data actually shows: a visualization chosen from the schema alone is a guess.

## The fixed edges

These reach `/imagine:build` as the vision's fixed edges, verbatim into its U1 and the brief.

- The point of view: the work and its flow, from the inside — who did what, in what order, how it nested and connected, how it grew and where it surprised. How the recorded work is organised and linked is part of the picture. The container that holds it (file size, pages, storage mechanics) and row counts as the subject are not: a picture of the box is not a picture of what happened.
- Every recorded thing is reachable in detail: each chat, prompt, agent run and tool call can be found in the work and opened down to its own record, with its fields, its place in the tree, its time and its size. Spectacle never replaces inspectability; a first delivery that leaves its viewer blind has failed however striking it looks.
- The boldest element opens the data and never covers it: the receiver travels from the boldest view to one exact record in it.
- What the page may carry: the store keeps no message content, yet it holds command text, file paths, working directories, error labels and titles. The published page carries structure, timing and counts only: never a command, a path, a prompt, a project or directory name, an error text or a chat title. Sessions and agents go by a short anonymous label. Anonymous does not mean blind: the drill-down inspector covers every recorded entity with what is safe — times, durations, tool classes, sizes, ok or failed, token counts, the agent tree and the counts at each level.
- The identity question: when the focus asks to see the things themselves (what ran, which command, which file, which folder), U1's first round asks once how much real identity the page may carry — structure only, names and roles, or everything including commands and paths — and the build goes to that answer and nothing past it. With real content: a visible notice on the page says so, obvious secrets are masked by pattern (keys, tokens, bearer strings, `KEY=value` assignments for secret-named keys) and counted for the report, the page says plainly that masking is by pattern and not a guarantee, and the link stays private. The ask is the user's call, never a default.
- The material: the snapshot's path, read-only; the live store is never opened.

## Build

Load `/imagine:build` with the Skill tool, its argument the focus as the vision (none: the whole history), with the fixed edges above and the snapshot as its material. Its U1 keeps the Pro-or-standard question and asks only what the edges leave open; the `imagine` agent writes the spec and the `imagination` agent builds the page. A refused load stops here and says so.

## Deliver

`/imagine:build`'s Deliver, adding what the data could not show and where the snapshot copy sits, with an offer to delete it.

A reaction that the work shows too little ("I feel blind", "I expected to see each prompt, agent and call") is a finding about detail, not about taste: the answer is more reachable detail, never more polish. Ask the identity question once, then the builder rebuilds the way in so that every entity opens to its record. If the user orders a new idea from scratch, the old boldest element is theirs to take out; the new work still holds every fixed edge.

## Rules

- The store is only ever read, from a snapshot: never run `callmeter redact` or write to it.
