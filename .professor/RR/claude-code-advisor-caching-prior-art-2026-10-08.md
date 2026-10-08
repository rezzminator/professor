# RR — Has anyone made Claude Code's advisor tool cache the advisor's read of the conversation, and how?

Question: has anyone already made the Claude Code advisor tool (or the Anthropic API advisor tool, type `advisor_20260301`, beta `advisor-tool-2026-03-01`) cache the advisor's read of the conversation — and how did they do it?

As of 2026-10-08, no one has been found making Claude Code's advisor cache its read. The one public request is an open feature issue with no replies. It says Claude Code "neither sets it nor exposes a way for users to set it", and its only workaround is dropping the advisor or running your own Messages API loop ([#91110](https://github.com/anthropics/claude-code/issues/91110)). The nearest gateway code is LiteLLM's advisor support, which only copies a caller-supplied `caching` field and never adds one ([LiteLLM PR #25525](https://github.com/BerriAI/litellm/pull/25525/files)). A subscription login can travel through an `ANTHROPIC_BASE_URL` gateway if the gateway forwards the OAuth `anthropic-beta` value ([llm-gateway docs](https://code.claude.com/docs/en/llm-gateway)). But the gateway guide warns that a gateway rewriting request bodies, including `tools` or `system`, can break requests ([gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol)). Separately, Claude Code v2.1.292 added prompt caching to `$.model.complete` for mods ([releases](https://github.com/anthropics/claude-code/releases)). That is a backed primitive for a cached side-chat advisor that leaves the server tool alone, though no one is shown using it for this.

## Map

### 1. Gateways, proxies, patches and forks that add `caching` or `max_tokens` to Claude Code's advisor

- **LiteLLM** passes `caching` through but never injects it. PR #25525, "feat(anthropic): support advisor_20260301 tool type", merged Apr 10, 2026, builds the advisor tool from `type`, `name` and `model` and then copies optional keys only when the caller sent them: `if _tool_dict.get("caching") is not None:` / `_advisor_tool["caching"] = _tool_dict["caching"]` ([PR #25525 diff](https://github.com/BerriAI/litellm/pull/25525/files)). The LiteLLM advisor docs show `caching` only as a field the caller sets (unquoted) ([LiteLLM advisor docs](https://docs.litellm.ai/docs/completion/anthropic_advisor_tool)).
- **OpenRouter**'s `ToolAdvisor20260301` type has both a `caching` field (example `{"type": "ephemeral"}`) and a `cacheControl` field ([OpenRouter ToolAdvisor20260301](https://openrouter.ai/docs/client-sdks/typescript/models/tooladvisor20260301.md)). The page does not describe `caching`. Whether OpenRouter forwards it to Anthropic, or whether anyone sets it for Claude Code traffic, is unverified.
- **Portkey** has webhook guardrails that can "Modify the user's request before it reaches the LLM provider" (unquoted). By default they run only on `/v1/chat/completions`-style requests, and the page says nothing about tools ([Portkey BYO guardrails](https://portkey.ai/docs/product/guardrails/list-of-guardrail-checks/bring-your-own-guardrails)). No one is shown using it on the advisor.
- **Nothing found** for claude-code-router, Helicone, Cloudflare AI Gateway, Vercel AI Gateway, Kong, claudish ([README](https://github.com/MadAppGang/claudish), no advisor mention), claude-code-cache-fix ([README](https://github.com/cnighswonger/claude-code-cache-fix), no advisor mention), tweakcc or other cli.js patches, or mitmproxy addons. These are search-level negatives, because web search does not index GitHub code.
- **SDK pattern.** The Vercel AI SDK request #14285 describes "Prompt caching for the advisor's transcript" with `{"type":"ephemeral","ttl":"5m"|"1h"}`, default null (unquoted) ([vercel/ai #14285](https://github.com/vercel/ai/issues/14285)). The linked PR #15171, merged May 12, 2026, never mentions caching (unquoted) ([vercel/ai #15171](https://github.com/vercel/ai/pull/15171)).
- **Why a proxy rewrite is risky.** The gateway guide says a gateway that "rewrites or redacts request bodies ... breaks the pairing the same way stripping does". It also says a gateway that rewrites `system`, `tools`, or earlier `messages` content can trigger the preserved-thinking rejection `bound to a different conversation` ([gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol)). Adding `caching` to the advisor entry in `tools` is such a rewrite. Whether keeping the rewrite constant from the first request avoids the rejection is untested.

### 2. Subscription (Pro/Max OAuth) login through `ANTHROPIC_BASE_URL`

- **Officially supported, if the gateway forwards the OAuth beta value.** The docs say: "Setting only that variable, without a gateway credential, doesn't replace the subscription. Requests still route through the gateway, but a saved claude.ai login remains the active credential, so its usage limits and billing apply. Gateways that pass this traffic on to Anthropic must forward the OAuth capability in `anthropic-beta`" ([llm-gateway docs](https://code.claude.com/docs/en/llm-gateway)). Stripping that value "fails those requests with `401`" ([gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol)). Neither page carries a date.
- **What else breaks behind a custom base URL** ([gateway compatibility guide](https://code.claude.com/docs/en/llm-gateway-protocol)):
  - **Advisor rejection.** When the gateway rejects the advisor entry, "Claude Code retries the request once without that entry and its `anthropic-beta` value. Later requests to that base URL leave the advisor out until Claude Code exits ... Before v2.1.280, Claude Code didn't retry this rejection".
  - **Fast mode.** Its availability check "calls `api.anthropic.com` directly rather than following `ANTHROPIC_BASE_URL`".
  - **Fine-grained tool streaming** is "off by default whenever requests route through a custom base URL".
  - **Gateway hint headers** are off by default for a custom base URL.
  - **`CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS`** leaves "The OAuth `anthropic-beta` value that subscription authentication requires" in place.
- **Dated regression.** In issue #70563, opened Jun 24, 2026 and closed as a duplicate, the reporter writes: "When using Claude Code with a custom `ANTHROPIC_BASE_URL` (API proxy/gateway), all requests fail with a 400 error because Claude Code sends an `anthropic-beta` header containing `advisor-tool-2026-03-01`". Setting `"disableExperimentalBetas": true` did not help. The reporter says it broke between 2.1.181 and 2.1.187 ([#70563](https://github.com/anthropics/claude-code/issues/70563)).
- **LiteLLM Max tutorial.** LiteLLM's `forward_client_headers_to_llm_api: true` "forwards the user's OAuth token (in the `Authorization` header) through LiteLLM to the Anthropic API" ([LiteLLM Claude Max tutorial](https://docs.litellm.ai/docs/tutorials/claude_code_max_subscription), undated).
- **Not a gateway issue.** In #28089, OAuth tokens from `claude setup-token` (`sk-ant-oat01-*`) used directly by third-party integrations were rejected "As of ~Feb 20 2026" with "OAuth authentication is currently not supported." It was closed as not planned and labelled invalid ([#28089](https://github.com/anthropics/claude-code/issues/28089)).
- **No 429s found.** No source reports blanket 429s for a subscription login through a gateway. The 429 you saw from a plugin's own fetch is not documented anywhere this research reached.

### 3. Cached second-opinion or side-chat advisors built by other means

- **Nothing found.** The Zen/PAL MCP server does not mention Anthropic prompt caching or `cache_control`. It "works around MCP's 25K limit for large prompts and responses" ([zen-mcp-server](https://github.com/BeehiveInnovations/zen-mcp-server)).
- No plugin, hook or MCP server was found that re-reads the transcript with caching, and no measured cost or savings were found.
- The third-party write-ups test neither caching nor cost ([azukiazusa](https://azukiazusa.dev/en/blog/claude-advisor-tool), [makandracards](https://makandracards.com/makandra/626779-claude-code-advisor-tool), both unquoted on this point).
- A vendor post repeats "The advisor's own read of the conversation is not cached" and gives only illustrative figures (unquoted) ([OrcaRouter](https://www.orcarouter.ai/blog/claude-opus-5-5-advisor-tool-playbook)).
- **A cached primitive for building one.** Claude Code v2.1.292 "Added prompt caching to `$.model.complete` for mods: `prompt` and `system` take blocks of text, and `cache: true` on a block caches the request up to it" ([releases](https://github.com/anthropics/claude-code/releases)). A mod-built side-chat advisor could use this without touching the `advisor_20260301` server tool. This research found no one who has built one with it.

### 4. Official statements and requests

- **The one request.** Issue #91110, "[FEATURE] Expose the advisor tool's `caching` parameter in Claude Code", was opened Sep 1, 2026 and is Open, labelled `area:cost` and `enhancement`, with "No activity" and no staff reply ([#91110](https://github.com/anthropics/claude-code/issues/91110)).
  - It says: "The underlying API server tool (`advisor_20260301`) already supports advisor-side prompt caching via a `caching` parameter, but Claude Code neither sets it nor exposes a way for users to set it."
  - It proposes `"advisorCaching": { "type": "ephemeral", "ttl": "1h" }`, "defaulting to off so current behavior is preserved".
  - On workarounds: "The workaround today is to either drop the advisor on exactly the sessions that benefit most, or run my own agent loop against the Messages API where `caching` is available."
- **Changelog.** The two advisor entries found are v2.1.290 (`serverToolUses` on a mod's `turn.step` hook, "the tool calls the API ran itself (the advisor)") and v2.1.287 ("Fixed `/advisor` pairing checks"). No line mentions advisor caching or advisor `max_tokens` ([releases](https://github.com/anthropics/claude-code/releases); the page was truncated before v2.1.284).
- **API release notes.** April 9, 2026: "We've launched the advisor tool in public beta." June 2, 2026: "The advisor tool now supports a `max_tokens` parameter to cap the advisor model's output per call ... Set `tools[].max_tokens` on the advisor tool definition." There is no entry announcing advisor `caching` ([API release notes](https://platform.claude.com/docs/en/release-notes/api)).
- **Code with Claude talk.** "Caching, harnesses, and advisors: Building on Claude at GitHub scale" (May 6, 2026) mentions caching and the "Advisor strategy" in its blurb only, with no mechanism (unquoted) ([session page](https://claude.com/code-with-claude/session/sf-caching-harnesses-and-advisors-building-on-claude-at-github-scale)).
- **No official statement found** that advisor caching is planned, shipped, or declined for Claude Code.

## Coverage

- 1. Gateways, proxies, patches and forks: settled. LiteLLM's code passes only a caller-supplied `caching`. Search-level negatives for the rest.
- 2. Subscription OAuth through `ANTHROPIC_BASE_URL`: settled.
- 3. Alternative cached second-opinion advisors: settled. Nothing found.
- 4. Official statements and requests: settled.

Digging ended: every sub-area settled (after round 2).

## Verification

37 numbered statements were sent to 12 source pages, and 33 of them were checked against quoted text.
- 26 were confirmed as stated.
- 6 were confirmed absences, where the page lacks something we expected it might have: no staff reply on #91110, no advisor `caching` entry in the API release notes, no advisor mention in the LiteLLM Max tutorial, no advisor caching or `max_tokens` line in the releases, no caching in zen-mcp-server, and no advisor mention on the llm-gateway page.
- 1 was NOT ON PAGE: "#28089 involves `ANTHROPIC_BASE_URL` or a gateway". It was removed.

UNCHECKED (4) — the fetch saved the platform advisor-tool page to a file this run could not read:
- `caching` on the tool definition caches the advisor's own transcript.
- The break-even at roughly three calls.
- The `clear_thinking` `keep: "all"` warning about advisor cache stability.
- `max_tokens` on the tool definition. This one is confirmed through the API release notes instead.

## Open rabbit holes

- Does `$.model.complete` with `cache: true` (v2.1.292) use the session's subscription credential without the 429 a plugin's own fetch gets? (undug)
- Does OpenRouter forward the advisor `caching` field to Anthropic? (dug, unsettled)
- Does a proxy that adds `caching` to the advisor tool entry from the first request trigger `bound to a different conversation` or other 400s in Claude Code? Untested. (undug)
- Do Claude Code's own `clear_thinking` / context-management settings break advisor cache stability, which would defeat `caching` even if it were injected? (undug)
- Is there a GitHub code-search hit for mitmproxy addons or cli.js patches containing `advisor_20260301` with `caching`? Web search can't reach this. (dug, unsettled)
- The Code with Claude GitHub talk transcript: did GitHub Copilot enable advisor caching? (dug, unsettled)
- The OAuth-plugin 429: no public source found. (dug, unsettled)
- Two leads seen only in search snippets: a "$168 vs $21" advisor session comparison, and a pydantic-ai advisor `caching` option. (undug)
