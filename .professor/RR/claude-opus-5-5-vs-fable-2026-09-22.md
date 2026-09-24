# RR — Claude Opus 5.5: system card, benchmarks, working manual, and head-to-head vs Fable 5.1

Question: Cited aggregation of everything Anthropic (and credible third parties) have published about Claude Opus 5.5 (claude-opus-5-5): system card PDF, benchmarks (official vs third-party), working manual (docs, pricing, effort/thinking, prompting), and a head-to-head section Opus 5.5 vs Fable 5.1.

**Answer.** Claude Opus 5.5 (`claude-opus-5-5`) launched on 2026-09-22 at $4/$20 per MTok. Anthropic's own table puts it ahead of Fable 5.1 on all nine launch benchmarks, and the docs recommend it as the default model, with Fable 5.1 kept for "demanding reasoning and long-horizon agentic work". Fable 5.1 is real: a Mythos-class model, generally available since 2026-09-01, priced at $10/$50. Because Opus 5.5 is less than a day old, no independent leaderboard lists it yet. Every benchmark figure below is Anthropic's own.

## 1. Identity and release
- Release date is September 22, 2026. The API ID is `claude-opus-5-5`, and the model is "now available on all platforms, including Amazon Web Services, Google Cloud, and Microsoft Azure" ([anthropic.com/claude-opus-5-5](https://www.anthropic.com/claude-opus-5-5); [the-decoder](https://the-decoder.com/claude-opus-5-5-matches-fable-5-1-at-40-percent-lower-cost-as-anthropic-promises-to-fix-claudish-writing/)).
- It is the first model in the Claude 5.5 family. Sonnet 5.5 and Haiku 5.5 are "expected in the coming weeks" (search snippet only, unquoted).
- It was "tested before release by external evaluators, including Frontier Design and METR" ([anthropic.com](https://www.anthropic.com/claude-opus-5-5)).

## 2. System card (PDF)
- **Found and fetched:** [Claude Opus 5.5 System Card PDF](https://www-cdn.anthropic.com/fc1b44717c85dc068bc6ba5024219938094694bd/Claude%20Opus%205.5%20System%20Card.pdf), dated September 22, 2026. A digger fetched the full text (about 210K tokens) with the harvester. My step-6 WebFetch of the same PDF failed with `maxContentLength size of 10485760 exceeded`, so every quote in this section is marked UNCHECKED. Each one was still read verbatim from the PDF by the digger.
- Knowledge cutoff: "Claude Opus 5.5's knowledge cutoff date is June 2026." The docs agree: reliable knowledge cutoff and training-data cutoff are both Jun 2026 ([models overview](https://platform.claude.com/docs/en/models/overview), confirmed).
- Positioning: "It is an upgrade to Claude Opus 5, with gains in coding, agentic and computer use tasks, mathematical and scientific reasoning, and long-horizon professional work. On many evaluations, it matches or exceeds Claude Fable 5.1 and Claude Mythos 5.1."
- RSP / capability level: "we treat Opus 5.5 as having CB-1 capabilities (relating to the synthesis of non-novel weapons) but not CB-2 capabilities (relating to the synthesis of novel weapons)." **The card text that was extracted contains no explicit ASL-N label.**
- AI R&D: "at or slightly above those of Claude Mythos 5.1, but it remains far from substit[uting]…" The rest of the sentence was not extracted.
- Chem/bio weakness: "it did not improve on several of the weaknesses we considered disqualifying for CB-2 in that model. These failure modes limit its ability to substitute for scarce human expertise."
- Bio safeguards: "the same expanded biological safeguards that we have applied to Claude Fable 5 and Claude Fable 5.1."
- Alignment: Opus 5.5 is "the strongest-performing model we've tested to date" on the automated behavioral audit, and "much less likely than recent models to take hard-to-reverse actions" ([anthropic.com](https://www.anthropic.com/claude-opus-5-5); the audit wording comes from my step-1 fetch).
- **Behavioral quirk: evaluation awareness.** "We see signs that Opus 5.5 often suspects it is being evaluated, which challenges our ability to assess how it will act in the vast variety of real-world settings it is deployed in" ([anthropic.com](https://www.anthropic.com/claude-opus-5-5), confirmed).
- Safeguard routing: "most cybersecurity tasks will be re-routed to Opus 4.8" ([anthropic.com](https://www.anthropic.com/claude-opus-5-5), confirmed). Flagged bio / frontier-LLM requests go to Opus 5 ([the-decoder](https://the-decoder.com/claude-opus-5-5-matches-fable-5-1-at-40-percent-lower-cost-as-anthropic-promises-to-fix-claudish-writing/)).
- Anti-distillation: "preserved thinking" stops API users from editing prior context to extract reasoning, for accounts created on or after 2026-08-31 ([anthropic.com](https://www.anthropic.com/claude-opus-5-5)). The docs say the same: prefix-binding is enforced "for accounts created on or after August 31, 2026, 00:00 UTC" ([what's new](https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5), confirmed).
- **Not extracted from the card:** its own full benchmark table, METR's specific findings, and Gray Swan prompt-injection data.

## 3. Benchmarks: official vs third-party
All official figures come from [anthropic.com/claude-opus-5-5](https://www.anthropic.com/claude-opus-5-5) and were run with "adaptive thinking at max effort". The-decoder reproduces the same table, so it counts as the same origin, not independent confirmation.

| Benchmark | Opus 5.5 (official) | Fable 5.1 (official) | Opus 5 (official) | Third-party |
|---|---|---|---|---|
| Terminal-Bench 4.0 | 66.4% | 55.8% | 52.3% | not published (tbench.ai not yet listing) |
| FrontierCode v1.1 | 54.4% | 50.3% | 48.0% | not published |
| CursorBench 4.0 | 57.8% | 51.8% | 46.6% | not published |
| GDPval-AA v2.1 | 1846 Elo | 1735 | 1708 | not published |
| AutomationBench | 40.0% | 31.4% | 26.9% | not published |
| Humanity's Last Exam (tools) | 67.7% | 65.6% | 63.6% | not published |
| Terminal-Bench-Science 0.1 | 58.7% | 52.6% | 29.0% | not published |
| OSWorld 2.0 (partial) | 81.8% | 80.7% | 74.0% | not published |
| Chartography (tools) | 89.0% | 88.4% | 83.4% | not published |

Only the Terminal-Bench 4.0 pair was confirmed in step 6. The remaining cells come from my step-1 fetch of the same page.

Third-party status: each site below was looked up successfully, and Opus 5.5 is not listed on any of them yet.
- **Artificial Analysis**: no per-model page. A release footer reads "Claude Opus 5.5, 5 models, 58 Highest Intelligence" but gives no breakdown ([AA](https://artificialanalysis.ai/models/releases/claude-opus-5), unquoted).
- **Arena / LMArena**: no row yet.
- **ARC Prize**: latest entry is Opus 5.
- **METR**: no time-horizon measurement for Opus 5.5 or Fable 5.1.
- **Zvi Mowshowitz and Simon Willison**: no Opus 5.5 posts yet.

## 4. Working manual (docs)
Sources: [models overview](https://platform.claude.com/docs/en/models/overview), [pricing](https://platform.claude.com/docs/en/about-claude/pricing), [what's new](https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5), [prompting Opus 5.5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5). All items were confirmed in step 6.

**Specs:**
- 1M context, 128K max output. Up to 300k output on the Batch API with the `output-300k-2026-03-24` beta header.
- Adaptive thinking is always on. Default effort is `medium`. Latency class is "Moderate". Retirement is not sooner than September 22, 2027.
- 1M context is billed at standard pricing: "A 900k-token request is billed at the same per-token rate as a 9k-token request."

**Pricing (per MTok):**
- Input $4, output $20.
- Cache writes: 5-minute $5, 1-hour $8.
- Cache hits: $0.20 (0.05x base input).
- Batch: $2 / $10.
- Fast mode (research preview, Claude API only): $8 / $40.

**Breaking changes from Opus 5:**
- `thinking: disabled` or `budget_tokens` returns a 400 `invalid_request_error`.
- `tool_choice` `any` or `tool` returns a 400.
- Thinking blocks are tied to the model and the conversation.
- `computer_20251124` is rejected on the Claude API and Google Cloud.
- Text written between tool calls now arrives in `thinking` blocks, which are empty at the default `display`.

**Do:**
- Start at `medium`, set effort explicitly, and "test several levels against your own evals rather than carrying over the setting you used on Claude Opus 5."
- Set `max_tokens` with room for thinking. 128,000 "has worked well" for agentic coding.
- Lower effort, not prompt wording, when you want less thinking.
- Use per-message effort (beta) to change effort without breaking the cache.
- Keep conversations append-only. Change instructions through mid-conversation system messages.
- For forced-tool use cases, use `tool_choice: auto` plus `strict: true`, or structured outputs.
- Read responses "by block type", never by position.
- For unattended agents, treat a text-only `end_turn` as a report, not as proof the task is done. Allow two or three automatic continuations at most.
- For progress updates, set `display: "updates"`.
- Wrap text the user pastes in `<pasted_content id=…>` tags to resist injection.
- For multi-agent harnesses, give elapsed-time budgets (e.g. `elapsed 340s / 1200s`).
- For frontend work, name the specific styles to avoid.

**Don't:**
- Keep "think carefully" lines in chat system prompts. Removing them made replies start sooner.
- Ask the model to write out its reasoning in the response. That can be declined under `reasoning_extraction`. Use `display: "summarized"` instead.
- Add a tool or change the system prompt mid-session. Either one invalidates earlier thinking blocks.
- Rely on "avoid a generic AI look" style instructions.

**Speed:** "generates output tokens more than 30 percent faster than Claude Opus 5 and tends to finish the same task with fewer tokens."

## 5. Opus 5.5 vs Fable 5.1
Fable 5.1 exists. Its system card, "[Claude Fable 5.1 & Claude Mythos 5.1 System Card](https://www-cdn.anthropic.com/0339e6a7c5c7b87f5c07798616dc32c215d14235/Claude%20Fable%205.1%20&amp;%20Claude%20Mythos%205.1%20System%20Card.pdf)", was fetched by a digger with the harvester (UNCHECKED in step 6). It says Fable 5.1 "includes additional safeguards … Claude Mythos 5.1 is the same model with more permissive safeguards."

| Dimension | Opus 5.5 | Fable 5.1 |
|---|---|---|
| Release | 2026-09-22 ([anthropic](https://www.anthropic.com/claude-opus-5-5)) | 2026-09-01 ([Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5-1.html)) |
| Positioning | "For long-running agentic coding and knowledge work"; the docs' starting default ([overview](https://platform.claude.com/docs/en/models/overview)) | "For demanding reasoning and long-horizon agentic work, or when your evals on Claude Opus 5.5 at higher effort still fall short" ([overview](https://platform.claude.com/docs/en/models/overview)) |
| Price in/out per MTok | $4 / $20 ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)) | $10 / $50 ([pricing](https://platform.claude.com/docs/en/about-claude/pricing); [VentureBeat](https://venturebeat.com/technology/anthropics-claude-fable-5-1-and-mythos-5-1-arrive-with-a-75-cost-reduction-for-fable-cache-reads)) |
| Cache hit | $0.20 (0.05x) | $0.25 (0.025x) ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)) |
| Batch in/out | $2 / $10 | $5 / $25 ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)) |
| Fast mode | $8 / $40 | not offered (not on the fast-mode list) ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)) |
| Context / max output | 1M / 128K | 1M / 128K ([overview](https://platform.claude.com/docs/en/models/overview); [Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5-1.html)) |
| Knowledge cutoff | Jun 2026 | Jun 2026 ([overview](https://platform.claude.com/docs/en/models/overview)) |
| Thinking / default effort | Adaptive always on / `medium` | Adaptive always on / `high` ([overview](https://platform.claude.com/docs/en/models/overview)) |
| Latency class | Moderate | Slower ([overview](https://platform.claude.com/docs/en/models/overview)) |
| Measured tokens/s | not published (independent) | 66.2 t/s per Artificial Analysis (search snippet, unquoted) |
| Rate limits Start (RPM/ITPM/OTPM) | 1,000 / 2,000,000 / 400,000 | 1,000 / 500,000 / 100,000, a limit shared with Fable 5 ([rate limits](https://platform.claude.com/docs/en/api/rate-limits)) |
| Rate limits Build | 5,000 / 5,000,000 / 1,000,000 | 2,000 / 1,500,000 / 300,000 ([rate limits](https://platform.claude.com/docs/en/api/rate-limits)) |
| Rate limits Scale | 10,000 / 10,000,000 / 2,000,000 | 4,000 / 4,000,000 / 800,000 ([rate limits](https://platform.claude.com/docs/en/api/rate-limits)) |
| Benchmarks | leads all 9 official rows (table in §3) | behind on all 9 official rows; no independent figures |
| CB level | CB-1, not CB-2 (Opus card, UNCHECKED) | CB-1 (Fable card, UNCHECKED; the full clause was not extracted) |
| Safeguards | bio safeguards "same as Claude Fable 5.1's"; cyber requests routed to Opus 4.8; `reasoning_extraction` refusals ([prompting](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5)) | dual-use cyber and life-sciences blocking classifiers; "Refusal rates on this model are materially higher than on previous Claude models" ([Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5-1.html)) |
| Thinking-block portability | reads blocks from Opus 5 and earlier, not from Fable or Mythos | Fable 5.1 and Mythos 5.1 read Opus 5.5 blocks on the Claude API ([what's new](https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5)) |
| ASL label | not published (not found) | not published (not found) |

## Coverage
- System card (Opus 5.5): **partial**. The PDF was found and read. Missing: the card's own benchmark table, an ASL label, and METR's findings.
- Benchmarks, official: **settled**. Third-party: **open**. Nothing is listed yet because the model launched the same day.
- Working manual: **settled**.
- Fable 5.1 identity and system card: **settled**. The CB clause and the card's benchmark cells were only partly extracted.
- Head-to-head: **settled**, except independent speed measurements and the ASL label.

## Verification
33 facts checked across 8 pages.
- **UNCHECKED — `maxContentLength size of 10485760 exceeded`:** all 6 Opus 5.5 system-card quotes (cutoff, "matches or exceeds", CB-1/CB-2, bio safeguards, CB-2 weaknesses, AI R&D). The Fable 5.1 system-card quotes were not sampled.
- **NOT ON PAGE:** "five-hour subscriber usage limits increase by 20 percent" on anthropic.com, which says only that limits are "increasing". The figure appears only on [the-decoder](https://the-decoder.com/claude-opus-5-5-matches-fable-5-1-at-40-percent-lower-cost-as-anthropic-promises-to-fix-claudish-writing/) and is removed from the map.

## Rabbit holes left open
- The card's full benchmark table and any explicit ASL/RSP deployment standard. This needs a page-by-page read of the PDF, since WebFetch cannot open it (10 MB limit).
- METR's pre-release findings for Opus 5.5, and any time-horizon figure.
- Independent leaderboards (Artificial Analysis, Arena, ARC Prize, tbench.ai, SWE-bench). Check again in the coming days.
- The scale of the evaluation-awareness problem and how Anthropic mitigates it.
- The Gray Swan prompt-injection data behind the parity-with-Fable claim.
- The September 2026 distillation threat report cited as the reason for preserved thinking.
- Life Sciences Verification Program and Mythos 5.1 (Glasswing) access criteria.
