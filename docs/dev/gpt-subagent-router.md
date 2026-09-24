# GPT sub-agents behind a model-fork router

**Status:** PARKED — research and design, nothing built. Wire facts measured 2026-09-22 on Claude Code 2.1.280 and codex-cli 0.155.1 against a loopback fake Anthropic server (§ Reproducing the probes); claude-code-proxy facts from its published docs (`github.com/raine/claude-code-proxy`, MIT, Rust). Resume at § Open questions, then § Build plan.

## Contents

- [The finding](#the-finding)
- [Goal and boundary](#goal-and-boundary)
- [Wire facts](#wire-facts)
- [claude-code-proxy](#claude-code-proxy)
- [Design](#design)
  - [Topology](#topology)
  - [Router contract](#router-contract)
  - [GPT agents live on the launch line](#gpt-agents-live-on-the-launch-line)
  - [Spawning a GPT agent](#spawning-a-gpt-agent)
- [Costs and risks](#costs-and-risks)
- [Alternatives](#alternatives)
- [Open questions](#open-questions)
- [Build plan](#build-plan)
- [Reproducing the probes](#reproducing-the-probes)

## The finding

A sub-agent's model travels in its **definition**, and Claude Code puts whatever ID the definition names on the wire, unvalidated: `model: gpt-5.6-sol` leaves as `"model": "gpt-5.6-sol"` while the main thread's requests leave as `claude-opus-5-5`. A router in front of Claude Code can therefore fork per request on the `model` field — `claude-*` to Anthropic untouched, everything else to a GPT translator. The four-name limit (`sonnet|opus|haiku|fable`) binds only the `Agent` tool's per-spawn `model` override, which is enum-validated and overrides the definition, so a GPT agent is spawned by **name**, with no `model`. claude-code-proxy alone cannot do the fork: it has no Anthropic upstream and maps every Claude ID to a GPT tier.

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

## claude-code-proxy

A local Anthropic-compatible proxy, default listener `127.0.0.1:18765`, translating Claude Code traffic for subscription-backed providers: Codex (ChatGPT Plus/Pro OAuth, `gpt-*` IDs), Kimi, Grok, OpenCode Go, Cursor.

- Routes per request by model ID; an unknown ID returns HTTP 400 with the catalog.
- **No Anthropic upstream.** `claude-*` IDs and the `haiku|sonnet|opus|fable` aliases route to its `aliasProvider` (Codex by default: Haiku → Luna, Sonnet → Terra, Opus → Sol). Pointed at directly, it turns the whole session GPT.
- Owns its provider credentials (its own login, not Codex CLI's); an incoming `ANTHROPIC_AUTH_TOKEN` is accepted and ignored.
- The listener is unauthenticated — loopback only.
- Its README: "Unofficial clients may carry account risk."
- Open issue #117 asks for a per-request routes list; no issue asks for an Anthropic passthrough.

Adjacent facts: codex-cli 0.155.1 has no `mcp-server` subcommand, so Codex as an MCP tool is closed. claude-code-router (`github.com/musistudio/claude-code-router`) speaks Anthropic Messages as a provider; its README names no Claude-subscription passthrough, and it was not evaluated further.

## Design

### Topology

```text
claude  (ANTHROPIC_BASE_URL=http://127.0.0.1:{router-port})
  └─ model-fork router
       ├─ model starts with "claude-" → https://api.anthropic.com  (request untouched: subscription OAuth)
       └─ any other model             → claude-code-proxy :18765   (authorization stripped → ChatGPT)
```

### Router contract

- Listens on loopback only.
- `POST /v1/messages` and `POST /v1/messages/count_tokens`: read the JSON body's `model`, forward the original bytes. `claude-` prefix → Anthropic with every header intact; any other model → claude-code-proxy with `authorization` and `x-api-key` removed.
- Every other path (`HEAD /api/hello`, …) → Anthropic.
- Streams SSE back unbuffered.
- An upstream that refuses or is unreachable returns an Anthropic-shaped error naming **which** upstream failed — never a hang, never an empty stream.
- One stateless process serves every router session.

### GPT agents live on the launch line

A GPT agent exists only in a session started through the router: pfm's launch line sets `ANTHROPIC_BASE_URL` and passes `--agents` with the GPT definitions (W10). Each definition is compiled at launch from the Claude file it twins — prompt body from the file, `model` swapped — so no hand-kept copy drifts.

- General workers: `sol`, `terra`, `luna` (`gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`).
- Fleet roles: a same-named GPT twin (for example `flights-mechanical-executor`) replaces the file definition in that session only.
- Not a file: a GPT-pinned `.claude/agents/*.md` loads in every session, and in one without the router its request reaches Anthropic as `gpt-*` and fails.
- Not an alias remap: W2 and W8.

### Spawning a GPT agent

`Agent(subagent_type: "sol", description: …, prompt: …)` — no `model`. A spawn-time `model` silently overrides the GPT definition back to Claude (W9).

- `flights-orchestrator` already spawns executors with "no model override" (`templates/global/agents/flights-orchestrator.md`, the executor spawn step), so a GPT executor twin needs no orchestrator change.
- The fleet prompt names a tier alias at each spawn site (`pfm/harness-prompts/claude/professor.md`, § Claude Code: "The tiers, by model alias, named inline at each spawn site"). A GPT-pinned agent needs an exception there: it is spawned with no `model`.

## Costs and risks

- **Two local processes in every request path.** Router or proxy down → the session's main thread is down. Only router-launched sessions carry this; global settings never point at the router.
- **ChatGPT account risk** from an unofficial client — the user's call.
- **Claude token exposure** (W4) — closed by the router stripping the token on the GPT branch.
- **Auto-mode classifier billing** — ineligible under a custom base URL; moot in bypass-mode seats.
- **Harness fit, unmeasured** — GPT gets Claude Code's tool schemas and fleet prompts written for Claude; its native harness is Codex's. Open question 1.
- **Third-party binary** — installing claude-code-proxy falls under "never install unvalidated libraries": validate before install.

## Alternatives

- **Cross-harness seat, exists today.** `/flights:orchestrate-cross-harness {dir} engine codex` runs a task file on GPT in its native Codex harness — a chat seat briefed by inject, not an `Agent` call.
- **`codex exec` wrapper agent.** A thin Claude sub-agent runs `codex exec` and returns its output verbatim — GPT in its own harness, no traffic routing, a Claude call around every GPT call.
- **Whole-session GPT.** claude-code-proxy alone — loses the Claude main thread.

## Open questions

1. **Harness fit** — the gating measurement: one flight task file run by the Sonnet executor and by its GPT twin; compare the diff, the tests, calls and tokens.
2. **Router home** — a pfm verb (Go, `httputil.ReverseProxy`) or a standalone script.
3. **Launch-line composition** — which fleet roles get GPT twins, and how pfm names a GPT-enabled seat.
4. **Fleet-prompt exception** — the wording for "a GPT-pinned agent is spawned with no `model`".
5. **Anthropic's terms** for subscription traffic through a local passthrough gateway — not checked.
6. **`CLAUDE_CODE_SUBAGENT_MODEL` vs a definition's `model`** — which wins is unprobed.
7. **Interactive sessions** — every probe ran headless (`-p`).

## Build plan

1. Validate claude-code-proxy (source, release checksums); install it and sign it in to ChatGPT — user approval for both.
2. Router spike under `/tmp/professor/`, wire-checked with the probe method below.
3. `sol` through `--agents` in one router session; one real task.
4. Answer open question 1.
5. Productize through `/flights:spec` (pfm launch line, router, fleet-prompt exception), or drop.

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
