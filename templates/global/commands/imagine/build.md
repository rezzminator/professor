---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:build
description: 'Builds visions with sub-agents — `/imagine:build [vision | file]`, or "spec it first, then build it with agents". /imagine:build → imagine → builders; the spec alone → /imagine:build-spec. Returns the work and its spec path. Not for one piece made in this chat → /imagine:build-fast.'
argument-hint: '[vision | file holding it]'
---

# Build

The vision: $ARGUMENTS. The point is something amazing past the limits of anything of its kind: you draw out its edges, the `imagine` agent imagines it to the ceiling of its imagination and past it, you tell the user what it will make, builder sub-agents build it. A step that narrows the vision's reach has failed.

Load `/imagine:build-spec` with the Skill tool, the vision as its argument, and run its steps through U3: they load the core, draw out the edges with the mode, write the brief and get the spec. A load that fails stops here and says so. U4 follows here.

## U4 — Say what it will make

Before any builder starts, tell the user in one plain message of about ten lines:

- what will exist: the idea and its boldest element, as its receiver meets it
- where it is built and where it lands (directory, publish target), and which of the vision's fixed values it keeps
- how it is built: the builders, their split, their tier
- what the user must do next, if anything

Then dispatch in the same turn; no answer is awaited. A reserved command from U1 is the one stop: the message ends with the command to type and the spec's path, the turn ends there, and the build goes through what that command starts.

## U5 — Build with sub-agents

This chat orchestrates and writes no part of the work; builders make it. Size picks the builder:

- one build unit that fits about 80 calls: one `Agent(subagent_type: "general-purpose", model: "opus")`
- anything larger or crossing build units (a data pipeline plus a front end): one `foreman-pro`, which splits by ownership itself and returns one verdict

Tier `opus` at xhigh effort (`high` for a small unit); `fable` only on the user's say. Every dispatch carries: the goal in one sentence and the artifact it returns; the boundary, the build directory in and everything else out; the anchors, the paths of `spec.md` and `brief.md`, the build directory and the target's runtime; and what its failure looks like, a dead end or a tool that will not run, returned as such. All builders of a round go out in one message.

The brief carries these to the builder as its rules:

- load the `imagine-core` skill before the first file — the Skill tool, or a Read of `~/.claude/skills/imagine-core/SKILL.md` — and the `ultimate.md` it names, and make the work under it; a load that fails is the return
- the spec's boldest element is fixed; every other line is raised when a look finds it weak; a spec line the materials refuse becomes the nearest version that keeps its intent
- every decision the spec leaves open is the builder's; a fact only the user holds that U1 missed is built around and named in the return
- the return lists every spec line changed and why, what was verified and on what, and what was not

End the message; the returns arrive. A return is a claim: the work on disk is what is read.

## Look and raise

The builder looks, by the core's § Look and raise, first at the first frame before the plumbing is finished, then after every raise. This chat then meets the finished work once as its receiver does, from disk, and sends the weakest findings back to the builder that made it (`SendMessage`); rounds end when a whole look finds only polish.

## Deliver

The core's § Deliver, adding the spec's path, who built it and how the work was split, and every spec line the build changed and why.

## Rules

- A user who rules in the run to build by hand: this chat builds the work itself, § Look and raise unchanged.
