# imagine

The imagine pair is the `/imagine` command and the `imagination` agent. Together they take a work, usually a visual one (a page, an Artifact, a scene, a terminal screen or picker), from the user's words to a delivered, looked-at result. The `ultimate` skill (`ultimate.md` in this directory) is the law both load.

## Contents

- [Identity](#identity)
- [The flow](#the-flow)
- [Why one builder](#why-one-builder)
- [The brief and the dispatch](#the-brief-and-the-dispatch)
- [Look and raise](#look-and-raise)
- [Deliver](#deliver)
- [The imagination agent](#the-imagination-agent)
- [Why the family is three pieces](#why-the-family-is-three-pieces)
- [Boundaries](#boundaries)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | `/imagine` | `imagination` |
| --- | --- | --- |
| Kind | command, `templates/global/commands/imagine.md` | agent, `templates/global/agents/imagination.md` |
| Entry | `/imagine [work]`, or asks like "go nuts" and "impress me"; with no argument, the work in hand | dispatched by `/imagine` only |
| Model, effort | the chat's own | `sonnet`, `xhigh` |
| Owns | the brief, the dispatch, the look, the raise rounds, delivery | the build, its checkpoint, its publish, the law of the work |
| Returns | the work, how to see it, what was verified | `BUILT`, `PARTIAL` or `BLOCKED`, how to see it, what was verified and on what |

## The flow

`/imagine [work]` → loads `ultimate` → the chat writes `$HOME/.local/state/imagine/{slug}/brief.md` → one `imagination` agent (loads `ultimate`) builds it → the parent chat looks and raises → delivers to the user.

1. The chat loads `ultimate`; a failed load stops the run and says so.
2. It says in a few plain lines what will exist, where the user meets it, the material and its scope in the user's own words, and who builds it. A request that reads two ways on the surface or the scope gets one question first; under god speed the likelier reading is taken and named.
3. It writes the brief and dispatches the builder.
4. It gives the user the path of the build's `CHECKPOINT.md` and ends the message; asked how it goes, it answers from that file.
5. When the builder returns, the chat looks at the work as its receiver does, sends the weakest findings back to the same builder, and delivers.

## Why one builder

The main chat used to build by hand, so the work was made under the chat's own habits instead of the law the agent carries. Now the chat writes no part of the work.

- One `imagination` agent builds all of it, whatever its size and however many units it crosses. There is no foreman, no fork, no second builder and no hand-written brief: one maker means one conception, and a visual work is conceived whole.
- A model, effort or budget the user names goes into the dispatch; otherwise the agent's own apply.
- Raise passes go back to the same builder by message, because it holds the checkpoint and the idea; a fresh builder would re-conceive the work.
- The user keeps one override. "Build it by hand" said in the run is honoured: the chat then builds the work itself, with `~/.claude/agents/imagination.md` read as the law, and the look is unchanged.
- A session started before `imagination` was registered dispatches one `general-purpose` agent at the same model and effort, whose prompt opens with a read of the agent file as its protocol.

## The brief and the dispatch

The brief carries the vision and its hard edges only: the user's words verbatim; every edge they fixed (medium, target, scope, model, effort, budget); where the material lives and its read-only route; what the machine and the target run. Angle, metaphor, form and which data to use stay the maker's to discover. That split keeps the chat from pre-deciding what the builder was dispatched to imagine.

The dispatch carries the goal in one sentence, the boundary (the build directory in, everything else out), the brief's path, the material's read-only route, and the target with its own limits (publish route, size and sync caps, frame CSP and script hosts, pre-authorised touchpoints). The build directory is the one the user named; work inside an existing project goes where its rules put it (a worktree); otherwise it is `build/` beside the brief.

The builder's return is a claim: the work on disk is what the chat reads. `BLOCKED` reaches the user as the first line with the file and line or the command they type, and no other medium stands in for the target unless the user names it.

## Look and raise

The chat meets the finished work once as its receiver does, on its real surface, and sends the weakest findings back to the same builder. Rounds end when a whole look finds only polish. A stand-in surface proves a code path and is reported unverified, never as the control working. Questions the user asks during the run are answered, never acted on as orders.

## Deliver

The first line of the report is how to see it: the URL, the command, or a screenshot of it on its target. Then, in everyday words with no internal term: what the user is looking at, the idea behind it, how to use it, what was verified and on what, and what was not.

A reaction to the delivered work is read by its content, never its tone. An ambiguous reaction (a swearing "what is this?") keeps the boldest element and asks which way, one question: push it further or take it out. Only an explicit "take it out" removes it, with everything that existed only for it. This protects the boldest element from being deleted as a composition error when the user was reacting to its force.

## The imagination agent

The agent is the one maker. Its prompt carries the family's law in five parts:

| Part | What it holds |
| --- | --- |
| The nine laws | floor not ceiling; imagine; the boldest element; one living view; imagination not a menu; the form imagined and the facts read, never invented; look and raise; deliver plain; raise the work, never the permissions |
| Imagine | the steps before the first file: find the work's own conception, push past the first idea until one surprises, leap in kind not polish, name the boldest element, go deeper never wider |
| Make | one maker, one finished work, a prototype before wiring into an existing system, the visual work as one living view with time as its motion, scripted numbers, chosen details |
| Look and raise | meeting the work on its real surface with real input, the checks per kind of work, the harshest-expert judgment, forcing a failure to prove it |
| Live and visual work | the target proof (an `Artifact` that is absent or denied ends the run as `BLOCKED`, never a local file in its place), the limits, the traps met before, the browser look, publishing |

Its run is bounded by a checkpoint: `CHECKPOINT.md` in the build directory is updated at every milestone and before every compaction, and a resumed or raised build starts from it, never from a fresh read of the brief. The agent's own first move after loading `ultimate` is to read the brief once and go into the material under the Depth rule.

## Why the family is three pieces

The family shipped as a larger imagine family: a spec path (`/imagine:build-spec`), a build command, a fast build command (`/imagine:build-fast`), a meter command (`/imagine:callmeter`), an `imagine` spec agent and an `imagine-core` skill. Measured usage on the user's machine over 13 to 18 October: `/ultimate` ran in 132 chats, `/imagine:build-fast` in 10, the `imagination` agent in 2, and `/imagine:build`, `/imagine:build-spec`, `/imagine:callmeter`, the `imagine` agent and a local `ultimate-ultimate` variant in none.

The pieces nobody ran were removed, and the used ones were made the whole:

- The spec path is gone; a second spec path in this family was dead weight.
- `/imagine:build-fast` became `/imagine`, the one entry point, because it was the only build command in use.
- `imagine-core` was folded into its two readers: the law into the `imagination` agent, and the dispatch plus the chat's half of delivery into `/imagine`. Each reader now holds exactly what it uses, and nothing is loaded for the other's half.
- `ultimate-ultimate`'s raise-the-ask was folded into `ultimate`, which is why the skill now opens with it.

The `imagine` agent name is registered as retired in `templates/global/engine-sync.json` so that mirrors drop it.

## Boundaries

- The chat never builds, except on the user's ruling to build by hand.
- The builder writes only inside the build directory; the material and everything outside it are read-only.
- The builder spawns no agent, foreman or second maker; a helper only reads or extracts bulk material.
- Nothing is published, pushed or committed by the family beyond the target's publish route the brief names.

## Surfaces that stay in sync

| Surface | Holds |
| --- | --- |
| `templates/global/commands/imagine.md` | the command |
| `templates/global/agents/imagination.md` | the agent and the law |
| `templates/global/skills/ultimate/SKILL.md` | the skill both load |
| `templates/global/engine-sync.json` | `imagine` among the retired agents |
| `templates/refresh-map.json` | the family's refresh-map entries |
