# RR — Anthropic first-party API list prices for Claude Opus 5.5 and Sonnet 5.5, vs Opus 5, Sonnet 5, Fable 5.1

Question: the current Anthropic first-party API list prices for Claude Opus 5.5 (`claude-opus-5-5`) and Claude Sonnet 5.5 (`claude-sonnet-5-5`), both recently released. Per model, USD/MTok: base input; output; 5m cache write; 1h cache write; cache read; long-context tier; Batch discount; fast mode. Compare base input / output / cache read for Opus 5, Sonnet 5, Fable 5.1. Confirm or refute the cached internal table (2026-09-25): Opus 5.5 = $4 / $20 / $0.20 cache read; Sonnet 5.5 = $2 / $10 / $0.20.

## Answer

Primary pages confirm all six numbers in the internal table. Opus 5.5 costs [$4 input / $20 output with a $0.20 cache read](https://platform.claude.com/docs/en/about-claude/pricing), [20% less per token than Opus 5 and 60% less on cache reads](https://www.anthropic.com/news/claude-opus-5-5). Sonnet 5.5 costs [$2 / $10 with a $0.20 cache read](https://platform.claude.com/docs/en/models/sonnet-5-5/overview), [the same as Sonnet 5](https://platform.claude.com/docs/en/about-claude/pricing). Neither model has a long-context surcharge ([Claude 4.6 and later models get the full 1M window at standard pricing](https://platform.claude.com/docs/en/about-claude/pricing)). Fast mode is [offered for Opus 5.5 at $8 / $40](https://platform.claude.com/docs/en/build-with-claude/fast-mode) but [not for Sonnet 5.5](https://platform.claude.com/docs/en/build-with-claude/fast-mode).

## Map

Source keys:
- P = https://platform.claude.com/docs/en/about-claude/pricing
- O = https://platform.claude.com/docs/en/models/opus-5-5/overview
- W = https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5
- A = https://www.anthropic.com/news/claude-opus-5-5
- S = https://platform.claude.com/docs/en/models/sonnet-5-5/overview
- F = https://platform.claude.com/docs/en/build-with-claude/fast-mode
- C = https://claude.com/pricing

### Claude Opus 5.5 (`claude-opus-5-5`, released September 22, 2026 per [O](https://platform.claude.com/docs/en/models/opus-5-5/overview) and [A](https://www.anthropic.com/news/claude-opus-5-5))

| Rate | USD / MTok | Sources |
|---|---|---|
| Base input | $4 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [O](https://platform.claude.com/docs/en/models/opus-5-5/overview), [A](https://www.anthropic.com/news/claude-opus-5-5), [C](https://claude.com/pricing) |
| Output | $20 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [O](https://platform.claude.com/docs/en/models/opus-5-5/overview), [A](https://www.anthropic.com/news/claude-opus-5-5), [C](https://claude.com/pricing) |
| 5-minute cache write | $5 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [O](https://platform.claude.com/docs/en/models/opus-5-5/overview), [C](https://claude.com/pricing) ("Write $5") |
| 1-hour cache write | $8 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [O](https://platform.claude.com/docs/en/models/opus-5-5/overview), [W](https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5) |
| Cache read (hit and refresh) | $0.20 (0.05x input, instead of the standard 0.1x) | [P](https://platform.claude.com/docs/en/about-claude/pricing): "On Claude Opus 5.5, a cache hit costs 5% of the standard input price ($0.20 USD per million tokens)"; also [O](https://platform.claude.com/docs/en/models/opus-5-5/overview), [A](https://www.anthropic.com/news/claude-opus-5-5), [C](https://claude.com/pricing) |
| Long-context tier | None; 1M context at standard rates | [P](https://platform.claude.com/docs/en/about-claude/pricing): "Claude 4.6 and later models ... include the full 1M token context window at standard pricing." |
| Batch API | 50% off: $2 input / $10 output | [P](https://platform.claude.com/docs/en/about-claude/pricing) (batch table), [W](https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5): "Batch processing is half price: $2 and $10." |
| Fast mode (research preview, Claude API only) | $8 input / $40 output | [P](https://platform.claude.com/docs/en/about-claude/pricing), [F](https://platform.claude.com/docs/en/build-with-claude/fast-mode), [A](https://www.anthropic.com/news/claude-opus-5-5) |

### Claude Sonnet 5.5 (`claude-sonnet-5-5`, released September 28, 2026 per [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview))

| Rate | USD / MTok | Sources |
|---|---|---|
| Base input | $2 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview), [C](https://claude.com/pricing) |
| Output | $10 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview), [C](https://claude.com/pricing) |
| 5-minute cache write | $2.50 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview), [C](https://claude.com/pricing) |
| 1-hour cache write | $4 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview) |
| Cache read | $0.20 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview), [C](https://claude.com/pricing) |
| Long-context tier | None; 1M context at standard rates | [P](https://platform.claude.com/docs/en/about-claude/pricing) (same "Claude 4.6 and later" sentence) |
| Batch API | 50% off: $1 input / $5 output | [P](https://platform.claude.com/docs/en/about-claude/pricing) (batch table), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview) ("50% discount on input and output") |
| Fast mode | Not offered | [F](https://platform.claude.com/docs/en/build-with-claude/fast-mode) lists supported models as Opus 5.5, Opus 5 and Opus 4.8 only; the [P](https://platform.claude.com/docs/en/about-claude/pricing) fast-mode table lists only Opus models |

### Comparison (USD / MTok)

| Model | Input | Output | Cache read | Sources |
|---|---|---|---|---|
| Opus 5.5 | $4 | $20 | $0.20 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [C](https://claude.com/pricing) |
| Opus 5 | $5 | $25 | $0.50 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [C](https://claude.com/pricing) |
| Sonnet 5.5 | $2 | $10 | $0.20 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [C](https://claude.com/pricing) |
| Sonnet 5 | $2 | $10 | $0.20 | [P](https://platform.claude.com/docs/en/about-claude/pricing), [C](https://claude.com/pricing) |
| Fable 5.1 | $10 | $50 | $0.25 (0.025x input) | [P](https://platform.claude.com/docs/en/about-claude/pricing), [C](https://claude.com/pricing) |

What changed in the 5.5 generation:
- Opus prices dropped. The announcement says "Input and output tokens are $4 and $20 per million, 20% less than Opus 5" and cache reads are "$0.20 per million tokens, 60% less than Opus 5" ([A](https://www.anthropic.com/news/claude-opus-5-5)).
- Opus 5.5 fast mode is cheaper at $8 / $40, against $10 / $50 for Opus 5 ([P](https://platform.claude.com/docs/en/about-claude/pricing)).
- Sonnet 5.5 costs the same as Sonnet 5. The Sonnet 5 rates of $2 / $10, first introductory, "is now the standard price. The previously scheduled increase to $3/$15 ... will not occur" ([P](https://platform.claude.com/docs/en/about-claude/pricing), footnote 3).

Other context:
- The general caching rule is 5m write = 1.25x input, 1h write = 2x input and read = 0.1x input, with exceptions for Fable 5.1 / Mythos 5.1 (0.025x) and Opus 5.5 (0.05x) ([P](https://platform.claude.com/docs/en/about-claude/pricing)). Every cell above is a published figure; none is derived from this rule.
- US-only inference (`inference_geo: "us"`) adds a 1.1x multiplier on all token categories ([P](https://platform.claude.com/docs/en/about-claude/pricing)).
- Bedrock and Google Cloud are priced separately by the cloud provider. Regional and multi-region endpoints there carry a 10% premium over global endpoints ([P](https://platform.claude.com/docs/en/about-claude/pricing)).

## Coverage

- Opus 5.5 rates: settled.
- Sonnet 5.5 rates: settled.
- Comparison rows: settled.
- Corroboration from claude.com/pricing and the announcements: partial. No anthropic.com launch announcement for Sonnet 5.5 was found. Its overview page links none, while the Opus 5.5 overview does.

Digging ended: every rate is settled; the only unsettled question is whether a Sonnet 5.5 announcement exists on anthropic.com.

## Verification

- 14 facts were checked by quote against 3 pages ([A](https://www.anthropic.com/news/claude-opus-5-5), [C](https://claude.com/pricing), [S](https://platform.claude.com/docs/en/models/sonnet-5-5/overview)). All were confirmed.
- Pages [P](https://platform.claude.com/docs/en/about-claude/pricing) and [O](https://platform.claude.com/docs/en/models/opus-5-5/overview) were read in full by the lead.
- Pages [W](https://platform.claude.com/docs/en/models/opus-5-5/whats-new-opus-5-5) and [F](https://platform.claude.com/docs/en/build-with-claude/fast-mode) were quoted by a digger. Their figures match [P](https://platform.claude.com/docs/en/about-claude/pricing).
- NOT ON PAGE: none. UNCHECKED: none.

## Open rabbit holes

- Sonnet 5.5 launch announcement on anthropic.com (dug, unsettled): only third-party coverage was found. Unite.AI says the release kept Sonnet 5 pricing.
- Bedrock and Vertex per-model rates for the 5.5 models (undug; out of scope).
- The 5-minute and 1-hour cache-write and batch rates of the comparison models are in [P](https://platform.claude.com/docs/en/about-claude/pricing) but were not requested, so they are not tabled here.
