# GPT sub-agents behind a model-fork router

**Status:** Implemented as the standalone `plugins/gpt-gateway` plugin, with its ChatGPT translation in `gpt_gateway/chatgpt/`; the build plan below records the original design rather than the current file layout. Wire facts were measured on 2026-09-22 (Claude Code 2.1.280, codex-cli 0.155.1) against a loopback fake Anthropic server (§ Reproducing the probes). The translator design (2026-10-09) rests on two source maps, both cited in § Prior art. Current setup and behavior are documented in the plugin's README and `docs/design/gpt-gateway-layout.md`.

## Contents

- [The finding](#the-finding)
- [Goal and boundary](#goal-and-boundary)
- [Wire facts](#wire-facts)
- [Prior art](#prior-art)
- [Design](#design)
- [Translator](#translator)
  - [Upstream and identity](#upstream-and-identity)
  - [Request mapping](#request-mapping)
  - [Caching](#caching)
  - [Reasoning](#reasoning)
  - [Tools](#tools)
  - [Models and lanes](#models-and-lanes)
  - [Streaming](#streaming)
  - [Errors and retries](#errors-and-retries)
  - [Authentication](#authentication)
  - [count_tokens](#count_tokens)
  - [Deviations from Codex](#deviations-from-codex)
- [Distillation sources](#distillation-sources)
- [Decisions for the user](#decisions-for-the-user)
- [Costs and risks](#costs-and-risks)
- [Alternatives](#alternatives)
- [Open questions](#open-questions)
- [Build plan](#build-plan)
- [Reproducing the probes](#reproducing-the-probes)

## The finding

A sub-agent's model travels in its **definition**, and Claude Code puts whatever ID the definition names on the wire, unvalidated: `model: gpt-5.6-sol` leaves as `"model": "gpt-5.6-sol"` while the main thread's requests leave as `claude-opus-5-5`. A router in front of Claude Code can therefore fork per request on the `model` field — `claude-*` to Anthropic untouched, every GPT catalog ID to a translator. The four-name limit (`sonnet|opus|haiku|fable`) binds only the `Agent` tool's per-spawn `model` override, which is enum-validated and overrides the definition, so a GPT agent is spawned by **name**, with no `model`. claude-code-proxy alone cannot do the fork: it has no Anthropic upstream and maps every Claude ID to a GPT tier. The implemented fork lives in `plugins/gpt-gateway`, with the translator split across `gpt_gateway/chatgpt/` (the original design is in § Translator).

## Goal and boundary

Goal: the main thread stays on Claude under the user's subscription; chosen sub-agents run on GPT through a ChatGPT subscription, spawned with the ordinary `Agent` tool, their return landing as a normal tool result.

Not in scope: a whole-session GPT harness (claude-code-proxy's own use case), GPT through an OpenAI API key, any change to the four Claude aliases.

## Wire facts

Each row is one headless probe (`claude -p`, Claude Code 2.1.280). "Main" and "Sub" are the `model` field of the main thread's and the sub-agent's `POST /v1/messages`.

| # | Setup | Main | Sub |
| --- | --- | --- | --- |
| W1 | `--agents` JSON, `model: gpt-5.5` | `claude-opus-5-5` | `gpt-5.5` |
| W2 | agent `model: haiku`, `ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-5.5` | `claude-opus-5-5` | `gpt-5.5` |
| W3 | agent without `model`, `CLAUDE_CODE_SUBAGENT_MODEL=gpt-5.5` | `claude-opus-5-5` | `gpt-5.5` |
| W4 | subscription login, no API key, custom `ANTHROPIC_BASE_URL` | OAuth bearer + `oauth-2025-04-20` beta | same bearer |
| W5 | file `.claude/agents/{name}.md`, `model: gpt-5.6-sol` | `claude-opus-5-5` | `gpt-5.6-sol` |
| W6 | spawn `model: "claude-opus-4-1"` | `claude-opus-5-5` | refused: `InputValidationError` |
| W7 | spawn `model: "gpt-5.6-sol"` | `claude-opus-5-5` | refused: `InputValidationError` |
| W8 | `ANTHROPIC_DEFAULT_OPUS_MODEL=gpt-5.6-sol`, main on the default model | `gpt-5.6-sol` | `gpt-5.6-sol` |
| W9 | file `model: gpt-5.6-sol`, spawn `model: "sonnet"` | `claude-opus-5-5` | `claude-sonnet-5` |
| W10 | file `model: gpt-5.6-sol`, `--agents` same name with `model: gpt-5.6-terra` | `claude-opus-5-5` | `gpt-5.6-terra` |

What the rows establish:

- **Definitions take any ID** (W1, W5). Claude Code prints `[claude-code:unrecognized_model] {"model":"…","query_source":"agent:custom:{name}"}` and sends the ID anyway.
- **The spawn override is a four-name enum** (W6, W7): the `Agent` tool's `model` accepts `sonnet|opus|haiku|fable` only, so an old Claude ID used as a routing decoy is refused exactly like a GPT ID. Its schema text says it "Takes precedence over the agent definition's model frontmatter and the configured default subagent model"; W9 confirms it.
- **The launch line beats the file** (W10): an `--agents` definition overrides a same-named `.claude/agents/*.md`.
- **Alias remaps are session-wide** (W2, W8): a remapped alias moves every use of it — background calls, collectors, and for `opus` the main thread itself, because the default model resolves through that alias.
- **Aliases resolve before sending**: the main thread leaves as `claude-opus-5-5`, never `opus`, so a `claude-` prefix match identifies every Claude request.
- **The subscription token reaches a custom base URL** (W4), on sub-agent requests too, so the router strips it before a request reaches a third-party process.
- Sub-agent requests carry `thinking` and Anthropic betas (`context-management-2025-06-27`, `advisor-tool-2026-03-01`, …); the GPT translator drops or maps them.
- A custom base URL makes the session ineligible for no-charge auto-mode classifier requests; Claude Code prints that notice naming `127.0.0.1:{port}`.
- `--bare` strips the `Agent` tool (`--tools Agent,Read` leaves only `Read`; `--tools default` gives `Bash`, `Edit`, `Read`), so these probes cannot run bare.

## Prior art

Two source maps back this section and § Translator, each code fact quoted and pinned to a commit:

- `.professor/RR/codex-chatgpt-backend-wire-protocol-2026-10-09.md`: the official Codex CLI (`openai/codex`, `codex-rs/`, commit `9a59289`) against the ChatGPT backend.
- `.professor/RR/claude-code-to-codex-translators-2026-10-09.md`: claude-code-proxy (`raine/claude-code-proxy`, commit `4b5c058a`), compared with CLIProxyAPI, LiteLLM, opencode-openai-codex-auth, musistudio/llms and ccproxy-api.

What they settle:

- **Nothing local runs a model.** Every bridge is a protocol translator: it holds a ChatGPT login, rewrites Anthropic Messages into the Responses API, posts to `https://chatgpt.com/backend-api/codex/responses`, and rewrites the Responses stream back into Anthropic SSE.
- **claude-code-proxy is the most complete bridge**, with three gaps. It has no Anthropic upstream, so it cannot fork. It keys the prompt cache by session alone, which puts every sub-agent of a session in one cache lane (its open issue #163). And it strips only `pattern` from tool schemas.
- **Three independent bridges carry GPT's encrypted reasoning inside the Anthropic thinking `signature`**: raine (`ccp:codex:v1:`), CLIProxyAPI, LiteLLM (`litellm_encrypted_reasoning:`). This is the one channel Claude Code round-trips untouched.
- **musistudio/llms, the transformer behind claude-code-router, has no ChatGPT-subscription path.** It never sends `prompt_cache_key`, `store` or `include`, so reasoning cannot round-trip through it.

## Design

### Topology

```text
claude  (ANTHROPIC_BASE_URL=http://127.0.0.1:18787/{per-install-secret})
  └─ gpt-gateway                              one process, loopback, fail-open supervisor
       ├─ model starts with "claude-" → https://api.anthropic.com          byte-for-byte passthrough
       └─ model in the GPT catalog    → in-process translator → https://chatgpt.com/backend-api/codex/responses
```

The router is the standalone `plugins/gpt-gateway`, not a second process. Its implemented contract includes:

- loopback bind
- chunk-flushed SSE relay
- subscription OAuth passthrough (W4, in daily use)
- the `x-claude-code-agent-id` main/sub discriminator
- graceful restart
- a supervisor that removes `ANTHROPIC_BASE_URL` whenever the gateway stops answering

This settles former open question 2 (where the router lives).

### Router contract

- `POST /v1/messages` and `POST /v1/messages/count_tokens`: parse `model` once.
  - **`claude-*`**: the current path, unchanged.
  - **A GPT catalog ID**: the translator. The Claude `authorization` and `x-api-key` never leave the process on this branch.
  - **Any other ID**: an Anthropic-shaped 400 that names the catalog.
- **A GPT model on a request without `x-claude-code-agent-id`** gets a 400 `invalid_request_error`, "GPT models run as sub-agents only". This catches the W8 misconfiguration, which would otherwise move the main thread silently.
- **Every other path** goes to Anthropic.
- **Errors name their upstream**: `gateway: anthropic upstream …` or `gateway: chatgpt upstream …`.
- **Capture** (`tmp/gw-logs/`) records the GPT branch too. Its folder name carries `gpt` instead of `main` or `sub`. `3-request.sent.body` holds the translated Responses body, so every translation can be audited after the fact.

### GPT agents live on the launch line

A GPT agent exists only in a session started through the router. pfm's launch line sets `ANTHROPIC_BASE_URL` and passes `--agents` with the GPT definitions (W10). Each definition is compiled at launch from the Claude file it twins: the prompt body comes from the file and only `model` is swapped, so no hand-kept copy can drift.

- **General workers:** `sol`, `luna`, `astra`, pinned to the catalog's current Sol, Luna and Astra IDs.
- **Fleet roles:** a same-named GPT twin (for example `flights-mechanical-executor`) replaces the file definition, in that session only.
- **Not an ungated file:** a GPT-pinned `.claude/agents/*.md` loads in every session. In a session without the router, its request reaches Anthropic as `gpt-*` and fails. The shipped twins are therefore machine-source `{name}.md` declarations with `from:` that require the `gpt-agents` capability, installed only on a host that sets `claude.gptAgents` ([GPT twins](../design/gpt-twins/gpt-twins.md)).
- **Not an alias remap:** see W2 and W8.

### Spawning a GPT agent

`Agent(subagent_type: "sol", description: …, prompt: …)`, with no `model`. A spawn-time `model` silently overrides the GPT definition back to Claude (W9).

- **The foreman needs no change.** `flights-foreman` already spawns its children and `flights-mechanical-executor` with no model override (`templates/global/agents/flights-foreman.md`).
- **The fleet prompt needs an exception.** It names a tier alias at each spawn site (`pfm/harness-prompts/claude/professor.md`, § Claude Code: "The tiers, by model alias, named inline at each spawn site"). A GPT-pinned agent must be spawned with no `model`.

## Translator

The implementation is a Python stdlib package under `plugins/gpt-gateway/gpt_gateway/chatgpt/` (routing in `route.py`, translation in `request.py` and `stream.py`, authentication in `auth.py` and `codex_login.py`). The rule for every choice below: **send what Codex sends.** Each deviation is listed in § Deviations from Codex with its reason. "Codex" means `openai/codex` at `9a59289`, and "raine" means claude-code-proxy at `4b5c058a`.

### Upstream and identity

| | Value | Source |
| --- | --- | --- |
| Endpoint | `POST https://chatgpt.com/backend-api/codex/responses`, `Accept: text/event-stream` | Codex `model-provider-info/src/lib.rs:80`, `codex-api/src/endpoint/responses.rs:123-158` |
| Transport | HTTP SSE. Codex prefers a WebSocket; see § Deviations. | Codex `core/src/client.rs:1084-1095` |
| Auth | `Authorization: Bearer {access_token}`, `ChatGPT-Account-ID: {account_id}` | Codex `bearer_auth_provider.rs:31-47` |
| Cache affinity | `session-id: {lane key}` beside `prompt_cache_key` (the two carry the same value) | Codex `core/src/client.rs:652-660`: "ChatGPT derives cache affinity from the Responses session-id header." |
| Request id | `x-client-request-id: {lane key}` | Codex `codex-api/src/requests/headers.rs:5-31` |
| Sub-agent mark | `x-openai-subagent` | same |
| Client identity | `originator`, `User-Agent`, `version`: **decision D1** | Codex `login/src/auth/default_client.rs:152-179` |

The installed codex-cli 0.155.1 binary carries `session-id`, `ChatGPT-Account-ID`, `prompt_cache_key`, `reasoning.encrypted_content` and `x-openai-internal-codex-responses-lite`, so the HEAD map matches the release on those names.

### Request mapping

The body fields go in Codex's serialization order (`codex-api/src/common.rs:278-304`): `model, stream, service_tier?, input, tools, tool_choice, parallel_tool_calls, reasoning, store, include, prompt_cache_key, text`.

| Anthropic request | Responses request |
| --- | --- |
| `model` | catalog slug, with a `[1m]` suffix stripped. A `-fast` suffix sets `service_tier: "priority"` (raine `model_allowlist.rs:45-122`). |
| `stream` | always `true`. A non-streaming caller gets the accumulated stream as one JSON body (raine `accumulate.rs`). |
| `system` blocks | one leading `developer` message with one `input_text`, the text blocks joined by `\n\n`; an `x-anthropic-billing-header:` block dropped (raine `translate_shared.rs:36-58`). Codex sends no top-level `instructions` (`core/src/client.rs:988-998`). |
| `role: "system"` entries inside `messages` (Claude Code 2.1.293 sends them; seen in capture) | `developer` message, in place (raine `request.rs:919-935`) |
| user `text` | `message` role `user`, `input_text` |
| assistant `text` | `message` role `assistant`, `output_text` |
| assistant `thinking` with a `pfm:codex:v1:` signature | `reasoning {id, summary: [], encrypted_content}`, in position (§ Reasoning) |
| assistant `thinking` with any other signature | dropped: Anthropic reasoning cannot be replayed to GPT |
| assistant `tool_use` | `function_call {call_id: id, name, arguments: json(input)}`; ids pass through unchanged (raine `request.rs:954-966`) |
| user `tool_result` | `function_call_output {call_id, output}`. With text only, the output is a string; with images, an `input_text`/`input_image` array. `is_error` prepends `[tool execution error]` (raine `request.rs:877-908`). |
| `image` base64 | `input_image {image_url: "data:{media};base64,…"}`; media type checked against png, jpeg, gif and webp |
| `image` URL, `document`, other blocks | a text placeholder naming the block type. Codex turns remote images into placeholders (`utils/image/src/lib.rs:181-207`). |
| `cache_control` | dropped. OpenAI caches by prefix and key, not by breakpoint. |
| `metadata`, `context_management`, `anthropic-beta`, `max_tokens`, `temperature`, `top_p`, `stop_sequences` | dropped. Codex sends none of these (`core/src/client.rs:947-1071`), and raine pins `max_output_tokens` absent in a test (`request.rs:2021`). |
| `output_config.effort` | `reasoning.effort`, capped at the slug's highest supported level. `max` becomes `xhigh` where `max` is unsupported. |
| `thinking.budget_tokens`, when no `output_config.effort` is present | effort by threshold: ≥ 4096 high, ≥ 2048 medium, ≥ 1024 low, otherwise minimal (LiteLLM `transformation.py:459-496`) |
| `thinking` (any) | `reasoning.summary: "auto"` where the catalog says the slug supports summaries |
| `output_config.format` (json_schema) | `text.format`, strict |
| always | `store: false`, `include: ["reasoning.encrypted_content"]`, `text.verbosity`: the slug's catalog default |

Codex normalizes history before sending (`core/src/context_manager/normalize.rs:46-67`). The translator does the same two things:

- a `function_call` with no output gets a synthetic `function_call_output` of `"aborted"` placed right after it;
- an output with no call is dropped.

### Caching

This is the part bridges get wrong most often. OpenAI caches by **exact prefix** and routes by **`prompt_cache_key`** plus the `session-id` header.

- **Lane key.** `uuidv5(NAMESPACE_OID, "{x-claude-code-session-id}:{x-claude-code-agent-id}")`. Each sub-agent is its own conversation with its own prefix, so each needs its own lane. This is how Codex keys its internal sub-threads, `"{source}:{parent_thread_id}"` (`core/src/client.rs:638-660`), and how CLIProxyAPI keys Claude Code (`claude_code_session.go:102-128`). raine's session-only key mixes every sub-agent of a session into one lane (its issue #163).
- **Prefix determinism.** The same Anthropic prefix must translate to byte-identical Responses JSON on every request of a lane. The rules:
  - compact separators;
  - fixed field order;
  - tools in the order Claude Code sent them;
  - a pure schema sanitizer;
  - reasoning items rebuilt exactly from the signature;
  - no clock, random or counter value anywhere in `input`.

  The developer message comes first and the volatile tail (the newest user turn and its system-reminders) comes last, so the stable prefix is as long as Claude Code makes it.
- **The thinking hazard does not apply here.** The gateway measured that Claude Code sends earlier-turn thinking back emptied, keeping only its signature, which would break an Anthropic cache at turn boundaries. For GPT, the signature *is* the payload. The reasoning item rebuilt from it is byte-identical every turn, so the prefix holds across turns. A captured sub-agent request shows these blocks as `thinking: ""` with signatures of 892–1668 bytes.
- **Usage back to Claude Code.**
  - `cache_read_input_tokens` = `input_tokens_details.cached_tokens`
  - `cache_creation_input_tokens` = `cache_write_tokens` when present, else 0
  - `input_tokens` = `input_tokens` − `cached_tokens` − cache writes
  - `output_tokens` = `output_tokens`, which includes reasoning tokens

  Codex parses these fields at `codex-api/src/sse/responses.rs:117-156`, and raine maps them at `reducer.rs:1023-1052`. The gateway logs one `gpt` line per response with the lane's `cached/input` ratio, and `gateway.py chatgpt-stats` reports the hit rate per lane. No bridge publishes a measured hit rate, so this log is the acceptance evidence.

### Reasoning

- **Request:** `store: false` plus `include: ["reasoning.encrypted_content"]`, which is what Codex sends.
- **Response:**
  - Each `reasoning` output item becomes one Anthropic `thinking` block. Its summary text streams as `thinking_delta`, with `"\n\n"` at each `reasoning_summary_part.added`.
  - Its `signature` is `pfm:codex:v1:{base64url(item id)}:{encrypted_content}`, emitted as `signature_delta` when the item is done.
  - A reasoning item with no summary still yields a block, with `thinking: ""` and the signature, so nothing is lost (raine `live_stream.rs:990-1071`).
- **Replay:** the next request decodes the signature back into `{type: "reasoning", id, summary: [], encrypted_content}`. Reasoning items travel unfiltered, as Codex re-sends them (`context_manager/history.rs:960-979`).
- **Bad blob:** an `invalid_encrypted_content` error drops reasoning items from that lane's replay, retries once, and logs it (CLIProxyAPI clears its replay cache on the same error, `codex_executor_reasoning.go:760-826`).
- **Model switch inside a lane:** a signature minted for another slug is dropped, and a `developer` `<model_switch>` message is prepended to the new turn, as Codex does (`core/src/context/model_switch_instructions.rs:17-40`).

### Tools

- **Function tools.** `{type: "function", name, description, strict: false, parameters}`. Codex sends its own shell tool with `strict: false` (`core/src/tools/handlers/shell_spec.rs:24-114`).
- **Schema sanitizer.** It applies three groups of rules, all pure functions:
  - Codex's own rules (`tools/src/json_schema.rs:75-150`): a boolean schema becomes `{"type": "string"}`, `const` becomes a one-value `enum`, a missing `type` is inferred, and an uninferable schema becomes `{}`.
  - raine's rule: `pattern` is removed recursively. OpenAI's regex dialect rejects some of Claude Code's patterns, and its issue #142 names this session's own `Artifact` tool as the failure.
  - CLIProxyAPI's rule: `$schema` and `$id` are removed.
- **Names.** Names over 64 characters are shortened to a prefix plus a hash and mapped back on the response, as CLIProxyAPI does. Today's longest name, `mcp__claude_ai_Claude_Docs__batch`, is 33 characters.
- **Server tools.**
  - The advisor is stripped by the existing `strip_advisor`, because a GPT slug never matches `advisor_models`.
  - `web_search_20250305` becomes the hosted `{type: "web_search", external_web_access: true}`, with `allowed_domains` and `blocked_domains` as filters (raine `request.rs:652-680`).
  - Any other server tool is dropped and logged by type.
- **`tool_choice`.**
  - `auto` → `auto`.
  - `any` → `required`.
  - `none` → `none`.
  - `{type: tool, name}` → `{type: function, name}`.
  - A forced web search becomes `allowed_tools` when a web_search tool is registered, else `auto`, because raine reports the backend answers 502 otherwise.
- **`parallel_tool_calls`.** It is `!disable_parallel_tool_use`, which defaults to true. It is forced to false on Responses-Lite slugs, as Codex does (`core/src/client.rs:1058`).
- **Tool arguments.** Codex takes them from `output_item.done` and ignores the argument deltas (`codex-api/src/sse/responses.rs:344-505`). The translator does the same: it emits each `tool_use` block whole at `output_item.done`, so Claude Code never sees half-parsed JSON. raine needed Read-only JSON repair because it streamed deltas.

### Models and lanes

- **Catalog.** At start, and then every 300 seconds, the gateway fetches `GET https://chatgpt.com/backend-api/codex/models?client_version={v}`, the way Codex does (`codex-api/src/endpoint/models.rs:33-45`, cache TTL `models-manager/src/manager.rs:34-35`).
  - Per slug it reads: context window, supported reasoning levels, default verbosity, whether summaries are supported, `use_responses_lite`, and service tiers.
  - The router's "is this a GPT model?" test is membership in this catalog.
  - If the catalog is unreachable, the gateway uses the last good copy on disk. With no copy at all, GPT requests get a 503 naming the cause, and Claude requests are unaffected.
- **Responses-Lite slugs.** These are the Sol, Terra, Luna and Astra families in raine's allow-list (`model_allowlist.rs:124-147`). They differ in four ways:
  - the header `x-openai-internal-codex-responses-lite: true`;
  - the tools move into an `additional_tools` item with role `developer`, ahead of the developer message (raine `request.rs:512-549`);
  - `parallel_tool_calls: false`;
  - `reasoning.context: "all_turns"`.

  These are exactly the models this design targets.
- **Context window.** For a slug unknown to Claude Code, Claude Code's own compaction threshold is unmeasured (open question 8). The catalog's window (272,000 for gpt-5.5) is the ceiling Claude Code must compact under.

### Streaming

| Responses event | Anthropic event |
| --- | --- |
| first byte of a 200 | `message_start {usage: {input_tokens: estimate, output_tokens: 0}}` |
| `response.created`, `response.in_progress`, `codex.rate_limits`, keepalive | `ping` |
| `output_item.added` `message` | `content_block_start {type: text}` |
| `output_text.delta` | `content_block_delta text_delta` |
| `output_item.added` `reasoning` / `reasoning_summary_text.delta` / `reasoning_summary_part.added` | `thinking` block start / `thinking_delta` / `thinking_delta "\n\n"` |
| `output_item.done` `reasoning` | `signature_delta`, `content_block_stop` |
| `output_item.done` `function_call` | `content_block_start {type: tool_use, id: call_id, name, input: {}}`, one `input_json_delta` with the whole arguments, `content_block_stop` |
| `output_item.done` `message` | `content_block_stop` |
| `response.completed` | `message_delta {stop_reason, usage}`, `message_stop` |
| `response.incomplete` reason `max_output_tokens` | `message_delta {stop_reason: "max_tokens"}`, `message_stop` |
| `response.incomplete` any other reason, `response.failed`, `error` | § Errors and retries |

`stop_reason` is `tool_use` when any `function_call` was emitted, and `end_turn` otherwise. A heartbeat `ping` goes out every 15 seconds of upstream silence. Codex's idle timeout is 300 seconds (`model-provider-info/src/lib.rs:66-68`), and so is the translator's.

### Errors and retries

| Upstream | Before any output | After output began |
| --- | --- | --- |
| transport error, 5xx, unclassified `response.failed` | retry up to 4 times, backoff `200 ms × 2^(n−1)` × jitter 0.9–1.1 (Codex `codex-client/src/retry.rs:15-52`) | close open blocks, then `event: error {type: api_error}` |
| 401 | refresh the token once, retry | same as above |
| 429 `usage_limit_reached` | 429 `rate_limit_error`, `retry-after` from `resets_at`, `x-should-retry: false`, `anthropic-ratelimit-unified-status: rejected` (raine `mod.rs:1439-1572`) | same, as `event: error` |
| `context_length_exceeded`, or a message naming the context window | 413 `request_too_large`, which starts Claude Code's compaction (raine changelog v0.1.14) | same |
| `server_is_overloaded`, `slow_down` | 529 `overloaded_error` | same |
| `usage_not_included` | 403 `permission_error` | same |
| empty completion after retries | 503 `api_error` with `x-should-retry: false`, because Claude Code otherwise repeats a 503 up to ten times (raine #174) | n/a |
| stream ends with no terminal event | retry when nothing was emitted; otherwise `event: error` "upstream stream closed before the terminal event" | |

Every error line in the gateway log carries the lane key, the slug and the upstream body head. An error never reaches Claude Code as an empty stream.

### Authentication

- **Login source (implemented).** `gpt_gateway/chatgpt/codex_login.py` re-reads Codex's `auth.json` before refreshing and writes rotated credentials back in Codex's format; its own store is used only when Codex has no ChatGPT login. See the plugin README for the active credential precedence.
- **Own login fallback.** `gateway.py chatgpt-login` runs the browser PKCE flow every bridge uses:
  - `https://auth.openai.com/oauth/authorize` with `client_id=app_EMoamEEZ73f0CkXaXp7hrann`, scope `openid profile email offline_access` and redirect `http://localhost:1455/auth/callback`;
  - the state is checked on the callback;
  - the code is exchanged at `/oauth/token`.

  A device-code flow (`/api/accounts/deviceauth/usercode`) covers headless use (raine `pkce.rs:48-98`, `device.rs`). The client id is OpenAI's public Codex value and is identical in all five bridges.
- **Storage (implemented).** `gpt_gateway/chatgpt/codex_login.py` uses Codex's login in place when available; otherwise `gpt_gateway/chatgpt/auth.py` uses a `0600` file under `~/.local/state/gpt-gateway/` (or Keychain when explicitly selected).
- **Refresh.** It runs when the access token's JWT `exp` is within 5 minutes, or after a 401. Refreshes are single-flight behind a lock, and the store is re-read before each one. Codex's request body is used: JSON `{client_id, grant_type: "refresh_token", refresh_token}` (`login/src/oauth/client.rs:76-126`).
- **Account id.** `tokens.account_id`, falling back to the id_token claim `chatgpt_account_id` (`login/src/token_data.rs:88-117`).

### count_tokens

There is no upstream equivalent, and stdlib Python has no o200k tokenizer. The estimate is calibrated per lane:

- the translated request's character count × that lane's last measured ratio (`input_tokens` ÷ request characters from the previous `response.completed`);
- characters ÷ 4 before the first response;
- 2000 tokens per image, as raine counts them.

The same estimate fills `message_start.usage.input_tokens`. The real figure arrives on `message_delta`.

### Deviations from Codex

| Codex does | The translator does | Why |
| --- | --- | --- |
| WebSocket with `previous_response_id`, sending only new items | HTTP SSE, full input every request | stdlib has no WebSocket client. The cache comes from the prefix and the key, not the socket. raine's WebSocket path currently fails with 403 for some users (issue #172). Revisit only if the measured hit rate is poor. |
| Its own base instructions from models.json as the first developer message | Claude Code's system prompt as the first developer message | The agent's identity is the Claude Code system prompt plus its agent definition. Codex's prompt describes Codex's tools, not Claude Code's. |
| Remote compaction (`compaction_trigger` item) | Claude Code's own compaction, translated like any request | Claude Code owns the conversation, so it owns compaction. |
| `apply_patch` as a freeform grammar tool, `exec_command` shell | Claude Code's `Edit`, `Write` and `Bash` as JSON function tools | Claude Code runs the tools. GPT's native tool habits are the harness-fit risk, open question 1. |

## Distillation sources

Every piece is distilled, not vendored: rewritten as stdlib Python from the named source. Each distilled file carries its source's licence attribution. The evidence is in `.professor/RR/distill-sources-codex-translator-2026-10-09.md`, plus a code trace of ChatMock (`RayBytes/ChatMock` @ `5bd6e77`) and codex-lb (`Soju06/codex-lb` @ `f8ffbac`), both MIT and both Python. No candidate can be taken whole: none combines an Anthropic ingress, a ChatGPT login and per-agent cache lanes.

| Piece | Primary source | Adds | Skipped, and why |
| --- | --- | --- | --- |
| Login, token store, refresh | opencode `packages/opencode/src/plugin/openai/codex.ts` (575 lines; browser + device + single-flight refresh) | openai/codex `login/`: 15-minute device limit, port 1455→1457 fallback. codex-lb `balancer/logic.py:19`: `refresh_token_reused` is a permanent failure needing re-login. ChatMock `utils.py:328,333`: refresh when within 5 minutes of `exp`, or when the last refresh is 55 minutes old. | ChatMock needs `requests` and has no refresh lock; codex-lb's refresh is aiohttp plus a database |
| Request mapping | claude-code-proxy `request.rs`, `translate_shared.rs`, `reasoning_signature.rs` (~1.35k lines Rust) | LiteLLM `translate_request` for the Python shape | ChatMock and codex-lb have no Anthropic path (they map OpenAI chat and Ollama only) |
| Schema sanitizer | openai/codex `tools/src/json_schema.rs` `sanitize_json_schema` (~80 Python lines once ported) | opencode `sanitizeOpenAISchema`: removes `pattern`, `$schema`, `format` | Neither ChatMock nor codex-lb sanitizes schemas |
| Stream mapping | claude-code-proxy's streaming path (~2.4k lines; emits thinking blocks that carry only a signature) | LiteLLM `_process_event` | LiteLLM emits `redacted_thinking`, which breaks the reasoning round-trip |
| Errors, retries | openai/codex `responses_error.rs`, `api_bridge.rs`, `codex-client/src/retry.rs`; claude-code-proxy's Anthropic error mapping | codex-lb `modules/proxy/helpers.py:153` (error classes) and `selection_errors.py:17` (429 envelope with `resets_at`) | opencode and LiteLLM mishandle `usage_limit_reached` |
| Model catalog, Lite lane | openai/codex `models.json` field set; codex-lb `model_fetcher.py:144` (`/codex/models?client_version=`, 300-second snapshot) | codex-lb `proxy.py:129,2342,2430`: the Lite header is set when an `additional_tools` item is present. codex-lb `requests.py:402,757`: the instruction lift is skipped and top-level `tools` are omitted on Lite. ChatMock `fast_mode.py`: fast → `priority` | — |
| Token estimate | bytes ÷ 4 (openai/codex), calibrated per lane | claude-code-proxy's per-image (2000) and per-reasoning rules | o200k needs tiktoken, which is not stdlib |

Fixtures:

- **Recorded Responses SSE.** Only opencode has them: `packages/llm/test/fixtures/recordings/openai-responses/`, recorded against the public API. Whether they match the ChatGPT backend's event set is unverified.
- **Lite request bodies.** codex-lb `tests/fixtures/codex_bodies/captured_gpt56sol_lite_http.json` and `gpt56_lite_bundle.json` are captured backend bodies, rebuilt to shape only. They are the only evidence of the Lite request layout outside claude-code-proxy's code.
- **Schema inputs.** openai/codex `tools/tests/fixtures/json_schema_policy/`, six real MCP schemas.
- **Golden Anthropic streams and Claude Code bodies.** Only this gateway's own capture has them.

Licences:

- **MIT** (claude-code-proxy, opencode, LiteLLM, codex-lb, ChatMock): keep the holder's copyright line and the permission notice.
- **Apache-2.0** (openai/codex): each derived file says it was changed, and a copy of the licence and Codex's NOTICE ("OpenAI Codex / Copyright 2025 OpenAI") ship with the gateway.
- **Excluded:** GPL-3.0 (`caixiaoshun/claudex`), non-commercial (`icebear0828/codex-proxy`), and no licence file (`insightflo/chatgpt-codex-proxy`).

## Decisions for the user

Ruled on 2026-10-09:

- **D1, client identity: Codex's identity.** The Responses-Lite models (Sol, Terra, Luna) answer "Model not found" unless the request carries `originator: codex_cli_rs` (claude-code-proxy #45). The translator sends Codex's `originator` and `User-Agent`. The user ruled that third-party clients on a ChatGPT subscription are accepted practice, opencode being one.
- **D2, build.** The translator is built in this repo in stdlib Python, distilled from existing adapters where they fit. The distillation source map is the next research step.
- **D3, rename.** The gateway becomes `gpt-gateway`, renamed end to end: launchd label, install paths, log names, README, tests.

## Costs and risks

- **One local process in every request path of a router session.** The supervisor's fail-open covers a dead gateway: Claude Code goes direct to Anthropic, and GPT agents fail with a connection error instead of a hang.
- **ChatGPT account risk** from an unofficial client, made sharper by D1.
- **Claude token exposure** is closed: the GPT branch never forwards Claude credentials.
- **Auto-mode classifier billing**: a custom base URL makes the session ineligible. It does not matter in bypass-mode seats.
- **Harness fit is unmeasured.** GPT gets Claude Code's tool schemas and fleet prompts written for Claude (open question 1).
- **Capture volume.** GPT lanes add their traffic to `tmp/gw-logs/`, which is gitignored and never pruned.

## Alternatives

- **Cross-harness seat, available today.** `/flights:orchestrate-cross-harness {dir} engine codex` runs a task file on GPT in its native Codex harness. It is a chat seat briefed by inject, not an `Agent` call.
- **`codex exec` wrapper agent.** A thin Claude sub-agent runs `codex exec` and returns its output verbatim. GPT stays in its own harness and there is no traffic routing, but every GPT call is wrapped in a Claude call.
- **Whole-session GPT.** claude-code-proxy alone. It loses the Claude main thread.

## Open questions

1. **Harness fit.** This is the gating measurement: run one flight task file with the Sonnet executor and with its GPT twin, then compare the diff, the tests, the calls and the tokens.
2. **Launch-line composition.** Which fleet roles get GPT twins, and how pfm names a GPT-enabled seat.
3. **Fleet-prompt exception.** The wording for "a GPT-pinned agent is spawned with no `model`".
4. **Anthropic's terms** for subscription traffic through a local passthrough gateway. Not checked.
5. **`CLAUDE_CODE_SUBAGENT_MODEL` vs a definition's `model`.** Which wins is unprobed.
6. **Interactive sessions.** Every probe so far ran headless (`-p`).
7. **Backend acceptance.** Whether `chatgpt.com/backend-api/codex/responses` rejects a request without top-level `instructions`. Codex HEAD sends none, raine sends them on the full lane, and both work. The first live request settles it.
8. **Claude Code's context window for an unknown model ID.** Is its compaction threshold below the slug's window (272,000 for gpt-5.5)? To be probed with the fake server.

## Build plan

1. **Distillation sources** are picked (§ Distillation sources).
2. **Login.** `codex-login` and the token store. Acceptance: a refresh survives a forced expiry, and two concurrent refreshes produce one rotation.
3. **Translator, offline.** Golden tests from captured traffic:
   - Captured Claude Code sub-agent bodies (`tmp/gw-logs/*/…sub-POST-v1_messages/2-request.body`) translate to checked-in Responses bodies.
   - Recorded Responses SSE streams translate to checked-in Anthropic streams.
   - **Prefix test:** request N+1 of a lane, translated, starts with request N's translated `input` byte for byte.
   - **Signature test:** a reasoning item survives encode, Claude Code's emptying of the thinking text, and decode, unchanged.

   Fixtures are invented or scrubbed: no account ids or conversation text in tracked files.
4. **Router fork in `gateway.py`**, with the sub-agent-only guard and per-upstream connections.
5. **Live acceptance.** One `sol` sub-agent in a router session runs a tool-using task. On every turn from the third, the lane's `cached_tokens / input_tokens` is ≥ 0.8 and every reasoning item round-trips. Claude Code shows no error.
6. **Answer open question 1.**
7. **Productize.** Use `flights-foreman` with this doc as the spec (pfm launch line, GPT twins, fleet-prompt exception), or drop.

## Reproducing the probes

A loopback fake Anthropic server answers every request; nothing leaves the machine and no real key is used.

- Logs per `POST /v1/messages`: `model`, the tool names, the `Agent` tool's `model` schema, `anthropic-beta`, and the auth **class** (OAuth / API key / other — never the value).
- Scripted streaming replies (`message_start` → `content_block_start` → `content_block_delta` → `content_block_stop` → `message_delta` → `message_stop`): main turn 1 → an `Agent` `tool_use` for the probe agent (plus `model` for W6, W7, W9); the sub-agent, recognized by a marker in its system prompt → text; the main turn after the `tool_result` → text. `count_tokens` → `{"input_tokens": 10}`; `HEAD /api/hello` → 200.
- Run from a scratch directory, which holds `.claude/agents/{name}.md` for W5, W9 and W10:

```sh
ANTHROPIC_BASE_URL=http://127.0.0.1:{port} ANTHROPIC_API_KEY=sk-fake-probe \
  claude --setting-sources project --strict-mcp-config -p "delegate" \
    --tools "Agent,Read" \
    --agents '{"{name}":{"description":"probe","prompt":"{marker} probe","model":"gpt-5.5"}}'
```

`--setting-sources project` (or `--restricted`) keeps the user's hooks out. W4 drops `ANTHROPIC_API_KEY` so the subscription login is used.
