# RR — Spec of Claude Code's removed /buddy companion and a survey of community revivals

Question: a cited spec of Claude Code's removed `/buddy` companion feature (shipped ~v2.1.8x–2.1.96, removed v2.1.97 on 2026-04-09) plus a survey of community revivals.

**Answer:** `/buddy` was an April Fools 2026 tamagotchi, off by default. Running `/buddy` hatched a companion that sat beside the prompt input box. Its species, rarity and traits were derived from the account ID plus the salt `friend-2026-401`. Only its model-generated name and personality were stored in `~/.claude.json`. Speech-bubble reactions came from a server `buddy_react` endpoint. v2.1.97 removed the client code on 2026-04-09, and the endpoint was emptied on April 10. The main revivals that work today are MCP, hook and statusline rebuilds: ramarivera/coding-buddy (463 stars), fiorastudio/buddy, jrykn/save-buddy, DragonSecurity/buddy-mcp and shawnpetros/claude-buddy. The many rerollers only patch binaries up to v2.1.96.

## Map

### 1. Hatching and identity
- **Seed:** the identity string is `oauthAccount.accountUuid ?? userID ?? 'anon'`. It is combined with the salt `"friend-2026-401"` and hashed into a Mulberry32 PRNG. Five PRNG draws are made in order: rarity, species (uniform over 18), eyes, hat (always none for common), then shiny at 1%. Sources: [ithiria894](https://github.com/ithiria894/claude-code-buddy-reroll/blob/master/README.md), [Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md). [claudefa.st](https://claudefa.st/blog/guide/mechanics/claude-buddy) says: "The salt string is `friend-2026-401`, a nod to April 1st."
- **Hash function: DISPUTED.**
  - FNV-1a: [claudefa.st](https://claudefa.st/blog/guide/mechanics/claude-buddy) ("your user ID gets hashed with FNV-1a, seeded into a Mulberry32 PRNG"), [ithiria894](https://github.com/ithiria894/claude-code-buddy-reroll/blob/master/README.md) and [save-buddy](https://github.com/jrykn/save-buddy).
  - Bun wyhash at runtime, with FNV-1a only as the Node fallback: [dev.to/picklepixel](https://dev.to/picklepixel/how-i-reverse-engineered-claude-codes-hidden-pet-system-8l7) ("the production hash is `Bun.hash()`, which is native C wyhash. The Node.js fallback is FNV-1a") and [Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md).
- **The 18 species:** duck, goose, blob, cat, dragon, octopus, owl, penguin, turtle, snail, ghost, axolotl, capybara, cactus, robot, rabbit, mushroom, chonk ([Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md)). [#45596](https://github.com/anthropics/claude-code/issues/45596) says "18 species. 5 rarity tiers."
- **Rarity:** Common 60%, Uncommon 25%, Rare 10%, Epic 4%, Legendary 1% ([claudefa.st](https://claudefa.st/blog/guide/mechanics/claude-buddy), [Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md)). Shiny is an independent 1%.
- **Stats:** DEBUGGING, PATIENCE, CHAOS, WISDOM, SNARK.
- **Eyes:** · ✦ × ◉ @ °
- **Hats:** none, crown, tophat, propeller, halo, wizard, beanie, tinyduck.
- Source for stats, eyes and hats: [Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md).

### 2. The `~/.claude.json` companion object
- **Stored:** `name` and `personality` (both AI-generated at hatch) and `hatchedAt` ([ithiria894](https://github.com/ithiria894/claude-code-buddy-reroll/blob/master/README.md), [#45596](https://github.com/anthropics/claude-code/issues/45596)).
- **Never stored:** rarity, species, eye, hat, shiny and stats are regenerated from the hash on every read. The stated reason is "so species renames don't break stored companions and users can't edit their way to a legendary" ([Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md)).
- `companion_intro` was injected into the session context, but the speech-bubble comments were not ([#44898](https://github.com/anthropics/claude-code/issues/44898), v2.1.94).

### 3. Rendering, reactions, subcommands and timeline
- **Placement:** it "sits beside the user's input box and occasionally comments in a speech bubble" ([apiyi guide](https://help.apiyi.com/en/claude-code-buddy-terminal-pet-companion-activation-guide-en.html)).
  - #47254 describes it as in the status line ([#47254](https://github.com/anthropics/claude-code/issues/47254)).
- **Sprite size:** "5 lines tall, 12 characters wide, 3 animation frames" ([claudefa.st](https://claudefa.st/blog/guide/mechanics/claude-buddy), [dev.to](https://dev.to/picklepixel/how-i-reverse-engineered-claude-codes-hidden-pet-system-8l7)).
- **Animation timing:** a 500ms tick ([dev.to](https://dev.to/picklepixel/how-i-reverse-engineered-claude-codes-hidden-pet-system-8l7)) and a 15-frame idle loop ([claude-harness.dev](https://claude-harness.dev/en/articles/26-buddy-system)). Both unquoted.
- **Animation bug:** the animation periodically shifted terminal content upward ([#42704](https://github.com/anthropics/claude-code/issues/42704)).
- **Subcommands:** `/buddy` (hatch or show), `/buddy card`, `/buddy pet`, `/buddy mute`, `/buddy unmute`, `/buddy off` ([claudefa.st](https://claudefa.st/blog/guide/mechanics/claude-buddy); pet and off are also in the [apiyi guide](https://help.apiyi.com/en/claude-code-buddy-terminal-pet-companion-activation-guide-en.html)).
- **Reactions:**
  - They were "generated by the same `buddy_react` API endpoint that the native feature used."
  - There was a "30-second cooldown between calls. Addressing your companion by name or triggering a special event (test failure, error) bypasses the cooldown."
  - It fired a greeting at session start or resume.
  - Source: [save-buddy](https://github.com/jrykn/save-buddy).
- **Model behind reactions: DISPUTED.**
  - Claude 3.5 Sonnet, per community research on r/Anthropic: [save-buddy](https://github.com/jrykn/save-buddy).
  - A Haiku 4.5 small-model route in a Bedrock config: [#42364](https://github.com/anthropics/claude-code/issues/42364), unquoted.
  - The prompt text is UNSOURCED.
- **Endpoint shutdown:** as of April 10, 2026, every `buddy_react` response is `{"reaction":""}` ([save-buddy](https://github.com/jrykn/save-buddy)).
- **Timeline:**
  - It was an April Fools feature and off by default ([xeiaso](https://xeiaso.net/notes/2026/claude-code-wins-april-fools/)).
  - Launched April 1, 2026, for v2.1.89 and later ([apiyi guide](https://help.apiyi.com/en/claude-code-buddy-terminal-pet-companion-activation-guide-en.html)).
  - The code first surfaced in the v2.1.88 sourcemap leak on March 31 ([Combjellyshen](https://github.com/Combjellyshen/claude-buddy/blob/main/README.md)).
  - Internal flag `tengu_penguins_enabled` (#42364, unquoted).

### 4. Removal
- The feature was "completely gone in v2.1.97 … Reverting to v2.1.96 brings /buddy back immediately" ([#45517](https://github.com/anthropics/claude-code/issues/45517), April 9, 2026).
- After the removal, `/buddy` returns "Unknown skill: buddy" ([#45525](https://github.com/anthropics/claude-code/issues/45525)).
- "No changelog mention" ([#45596](https://github.com/anthropics/claude-code/issues/45596)).
- No staff comment was found on #45517, #45596 or #47254; the related issues were closed as not planned or as duplicates.

### 5. Community revivals
**Post-removal rebuilds (they work on v2.1.97 and later):**
- **ramarivera/coding-buddy**, the same repo as 1270011/claude-buddy (npm `@ramarivera/coding-buddy`).
  - 463 stars.
  - Layer: MCP, a skill, statusline and hooks. "Anthropic removed `/buddy` in Claude Code v2.1.97. This brings it back — forever."
  - It also lists a 19th species, wyvern. [repo](https://github.com/1270011/claude-buddy)
- **fiorastudio/buddy**
  - About 103–112 stars (unquoted).
  - Layer: MCP, statusline and hooks, across several clients.
  - Lacks: no statusline on Codex. [repo](https://github.com/fiorastudio/buddy)
- **jrykn/save-buddy**
  - 28 stars.
  - Layer: an MCP server with 5 tools, Stop/UserPromptSubmit/SessionStart hooks and a statusline wrapper.
  - Lacks: live AI reactions. Since the endpoint died it uses local templates, and the Haiku fallback is not implemented. [repo](https://github.com/jrykn/save-buddy)
- **DragonSecurity/buddy-mcp**
  - 1 star (unquoted).
  - Layer: MCP. It has a `rescue` command that imports the identity from `~/.claude.json`. [repo](https://github.com/DragonSecurity/buddy-mcp)
- **shawnpetros/claude-buddy**
  - Layer: a starship statusline module, hooks and a `claude -p` worker. Loads via `--plugin-dir` and is in no marketplace.
  - Its art and prompts are original, not the leaked set. [repo](https://github.com/shawnpetros/claude-buddy)
- **Lyellr88/buddy-mcp**
  - Archived. [repo](https://github.com/Lyellr88/buddy-mcp)

**Rerollers and patchers (v2.1.96 and earlier only):**
- **cpaczek/any-buddy**: 614 stars; patches the binary with an auto-patch hook. [repo](https://github.com/cpaczek/any-buddy)
- **grayashh/buddy-reroll**: 239 stars; `bunx`. [repo](https://github.com/grayashh/buddy-reroll)
- **fengshao1227/cc-buddy**: 108 stars; patches cli.js through the AST. It says it "only works with Claude Code <= 2.1.96". [repo](https://github.com/fengshao1227/cc-buddy)
- **RoggeOhta/claude-buddy-reroll**: 10 stars; patches the binary. [repo](https://github.com/RoggeOhta/claude-buddy-reroll)
- **Combjellyshen/claude-buddy**: 7 stars; patches the salt in 3 places in the binary. [repo](https://github.com/Combjellyshen/claude-buddy)
- **kaly7004/buddy-reroll**: 0 stars; a Windows executable. [repo](https://github.com/kaly7004/buddy-reroll)
- **ithiria894/claude-code-buddy-reroll**. [repo](https://github.com/ithiria894/claude-code-buddy-reroll)

**Not revivals:** TeXmeijin/claude-code-mascot-statusline, claude-buddy.dev and rsts-dev/claude-buddy-marketplace.

## Coverage
- Hatching and identity: settled. The hash function is DISPUTED.
- Companion schema: settled.
- Rendering, reactions and subcommands: partial. Narrow-terminal behavior and the reaction prompt are UNSOURCED, and the model is DISPUTED.
- Removal: settled. No staff rationale was found.
- Revivals: settled.
- Digging ended: every sub-area was settled or reduced to named gaps after round 2.

## Verification
12 facts were checked on 3 pages. One was NOT ON PAGE: "claudefa.st says the runtime hash is Bun wyhash." That page says FNV-1a, so the hash is recorded as DISPUTED. Also, the ramarivera page lists 19 species (it adds wyvern), not 18.

## Open rabbit holes
- Official reason for the removal and any v2.1.97 changelog text. Dug, unsettled.
- The `buddy_react` request/response schema and its prompt. Dug, unsettled.
- Which model generated reactions (Sonnet vs Haiku): the r/Anthropic thread. Dug, unsettled.
- Narrow-terminal fallback rendering. Dug, unsettled.
- `tengu_penguins_enabled` flag (#42364) and the rename request (#41908). Undug.
- The buddy.yadongxie.com gallery. Undug.
- Primary v2.1.88 sourcemap `src/buddy/`, to settle the hash dispute. Undug.
