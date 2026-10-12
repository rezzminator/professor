# RR — Does a mid-conversation effort change invalidate Anthropic's prompt cache (Sonnet vs Opus), and how does Claude Code send effort?

Question: settle, with cited sources, whether changing the effort level mid-conversation invalidates Anthropic's prompt cache, separately for the Sonnet family (current: Sonnet 5.5) and the Opus family; how Claude Code delivers effort on the wire; whether a model switch invalidates; and what explains a cache read that stopped at ~23.7k of a ~40.3k prefix right after /effort on Sonnet 5.5.

## Answer

On Sonnet 5.5 and Opus 5.5, a mid-conversation effort change keeps the cache when it is sent as a per-message `role: "system"` message carrying `output_config` ([effort](https://platform.claude.com/docs/en/build-with-claude/effort)). Claude Code says it keeps the cache on these models with an API key or a Claude subscription ([CC prompt-caching](https://code.claude.com/docs/en/prompt-caching)). A change to the top-level `output_config.effort` "always invalidates message blocks", and on some models it also invalidates the tool and system caches ([API prompt-caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)). On most other models, Sonnet 5, Sonnet 4.6 and Opus ≤4.8 among them, changing effort restarts the cache. "Each model has its own cache", so a model switch always re-reads everything ([CC prompt-caching](https://code.claude.com/docs/en/prompt-caching)). Your 23.7k partial hit is not what a top-level effort change produces: that would diverge at the start of the messages, or earlier. A per-message change would diverge nowhere. The docs name one mechanism that matches a cut partway through the prefix on Sonnet 5.5. When the API drops a Sonnet 5.5 thinking block that isn't preserved, "the cached prefix changes from that block's position onward" ([API prompt-caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)). That mechanism is a candidate. It has not been confirmed for your session.

## Map

### 1. API: effort and the cache key
- The invalidation table has an **Effort setting** row, marked Model-specific for tools and system and ✘ for messages: "Changing the `output_config.effort` value always invalidates message blocks, with the same model-specific effect on tool and system caches as thinking parameters. Setting effort explicitly to the model's default is equivalent to omitting it and does not invalidate. On models that support per-message effort, an effort change carried in a `role: "system"` message inside `messages` leaves the cached prefix intact." ([prompt-caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching))
- Thinking parameters row: "The thinking configuration (mode, and `budget_tokens` in extended mode) is rendered into the prompt, so changing it always invalidates message blocks; tool and system caches are also invalidated on models that render the configuration ahead of them." (same page)
- The table has no row per level pair (low→medium and so on). Any change of value counts, except a change to the model's own default.
- Top-level change: "Because top-level effort shapes the rendered prompt, changing it between requests doesn't preserve cached prefixes from earlier turns." Per-message form: "Add a `role: "system"` message with empty `content` and the new level in `output_config.effort` … Everything before that message is unchanged, so the cached prefix still matches." It needs the beta header `mid-conversation-output-config-2026-07-01`. ([effort](https://platform.claude.com/docs/en/build-with-claude/effort))

### 2. Per model
- Per-message effort, which keeps the cache: "On Claude Fable 5.1, Claude Mythos 5.1, Claude Opus 5.5, Claude Opus 5, Claude Sonnet 5.5, and Claude Haiku 5.5, use a per-message effort change, which keeps the prompt cache. On other models, set a new top-level value on the next request, which starts the cache over." ([effort](https://platform.claude.com/docs/en/build-with-claude/effort)) On Bedrock, only Fable 5.1, Mythos 5.1 and Opus 5.5 have it.
- **Sonnet 5.5:** "With `between_tools`, effort can't change mid-conversation: a per-message `output_config.effort` that differs from the level in effect returns a 400 error." The default is `high`. (effort page)
- **Opus 5.5:** the default is `medium`. "Adaptive thinking is always on and can't be turned off." It supports per-message effort. (effort page)
- **Sonnet 5, Sonnet 4.6, Opus 4.5–4.8:** they support effort, but not per-message effort, so a change restarts the cache (effort page). On Opus 4.5, effort works alongside `budget_tokens` (effort page).
- **Which models render the configuration ahead of tools/system:** not documented. The thinking and thinking-troubleshooting pages say only "depending on where the model renders the configuration", and name no model (digger read of [thinking](https://platform.claude.com/docs/en/build-with-claude/thinking) and [thinking-troubleshooting](https://platform.claude.com/docs/en/build-with-claude/thinking-troubleshooting); unquoted by the lead).

### 3. Claude Code on the wire
- Documented behaviour: "On most models, changing the effort level mid-session means the next request reads the entire conversation history with no cache hits. While the cache is still warm, Claude Code asks you to confirm the change first. On Opus 5.5, Sonnet 5.5, Haiku 5.5, and Fable 5.1 with an API key or a Claude subscription, changing effort keeps the cache, and Claude Code applies the new level without asking. This doesn't apply on Amazon Bedrock, Google Cloud's Agent Platform, or a Claude apps gateway, or when you set `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS` or your organization has a HIPAA configuration." ([CC prompt-caching](https://code.claude.com/docs/en/prompt-caching))
- Not documented: the wire form itself. **Inferred**, not documented, that Claude Code uses the per-message beta for this. The evidence is that the keep-cache list matches the per-message model set, and that the opt-out is the "experimental betas" switch.
- A third-party capture of `claude -p --bare --model claude-opus-5-5 --effort xhigh` shows a top-level `"output_config": {"effort": "xhigh"}`, with no Claude Code version given ([opencodex #6730](https://github.com/lidge-jun/opencodex/issues/6730); unquoted, digger). That fits the initial level being top-level. No source shows effort sent as text in the system prompt or in a user-turn tag.
- History: "Before v2.1.260, changing effort on Fable 5.1 with an API key or a Claude subscription also invalidated the cache." (CC prompt-caching)
- **DISPUTED, by date:** Claude Code issue [#61984](https://github.com/anthropics/claude-code/issues/61984) (Opus 4.7, v2.1.150) quotes an old doc line: "Effort level: not part of the cache key or the prompt". [#63962](https://github.com/anthropics/claude-code/issues/63962) measured a full hit on Sonnet 4.6. [#92444](https://github.com/anthropics/claude-code/issues/92444) measured misses on Sonnet 5 and Opus 5 (all unquoted, digger). The current docs side with #92444.

### 4. Model switch
- "Each model has its own cache. Switching with `/model` means the next request reads the entire conversation history with no cache hits, even though the content is identical." The same holds for `opusplan` toggles, automatic model fallback, and a skill whose frontmatter names a `model` (CC prompt-caching). The API caching page has no model-switch sentence (digger, 0–100k and 150k–160k read).

### 5. The 23.7k / 40.3k pattern
- The prefix layers in Claude Code are, in order: the system prompt with tool definitions, then project context, then the conversation (CC prompt-caching). A top-level effort change would diverge at the start of the messages, or at the system or tool boundary on models that render the configuration earlier (API prompt-caching). Either point sits well before 23.7k only if the system plus project layers are under 23.7k tokens. That size is unknown. **If those layers come to about 23.7k, a top-level change fits your numbers exactly.** This competing explanation was not ruled out.
- A per-message change "leaves the cached prefix intact", so it predicts a full read, which you did not see.
- Other documented mechanisms that change content partway through the prefix:
  - Dropped thinking blocks. "When the API drops a … Claude Sonnet 5.5 … thinking block that isn't [preserved] … the cached prefix changes from that block's position onward on that request." (API prompt-caching)
  - Image batch removal. "the next request reprocesses the conversation from the earliest of those messages onward." (CC prompt-caching)
  - Compaction, which "invalidates the conversation layer". (CC prompt-caching)
- Deferred tools: with tool search, "Claude Code keeps the tool list from the conversation's first request", so an MCP change does not move the prefix (CC prompt-caching). That rules this out as a cause on supported setups.
- Lookback limit: "The lookback window is 20 blocks … If a growing conversation pushes your breakpoint 20 or more blocks past the last write, the lookback window misses it." (API prompt-caching). In your session the next request read the full 40.6k, so the earlier entries were reachable. This makes the lookback an unlikely primary cause. That is an inference.
- No documented mechanism ties `/effort` on Sonnet 5.5 to a cut partway through the conversation. The block that changed is **undetermined**.

## Coverage
- 1 API effort and the cache key: settled
- 2 Per-model differences: settled, except render position, which is not documented
- 3 Claude Code wire delivery: partial. The behaviour is documented, the wire form is inferred.
- 4 Model switch: settled
- 5 Mid-prefix divergence: partial. Candidate mechanisms are documented, the cause is not settled.
- Digging ended: stopped after round 1 by budget choice (tier-1, low effort). No stop condition fired.

## Verification
- I read the Claude Code prompt-caching page in full and quoted directly from it the facts in sections 3, 4 and 5. The API prompt-caching and effort pages were also read directly, and their quotes are the lead's own. No separate verification pass was run. Facts carried from the diggers are marked unquoted. NOT ON PAGE: none. UNCHECKED: none.

## Open rabbit holes
- Capture a Claude Code request body before and after `/effort` on Sonnet 5.5 (via a proxy) to see whether a `role:"system"` + `output_config` message appears and whether `output_config` moves (undug).
- Measure the size of the system plus project-context layers in that session: if it is about 23.7k, the request ran a top-level change rather than per-message, perhaps because of a provider, an experimental-betas opt-out, or `between_tools` (undug).
- Find which models render thinking and effort ahead of tools and system (dug, unsettled).
- Read the 2.1.260 Claude Code changelog entry, and the 5.5 keep-cache entry (undug).
- Resolve Opus 5 in Claude Code: the API lists it for per-message effort, but the Claude Code keep-cache list omits it (dug, unsettled).
- Read API prompt-caching chars 100k–150k (dug, unread).
