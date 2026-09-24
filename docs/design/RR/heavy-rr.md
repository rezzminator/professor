# heavy-rr

`heavy-rr` is the `rr` lead with no round ceiling: 8 diggers a round, 16 verification pages, `opus` at effort `medium`, and digging that ends only when the map converges. Every other line of its prompt is `rr.md`'s, so the run, the document, the marks and the verification rules are the family's (`rr.md` in this directory). This file holds what is its own: the caps, the missing ceiling and what brakes a run without one, the cost, how it is run, and what its runs measured.

## Contents

- [Identity](#identity)
- [How the variant is built](#how-the-variant-is-built)
- [No round ceiling](#no-round-ceiling)
- [Cost](#cost)
- [Running it](#running-it)
- [What the runs measured](#what-the-runs-measured)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | variant of `rr`, declared in `templates/global/agents/variants.json` |
| Model, effort | `opus`, `medium` |
| Tools | `WebSearch, WebFetch, Write, Agent, mcp__harvester__findWorks, mcp__harvester__readWork, mcp__harvester__readPage` (inherited from `rr.md`) |
| Spawns | `sub-rr` only |
| Writes | the one RR document, into the directory on its `RR-DIR:` line |
| Start hook | `rr-dir`, matcher `rr\|super-rr\|heavy-rr` |

Description, verbatim: `Exhaustive rr — delegate for "heavy rr", "heavy-rr X" when the map must settle whatever the cost; lighter → super-rr. Returns the saved RR path, then the cited map.`

## How the variant is built

`pfm install` and `pfm codex agents` render it from `rr.md` by the same mechanism as `super-rr`: `name:` and the declared frontmatter keys are overridden, and each `replace` entry swaps one piece of body text that must occur exactly once in `rr.md`'s body, or the render fails naming the text and its count. The swaps, verbatim from `variants.json`:

| `rr.md` text | `heavy-rr` text |
| --- | --- |
| `Spawn at most 4 diggers a round` | `Spawn at most 8 diggers a round` |
| `so 4 diggers carry the entire frontier` | `so 8 diggers carry the entire frontier` |
| `Digging ends when every sub-area is settled, when a round settled nothing and added no sub-area, or at the end of round 3 — a safety ceiling, never a target.` | `Digging ends when every sub-area is settled, or when a round settled nothing and added no sub-area; no round ceiling applies.` |
| `on at most 8 source pages` | `on at most 16 source pages` |

The third swap replaces the whole stop sentence, so any edit to that sentence in `rr.md` must be mirrored in this entry; `TestShippedGlobalAgentVariantsRender` fails until it is.

## No round ceiling

A heavy run digs until the map converges. Two stops remain, both from the family's stop rule:

- every sub-area is `settled` against its `settled when` line;
- a round settled nothing and added no sub-area.

Nothing else ends a run. A lead that keeps adding sub-areas, or judges each round as having settled something, keeps digging; the brake is the lead's own status block, which names every sub-area's state and `Next: round {n}` or `Digging ended: {condition}` after each round, so a run that will not converge is visible in its transcript while it runs. The accepted risk is cost, not a wrong answer.

## Cost

A run costs `r(d + 1) + 5` lead calls; each round of 8 diggers costs 9, and `r` has no bound. Each lead call re-sends the lead's whole context, which grows by every digger's findings, so a round late in a run costs more than an early one. `/tokens --filter rr` lists each lead and digger with its tokens; the number to watch is rounds used, since no ceiling reports a runaway for you.

## Running it

- The `rr-dir` hook's matcher is a pipe-separated list of exact names: the Claude Code hooks reference evaluates a matcher made only of letters, digits, `_`, `-`, spaces, `,` and `|` as "Exact string, or list of exact strings separated by `|` or `,`". A lead missing from `hookRRDirMatcher` gets no `RR-DIR:` line and returns `NOT SAVED — no RR-DIR line arrived`. A caller can always pass the line in the brief; the brief's line wins.
- Run it from an interactive session or as a sub-agent. A headless `claude -p` wrapper ends its process when its own turn ends, and a heavy lead waiting on its diggers dies with it, its diggers' work lost.
- A new agent type becomes spawnable once the harness reloads its registry; a session that started before `heavy-rr` was linked answers `Agent type 'heavy-rr' not found` until then.

## What the runs measured

Two completed runs and one orphaned run on one query (what benchmarks evaluate deep-research agents) set these numbers.

- Width is used: rounds of 6 and 5 diggers, then 9 diggers over a run under the current prompt; the first run's lead held exactly its budget, 18 calls for rounds of 6 and 5.
- The first run stopped after round 2 with 3 sub-areas `partial`, round 2 having settled something, and no stop condition fired; the plan and the stop decision appeared nowhere in its transcript. That is what the status block and the `Coverage` line `Digging ended: {condition}` put on the record.
- Verification over 16 pages: 55 facts checked and 3 struck; then 41 checked — 34 confirmed, 5 `UNCHECKED` behind an HTTP 403, 3 `NOT ON PAGE`, two of which the lead moved to another page, the move the verification rule now forbids. Judging the quote, not the checker's YES or NO, kept a fact whose checker answered NO while quoting the sentence that states it.
- Digger citations of pages never fetched fell from 36% to 20% of findings' links, and harvester reads rose from 0 to 22 calls a run.

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The declaration | `templates/global/agents/variants.json`, entry `heavy-rr` |
| The body it swaps into | `templates/global/agents/rr.md` — the stop sentence is swapped whole |
| The renderer and its tests | `pfm/internal/codexgen/globalvariants.go`, `globalvariants_test.go` |
| The start hook's matcher | `pfm/internal/installer/expected_hooks.go` (`hookRRDirMatcher`), `settings_wiring_test.go`, `settings_dedupe_test.go` |
| The hook's docs | `docs/design/hooks/hooks.md`, `docs/dev/pfm-surface.md` |
| The family doc | `docs/design/RR/rr.md` |
