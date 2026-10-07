# RR — How should an LLM watcher agent keep a true, bounded, provenance-carrying memory of a coding-agent chat and time its interventions?

Question: how should an LLM "watcher" agent keep a living memory of a long-running coding-agent chat it observes and judges, so its memory stays true, carries what matters, and drives well-timed interventions? Map memory architectures (write/update/forget), provenance and grounding, cross-session continuity, runtime monitor/critic agents and intervention policy, and memory evaluation — built and measured mechanisms, not opinions — and rank concrete mechanisms adoptable by "buddy" against its measured defects.

## Answer

The systems that were built and measured converge on four things.
- **Attribute every note to its source.** Keep memories tagged by source ([mem0 `attributed_to`](https://raw.githubusercontent.com/mem0ai/mem0/main/mem0/configs/prompts.py)), with citations that "may include direct user quotes" ([GitHub Copilot memory](https://docs.github.com/en/copilot/concepts/agents/copilot-memory)).
- **Supersede old notes explicitly, never by wholesale rewrite.** Decide at write time whether each old note stays active, becomes stale, is replaced or is unresolved. That lifted accuracy "from 8.7% to 68.0%" ([STALE](https://arxiv.org/html/2605.06527)). Contradicted facts are invalidated rather than deleted, with a guard against merging facts whose numbers differ ([Graphiti prompt](https://raw.githubusercontent.com/getzep/graphiti/main/graphiti_core/prompts/dedupe_edges.py)).
- **Re-check a fact against current evidence before using it** ([Copilot](https://docs.github.com/en/copilot/concepts/agents/copilot-memory)).
- **Escalate repeated misbehaviour instead of re-issuing the same warning.** One example is Ctrl-Z's resample-then-audit after a third strike ([Ctrl-Z](https://arxiv.org/html/2504.10374)). Another is a cheap filter that escalates only flagged cases to a reasoning stage that does not see the agent's own prose ([Anthropic auto mode](https://www.anthropic.com/engineering/claude-code-auto-mode)).

## Ranked mechanisms for buddy

Defect key:
- D1: notes built from wrong numbers persist as doubts.
- D2: the watcher's misreading is saved as a rule in the user's name.
- D3: stale counts are copied forward.
- D4: standing rules are dropped at handoff or by the 8-note cap.
- D5: absence is claimed from a truncated view.
- D6: the same intervention is repeated 7 times.
- D7: strong verdicts are shown only on screen while the user is away.

1. **Source-attributed notes, with user rules kept verbatim** (fixes D2, D4)
   - What it is: every note carries who said it (user, assistant or watcher-inferred) and a pointer to its source. User rules keep the user's words.
   - Who built it:
     - mem0's extraction prompts have a user-only mode, "GENERATE FACTS SOLELY BASED ON THE USER'S MESSAGES. DO NOT INCLUDE INFORMATION FROM ASSISTANT OR SYSTEM MESSAGES.", and an `attributed_to` field ("Use 'user' for facts stated by or about the user... Use 'assistant' for information provided by the assistant") ([prompts.py](https://raw.githubusercontent.com/mem0ai/mem0/main/mem0/configs/prompts.py)).
     - mem0 also writes a history row with `actor_id` and `role` on every ADD, UPDATE and DELETE ([main.py](https://raw.githubusercontent.com/mem0ai/mem0/main/mem0/memory/main.py)).
     - Copilot: "User-level preferences are stored with citations that may include direct user quotes" ([docs](https://docs.github.com/en/copilot/concepts/agents/copilot-memory)).
     - Codex memories require `### rollout_summary_files` per task, so each memory points back to its session ([consolidation.md](https://raw.githubusercontent.com/openai/codex/main/codex-rs/memories/write/templates/memories/consolidation.md)).
   - Evidence it works: Copilot reports "90% with memories vs. 83% without" PR merge rate ([GitHub blog](https://github.blog/ai-and-ml/github-copilot/building-an-agentic-memory-system-for-github-copilot/)). Ground Truth First finds a "provenance-typed graph rises to 90%" recall at nine weeks, while a budgeted curated map falls "96% to 72%" ([arXiv 2607.21962](https://arxiv.org/abs/2607.21962)).
   - Maps to: a "rule" note is valid only with a verbatim user quote and a turn id. A watcher inference can never be typed as a user rule.

2. **Explicit, operation-based supersession instead of wholesale rewrite** (fixes D1, D3)
   - What it is: on each turn the model emits operations over identified notes rather than rewriting the list. Old notes are adjudicated, not silently dropped or copied.
   - Who built or measured it:
     - mem0's update prompt offers "ADD … UPDATE … DELETE … NONE" ([prompts.py](https://raw.githubusercontent.com/mem0ai/mem0/main/mem0/configs/prompts.py)).
     - STALE/CUPMem: "An LLM-based adjudicator evaluates each candidate old state and decides whether it should remain active, be archived as STALE, be replaced, or be marked unresolved." That took GPT-4o-mini "from 8.7% to 68.0%". Without it, "Even the best evaluated model achieving only 55.2% overall accuracy" ([arXiv 2605.06527](https://arxiv.org/html/2605.06527)).
     - Graphiti asks "Determine which facts the NEW FACT contradicts from either list." and warns "NEVER mark facts as duplicates if they have key differences, particularly around numeric values, dates, or key qualifiers." ([dedupe_edges.py](https://raw.githubusercontent.com/getzep/graphiti/main/graphiti_core/prompts/dedupe_edges.py)). It then sets `invalid_at` and `expired_at` and appends the source episode, rather than deleting ([edge_operations.py](https://raw.githubusercontent.com/getzep/graphiti/main/graphiti_core/utils/maintenance/edge_operations.py)).
   - Evidence it works: STALE above. A 12-system study found "Graph-based methods handle knowledge updates most reliably, whereas popular fact-extraction plugins and append-only stores struggle with targeted overwrites". Letta, which rewrites blocks, scored 0.0 Substring EM on knowledge update ([arXiv 2606.24775](https://arxiv.org/html/2606.24775)).
   - Maps to: a doubt built on a number is invalidated when a newer numbers line contradicts it. The Graphiti numeric guard stops "same fact, different count" from being carried forward.

3. **Re-validate before use, and expire what is unused** (fixes D1, D3)
   - Who built it: Copilot "checks those citations against the current branch to confirm the information is still accurate. Only validated facts are used". Any "fact or preference that goes unused is automatically deleted after 28 days" ([docs](https://docs.github.com/en/copilot/concepts/agents/copilot-memory)).
   - Related rule: Codex consolidation says "fresher validated evidence usually wins" and "Delete only memory supported by deleted inputs" ([consolidation.md](https://raw.githubusercontent.com/openai/codex/main/codex-rs/memories/write/templates/memories/consolidation.md)).
   - Maps to: counts are never notes. Recompute them each turn from the numbers line, and keep in memory only the claim plus the turn whose numbers support it.

4. **A pinned standing-rules store outside the capped working notes, persisted across handoff** (fixes D4)
   - Who built it:
     - Letta's read-only blocks "prevent agent modification by setting `read_only: true`", and core blocks are "always visible - no retrieval needed" ([Letta memory blocks](https://docs.letta.com/guides/core-concepts/memory/memory-blocks)).
     - In Claude Code, "Project-root CLAUDE.md survives compaction", while "If an instruction disappeared after compaction, it was given only in conversation" ([memory docs](https://code.claude.com/docs/en/memory)).
     - Anthropic's compaction API states "their text instructions stop applying once the block replaces them" for summarized system messages ([compaction docs](https://platform.claude.com/docs/en/build-with-claude/compaction)).
   - Evidence the risk is real:
     - LLMs show "an average drop of 39%" in multi-turn settings ([arXiv 2505.06120](https://arxiv.org/abs/2505.06120)).
     - Instruction drift sets in "within eight rounds" ([arXiv 2402.10962](https://arxiv.org/html/2402.10962)).
   - Maps to: user rules live in a separate per-project store that the 8-note cap cannot evict. The store is re-read by the next chat's watcher.

5. **Strike counting, de-duplication and an escalation ladder instead of repeating** (fixes D6)
   - Who built or measured it:
     - Ctrl-Z resamples above a deferral threshold and audits "Once this happens for the third time", reducing attacks "from 58% to 7% at a 5% cost" ([arXiv 2504.10374](https://arxiv.org/abs/2504.10374)).
     - PagerDuty: "Subsequent alerts with a matching `dedup_key` deduplicate into the same incident" ([PagerDuty](https://support.pagerduty.com/docs/event-management)).
     - Alertmanager waits `repeat_interval` (default `4h`) before repeating the same notification ([Prometheus docs](https://prometheus.io/docs/alerting/latest/configuration/)).
     - Pydantic's harness caps a reminder with `max_fires` and never persists it to history ([pydantic](https://pydantic.dev/docs/ai/harness/system-reminders/)).
   - Evidence that repetition decays: "fMRI activation continued to decrease over all six repetitions" of a warning ([Frontiers 2020](https://www.frontiersin.org/journals/psychology/articles/10.3389/fpsyg.2020.528079/full)).
   - Maps to: key each intervention on the rule it enforces. On a repeat, escalate (inject, then block via Stop hook, then notify the user) instead of re-injecting the same text.

6. **A two-stage judge that is blind to the agent's own claims and grounded in external signals** (fixes D1, D2)
   - Who built it: Anthropic auto mode strips "Assistant prose, tool-call descriptions, and tool outputs … only user messages and the executable payload remain". Stage 1 is "a fast filter tuned to cautious; only what it flags escalates to stage 2". Results: 0.4% false positives on 10,000 real calls and 17% false negatives on 52 real overeager actions ([Anthropic](https://www.anthropic.com/engineering/claude-code-auto-mode)).
   - Supporting evidence:
     - "LLMs struggle to self-correct their responses without external feedback" ([arXiv 2310.01798](https://arxiv.org/abs/2310.01798)).
     - "monitor scaffolding matters more than monitor awareness" ([arXiv 2508.19461](https://arxiv.org/abs/2508.19461)).
   - Maps to: verdicts and notes derive from tool results and numbers, not from Claude's narrative in the head or tail of its message.

7. **Truncation-aware abstention on absence claims** (fixes D5)
   - Evidence: AbsenceBench: "Claude-3.7-Sonnet achieve only 69.6% F1-score" at about 5K tokens, because absences "don't correspond to any specific keys" ([arXiv 2506.11440](https://arxiv.org/abs/2506.11440)).
   - Who built a fix: Compression-aware abstention addresses hallucination "when the evicted tokens contain answer-bearing evidence" ([arXiv 2608.29934](https://arxiv.org/abs/2608.29934)). LongMemEval scores abstention as a core ability ([arXiv 2410.10813](https://arxiv.org/html/2410.10813)).
   - Maps to: mark the view as truncated in the input. Ban "not done" or "never ran" claims unless the elided span is covered by the step summary.

8. **Tiered alerts and away-aware delivery** (fixes D7)
   - Evidence:
     - Shah 2006: "12,933 (71%) were noninterruptive and 5,182 (29%) interruptive"; "Of the 5,182 interruptive alerts, 67% were accepted" ([AHRQ](https://digital.ahrq.gov/health-it-tools-and-resources/workflow-assessment-health-it-toolkit/research/shah-nr-et-al-2006)).
     - Proactive coding help: suggestions come "after 5 seconds, limited to every 20 seconds" when the user is idle, and "immediately … when the user runs or submits code that leads to an error". Only "47%" preferred the high-frequency variant ([arXiv 2410.04596](https://arxiv.org/html/2410.04596)).
   - Who built it: Claude Code exposes Notification matchers such as `idle_prompt` and `agent_needs_input` ([hooks](https://code.claude.com/docs/en/hooks)).
   - Maps to: WRONG verdicts are interruptive and reach the user through a notification when the prompt is idle. SHORTCUT verdicts are non-interruptive.

9. **An anchored, sectioned handoff summary with a separate artifact ledger** (fixes D4 and handoff loss)
   - Evidence: Factory's anchored summary keeps "session intent, file modifications, decisions made, and next steps". It scored 3.70 overall, against 3.44 for Anthropic and 3.35 for OpenAI, over "36,611 messages". Artifact trail was the weakest dimension for every method (2.45, 2.33 and 2.19) ([Factory](https://factory.com/news/evaluating-compression)).
   - Related: ACON learns compression guidelines from failures, cutting "peak token usage by 26–54%" ([arXiv 2510.00615](https://arxiv.org/pdf/2510.00615v1)).
   - Caveat: this is a vendor self-evaluation.

10. **Hierarchical or sequential monitor summaries over chunks** (scaffold for the watcher's notes)
    - Who built it: a sequential monitor scores each chunk "with the previous summary in context". Escalating "only pre-flagged cases to human reviewers improved the TPR by approximately 15% at FPR = 0.01" ([arXiv 2508.19461](https://arxiv.org/html/2508.19461)).

11. **A memory regression suite built from the defect logs** (tests all of the above)
    - Ground Truth First seeds facts "with validity intervals, volatility classes, and source channels before any text exists" ([arXiv 2607.21962](https://arxiv.org/abs/2607.21962)).
    - MemoryAgentBench FactConsolidation orders a "rewritten (new) fact" after the original; "all methods fail on the multi-hop situation (with achieving at most 28% accuracy)" ([arXiv 2507.05257](https://arxiv.org/html/2507.05257)).

### Adopt first (five)

1. **Source-attributed notes with verbatim user rules (#1).** This removes D2 at the root, and it is cheap: a `source: user|claude|watcher` field plus a turn id and quote.
2. **Operation-based supersession with write-time adjudication (#2).** This is the best-measured fix here (STALE, 8.7% to 68.0%), and it replaces the wholesale rewrite that lets wrong numbers and stale counts survive (D1, D3).
3. **A pinned rules store outside the cap, persisted per project (#4).** D4 is a structural loss, which no prompt fix can address. Claude Code and Letta both pin standing rules outside compaction.
4. **Strike counting with de-duplication and an escalation ladder (#5).** This turns 7 identical injections (D6) into inject, block, notify. It is backed by Ctrl-Z, alert-dedup practice and habituation data.
5. **Away-aware tiered delivery (#8) together with truncation-aware abstention (#7).** Both are small, deterministic changes in the adapter and prompt that close D7 and D5.

## Map

### A. Memory write/update/forget policies
- **mem0**
  - Write and supersede: extraction uses a summary plus recent messages. The update step compares each fact with the top-s similar memories and chooses ADD, UPDATE, DELETE or NONE ([prompts.py](https://raw.githubusercontent.com/mem0ai/mem0/main/mem0/configs/prompts.py); [arXiv 2504.19413](https://arxiv.org/html/2504.19413)).
  - Delete with history: DELETE removes the vector, and the history table records prev_value, role and actor_id ([main.py](https://raw.githubusercontent.com/mem0ai/mem0/main/mem0/memory/main.py)).
  - Mem0g marks relations invalid "rather than physically removing them" ([arXiv](https://arxiv.org/html/2504.19413)).
  - Decay is "opt-in per project and off by default" ([docs](https://docs.mem0.ai/platform/features/memory-decay)).
- **Zep/Graphiti**
  - Four timestamps: created, expired, valid and invalid.
  - On contradiction, it sets the old edge's `tinvalid` to the new edge's `tvalid`.
  - Episodes and edges keep bidirectional provenance indices ([arXiv 2501.13956](https://arxiv.org/abs/2501.13956); code in edge_operations.py and dedupe_edges.py above).
  - LongMemEval with gpt-4o-mini: 63.8% for Zep against 55.4% for full context (unquoted).
- **Letta/MemGPT**
  - `CORE_MEMORY_BLOCK_CHAR_LIMIT: int = 5000` ([constants.py @0.8.0](https://raw.githubusercontent.com/letta-ai/letta/0.8.0/letta/constants.py)).
  - Edit tools: core_memory_append and core_memory_replace (where "use an empty string for new_content" deletes), memory_rethink and memory_insert ([base.py @0.8.0](https://raw.githubusercontent.com/letta-ai/letta/0.8.0/letta/functions/function_sets/base.py)).
  - A warning tells the agent to save before trimming. Edits overwrite in place, with no history field.
  - Sleep-time agents own the rewrite tools (`BASE_SLEEPTIME_TOOLS`).
- **LangMem**
  - `enable_deletes` covers memories "outdated or contradicted by new information".
  - Reflection is debounced "whenever the user is actively engaging" ([conceptual guide](https://langchain-ai.github.io/langmem/concepts/conceptual_guide/)).
- **A-MEM**
  - Notes carry keywords, tags and context, and are linked to the top-k nearest notes.
  - "Memory evolution" rewrites older notes' context ([arXiv 2502.12110](https://arxiv.org/html/2502.12110)).
- **Generative Agents**
  - Importance is scored 1–10, and recency decays at 0.995.
  - Retrieval adds recency, importance and relevance with α=1.
  - Reflection fires when summed importance "exceeds a threshold (150 in our implementation)".
  - Reflections cite evidence ids ([ar5iv 2304.03442](https://ar5iv.labs.arxiv.org/html/2304.03442)).
- **MemoryOS**: heat-based promotion and eviction across short, mid and long tiers (unquoted) ([arXiv 2506.06326](https://arxiv.org/html/2506.06326)).
- **Cross-system comparison**: "Graph-based methods handle knowledge updates most reliably", and "localized maintenance is more cost-efficient than global reorganization" ([arXiv 2606.24775](https://arxiv.org/html/2606.24775)).

### B. Provenance, grounding, staleness
- **Copilot**: quotes, citations validated against the current branch, and 28-day expiry ([docs](https://docs.github.com/en/copilot/concepts/agents/copilot-memory)).
- **mem0**: `attributed_to` and user-only extraction (above).
- **Hindsight**
  - Separates world facts, experiences, entity summaries and opinions into "four logical networks, each serving a distinct epistemic role".
  - Opinions carry "c∈[0,1] as a confidence score representing belief strength".
  - The update rule is c′ = min(c+α,1) on reinforce, max(c−α,0) on weaken and max(c−2α,0) on contradict ([arXiv 2512.12818](https://arxiv.org/html/2512.12818)).
- **STALE/CUPMem**: write-time adjudication (above). A model gets "76.0% on Type I-SR but only 39.0% on Type I-IPA", that is, knowing a memory is stale is not the same as acting on it ([arXiv 2605.06527](https://arxiv.org/html/2605.06527)).
- **Experience-following**: "inaccuracies in past experiences compound and degrade future performance" ([arXiv 2505.16067](https://arxiv.org/abs/2505.16067)).
- **A-MemGuard**: consensus validation and a separate "lessons" memory "cuts attack success rates by over 95%" ([arXiv 2510.02373](https://arxiv.org/abs/2510.02373)).
- **Absence**: see AbsenceBench and compression-aware abstention above.

### C. Continuity across sessions and handoffs
- **Claude Code**
  - Auto memory loads "The first 200 lines of `MEMORY.md`, or the first 25KB".
  - Memory files carry a `modified` timestamp.
  - CLAUDE.md survives compaction ([memory docs](https://code.claude.com/docs/en/memory); [context window](https://code.claude.com/docs/en/context-window)).
- **Anthropic API**
  - The memory tool is client-side ([memory tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool)).
  - Compaction `instructions` of up to 16,384 characters replace the default summarization prompt ([compaction](https://platform.claude.com/docs/en/build-with-claude/compaction)).
- **Codex**
  - AGENTS.md is concatenated root-down, capped by `project_doc_max_bytes` (32 KiB) ([agents-md](https://learn.chatgpt.com/docs/agent-configuration/agents-md)).
  - Memories are "off by default", skip short sessions and redact secrets ([memories](https://learn.chatgpt.com/docs/customization/memories?surface=app)).
  - Consolidation keeps rollout provenance and, on unclear conflict, says to "preserve the uncertainty explicitly" ([consolidation.md](https://raw.githubusercontent.com/openai/codex/main/codex-rs/memories/write/templates/memories/consolidation.md)).
  - The OpenAI compaction item is "opaque and not intended to be human-interpretable" ([guide](https://developers.openai.com/api/docs/guides/compaction.md)).
- **ChatGPT**: saved memories are separate from chat-history reference, and "Deleting the original chat does not automatically delete a separate saved memory" ([FAQ](https://help.openai.com/en/articles/8590148-memory-faq)).
- **Measured handoff loss**
  - Factory probe evaluation (above).
  - Chroma: "Significantly higher performance on focused prompts compared to full prompts" ([context rot](https://www.trychroma.com/research/context-rot)).
  - Multi-IF: o1-preview "drops from 0.877 at the first turn to 0.707 at the third turn" ([arXiv 2410.15553](https://arxiv.org/abs/2410.15553)).

### D. Runtime monitors and critics
- **AI Control trusted monitoring**: audit threshold set at a 2% false-positive rate on benign outputs; 62% safety at 97% usefulness (unquoted) ([arXiv 2312.06942](https://arxiv.org/abs/2312.06942)).
- **Ctrl-Z**: resample above the deferral threshold, audit when the minimum over samples exceeds the audit threshold, and three strikes trigger an audit ([arXiv 2504.10374](https://arxiv.org/html/2504.10374)).
- **SHADE-Arena**: the best monitor reaches "an AUC of 0.87" ([arXiv 2506.15740](https://arxiv.org/abs/2506.15740)).
- **CoT monitoring**: GPT-4o "can effectively monitor a stronger model". Under optimization pressure, agents learn "obfuscated reward hacking" ([arXiv 2503.11926](https://arxiv.org/abs/2503.11926)).
- **CriticGPT**: critiques preferred "in 63% of cases". Human-plus-critic teams hallucinate less. Force sampling beam search trades bug recall against nitpicks, and the paper says "it is not obvious what the right tradeoff … is" ([arXiv 2407.00215](https://arxiv.org/html/2407.00215v1)).
- **Reflexion**: an episodic reflection buffer reached "91% pass@1" on HumanEval ([arXiv 2303.11366](https://arxiv.org/abs/2303.11366)).
- **Self-Refine**: "~20% absolute" ([arXiv 2303.17651](https://arxiv.org/abs/2303.17651)).
- **LlamaFirewall AlignmentCheck**: attack success from 0.18 to 0.03 ([arXiv 2505.03574](https://arxiv.org/html/2505.03574)).
- **Process supervision**: 78% on a MATH subset ([arXiv 2305.20050](https://arxiv.org/abs/2305.20050)).
- **Auto mode and weak-to-strong monitors**: see #6 and #10 above.

### E. Evaluating memory
- **LongMemEval**
  - Five abilities, including knowledge updates and abstention.
  - "Decomposing sessions into rounds significantly enhances reading performance."
  - Time-aware query expansion "improves recall by an average of 11.3%" ([arXiv 2410.10813](https://arxiv.org/html/2410.10813)).
- **LoCoMo**
  - 50 dialogues of about 300 turns ([arXiv 2402.17753](https://arxiv.org/html/2402.17753)).
  - Disputes: Zep says Mem0 misconfigured Zep and reports 75.14% against Mem0's reported 65.99% ([Zep](https://www.getzep.com/blog/lies-damn-lies-statistics-is-mem0-really-sota-in-agent-memory/)). Letta's plain-file agent scored "74.0% on LoCoMo" ([Letta](https://www.letta.com/blog/benchmarking-ai-agent-memory)). All of these are vendor self-reports.
- **MemoryAgentBench FactConsolidation**: multi-hop conflict at most 28% ([arXiv 2507.05257](https://arxiv.org/html/2507.05257)).
- **Ground Truth First**: validity-interval ground truth, with question types for conflict and expired state ([arXiv 2607.21962](https://arxiv.org/abs/2607.21962)).
- **GateMem (deletion)**: "no method simultaneously achieves strong utility, robust access control, and reliable forgetting" ([papers.cool 2606.18829](https://papers.cool/arxiv/2606.18829)).
- **STALE**: as above.

### F. Intervention timing, de-duplication, alarm fatigue, human-away
- **Proactive coding assistant timing**: see #8 ([arXiv 2410.04596](https://arxiv.org/html/2410.04596)).
- **Proactive Agent**: fine-tuned Qwen2-7B "66.47% F1-Score" ([arXiv 2410.12361](https://arxiv.org/html/2410.12361)).
- **Clinical drug alerts**: "49-96% of drug safety alerts are overridden" ([van der Sijs ref](https://textbookofdigitalhealth.com/references/h2006overriding.html)). Shah tiering: see #8.
- **SRE**: multiwindow, multi-burn-rate alerts stop firing "five minutes later, rather than one hour later" ([SRE workbook](https://sre.google/workbook/alerting-on-slos/)).
- **De-duplication and repeat suppression**: Alertmanager, PagerDuty and pydantic `max_fires` (see #5).
- **Claude Code Stop hook**
  - Exit code 2 "Prevents Claude from stopping" ([hooks](https://code.claude.com/docs/en/hooks)).
  - The consecutive-block cap of 8 and `stop_hook_active` are described only on a community issue mirror ([claudeissues 59583](https://claudeissues.com/issue/59583-docs-stop-hook-docs-omit-consecutive-block-cap-and-override-env-var)). This is unverified against primary docs.
  - `idle_prompt` has a 60-second delay according to a feature request ([claudeissues 13922](https://claudeissues.com/issue/13922-feature-configurable-timeout-for-idle-prompt-notification-hook)). This is unverified against primary docs.
- **Away notifications elsewhere**: Devin per-run Slack notifications ([fast.io guide](https://www.fast.io/resources/devin-ai-slack-integration.md)). Cursor desktop notifications "in version 1.5.x" ([forum](https://forum.cursor.com/t/push-notifications-mobile-desktop-when-agent-needs-user-input-or-approval/148230)).
- **Instruction drift**: system-prompt repetition "excels in regions with a larger number of turns", while split-softmax is better early ([arXiv 2402.10962](https://arxiv.org/html/2402.10962)).

## Coverage

| Sub-area | Status | Evidence |
|---|---|---|
| A. Memory write/update/forget policies | settled | Graphiti, mem0, Letta code; LangMem, A-MEM, Generative Agents, MemoryOS |
| B. Provenance, grounding, staleness | settled | Copilot, mem0 attribution, Hindsight, STALE, Graphiti |
| C. Cross-session continuity | settled | Claude Code, Codex, ChatGPT, Letta; Factory, ACON |
| D. Runtime monitors and critics | settled | AI Control, Ctrl-Z, weak-to-strong, auto mode, CriticGPT, SHADE-Arena |
| E. Memory evaluation | settled | LongMemEval, LoCoMo, MemoryAgentBench, Ground Truth First, GateMem |
| F. Intervention timing and alarm fatigue | settled | CHI proactive, Shah, SRE, Alertmanager/PagerDuty, habituation, Claude Code Notification |

Digging ended: every sub-area settled (after round 3).

## Verification

- 38 facts were checked on 12 pages: Copilot docs, STALE, Ground Truth First, Factory, Ctrl-Z, Anthropic auto mode, Graphiti prompt, mem0 prompts, CHI proactive, arXiv 2606.24775, Hindsight and arXiv 2508.19461.
- 37 were confirmed.
- NOT ON PAGE: the Hindsight opinion tuple "(t,c,τ)". The paper gives (t,c,τ,b,ℰ), so the tuple wording was dropped. The confidence c∈[0,1] and the update rule were confirmed.
- UNCHECKED: none.

## Open rabbit holes

- **Undug**
  - The primary Claude Code changelog entry for the Stop-hook consecutive-block cap (8) and `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP`; only a community mirror backs it.
  - Copilot's memory A/B gives no ablation of the citation-validation step, so its marginal value is unmeasured.
  - The CUPMem prompt and its need for predefined state schemas, which matters for free-form chat notes.
  - Codex stage-one extraction prompt (`stage_one_system.md`) and the `max_unused_days` default.
  - The Letta summarizer trigger fraction (`MESSAGE_SUMMARY_WARNING_FRAC`) and where the 5000-character limit is enforced.
  - The independent evaluation reporting an 81% false-negative rate for auto mode on ambiguous scenarios (search snippet only).
  - Mem0's rebuttal of Zep's LoCoMo critique.
  - Memora, MEMTRACK and the "Control-Plane Placement Shapes Forgetting" benchmark.
- **Dug, unsettled**
  - No study measures LLM agents ignoring repeated identical feedback, that is, an LLM-side analogue of warning habituation.
  - Numeric stability values for system-prompt repetition, which sit only in figures in arXiv 2402.10962.
  - No measured rate of attribution error (assistant statements stored as user facts) was found.
  - Ctrl-Z per-protocol table numbers; the fetched cells looked garbled.
  - The chunk size and baseline TPR for the hierarchical and sequential monitors in arXiv 2508.19461 (appendix).
