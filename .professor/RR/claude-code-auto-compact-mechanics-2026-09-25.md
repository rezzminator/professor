# RR — How auto-compact works in the official Claude Code CLI (mechanism, trigger/controls, main chat vs sub-agents)

Question: How does auto-compact work in the OFFICIAL Anthropic Claude Code CLI harness (the stock product, not any third-party framework or wrapper)? 1. Mechanism 2. Trigger and controls 3. Main chat vs sub-agents.

## Answer

When the context fills up, Claude Code first clears older tool outputs, then summarizes the conversation if it still needs room. After a compaction it reloads a small set of things: the root CLAUDE.md, a few recently used files and skill bodies. Today it compacts at an "auto-compact window": by default the model's context limit, or about 967K tokens on native-1M models. You can change the window with `/autocompact`, `--autocompact`, the `autoCompactWindow` setting or `CLAUDE_CODE_AUTO_COMPACT_WINDOW`. `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` can only make it compact earlier. Sub-agents compact inside their own context window, which is sized by the sub-agent's own model. The one official line on this says the percentage override "applies to both main conversations and subagents". The docs give no separate sub-agent threshold.

Evidence labels: **[official]** means Anthropic docs. **[unofficial]** means a community issue or reverse-engineering. A fact marked **UNCHECKED** comes from a digger's fetch that I could not re-verify.

## 1. Mechanism

- [official] "Claude Code manages context automatically as you approach the limit. It clears older tool outputs first, then summarizes the conversation if needed. Your requests and key code snippets are preserved; detailed instructions from early in the conversation may be lost. Put persistent rules in CLAUDE.md rather than relying on conversation history." ([how-claude-code-works](https://code.claude.com/docs/en/how-claude-code-works))
- [official] "To control what's preserved during compaction, add a "Compact Instructions" section to CLAUDE.md or run `/compact` with a focus (like `/compact focus on the API changes`)." (same page)
- [official] Guard against thrashing: "If a single file or tool output is so large that context refills immediately after each summary, Claude Code stops auto-compacting after a few attempts and shows an error instead of looping." (same page)
- [official, UNCHECKED — the verification fetch of this page returned raw page source and answered nothing] From [context-window](https://code.claude.com/docs/en/context-window), per a digger:
  - "As of v2.1.198, the summarization request inherits your session's extended thinking configuration".
  - "Right after compaction, Claude Code re-reads up to five of the files Claude has read or edited in the session, choosing the ones modified most recently. A file over 5,000 tokens comes back as a path reference".
  - Path-scoped rules and nested CLAUDE.md files "compaction summarizes them away"; project-root CLAUDE.md persists.
  - "Skill bodies are re-injected after compaction, but large skills are truncated to fit the per-skill cap".
- [official CHANGELOG, UNCHECKED] v2.1.282 "Fixed compaction failing when the summarization request is refused; it now retries on a fallback model". v2.1.274 fixed sessions "ending with 'Prompt is too long' instead of compacting when the context overflowed again after a reactive compaction". This shows there is also a *reactive* compaction when a request overflows. ([CHANGELOG raw](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md))

## 2. Trigger and controls

- [official] "The auto-compact window is how full the context window can get before Claude Code compacts the conversation." ([model-config](https://code.claude.com/docs/en/model-config))
- [official] Default: "If you don't set an auto-compact window, Claude Code compacts when the conversation reaches the model's context limit, except in these sessions:" Two cases follow:
  - "Models running with a native 1M window, such as Sonnet 5, the Fable models, and Opus 4.7 and later on the Anthropic API, compact before the window fills, at about 967K tokens by default."
  - "Sonnet 4.6 and Opus 4.6 without extended context compact at the 200K boundary, and so do Opus 4.8 and later when they run with a 200K context window, such as on Amazon Bedrock, Google Cloud's Agent Platform, and Microsoft Foundry". (model-config)
- [official] Controls, all from model-config:
  - `/autocompact 500k`: "Claude Code saves it to your user settings as `autoCompactWindow`".
  - `--autocompact`: "The flag overrides your saved setting for that launch without changing it".
  - `CLAUDE_CODE_AUTO_COMPACT_WINDOW`: "While it's set, it takes precedence over the command, the flag, and the setting".
  - "The command and the flag accept a window size from 100K to 1M tokens".
  - `/autocompact auto` returns to the tuned default.
- [official] `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`: "Set the percentage (1-100) of the auto-compact window at which auto-compaction triggers. Use lower values like `50` to compact earlier; the variable can't raise the threshold, so values above the default percentage are ignored. It applies only in sessions that compact before the model's context limit. Applies to both main conversations and subagents" ([env-vars](https://code.claude.com/docs/en/env-vars))
- [unofficial, older builds, version not stated] Issue #31806 quotes decompiled code: `defaultThreshold = effectiveWindow − 13000; // ~83.5% of 200k` and `Math.min(userThreshold, defaultThreshold)`, so the override is capped ([#31806](https://github.com/anthropics/claude-code/issues/31806)). **DISPUTED against the current docs**: the reverse-engineered code says about 83% of 200K, while model-config now documents compaction "at the 200K boundary" or about 967K. That looks like a behavior change over time, but no source dates it.
- [unofficial] Issue #36381, v2.1.79: the override did not fire at the configured percentage. Context "reached 67% without triggering", and in another session "over 80%" ([#36381](https://github.com/anthropics/claude-code/issues/36381)). UNCHECKED.
- [official, UNCHECKED] Hooks: "PreCompact | Before context compaction" and "PostCompact | After context compaction completes". Both match `manual` or `auto`. A digger read that PreCompact/PostCompact are missing from the exit-code-2 blocking table, but that reading is unquoted. ([hooks](https://code.claude.com/docs/en/hooks))
- Not found in official docs: which token count is compared with the threshold (input, cache or output fields); `autoCompactEnabled` / the `/config` toggle; `DISABLE_AUTO_COMPACT` and `DISABLE_COMPACT`, which are not on the env-vars page; and the text of the pre-compaction warning. model-config was checked and has no statement on disabling auto-compact.

## 3. Main chat vs sub-agents

- [official] "Subagents work in their own context window. A subagent starts fresh unless it's a fork, which starts with a copy of your conversation so far. Either way, the subagent's tool calls stay out of your context, and Claude gets back a summary when the subagent finishes." ([how-claude-code-works](https://code.claude.com/docs/en/how-claude-code-works))
- [official] "a subagent's context window is sized by its own model, not the parent's. Delegating to a model with a smaller window gives that subagent the smaller window." ([sub-agents](https://code.claude.com/docs/en/sub-agents)). The sub-agents page never mentions compaction (checked).
- [official] `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` "Applies to both main conversations and subagents" ([env-vars](https://code.claude.com/docs/en/env-vars)). This is the only official statement that sub-agents auto-compact.
- Inference, not stated anywhere: sub-agents follow the same per-model default, each against its own window. No source says whether a sub-agent inherits `autoCompactWindow`, `/autocompact` or `CLAUDE_CODE_AUTO_COMPACT_WINDOW`.

## Coverage

- Mechanism: settled
- Trigger/controls: partial. The counted token fields, the disable toggle, PreCompact blocking and the warning text are unsourced.
- Main chat vs sub-agents: partial. There is one official line, and nothing on whether sub-agents inherit the window settings.
- Digging ended: round 2 settled nothing and added no sub-area.

## Verification

17 statements checked on 4 pages: model-config (7), context-window (6), how-claude-code-works (4), sub-agents (2). model-config, how-claude-code-works and sub-agents answered. None came back NOT ON PAGE; model-config found nothing on disabling auto-compact, which is recorded as a gap.

UNCHECKED:
- The four context-window facts (thinking inheritance, five-file reload, path-scoped rules, skill re-injection). The fetch returned raw page source, not answers.
- The CHANGELOG entries, the hooks table, and issues #36381 and #34126. These were not re-fetched.

## Open rabbit holes

- Older CHANGELOG history for when the ~83% / 13K buffer changed to the model-limit / 967K default — dug, unsettled
- Which usage fields are counted against the threshold — dug, unsettled
- Where `autoCompactEnabled`, `DISABLE_AUTO_COMPACT` and the `/config` toggle are documented (settings-reference page) — dug, unsettled
- Whether a PreCompact hook can block compaction, and its input fields (trigger, custom_instructions) — dug, unsettled
- Whether sub-agents inherit `autoCompactWindow`; issue #90347 (per-agent auto-compact window request) — undug
- How a forked sub-agent compacts the history it copied — undug
- The troubleshooting page on the thrashing error — undug
