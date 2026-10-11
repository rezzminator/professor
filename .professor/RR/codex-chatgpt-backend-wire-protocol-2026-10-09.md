# RR — How the codex-rs CLI talks to the ChatGPT-subscription backend (wire protocol from source)

Question: map, from source code, EXACTLY how the official OpenAI Codex CLI (Rust, github.com/openai/codex, the `codex-rs/` tree, current default branch) talks to the ChatGPT-subscription backend, so a third-party translator can reproduce its requests byte-for-byte in behaviour. Return the saved RR path, then a cited map answering every numbered question with file:line and quoted code from the repo.

All code links are pinned to commit `9a59289fbea0e7e829c06aecfdd627651854da0f` (default-branch HEAD, committed 2026-10-09T14:21:14Z). The code was read from a clone by `tracer-rr`. Short form below: `{crate}/…` means `codex-rs/{crate}/…`.

## Answer

With ChatGPT auth, Codex sends `POST https://chatgpt.com/backend-api/codex/responses` as SSE ([model-provider-info/src/lib.rs#L426-L473](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider-info/src/lib.rs#L426-L473), [codex-api/src/endpoint/responses.rs#L123-L158](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/endpoint/responses.rs#L123-L158)). The built-in OpenAI provider prefers a `wss://…/responses` WebSocket carrying `response.create` frames ([core/src/client.rs#L1084-L1095](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1084-L1095), [codex-api/src/common.rs#L330-L390](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/common.rs#L330-L390)). Each request carries `Authorization: Bearer` and `ChatGPT-Account-ID` ([bearer_auth_provider.rs#L31-L47](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider/src/bearer_auth_provider.rs#L31-L47)) and a body with `store: false`, `stream: true`, `tool_choice: "auto"` and `include: ["reasoning.encrypted_content"]` ([core/src/client.rs#L947-L1071](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L947-L1071)). The body has no top-level `instructions` field: the base prompt from models.json goes first in `input` as a `developer` message ([core/src/client.rs#L988-L998](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L988-L998), [core/src/context/base_instructions.rs#L26-L46](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/base_instructions.rs#L26-L46)). Cache affinity comes from `prompt_cache_key` and the `session-id` header, both set to the session id ([core/src/client.rs#L638-L660](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L638-L660)).

## Map

### A. Transport, endpoint, headers (Q1, Q2)

**Base URL**
- ChatGPT-type auth defaults to `pub const CHATGPT_CODEX_BASE_URL: &str = "https://chatgpt.com/backend-api/codex";`. Every other mode defaults to `"https://api.openai.com/v1"`. A configured `base_url` overrides both. Sources: [model-provider-info/src/lib.rs#L80](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider-info/src/lib.rs#L80-L80), [#L426-L473](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider-info/src/lib.rs#L426-L473).

**HTTP**
- The request is always `Method::POST` to path `"/responses"` with `Accept: text/event-stream`, and the body always has `stream: true`, so every HTTP request is SSE. Source: [codex-api/src/endpoint/responses.rs#L123-L158](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/endpoint/responses.rs#L123-L158).

**WebSocket**
- When it is used: whenever the provider has `supports_websockets`, which the built-in OpenAI provider sets, and the session has not fallen back to HTTP. Models also carry a `prefer_websockets` flag in models.json. Sources: [core/src/client.rs#L1084-L1095](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1084-L1095), [fallback #L711-L730](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L711-L730).
- URL: the base URL plus `/responses`, with the scheme switched to `wss`.
- Handshake header: `OpenAI-Beta: responses_websockets=2026-02-06`. Sources: [client.rs#L175](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L175-L175), handshake headers at [#L1360-L1395](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1360-L1395).
- Framing: each request is one text frame `{"type":"response.create", …}` holding the HTTP body fields plus `previous_response_id`. Only the new input items are sent. A prewarm frame carries `generate:false`. Sources: [codex-api/src/common.rs#L330-L390](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/common.rs#L330-L390), [core/src/client.rs#L2081-L2108](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L2081-L2108).

**Headers**

| Group | Headers | Source |
|---|---|---|
| Auth | `Authorization: Bearer {access_token}`, `ChatGPT-Account-ID: {account_id}`, `X-OpenAI-Fedramp: true` for FedRAMP accounts | [model-provider/src/bearer_auth_provider.rs#L31-L47](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider/src/bearer_auth_provider.rs#L31-L47) |
| Ids | `x-client-request-id: {thread_id}`, `session-id`, `thread-id` (hyphenated; not `session_id`/`conversation_id`), `x-openai-subagent` | [codex-api/src/endpoint/responses.rs#L86-L93](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/endpoint/responses.rs#L86-L93), [codex-api/src/requests/headers.rs#L5-L31](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/requests/headers.rs#L5-L31) |
| Per turn | `x-codex-beta-features`, `x-codex-turn-state`, `x-codex-window-id`, `x-codex-turn-metadata`, `x-codex-parent-thread-id`, `x-oai-attestation`, `x-openai-internal-codex-responses-lite` | [core/src/client.rs#L2400-L2433](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L2400-L2433), [core/src/responses_metadata.rs#L382-L410](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/responses_metadata.rs#L382-L410), assembly [core/src/client.rs#L1418-L1452](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1418-L1452) |
| Defaults | `originator: codex_cli_rs`; `User-Agent: {originator}/{version} ({os} {ver}; {arch}) {terminal}` | [login/src/auth/default_client.rs#L152-L179](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/default_client.rs#L152-L179), [#L456-L469](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/default_client.rs#L456-L469) |
| Provider | `version: {CARGO_PKG_VERSION}` on the OpenAI provider | [model-provider-info/src/lib.rs#L526-L561](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider-info/src/lib.rs#L526-L561) |

**The only "backend needs this" comment found** is `// ChatGPT derives cache affinity from the Responses session-id header.` ([core/src/client.rs#L652-L660](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L652-L660)). No code marks any other header as required.

### B. Request body, instructions, caching (Q3, Q4)

**Field order and serde**
- Fields serialize in this order: `model, stream, service_tier?, input, tools?, tool_choice, parallel_tool_calls, reasoning, store, stream_options?, include, prompt_cache_key?, text?, client_metadata?, access_programs?`. Source: [codex-api/src/common.rs#L278-L304](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/common.rs#L278-L304).

**Values the builder hard-codes**
- `tool_choice: "auto"`.
- `store: false` for every provider.
- `stream: true`.
- `include = ["reasoning.encrypted_content"]`.
- Sources: [core/src/client.rs#L947-L1071](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L947-L1071), [#L1053-L1061](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1053-L1061).

**Fields that are absent**
- There is no `instructions`, `max_output_tokens`, `temperature` or `top_p` field in the request.
- `previous_response_id` exists only on the WebSocket frame.
- Grepping `instructions` in common.rs found nothing, and in client.rs it finds only the fragment block.

**What gets filtered**
- `input.retain(|item| !matches!(item, ResponseItem::ConfigurationUpdate { .. }))`.
- For non-OpenAI providers only, the metadata passthrough and `encrypted_function_args` are stripped.
- Source: [core/src/client.rs#L999-L1018](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L999-L1018).

**`parallel_tool_calls`**
- The value sent is `prompt.parallel_tool_calls && !model_info.use_responses_lite` ([core/src/client.rs#L1058](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1058-L1058)).
- Every turn sets the prompt value to `true` ([core/src/session/turn.rs#L1569-L1577](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/session/turn.rs#L1569-L1577)).

**`reasoning.summary`**
- It is sent only behind `supports_reasoning_summary_parameter`.

**Ids on re-sent items**
- When items are re-sent, item ids without the expected prefix are cleared ([core/src/client.rs#L1073-L1082](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L1073-L1082)).

**Instructions**
- Source text: the model's `model_messages.instructions_template` from models.json. A `config.base_instructions` override or a resumed session's instructions take priority ([core/src/session/mod.rs#L791-L795](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/session/mod.rs#L791-L795)). Unknown slugs fall back to `models-manager/prompt.md` ([models-manager/src/model_info.rs#L16](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/models-manager/src/model_info.rs#L16-L16)).
- Wire form: a `"developer"` message with one `input_text` item ([core/src/context/base_instructions.rs#L26-L46](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/base_instructions.rs#L26-L46), [context-fragments/src/fragment.rs#L35-L53](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/context-fragments/src/fragment.rs#L35-L53)).
- Position and id: first in `input`, with id `msg_{uuidv5(uuidv5(NAMESPACE_OID, thread_id), text)}` ([core/src/client.rs#L964-L970](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L964-L970), [#L988-L998](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L988-L998)).
- Known-prompt check: none found. A grep for `known prompt|unknown prompt|instructions are not valid` across codex-rs returned no hits.

**Injected context in `input`**

| Context | Role | Wrapper | Source |
|---|---|---|---|
| AGENTS.md | `user` | `# AGENTS.md instructions for {dir}\n\n<INSTRUCTIONS>\n{text}\n</INSTRUCTIONS>` | [core/src/context/user_instructions.rs#L10-L35](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/user_instructions.rs#L10-L35) |
| Developer instructions | `developer` | none | [developer_instructions.rs#L17-L37](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/developer_instructions.rs#L17-L37) |
| Environment | `user` | `<environment_context>…</environment_context>` | [world_state/environment.rs#L203-L223](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/world_state/environment.rs#L203-L223) |
| Permissions | `developer` | `<permissions instructions>…</permissions instructions>` | [prompts/src/permissions_instructions.rs#L258-L274](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/prompts/src/permissions_instructions.rs#L258-L274) |

**Caching**
- `prompt_cache_key` is the configured override if there is one, otherwise `"{source}:{parent_thread_id}"` for internal sources, otherwise `session_id`. For root agents the `session-id` header carries the same value. Source: [core/src/client.rs#L638-L660](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L638-L660).

**Usage parsing**
- `input_tokens`, `input_tokens_details.cached_tokens` (plus `cache_write_tokens`), `output_tokens_details.reasoning_tokens` and `total_tokens` are read from `response.completed`. Source: [codex-api/src/sse/responses.rs#L117-L156](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/sse/responses.rs#L117-L156).

### C. Items, reasoning, tools, images (Q5, Q6, Q12)

**ResponseItem**
- The enum is tagged by `type` in snake_case. Source: [protocol/src/models.rs#L1013-L1250](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L1013-L1250).
- Variants:
  - `message`
  - `reasoning {id?, summary, content, encrypted_content}`
  - `function_call {name, namespace?, arguments: string, encrypted_function_args?, call_id}`
  - `function_call_output` and `custom_tool_call_output`: the output is a string or an array of content items
  - `custom_tool_call`, `local_shell_call`, `web_search_call`, `image_generation_call`, `tool_search_*`
  - `compaction`, `configuration_update`, `compaction_trigger`, `context_compaction`

**Reasoning item serialization** ([models.rs#L1051-L1063](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L1051-L1063), [#L1651-L1658](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L1651-L1658))
- `id` is omitted when None.
- `summary` is always sent, even as `[]`.
- `encrypted_content` has no skip attribute, so None is sent as `null`.
- `content` is skipped only when it is `Some` and holds no `ReasoningText` part. When it is None, it is sent as `"content": null`.

**Reasoning across turns**
- Because `store:false` is combined with `include: reasoning.encrypted_content`, reasoning items come back encrypted and are re-sent from history unchanged.
- Nothing in the request path filters Reasoning items:
  - `get_formatted_input_for_request` only adjusts image `detail` ([core/src/client_common.rs#L59-L66](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client_common.rs#L59-L66), [#L89-L107](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client_common.rs#L89-L107)).
  - `for_prompt` calls `normalize_history` ([context_manager/history.rs#L598-L615](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context_manager/history.rs#L598-L615)).
  - `normalize_history` adds missing outputs, drops orphan outputs and strips images and audio, but never touches Reasoning ([#L960-L979](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context_manager/history.rs#L960-L979)).

**Model switch**
- Codex adds a `developer` `<model_switch>` message: "The user was previously using a different model. Please continue the conversation according to the following instructions:" followed by the new model's instructions. Sources: [core/src/context/model_switch_instructions.rs#L17-L40](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/model_switch_instructions.rs#L17-L40), [world_state/model.rs#L42-L64](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context/world_state/model.rs#L42-L64).
- Reasoning is dropped only through compaction. `maybe_run_previous_model_inline_compact` compacts with the previous model when the model's `comp_hash` changed, or when the switch is to a smaller context window and usage exceeds it. Sources: [core/src/session/turn.rs#L1336-L1362](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/session/turn.rs#L1336-L1362), [#L1376-L1407](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/session/turn.rs#L1376-L1407).

**Tool specs**
- Shapes ([tools/src/tool_spec.rs#L20-L56](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/tools/src/tool_spec.rs#L20-L56), [tools/src/responses_api.rs#L25-L81](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/tools/src/responses_api.rs#L25-L81)):
  - `{type:"function", name, description, strict, defer_loading?, parameters}`
  - `namespace`
  - `tool_search`
  - `web_search {external_web_access?, indexed_web_access?, …}`
  - `custom {format:{type, syntax, definition}}`

**apply_patch**
- It is only a custom tool: `name:"apply_patch"`, format `{type:"grammar", syntax:"lark"}`.
- The grammar file is `core/assets/tools/apply_patch.lark` and starts `start: begin_patch hunk+ end_patch`.
- It is registered when `apply_patch_tool_type.is_some()`.
- Sources: [core/src/tools/handlers/apply_patch_spec.rs#L5-L28](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/tools/handlers/apply_patch_spec.rs#L5-L28), [apply_patch.lark#L1-L19](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/assets/tools/apply_patch.lark#L1-L19), [spec_plan.rs#L1450-L1453](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/tools/spec_plan.rs#L1450-L1453).

**Shell**
- The function is `exec_command`, with `strict:false` and `additionalProperties:false`.
- Parameters: `cmd` (required), `workdir`, `tty`, `yield_time_ms`, `max_output_tokens`, and optionally `shell`, `login`, `environment_id`, plus the approval parameters.
- A `local_shell` tool type appears only in rollout-trace.
- Source: [core/src/tools/handlers/shell_spec.rs#L24-L114](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/tools/handlers/shell_spec.rs#L24-L114).

**Schema sanitizer**
- A boolean schema becomes `{"type":"string"}`.
- `const` becomes a one-value `enum`.
- A missing `type` is inferred.
- If no type can be inferred, the schema is cleared to `{}`.
- Source: [tools/src/json_schema.rs#L75-L150](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/tools/src/json_schema.rs#L75-L150).

**Parallel calls**
- A parallel-capable tool takes a read lock and runs concurrently. Any other tool takes the write lock and runs alone ([core/src/tools/parallel.rs#L213-L217](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/tools/parallel.rs#L213-L217)).
- A call with no output gets a synthetic `function_call_output` of `"aborted"`, placed right after the call ([core/src/context_manager/normalize.rs#L46-L67](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/context_manager/normalize.rs#L46-L67)).

**Images**
- Wire shape: `input_image {image_url, detail?: auto|low|high|original}` ([protocol/src/models.rs#L877-L889](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L877-L889)). The same item is used inside tool outputs, `{type:"input_image", image_url|file_id, detail?}` ([#L2121-L2145](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L2121-L2145)).
- Detail and size limits: no detail, `auto` or `high` uses a maximum dimension of 2048 and 2,500 patches; `original` uses 6000 and 10,000; `low` is replaced by a text placeholder ([core/src/image_preparation.rs#L387-L427](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/image_preparation.rs#L387-L427), [utils/image/src/lib.rs#L74-L83](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/utils/image/src/lib.rs#L74-L83)).
- Encoding: only `data:` URLs are processed, and remote URLs become a placeholder. PNG, JPEG and WebP pass through unchanged when no resize is needed; otherwise the image is re-encoded, keeping JPEG or WebP and turning anything else into PNG ([utils/image/src/lib.rs#L181-L207](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/utils/image/src/lib.rs#L181-L207)).

### D. SSE, rate limits, errors, retries (Q7, Q8)

**SSE events handled**
- Handled: `response.created`, `response.output_item.added` / `.done`, `response.output_text.delta`, `response.custom_tool_call_input.delta`, `response.reasoning_summary_text.delta` / `.done`, `response.reasoning_text.delta`, `response.reasoning_summary_part.added`, `response.failed`, `error`, `response.completed`, `response.incomplete`.
- `response.function_call_arguments.*` is ignored; the arguments are taken from `output_item.done`.
- For `response.incomplete`: reason `interrupted` means end_turn=false, `content_filter` is an error, and anything else is a stream error.
- The idle timeout defaults to 300000 ms.
- Sources: [codex-api/src/sse/responses.rs#L344-L505](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/sse/responses.rs#L344-L505), [#L536-L571](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/sse/responses.rs#L536-L571), [model-provider-info/src/lib.rs#L66-L68](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider-info/src/lib.rs#L66-L68).

**Rate-limit headers**
- Usage windows: `x-{limit}-primary|secondary-used-percent|window-minutes|reset-at`, where `{limit}` defaults to `codex` and other limit families are collected too, plus `-limit-name`.
- Other headers: `x-codex-credits-*`, `x-codex-promo-message`, `x-codex-rate-limit-reached-type`.
- An in-stream `codex.rate_limits` event is also parsed.
- Sources: [codex-api/src/rate_limits.rs#L22-L95](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/rate_limits.rs#L22-L95), [#L135-L178](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/rate_limits.rs#L135-L178).
- `X-Models-Etag` is read from `/responses` replies ([sse/responses.rs#L44-L48](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/sse/responses.rs#L44-L48)).

**Errors**
- 429 with `error.type == "usage_limit_reached"` becomes UsageLimitReached {plan_type, resets_at, limit_window_minutes}. 503, 400 and 500 have their own mappings. Source: [codex-api/src/api_bridge.rs#L95-L235](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/api_bridge.rs#L95-L235).
- Error code `context_length_exceeded` becomes ContextWindowExceeded ([codex-api/src/sse/responses_error.rs#L47-L100](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/sse/responses_error.rs#L47-L100)). The turn then marks the context window full and returns without retrying ([core/src/session/turn.rs#L1634-L1718](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/session/turn.rs#L1634-L1718)).

**Retries**
- Request retries default to 4 with a 200 ms base. They cover 5xx and transport errors, not 429. Stream retries default to 5.
- Backoff is `base * 2^(attempt-1) * jitter 0.9..1.1`.
- Source: [codex-client/src/retry.rs#L15-L52](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-client/src/retry.rs#L15-L52).
- On 401: provider recovery first, then reload auth.json, then refresh the token ([core/src/client.rs#L2710-L2794](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/client.rs#L2710-L2794)).

### E. Auth refresh (Q9)

**Refresh request**
- `POST https://auth.openai.com/oauth/token` with a JSON body (`TokenEncoding::Json`).
- Body: `client_id: "app_EMoamEEZ73f0CkXaXp7hrann"`, `grant_type: "refresh_token"`, `refresh_token`. No `scope`.
- Sources: [login/src/auth/manager.rs#L1625-L1678](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L1625-L1678), [login/src/oauth/client.rs#L76-L126](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/oauth/client.rs#L76-L126), [manager.rs#L1711-L1730](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L1711-L1730).
- The returned id, access and refresh tokens are merged into auth.json, and `last_refresh` is set to now ([manager.rs#L1600-L1623](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L1600-L1623)).

**When refresh fires**
- Proactively, when the access-token JWT `exp` is within 5 minutes. If that cannot be read, when `last_refresh` is older than 8 days.
- After a 401: reload auth.json, then refresh.
- Source: [manager.rs#L3001-L3023](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L3001-L3023).

**account_id**
- The forced workspace id if set, otherwise `tokens.account_id`, otherwise the id_token claim `chatgpt_account_id`.
- The claims also carry `chatgpt_plan_type` and `chatgpt_account_is_fedramp`.
- Sources: [manager.rs#L886-L891](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L886-L891), [login/src/token_data.rs#L88-L117](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/token_data.rs#L88-L117).

**Concurrency**
- Within one process, refreshes are serialized by a `refresh_lock` semaphore.
- Before refreshing, Codex reloads auth.json; if the tokens changed, another writer already refreshed and the refresh is skipped ([manager.rs#L2841-L2892](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L2841-L2892)).
- `refresh_token_reused`, `refresh_token_expired`, `refresh_token_invalidated`, a 400 `invalid_grant` and a 401 are all treated as permanent failures ([manager.rs#L1680-L1691](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/login/src/auth/manager.rs#L1680-L1691)).
- No cross-process file lock on auth.json was found; storage.rs has only an in-memory Mutex. Two processes refreshing at the same moment can therefore still race and hit `refresh_token_reused`; the reload-before-refresh guard narrows that window but does not close it.

### F. Models, compaction (Q10, Q11)

**Model metadata**
- Bundled in `models-manager/models.json`, 11 slugs. Keys include max/context window, supported reasoning levels, verbosity, shell_type, truncation_policy, service_tiers, prefer_websockets, use_responses_lite and instructions_template.
- Example, `gpt-5.5`: context_window 272000, default_reasoning_level medium, default_reasoning_summary none, default_verbosity low, apply_patch_tool_type freeform, service_tiers [priority "Fast"], prefer_websockets true ([models-manager/models.json#L1340-L1462](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/models-manager/models.json#L1340-L1462)).

**/models endpoint**
- `GET {base}/models?client_version={v}` ([codex-api/src/endpoint/models.rs#L33-L45](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/endpoint/models.rs#L33-L45)).
- Fetched only with ChatGPT backend auth, command auth, or API-key discovery ([models-manager/src/manager.rs#L675-L679](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/models-manager/src/manager.rs#L675-L679)).
- Cached in `models_cache.json` under CODEX_HOME for 300 s ([manager.rs#L34-L35](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/models-manager/src/manager.rs#L34-L35)).
- The ETag from the response is stored, but no If-None-Match header is sent ([models.rs#L117-L123](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/codex-api/src/endpoint/models.rs#L117-L123)). A matching `X-Models-Etag` renews the cache TTL; a different one triggers a refetch ([manager.rs#L516-L542](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/models-manager/src/manager.rs#L516-L542)).

**Compaction**
- No code calls `/responses/compact`; a grep across codex-rs found nothing.
- Remote compaction v2 runs when the provider `is_openai()` or is an Azure Responses provider. Every other provider uses the local `SUMMARIZATION_PROMPT`. Sources: [model-provider/src/capabilities.rs#L30-L41](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/model-provider/src/capabilities.rs#L30-L41), [core/src/tasks/compact.rs#L63-L77](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/tasks/compact.rs#L63-L77).
- The request is a normal Responses request: the normalized history, then a trailing `{"type":"compaction_trigger"}` item, plus the tools, `parallel_tool_calls:true` and the base instructions ([core/src/compact_remote_v2_attempt.rs#L85-L108](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/compact_remote_v2_attempt.rs#L85-L108)). A unit test pins the trigger's shape ([protocol/src/models.rs#L3780-L3833](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L3780-L3833)).
- The first output item `{"type":"compaction","encrypted_content":"…"}` is captured. The alias `compaction_summary` is accepted. Sources: [compact_remote_v2.rs#L440-L450](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/compact_remote_v2.rs#L440-L450), [models.rs#L1230-L1258](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/models.rs#L1230-L1258).

**New history after compaction**
- What survives: user messages and some agent messages, cut down to `RETAINED_MESSAGE_TOKEN_BUDGET: usize = 64_000`. No reasoning items, tool calls or assistant messages are kept ([compact_remote_v2.rs#L73](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/compact_remote_v2.rs#L73-L73), [#L494-L590](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/compact_remote_v2.rs#L494-L590)).
- The `compaction` item goes last, and the initial context is re-injected before the last real user message ([core/src/compact.rs#L80-L112](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/compact.rs#L80-L112), [#L606-L670](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/compact.rs#L606-L670)).

**Auto-compaction threshold**
- `auto_compact_token_limit()` is the smaller of the model's configured limit and `(context_window * 9) / 10` ([protocol/src/openai_models.rs#L527-L538](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/protocol/src/openai_models.rs#L527-L538), [core/src/session/context_window.rs#L57-L111](https://github.com/openai/codex/blob/9a59289fbea0e7e829c06aecfdd627651854da0f/codex-rs/core/src/session/context_window.rs#L57-L111)).

## Coverage

Plan:
- A. Transport, endpoint & headers — settled
- B. Request body, instructions & caching — settled
- C. Conversation items, reasoning, tools & images — settled
- D. SSE parsing, errors & retries — settled
- E. Auth refresh — settled
- F. Models & compaction — settled

Digging ended: every sub-area settled (3 rounds, one `tracer-rr` per round on openai/codex).

Result files:
- `tracer-rr/openai-codex-2026-10-09-2.md`
- `tracer-rr/openai-codex-2026-10-09-3.md`
- `tracer-rr/openai-codex-2026-10-09-4.md`

Correction: the round-1 file mislabels two `core/src/client.rs` ranges. The block labelled 1421-1456 is lines 1418-1452 (`build_responses_options`), and the block labelled 1484-1513 is lines 1360-1395 (`build_websocket_headers`). The links in this map use the corrected ranges.

## Verification

- Every fact in the map is a `tracer-rr` code finding, quoted from the clone and linked to commit 9a59289. Under step 6 these count as fetched and are not re-fetched.
- There were no web facts to check.
- 0 facts were checked by web fetch, so there is no NOT ON PAGE and no UNCHECKED fact.

## Open rabbit holes

- **Gap between codex-cli 0.155.1 (installed locally) and HEAD 9a59289 (mapped here)** — undug. The top-level `instructions` field, `/responses/compact` and the header names (`session_id` vs `session-id`) may differ in the released binary. Diff tag `rust-v0.155.1` against HEAD for `codex-api/src/common.rs` and `core/src/client.rs`.
- **Whether the backend rejects requests without `instructions`, or with `max_output_tokens` / `temperature`** — dug, unsettled. The code simply never sends these fields, and no rejection handling or comment was found.
- **`split_prefix_updates` and `truncate_retained_messages` bodies** (exactly how compacted history is assembled) — dug, unsettled.
- **How the configured `model_auto_compact_token_limit` reaches `ModelInfo` in the `Total` scope** — dug, unsettled.
- **Exact semantics of `x-openai-internal-codex-responses-lite` / `use_responses_lite`** — undug.
- **https://github.com/openai/codex/discussions/7782** — undug. The error for the removed `wire_api = "chat"` cites it.
- **https://platform.openai.com/docs/guides/function-calling#custom-tools** and **https://platform.openai.com/docs/guides/tools-web-search#live-internet-access** — undug. The code comments cite them for the custom-tool grammar format and web_search access flags; both are outside this query's boundary.
