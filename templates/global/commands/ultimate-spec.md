---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: ultimate-spec
description: 'Builds visions with sub-agents — `/ultimate-spec [vision | file]`, or "spec it first, then build it with agents". /ultimate-spec → ultimate → builders. Returns the work and its spec path. Not for one piece made in this chat → /ultimate.'
argument-hint: '[vision | file holding it]'
---

# Ultimate spec

The vision: $ARGUMENTS — inline, a file holding it, or nothing (then the first round asks what it is). The point is something amazing past the limits of anything of its kind: you draw out its edges, the `ultimate` agent imagines it to the ceiling of its imagination and past it, you tell the user what it will make, builder sub-agents build it. A step that narrows the vision's reach has failed.

## U1 — Draw out the edges

The vision travels in the user's own words, never converted into a purpose, a goal or a feature list. Ask only what the build cannot stand without:

- hard edges: a medium or stack the user fixes, the target it runs on, a budget, what it must never be or do
- touchpoints: everything the build will need from the user — the directory it is built in, a secret, an account, a publish, a destructive step — pre-authorised now or cut, so the build never stops to ask
- a command reserved for the user — `/design`, or any the Skill tool refuses with "disable-model-invocation": the one stop the build keeps; the confirm round tells the user they type it, with the spec's path, once the spec lands

Everything else — medium, look, structure, names, features, what it is for beyond the user's words — stays the agent's. A design choice the user volunteers is a fixed value.

Ask through AskUserQuestion, up to four questions a round. It is the user's whole screen: each question carries its own context, each option is a concrete answer, your best guess first, and their own words go in Other. One round, two at most, the second simpler and more concrete, never a rephrase. Then put it back — the vision verbatim, then the edges and touchpoints as one numbered list — inside a last AskUserQuestion, confirm or adjust, and go on only once they confirm.

## U2 — Brief

Choose `{slug}`: short kebab-case; on a collision under `$HOME/.local/state/ultimate/` append `-v2`. Write `$HOME/.local/state/ultimate/{slug}/brief.md` holding three things and nothing else:

- the vision verbatim, with its confirmed edges and touchpoints
- where its material lives and the read-only route into it: paths, access, a store's snapshot method
- what the machine can run: runtimes, package managers and tools with versions, whether the network reaches package registries, the target's limits, how this chat will see the work (a browser driver for visual work, a terminal for a binary)

The agent reads the material itself: a summary of it, a correctness rule or a medium hedge in the brief narrows what it imagines. A rule the build needs is yours, held for U5.

## U3 — Spec

Spawn one `Agent(subagent_type: "ultimate")` whose prompt names the absolute paths of `brief.md` and of `spec.md` beside it. End your message; the return arrives.

## U4 — Say what it will make

Read `spec.md` whole: the return is a claim, the file is the direction. Before any builder starts, tell the user in one plain message of about ten lines:

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

- the spec's boldest element is fixed; every other line is raised when a look finds it weak; a spec line the materials refuse becomes the nearest version that keeps its intent
- every decision the spec leaves open is the builder's, made on the spot; the user asked for imagination, not a menu
- every detail is chosen: names, timing, colour, spacing, copy, the empty, error and reduced-motion states
- the work carries its own identity: the chat persona's traits, catchphrases and costume stay out unless the vision asks for them
- a fact the work needs that sits on disk is read, never asked for; a fact only the user holds that U1 missed is never invented: build around it and name it in the return
- § Look and raise, whole
- the return lists every spec line changed and why, what was verified and on what, and what was not

End the message; the returns arrive. A return is a claim: the work on disk is what is read.

## Look and raise

The builder looks first at the first frame, before the plumbing is finished, then after every raise. This chat then meets the finished work once as its receiver does, from disk, and sends the weakest findings back to the builder that made it (`SendMessage`); rounds end when a whole look finds only polish. Meet the work as its receiver does (the published page, the running binary, the sent file, the prompt a model runs), never the source alone:

- visual or interactive: real frames at several moments and widths; every interaction driven
- code: run it, its edge cases, a benchmark where speed matters
- prose or prompt: read as its reader, or as the model that runs it; each line pulls or goes
- analysis: every claim to its source; the strongest counter-argument answered

Sums, tests and width checks are proof, never the look. Judge as the harshest expert in its field: where it is timid, where generic, what a first-time receiver will read it as, what a version ten times bolder would do. Raise the weakest, sharpen the strongest, and see every change before it ships.

A finding that the boldest element is too much, too strange or misreadable raises it until it reads, never tames or cuts it; only the user takes it out.

## Deliver

Report plainly: what it is, the idea behind it in a few lines, how to use it, the spec's path, who built it and how the work was split, every spec line the build changed and why, what was verified and on what, what was not. The work carries the ambition; the report carries none.

A reaction to the delivered work is read by its content, never by its tone; an ambiguous one keeps the boldest element and asks which way.

- ✗ On a self-portrait built around one giant eye: "What the fuck is this!? Did you make yourself the Eye of Sauron!?" → read as anger, the eye deleted as a composition error. The user wanted it back, then the whole piece rebuilt around it.
- ✓ Same message → the eye stays; one question: push it further, or take it out?
- ✓ "Take the eye out." → out, with everything that existed only for it.

## Rules

- A budget the vision or the project sets binds, and the report names what it cut; otherwise calls go to making and looking, never to ceremony.
- A user who rules in the run to build by hand: this chat builds the work itself, § Look and raise unchanged.
- Ultimate raises the work, never the permissions: every project rule, gate and publication boundary holds unchanged.
