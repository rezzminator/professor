---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:build-spec
description: 'Specs a vision, then stops — `/imagine:build-spec [vision | file]`, or "just spec it, I''ll build it later". /imagine:build-spec → imagine; building it → /imagine:build. Returns the spec path and its boldest element.'
argument-hint: '[vision | file holding it]'
---

# Build spec

The vision: $ARGUMENTS — inline, a file holding it, or nothing (then the first round asks what it is). The point is something amazing past the limits of anything of its kind: you draw out its edges, the `imagine` agent imagines it to the ceiling of its imagination and past it and writes the spec. A step that narrows the vision's reach has failed.

Load the `imagine-core` skill first — the Skill tool, or a Read of `~/.claude/skills/imagine-core/SKILL.md` where no Skill tool serves it — and the `ultimate.md` it names; a load that fails stops here and says so.

## U1 — Draw out the edges

The vision travels in the user's own words, never converted into a purpose, a goal or a feature list. Ask only what the build cannot stand without:

- the mode, in the first round: Pro, the agent forking itself into lenses that each see the whole work a different way, or standard, the agent imagining alone
- hard edges: a medium or stack the user fixes, the target it runs on, a budget, what it must never be or do
- touchpoints: everything the build will need from the user — the directory it is built in, a secret, an account, a publish, a destructive step — pre-authorised now or cut, so the build never stops to ask
- a command reserved for the user — `/design`, or any the Skill tool refuses with "disable-model-invocation": the one stop the build keeps; the confirm round tells the user they type it, with the spec's path, once the spec lands

Everything else — medium, look, structure, names, features, what it is for beyond the user's words — stays the agent's. A design choice the user volunteers is a fixed value.

Ask through AskUserQuestion, up to four questions a round. It is the user's whole screen: each question carries its own context, each option is a concrete answer, your best guess first, and their own words go in Other. One round, two at most, the second simpler and more concrete, never a rephrase. Then put it back — the vision verbatim, then the mode, edges and touchpoints as one numbered list — inside a last AskUserQuestion, confirm or adjust, and go on only once they confirm.

## U2 — Brief

Choose `{slug}`: short kebab-case; on a collision under `$HOME/.local/state/imagine/` append `-v2`. Write `$HOME/.local/state/imagine/{slug}/brief.md` holding four things and nothing else:

- the vision verbatim, with its confirmed edges and touchpoints
- the mode: Pro or standard
- where its material lives and the read-only route into it: paths, access, a store's snapshot method
- what the machine can run: runtimes, package managers and tools with versions, whether the network reaches package registries, the target's limits, how this chat will see the work (a browser driver for visual work, a terminal for a binary)

The agent reads the material itself: a summary of it, a correctness rule or a medium hedge in the brief narrows what it imagines. A rule the build needs is yours, held for the builders' brief.

## U3 — Spec

Spawn one `Agent(subagent_type: "imagine")` whose prompt names the absolute paths of `brief.md` and of `spec.md` beside it. End your message; the return arrives. Read `spec.md` whole: the return is a claim, the file is the direction.

## Stop

Run from `/imagine:build`, its U4 follows. Run on its own, the work ends here: give the user the spec's path, its boldest element in one sentence, the mode and the agent's `FORKS` line; the spec is theirs to read or hand on.
