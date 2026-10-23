# rr-pro-max

`rr-pro-max` is the `rr` lead with no round ceiling: 8 diggers a round, 16 verification pages, `opus` at effort `medium`, the repository lane `rr-pro` also carries, digging that ends only when the map converges, and the `Read` tool the other leads lack. Every other line of its prompt is `rr.md`'s, so the run, the document, the marks and the verification rules are the family's (`rr.md` in this directory). This file holds what is its own: the caps, the missing ceiling and what brakes a run without one, the `Read` it holds and the name guard that costs, the cost, how it is run, and what its runs measured.

## Contents

- [Identity](#identity)
- [How the variant is built](#how-the-variant-is-built)
- [No round ceiling](#no-round-ceiling)
- [Read, and the name guard it gives up](#read-and-the-name-guard-it-gives-up)
- [Cost](#cost)
- [Running it](#running-it)
- [What the runs measured](#what-the-runs-measured)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | variant of `rr`, declared in `templates/global/agents/rr-pro-max.md` |
| Model, effort | `opus`, `medium` |
| Tools | `WebSearch, WebFetch, Read, Write, Agent, mcp__plugin_harvester_harvester__read, mcp__plugin_harvester_harvester__search_literature, mcp__plugin_harvester_harvester__search_web` — Claude Code names inherited from `rr.md` plus `Read`, added by `add.tools` |
| Spawns | `sub-rr`, and `clone-distiller` for a repository sub-area |
| Writes | the run's plan and synthesis, `plan.md` and `{slug}.md` in its run directory under the `RR-DIR:` line (`rr.md` § The document); an existing plan or synthesis of the same name is overwritten (§ Read, and the name guard it gives up) |
| Start hook | `rr-dir`, matcher `rr\|rr-pro\|rr-pro-max\|gpt-rr\|gpt-rr-pro\|gpt-rr-pro-max` |

Description, verbatim: `rr tier 3 on opus medium`

## How the variant is built

`pfm install` renders it from `rr.md` by the same mechanism as `rr-pro`: its frontmatter overrides `description` and `effort`, its `add` appends `Read` to `tools`, its body replaces § Budget, and `extends: [shared/repo-lane.md]` supplies § Spawning. Both sections carry their base-text hash in `pins:`:

| `rr.md` section | `rr-pro-max` text |
| --- | --- |
| § Budget | `- Diggers: at most 8 a round.` · `- Rounds: no ceiling — digging ends only on step 5's other two conditions.` · `- Verification: at most 16 source pages.` |
| § Spawning | the shared file `shared/repo-lane.md`: `sub-rr` for the web, `clone-distiller` for a sub-area whose answer lives in a repository's code — the full text is in `clone-distiller.md`, § Who spawns it |

- Each override carries the sha256 pin of the `rr.md` section it replaces. An edit to `rr.md`'s § Budget or § Spawning fails the render with `SECTION-DRIFT rr-pro-max: rr.md § {heading} changed since pinned …` until the override is reviewed and `engine-sync variants pin rr-pro-max` re-pins it; a renamed or deleted heading fails with `SECTION-MISSING`. The procedure's steps cite § Budget and § Spawning by name, so an override changes the caps or the lane without touching a step.
- The fence's `templates.generated-agents` step (`infra/fence/checks.sh`, `checks_templates_generated_agents`) renders the shipped declaration and agent bodies through the built engine-sync binary, so a drifted pin fails before an install does.
- An installed engine-sync older than `0.3.0-alpha.2` cannot compile declaration files, and `pfm install` refuses. The fix is the host mirror build, never an edit to the declaration.

## No round ceiling

A heavy run digs until the map converges. Two stops remain, both from the family's stop rule:

- every sub-area is `settled` against its `settled when` line;
- a round settled nothing and added no sub-area.

Nothing else ends a run. A lead that keeps adding sub-areas, or judges each round as having settled something, keeps digging; the brake is the lead's own status block, which names every sub-area's state and `Next: round {n}` or `Digging ended: {condition}` after each round, so a run that will not converge is visible in its transcript while it runs. The accepted risk is cost, not a wrong answer.

## Read, and the name guard it gives up

`rr-pro-max` holds `Read`; `rr` and `rr-pro` do not (`rr.md` § The document). The harness refuses a Write over an existing file the agent has not read, and lifts that refusal for an agent holding `Read`. An `rr-pro-max` run that derives a slug already taken on the same date therefore claims the earlier run's directory and overwrites its plan and synthesis silently, where the other leads would be refused; its diggers hold no `Read`, so a report path the earlier run left is refused and that digger returns `NOT SAVED` with its report inline. The user ruled this accepted: the lead gains local reads, and the ledger loses its overwrite guard for this lead alone. `Read` also lets the lead open an earlier RR or a digger's report; the prompt forbids reading a report and still admits only web URLs and document identifiers as sources, so a map built on a local file is a prompt violation, not a permitted path.

## Cost

A run costs `r(d + 1) + 6` lead calls; each round of 8 diggers costs 9, and `r` has no bound. Each lead call re-sends the lead's whole context, which grows by every digger's findings, so a round late in a run costs more than an early one. `pfm tokens --family {the chat's session-id prefix}` prices the lead and the diggers per agent type, then the 14 costliest agents one by one, so a long run's cheaper diggers drop out of that list; the number to watch is rounds used, since no ceiling reports a runaway for you.

## Running it

- The `rr-dir` hook's matcher is a pipe-separated list of exact names: the Claude Code hooks reference evaluates a matcher made only of letters, digits, `_`, `-`, spaces, `,` and `|` as "Exact string, or list of exact strings separated by `|` or `,`". A lead missing from `claudelaunch.HookRRDirMatcher` gets no `RR-DIR:` line and returns `NOT SAVED — no RR-DIR line arrived`. A caller can always pass the line in the brief; the brief's line wins.
- Run it from an interactive session or as a sub-agent. A headless `claude -p` wrapper ends its process when its own turn ends, and a heavy lead waiting on its diggers dies with it, its diggers' work lost.
- A new agent type becomes spawnable once the harness reloads its registry; a session that started before `rr-pro-max` was linked answers `Agent type 'rr-pro-max' not found` until then.

## What the runs measured

Two completed runs and one orphaned run on one query (what benchmarks evaluate deep-research agents) set these numbers.

- Width is used: rounds of 6 and 5 diggers, then 9 diggers over a run under the current prompt; the first run's lead held exactly its budget, 18 calls for rounds of 6 and 5.
- The first run stopped after round 2 with 3 sub-areas `partial`, round 2 having settled something, and no stop condition fired; the plan and the stop decision appeared nowhere in its transcript. That is what the status block and the `Coverage` line `Digging ended: {condition}` put on the record.
- Verification over 16 pages: 55 facts checked and 3 struck; then 41 checked — 34 confirmed, 5 `UNCHECKED` behind an HTTP 403, 3 `NOT ON PAGE`, two of which the lead moved to another page, the move the verification rule now forbids. Judging the quote, not the checker's YES or NO, kept a fact whose checker answered NO while quoting the sentence that states it.
- Digger citations of pages never fetched fell from 36% to 20% of findings' links, and harvester reads rose from 0 to 22 calls a run.

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The declaration | `templates/global/agents/rr-pro-max.md` |
| The body it overrides | `templates/global/agents/rr.md`, § Budget and § Spawning |
| The repository digger | `templates/global/agents/clone-distiller.md`, `docs/design/clone-distiller/clone-distiller.md` — the lane text is shared through `templates/global/agents/shared/repo-lane.md` |
| The renderer and its tests | `plugins/engine-sync/internal/source/declarationrender.go`, `plugins/engine-sync/internal/source/declarationrender_test.go`, `plugins/engine-sync/internal/machine/globaldeclarations_test.go` |
| The start hook's matcher | `pfm/internal/claudelaunch/hooks.go` (`HookRRDirMatcher`) |
| The hook's docs | `docs/design/hooks/hooks.md`, `docs/dev/pfm-surface.md` |
| The family doc | `docs/design/RR/rr.md` |
