# RR — Which existing source code to distill, piece by piece, into a Python-stdlib Anthropic Messages → ChatGPT-Codex translator

Question: find the best existing source code to DISTILL into a Python-stdlib translator that sits in front of Claude Code: it receives Anthropic Messages API requests (`POST /v1/messages`, streaming SSE) and serves them from GPT via a ChatGPT Plus/Pro subscription login on `https://chatgpt.com/backend-api/codex/responses` (Responses API, `store:false`, `include:["reasoning.encrypted_content"]`). Return the saved RR path, then a ranked distillation map: for each candidate repo, license, commit, language, the exact files/functions that implement each piece below, their line counts, how coupled they are to their framework, and a verdict "take whole / distill piece X / skip" with the reason. Pieces P1 ChatGPT OAuth; P2 Anthropic request → Responses request; P3 tool schema sanitizing; P4 Responses SSE → Anthropic SSE; P5 error mapping and retries; P6 model catalog, Responses-Lite lane, -fast/service_tier; P7 token counting. Candidates: opencode, LiteLLM's adapter and chatgpt provider, raine/claude-code-proxy, other GitHub adapters. Also: reusable test fixtures, and each license's attribution requirement.

## Answer

No candidate can be taken whole. Each piece has a different best source:
- **P2 and P4 (the translation core):** distill from [raine/claude-code-proxy](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/LICENSE#L1-L3) (MIT). Its core is about 5.5k lines of Rust, and about 11k lines of WebSocket, continuation and compaction code can be dropped.
- **P3, P5 classification, P7 and the P1 flows:** the reference is [openai/codex](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/tools/src/json_schema.rs#L71-L197) (Apache-2.0 with a [NOTICE](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/NOTICE#L1-L6)).
- **P1 code to port:** [opencode's `codex.ts`](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L88-L102) (MIT) is the most direct, with browser and device login plus a shared refresh. Its tree also holds the only recorded Responses SSE fixtures found.
- **Python skeleton:** [LiteLLM's adapter](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/LICENSE#L1-L8) (MIT outside `enterprise/`) is the only Python one, but it is device-code-only, does no schema sanitizing, and has no `usage_limit_reached` mapping.
- **Smaller adapters:** each was skipped for its license, for buffering the stream and dropping reasoning, or for having no tests.

## Ranked distillation map

| Rank | Repo @ commit | Lang · license | Verdict |
| --- | --- | --- | --- |
| 1 | raine/claude-code-proxy @ `4b5c058a` | Rust · MIT | **distill P2, P4, P5 mapping, P6 Lite lane, P7 rules**; the behavioural spec |
| 2 | openai/codex @ `c63929af` | Rust · Apache-2.0 + NOTICE | **distill P3 sanitizer, P5 classification + retry, P1 flows, P7 bytes/4, P6 catalog fields** |
| 3 | anomalyco/opencode @ `38840623` | TypeScript · MIT | **distill P1 (`codex.ts`), P3 (`sanitizeOpenAISchema`), P5 tables; take its recorded SSE fixtures**; skip P2/P4 (not Anthropic-shaped) |
| 4 | BerriAI/litellm @ `63a4f3f2` | Python · MIT | **distill P1 device flow and P2/P4 skeleton** (25–35% of lines rewritten); skip handler.py, P3, P5, P6 |
| 5 | luiapidev/gpt-proxy @ `86396f1f` | TypeScript · MIT | **reference only** (signature codec, cache-key hash); no tests, Bun-bound login |
| — | insightflo/chatgpt-codex-proxy @ `11b0a3c7` | TypeScript · no LICENSE file | **skip**: buffered stream, drops reasoning, no `include` |
| — | caixiaoshun/claudex | TypeScript · GPL-3.0 | **skip**: copyleft |
| — | icebear0828/codex-proxy | TypeScript · non-commercial | **skip**: license |
| — | RayBytes/ChatMock, Soju06/codex-lb | MIT | **skip**: no Anthropic route |

### 1. raine/claude-code-proxy (Rust, MIT, @ `4b5c058a`)

- **Commit and license.** HEAD is still `4b5c058a` ("release v0.1.44", 2026-10-07), so the earlier file:line map holds verbatim. The license is MIT, "Copyright (c) 2026 Raine Virta" ([LICENSE#L1-L3](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/LICENSE#L1-L3), [Cargo.toml#L1-L8](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/Cargo.toml#L1-L8)).
- **Size.** `src/providers/codex/` plus the shared files total about 37.6k lines, roughly 60% production. Each file ends in one `#[cfg(test)] mod tests` block that runs to the end of the file (tracer-rr result file, per-file table).
- **Production lines per piece** (WebSocket, continuation, compaction, search and passthrough excluded; about 11k production lines skippable):

  | Piece | Production lines | Where |
  | --- | --- | --- |
  | P1 | ~870 | `auth/` |
  | P2 | ~1,350 | `request.rs`, `translate_shared.rs`, `reasoning_signature.rs` |
  | P3 | 45 | removes `pattern` only ([request.rs#L727-L731](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/request.rs#L727-L731)) |
  | P4 | ~2,400 | live path: `live_stream.rs`, `reducer.rs`, `accumulate.rs` |
  | P5 | ~800 | `events.rs`, the mapper in `mod.rs` |
  | P6 | ~175 | [model_allowlist.rs#L45-L53](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/translate/model_allowlist.rs#L45-L53) |
  | P7 | 147 | `count_tokens.rs` |

  Per-function end lines were not traced individually.
- **Coupling.** The translation core (P2–P4, P6) uses only serde/serde_json and base64. P7 uses tiktoken-rs (`use tiktoken_rs::o200k_base_singleton;`, [count_tokens.rs#L1-L5](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/count_tokens.rs#L1-L5)), so stdlib Python must replace it with an estimate. axum, reqwest and tokio are confined to `client.rs`, `mod.rs` and the auth refresh and device paths. No keyring crate is used ([Cargo.toml#L10-L48](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/Cargo.toml#L10-L48)).
- **No models fetch.** It never fetches `/backend-api/codex/models`; the only `models` route is its own `/v1/models` ([server.rs#L268](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/server.rs#L268-L268)).
- **Fixtures.** None are recorded. `tests/fixtures/` holds two small hand-written files ([sse-basic.txt](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/tests/fixtures/sse-basic.txt#L1-L12), [anthropic-message.json](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/tests/fixtures/anthropic-message.json#L1-L11)). Streams are inline, hand-written test strings ([smoke_cutover.rs#L303-L309](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/tests/smoke_cutover.rs#L303-L309)). One usage-limit shape is commented as "recorded from a live turn" ([events.rs#L583](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/src/providers/codex/events.rs#L583-L583)).
- **Integration tests.** 12 files, 10,747 lines. `tests/smoke_cutover.rs` (3,410 lines, 40 tests: retries, usage limits, empty and incomplete completions) and `tests/server.rs` (1,578 lines, 41 tests) are the best case lists to re-express as Python tests.
- **Verdict:** distill P2, P4 (live path), the P5 mapper, the P6 Lite-lane shaping and the P7 counting rules. Skip WebSocket, continuation, compaction and search. Replace its P3 with Codex's rules.

### 2. openai/codex (Rust, Apache-2.0, @ `c63929af`)

- **License and NOTICE.** Apache-2.0 ([LICENSE](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/LICENSE#L1-L3)). The root NOTICE is 6 lines: "OpenAI Codex / Copyright 2025 OpenAI", plus a Ratatui credit that covers only the terminal UI ([NOTICE#L1-L6](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/NOTICE#L1-L6)).
- **P3, the sanitizer.** `sanitize_json_schema` is at [json_schema.rs#L71-L197](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/tools/src/json_schema.rs#L71-L197). Its rules:
  - a boolean schema becomes `{"type":"string"}`;
  - `const` becomes a one-value `enum`;
  - a missing type is inferred (object, array, string, number), and a schema that cannot be inferred becomes `{}`;
  - unused `$defs` are pruned;
  - schemas over 5,000 bytes are compacted ([compaction.rs#L14-L38](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/tools/src/json_schema/compaction.rs#L14-L38));
  - tools are sent with `strict: false` ([responses_api.rs#L168](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/tools/src/responses_api.rs#L168-L168)).

  It has no rewrite for `pattern`, `$schema` or name length. It ports to about 80 lines of Python. A sanitizer test set exists: `tools/tests/fixtures/json_schema_policy/`, 6 real MCP schemas (43,579 bytes).
- **P5, classification.**
  - Inside the stream: `parse_failed_response` reads `response.failed` codes such as context_length_exceeded, usage_not_included, invalid_prompt, server_is_overloaded and `rate_limit_exceeded|slow_down`; any other code is retryable ([responses_error.rs#L26-L143](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/codex-api/src/sse/responses_error.rs#L26-L143)).
  - On HTTP status: 429 `usage_limit_reached` and the 503 overload cases are handled in [api_bridge.rs#L91-L256](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/codex-api/src/api_bridge.rs#L91-L256).
  - Codex itself never handles `invalid_encrypted_content` (grep found nothing).
- **P5, retries.**
  - HTTP: 4 attempts, 200 ms base, doubling with ±10% jitter; a Retry-After header wins ([retry.rs#L8-L52](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/codex-client/src/retry.rs#L8-L52)). A 429 is not retried ([lib.rs#L454-L460](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/model-provider-info/src/lib.rs#L454-L460)).
  - Stream: 5 retries ([lib.rs#L66-L80](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/model-provider-info/src/lib.rs#L66-L80)). Quota, usage-limit, context, invalid-prompt and policy errors are never retried ([error.rs#L393-L446](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/protocol/src/error.rs#L393-L446)).
- **P1, login.**
  - Device code: 246 lines. It polls `deviceauth/token` at the server's interval, treats 403/404 as pending, and gives up after 15 minutes ([device_code_auth.rs#L19-L147](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/login/src/device_code_auth.rs#L19-L147)).
  - Browser: a PKCE server on port 1455 with fallback to 1457 ([server.rs#L77-L80](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/login/src/server.rs#L77-L80)). A state mismatch returns 400 ([#L364-L389](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/login/src/server.rs#L364-L389)).
  - Refresh: a JSON body ([oauth/client.rs#L15-L126](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/login/src/oauth/client.rs#L15-L126)). `refresh_token_reused` is treated as permanent ([manager.rs#L1625-L1730](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/login/src/auth/manager.rs#L1625-L1730)).
  - Account id: from the ID-token claim ([token_data.rs#L71-L117](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/login/src/token_data.rs#L71-L117)).
- **P4, SSE parser.** `responses.rs` is 2,035 lines: 1–651 production, the rest tests ([#L652-L657](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/codex-api/src/sse/responses.rs#L652-L657)). `process_responses_event` is a plain match on event type ([#L1-L36](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/codex-api/src/sse/responses.rs#L1-L36)).
- **P7.** `APPROX_BYTES_PER_TOKEN = 4`, rounded up ([truncate.rs#L101-L114](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/utils/string/src/truncate.rs#L101-L114)).
- **P6.** The shipped `models-manager/models.json` (472,072 bytes, 11 models) carries `use_responses_lite`, `context_window` and `service_tiers`, where `priority` means "Fast" ([models.json#L1-L40](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/models-manager/models.json#L1-L40), [#L162-L174](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/models-manager/models.json#L162-L174)). Its field names are a schema for parsing `/codex/models`; the file itself would only be an offline fallback.
- **Fixtures.** No `.sse` files exist. Streams are built from about 25 hand-written `ev_*` helpers ([core/tests/common/responses.rs#L752-L791](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/core/tests/common/responses.rs#L752-L791)). A raw `response.failed` JSON is inlined in a test ([responses.rs#L1137](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/codex-api/src/sse/responses.rs#L1137-L1137)).
- **Verdict:** distill P3 whole (about 80 Python lines), the P5 classification tables and retry, the P1 device and browser flows, P7, and the P6 field names. It provides no P2 or P4 Anthropic mapping.

### 3. anomalyco/opencode (TypeScript, MIT, @ `38840623`)

- **Canonical home and license.** `sst/opencode` and `anomalyco/opencode` resolve to the same HEAD. The license is MIT, "Copyright (c) 2025 opencode" ([LICENSE](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/LICENSE#L1-L3)).
- **The ChatGPT path is built in.** It is opencode's own `CodexAuthPlugin`, registered directly with no external plugin package ([plugin/index.ts#L66-L73](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/index.ts#L66-L73)). `plugin/openai/` holds `codex.ts` (575 lines), `ws.ts` (383), `ws-pool.ts` (271) and a README.
- **P1, login, all in `codex.ts`.**
  - Browser PKCE uses client_id `app_EMoamEEZ73f0CkXaXp7hrann`, redirect `localhost:1455/auth/callback`, `codex_cli_simplified_flow` and `originator=opencode` ([L88-L102](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L88-L102)). The callback's state is checked ([L196-L213](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L196-L213)).
  - Device code polls at the server's interval + 3 s, with 403/404 meaning pending ([L470-L549](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L470-L549)).
  - Refresh runs on expiry only, with one refresh in flight, and saves the rotated refresh_token ([L341-L397](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L341-L397)).
  - The account id tries `chatgpt_account_id`, then the namespaced claim, then `organizations[0].id` ([L38-L86](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L38-L86)).
  - Imports are plugin types, node `http`, WebCrypto and opencode's auth store ([L1-L8](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L1-L8)). It ports directly to stdlib Python.
- **Fetch rewrite.**
  - The custom fetch sets Bearer and `ChatGPT-Account-Id`, rewrites `/v1/responses` to the Codex URL, and leaves the body alone ([L348-L436](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L348-L436)).
  - A header hook adds `originator: opencode`, the User-Agent and `session-id` ([L559-L573](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L559-L573)).
  - With ChatGPT login, `instructions` is opencode's joined system prompt ([request.ts#L99-L112](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/session/llm/request.ts#L99-L112)).
  - `store=false` is set at [transform.ts#L1234-L1243](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/provider/transform.ts#L1234-L1243), and `promptCacheKey = sessionID` at [#L1323-L1336](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/provider/transform.ts#L1323-L1336).
  - For gpt-5*, `include ["reasoning.encrypted_content"]`, summary `auto` and verbosity `low` are set ([#L1353-L1385](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/provider/transform.ts#L1353-L1385)).
- **Reasoning round-trip.** The default runtime serializes through `@ai-sdk/openai` 3.0.88, which is not in the clone ([package.json#L70](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/package.json#L70-L70)). `itemId` is stripped when store is not true ([transform.ts#L501-L515](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/provider/transform.ts#L501-L515)). The opt-in native runtime is opencode's own `openai-responses.ts` (1,022 lines, built on Effect):
  - it captures `reasoningEncryptedContent` ([#L641-L642](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/llm/src/protocols/openai-responses.ts#L641-L642));
  - it replays `{type:"reasoning", summary, encrypted_content}` without an id ([#L385-L409](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/llm/src/protocols/openai-responses.ts#L385-L409)).
- **P3.** `sanitizeOpenAISchema` is a key allow-list. It drops `pattern`, `$schema` and `format`, turns `const` into an `enum`, turns a boolean schema into a string, and infers a missing type. It has no imports (transform.ts L1491-1573; the digger's link for it pointed at `packages/provider/transform.ts`, a path the other links do not use). It is applied for `@ai-sdk/openai` ([#L1594-L1597](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/provider/transform.ts#L1594-L1597)). There is no name-length limit.
- **P5.**
  - `parseStreamError` maps `context_length_exceeded` to overflow and marks `insufficient_quota`, `usage_not_included` and `invalid_prompt` non-retryable. `usage_limit_reached` falls through as retryable ([error.ts#L102-L154](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/provider/error.ts#L102-L154)).
  - Retries: 5, starting at 2 s, doubling with up to 25% jitter, capped at 30 s, honoring retry-after ([retry.ts#L26-L83](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/session/retry.ts#L26-L83)).
  - A 401 does not trigger a refresh.
- **P6.** A static allow-list filter, with no `/codex/models` fetch and no Lite lane or `-fast` handling ([codex.ts#L290-L326](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/openai/codex.ts#L290-L326)).
- **P7.** Characters ÷ 4 ([core/util/token.ts](https://github.com/anomalyco/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/core/src/util/token.ts#L1-L5)).
- **Fixtures, the best found anywhere.** `packages/llm/test/fixtures/recordings/openai-responses/` holds recorded Responses SSE streams of 28–58 lines each, including reasoning and reasoning-continuation recordings, with Anthropic-messages recordings in the same tree. The tests are `codex.test.ts` (506 lines: JWT, account id, model filter, shared refresh) and `openai-responses.test.ts` (1,472). These paths come from the `tracer-rr` result file, without per-file links.
- **Verdict:** distill P1 from `codex.ts` (the cleanest browser + device + single-flight refresh), P3's `sanitizeOpenAISchema` (merged with Codex's rules) and the P5 tables (fixing `usage_limit_reached`). Take the recorded SSE fixtures, after checking them for account data. Skip P2/P4: they target opencode's own event model, not Anthropic, and are Effect-coupled.

### 4. BerriAI/litellm (Python, MIT, @ `63a4f3f2`)

- **License.** The root LICENSE carves out only the top-level `enterprise/`; everything else is MIT, "Copyright (c) 2023 Berri AI" ([LICENSE#L1-L8](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/LICENSE#L1-L8)). Both target directories are MIT.
- **Sizes.**
  - `responses_adapters/`: 1,538 lines (transformation 737, streaming_iterator 495, handler 303).
  - `chatgpt/`: 1,151 lines (authenticator 411, common_utils 290, responses/transformation 260, plus chat files).
- **P1.** `Authenticator` (L55-411) is device-code only.
  - The device-token poll returns `code_verifier`, which is then exchanged ([authenticator.py#L233-L256](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/authenticator.py#L233-L256)).
  - Refresh fires within 60 s of expiry and keeps the rotated token ([#L325-L379](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/authenticator.py#L325-L379)). There is no lock, no atomic write and no chmod.
  - The account id comes from the namespaced claim ([#L130-L175](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/authenticator.py#L130-L175)).
- **P2.** `translate_request` is L498-626. The ChatGPT wire shaping is at [chatgpt/responses/transformation.py#L71-L118](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/responses/transformation.py#L71-L118), whose allow-list drops `prompt_cache_key`, which our design needs.
- **P3.** None: `translate_tools_to_responses_api` passes the schema through ([#L386-L412](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L386-L412)).
- **P4.** `_process_event` is L210-432.
  - A reasoning item with no summary becomes `redacted_thinking`, not a signature-only `thinking` block ([streaming_iterator.py#L182-L208](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/streaming_iterator.py#L182-L208)).
  - Tool arguments stream as deltas ([#L333-L347](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/streaming_iterator.py#L333-L347)).
  - stop_reason is set at [#L370-L432](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/streaming_iterator.py#L370-L432).
  - Cached tokens go to `cache_read_input_tokens` ([adapters/transformation.py#L1505-L1527](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/adapters/transformation.py#L1505-L1527)).
  - The non-stream path is `translate_response` ([L627-L737](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/transformation.py#L627-L737)).
- **P5.** `usage_limit_reached` is absent from `litellm/` and falls to 500. `context_length_exceeded` becomes 400 ([litellm/responses/streaming_iterator.py#L195-L220](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/responses/streaming_iterator.py#L195-L220)). There are no retries and no refresh on a 401.
- **P6.** A static price-file catalog. Service tier only, via `"fast": "priority"` ([L39](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/responses/transformation.py#L39-L39)). **P7:** absent.
- **Coupling.** Every file imports litellm internals; httpx and pydantic appear in a few ([authenticator.py#L1-L16](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/chatgpt/authenticator.py#L1-L16), [streaming_iterator.py#L1-L34](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/litellm/llms/anthropic/pass_through/responses_adapters/streaming_iterator.py#L1-L34)). The `tracer-rr` judged that P1 (about 300 lines) ports almost 1:1 to urllib, while P2/P4/P5 (about 1,100 lines) need 25–35% of their lines rewritten.
- **Fixtures.** 3,771 unit-test lines plus 775 integration lines, with streams built inline. The closest golden stream is the fake Codex backend's `_frames` ([tests/integration/_support/codex_vendor.py#L154-L207](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/tests/integration/_support/codex_vendor.py#L154-L207)).
- **Verdict:** distill the Python shape of P2/P4 and the P1 device flow. Fix what it lacks: a signature-only thinking block, `prompt_cache_key`, the schema sanitizer, `usage_limit_reached` → 429, retries, and a refresh lock.

### 5. luiapidev/gpt-proxy (TypeScript, MIT, @ `86396f1f`)

- **History and license.** One commit (2026-04-02), MIT, "Copyright (c) 2026 rayice" ([LICENSE#L1-L3](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/LICENSE#L1-L3)). The README credits caozhiyuan/copilot-api for the translation layers ([README.md#L314](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/README.md#L314-L314)).
- **Size.** 4,670 src lines and no tests ([package.json#L12-L17](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/package.json#L12-L17)).
- **Reasoning.** It does round-trip encrypted reasoning. The signature is `encrypted_content + "@" + id` ([responses-translation.ts#L518-L528](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/src/routes/messages/responses-translation.ts#L518-L528)), and replay splits it at the last `@` ([#L219-L238](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/src/routes/messages/responses-translation.ts#L219-L238)).
- **Cache key.** A UUID-shaped hash of the session ([handler.ts#L69-L90](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/src/routes/messages/handler.ts#L69-L90)).
- **Streaming.** Live ([#L92-L160](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/src/routes/messages/handler.ts#L92-L160)).
- **Weak pieces.** P5 passes errors through in a non-Anthropic shape with no retries ([error.ts#L15-L56](https://github.com/luiapidev/gpt-proxy/blob/86396f1fc960b00c02315e1f3b7a970d0ebd48fc/src/lib/error.ts#L15-L56)). P7 is absent. The browser login uses `Bun.serve`.
- **Verdict:** reference only, with no tests to trust. raine covers the same ground with tests.

### Skipped

- **insightflo/chatgpt-codex-proxy** @ `11b0a3c7`:
  - No license. The repo tracks no LICENSE file, package.json is `"private": true` with no license field, and the only license text is a README line ([package.json#L1-L8](https://github.com/insightflo/chatgpt-codex-proxy/blob/11b0a3c7b080694c5eaf07f8926d0dabfbb6fc95/package.json#L1-L8), [README.md#L313-L315](https://github.com/insightflo/chatgpt-codex-proxy/blob/11b0a3c7b080694c5eaf07f8926d0dabfbb6fc95/README.md#L313-L315)).
  - No `include` and no `prompt_cache_key` ([request.ts#L69-L80](https://github.com/insightflo/chatgpt-codex-proxy/blob/11b0a3c7b080694c5eaf07f8926d0dabfbb6fc95/src/transformers/request.ts#L69-L80)).
  - Thinking is dropped ([request.ts#L339-L386](https://github.com/insightflo/chatgpt-codex-proxy/blob/11b0a3c7b080694c5eaf07f8926d0dabfbb6fc95/src/transformers/request.ts#L339-L386)).
  - The stream is fully buffered ([messages.ts#L101-L102](https://github.com/insightflo/chatgpt-codex-proxy/blob/11b0a3c7b080694c5eaf07f8926d0dabfbb6fc95/src/routes/messages.ts#L101-L102)).
  - History over 50 messages is cut to 20 ([request.ts#L438-L454](https://github.com/insightflo/chatgpt-codex-proxy/blob/11b0a3c7b080694c5eaf07f8926d0dabfbb6fc95/src/transformers/request.ts#L438-L454)).
- **caixiaoshun/claudex:** "GPL-3.0 license", 11 stars. It "forwards them to `chatgpt.com/backend-api/codex/responses`" ([repo](https://github.com/caixiaoshun/claudex)). Copyleft, so distilling it would carry GPL obligations.
- **icebear0828/codex-proxy:** 1.8k stars, serves `/v1/messages`. Its license is "本项目采用 **非商业许可 (Non-Commercial)**" ([repo](https://github.com/icebear0828/codex-proxy)).
- **RayBytes/ChatMock:** an "OpenAI & Ollama compatible API powered by Codex", MIT, 1.6k stars. No Anthropic endpoint ([repo](https://github.com/RayBytes/ChatMock)).
- **Soju06/codex-lb:** MIT, 3.3k stars. Only OpenAI-compatible endpoints are documented ([repo](https://github.com/Soju06/codex-lb)).

## Per-piece pick

| Piece | Primary source to distill | Second source | Why |
| --- | --- | --- | --- |
| P1 OAuth | opencode `codex.ts` (browser + device + single-flight refresh) | openai/codex `login/` (15-min device limit, 1455→1457 fallback, `refresh_token_reused` permanent) | opencode has the cleanest single-file flow. Neither LiteLLM (no lock) nor raine (Rust/tokio) beats it. |
| P2 request | raine `request.rs`/`translate_shared.rs`/`reasoning_signature.rs` (~1.35k lines) | LiteLLM `translate_request` (Python shape) | raine is complete on images, tool_result errors, the Lite lane and the signature codec. LiteLLM drops `prompt_cache_key`. |
| P3 schema | openai/codex `sanitize_json_schema` | opencode `sanitizeOpenAISchema` (drops `pattern`, `$schema`, `format`) | Neither alone covers the design's three rule groups, but the two together do. |
| P4 stream | raine live path (~2.4k lines) | LiteLLM `_process_event` | raine emits signature-only thinking blocks; LiteLLM emits `redacted_thinking` instead. |
| P5 errors | openai/codex `responses_error.rs` + `api_bridge.rs` + `retry.rs`; raine's Anthropic mapper (~800 lines) | — | opencode and LiteLLM both mishandle `usage_limit_reached`. |
| P6 models | openai/codex `models.json` field schema | raine `model_allowlist.rs` (Lite set, `-fast`) | No candidate fetches `/codex/models`. |
| P7 tokens | openai/codex bytes ÷ 4 / opencode chars ÷ 4 | raine's per-image and per-reasoning rules | raine's o200k needs tiktoken, which is not stdlib. |

## Fixtures

- **Recorded Responses SSE:** only opencode's `packages/llm/test/fixtures/recordings/openai-responses/` (28–58 lines each, including reasoning continuation). These come from the public OpenAI API, so whether they match the chatgpt.com backend's event set is unverified.
- **Hand-written streams:**
  - Codex `ev_*` helpers ([responses.rs#L752-L791](https://github.com/openai/codex/blob/c63929af50518ca638ccad12f2805298cb7f25a1/codex-rs/core/tests/common/responses.rs#L752-L791));
  - LiteLLM `_frames` ([codex_vendor.py#L154-L207](https://github.com/BerriAI/litellm/blob/63a4f3f2f3334bbd239c4c3e4c3302b63b02f71d/tests/integration/_support/codex_vendor.py#L154-L207));
  - raine's inline strings ([smoke_cutover.rs#L303-L309](https://github.com/raine/claude-code-proxy/blob/4b5c058a3c900031d2b2e094a265a5ade7acad3a/tests/smoke_cutover.rs#L303-L309)) and its 40 + 41 error and ingress test cases.
- **Schema test inputs:** Codex `tools/tests/fixtures/json_schema_policy/` (6 real MCP schemas).
- **Model catalog sample:** Codex `models-manager/models.json`.
- **Golden Anthropic SSE or captured Claude Code bodies:** none in any candidate. Build plan step 3's own captures remain the only source.

## License attribution for distilled code

- **MIT** (raine, opencode, LiteLLM, luiapidev): "The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software" ([opensource.org](https://opensource.org/license/mit)). Keep each holder's line:
  - "Copyright (c) 2026 Raine Virta"
  - "Copyright (c) 2025 opencode"
  - "Copyright (c) 2023 Berri AI"
  - "Copyright (c) 2026 rayice"
- **Apache-2.0** (openai/codex), per [apache.org](https://www.apache.org/licenses/LICENSE-2.0):
  - "You must give any other recipients of the Work or Derivative Works a copy of this License";
  - "You must cause any modified files to carry prominent notices stating that You changed the files";
  - retain "all copyright, patent, trademark, and attribution notices";
  - "must include a readable copy of the attribution notices contained within such NOTICE file". For Codex that is "OpenAI Codex / Copyright 2025 OpenAI"; the Ratatui credit concerns code we do not carry.
- **GPL-3.0** (claudex) and **non-commercial** (icebear0828): not distillable here. **No license file** (insightflo): no grant to rely on.

## Coverage

- opencode native ChatGPT path: settled (tracer-rr/anomalyco-opencode-2026-10-09.md)
- LiteLLM adapter + chatgpt provider: settled (tracer-rr/BerriAI-litellm-2026-10-09-2.md)
- raine/claude-code-proxy distillation data: settled (tracer-rr/raine-claude-code-proxy-2026-10-09-3.md)
- Other Anthropic↔Codex adapters: settled in round 2 (sub-rr round 1; tracer-rr/insightflo-chatgpt-codex-proxy-2026-10-09.md, tracer-rr/luiapidev-gpt-proxy-2026-10-09.md)
- openai/codex reference and fixtures (added): settled (tracer-rr/openai-codex-2026-10-09-6.md)

Digging ended: every sub-area settled.

## Verification

- 16 web facts were checked on 6 pages: MIT text (1), Apache-2.0 §4 (4), claudex (4), icebear0828 (3), ChatMock (4), codex-lb (3 of 4).
- Confirmed by a quote: all except one.
- NOT ON PAGE: "codex-lb is written in Python". The page's Languages section is empty and it lists Rust files, so the claim was removed.
- UNCHECKED: none.
- Code facts come from pinned clones read by `tracer-rr` and were not re-fetched.

## Open rabbit holes

- caozhiyuan/copilot-api, credited by luiapidev as the source of its Anthropic↔Responses layers. It may be a more mature, tested TS translator (undug).
- vercel/ai `packages/openai` v3.0.88: opencode's default runtime serializes reasoning replay there (undug).
- Whether opencode's recorded `openai-responses` fixtures (public API) match the chatgpt.com Codex backend's event set (undug).
- caixiaoshun/claudex `src/converter.ts` lines 1001–1942, the unread stream converter. Moot for distillation under GPL-3.0 (dug, unsettled).
- Yimura/claude-code-proxy (Python, no license): its Codex adapter source is unread (undug).
- The selector between raine's live and buffered P4 paths (`mod.rs` L442-450) was not read (dug, unsettled).
- Stars and last-commit dates for wowyuarm/codex-proxy, sgeraldes/claude2openai and ben0oil1/codex2api were reported only by the digger and not verified. Not in the map (dug, unsettled).
- Whether the MIT "substantial portions" threshold attaches to model-written distillations (undug; legal opinion out of scope).
