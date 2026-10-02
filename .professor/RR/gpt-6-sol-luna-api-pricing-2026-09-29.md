# RR — OpenAI API list prices for gpt-6-sol and gpt-6-luna (late Sept 2026)

Question: Quick rr: the current OpenAI API list prices (USD per 1M tokens: input, cached input, output; and how reasoning tokens are billed) for the Codex models `gpt-6-sol` and `gpt-6-luna` (display names GPT-6-Sol, GPT-6-Luna), as of late September 2026. Also note whether reasoning effort (low/medium/high/xhigh) changes the per-token price or only the token count. Cite the official OpenAI pricing page or model page for each number; mark anything not found on an official page as unconfirmed. Goal: price benchmark seats from their token counts.

The official pricing page lists gpt-6-sol at $2.00 input / $0.20 cached input / $10.00 output per 1M tokens, and gpt-6-luna at $0.10 / $0.01 / $0.50 (short context) ([OpenAI pricing](https://developers.openai.com/api/docs/pricing)). The page does not say how reasoning tokens are billed or whether reasoning effort changes the price, so both points are unconfirmed.

## Map

### Prices (official page, Flagship models table, standard tier)
| Model | Context | Input | Cached input | Cache writes | Output |
|---|---|---|---|---|---|
| gpt-6-sol | short | $2.00 | $0.20 | $2.50 | $10.00 |
| gpt-6-sol | long | $4.00 | $0.40 | $5.00 | $15.00 |
| gpt-6-luna | short | $0.10 | $0.01 | $0.125 | $0.50 |
| gpt-6-luna | long | $0.20 | $0.02 | $0.25 | $0.75 |

Source: [OpenAI pricing](https://developers.openai.com/api/docs/pricing), read 2026-09-29. The page does not give the short/long context threshold.
- Uplifts: FedRAMP is 10% above standard, and regional (data-residency) endpoints are 10% above standard for eligible models released on or after March 5, 2026 (same page). The page names Fast mode (`service_tier: "fast"`, formerly priority) but does not give its rates.
- Secondary sources report the same $2/$10 and $0.10/$0.50 figures, and VentureBeat reports that an OpenAI spokesperson called these rates permanent ([VentureBeat](https://venturebeat.com/technology/openai-releases-gpt-6-sol-and-luna-models-slashing-api-costs-50-or-more), search snippet only, unverified).

### Reasoning tokens and effort
- Unconfirmed: the pricing page never says how reasoning tokens are billed. OpenAI's usual convention bills them as output tokens at the output rate, but no page was read that confirms this for GPT-6.
- Unconfirmed: the page says nothing on whether effort (low/medium/high/xhigh) changes the price. No effort-based price rows appear, so effort probably changes only the token count, but that is an inference.

## Coverage
- Per-token list prices: settled (quoted table rows)
- Reasoning-token billing: open
- Effort vs price: open
Digging ended: quick run, no diggers spawned (user asked for a quick rr)

## Verification
The 4 price rows and the uplift sentences were read directly from the page text through harvester_read. WebFetch to developers.openai.com was blocked by the domain check. NOT ON PAGE: reasoning-token billing, effort pricing, the context threshold, and Fast mode rates.

## Open rabbit holes
- undug: the GPT-6 model pages (developers.openai.com/api/docs/models/gpt-6-sol and gpt-6-luna) and the reasoning guide, for reasoning-token billing and effort levels
- undug: the short/long context boundary in tokens
- undug: Fast mode and Batch/Flex multipliers for gpt-6-*
