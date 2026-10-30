---
name: quality:prompt
description: MANDATORY — load before editing any LLM-consumed prompt (CLAUDE.md, agents, commands, skills); leanness plus correctness law for any prompt. `cut <file>` rewrites the target leaner in place. Harness file rules → the `pcm` agent; CLAUDE.md shape → /quality:claude-md; a `description:` → /quality:description; doc shape → /quality:doc; markdown mechanics → /quality:md-forlint.
---

# Prompt Quality

Make every prompt line pay for itself on every invocation, whatever the consumer (Claude Code, an application's own LLM chains, or any future LLM surface).

## The cut test (apply to every line)

> Would removing this line cause the model to make a mistake?

If no — delete it.

## Cut mode — `quality:prompt cut <file>`

Rewrite the target leaner in place: read it, apply every rule below, cut hard. Preserve every distinct behavioral rule, threshold, and behavior-pinning example; unsure = keep and flag. Never weaken a sacred-ground rule (secrets, personal data, and whatever the project's law marks sacred) to save tokens. Every cut names its justification — the duplicate's surviving location or the failed cut test; a negative claim ("no duplicate", "zero references", "unused") is grep-verified before the cut lands, and a duplicate section's heading is grep-checked for citers first (a cited section is a navigation index: keep it or retarget its citers). Report each cut in one line.

## Compact aggressively

Compact the lines the cut test keeps in two passes, repeated until neither fires:

- Merge: rules covering overlapping ground collapse into one that covers both.
- One word for two: where one precise word carries a phrase, use it; recurse clause by clause until removing any word costs meaning.

Before → after, at the aggression this expects:

- `stopping to ask is the only failure.` → `only failure = stop/ask`
- `resolve every ambiguity and blocker yourself and carry the work to completion` → `resolve ambiguity/blocker by yourself & get to completion`
- `**Reuse before you write** — grep for an existing function/type/util and import it before adding one. Never keep a near-copy in sync; extract and call.` → `Reuse code: grep existing function/type/util & import; near-copy → extract & call`

## The prompt stream — audit in context, not in isolation

A prompt rarely loads alone: the model reads one concatenated context assembled from many files. Audit a prompt against that whole stream, not just the file in front of you: a rule may already live in a co-loaded file (duplication), contradict one (conflict), or push the combined context past what the model holds well (budget). (The Claude-harness stream composition is enumerated in the `pcm` agent § Claude-harness prompt law.)

## Size discipline

Every consumer imposes size limits on its prompt files; know the consumer's limits before writing and split via progressive disclosure above them, each reference file over 100 lines opening with a table of contents. The Claude-harness limits live in the `pcm` agent § Claude-harness prompt law.

## Anti-patterns — cut on sight

1. Time-sensitive narration: "On 2026-05-19...", "after the X incident", the one-word recency markers ("now", "recently", "no longer"), deferred-feature notes ("not wired yet", "planned"), and incident logs in references and gotcha lists. Encode the current rule; incidents go in the commit message or epic manifest, future intent in the epic.
2. Dates of change: changelog-style "changed 2026-06-07" lines or update-history dates inside a prompt — version control already timestamps every change.
3. Restating one rule, reworded or repeated across sections (no duplication): two phrasings of one rule, or the same rule echoed in a non-negotiable, a routing-table cell, and a process bullet, make the model pick one arbitrarily and rot out of sync. State each rule once in its canonical home. Before adding a rule, grep the whole file for its key noun; if it already lives somewhere, edit it there and stop. When a rule sits in both step prose and a Rules/checklist section, the Rules section is the canonical home. Two repeats are allowed: a sacred-ground rule's one point-of-use reminder, and the single most important constraint restated last (§ Correctness & model behavior).
4. Frontmatter ↔ body duplication: what `description:` says, the body opening leaves out.
5. Voice flavor that doesn't change behavior: backstory, character arcs, "I built this", "the meta layer", provenance ("adapted from X"). Test clause by clause: inside a voice trait, "(no sunk cost fallacy)" is a rule — keep the kernel, cut the costume. Working prompts carry zero voice; voice belongs in the consumer's dedicated persona layer (for the Claude harness: the `pcm` agent § Claude-harness prompt law).
6. Rationale that rephrases the rule, labeled "Why:" or not: trailing purpose clauses and rationale parentheticals whose content the rule's wording already implies.
7. Negative framing where positive works: "Use prose paragraphs" beats "don't use bullets." Reserve do NOT / NEVER for sacred ground — an isolated "do not X" measurably degrades untargeted behavior (~31.5% collateral).
8. Aggressive emphasis on non-sacred rules: "CRITICAL", "YOU MUST", "MANDATORY", "(MANDATORY)" heading suffixes, capitalized intensifiers. Claude Opus 4.5 and later overtrigger on it. Plain language for ordinary rules; reserve emphasis for sacred ground.
9. Inconsistent terminology: mixing "endpoint / URL / route", "field / box / element", "extract / pull / get". One canonical term per concept, used everywhere.
10. Cross-references that say nothing new ("See § X above" two paragraphs up): if the reference matters, summarize the takeaway inline.
11. Cross-file restatement: a file restating rules or bodies (SQL/SDL/contracts, a whole section) that live in another file. Keep only the local delta: nothing else when that file is co-loaded in its stream (a child config vs the parent it always loads with), plus a doc + section citation when it is only co-read.
12. Multiple options when one default suffices: "Use pypdf, pdfplumber, PyMuPDF, or pdf2image" → "Use pdfplumber. For OCR, use pdf2image+pytesseract."
13. Examples that don't pin down behavior: an example earns its tokens only if the rule alone wouldn't produce the same output. An example naming an external reader of a format (a grep pattern that must keep matching) pins behavior — keep it.
14. Vague descriptions: "Helps with documents", "Processes data" — the consumer cannot route to it; /quality:description owns the fix.
15. Deeply nested file references (entry file → reference.md → details.md): keep references one level deep.
16. Token-heavy formatting: HTML tags (`<example>`, `<div>`), XML-style wrappers, drawn ASCII boxes, decorative dividers, decorative emoji legends, bold labels restating the code block they introduce, stray editing debris (empty fences, orphaned markup) — they cost tokens markdown gives free. Use `## Example` over `<example name=…>`, a fenced block over a drawn box, a single `—` over a rule of dashes. Keep the structure, drop the scaffolding.
17. Run-on list-item definitions: a definition reads `- term: gloss`, a plain term, a colon, one tight gloss. The smell is a bold term, an em-dash, and clauses chained with `;` / `—` into a run-on. `- High: the default — step up only for a genuinely hard problem` beats `- **High** — the level you reach for in nearly all work; balances depth against cost; step up only when the task truly demands it`. Same rule for command references: a gloss restating the command's own flags is dead weight.
18. Self-verification rituals: "Double-check your answer", "include a final verification step", a subagent spawned only to re-check the caller's own output — stacked instructions buy over-verification, not quality. An independent reviewer (different agent, fresh context, own protocol) or a pass applying a law's own checklist is legitimate and is not this.
19. Recall muzzles: "Be conservative", "only report high-severity/certain findings", "under-flagging beats nitpicking" — taken literally, they suppress true findings (measured recall drop on review tasks). Have the model report everything; filter severity in a separate pass.
20. Weaker-model scaffolding: workarounds tuned to an older model generation — an instruction restated "so it isn't forgotten", escalating force markers on a rule already followed, an undated claim of a model weakness ("the model forgets", "can't count") justifying a rule. Attribute such claims to a model generation or delete them; re-test them when the model line changes.
21. Fabricated context: a hardcoded fake user quote or invented conversation history as a forcing device — the model sees in its own context that it is false; state the requirement directly.

## Correctness & model behavior

**Numbers.**

- Inject the computed value: compute a ratio/fraction/count-of-a-count ("no leaf > 40% of segments") in code as `{max_segments_per_topic}` and inject the number.
- One operative number. An illustrative "12" hardcoded beside a computed placeholder is two caps, one fake — illustrative numbers stay "e.g." in prose; the injected variable is the only cap.

**Instruction conflicts — one pull at a time.**

- No gas-and-brake. A cap + a completeness demand + an overrun penalty fight each other and the model thrashes; audit for mutually-exclusive rules, keep one.
- Rules interact. Changing one rule can worsen a failure elsewhere (whack-a-mole) — test every rule change against the other failure modes, not just the one it aims at.

**Structure & order.**

- Open with a one-line job statement.
- Define each term before its first use.
- Give every injected slot (`{prior_block}`, `{transcript}`) a one-line "what it is / what not to infer".
- Restate the single most important constraint at the very end.

**Language.**

- A translated prompt is a new prompt: a fresh translation silently regresses; validate each language independently.

**When a rule fails in practice:**

1. Observe the model's actual output, not how the prompt reads; resolve any rule contradicting the failing one and cut the noise burying it.
2. Add a contrastive example (§ Teaching by example).
3. Reframe positively (anti-pattern 7).
4. Shape the input, or enforce in code, so the wrong output is unreachable (§ Enforce invariants with mechanisms), e.g. leave the turns the model may not cite out of the index it cites from.

## Teaching by example — when a stated rule keeps leaking

When the model keeps violating a rule the prompt already states, that is confusion about where the rule applies, not disobedience — sharpening the wording or piling on emphasis only adds noise. Reach for a contrastive example: show the tempting wrong answer and the trap that produces it, then the correct one (✗→✓), drawn from a real failure. Counterweight every "avoid X" example with an "X is correct here" example — a contrastive example against a frequent label otherwise teaches the model to avoid that label everywhere, suppressing its legitimate uses.

## Example — encoding an incident rule

Wrong (in the prompt):

> The seed script once published the analysis request before registering the result waiter, so the seed hung to its full timeout. Never publish before registering again.

Right (in the prompt):

> Register the result waiter before publishing the analysis request.

## Enforce invariants with mechanisms, not repetition

For things that must happen every time (formatting, validation, secret-scanning), use the consumer's deterministic enforcement layer instead of repeating "ALWAYS DO X" in the prompt — and once a mechanism owns an invariant, delete the prompt rule that restated it. The Claude-harness mechanism is hooks: the `pcm` agent § Claude-harness prompt law.

## Pre-commit self-check

Before saving any prompt file, and so before any commit, check it against § Size discipline, every anti-pattern and § Correctness & model behavior, then the colleague test: could a colleague with no context follow it? Last, the cut test on every line: would removing it cause the model to make a mistake? If no, delete it.
