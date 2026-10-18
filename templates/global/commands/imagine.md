---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: imagine
description: 'Pushes work past its ceiling — `/imagine [work]`, or asks like "go nuts", "impress me", "the ultimate of your imagination". /imagine → imagination. Returns the work, how to see it and what was verified on it.'
argument-hint: '[work]'
---

# Imagine

The work: $ARGUMENTS — with none, the work in hand in this chat; with neither, one question asks what to make. One `imagination` agent imagines it at full stretch, then past it, and builds it; this chat draws its edges and looks.

1. Load the `ultimate` skill first — the Skill tool, or a Read of `~/.claude/skills/ultimate/SKILL.md` where no Skill tool serves it; a load that fails stops here and says so.
2. Say what it will make, in a few plain lines: what will exist and where the user meets it (an Artifact page, a menu over the prompt bar, a terminal screen), the material and its scope in the user's own words, who builds it. Where those words read two ways on the surface or the scope, one AskUserQuestion settles it first; under god speed the likelier reading is taken and named. Otherwise go on in the same turn.
3. Write `$HOME/.local/state/imagine/{slug}/brief.md`, `{slug}` short kebab-case with `-v2` on a collision: the user's words verbatim; every edge they fixed — medium, target, scope, model, effort, budget; where the material lives and its read-only route; what the machine and the target run. The brief carries the vision and its hard edges only; the angle, metaphor, form and which data to use stay the maker's to discover.
4. § Build through imagination, `brief.md` the builder's whole input; then § Deliver.

## Build through imagination

This chat writes no part of the work:

- One `Agent(subagent_type: "imagination")` builds all of it, whatever its size and however many units it crosses: never a foreman, a fork, a second builder or a hand-written brief. A model, effort or budget the user named goes in the dispatch; otherwise the agent's own. A session started before `imagination` was registered dispatches one `general-purpose` agent at sonnet, effort xhigh, whose prompt opens with a Read of `~/.claude/agents/imagination.md` as its protocol.
- The build directory: the one the user named; work inside an existing project, where its rules put it (a worktree); otherwise `build/` beside `brief.md`.
- The dispatch carries the goal in one sentence; the boundary, the build directory in and everything else out; the path of `brief.md`; the material's read-only route as the brief names it (a store's snapshot path); and the target with its own limits — publish route, size and sync caps, frame CSP and script hosts, the touchpoints the user pre-authorised.
- Then give the user the path of `{build directory}/CHECKPOINT.md` and end the message; asked how it goes, answer from that file in a line. A question the user asks during the run is answered, never acted on as an order.
- The return is a claim: the work on disk is what is read. `BLOCKED` reaches the user as the first line, with the file and line or the command they type; no other medium stands in for the target unless the user names it.
- Meet the finished work once as its receiver does, on its real surface, and send the weakest findings back to the same builder (`SendMessage`); rounds end when a whole look finds only polish.
- A user who rules in the run to build it by hand: this chat builds the work itself, `~/.claude/agents/imagination.md` read as the family's law, the look unchanged.

## Deliver

Report plainly. The first line is how to see it: the URL, the command to run, or a screenshot of it on its target. Then what the user is looking at, in a few everyday words with no internal term; the idea behind it in a few lines, how to use it, what was verified and on what, what was not.

A reaction to the delivered work is read by its content, never by its tone; an ambiguous one keeps the boldest element and asks which way.

- ✗ On a self-portrait built around one giant eye: "What the fuck is this!? Did you make yourself the Eye of Sauron!?" → read as anger, the eye deleted as a composition error. The user wanted it back, then the whole piece rebuilt around it.
- ✓ Same message → the eye stays; one question: push it further, or take it out?
- ✓ "Take the eye out." → out, with everything that existed only for it.
