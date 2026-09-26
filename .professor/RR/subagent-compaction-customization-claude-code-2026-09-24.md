# RR — Every way to customize auto-compaction for Claude Code sub-agents, ranked for a 100K–800K-token fleet on 1M-context models

Question: find every way to customize (auto-)compaction for SUB-AGENTS in Claude Code (Anthropic's CLI; installed here v2.1.281, Sept 2026) — when it triggers, what it keeps, how it summarizes, or replacing it — and rank them by how well they work for a fleet whose sub-agents run 100K–800K tokens on 1M-context models.

## Answer

Claude Code has no per-sub-agent compaction control. Every native lever is session-wide: the auto-compact window and percentage env vars, `DISABLE_COMPACT`, PreCompact hooks and CLAUDE.md compact instructions. Some are documented to reach sub-agents; the hooks reach them only in a form that cannot tell a sub-agent's compaction from the parent's. On 1M-native models the default trigger is about 967K, so a 100K–800K sub-agent normally never compacts. For a fleet, the controls that actually work are hand-rolled budgets: watch each task's tokens from outside, then checkpoint and respawn. They beat any compaction tuning. Next come lowering the global window, and running the agent in its own Agent SDK process with its own env.

## Ranking (for sub-agents at 100K–800K on 1M-context models)

1. **Hand-rolled token budget, checkpoint and respawn.** This is the only lever that works per agent and does not depend on buggy auto-compact. The parent reads each task's `tokenCount`/`contextWindowSize` from `subagentStatusLine`, which is UNCHECKED. The sub-agent writes a handoff file and returns early, and the parent respawns it or resumes it with that file. Community example: [pf-handoff](https://github.com/MILSPIL/pf-handoff). Cost: you build it yourself, and a sub-agent cannot see its own remaining budget (see E).
2. **A lower global auto-compact window (`CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`).** The percentage override is documented to reach sub-agents. But it is session-wide, it can only lower the trigger, and open issues report it failing on v2.1.220+ and for 1M agents (#82761, #91984). Treat it as a safety net, never a policy.
3. **An Agent SDK process per agent.** Run the heavy agent as its own `query()` with its own `env`, so it gets its own threshold. The docs quote the `env` passthrough, but no page shows it setting a compaction threshold, so that part is unverified. The SDK PreCompact callback can add context to the summary. There is still no per-`AgentDefinition` compaction field.
4. **PreCompact hook plus CLAUDE.md `# Compact instructions`.** These steer what a summary keeps, but for the whole session. A hook cannot tell which sub-agent is compacting (#91910). The docs never say whether a sub-agent's compaction reads compact instructions.
5. **API context editing and server-side compaction.** These are the strongest mechanisms (tool-result clearing, a custom summarization prompt), but Claude Code does not expose them. You get them only by building your own harness on the Messages API.
6. **Per-agent frontmatter.** Does not exist; the feature request (#90347) is open.

## Map

### A. Trigger threshold (Q1): PARTIAL, global only

- `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` reaches sub-agents: "Set the percentage (1-100) of the auto-compact window at which auto-compaction triggers." It can only lower the trigger: "the variable can't raise the threshold, so values above the default percentage are ignored." Scope: "Applies to both main conversations and subagents". It is a process-wide env var, so there is no per-agent setting. [env-vars](https://code.claude.com/docs/en/env-vars)
- Default on 1M-context models: "Models running with a native 1M window, such as Sonnet 5, the Fable models, and Opus 4.7 and later on the Anthropic API, compact before the window fills, at about 967K tokens by default." [model-config](https://code.claude.com/docs/en/model-config)
- `CLAUDE_CODE_AUTO_COMPACT_WINDOW`: "While it's set, it takes precedence over the command, the flag, and the setting, and `/autocompact` reports the override instead of changing the window." The page does not say whether sub-agents are in scope; the docs are silent. [model-config](https://code.claude.com/docs/en/model-config)
- `DISABLE_COMPACT` "disables all compaction" ([model-config](https://code.claude.com/docs/en/model-config)). It is global, not per agent. The env-vars page documents neither `DISABLE_AUTO_COMPACT` nor `DISABLE_COMPACT`. `DISABLE_AUTO_COMPACT=1` appears only as an issue workaround, in [#42375](https://github.com/anthropics/claude-code/issues/42375) (issue-only). `autoCompactEnabled` does not appear on the fetched env-vars or settings pages.
- Frontmatter and AgentDefinition have no compaction field. Issue [#90347](https://github.com/anthropics/claude-code/issues/90347) (open, "Claude Code v2.1.250") says: "Agent definition frontmatter (`.claude/agents/*.md`) supports `model`, `effort`, `maxTurns`, tool lists, etc., but has **no** context/compaction field (`autoCompactWindow`, `maxContextTokens`, or similar)." It asks to "Allow the auto-compact window to be scoped **per agent role**". The SDK's Python `AgentDefinition` has the fields description, prompt, tools, disallowedTools, model, skills, memory, mcpServers, initialPrompt, maxTurns, background, effort and permissionMode, none of them about compaction ([types.py](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/main/src/claude_agent_sdk/types.py)).
- Known threshold bugs (issue-only):
  - [#82761](https://github.com/anthropics/claude-code/issues/82761), open: "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE silently stopped taking effect after 2026-07-14 (v2.1.220)".
  - [#91984](https://github.com/anthropics/claude-code/issues/91984), open, "Claude Code 2.1.258/2.1.260": "Autocompact fires once then never again on opus[1m]; never fires for agent-team teammates". A teammate on opus-5[1m] peaked at "931K (93%) **0** — no `compact_boundary`", with the override set to 80.
  - Older reports: [#36381](https://github.com/anthropics/claude-code/issues/36381) (v2.1.79, override 55 ignored), [#34202](https://github.com/anthropics/claude-code/issues/34202) (v2.1.75), [#52390](https://github.com/anthropics/claude-code/issues/52390).
- Sub-agents do auto-compact. Issue-only evidence from [#16944](https://github.com/anthropics/claude-code/issues/16944) (v2.1.1, closed): a `compact_boundary` with `"trigger": "auto", "preTokens": 167189`.
- Changelog-only, v2.1.273/274: "Fixed the context meter and auto-compact counting advisor-tool turns at roughly twice their real context size, which made auto-compact fire at about half the real window." ([CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md)). The diggers gave two different version numbers for this line.

### B. What compaction keeps, and hooks (Q2): PARTIAL, global only, no agent identity

- Summary instructions. `/compact Focus on code samples and API usage` "tells Claude what to preserve during summarization", and "You can also customize compaction behavior in your CLAUDE.md file at the root of your project" with a `# Compact instructions` section ([costs](https://code.claude.com/docs/en/costs)). The docs say nothing on whether a sub-agent's auto-compaction reads these. Sub-agents load CLAUDE.md unless `omitClaudeMd` is set, so the section is probably in their context, but that is unverified.
- The hooks fire for sub-agents but cannot tell which one. [#91910](https://github.com/anthropics/claude-code/issues/91910) (open, "Observed on Claude Code 2.1.259"): "When a background subagent (Agent tool) auto-compacts, the `PreCompact` hook and the `SessionStart` hook with `matcher: compact` both run." Also: "The payload carries the parent session's `session_id`, `transcript_path` and `cwd`, and nothing that identifies the subagent: no `agent_id`, no `agent_type`, no `agent_transcript_path`". This conflicts with the docs' general rule for sub-agent hooks: "When running with `--agent` or inside a subagent, two additional fields are included: `agent_id` [and] `agent_type`" ([hooks](https://code.claude.com/docs/en/hooks.md)). The issue reports that this rule does not hold for compaction hooks. **DISPUTED**: docs vs issue.
- The same issue: "In one long session, 342 of the 381 distinct `agent_transcript_path` values a `SubagentStop` hook recorded were such phantoms" ([#91910](https://github.com/anthropics/claude-code/issues/91910)).
- What a PreCompact hook can change, in the SDK types: `PreCompactHookInput` carries `trigger: Literal["manual", "auto"]` and `custom_instructions: str | None`, and `PreCompactHookSpecificOutput` carries `additionalContext: NotRequired[str]` and no `customInstructions` field ([types.py](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/main/src/claude_agent_sdk/types.py)). The CLI docs' own wording on PreCompact output (`customInstructions`, `additionalContext`, `decision: "block"`, exit code 2 "Blocks compaction") failed the page check and is kept out of the map; see Verification.
- Issue-only: a sub-agent's compaction summary points at the parent's transcript ("read the full transcript at:" the PARENT session's file). [#96619](https://github.com/anthropics/claude-code/issues/96619), open, 2.1.281.

### C. Alternatives to summarization (Q3): NO, not exposed in Claude Code

- API context editing: "The API automatically clears the oldest tool results in chronological order. The API replaces each cleared result with placeholder text". Beta header `context-management-2025-06-27`. Defaults: `trigger` 100,000 input tokens, `keep` 3 tool uses. "the `clear_thinking_20251015` strategy must be listed first in the `edits` array." [context-editing](https://platform.claude.com/docs/en/build-with-claude/context-editing)
- API threshold compaction: strategy `compact_20260112`, header `compact-2026-01-12`. The trigger defaults to 150,000, and "`value` must be at least 50,000 tokens." `instructions`: "Custom summarization prompt. Completely replaces the default prompt when provided." With `pause_after_compaction`, "the API returns a message with the `compaction` stop reason after generating the compaction block". [compaction-threshold](https://platform.claude.com/docs/en/build-with-claude/compaction-threshold). A newer on-demand `compact-2026-09-04` exists ([compaction](https://platform.claude.com/docs/en/build-with-claude/compaction)).
- Memory tool: "pairs with context editing to manage long-running conversations" ([memory-tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool)).
- Claude Code exposes none of these through settings, env or frontmatter. Its internal microcompact and tool-result clearing is not user-configurable. Issue-only: [#7176](https://github.com/anthropics/claude-code/issues/7176) asked for an option to disable microcompact and was "Closed as not planned". [#42542](https://github.com/anthropics/claude-code/issues/42542) names the internal microcompact and session-memory-compact paths, which are server-flag gated. The costs docs do mention "clearing old tool results from context" as an internal rebuild cause ([costs](https://code.claude.com/docs/en/costs)).

### D. Agent SDK route (Q4): PARTIAL

- Neither `AgentDefinition` nor `ClaudeAgentOptions` has a compaction or context-management field ([types.py](https://raw.githubusercontent.com/anthropics/claude-agent-sdk-python/main/src/claude_agent_sdk/types.py)). The PreCompact callback exists (input `trigger`/`custom_instructions`, output `additionalContext`, as in B).
- Per-process env: TS `env` "replaces the subprocess environment instead of merging with `process.env`"; Python merges it ([agent-sdk/typescript](https://code.claude.com/docs/en/agent-sdk/typescript), [agent-sdk/subagents](https://code.claude.com/docs/en/agent-sdk/subagents)). So one `query()` per heavy agent can carry its own `CLAUDE_CODE_AUTO_COMPACT_WINDOW`/`PCT_OVERRIDE`. The idea is sound, but no page shows it used for compaction, so it is unverified.
- Issue-only:
  - [claude-agent-sdk-python#570](https://github.com/anthropics/claude-agent-sdk-python/issues/570) (Feb 13 2026, open) asks to pass `context_management` through and to expose `pause_after_compaction`. It states the SDK "does its own client-side auto-compact at ~95% context usage".
  - [claude-code#19686](https://github.com/anthropics/claude-code/issues/19686) (closed, not planned) reports that the documented `compaction_control` is missing from `ClaudeAgentOptions`.

### E. Hand-rolled patterns (Q5): YES, the most controllable

- `subagentStatusLine` gives each task, among other fields, `contextWindowSize`, `tokenCount` and `tokenSamples`, "v2.1.205 or later" ([statusline](https://code.claude.com/docs/en/statusline)). UNCHECKED, see Verification.
- Community pattern [pf-handoff](https://github.com/MILSPIL/pf-handoff):
  - Budget zones in a `context-budget.json` with thresholds `[50, 70, 85]`: "zone 1 — checkpoint, delegate big chunks; zone 2 — no new medium/large chunks; zone 3 — full handoff immediately".
  - A handoff file of at most 120 lines.
  - "A subagent's tool calls are measured against the _subagent's own_ transcript and window".
- A sub-agent cannot see its own budget. [#17033](https://github.com/anthropics/claude-code/issues/17033) (issue-only) asks for a "Context Budget Monitoring API". It says "there's no mechanism for Claude to detect this and trigger preventive actions."
- Resume: "Resumed subagents retain their full conversation history" ([sub-agents](https://code.claude.com/docs/en/sub-agents)). Resuming does not free context, so respawning with a handoff is the way to reset it.
- Changelog-only, v2.1.163 (via [#65495](https://github.com/anthropics/claude-code/issues/65495)): "Stop and SubagentStop hooks can now return `hookSpecificOutput.additionalContext`". The current docs wording was not verified.

## Coverage

- A. Trigger threshold: settled
- B. What compaction keeps, and hooks: partial. The CLI docs' PreCompact output wording failed the page check, and the docs are silent on compact instructions inside a sub-agent.
- C. API alternatives: settled
- D. Agent SDK route: settled. `compact_boundary` type unquoted.
- E. Hand-rolled patterns: settled. The statusline facts are UNCHECKED.

Digging ended: every sub-area settled after round 3. B's remaining gaps are verification failures, not undug ground.

## Verification

- 12 pages were checked; 11 returned a usable verdict and 1 (statusline) returned output too large to inspect.
- Confirmed:
  - env-vars (3 facts)
  - model-config (3)
  - #91910 (4)
  - #90347 (3)
  - #91984 (3)
  - #82761 (title and status)
  - costs (compact instructions)
  - compaction-threshold (4)
  - context-editing (4)
  - types.py (PreCompactHookInput, AgentDefinition with no compaction field, PreCompact output `additionalContext` only, no compaction option)
- NOT ON PAGE, checked against [hooks.md](https://code.claude.com/docs/en/hooks.md) (likely truncation of a very long page, but removed from the map per the rule):
  - PreCompact `custom_instructions` input (confirmed instead from SDK types.py)
  - PreCompact output `customInstructions` replaces the instructions
  - PreCompact `additionalContext` added to the summary (confirmed as a field from types.py only)
  - PreCompact `decision: "block"` / exit 2 blocks compaction
  - SubagentStart `additionalContext` injection
  - SubagentStop exit 2 / block keeps the sub-agent running
- NOT ON PAGE, checked against types.py: PreCompact output `customInstructions`. The Python SDK has only `additionalContext`.
- UNCHECKED — the statusline output was too large to inspect: `subagentStatusLine` rows, the `tokenCount`/`contextWindowSize` fields, v2.1.205.

## Open rabbit holes

- Can PreCompact block a sub-agent's compaction or replace its instructions? Dug, unsettled: the hooks.md wording failed the check. Test it against the installed v2.1.281 binary with a PreCompact hook that exits 2 inside a sub-agent.
- Does a sub-agent's auto-compaction use CLAUDE.md `# Compact instructions`? Dug, unsettled: the docs are silent. Check with the binary by reading a sub-agent's compaction summary in `subagents/agent-<id>.jsonl`.
- Does `CLAUDE_CODE_AUTO_COMPACT_WINDOW` apply to sub-agents? Dug, unsettled: the docs are silent. Check with the binary.
- Does an SDK `query()` with `env` carrying `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` compact at that threshold? Undug (inference only).
- Are #82761 and #91984 fixed at v2.1.281? Undug. Check the changelog after v2.1.260, or the binary.
- `SDKCompactBoundaryMessage` / `compact_metadata` shape: dug, unsettled (the type-file fetches truncated).
- `subagentStatusLine` field list: dug, UNCHECKED. Re-fetch the statusline page in sections.
- TS SDK PreCompact output `customInstructions?`: dug, unsettled (TS `.d.ts` paraphrase only).
- Microcompact, cached microcompact and session-memory-compact scope for sub-agents (#42542): undug.
- Per-agent startup opt-outs, [#96405](https://github.com/anthropics/claude-code/issues/96405) (open, 2.1.280): undug. It bears on startup context, not compaction.
