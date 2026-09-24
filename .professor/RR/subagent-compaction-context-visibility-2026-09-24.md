# RR — Claude Code: sub-agent self-compaction and reading/surfacing sub-agent context size (Sept 2026)

Question: map what Claude Code (Anthropic's CLI, current as of Sept 2026) offers for (A) a SUB-AGENT (Task/Agent-tool spawned agent) compacting its own context, and (B) reading a sub-agent's context size and surfacing it (statusline or elsewhere).

**Answer:** Subagents auto-compact on the same threshold as the main thread, and `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` is documented to apply to both. No source documents a way for a subagent to trigger compaction on demand. The compaction hooks do fire for a subagent, but without its identity (open bug). The main statusline JSON covers only the main thread. A separate `subagentStatusLine` setting reportedly gets a per-task `tokenCount` and `contextWindowSize` (digger-quoted, not re-verified). Per-subagent tokens are also available from the Agent tool result's `usage`/`totalTokens`, from OTel `query_source`/`agent.name` attributes, and from subagent transcript files.

## Map

### A1. Does a subagent auto-compact? YES
- [env-vars](https://code.claude.com/docs/en/env-vars), on `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`: "Applies to both main conversations and subagents". The page documents no env var that disables auto-compaction.
- [Issue #16944](https://github.com/anthropics/claude-code/issues/16944) (v2.1.1, closed): a subagent transcript carries `"subtype": "compact_boundary"` with `"compactMetadata": {"trigger": "auto", "preTokens": 167189}` and `isSidechain: true`, `agentId`. The reporter says this is undocumented.
- The [sub-agents doc](https://code.claude.com/docs/en/sub-agents) says nothing about compaction in the part fetched; the page was truncated before its resume section. The CHANGELOG has no line announcing subagent auto-compaction (per digger).

### A2. Can a subagent trigger compaction itself? UNSETTLED (the docs are silent)
- None of these documents a subagent-side `/compact`, a compaction tool, or an SDK option to compact a running subagent: the Agent SDK subagents page ([agent-sdk/subagents](https://code.claude.com/docs/en/agent-sdk/subagents)) (digger-quoted: "See resume subagents in Claude Code for compaction behavior"), the env-vars page, or the hooks docs.
- A search snippet mentions the Messages API server-side `compact_20260112` / `clear_tool_uses_20250919`. It was not fetched, and nothing ties it to Claude Code subagents, so it goes to the open rabbit holes.
- The only control is indirect: `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` (shared with the parent; no per-agent threshold found). A PreCompact hook can block compaction, but it cannot target a single subagent (see A3).

### A3. Hooks for subagent compaction: PARTIAL (they fire, but without the subagent's identity)
- [Issue #91910](https://github.com/anthropics/claude-code/issues/91910) (open, v2.1.259): "When a background subagent (Agent tool) auto-compacts, the `PreCompact` hook and the `SessionStart` hook with `matcher: compact` both run. The payload carries the parent session's `session_id`, `transcript_path` and `cwd`, and nothing that identifies the subagent: no `agent_id`, no `agent_type`, no `agent_transcript_path`…"
- Same issue: "`PostCompact` fires roughly 35 ms after `SessionStart(compact)` for a subagent's compaction, with the parent's fields plus `compact_summary`, and no agent identity." The issue also reports phantom `SubagentStop` events from internal summarizer calls (per digger).
- [hooks doc](https://code.claude.com/docs/en/hooks): PreCompact/PostCompact matchers are `manual` / `auto`. `agent_id` and `agent_type` are common fields when a hook fires inside a subagent. SubagentStop carries `agent_id`, `agent_type`, `agent_transcript_path` and `last_assistant_message` (fetch-model rendering; `unquoted`).
- [agent-sdk/hooks](https://code.claude.com/docs/en/agent-sdk/hooks) (digger-quoted): "`agent_id` and `agent_type` are populated when the hook fires inside a subagent." In Python they are required on SubagentStart/Stop, and PreCompact is not in the list of events that carry them.

### B1. Statusline JSON: PARTIAL (the main fields cover the main thread only; a separate subagent row hook exists)
- [statusline](https://code.claude.com/docs/en/statusline) (digger-quoted; my verification fetch overflowed, so UNCHECKED): "The subagentStatusLine setting renders a custom row body for each subagent shown in the agent panel below the prompt." Its payload is "a tasks array. Each task has id, name, type, status, description, label, startTime, model, effort, contextWindowSize, tokenCount, tokenSamples, and cwd". Both `contextWindowSize` and `tokenCount` "require Claude Code v2.1.205 or later".
- Main `context_window` fields: `total_input_tokens`, `total_output_tokens`, `current_usage.{input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens}`, `used_percentage` ("calculated from input tokens only"). The digger did not find `exceeds_200k_tokens` on the page it fetched (`unquoted`).
- One community tool disputes the above: a B3 digger found that a gist it fetched lacked `subagentStatusLine`. The B1 digger quotes it from the official page. Unresolved until the statusline page is read directly.

### B2. Reading a running subagent's token usage: YES, several routes
- Agent tool result. [Issue #88107](https://github.com/anthropics/claude-code/issues/88107): Claude-native subagents return `usage` with the input/output/cache token split and `"totalTokens": 44927`. Custom-provider models return `"usage": {}` and `"totalTokens": null` (reproduced on 2.1.235 and 2.1.236).
- OpenTelemetry. [monitoring-usage](https://code.claude.com/docs/en/monitoring-usage): "`query_source`: Category of the subsystem that issued the request. One of `"main"`, `"subagent"`, or `"auxiliary"`". "`agent.name`: Subagent type that issued the request… Other user-defined agent names are replaced with `"custom"`." These attributes are on the cost and token counters. There is no metric for a subagent's remaining context.
- Transcript files at `~/.claude/projects/{project}/{sessionId}/subagents/agent-{agentId}.jsonl`, as cited in [#33945](https://github.com/anthropics/claude-code/issues/33945) (digger-quoted). Rows carry `isSidechain`/`agentId` (the #16944 JSON), and `compact_boundary.preTokens` marks the size at compaction. Not an officially documented schema.
- `/context` shows only the main session. [#10164](https://github.com/anthropics/claude-code/issues/10164) (closed as not planned, digger-quoted).

### B3. Community prior art
- ccstatusline [PR #441](https://github.com/sirmalloc/ccstatusline/pull/441): "subagent-inclusive session token accounting", with the option defaulting off. The PR is still OPEN, not merged.
- ccusage [issue #313](https://github.com/ryoppippi/ccusage/issues/313) (digger-quoted): it "did not capture tokens used by these sub-tasks".
- Feature requests on anthropics/claude-code: [#15677](https://github.com/anthropics/claude-code/issues/15677) (a `sub_agents` array in the statusline JSON), [#33945](https://github.com/anthropics/claude-code/issues/33945) (closed) and [#10164](https://github.com/anthropics/claude-code/issues/10164). All digger-quoted.

## Coverage
- A1 auto-compact: settled
- A2 self-trigger / SDK control: partial (docs silent)
- A3 hooks: settled
- B1 statusline fields: partial (the subagentStatusLine quote is unverified by my check)
- B2 usage routes: settled
- B3 community: settled

Digging ended: after round 1, by the lead's judgment. The remaining gaps (A2) were docs-silent, and no lead in the returned rabbit holes promised to settle them. This is not a formal stop condition.

## Verification
I checked 16 facts on 8 pages. Confirmed: env-vars (1), #91910 (2), monitoring-usage (2), #16944 (2), ccstatusline PR #441 (2, and it is not merged), #88107 (3).
- UNCHECKED — the output overflowed and was saved to a file I could not read: all six statusline facts, including `subagentStatusLine`, `tasks[].tokenCount/contextWindowSize`, v2.1.205, `used_percentage` and `exceeds_200k_tokens`.
- NOT ON PAGE: the claim that the sub-agents doc's resume section covers compaction. The fetch was truncated before that section, so this is closer to "not reached" than "absent".

## Open rabbit holes
- Read the `subagentStatusLine` section of code.claude.com/docs/en/statusline verbatim (and settings-reference). The key B1 fact is unverified, and one digger disputed it. (dug, unsettled)
- A subagent-initiated `/compact` or an SDK option to compact a running subagent (dug, unsettled; docs silent)
- Does the Messages API `compact_20260112` / `clear_tool_uses` apply through the Agent tool path? (undug)
- A per-agent autocompact threshold (undug)
- The definition of `tokenSamples` (undug)
- The sub-agents doc "resume subagents" section on compaction and `cleanupPeriodDays` (dug, unsettled; truncated)
- Status of ccusage issue #806 on the subagent token discrepancy (undug)
