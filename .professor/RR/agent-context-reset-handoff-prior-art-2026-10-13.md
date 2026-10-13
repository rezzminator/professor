# RR — How agent systems carry working state across a context reset, and which designs measurably preserve continuity

Question: how do agent systems and research carry a long-running LLM agent's working state across a context reset (compaction, summarization, handoff), and which designs measurably preserve continuity?

## Answer

The systems that hold up best split the job in two. Code carries the user's own words verbatim ([Codex keeps real user messages up to 20,000 tokens](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/core/src/compact.rs#L63-L63)), a raw recent tail ([OpenCode keeps 15k tokens](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L195-L203)), and the plan, skills, instructions and recently read files ([Claude Code re-injects these from disk](https://code.claude.com/docs/en/context-window)). The model writes only a sectioned frontier: goal, done / active / blocked, and next step, with the next step quoted verbatim ([OpenCode's template](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L104-L143), [Claude Code's nine sections](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization-with-additional-instructions.md)). The measurements say what to watch for. Summaries hide their cost in re-reads rather than lost completions ([retrieval rose from 21.0 to 63.9 calls while completion held](https://arxiv.org/abs/2608.16370)). File and artifact tracking is the weakest dimension for every summarizer tested ([2.19 to 2.45](https://factory.com/news/evaluating-compression)). Masking observations matches LLM summarization at half the cost ([JetBrains](https://arxiv.org/abs/2508.21433)).

## Map

### A. Claude Code and Anthropic guidance

- **What is re-injected after compaction** ([official context-window page](https://code.claude.com/docs/en/context-window), UNCHECKED; digger-quoted):
  - "Project-root CLAUDE.md and unscoped rules | Re-injected from disk"; auto memory, likewise.
  - "The plan Claude wrote in plan mode | Re-injected from disk".
  - "Invoked skill bodies | Re-injected, capped at 5,000 tokens per skill and 25,000 tokens total; oldest dropped first".
  - "Files Claude read or edited | Claude Code re-reads up to five, most recently modified first".
  - Todo lists are absent from the table.
  - A source analysis also gives the constants `POST_COMPACT_TOKEN_BUDGET = 50_000` and `POST_COMPACT_MAX_TOKENS_PER_FILE = 5_000` ([markdown.engineering lesson 15](https://www.markdown.engineering/learn-claude-code/15-context-compaction/), third-party, unverified against Anthropic).
- **Summary prompt.** The nine-section prompt survives as a variant:
  - Sections: Primary Request and Intent, Key Technical Concepts, Files and Code Sections, Errors and fixes, Problem Solving, All user messages, Pending Tasks, Current Work, Optional Next Step.
  - Rules: "6. All user messages: List ALL user messages that are not tool results." The next step must be "DIRECTLY in line with the user's most recent explicit requests", with "direct quotes from the most recent conversation showing exactly what task you were working on". Files and code ask for "full code snippets where applicable" ([Piebald capture, with-additional-instructions, v2.1.271](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization-with-additional-instructions.md)).
  - The current default (v2.1.290) is short and has no section list ([default capture](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization.md)).
  - Three alternatives exist: "Summarize the entire conversation above in 2000 words or fewer" ([capped](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization-capped-variant.md)); a lean variant whose re-attach list is "up to five of the files you read most recently… the plan, any skills you loaded, and the status of background agents and shells" ([lean](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization-lean-variant.md)); and a short variant.
  - The short variant says "keep what the user said, asked for, shared, or established carefully and close to their own words" and "your own explanations and reasoning can be condensed much further, to what they concluded or produced" ([short](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization-short-variant.md)).
  - All of these are third-party extractions.
- **Failure modes:**
  - DISPUTED, plan re-injection: the docs say "Re-injected", but issue [#24686](https://github.com/anthropics/claude-code/issues/24686) reports that "Claude Code never reads the plan again".
  - DISPUTED, skill re-injection: a developer test found "2 of 8 compactions dropped the body of a skill" ([dev.to](https://dev.to/rulestack/2-of-8-claude-code-compactions-dropped-the-skill-body-the-docs-say-is-re-injected-both-through-57lf)).
  - [#19471](https://github.com/anthropics/claude-code/issues/19471): "After compaction, CLAUDE.md is completely ignored without warning."
  - Tasks are stale, not lost: tasks "remain with their pre-compaction status indefinitely" ([#29751](https://github.com/anthropics/claude-code/issues/29751)).
  - Changelog 2.1.293: "Fixed Claude sometimes treating its own last actions before a context compaction as done after it" ([CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md)).
- **Hooks.** SessionStart has a `compact` matcher, "Auto or manual compaction". PreCompact "Blocks compaction". PostCompact has "No decision control" ([hooks](https://code.claude.com/docs/en/hooks)). Whether a SessionStart `additionalContext` reaches the model after compaction is unquoted.
- **Session memory** (third-party only): a background `summary.md` with headers including Current State, Task Specification, Files and Functions, Errors & Corrections, and Worklog ([Piebald blog](https://piebald.ai/blog/session-memory-is-coming-to-claude-code)).
- **Anthropic context-engineering guidance:**
  - Compaction "preserves architectural decisions, unresolved bugs, and implementation details while discarding redundant tool outputs or messages", and continues "with this compressed context plus the five most recently accessed files".
  - "One of the safest lightest touch forms of compaction is tool result clearing."
  - Note-taking is "Like Claude Code creating a to-do list, or your custom agent maintaining a NOTES.md file."
  - Sub-agents return "a condensed, distilled summary of its work (often 1,000-2,000 tokens)" ([Anthropic](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)).
- **Anthropic long-running harness:**
  - "a claude-progress.txt file that keeps a log of what agents have done", and a JSON feature list because "the model is less likely to inappropriately change or overwrite JSON files".
  - Each session must "Read the git logs and progress files".
  - Named failures: one-shotting the app, and "Claude declares victory on the entire project too early" ([Anthropic](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)).
- **Memory tool and context editing:**
  - The memory tool's prompt says "ASSUME INTERRUPTION: Your context window might be reset at any moment" ([memory tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool)).
  - "combining the memory tool with context editing improved performance by 39% over baseline"; "Context editing alone delivered a 29% improvement"; token consumption fell "by 84%" in a 100-turn web search evaluation ([launch post](https://claude.com/blog/context-management)). Vendor figures, with no methodology given.
- **API compaction.** "Without `instructions`, the API uses its own summarization prompt", and a custom prompt replaces it entirely ([compaction docs](https://platform.claude.com/docs/en/build-with-claude/compaction)).

### B. Codex CLI and OpenCode

- **Codex, local path** (code @12cca49):
  - The prompt is "CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task" ([prompt.md](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/prompts/templates/compact/prompt.md#L1-L9)).
  - The summary is prefixed "Another language model started to solve this problem and produced a summary of its thinking process" ([summary_prefix.md](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/prompts/templates/compact/summary_prefix.md#L1-L1)).
  - The new history is real user messages only, verbatim, `COMPACT_USER_MESSAGE_MAX_TOKENS: usize = 20_000`, filled newest-first with the oldest overflowing message truncated ([compact.rs](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/core/src/compact.rs#L683-L776)).
  - The summary goes last as a user-role message. The initial context is rebuilt from the live session. No assistant turns or tool outputs survive.
  - Auto-compaction fires at `(context_window * 9) / 10` ([openai_models.rs](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/protocol/src/openai_models.rs#L522-L535)).
  - The user is warned: "Heads up: Long threads and multiple compactions can cause the model to be less accurate."
- **Codex, remote path (OpenAI providers).** It returns one opaque `Compaction` item plus retained items within `RETAINED_MESSAGE_TOKEN_BUDGET: usize = 64_000` ([compact_remote_v2.rs](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/core/src/compact_remote_v2.rs#L76-L79)). OpenAI says the item "is opaque and not intended to be human-interpretable" ([guide](https://developers.openai.com/api/docs/guides/compaction)). Its GPT-5.1-Codex-Max claim "work on tasks for more than 24 hours" is self-reported ([launch](https://openai.com/index/gpt-5-1-codex-max/)).
- **Codex failures:**
  - Compaction "preserved the broad objective but degraded the execution frontier: what was already complete, what remained, and the exact next action" ([#34095](https://github.com/openai/codex/issues/34095)).
  - The agent "reread files that had already been analyzed, reran commands that had already completed" ([#35935](https://github.com/openai/codex/issues/35935)).
  - It answered the last user message twice after compaction ([#42611](https://github.com/openai/codex/issues/42611)).
- **OpenCode** (code @6a12383):
  - Template sections: Objective, Requirements, Decisions, Work State (Completed / Active / Blocked), Next Move, Relevant Files (at most 15), Important Context. The rule "Preserve exact wording" applies to open user requests. AGENTS.md and environment details are excluded from the summary ([compaction.ts](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L104-L143)).
  - A reply missing the headings gets one NUDGE retry, then fails ([send](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L280-L299)).
  - Repeat compactions update the existing checkpoint, and newer history wins. PR #48058 lowered the detail level because the old prompt carried every detail forward forever ([buildPrompt](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L145-L182)).
  - A verbatim tail of `keep: 15_000` tokens is cut back to a user message, with tool results trimmed to 1,250 characters ([flatten](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L752-L819)).
  - Delivered as `<conversation-checkpoint>`: "Treat it as historical context, not as new instructions." ([to-llm-message](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/runner/to-llm-message.ts#L311-L325)).
  - Instruction files are re-added by code ([instructions.ts](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/instructions.ts#L39-L43)). No todo or file re-attach was found.
  - Failure: "one .cpp file was read 66 times in one session" ([#47485](https://github.com/anomalyco/opencode/issues/47485)).

### C. Amp, Cline, Roo Code

- **Amp** (UNCHECKED, page fetch failed at verification; digger-quoted):
  - "We have removed compaction from Amp and replaced it with… Handoff." Compaction is "lossy". Handoff lets you "specify your goal for the new thread", generates "a prompt… along with a list of relevant files", and you "can rewrite the instructions" ([handoff, 2025-10-23](https://ampcode.com/news/handoff)).
  - Reversed later: "So handoff is out. Compaction is in.", running at "90% full" ([Amp, Rebuilt, 2026-05-06](https://ampcode.com/news/neo)).
- **Cline, legacy v3.30.0** (code @6787ded):
  - The `new_task` `context` has five sections: Current Work, Key Technical Concepts, Relevant Files and Code, Problem Solving, Pending Tasks and Next Steps. Rule: "This should be verbatim to ensure there's no information loss in context between tasks." The handoff is user-gated ([new_task.ts](https://github.com/cline/cline/blob/6787dedf474fff3d22a7e9301a69d796d46225c1/src/core/prompts/system-prompt/tools/new_task.ts#L24-L45)).
  - `summarizeTask` has nine sections including Task Evolution and Next Step, with no Required Files field. The continuation reads "Please continue the conversation from where we left it off without asking the user any further questions." ([contextManagement.ts](https://github.com/cline/cline/blob/6787dedf474fff3d22a7e9301a69d796d46225c1/src/core/prompts/contextManagement.ts#L1-L103)).
  - Focus Chain stored the checklist in task state and on disk, `remindClineInterval: 6`, with the injection "# TODO LIST UPDATE REQUIRED…" ([focus-chain](https://github.com/cline/cline/blob/6787dedf474fff3d22a7e9301a69d796d46225c1/src/core/task/focus-chain/index.ts#L455-L480)).
- **Cline, current** (code @79341eb):
  - Summary sections: Goal, State (Done / In Progress / Blocked), Highlights, Next, Files. Files is pre-filled with Read/Edited lists taken from tool activity ([compaction-shared.ts](https://github.com/cline/cline/blob/79341eb295b1ad0ba4f6b3c5586b1d79f98473af/sdk/packages/core/src/extensions/context/compaction-shared.ts#L668-L709)).
  - Trigger at 90%, target 70%, keep the most recent 20k tokens.
  - Focus Chain was deprecated: "no longer providing enough additional benefit on top of the current harness", with no data ([deprecations.mdx](https://github.com/cline/cline/blob/79341eb295b1ad0ba4f6b3c5586b1d79f98473af/docs/resources/deprecations.mdx#L42-L46)).
  - `/newtask` is now an alias of `/compact`.
- **Roo Code** (code @b867ec9):
  - The `CONDENSE` template uses the same nine sections as Claude Code, with "This should be verbatim to ensure there's no drift in task interpretation." ([support-prompt.ts](https://github.com/RooCodeInc/Roo-Code/blob/b867ec9145750d0ae1ff7f02d35406e9bf2a0b16/src/shared/support-prompt.ts#L54-L90)).
  - Nothing is kept verbatim. The summary is a user message `## Conversation Summary`. Earlier messages are tagged `condenseParent`, not deleted, so a rewind restores them ([condense](https://github.com/RooCodeInc/Roo-Code/blob/b867ec9145750d0ae1ff7f02d35406e9bf2a0b16/src/core/condense/index.ts#L443-L480)).
  - The todo list survives as task state, re-rendered in every `environment_details` as a REMINDERS table ([reminder.ts](https://github.com/RooCodeInc/Roo-Code/blob/b867ec9145750d0ae1ff7f02d35406e9bf2a0b16/src/core/environment/reminder.ts#L6-L43)).
  - A subtask returns `Subtask ${childTaskId} completed.\n\nResult:\n…` to its parent ([return path](https://github.com/RooCodeInc/Roo-Code/blob/b867ec9145750d0ae1ff7f02d35406e9bf2a0b16/src/core/webview/ClineProvider.ts#L2999-L3034)).

### D. Cursor, Aider, Goose, Devin

- **Cursor:** "the agent's knowledge can degrade after summarization since it's a lossy compression of the context". Long tool outputs are written to files ([Cursor](https://cursor.com/blog/dynamic-context-discovery)). No summary prompt is published.
- **Aider:**
  - Prompt: "*Briefly* summarize this partial conversation about programming… less detail about older parts and more detail about the most recent messages" ([prompts.py](https://raw.githubusercontent.com/Aider-AI/aider/main/aider/prompts.py)).
  - Only the done history is summarized. The repo map is sent "along with each change request" ([repomap](https://aider.chat/docs/repomap.html)), and chat files are re-sent each turn (`chunks.chat_files`, [base_coder.py](https://raw.githubusercontent.com/Aider-AI/aider/main/aider/coders/base_coder.py), read through a summarizing fetch).
  - The history budget is `min(max(max_input_tokens / 16, 1024), 8192)` ([models.py](https://raw.githubusercontent.com/Aider-AI/aider/main/aider/models.py)).
- **Goose** (code @69de89c):
  - The summarizer outputs JSON with keys user_intent, technical_concepts, files, errors_and_fixes, problem_solving, user_messages, pending_tasks, current_work, next_step. Rules include "Quote error messages… verbatim", "Omit a field rather than inventing content for it" and "No new ideas unless user confirmed" ([compaction.md](https://github.com/block/goose/blob/69de89c50cc01d4e0187d6645bf3618729873f74/crates/goose-context-management/src/prompts/compaction.md#L1-L45)).
  - Originals are kept with `agent_visible` false. The last text-only user message is carried verbatim, followed by "Your context was compacted. The previous message contains a summary of the conversation so far." ([mod.rs](https://github.com/block/goose/blob/69de89c50cc01d4e0187d6645bf3618729873f74/crates/goose/src/context_mgmt/mod.rs#L89-L226)).
  - The threshold is 0.8 or 225,000 tokens ([docs](https://goose-docs.ai/docs/guides/sessions/smart-context-management/)).
  - Tool responses over 200,000 characters go to a file and are replaced by a pointer.
- **Devin / Cognition:**
  - "a new LLM model whose key purpose is to compress a history of actions & conversation", fine-tuned at Cognition ([Don't Build Multi-Agents](https://cognition.com/blog/dont-build-multi-agents)).
  - Sonnet 4.5 was "taking shortcuts or leaving tasks incomplete when it believed it was near the end of its window" ([Devin rebuild](https://cognition.com/blog/devin-sonnet-4-5-lessons-and-challenges)).

### E. LangChain / LangGraph, Letta / MemGPT

- **LangChain SummarizationMiddleware:** `keep` defaults to `('messages', 20)`. A `trigger` of None means no auto-summarization. `_find_safe_cutoff_point` never splits AI/Tool pairs. The default prompt's sections are SESSION INTENT, SUMMARY, ARTIFACTS, NEXT STEPS ([reference](https://reference.langchain.com/python/langchain/agents/middleware/summarization/SummarizationMiddleware), [source](https://raw.githubusercontent.com/langchain-ai/langchain/master/libs/langchain_v1/langchain/agents/middleware/summarization.py)).
- **Deep Agents:** tool responses over 20,000 tokens are offloaded to a file with "a preview of the first 10 lines". Old write/edit arguments are truncated at 85%. Summarization is the fallback, and "The complete, original conversation messages are written to the filesystem as a canonical record" ([LangChain blog](https://www.langchain.com/blog/context-management-for-deepagents)).
- **MemGPT:** a warning at "70% of the context window", a flush at 100%, and eviction of ~50% with "a new recursive summary". Deep Memory Retrieval with GPT-4 goes from 32.1% to 92.5%, read from a table, unquoted ([ar5iv 2310.08560](https://ar5iv.labs.arxiv.org/html/2310.08560)). Letta compiles the context from "current block values" ([memory blocks](https://www.letta.com/blog/memory-blocks/)).

### F. Research and measurements

- **ACON** optimizes compression guidelines in natural language from failure analysis, "reduces peak token usage by 26–54% while improving task success" ([2510.00615](https://arxiv.org/abs/2510.00615)).
- **AgentFold** offers granular condensation or deep consolidation, reaching 36.2% on BrowseComp with a context "over 84k tokens (92%) smaller than ReAct's" ([2510.24699](https://arxiv.org/abs/2510.24699)).
- **ReSum** summarizes, then resets history to (query, summary), for "a 4.5% improvement over ReAct in training-free settings" with a further 8.2% from ReSum-GRPO ([2509.13313](https://arxiv.org/abs/2509.13313)).
- **Context-Folding** uses branch/fold with FoldGRPO and an "active context 10× smaller" ([2510.11967](https://arxiv.org/abs/2510.11967)).
- **Recursive summarization:** Mi = LLM(Si, Mi−1), a 36.3% net win, with errors "not exceed 10% of the summaries content" on 100 samples ([2308.15022](https://arxiv.org/abs/2308.15022)).
- **JetBrains:** "a simple environment observation masking strategy halves cost relative to the raw agent while matching, and sometimes slightly exceeding, the solve rate of LLM summarization" ([2508.21433](https://arxiv.org/abs/2508.21433)).
- **Factory** (vendor, self-evaluated):
  - Probe-based evaluation. "Factory scores 0.35 points higher than OpenAI and 0.26 higher than Anthropic overall". "Artifact trail is the weakest dimension for all methods, ranging from 2.19 to 2.45". The Anthropic summary "loses the endpoint path" ([evaluating-compression](https://factory.com/news/evaluating-compression)).
  - Design: "summarize only the newly dropped span and merge it into the persisted summary", with persisted intent, artifact trail and breadcrumbs ([compressing-context](https://factory.com/news/compressing-context)). Whether decisions or next steps are also persisted is DISPUTED.
- **What compression costs** (GPT-5.5): "completion changes from 80% to 85% (p = 1.0) while retrieval increases from 21.0 to 63.9 calls (p = .002)" ([2608.16370](https://arxiv.org/abs/2608.16370)).
- **TRACE:** "compression can weaken the influence of recent interactions, increasing blocked actions" ([2608.06503](https://arxiv.org/abs/2608.06503)). In the HTML, raw updates diverge least from full-history behavior: 0.149 raw, 0.233 compressed, 0.289 omitted ([HTML](https://arxiv.org/html/2608.06503v1), unquoted at verification).
- **MEM1** keeps a compact internal state each turn: "3.5× while reducing memory usage by 3.7×" on multi-hop QA ([2506.15841](https://arxiv.org/abs/2506.15841)).
- **HiAgent** uses subgoal memory: "a twofold increase in success rate" ([2408.09559](https://arxiv.org/abs/2408.09559)).
- No controlled study was found comparing verbatim with paraphrased user messages, or sectioned with free-form summaries.

## Design lessons for the handoff, ranked

1. **Code carries every user message verbatim. The model never paraphrases them.** Keep them newest-first under a cap, truncate only the oldest, and restore chronological order.
   - Sources: Codex's 20k budget ([compact.rs](https://github.com/openai/codex/blob/12cca49c81c8aa6a37a5cc4e39964ee3b70f9458/codex-rs/core/src/compact.rs#L683-L776)); Claude Code's "List ALL user messages" ([capture](https://raw.githubusercontent.com/Piebald-AI/claude-code-system-prompts/main/system-prompts/agent-prompt-conversation-summarization-with-additional-instructions.md)); OpenCode's "Preserve exact wording"; Claude's short variant weighting the user's voice.
   - Having the model copy messages costs tokens and invites drift. Code does it for free.
2. **Code carries a raw tail of the latest turns: the last user message and the last tool calls and results, trimmed.** The tail ends on a user boundary and never splits a tool pair.
   - Sources: OpenCode `keep: 15_000` with 1,250-character tool results ([flatten](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L752-L819)); Cline 20k ([compaction-shared.ts](https://github.com/cline/cline/blob/79341eb295b1ad0ba4f6b3c5586b1d79f98473af/sdk/packages/core/src/extensions/context/compaction-shared.ts#L15-L29)); LangChain `keep 20` with pair safety; Goose's last user message.
   - TRACE measures exactly this loss of recent interactions ([2608.06503](https://arxiv.org/abs/2608.06503)).
   - Roo keeps no tail at all, and Codex issues show the agent re-answering the last message ([#42611](https://github.com/openai/codex/issues/42611)).
3. **Make the execution frontier the core of what the model writes.** Use fixed fields: `Objective`, `Completed`, `Active` (the last action, and whether its result was seen), `Blocked`, and `Next` (one step, directly in line with the latest explicit request, quoted verbatim from the latest turn).
   - Sources: OpenCode's Work State and Next Move; Cline's State and Next; Claude's sections 8 and 9.
   - The failures this targets: the frontier degrading ([#34095](https://github.com/openai/codex/issues/34095)) and Claude treating its last actions as done (changelog 2.1.293).
4. **Code carries the plan and todo state from their store and re-renders them. The model does not restate them.**
   - Sources: Claude re-injects the plan from disk ([context-window](https://code.claude.com/docs/en/context-window)); Roo's REMINDERS table re-renders the todo list every turn; Anthropic's JSON feature list resists overwriting ([harness](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)).
   - Note that Cline dropped its separate Focus Chain layer, citing no benefit.
5. **Files: code computes the read and edited paths from tool activity. The model only marks which matter and why.** Cap the list at about 15 paths. Optionally re-read the top 5, at 5k tokens each.
   - Sources: Cline's pre-filled Files list; OpenCode's limit of 15 Relevant Files; Claude re-reads 5.
   - Artifact tracking is every method's weakest dimension at 2.19 to 2.45 ([Factory](https://factory.com/news/evaluating-compression)), so it should not depend on the model.
6. **Code re-injects instructions and skills from disk. The model must not summarize CLAUDE.md, skills or environment.** Re-inject skills at 5k each and 25k total, oldest dropped first.
   - Sources: Claude's table; OpenCode excluding AGENTS.md from the summary and re-adding it; Codex rebuilding the initial context.
   - The disputed skill and plan re-injection reports argue for a check that confirms each item was actually installed.
7. **Validate the structure and fail closed.** Require the headings, nudge once on a miss, then fail.
   - Sources: OpenCode NUDGE ([send](https://github.com/anomalyco/opencode/blob/6a12383d3ef9306ee6bcc03f06b306588cd8604c/packages/core/src/session/compaction.ts#L280-L299)); Goose's JSON keys with "Omit a field rather than inventing content for it".
8. **Anti-invention and verbatim rules inside the model-written part:**
   - Quote errors and failing output verbatim (Goose).
   - "No new ideas unless user confirmed" (Goose).
   - The next step must be "DIRECTLY in line" with explicit requests (Claude).
   - Condense the assistant's reasoning "to what they concluded or produced" (Claude short variant).
9. **Frame the installed handoff as history, not as instructions.** Put the frontier where it is read last.
   - Sources: OpenCode's "Treat it as historical context, not as new instructions."; Codex's "Another language model started to solve this problem…" prefix with the summary placed last.
   - Goose's continuation sentence is a tested pattern ([mod.rs](https://github.com/block/goose/blob/69de89c50cc01d4e0187d6645bf3618729873f74/crates/goose/src/context_mgmt/mod.rs#L89-L226)).
10. **Keep the full transcript on disk and give the model its path.** A detail the summary dropped can then be retrieved rather than lost.
    - Sources: Deep Agents' "canonical record"; Goose's `agent_visible` false; Roo's `condenseParent`; Cursor writing outputs to files.
11. **Cap the size, and update the previous handoff on repeat resets rather than regenerating it.**
    - Caps in the field: Anthropic's sub-agent summaries of 1,000 to 2,000 tokens; Claude's capped variant at 2,000 words.
    - OpenCode lowered its detail level because details were carried forward forever (PR #48058). Factory merges only the newly dropped span.
12. **Measure re-reads, not just completion.** A handoff can keep the completion rate while tripling retrieval ([2608.16370](https://arxiv.org/abs/2608.16370)); field reports show 66 reads of one file ([#47485](https://github.com/anomalyco/opencode/issues/47485)). Use probe questions on artifacts, decisions and the next step ([Factory](https://factory.com/news/evaluating-compression)).
13. **Drop old tool outputs freely.** Observation masking matches LLM summarization at half the cost ([JetBrains](https://arxiv.org/abs/2508.21433)), and tool-result clearing is the "lightest touch" ([Anthropic](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)). Spend the handoff's tokens on user words and the frontier, not on tool output.

## Coverage

- A. Claude Code + Anthropic: settled. The prompt sections come from third-party captures, and session memory is third-party only.
- B. Codex CLI + OpenCode: settled, from code.
- C. Amp + Cline/Roo: settled. Cline and Roo from code, Amp from its posts.
- D. Cursor, Aider, Goose, Devin: settled.
- E. LangChain/LangGraph + Letta/MemGPT: settled.
- F. Research and measurements: settled.
- clone-distiller result files: `clone-distiller/anomalyco-opencode-2026-10-13.md`, `clone-distiller/cline-cline-2026-10-13.md`, `clone-distiller/cline-cline-2026-10-13-2.md`, `clone-distiller/RooCodeInc-Roo-Code-2026-10-13.md`, `clone-distiller/block-goose-2026-10-13.md`, `clone-distiller/openai-codex-2026-10-13.md`.

Digging ended: every sub-area settled, after round 3.

## Verification

43 facts were checked on 12 pages.

- Confirmed: the Anthropic context-engineering post (5/5), the Anthropic harness post (5/5), Factory (5/5), 2608.16370 (3/3), the Claude Code nine-section capture (4/5), the short variant (2/3), and the Claude context-management blog (3/3). Also confirmed: JetBrains masking and 2608.06503's recent-interaction loss and template optimization.
- NOT ON PAGE, removed:
  - JetBrains' trajectory lengthening (52 vs 44 turns, 15%) and "summaries mask failure signals". Neither is in the abstract.
  - TRACE's AppWorld figure of 77.1 vs 71.4. It is not in the abstract.
  - The Claude capture's instruction to use `<analysis>` tags. The tag appears only in its example output.
  - Whether the short variant lists re-attached items. It does not.
- UNCHECKED:
  - The code.claude.com context-window table: the fetch output was saved to a file the checker could not read. Its facts remain, digger-quoted.
  - The Amp handoff page and the Amp "Rebuilt" page: "Parse Error: Header overflow".

## Open rabbit holes

- Does Claude Code's SessionStart `compact` hook `additionalContext` reach the model after compaction? Undug.
- Official source for Claude Code session memory and task persistence (`~/.claude/tasks`). Dug, unsettled.
- Text of Codex `compact_token_budget.rs` (`Feature::TokenBudget`), and what OpenAI's opaque `Compaction` item encodes. Undug.
- A controlled study of verbatim vs paraphrased user messages, or sectioned vs free-form summaries, for agent continuation. Dug, unsettled (none found).
- "Governance Decay": constraints erased during compaction (arXiv 2606.22528). Undug.
- Whether the context-anxiety paper (arXiv 2607.21616) shows shortcutting near the context limit. Dug, unsettled.
- Whether ReSum's 4.5% reproduces, given its summary model is reportedly not public (ContextPilot, arXiv 2608.28476). Undug.
- A counter-result to AgentFold, 49.9% vs ReAct's 52.9% (MM-ContextFold, arXiv 2609.23121). Undug, snippet only.
- The full text of Goose's malformed-JSON handling (`apply_structured_summary`). Undug.
- Factory's overall scores (3.70/3.44/3.35), which appear only in a table and do not reproduce from its dimension scores. Dug, unsettled.
