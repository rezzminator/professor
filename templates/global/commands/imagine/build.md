---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:build
description: 'Builds visions with sub-agents — `/imagine:build [vision | file]`, or "spec it first, then build it with agents". /imagine:build → imagine → imagination; the spec alone → /imagine:build-spec. Returns the work and its spec path. No spec pass → /imagine:build-fast.'
argument-hint: '[vision | file holding it]'
---

# Build

The vision: $ARGUMENTS. The point is something amazing past the limits of anything of its kind: you draw out its edges, the `imagine` agent imagines it to the ceiling of its imagination and past it, you tell the user what it will make, the `imagination` agent builds it. A step that narrows the vision's reach has failed.

Load `/imagine:build-spec` with the Skill tool, the vision as its argument, and run its steps through U3: they load the core, draw out the edges with the mode, write the brief and get the spec. A load that fails stops here and says so. U4 follows here.

## U4 — Say what it will make

Before the builder starts, tell the user in one plain message of about ten lines:

- what will exist: the idea and its boldest element, as its receiver meets it
- where it is built and where it lands (directory, publish target), and which of the vision's fixed values it keeps
- who builds it: one `imagination` agent
- what the user must do next, if anything

Then dispatch in the same turn; no answer is awaited. A reserved command from U1 is the one stop: the message ends with the command to type and the spec's path, the turn ends there, and the build goes through what that command starts.

## U5 — Build

The core's § Build through imagination, with the paths of `spec.md` and `brief.md`.

## Deliver

The core's § Deliver, adding the spec's path and every spec line the build changed and why. The user is asked to confirm an unseen viewer renders.
