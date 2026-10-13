---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:build
description: 'Builds visions with sub-agents — `/imagine:build [vision | file]`, or "spec it first, then build it with agents". /imagine:build → imagine → imagination; the spec alone → /imagine:build-spec. Returns the work and its spec path. Not for one piece made in this chat → /imagine:build-fast.'
argument-hint: '[vision | file holding it]'
---

# Build

The vision: $ARGUMENTS. The point is something amazing past the limits of anything of its kind: you draw out its edges, the `imagine` agent imagines it to the ceiling of its imagination and past it, you tell the user what it will make, the `imagination` agent builds it. A step that narrows the vision's reach has failed.

Load `/imagine:build-spec` with the Skill tool, the vision as its argument, and run its steps through U3: they load the core, draw out the edges with the mode, write the brief and get the spec. A load that fails stops here and says so. U4 follows here.

## U4 — Say what it will make

Before the builder starts, tell the user in one plain message of about ten lines:

- what will exist: the idea and its boldest element, as its receiver meets it
- where it is built and where it lands (directory, publish target), and which of the vision's fixed values it keeps
- who builds it: one `imagination` agent, by its own hand
- what the user must do next, if anything

Then dispatch in the same turn; no answer is awaited. A reserved command from U1 is the one stop: the message ends with the command to type and the spec's path, the turn ends there, and the build goes through what that command starts.

## U5 — Build

This chat orchestrates and writes no part of the work. One `Agent(subagent_type: "imagination")` builds all of it, whatever its size and however many units it crosses (a data pipeline plus a front end included); a model or effort the user named goes in the dispatch. A session started before `imagination` was registered dispatches one `general-purpose` agent at sonnet whose prompt opens with a Read of `~/.claude/agents/imagination.md` as its protocol. The dispatch carries the goal in one sentence; the boundary, the build directory in and everything else out; the paths of `spec.md` and `brief.md`, the build directory, the material's read-only route as the brief names it (a store's snapshot path), and the target with its own limits — its publish route, size and sync caps, frame CSP and script hosts, and the touchpoints the user pre-authorised.

End the message; the return arrives in the shape of the agent's § Return. A return is a claim: the work on disk is what is read.

## Look and raise

The builder looks and raises as it builds. This chat then meets the finished work once as its receiver does, from disk, and sends the weakest findings back to the builder (`SendMessage`); rounds end when a whole look finds only polish.

## Deliver

The core's § Deliver, adding the spec's path and every spec line the build changed and why. The user is asked to confirm an unseen viewer renders.

## Rules

- A user who rules in the run to build by hand: this chat builds the work itself, § Look and raise unchanged.
