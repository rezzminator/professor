# rr-pro

`rr-pro` is the `rr` lead with wider caps, more reasoning and the repository lane: 6 diggers a round, a ceiling of 5 rounds, 12 verification pages, `opus` at effort `medium`, and a `clone-distiller` for each repository whose code holds an answer. Every other line of its prompt is `rr.md`'s, so the run, the document, the marks and the verification rules are the family's (`rr.md` in this directory). This file holds what is its own: the caps, how the variant is built, what it costs, when a caller picks it, and what its runs measured.

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
| Kind | variant of `rr`, declared in `templates/global/agents/rr-pro.md` |
| Model, effort | `opus`, `medium` |
| Tools | `WebSearch, WebFetch, Write, Agent, mcp__harvester-professor__read, mcp__harvester-professor__search_literature, mcp__harvester-professor__search_web` (inherited from `rr.md`) |
| Spawns | `sub-rr`, and `clone-distiller` for a repository sub-area |
| Writes | the one RR document, into the directory on its `RR-DIR:` line |
| Start hook | `rr-dir`, matcher `rr\|rr-pro\|rr-pro-max\|gpt-rr\|gpt-rr-pro\|gpt-rr-pro-max` |

Description, verbatim: `Maps a query deeper — tier 2, higher stakes: rr → here → rr-pro-max. Delegate for "rr pro", "rr-pro X", when a wrong or missing fact costs something or the answer is partly in a repository's code. Returns the saved RR path, then the cited map.`

## How the variant is built

`pfm install` renders the variant from `rr.md` through engine-sync: its frontmatter overrides `description` and `effort`, its body replaces § Budget, and `extends: [shared/repo-lane.md]` supplies § Spawning. Both sections carry their base-text hash in `pins:`:

| `rr.md` section | `rr-pro` text |
| --- | --- |
| § Budget | `- Diggers: at most 6 a round.` · `- Rounds: at most 5 — a safety ceiling, never a target.` · `- Verification: at most 12 source pages.` |
| § Spawning | the shared file `shared/repo-lane.md`: `sub-rr` for the web, `clone-distiller` for a sub-area whose answer lives in a repository's code — the full text is in `clone-distiller.md`, § Who spawns it |

- Each override carries the sha256 pin of the `rr.md` section it replaces. An edit to `rr.md`'s § Budget or § Spawning fails the render with `SECTION-DRIFT rr-pro: rr.md § {heading} changed since pinned …` until the override is reviewed and `engine-sync variants pin rr-pro` re-pins it; a renamed or deleted heading fails with `SECTION-MISSING`. The procedure's steps cite § Budget and § Spawning by name, so an override changes the caps or the lane without touching a step.
- The fence's `templates.generated-agents` step (`infra/fence/checks.sh`, `checks_templates_generated_agents`) renders the shipped declaration and agent bodies through the built engine-sync binary, so a drifted pin fails before an install does.
- An installed engine-sync older than `0.3.0-alpha.2` cannot compile declaration files, and `pfm install` refuses. The fix is the host mirror build, never an edit to the declaration.

## Cost

A run costs `r(d + 1) + 5` lead calls (the family's lead-call budget: one spawn message and one note per digger each round, 2 to open, 1 to verify, 2 to save and return). At the ceiling of 5 rounds of 6 diggers that is 40 calls, each re-sending the lead's whole context. A digger costs the lead one call on return, so the extra width is bought knowingly; the convergence stops (every sub-area settled, or a round that settled nothing and added no sub-area) usually end a run well before the ceiling.

## When a caller picks it

| Need | Agent |
| --- | --- |
| A map of a question, cheaply | `rr` |
| A map where a wrong or missing fact costs something, or whose answer is partly in a repository's code | `rr-pro` |
| A map that must settle every sub-area whatever the cost | `rr-pro-max` |

## What the runs measured

Three runs on one query (how faithfully fetch-and-answer tools quote a page) set these numbers.

- Budget held: rounds of 4 and 2 diggers cost 13 lead calls against a budget of 13; with the round status block in the prompt, a run ended at 2 rounds with `Digging ended: every sub-area settled (after round 2)` on the record.
- Verification is where the variant earns its effort. The first run confirmed 20 of 20 re-checked facts yet missed a quote its digger had made up from the fetch model's own prose; once verification judged the quoted sentence and checked the pages behind `unquoted` facts first, the next run struck 2 facts and corrected 3 figures a digger had drifted (`5000` reported as `50000`, "over 1200" as "1,294"), and the one after struck 4.
- Digger citations of pages never fetched fell from 12% to 5% of findings' links, and harvester reads rose from 2 to 10 calls a run, after `sub-rr` gained the live harvester tools and the snippet rule.
- The return in the family's fixed shape is about 2 KB against about 10 KB when it repeated the map.

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The declaration | `templates/global/agents/rr-pro.md` |
| The body it overrides | `templates/global/agents/rr.md`, § Budget and § Spawning |
| The repository digger | `templates/global/agents/clone-distiller.md`, `docs/design/clone-distiller/clone-distiller.md` — the lane text is shared through `templates/global/agents/shared/repo-lane.md` |
| The renderer and its tests | `plugins/engine-sync/internal/source/variantrender.go`, `plugins/engine-sync/internal/machine/globalvariants_test.go` |
| The start hook's matcher | `pfm/internal/claudelaunch/hooks.go` (`HookRRDirMatcher`) |
| The family doc | `docs/design/RR/rr.md` |
