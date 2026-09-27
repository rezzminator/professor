# RR — How Claude Code plugins work end to end (2026-09), and can one give per-agent auto-compact control or compaction-time agent identity?

Question: map how Claude Code PLUGINS work, end to end, as of 2026-09, and list their full capability surface; settle whether a plugin can do per-party (main vs each sub-agent) auto-compact thresholds, or at least know which agent a compaction belongs to, or control/customise compaction itself (context: PreCompact carries no agent_id/agent_type — anthropics/claude-code#91910; CLAUDE_CODE_AUTO_COMPACT_WINDOW is per-process and settings `env` reaches sub-agents); deep-read tamaratran/fast-jev-compaction.

## Answer

The shipped plugin surface can't do it. That covers settings-style hooks, agents, skills, MCP, LSP and monitors. Compaction hooks fire for a sub-agent's compaction with only the parent's fields. The auto-compact window is one session-wide value that sub-agents inherit unconditionally. Plugin agents ignore `hooks` frontmatter. The only route with the needed power is Claude Code's **experimental, unshipped "function hooks"**, turned on with `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`. These are in-process TypeScript middleware that fast-jev-compaction uses. They can replace or veto the compaction result, trigger compaction on the plugin's own threshold (`$.session.compact()` called from `turn.complete`), and their declared types carry an `agentId` that is "absent on the main loop". No source shows that `session.compact` or `$.session.compact()` is scoped per sub-agent loop, so per-party thresholds through function hooks are plausible but **unverified**.

## Map

### 1. Plugin structure, manifest, env and data dirs (settled)

- **Manifest:** "A plugin manifest is the `plugin.json` file in a plugin's `.claude-plugin/` directory." Also: "`name` is the only required key." ([plugins-reference](https://r.jina.ai/https://code.claude.com/docs/en/plugins-reference))
- **Manifest fields:** the table has rows for `channels`, `experimental.monitors`, `dependencies`, `lspServers` and `outputStyles` ([plugins-reference](https://r.jina.ai/https://code.claude.com/docs/en/plugins-reference)).
  - `version`: "A version string, not checked against semver. Setting it pins the plugin to that version until you change it."
  - `defaultEnabled`: "Whether the plugin starts enabled when the user hasn't set it in `enabledPlugins`. Defaults to `true`."
- **Variables** ([plugins-reference](https://r.jina.ai/https://code.claude.com/docs/en/plugins-reference)):
  - `${CLAUDE_PLUGIN_ROOT}`: "Absolute path of the plugin's installed version".
  - `${CLAUDE_PLUGIN_DATA}`: "`~/.claude/plugins/data/<id>/`, created on first reference and kept across plugin updates."
  - `CLAUDE_PLUGIN_OPTION_<KEY>`: "exported to hook processes for every option, with `<KEY>` uppercased."
  - `${user_config.KEY}`: substituted in MCP and LSP config, exec-form hook `args`, and skill and agent content (a round-3 quote, not re-checked).
  - The data dir is per-plugin, never per-agent.
- **Components** ([plugins/components](https://r.jina.ai/https://code.claude.com/docs/en/plugins/components)):
  - Skills, commands and agents live in their own directories.
  - Hooks go in `hooks/hooks.json`, MCP servers in `.mcp.json` and LSP servers in `.lsp.json`.
  - "Save each output style as `output-styles/<name>.md`" and "Save each theme as `themes/<slug>.json`".
  - Monitors: "A monitor is a shell command that runs in the background for the whole session... Save the entries in `monitors/monitors.json`".
  - bin/: "Files in `bin/` at the plugin root are on the `PATH` of the Bash tool's shell while the plugin is enabled".
  - Plugin `settings.json`: "Set `agent` to run one of the plugin's own agents as the main thread". `subagentStatusLine` is also allowed (round-3 quote, not re-checked).
- **What plugin agents lose** ([plugins/components](https://r.jina.ai/https://code.claude.com/docs/en/plugins/components), corroborated by [sub-agents](https://r.jina.ai/https://code.claude.com/docs/en/sub-agents), which marks each field "Ignored for plugin subagents"):
  - "**Ignored fields**: `permissionMode`, `hooks`, `mcpServers`, and `initialPrompt`."
  - "An agent file can't add hooks or MCP servers on its own, so add those as plugin hooks and MCP servers instead".
  - None of the agent frontmatter fields controls compaction ([#90347](https://github.com/anthropics/claude-code/issues/90347)).

### 2. Marketplaces, install, scopes, versioning, trust (settled)

- **Anthropic's three marketplaces** ([anthropic-marketplaces](https://code.claude.com/docs/en/plugins/anthropic-marketplaces.md)):
  - **Official:** repo `anthropics/claude-plugins-official`, marketplace name `claude-plugins-official`. "Claude Code adds it the first time you start an interactive terminal session, unless a managed policy or `CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL` blocks it".
  - **Community:** `claude-community`.
  - **Demo:** "If a tutorial or an older set of instructions tells you to run `/plugin marketplace add anthropics/claude-code`, that adds the demo marketplace, named `claude-code-plugins`. It isn't the official marketplace".
- **Marketplace file:** a marketplace is "a directory or repository with a `.claude-plugin/marketplace.json` file". Plugin sources can be relative, `github`, `git-subdir`, `url`, `archive`, `npm` or `command` ([plugin-marketplaces](https://code.claude.com/docs/en/plugin-marketplaces), round-1 quote).
- **Scopes:** user scope writes `enabledPlugins` in `~/.claude/settings.json`, project scope writes `.claude/settings.json`, and local scope writes `.claude/settings.local.json`. "the local setting overrides the project setting, and the project setting overrides the user setting" ([discover-plugins](https://code.claude.com/docs/en/discover-plugins.md)).
- **Updates:** auto-update is on by default for `claude-plugins-official` and off for third-party marketplaces. `claude plugin update <plugin>@<marketplace>`. "The running session keeps the versions it already loaded" until `/reload-plugins` ([discover-plugins](https://code.claude.com/docs/en/discover-plugins.md)).
- **Cache:** installed plugins are cached under `~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/` ([plugins/security](https://code.claude.com/docs/en/plugins/security), round-1 quote).
- **CLI verbs** ([plugins/cli-reference](https://r.jina.ai/https://code.claude.com/docs/en/plugins/cli-reference)):
  - `init` (alias `new`), `install` (`i`), `uninstall` (`remove`, `rm`), `enable`, `disable`, `update`, `list`, `details`, `validate`, `prune` (`autoremove`), `eval`, `eval init`, `tag`, and `marketplace add|list|remove|update`.
  - "`--scope` takes `user`, `project`, or `local`... `update` also takes `managed`" ([cli-reference.md](https://code.claude.com/docs/en/plugins/cli-reference.md)).
- **Trust** ([plugins/security](https://code.claude.com/docs/en/plugins/security)):
  - "A Claude Code plugin you install can execute arbitrary code on your machine with your user privileges."
  - "Claude Code runs hooks and MCP servers outside the sandbox."
  - Managed controls include `strictKnownMarketplaces`, `pluginTrustMessage` and `allowManagedHooksOnly` ([hooks](https://code.claude.com/docs/en/hooks), round-1 quote).

### 3. Official tooling for building plugins (settled)

- **plugin-dev** lives at `anthropics/claude-plugins-official/plugins/plugin-dev` ([README](https://github.com/anthropics/claude-plugins-official/blob/main/plugins/plugin-dev/README.md)).
  - It is "A comprehensive toolkit for developing Claude Code plugins with expert guidance on hooks, MCP integration, plugin structure, and marketplace publishing".
  - It has seven skills: hook-development, mcp-integration, plugin-structure, plugin-settings, command-development, agent-development and skill-development.
  - `/plugin-dev:create-plugin` runs an "8-Phase Process".
- **Install command, disputed.** The README's install line reads `/plugin install plugin-dev@claude-code-marketplace`. No marketplace by that name is listed on [anthropic-marketplaces](https://code.claude.com/docs/en/plugins/anthropic-marketplaces.md), where the official one is `claude-plugins-official`. The working form is therefore most likely `/plugin install plugin-dev@claude-plugins-official` (inferred; not run).
- **`claude plugin eval`** "runs your plugin against a suite of test cases and scores the results. Each case is a realistic prompt plus one or more graders". `claude plugin eval init` drafts the cases ([plugin-evals](https://code.claude.com/docs/en/plugin-evals), round-1 quote).
- `claude plugin validate` checks the manifest ([plugins-reference](https://r.jina.ai/https://code.claude.com/docs/en/plugins-reference)).

### 4. Runtime capability: what a plugin actually executes (partial)

- **Shipped hook types.** "There are five types: Command hooks... HTTP hooks... MCP tool hooks... Prompt hooks... Agent hooks" ([hooks](https://r.jina.ai/https://code.claude.com/docs/en/hooks)). None of them runs in-process; a command hook is a subprocess.
- **Plugin hooks are settings hooks.** "When a plugin is enabled, its hooks merge with your user and project hooks." ([hooks](https://r.jina.ai/https://code.claude.com/docs/en/hooks)) They use the same events, input and output.
- **Output fields** that a hook can return include `additionalContext`, `updatedInput`, `updatedToolOutput`/`updatedMCPToolOutput`, `permissionDecision`, `systemMessage` and `continue` (round-1 digger, **unquoted** as a consolidated table). No shipped hook field rewrites the whole transcript or the compaction prompt.
- **Function hooks (experimental in-process TypeScript).**
  - The proposal is [#91870](https://github.com/anthropics/claude-code/issues/91870), opened by Anthropic's @poteat on 2026-09-03: "hook into CC using TypeScript functions a la Express (or Koa!), and basically change whatever you want."
  - "We're now committed to shipping function hooks, on the scale of weeks in lieu of days or months."
  - "A mod is just a plugin that uses function hooks".
  - Testers "may use `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 claude`."
  - A plugin opts in through `hooks/hooks.json` = `{"modules": ["./fast-jev.ts"]}` ([fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction)).
  - [#95328](https://github.com/anthropics/claude-code/issues/95328) (2.1.276, 2026-09-18) corroborates the contract: "A plugin answers session.compact with { messages }, handing most messages back unchanged".
- **Function-hook host types (2.1.274)**, from [types/claude-code.d.ts](https://r.jina.ai/https://raw.githubusercontent.com/tamaratran/fast-jev-compaction/main/types/claude-code.d.ts):
  - Declared events include `session.compact`, `session.start`, `turn.step`, `turn.complete`, `prompt.submit`, `tool.call`, `agent.spawn`, `agent.offer` and `classic.*` (round-2 digger, not re-checked).
  - `AgentLoop.agentId`: "The id of the loop this call runs in: for a subagent or a teammate, the `id` `$.agent.list()` gives it; absent on the main loop."
  - `$.session.compact`: "Compacts the conversation: the event `session.compact` with `trigger` `plugin`, the same call `/compact` makes, between turns." Also: "Resolves `{ skip }` when a hook vetoed it; rejects while a turn runs."
  - `usage: (args?: SessionUsageArgs) => Promise<SessionUsage>;`
  - `BaseHookInput` declares `agent_id?: string;` and `agent_type?: string;`.
- **Third-party only:** claudefa.st says "The runtime sits in build 2.1.260 behind a flag, and it has not shipped" ([claudefa.st](https://claudefa.st/blog/tools/hooks/function-hooks)).
- **Agent SDK:** its hooks are in-process callbacks ("Hooks are callback functions that run your code in response to agent events") and include PreCompact ([agent-sdk/hooks](https://code.claude.com/docs/en/agent-sdk/hooks), round-1, **unquoted** beyond the intro). That is a different host from the CLI plugin system.

### 5. Compaction hooks and agent identity (settled)

- **agent_id rule:** "`agent_id` | Unique identifier for the subagent. Present only when the hook fires inside a subagent call." ([hooks](https://r.jina.ai/https://code.claude.com/docs/en/hooks))
- **Sub-agent compaction carries no identity** ([#91910](https://github.com/anthropics/claude-code/issues/91910), open):
  - "When a background subagent (Agent tool) auto-compacts, the `PreCompact` hook and the `SessionStart` hook with `matcher: compact` both run. The payload carries the parent session's `session_id`, `transcript_path` and `cwd`, and nothing that identifies the subagent: no `agent_id`, no `agent_type`, no `agent_transcript_path`, no flag that says the compaction is a subagent's."
  - "`PostCompact` fires roughly 35 ms after `SessionStart(compact)` for a subagent's compaction, with the parent's fields plus `compact_summary`, and no agent identity."
  - "a `PreCompact` hook that checkpoints state, or holds a compaction with `{"decision":"block"}`, applies to every subagent's compaction as well."
  - Workaround: "reading the transcript tail for `isSidechain` records, which is a heuristic", plus the `agent-<id>.meta.json` sibling.
- **Sub-agents compact on the main session's settings** ([sub-agents](https://r.jina.ai/https://code.claude.com/docs/en/sub-agents)): "Subagents support automatic compaction using the same logic as the main conversation. Compaction triggers under the same conditions, and `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` applies to subagents as well."
- **Threshold override** ([env-vars](https://code.claude.com/docs/en/env-vars)):
  - `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`: "the variable can't raise the threshold, so values above the default percentage are ignored"; "Applies to both main conversations and subagents".
  - `CLAUDE_CODE_AUTO_COMPACT_WINDOW` is **not** on the env-vars page.
- **One window for everyone** ([#90347](https://github.com/anthropics/claude-code/issues/90347), open feature request for a per-agent window):
  - "The auto-compact window is a single session-wide value, resolved from `CLAUDE_CODE_AUTO_COMPACT_WINDOW` / `--autocompact` / `/autocompact` / the `autoCompactWindow` setting."
  - "Subagents inherit the session's resolved auto-compact configuration unconditionally".
  - Agent frontmatter "has **no** context/compaction field".
- **Frontmatter hooks** ([sub-agents](https://r.jina.ai/https://code.claude.com/docs/en/sub-agents)): "Define hooks directly in the subagent's markdown file. These hooks only run while that specific subagent is active and are cleaned up when it finishes." The page does not say whether a frontmatter `PreCompact` fires for that sub-agent's compaction.
- **Custom compaction instructions:** per [#14160](https://github.com/anthropics/claude-code/issues/14160) (round-1 quote, closed as duplicate), "PreCompact hook receives empty `custom_instructions` for auto-trigger". No shipped setting injects instructions into auto-compaction.
- **DISPUTED: can PreCompact block compaction?**
  - Round-2 fetch of [hooks](https://code.claude.com/docs/en/hooks): "On `PreCompact` and `PostCompact`, exit code 2 isn't honored".
  - Step-6 reader fetch of [the same page](https://r.jina.ai/https://code.claude.com/docs/en/hooks): its exit-code table row is "PreCompact | Yes | Blocks compaction".
  - [#91910](https://github.com/anthropics/claude-code/issues/91910) describes holding a compaction with `{"decision":"block"}`.
  - Unresolved.

### 6. tamaratran/fast-jev-compaction (settled)

- **What it is:** "a Claude Code function-hook plugin: `hooks/fast-jev.ts` is a thin adapter that feeds `session.compact` transcripts through `src/`" ([repo](https://github.com/tamaratran/fast-jev-compaction)).
- **Manifest:** `.claude-plugin/plugin.json` v0.3.0, with `userConfig` defaults `keepThreshold` 0.5, `preserveRecentMessages` 6, `compactAtPercent` 60, `minReductionRatio` 0.25, `maxStateTokens` 25000 and `model` `jev-latest` (round-1 digger).
- **hooks/ holds three files** (round-1 digger):
  - `hooks.json` = `{"modules": ["./fast-jev.ts"]}`, which binds no events itself.
  - `fast-jev.ts`.
  - `README.md`: the flag `export CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`, minimum 2.1.274, install `claude plugin marketplace add tamaratran/fast-jev-compaction` and then `claude plugin install fast-jev-compaction@fast-jev-compaction` (round-2 quote). It needs `TYPESAFE_API_KEY`.
- **`fast-jev.ts`** ([raw](https://raw.githubusercontent.com/tamaratran/fast-jev-compaction/main/hooks/fast-jev.ts)):
  - It binds `on('session.compact', async ($, event, next) => {` and `on('turn.complete', async ($, event: TurnCompleteInput, next) => {`.
  - The `turn.complete` handler runs its own threshold: `if ((context.percent ?? 0) < configured.compactAtPercent) return next(event); compacting = true; await $.session.compact();`.
  - The `session.compact` handler returns `return { messages };` on success and `return next(event);` on failure, falling back to the built-in summary.
- **Mechanism:** it rewrites the in-memory message array; nothing is written to the transcript file and no hook-output JSON is used.
  - Jev scores every tool call and result. Calls are batched: "Splits the candidate calls into batches whose questions, together with the (always complete) state, fit one request", then run concurrently with `Promise.all` ([src/compact.ts](https://github.com/tamaratran/fast-jev-compaction), round-1 digger).
  - Kept items stay verbatim; unneeded results are dropped or truncated to `truncateHeadChars`.
  - It falls back when "the estimated reduction is below `minReductionRatio`" (round-1, README).
- **Sub-agents:** "The page does not reference agentId, agent_id, agent_type, or subagent anywhere" ([fast-jev.ts check](https://raw.githubusercontent.com/tamaratran/fast-jev-compaction/main/hooks/fast-jev.ts)). Its tests contain no agent terms either. It treats every compaction the same.

### 7. Verdict: can a plugin do per-party compaction control or report compaction-time identity?

| Route | Status | Evidence and limits |
|---|---|---|
| Plugin (or settings) `PreCompact`/`PostCompact`/`SessionStart(compact)` hooks for identity | **Dead end** | Sub-agent compaction fires them with the parent's fields and no agent identity ([#91910](https://github.com/anthropics/claude-code/issues/91910), open). The only fallback is the issue's `isSidechain`/`agent-<id>.meta.json` heuristic. |
| Per-party threshold via env or settings (`CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`, `autoCompactWindow`, settings `env`) | **Dead end** | "Applies to both main conversations and subagents" ([env-vars](https://code.claude.com/docs/en/env-vars)). "Subagents inherit the session's resolved auto-compact configuration unconditionally" ([#90347](https://github.com/anthropics/claude-code/issues/90347)). |
| A per-agent compaction field in agent frontmatter | **Dead end** | The frontmatter "has **no** context/compaction field" ([#90347](https://github.com/anthropics/claude-code/issues/90347)). |
| Hooks in agent frontmatter (identity by scoping) | **Unverified, weak** | The docs say "These hooks only run while that specific subagent is active" ([sub-agents](https://r.jina.ai/https://code.claude.com/docs/en/sub-agents)). They are ignored for plugin-shipped agents ([components](https://r.jina.ai/https://code.claude.com/docs/en/plugins/components)). Nothing says PreCompact fires there only for that agent's own compaction, and a hook cannot change the threshold. |
| Shipped plugin runs JS in-process | **Dead end** | All five shipped hook types run as a subprocess, HTTP call, MCP call, prompt or agent ([hooks](https://r.jina.ai/https://code.claude.com/docs/en/hooks)). "More power" exists only in function hooks. |
| **Function hooks: replace or veto the compaction result** | **Viable (experimental)** | A `session.compact` handler returns `{ messages }` or falls through to `next` ([fast-jev.ts](https://raw.githubusercontent.com/tamaratran/fast-jev-compaction/main/hooks/fast-jev.ts); [#95328](https://github.com/anthropics/claude-code/issues/95328)). A hook can veto (`{ skip }`, [d.ts](https://r.jina.ai/https://raw.githubusercontent.com/tamaratran/fast-jev-compaction/main/types/claude-code.d.ts)). Limits: needs the flag, unshipped, and "this surface may change". |
| **Function hooks: plugin-owned threshold** | **Viable for the main loop** | `turn.complete` plus `context.percent` plus `await $.session.compact()` is exactly what fast-jev does with `compactAtPercent`. Limits: `$.session.compact()` "rejects while a turn runs"; it can only lower the effective threshold (it cannot stop native auto-compact firing first) unless the `session.compact` handler vetoes native triggers. |
| **Function hooks: per-sub-agent threshold or identity** | **Plausible, UNVERIFIED** | `AgentLoop.agentId` is "absent on the main loop" and present "for a subagent or a teammate" ([d.ts](https://r.jina.ai/https://raw.githubusercontent.com/tamaratran/fast-jev-compaction/main/types/claude-code.d.ts)). No source showed whether the `session.compact` input or `TurnCompleteInput` carries `AgentLoop`, or whether `$.session.compact()` targets the calling sub-agent's loop rather than the main conversation. The next step is a live test under the flag: log `event.agentId` in `turn.complete` and `session.compact` while a sub-agent runs. |

## Coverage

- Plugin structure, manifest, env and data dirs: **settled**
- Marketplaces, install, scopes, versioning and trust: **settled**
- Official plugin-building tooling (plugin-dev, eval): **settled** (added as its own section from brief item 2)
- Runtime capability, including function hooks (added mid-run): **partial**. The session.compact and turn.complete input types are unquoted because the 11k-line d.ts truncates in every summarizing fetch.
- Compaction hooks and agent identity: **settled**, with one DISPUTED point (can PreCompact block?)
- fast-jev-compaction: **settled**
- Verdict: derived from the above

Digging ended: a round settled nothing and added no sub-area (round 4 of 5).

## Verification

I checked 52 facts on 12 pages in step 6. 48 were confirmed. NOT ON PAGE:
- **"On PreCompact and PostCompact, exit code 2 isn't honored"**: the reader fetch of the hooks page instead quotes "PreCompact | Yes | Blocks compaction". It is kept only as the DISPUTED entry, with both sides.
- **PreCompact input example with `trigger`/`custom_instructions` on the hooks page**: not in the reader fetch, which was truncated. `custom_instructions` stays sourced to #14160 (round-1 quote) and is no longer attributed to the docs.
- **PostCompact input example with `compact_summary` on the hooks page**: not in the reader fetch. `compact_summary` stays sourced to #91910, which confirms it.
- **#91870 mentions sub-agents or agent-scoped hooks**: NO. This is recorded as an absence, not a fact.

Also: the hooks page does not document `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, and nor does env-vars (NO on both). #90347 names it.

UNCHECKED: none. Facts marked "round-N quote" in the map were quoted by diggers but not re-sampled in step 6.

## Open rabbit holes

- Does the `session.compact` function-hook input (and `TurnCompleteInput`) carry `AgentLoop.agentId`, and does `$.session.compact()` compact the calling sub-agent's loop? Read the raw d.ts with a non-summarizing tool (`curl` plus grep for `OpEventOf`, `TurnCompleteInput`, `AgentLoop`), or run a live test under the flag. **dug, unsettled**
- Can PreCompact block, via exit 2 or `decision:block`? The docs disagree across two fetches, and #91910 says it can. **dug, unsettled**
- Does a `PreCompact` hook declared in a non-plugin agent's frontmatter fire for that agent's own compaction only? **dug, unsettled**
- The plugin-dev README says `@claude-code-marketplace` but the official marketplace name is `claude-plugins-official`. Is the README stale? **undug**
- The claudefa.st list of function-hook events and `$` nouns (`$.agent.spawn`, `agent.offer`): can `agent.spawn` set per-agent context or compaction options? **dug, unsettled**
- Agent SDK PreCompact callback input: does it carry agent identity? **dug, unsettled**
- Status of #90347 and #91910: neither has a maintainer reply yet. **undug**
- Does `claude plugin eval` run in a sandbox, and what is its minimum version (v2.1.269 per snippets only)? **undug**
