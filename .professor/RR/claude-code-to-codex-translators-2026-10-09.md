# RR — How open-source translators map Claude Code's Anthropic Messages traffic onto the ChatGPT-subscription Codex Responses backend, and back

Question: map, from source code, how existing translators turn Anthropic Messages API traffic (from Claude Code) into OpenAI Responses API calls on a ChatGPT subscription, and back, so we can design a translator that is correct on caching, tools, reasoning and streaming. Return the saved RR path, then a cited map answering each numbered question with file:line and quoted code. Primary target: github.com/raine/claude-code-proxy (Rust, MIT), its `src/` and its docs site claude-code-proxy.raine.dev. Secondary, for comparison only: other open-source Claude-Code/Anthropic → Codex/Responses bridges. Questions 1-11 (upstream; request mapping; tools; caching; reasoning; streaming; other endpoints; errors; auth; known limitations/issues incl. #117; model list and aliasing).

## Answer

raine/claude-code-proxy (at commit `4b5c058a`) does the following:
- It posts a `store:false`, always-`stream:true` Responses request to [`https://chatgpt.com/backend-api/codex/responses`](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/constants.rs#L1-L9), over WebSocket by default.
- It puts Claude Code's `system` into [`instructions`](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L36-L58). On the Responses-Lite lane the system text becomes a developer message and the tools go into an `additional_tools` item ([request.rs L512-549](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L512-L549)).
- It sets [`prompt_cache_key`](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L566-L568) to the incoming [`x-claude-code-session-id`](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/server.rs#L1443-L1446) header.
- It carries GPT's encrypted reasoning across turns inside the Anthropic thinking `signature`, formatted as [`ccp:codex:v1:{b64url(id)}:{encrypted_content}`](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/reasoning_signature.rs#L4-L95).
- It maps cached tokens to [`cache_read_input_tokens` with `cache_creation_input_tokens: 0`](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/reducer.rs#L1023-L1052).

Two other bridges handle the same problems differently. [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/runtime/executor/helps/claude_code_session.go#L102-L128) derives a UUIDv5 cache key per session+agent and shortens tool names over 64 characters. [LiteLLM](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L163-L186) uses the same signature-smuggling idea as raine, with a `litellm_encrypted_reasoning:` prefix.

## Map

All raine links are pinned to commit `4b5c058a3c900031d2b2e094a265a5ade7acad3a` and were read from a clone by `tracer-rr` (round 1 and round 2).

### 1. Upstream: URL, headers, body (raine, Codex provider)

**URL and transport**
- The constants are in [constants.rs L1-9](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/constants.rs#L1-L9):
  ```rust
  pub const CLIENT_ID: &str = "app_EMoamEEZ73f0CkXaXp7hrann";
  pub const ISSUER: &str = "https://auth.openai.com";
  pub const CODEX_API_ENDPOINT: &str = "https://chatgpt.com/backend-api/codex/responses";
  pub const ORIGINATOR: &str = "claude-code-proxy";
  pub const RESPONSES_LITE_ORIGINATOR: &str = "codex_cli_rs";
  pub const REFRESH_MARGIN_MS: u64 = 5 * 60 * 1000;
  pub const OAUTH_PORT: u16 = 1455;
  ```
- Transport defaults to WebSocket ([config.rs L883-898](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/config.rs#L883-L898)): `CCP_CODEX_TRANSPORT` → config `codex.transport` → `CodexTransport::WebSocket`.
- The WebSocket URL is the same path with the `wss://` scheme; its test fixture is `"wss://chatgpt.com/backend-api/codex/responses?mode=live"` ([websocket.rs L3019-3020](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/websocket.rs#L3019-L3020)).
- A forced web search goes to `format!("{base_url}/alpha/search")` ([client.rs L303-304](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/client.rs#L303-L304)).

**Headers** (`build_codex_headers`, [client.rs L95-165](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/client.rs#L95-L165)):
```rust
headers.insert("originator", header_value("originator", &originator)?);
headers.insert("openai-beta", header_value("openai-beta", "responses=experimental")?);
headers.insert("x-codex-beta-features", header_value("x-codex-beta-features", "remote_compaction_v2")?);
if use_responses_lite { headers.insert("x-openai-internal-codex-responses-lite", … "true") }
if let Some(ref account_id) = auth.account_id { headers.insert("ChatGPT-Account-Id", …) }
if let Some(ref session_id) = ctx.session_id {
    // Match the official Codex Responses session header.
    headers.insert("session-id", header_value("session-id", session_id)?);
    headers.insert("x-client-request-id", header_value("x-client-request-id", session_id)?);
    let window_id = format!("{session_id}:0");
    headers.insert("x-codex-window-id", header_value("x-codex-window-id", &window_id)?);
}
```
- The default User-Agent is `format!("claude-code-proxy/{}", env!("CARGO_PKG_VERSION"))`, overridable by `CCP_CODEX_USER_AGENT` or `CCP_USER_AGENT` ([config.rs L653-676](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/config.rs#L653-L676)).
- The originator is overridden by `CCP_CODEX_ORIGINATOR` on the full lane only. The Lite lane hard-codes `codex_cli_rs`.
- On WebSocket, `openai-beta` is rewritten to `"responses_websockets=2026-02-06"` and content-type/accept are dropped ([websocket.rs L40-42](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/websocket.rs#L40-L42), [L636-660](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/websocket.rs#L636-L660)).
- No `version` or `conversation_id` header is set.
- The session id comes from `.get("x-claude-code-session-id")` ([server.rs L1443-1446](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/server.rs#L1443-L1446)). The account id comes from the JWT claims `chatgpt_account_id`, then `https://api.openai.com/auth`.chatgpt_account_id, then the first organization ([jwt.rs L57-63](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/jwt.rs#L57-L63)).

**Body** (`ResponsesRequest` [request.rs L78-120](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L78-L120), builder [L474-608](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L474-L608)):
```rust
let mut out = ResponsesRequest { model: opts.model, instructions, input,
    store: false, stream: true, parallel_tool_calls, tool_choice, text, tools: None,
    include: None, client_metadata: None, service_tier: None, prompt_cache_key: None, reasoning: None };
…
if let Some(sid) = opts.session_id { out.prompt_cache_key = Some(sid); }
…
if wants_reasoning { out.include = Some(vec!["reasoning.encrypted_content".to_string()]); }
```
- `text` is `{verbosity: "low", format}`. `format` comes from `output_config.format`; a `json_schema` there becomes strict, with every property made required ([L614-636](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L614-L636)).
- The struct has no `max_output_tokens`, temperature, top_p, stop_sequences or metadata field. A test asserts `value.get("max_output_tokens").is_none()` ([L2021](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L2021-L2021)).
- `previous_response_id` comes only from the opt-in continuation layer (§4).

### 2. Request mapping (raine)

**System prompt**
- `flatten_system_text` joins the text blocks with `"\n\n"` and drops any block that `starts_with("x-anthropic-billing-header:")` ([translate_shared.rs L36-58](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L36-L58)). The result becomes `instructions` on the full lane.
- No fixed Codex instructions prompt is injected.
- On the Responses-Lite lane, the code moves `instructions` into the input ([request.rs L512-549](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L512-L549)):
  ```rust
  prefix.push(ResponsesInputItem::AdditionalTools { id: None, role: "developer".to_string(), tools });
  if let Some(instructions) = out.instructions.take() && !instructions.is_empty() {
      prefix.push(ResponsesInputItem::Message { role: "developer".to_string(), content: vec![InputText { text: instructions }] });
  }
  ```
- The Lite lane is selected by seven exact ids: gpt-5.6-luna, gpt-5.6-sol, gpt-5.6-terra, gpt-6-astra, gpt-6-luna, gpt-6-sol and gpt-6.1-sol ([model_allowlist.rs L124-147](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/model_allowlist.rs#L124-L147)).
- A hosted web_search tool forces the full lane and upgrades luna to sol ([mod.rs L612-622](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L612-L622)).
- A `system`-role entry inside `messages` becomes a `developer` message ([request.rs L919-935](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L919-L935)).

**Text and images**
- User text becomes `InputText`. Assistant text becomes `OutputText` inside `role:"assistant"` messages ([request.rs L936-989](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L936-L989)).
- Images use `image_source_to_url` ([translate_shared.rs L111-117](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L111-L117)):
  ```rust
  if source.source_type == "url" { source.data.clone() } else { format!("data:{};base64,{}", source.media_type, source.data) }
  ```
- **The code contradicts the docs here.** A top-level user URL image is sent upstream as `input_image.image_url` = the remote URL. It is not turned into a placeholder, and top-level base64 images are not validated: media type defaults to `image/png` ([translate_shared.rs L158-183](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L158-L183)).
- Validation applies only inside a `tool_result` (`validated_image_data_url`: `"image/jpeg" | "image/png" | "image/gif" | "image/webp"` plus a base64 decode; [request.rs L1119-1165](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L1119-L1165)).
- The docs say: "Remote URLs and malformed or unsupported nested images remain text placeholders." ([compatibility-and-limitations](https://claude-code-proxy.raine.dev/reference/compatibility-and-limitations/)).
- The content parser keeps text, image, tool_use, tool_result and thinking blocks. Every other block type (document, redacted_thinking) hits `_ => None` and is dropped. `cache_control` is never read ([translate_shared.rs L215-232](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L215-L232)).

**tool_use → function_call**
- `call_id: id.clone()`, `arguments: serde_json::to_string(input)` ([request.rs L954-966](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L954-L966)).
- IDs pass through unchanged in both directions, with no mapping table. On the way back, upstream `call_id` becomes the Anthropic `tool_use.id` ([live_stream.rs L353-396](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L353-L396)).

**tool_result → function_call_output** ([request.rs L877-908](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L877-L908)):
```rust
let mut rendered = render_tool_result(content);
if is_error.unwrap_or(false) { rendered.prepend_text("[tool execution error]".to_string()); }
…
out.push(ResponsesInputItem::FunctionCallOutput { call_id: tool_use_id.clone(), output: function_call_output(rendered) });
```
- Without images, the output is joined text. With valid images, it is a content-items array of `input_text`/`input_image`.
- A URL image becomes `"[image omitted: url]"`. Any other block type becomes `"[unsupported content block omitted: {kind}]"` ([request.rs L1093-1197](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L1093-L1197)).
- Pending user parts are flushed before each `function_call_output`.

**Thinking on assistant turns**
- The thinking text is dropped. Only a decodable signature is replayed, as `Reasoning { id, summary: Vec::new(), encrypted_content }`.
- `[thinking, text, tool_use]` becomes `reasoning → message(assistant) → function_call` ([request.rs L936-984](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L936-L984)).

### 3. Tools (raine)

**Function tools** ([request.rs L638-711](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L638-L711)):
```rust
out.push(ResponsesTool::Function(ResponsesFunctionTool { kind: "function".to_string(), name, description, parameters, strict: false }));
```
- Sanitizing removes `pattern` recursively and nothing else ([L713-801](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L713-L801)). The code comment reads: "OpenAI's regex dialect differs from schemas emitted by clients. Drop pattern constraints rather than guessing backend compatibility".
- `$ref`, `format`, `$schema` and `additionalProperties` are not rewritten.
- There is no tool-name length or charset limit and no renaming.
- Only `Read` gets rewritten descriptions for `offset` and `limit`.

**Server tools**
- `web_search_20250305` becomes `{kind:"web_search", external_web_access:true, search_content_types:["text","image"], filters}` built from allowed_domains/blocked_domains ([L652-680](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L652-L680)).
- `max_uses` is never read. The docs agree: "the Anthropic `max_uses` value is not enforced" ([limitations](https://claude-code-proxy.raine.dev/reference/compatibility-and-limitations/)).
- No other server tool (advisor, code_execution and so on) is special-cased. Any other entry is treated as a function.
- A standalone search request is routed to `/alpha/search` (`search::is_standalone_search_request`, [mod.rs L116-130](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L116-L130)).

**tool_choice** ([L803-855](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L803-L855)):
- `"auto"`→Auto, `"none"`→None, `"any" | "required"`→Required.
- `{type:"tool",name}`→`{type:"function",name}`.
- When the name is the web-search tool: `{type:"allowed_tools", mode:"required", tools:[{"type":"web_search"}]}`. This is downgraded to auto when no web_search tool is registered, per the code comment "upstream 502s instead of ignoring it".

**Parallel calls**
- `disable_parallel_tool_use` becomes `parallel_tool_calls = !disabled`, defaulting to true ([translate_shared.rs L60-67](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L60-L67)).
- The Lite lane forces `out.parallel_tool_calls = false`, which is open issue #36 (§10).

### 4. Caching (raine)

**Cache key**
- `prompt_cache_key` = the session id verbatim, taken from `x-claude-code-session-id` ([request.rs L566-568](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L566-L568)).
- The same id feeds the `session-id`, `x-client-request-id` and `x-codex-window-id` headers (§1).
- `metadata.user_id` is not used.
- Changelog v0.1.41 (2026-09-19): "Codex requests use the current session header for cache affinity" ([changelog](https://claude-code-proxy.raine.dev/reference/changelog/)).

**Other affinity mechanisms**
- **WebSocket pool:** `POOL_IDLE_TTL_MS: u64 = 30 * 60 * 1000` ([websocket.rs L50-51](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/websocket.rs#L50-L51)).
- **Continuation:** an opt-in `previous_response_id` continuation (`CCP_CODEX_PREVIOUS_RESPONSE_ID`, [config.rs L686-690](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/config.rs#L686-L690)). It falls back to full context (`full_context_retry`) on `previous_response_not_found` ([mod.rs L1326-1347](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L1326-L1347)).
- **Server compaction:** opt-in replay of server compaction state ([mod.rs L219-297](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L219-L297)).
- The docs say: "`previous_response_id` and server compaction state are in memory and require stable session routing." ([limitations](https://claude-code-proxy.raine.dev/reference/compatibility-and-limitations/)).

**Usage mapping** ([reducer.rs L1023-1052](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/reducer.rs#L1023-L1052)):
```rust
let cached = usage.input_tokens_details_cached.unwrap_or(0);
let input_tokens = total_input.saturating_sub(cached);
AnthropicUsage { input_tokens, output_tokens: usage.output_tokens.unwrap_or(0),
    cache_creation_input_tokens: 0, cache_read_input_tokens: cached, server_tool_use: None }
```
- Reasoning tokens are parsed but not surfaced ([live_stream.rs L1098-1115](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L1098-L1115)).
- `message_start` carries the local o200k estimate as `input_tokens` and `"output_tokens": 0` ([L251-277](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L251-L277)).
- Changelog v0.0.3 (2026-04-20): "Improved token counting accuracy and fixed cached token usage reporting" ([changelog](https://claude-code-proxy.raine.dev/reference/changelog/)).

**Measured hit rates:** none found in raine's code, docs, changelog or issues (searched by the F digger; see the open rabbit holes).

### 5. Reasoning (raine)

**Request side**
- `include: ["reasoning.encrypted_content"]` is set only when the effort is not `none`, with `store:false` ([request.rs L603-605](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L603-L605)).

**Signature codec** ([reasoning_signature.rs L4-95](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/reasoning_signature.rs#L4-L95)) — yes, encrypted_content is smuggled in the signature:
```rust
const PREFIX: &str = "ccp:codex:v1:";
const MAX_ID_BYTES: usize = 4 * 1024;
const MAX_ENCRYPTED_CONTENT_BYTES: usize = 8 * 1024 * 1024;
…
let encoded_id = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(replay.id.as_bytes());
Some(format!("{PREFIX}{encoded_id}:{}", replay.encrypted_content))
…
pub fn decode_reasoning_signature(signature: &str) -> Option<ReasoningReplay> {
    let payload = signature.strip_prefix(PREFIX)?;
```
- The signature is emitted as a `signature_delta` when the thinking block closes. A reasoning item without a summary becomes a signature-only thinking block (`"thinking": ""`) ([live_stream.rs L990-1071](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L990-L1071)).
- A foreign signature (for example a real Anthropic one) fails `strip_prefix` and the block is skipped ([request.rs L967-979](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L967-L979)).
- There is no server-side cache.
- Changelog v0.1.18 (2026-07-15): "Codex preserves encrypted reasoning across turns, improving continuity when conversation history is replayed."

**Summaries**
- `summary: Some("auto")` is sent unless the reasoning-summary config is `off` or `none` ([L577-602](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L577-L602)).
- `response.reasoning_summary_text.delta` opens a `{"type":"thinking","thinking":"","signature":""}` block and streams `thinking_delta`. Each `reasoning_summary_part.added` emits a `"\n\n"` thinking_delta ([live_stream.rs L148-168](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L148-L168), [L404-450](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L404-L450)).
- The docs say: "Reasoning summaries can appear as thinking blocks. Codex decides whether a summary is emitted." ([limitations](https://claude-code-proxy.raine.dev/reference/compatibility-and-limitations/)).

**Effort**
- Effort is read only from `output_config.effort`, with allowed values `["low", "medium", "high", "xhigh", "max"]` ([translate_shared.rs L69-91](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/translate_shared.rs#L69-L91)).
- `thinking.budget_tokens` is not read.
- The config override also accepts `none` ([request.rs L269-317](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L269-L317)).
- The Lite lane adds `context: "all_turns"`.

**Compaction detection** ([L323-388](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L323-L388)):
- A request is treated as compaction when the system contains `"You are a helpful AI assistant tasked with summarizing conversations"`, or when the last user message contains both `"CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."` and `"Your task is to create a detailed summary of the conversation so far"`.
- Such requests are capped at `Effort::Low` by default (`CCP_COMPACT_EFFORT`).

### 6. Streaming (raine)

**Event table** (`LiveStreamTranslator::accept`, [live_stream.rs L116-191](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L116-L191), item handling [L303-402](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L303-L402)):

| Responses event | Anthropic output |
|---|---|
| `codex.rate_limits`, `keepalive`, `response.created`, `response.in_progress` | `ping` (preceded once by `message_start`) |
| `response.web_search_call.*` | nothing |
| `output_item.added` type `message` | `content_block_start {type:text}` (deferred while web searches are pending) |
| `output_item.added` type `function_call` | `content_block_start {type:tool_use, id: call_id, name, input:{}}` |
| `output_item.added` type `reasoning` | captures id and encrypted_content (no output) |
| `reasoning_summary_text.delta` / `reasoning_summary_part.added` | thinking block, `thinking_delta` |
| `output_text.delta` | `text_delta` |
| `output_text.annotation.added` | web-search citation capture |
| `function_call_arguments.delta` | `input_json_delta`, live; `Read` buffered until done |
| `output_item.done` | `content_block_stop` |
| `response.completed` / `response.incomplete` / `response.done` | close blocks, `message_delta` (stop_reason, usage), `message_stop` |
| `response.failed` / `error` / `response.error` / failed status | `Err` → retry or `event: error` |

- `Read` buffering: `buffer_until_done: name == "Read"`, with a Read-only JSON repair ([L368-379](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L368-L379), [L529-624](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L529-L624), [L1117-1160](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L1117-L1160)).

**stop_reason** ([live_stream.rs L905-927](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L905-L927)):
```rust
let stop_reason = if self.incomplete_response_policy == IncompleteResponsePolicy::AllowMaxOutputTokens
    && is_standard_max_output_tokens_incomplete(payload) { STOP_MAX_TOKENS }
  else if self.saw_tool_use { STOP_TOOL_USE } else { STOP_END_TURN };
```
- Any other incomplete terminal becomes a 503, `"Incomplete response returned, reason: {reason}"` ([events.rs L111-151](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/events.rs#L111-L151)).
- Final usage goes only on `message_delta`.

**Errors before any output** ([mod.rs L856-1004](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L856-L1004)):
- Frames stay private, per the code comment "Keep protocol framing private until real output makes a transparent retry unsafe".
- Retries happen transparently on 429/5xx/529, on handshake or keepalive failure, on a retryable message, and on a socket closed before the terminal event ([L1312-1392](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L1312-L1392)).
- An empty successful completion is retried.
- A usage-limit event returns 429 at once.

**Errors after output has started** ([L1100-1236](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L1100-L1236)):
- `translator.error_chunk(...)` closes open blocks, then emits `event: error {"type":"error","error":{"type": error_type, "message": message}}` ([live_stream.rs L209-249](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/live_stream.rs#L209-L249)).
- A stream that ends without a terminal event gets `"Upstream event stream closed before terminal Codex response event"` (`api_error`). The exception is a WebSocket close after a completed tool call, which is finished as `tool_use` (`finish_after_closed_completed_tool_call`).
- A heartbeat emits `ping` while waiting.

**Timeouts and backoff**
- Header timeout: `CODEX_DEFAULT_HEADER_TIMEOUT_MS: u64 = 300_000` ([config.rs L908-932](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/config.rs#L908-L932)).
- Backoff: `RETRY_INITIAL_DELAY_MS: u64 = 2000`, `RETRY_MAX_DELAY_MS: u64 = 30_000`, `MAX_RATE_LIMIT_RETRIES: u32 = 3` ([retry.rs L4-7](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/retry.rs#L4-L7)).

### 7. Other endpoints (raine)

**Router** ([server.rs L263-295](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/server.rs#L263-L295)):
```rust
.route("/healthz", get(healthz)).route("/monitor", get(handler_monitor))
.route("/v1/messages", post(handler_messages))
.route("/v1/messages/count_tokens", post(handler_count_tokens))
.route("/v1/models", get(handler_models));
```
- Feature-gated routes: `/v1/responses`, `/v1/chat/completions`, `/v1/images/*`, `/v1/audio/transcriptions`.
- The fallback returns 404 `not_found` with `"No route for {method} {path}"` ([L2177-2183](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/server.rs#L2177-L2183)).
- There is no `HEAD /` or `GET /` route.

**/v1/models**
- It returns an Anthropic-shaped list with `"display_name": format!("{model} ({provider})")` ([L342-384](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/server.rs#L342-L384)).
- The code comment says it is for `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`, and that Claude Code only lists ids starting with `claude` or `anthropic`.

**count_tokens** — always local ([count_tokens.rs L8-37](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/count_tokens.rs#L8-L37), [L68-99](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/count_tokens.rs#L68-L99)):
- It counts the translated request with o200k_base: `o200k_base_singleton().count_ordinary(text)`.
- Each image counts `2000`.
- Encrypted reasoning counts `((len*3/4) - 650 + 3)/4`.
- Framing adds `input.len() * 4` + `tools.len() * 4` + the model-name tokens, with a minimum of 1.

**Non-streaming**
- Upstream is always `stream:true`. The buffered SSE is retried on empty completion, then `accumulate_response_with_traffic` returns `(StatusCode::OK, Json(json))` ([mod.rs L367-424](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L367-L424), [L442-519](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L442-L519)).
- The accumulator replays the buffered SSE through the same reducer with `IncompleteResponsePolicy::Error`, and sorts blocks by their Anthropic index ([accumulate.rs L26-39](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/accumulate.rs#L26-L39), [L179-284](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/accumulate.rs#L179-L284)).
- Unparseable tool args fall back to the raw string: `.unwrap_or_else(|_| Value::String(args.clone()))` ([L56-177](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/accumulate.rs#L56-L177)).

### 8. Errors (raine)

**Mapper** ([mod.rs L1439-1572](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L1439-L1572)):
- **Usage limit** → 429 `rate_limit_error` with these headers:
  ```rust
  headers.insert(HeaderName::from_static("x-should-retry"), HeaderValue::from_static("false"));
  headers.insert(HeaderName::from_static("anthropic-ratelimit-unified-status"), HeaderValue::from_static("rejected"));
  ```
  Plus `anthropic-ratelimit-unified-reset` and `-representative-claim`.
- **Context window:** `is_context_window_overflow` = `message.to_ascii_lowercase().contains("context window")` → `json_error(StatusCode::PAYLOAD_TOO_LARGE, "request_too_large", message)`.
- **Empty completion, retries exhausted** → 503 `api_error` with `x-should-retry: false`. The code comment says "Claude Code resends a 503 up to ten times, each one repeating the proxy's full retry loop."
- **By status:** 401 → `authentication_error`; 403 → `permission_error`; 429 → `rate_limit_error` + `Retry-After`; other 400-599 keep their status with `api_error` (529 → `overloaded_error`); no status → 502 `api_error`.

**Event classifier** ([events.rs L133-250](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/events.rs#L133-L250)):
- `context_length_exceeded`, or a message containing "context window" or "context length exceeded" → 413.
- `insufficient_quota` → 429.
- `usage_not_included` → 403.
- `server_is_overloaded` or `slow_down` → Overloaded (529).
- `cyber_policy` → 400 "This request has been flagged for possible cybersecurity risk."
- An unclassified `response.failed` is Transient (retried), per the code comment "Native Codex treats an unclassified response.failed event as retryable".
- Client error types come from `client_error_type` ([events.rs L29-48](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/events.rs#L29-L48)).

**401 handling:** an upstream 401 triggers one `force_refresh(&auth.access)` and a retry ([client.rs L996-1005](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/client.rs#L996-L1005)).

**Unknown model:** 400 `invalid_request_error`, `"Model \"{model}\" resolves to unsupported model \"{}\""` ([mod.rs L100-115](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L100-L115)).

**Related changelog lines** ([changelog](https://claude-code-proxy.raine.dev/reference/changelog/)):
- v0.1.14 (2026-07-12): "Codex context-window errors trigger Claude Code's compaction flow instead of ending the request."
- v0.1.40 (2026-09-14): "Exhausted Codex subscription limits now fail immediately instead of retrying for minutes".
- v0.1.44 (2026-10-07): "Codex requests that repeatedly produce no output now fail after the proxy's retries".

### 9. Auth (raine)

**Browser PKCE login** ([pkce.rs L48-98](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/pkce.rs#L48-L98)):
```rust
.append_pair("response_type", "code").append_pair("client_id", CLIENT_ID)
.append_pair("redirect_uri", redirect_uri).append_pair("scope", "openid profile email offline_access")
.append_pair("code_challenge", &pkce.challenge).append_pair("code_challenge_method", "S256")
.append_pair("id_token_add_organizations", "true").append_pair("codex_cli_simplified_flow", "true")
.append_pair("state", state).append_pair("originator", ORIGINATOR);
```
- It uses Codex CLI's client id `app_EMoamEEZ73f0CkXaXp7hrann` and redirect `http://localhost:1455/auth/callback` (constants above).
- The code is exchanged by form POST to `{issuer}/oauth/token` with `code_verifier`.
- A state mismatch is rejected with `bail!("Invalid callback: state mismatch")` ([browser_login.rs L169-172](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/browser_login.rs#L169-L172)).

**Device flow**
- It POSTs `{issuer}/api/accounts/deviceauth/usercode`, then polls `/api/accounts/deviceauth/token` ([device.rs L84](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/device.rs#L84-L84), [L112](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/device.rs#L112-L112)).
- The user visits `{issuer}/codex/device`.
- The final exchange goes to `/oauth/token` with redirect `{issuer}/deviceauth/callback`.

**Storage**
- The keychain names are `KEYCHAIN_SERVICE: &str = "claude-code-proxy.codex"` and `KEYCHAIN_ACCOUNT: &str = "auth"`.
- The keychain is used when `cfg!(target_os = "macos") && std::env::var_os("CCP_CONFIG_DIR").is_none()`. Otherwise tokens go to `{config_dir}/codex/auth.json` ([token_store.rs L6-67](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/token_store.rs#L6-L67), [paths.rs L80](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/paths.rs#L80-L80)).
- The file is written with `.mode(0o600)` ([auth.rs L344-346](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/auth.rs#L344-L346)).
- The stored shape is `{access, refresh, expires, accountId}`.

**Refresh** ([manager.rs L52-158](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/auth/manager.rs#L52-L158)):
- It fires when `expires <= now + REFRESH_MARGIN_MS` (5 min).
- `let _refresh_guard = self.refresh_lock.lock().await;` makes it single-flight, then storage is reloaded (code comment: "Another request may have rotated and persisted the token while this caller was waiting").
- It sends a `grant_type=refresh_token` form and saves the rotated `refresh_token`.
- On a refresh 401/403 it clears stored auth, unless another writer already rotated the token.

**Sharing ~/.codex/auth.json**
- raine never reads `~/.codex/auth.json`: a grep for `.codex`/`CODEX_HOME` over src found nothing. It holds its own refresh token.
- The docs agree: "The proxy owns its tokens and does not read native Codex CLI credentials." ([llms.txt](https://claude-code-proxy.raine.dev/llms.txt)).
- Contrast: ccproxy-api stores its token in `~/.codex/auth.json` by default (§12).

### 10. Known limitations and tracker (raine)

**Docs Limitations page** ([compatibility-and-limitations](https://claude-code-proxy.raine.dev/reference/compatibility-and-limitations/)):
- "Anthropic-specific fields without a provider mapping can be dropped."
- "Incoming clients are not authenticated."
- "Encrypted reasoning and compaction items remain provider continuation data and are not exposed as raw chain of thought."
- "`[1m]` is a Claude Code client hint and does not change upstream context."

**#117** "per-request routes list": it proposes "one `routes` list in `config.json`" matching on kind, model or effort and setting provider/model/effort. "ItsAlbertZhang closed this as completed on Oct 2, 2026" (the author closed it). The body cites #63, #67, #72, #93 and #11 ([#117](https://github.com/raine/claude-code-proxy/issues/117)).

**Open or recent issues**
- **Streaming/transport:**
  - [#172](https://github.com/raine/claude-code-proxy/issues/172) (open): "Codex WebSocket: 403 "WebSocket upgrade was rejected" at low concurrency since 2026-10-05 ~03:33 UTC; re-login doesn't help". Reporter: "downgrading to **v0.1.42** makes the problem go away for me."
  - [#174](https://github.com/raine/claude-code-proxy/issues/174) (closed Oct 8, 2026): "Codex: exhausted empty-completion 503 is retried by Claude Code, so a deterministic empty turn hangs ~30 min". Fixed by #175 with `x-should-retry: false`, released in v0.1.44.
- **Continuation/caching state:**
  - [#163](https://github.com/raine/claude-code-proxy/issues/163) (open): "Codex server compaction state is lost after idle time, restarts, a second boundary, or a subagent request". It reports: "State is keyed by `x-claude-code-session-id` alone."
- **Tools:**
  - [#142](https://github.com/raine/claude-code-proxy/issues/142) (open): "Artifact tool schema (Unicode property regex) rejected by backend: 400 Invalid schema for function 'Artifact'". Maintainer: "Might be fixed in latest but untested." This is the reason for the `pattern` stripping in §3.
  - [#36](https://github.com/raine/claude-code-proxy/issues?q=is%3Aissue+quota+OR+%22parallel_tool_calls%22+OR+%22Model+not+found%22) (open): "Codex responses-lite: parallel_tool_calls is hardcoded to false, so models never emit parallel tool calls".
- **Errors:**
  - [#65](https://github.com/raine/claude-code-proxy/issues?q=is%3Aissue+quota+OR+%22parallel_tool_calls%22+OR+%22Model+not+found%22) (open, opened Jul 19, 2026): "Codex quota exhaustion at stream start returns 502 api_error, not 429 rate_limit_error". The current code maps usage-limit events to 429 (§8); whether #65 is stale is unsettled.
- **Identity:**
  - [#45](https://github.com/raine/claude-code-proxy/issues?q=is%3Aissue+quota+OR+%22parallel_tool_calls%22+OR+%22Model+not+found%22) (closed Jul 28, 2026): "GPT-5.6 Luna returns "Model not found" when User-Agent identity differs from the Responses Lite originator".
- Further issues and PRs were reported by the digger from issue-list pages; titles only, unquoted by step 6:
  - #118 ("continuation drops previous_response_id after tool calls", closed)
  - #95 (continuation collides across agents, closed)
  - #131 (standalone searches collide across agents, open)
  - #73 (WebSocket 401s, open)
  - #148 (`continue` requests forwarded as full conversations, open)
  - PRs #74 (reasoning summaries opt-in), #75 (disable reasoning signatures), #149 (surface reasoning tokens and rate limits in usage), #164 (persist compaction state), #66/#121 (429 handling), #77 (fullLane), #135 (WebSocket connect pacing)
- **Bans:** no raine issue mentions an account ban, suspension or warning (the digger's keyword search returned none).

### 11. Models and aliasing (raine)

**Allowed models** (`ALLOWED_MODELS`, [model_allowlist.rs L7-37](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/model_allowlist.rs#L7-L37)): `gpt-5.2`, `gpt-5.3-codex`, `gpt-5.3-codex-spark`, `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.5`, `gpt-5.6-luna`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-6-astra`, `gpt-6-luna`, `gpt-6-sol`, `gpt-6.1-sol`.

**Aliases** are an exact list, not wildcards:
- `haiku`, `claude-haiku-4-5`, `claude-haiku-4-5-20251001` → `gpt-6-luna`
- `sonnet`, `claude-sonnet-4-6`, `claude-sonnet-5` → `gpt-5.6-terra`
- `opus`, `claude-opus-4-7`, `-4-8`, `-5`, `-5-5`, `fable`, `claude-fable-5` → `gpt-6-sol`

**`-fast`**
- `format!("{m}-fast")` exists for every allowed id. It strips the suffix and sets `service_tier: Some(ServiceTier::Priority)` ([L45-122](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/model_allowlist.rs#L45-L122)).
- A `codex_service_tier()` override accepts fast, priority or flex ([request.rs L390-413](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L390-L413)).

**`[1m]` and context windows**
- `[1m]` is stripped case-insensitively before matching ([registry.rs L205-211](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/registry.rs#L205-L211)). The docs say: "`[1m]` is a Claude Code client hint and does not change upstream context."
- The code has no per-model context-window or max-output table (grep `context_window|max_output` found none outside tests).
- The README pairs `ANTHROPIC_MODEL=gpt-6-sol[1m]` with `CLAUDE_CODE_AUTO_COMPACT_WINDOW=272000` ([README](https://github.com/raine/claude-code-proxy)). llms.txt says this "keeps the local threshold within the ChatGPT context limit" ([llms.txt](https://claude-code-proxy.raine.dev/llms.txt)).
- The default model, when none is given, is `"gpt-6-sol"` ([mod.rs L100-115](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/mod.rs#L100-L115)).

### 12. Secondary translators, compared on the same problems

All links pinned. Each was read from a clone by a `tracer-rr`.

**CLIProxyAPI** (router-for-me/CLIProxyAPI @ `d318bcc3`, Go)
- **Instructions:** sent as `""`. The Claude system text becomes one leading `role:"developer"` message ([codex_claude_request.go L59-98](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/translator/codex/claude/codex_claude_request.go#L59-L98)).
- **Body:** always `store:false`, `include:["reasoning.encrypted_content"]` ([L379-434](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/translator/codex/claude/codex_claude_request.go#L379-L434)).
- **Cache key:** `prompt_cache_key` = UUIDv5 of model + `claude:{session}:agent:{agent}`. The session comes from `X-Claude-Code-Session-Id` or `metadata.user_id`; the agent from `X-Claude-Code-Agent-Id` ([claude_code_session.go L102-128](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/runtime/executor/helps/claude_code_session.go#L102-L128)). It is also sent as the `Session-Id` header ([codex_executor_request.go L131-147](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/runtime/executor/codex_executor_request.go#L131-L147)).
- **Identity:** originator `codex-tui` with a `codex-tui/0.154.0` User-Agent ([L26-31](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/runtime/executor/codex_executor_request.go#L26-L31)).
- **Tools:**
  - `strict:false`; the schema has `$schema`/`$id` and `\p{}` patterns stripped.
  - Names longer than 64 characters are shortened and mapped back on the response side.
  - `web_search_*` becomes `{"type":"web_search"}`.
  - Call ids longer than 64 characters become prefix + sha256 ([L485-669](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/translator/codex/claude/codex_claude_request.go#L485-L669)).
- **Tool results:** `is_error` is dropped.
- **Reasoning:**
  - encrypted_content travels in the thinking `signature` ([response.go L248-259](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/translator/codex/claude/codex_claude_response.go#L248-L259); request side [L172-202](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/translator/codex/claude/codex_claude_request.go#L172-L202)).
  - It is backed by a 1-hour server-side replay cache, cleared on `invalid_encrypted_content` ([codex_executor_reasoning.go L760-826](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/runtime/executor/codex_executor_reasoning.go#L760-L826)).
  - `budget_tokens` maps to effort by thresholds ([convert.go L60-97](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/thinking/convert.go#L60-L97)).
- **Usage:** cached tokens become cache_read, and input is reduced by them ([response.go L806-865](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/translator/codex/claude/codex_claude_response.go#L806-L865)).
- **Errors:** `usage_limit_reached` becomes 429 with a Retry-After taken from `resets_at`. Errors are rewritten to `context_too_large` or `thinking_signature_invalid` ([codex_executor_terminal.go L303-445](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/runtime/executor/codex_executor_terminal.go#L303-L445)).
- **OAuth:** same client_id and port 1455. Tokens are stored in `~/.cli-proxy-api/codex-*.json` ([openai_auth.go L25-28](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/auth/codex/openai_auth.go#L25-L28), [token.go L19-86](https://github.com/router-for-me/CLIProxyAPI/blob/d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5/internal/auth/codex/token.go#L19-L86)).

**LiteLLM** (BerriAI/litellm @ `63a4f3f2`, Python)
- **Adapter:** `litellm/llms/anthropic/pass_through/responses_adapters/`.
- **System:** becomes `instructions` ([transformation.py L518-549](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L518-L549)).
- **Cache key:** `prompt_cache_key` comes from `metadata.user_id` JSON `session_id` ([utils.py L15-47](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/utils.py#L15-L47)). The `chatgpt/` provider's allow-list then drops `prompt_cache_key` and `max_output_tokens` before the request reaches chatgpt.com ([chatgpt transformation.py L100-118](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/responses/transformation.py#L100-L118)).
- **Reasoning:**
  - The thinking `signature` = `"litellm_encrypted_reasoning:" + encrypted_content` ([L163-186](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L163-L186)).
  - `budget_tokens` buckets: ≥4096 high, ≥2048 medium, ≥1024 low, else minimal ([L459-496](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L459-L496)).
- **Tools:**
  - `tool_choice`: any→required, tool→function, none→none ([L386-426](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L386-L426)).
  - Schemas are not sanitized, and `is_error` is ignored.
- **Streaming:** the stream wrapper emits a `signature_delta` on the reasoning item's `output_item.done`. `response.failed` becomes `event: error` ([streaming_iterator.py L291-432](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/streaming_iterator.py#L291-L432)).
- **chatgpt provider** ([common_utils.py L14-25](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/common_utils.py#L14-L25), [L208-250](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/common_utils.py#L208-L250)):
  - It prepends fixed "You are Codex…" instructions.
  - It sets originator `codex_cli_rs` and no `OpenAI-Beta` header.
  - Tokens are stored in `~/.config/litellm/chatgpt/auth.json`.

**opencode-openai-codex-auth** (numman-ali @ `bec2ad69`, TypeScript; OpenCode plugin, Responses→Codex rather than Anthropic)
- **Instructions:** replaced with the official Codex CLI prompt fetched from `raw.githubusercontent.com/openai/codex/{tag}/codex-rs/core/{promptFile}` ([codex.ts L155-167](https://github.com/numman-ali/opencode-openai-codex-auth/blob/bec2ad69b252ef4ad7dd33b9532ff8b4fdb6d016/lib/prompts/codex.ts#L155-L167)). A bridge prompt goes in as `role:"developer"` ([request-transformer.ts L362-380](https://github.com/numman-ali/opencode-openai-codex-auth/blob/bec2ad69b252ef4ad7dd33b9532ff8b4fdb6d016/lib/request/request-transformer.ts#L362-L380)).
- **Body:** forces `store=false` and includes encrypted_content. It strips every item `id` and drops `item_reference` items ([L310-331](https://github.com/numman-ali/opencode-openai-codex-auth/blob/bec2ad69b252ef4ad7dd33b9532ff8b4fdb6d016/lib/request/request-transformer.ts#L310-L331)).
- **Headers:** `session_id` and `conversation_id` are copied from the host's `prompt_cache_key`. Also `OpenAI-Beta: responses=experimental` and `originator: codex_cli_rs` ([fetch-helpers.ts L173-196](https://github.com/numman-ali/opencode-openai-codex-auth/blob/bec2ad69b252ef4ad7dd33b9532ff8b4fdb6d016/lib/request/fetch-helpers.ts#L173-L196)).
- **Orphaned tool outputs:** converted to assistant text ([input-utils.ts L123-147](https://github.com/numman-ali/opencode-openai-codex-auth/blob/bec2ad69b252ef4ad7dd33b9532ff8b4fdb6d016/lib/request/helpers/input-utils.ts#L123-L147)).

**musistudio/llms** (claude-code-router's transformer library @ `ac0fafec`, TypeScript)
- It has no Codex-subscription path. It never sends `prompt_cache_key`, `store` or `include`.
- The thinking `signature` is set to the reasoning `item_id` (`signature: data.item_id`), so reasoning cannot round-trip ([openai.responses.transformer.ts L500-554](https://github.com/musistudio/llms/blob/ac0fafec239f7e75deaac513ef7b5f25ed058f0a/src/transformer/openai.responses.transformer.ts#L500-L554)).
- An array-form `system` becomes `role:"system"` input items rather than `instructions` ([L85-156](https://github.com/musistudio/llms/blob/ac0fafec239f7e75deaac513ef7b5f25ed058f0a/src/transformer/openai.responses.transformer.ts#L85-L156)).
- Streaming silently drops `response.failed` and `response.incomplete`.

**ccproxy-api** (CaddyGlow @ `8350f0d5`, Python)
- **Instructions:** the captured Codex CLI instructions are prepended to the Anthropic system text.
- **Cache key:** `prompt_cache_key` is a fixed template UUID, `"2442d30f-2543-48fc-8fd2-bf595e099655"` in the fallback ([codex_headers_fallback.json L103-114](https://github.com/CaddyGlow/ccproxy-api/blob/8350f0d53ff4d909f24aa666aca38873ee7294e0/ccproxy/data/codex_headers_fallback.json#L103-L114)), not derived from the session.
- **Reasoning:** there is no encrypted-reasoning round trip.
- **Streaming:** `response.failed` and `response.incomplete` map to an ordinary finish ([openai_to_openai/streams.py L653-688](https://github.com/CaddyGlow/ccproxy-api/blob/8350f0d53ff4d909f24aa666aca38873ee7294e0/ccproxy/llms/formatters/openai_to_openai/streams.py#L653-L688)).
- **Tokens:** stored by default in `Path.home() / ".codex" / "auth.json"`, the Codex CLI's own file ([storage.py L17-28](https://github.com/CaddyGlow/ccproxy-api/blob/8350f0d53ff4d909f24aa666aca38873ee7294e0/ccproxy/plugins/oauth_codex/storage.py#L17-L28)).

**Corroboration across independent codebases**
- Codex OAuth client_id `app_EMoamEEZ73f0CkXaXp7hrann` with redirect `localhost:1455/auth/callback`: raine, CLIProxyAPI, LiteLLM, opencode-openai-codex-auth and ccproxy-api. All five say the value derives from openai/codex, so they share one origin.
- Smuggling encrypted reasoning in the Anthropic thinking signature: raine, CLIProxyAPI and LiteLLM.

## Coverage

- **A. Upstream wire contract and auth (Q1, Q9):** settled.
- **B. Request translation, messages and tools (Q2, Q3):** settled (round 2 closed images and the Lite lane).
- **C. Caching and reasoning state (Q4, Q5):** settled. The code is fully quoted; no measured hit rate exists in any source searched.
- **D. Response translation, streaming, non-streaming and errors (Q6, Q8, Q7 non-stream):** settled (round 2 closed the accumulator).
- **E. Auxiliary endpoints and model catalog (Q7, Q11):** settled. The code has no context-window table; the 272000 figure is README config only.
- **F. Known limitations and issue tracker (Q10):** settled.
- **G. Secondary translators comparison:** settled. Five repositories read.

Digging ended: every sub-area settled.

Result files (relative to the RR directory):
- tracer-rr/raine-claude-code-proxy-2026-10-09.md
- tracer-rr/raine-claude-code-proxy-2026-10-09-2.md
- tracer-rr/router-for-me-CLIProxyAPI-2026-10-09.md
- tracer-rr/BerriAI-litellm-2026-10-09.md
- tracer-rr/numman-ali-opencode-openai-codex-auth-2026-10-09.md
- tracer-rr/musistudio-llms-2026-10-09.md
- tracer-rr/CaddyGlow-ccproxy-api-2026-10-09.md

## Verification

- 30 web facts were checked on 8 pages: the Limitations page (8), #117 (3), #174 (3), #163 (3), #172 (3), #142 (3), the issue list (3) and the changelog (6). All 30 were confirmed with a quoted sentence.
- NOT ON PAGE: none. UNCHECKED: none.
- Code facts were read from pinned clones by `tracer-rr` and were not re-fetched.
- The PR and issue titles listed under "Further issues and PRs" in §10 come from the digger's list-page reads and were not re-sampled at verification.

## Open rabbit holes

- Measured prompt-cache hit rates for any Claude Code → Codex bridge: none published (dug, unsettled). The way to measure is to log `input_tokens_details.cached_tokens`/`input_tokens` per turn. An unfetched lead says Yimura/claude-code-proxy issue #31 reports zero cache tokens on Codex streams (undug).
- Whether chatgpt.com's Codex backend accepts a remote `image_url` in `input_image`, given that raine passes top-level URL images through while its docs say placeholders (dug, unsettled).
- Whether `prompt_cache_key` = raw session id (raine) or UUIDv5(session+agent) (CLIProxyAPI) gives better hit rates when subagents share a session. Related: #95, #131, #163 (dug, unsettled).
- #172: the WebSocket 403 that started between v0.1.42 and v0.1.43. Compare the two tags (undug).
- #65 vs. the current code: is "quota exhaustion at stream start returns 502" stale now that `usage_limit_response` returns 429? (dug, unsettled).
- PR #77 "fullLane": there is no way to leave the Responses-Lite lane at this commit (undug).
- What native openai/codex sends (headers `version`, `conversation_id`, `X-Codex-Routing-Hint`, instructions per model). Out of scope here; a sibling researcher covers it (undug).
- `thinking.budget_tokens` is ignored by raine, which reads only `output_config.effort`. Whether current Claude Code always sends `output_config.effort` is not established (undug).
- OpenAI's stance on third-party use of Codex subscription OAuth, and account-ban reports: no raine issue mentions one (dug, unsettled; ToS opinions out of scope).
