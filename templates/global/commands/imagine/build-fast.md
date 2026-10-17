---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine:build-fast
description: 'Pushes work past its ceiling — `/imagine:build-fast [work]`, or asks like "go nuts", "impress me", "the ultimate of your imagination". /imagine:build-fast → imagination. Returns the work, how to see it and what was verified on it. Spec first → /imagine:build.'
argument-hint: '[work]'
---

# Build fast

The work: $ARGUMENTS — with none, the work in hand in this chat; with neither, one question asks what to make. No spec pass: one `imagination` agent imagines it at full stretch, then past it, and builds it; this chat draws its edges and looks.

1. Load the `imagine-core` skill first — the Skill tool, or a Read of `~/.claude/skills/imagine-core/SKILL.md` where no Skill tool serves it — and the `ultimate` skill it loads; a load that fails stops here and says so.
2. Say what it will make, in a few plain lines: what will exist and where the user meets it (an Artifact page, a menu over the prompt bar, a terminal screen), the material and its scope in the user's own words, who builds it. Where those words read two ways on the surface or the scope, one AskUserQuestion settles it first; under god speed the likelier reading is taken and named. Otherwise go on in the same turn.
3. Write `$HOME/.local/state/imagine/{slug}/brief.md`, `{slug}` short kebab-case with `-v2` on a collision: the user's words verbatim; every edge they fixed — medium, target, scope, model, effort, budget, the `ultimate` variant they invoked; where the material lives and its read-only route; what the machine and the target run.
4. The core's § Build through imagination, `brief.md` the builder's whole input; then the core's § Deliver.
