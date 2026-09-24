# super-rr

`super-rr` is the `rr` lead with wider caps and more reasoning: 6 diggers a round, a ceiling of 5 rounds, 12 verification pages, `opus` at effort `medium`. Every other line of its prompt is `rr.md`'s, so the run, the document, the marks and the verification rules are the family's (`rr.md` in this directory). This file holds what is its own: the caps, how the variant is built, what it costs, when a caller picks it, and what its runs measured.

## Contents

- [Identity](#identity)
- [How the variant is built](#how-the-variant-is-built)
- [Cost](#cost)
- [When a caller picks it](#when-a-caller-picks-it)
- [What the runs measured](#what-the-runs-measured)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | variant of `rr`, declared in `templates/global/agents/variants.json` |
| Model, effort | `opus`, `medium` |
| Tools | `WebSearch, WebFetch, Write, Agent, mcp__harvester__read, mcp__harvester__search_literature, mcp__harvester__search_web` (inherited from `rr.md`) |
| Spawns | `sub-rr` only |
| Writes | the one RR document, into the directory on its `RR-DIR:` line |
| Start hook | `rr-dir`, matcher `rr\|super-rr\|heavy-rr` |

Description, verbatim: `Deeper rr for higher stakes — delegate for "super rr", "super-rr X"; exhaustive → heavy-rr. Returns the saved RR path, then the cited map.`

## How the variant is built

`pfm install` and `pfm codex agents` render the variant from `rr.md`: `name:` and the declared frontmatter keys are overridden, and each `replace` entry swaps one piece of body text. The swaps, verbatim from `variants.json`:

| `rr.md` text | `super-rr` text |
| --- | --- |
| `Spawn at most 4 diggers a round` | `Spawn at most 6 diggers a round` |
| `so 4 diggers carry the entire frontier` | `so 6 diggers carry the entire frontier` |
| `or at the end of round 3 — a safety ceiling` | `or at the end of round 5 — a safety ceiling` |
| `on at most 8 source pages` | `on at most 12 source pages` |

- Each swapped text must occur exactly once in `rr.md`'s body. `renderGlobalAgentVariant` (`pfm/internal/codexgen/globalvariants.go`) refuses anything else with `"replace" text {text} occurs {n} times in the body, want exactly 1`, so an edit to `rr.md` that rewords a swapped sentence fails the render instead of shipping a variant with `rr`'s caps.
- `TestShippedGlobalAgentVariantsRender` renders the shipped `variants.json` against the shipped `rr.md` and compiles each variant to its Codex TOML; it is the gate that catches the rewording before an install does.
- The swaps touch the body only; a swapped text found only in the frontmatter counts as absent.
- An installed `pfm` older than the `replace` key reads `variants.json` as malformed: `pfm doctor` reports `global-agents … state=UNREADABLE`, and `pfm install` refuses. The fix is the host mirror build, never an edit to `variants.json`.

## Cost

A run costs `r(d + 1) + 5` lead calls (the family's lead-call budget: one spawn message and one note per digger each round, 2 to open, 1 to verify, 2 to save and return). At the ceiling of 5 rounds of 6 diggers that is 40 calls, each re-sending the lead's whole context. A digger costs the lead one call on return, so the extra width is bought knowingly; the convergence stops (every sub-area settled, or a round that settled nothing and added no sub-area) usually end a run well before the ceiling.

## When a caller picks it

| Need | Agent |
| --- | --- |
| Known sources, their exact words | `collector-rr` |
| A map of a question, cheaply | `rr` |
| A map where a wrong or missing fact costs something | `super-rr` |
| A map that must settle every sub-area whatever the cost | `heavy-rr` |

## What the runs measured

Three runs on one query (how faithfully fetch-and-answer tools quote a page) set these numbers.

- Budget held: rounds of 4 and 2 diggers cost 13 lead calls against a budget of 13; with the round status block in the prompt, a run ended at 2 rounds with `Digging ended: every sub-area settled (after round 2)` on the record.
- Verification is where the variant earns its effort. The first run confirmed 20 of 20 re-checked facts yet missed a quote its digger had made up from the fetch model's own prose; once verification judged the quoted sentence and checked the pages behind `unquoted` facts first, the next run struck 2 facts and corrected 3 figures a digger had drifted (`5000` reported as `50000`, "over 1200" as "1,294"), and the one after struck 4.
- Digger citations of pages never fetched fell from 12% to 5% of findings' links, and harvester reads rose from 2 to 10 calls a run, after `sub-rr` gained the live harvester tools and the snippet rule.
- The return in the family's fixed shape is about 2 KB against about 10 KB when it repeated the map.

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The declaration | `templates/global/agents/variants.json`, entry `super-rr` |
| The body it swaps into | `templates/global/agents/rr.md` |
| The renderer and its tests | `pfm/internal/codexgen/globalvariants.go`, `globalvariants_test.go` |
| The start hook's matcher | `pfm/internal/installer/expected_hooks.go` (`hookRRDirMatcher`) |
| The family doc | `docs/design/RR/rr.md` |
