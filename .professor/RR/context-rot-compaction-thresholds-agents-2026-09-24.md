# RR — How LLM performance degrades as the context fills, and which context-management strategies measurably preserve it

Question: map how LLM task performance degrades as the context window fills ("context rot"), and which context-management strategies measurably preserve it — so a fleet of Claude Code agents can set evidence-based thresholds for when a long-running sub-agent should compact, clear, or hand off. (1) degradation curve, (2) distractors vs neutral filler, (3) agentic workloads, (4) mitigations with measured effect, (5) practical thresholds and absolute tokens vs fill fraction.

## Answer

Accuracy falls steadily as input grows, not off a single cliff. On any task that is not literal lookup, the fall starts at tens of thousands of tokens, far below the advertised window: on NoLiMa, 11 models are below half their baseline by 32K. It is much steeper when the context holds near-miss content, such as literal-overlap distractors, stale updates or the agent's own earlier errors. How well performance holds at 1M varies sharply by model: 26.3% for Gemini 3.1 Pro against 76–78.3% for Opus 4.6 on 1M 8-needle MRCR v2. Among measured mitigations, cheap tool-output clearing and observation masking match or beat LLM summarization at roughly half the cost. Sub-agent isolation and memory report large gains, but only on vendor-internal evals. No source measures an optimal compaction point: the published triggers (70% / ~83.5% / 90% of the window, or 100k–150k tokens) are vendor defaults, not findings.

## Map

### 1. The degradation curve (settled)

- **Gradual, universal, measured at small lengths.** Chroma tested 18 LLMs and found "models do not use their context uniformly; instead, their performance grows increasingly unreliable as input length grows" ([Chroma, Jul 2025](https://www.trychroma.com/research/context-rot)). On LongMemEval, "significantly higher performance on focused prompts compared to full prompts": the focused prompts average about 300 tokens and the full ones about 113k ([Chroma](https://www.trychroma.com/research/context-rot)).
- **NoLiMa (no literal overlap between question and needle):** "At 32K, for instance, 11 models drop below 50% of their strong short-length baselines." GPT-4o has a base score of 99.3 and scores 69.7 at 32K ([NoLiMa, arXiv 2502.05167](https://arxiv.org/html/2502.05167v3)). Its per-length row falls smoothly: 98.1 at 1K, 98.0 at 2K, 95.7 at 4K, 89.2 at 8K, 81.6 at 16K, 69.7 at 32K. That is a gradual slope, not a step ([same](https://arxiv.org/html/2502.05167v3)).
- **RULER: effective context is often half the claimed window or less.** "While all models claim context size of 32k tokens or greater, only half of them can effectively handle sequence length of 32K." GPT-4 claims 128K and is effective to 64K; Yi-34B claims 200K and is effective to 32K ([RULER, arXiv 2404.06654](https://arxiv.org/html/2404.06654)).
- **Position (Lost in the Middle):** with 20 documents, GPT-3.5-Turbo scores 75.8% when the answer document comes first, 53.8% in the middle and 63.2% at the end. The middle score is below its closed-book 56.1% ([Liu et al., arXiv 2307.03172](https://arxiv.org/html/2307.03172)).
- **LongBench v2:** the best direct-answer model reaches 50.1% and o1-preview 57.7%, against 53.7% for human experts. The length buckets are confounded, because human accuracy itself falls from 59.1% (short) to 47.2% (long) ([arXiv 2412.15204](https://arxiv.org/html/2412.15204)).
- **Frontier 1M-token models:**
  - Gemini 3.1 Pro scores 84.9% on MRCR v2 (8-needle) at 128k (average) and 26.3% at 1M (pointwise). Gemini 3 Pro scores 77.0% and 26.3% ([Gemini 3.1 Pro model card](https://deepmind.google/models/model-cards/gemini-3-1-pro/)).
  - "On the 8-needle 1M variant of MRCR v2 … Opus 4.6 scores 76%, whereas Sonnet 4.5 scores just 18.5%" ([Anthropic, Opus 4.6](https://www.anthropic.com/news/claude-opus-4-6)).
  - **DISPUTED (two Anthropic pages disagree):** the later GA post says "Opus 4.6 scores 78.3% on MRCR v2" and "Claude Opus 4.6 and Sonnet 4.6 maintain accuracy across the full 1M window" ([claude.com 1M GA, Mar 13 2026](https://claude.com/blog/1m-context-ga)), against the 76% above. "Maintain accuracy" is a vendor claim; the page shows no length curve behind it.
  - GPT-4.1 on OpenAI-MRCR 2-needle scores 57.2% at 128k and 46.3% at 1M. On Graphwalks BFS it scores 61.7% below 128k and 19.0% above ([OpenAI GPT-4.1](https://openai.com/index/gpt-4-1/)). UNCHECKED: the verification fetch returned HTTP 403.
  - GPT-5.2 Thinking on 8-needle MRCR v2 scores 98.2% at 4k–8k and 77.0% at 128k–256k; GPT-5.1 Thinking falls from 65.3% to 29.6% over the same range. GPT-5.2 is "the first model we've seen that achieves near 100% accuracy on the 4-needle MRCR variant (out to 256k tokens)" ([OpenAI GPT-5.2](https://openai.com/index/introducing-gpt-5-2/)).
- **Shape verdict:** every fetched per-length table (NoLiMa, GPT-5.2 MRCR, Gemini 128k vs 1M) shows a continuous decline. Its slope depends heavily on the task: Graphwalks falls far faster than MRCR. It also depends on the model: at 1M, Opus 4.6 scores 76% against 18.5% for Sonnet 4.5. **No source states the fraction of the window at which accuracy halves.**

### 2. Distractors and semantic similarity (settled)

- **Distractors hurt beyond length alone:** "Even a single distractor reduces performance relative to the baseline (needle only), and adding four distractors compounds this degradation further." Also: "performance degrades more quickly in input length with lower similarity needle-question pairs" ([Chroma](https://www.trychroma.com/research/context-rot)). Chroma publishes no numeric delta in its text; the figures are charts only.
- **Literal-overlap distractors are the worst case.** With them, "GPT-4o now demonstrates an effective length of just 1K", against 8K without them. At 32K, Llama 3.3 70B scores 98.5 on literal-match (direct) needles, 56.2 on one-hop and 25.9 on two-hop ([NoLiMa](https://arxiv.org/html/2502.05167v3)).
- **Neutral filler still hurts, only less** ([Du et al., arXiv 2510.05381](https://arxiv.org/html/2510.05381)):
  - Headline: "performance still degrades substantially (13.9%–85%) as input length increases but remains well within the models' claimed lengths."
  - Whitespace: "at least 7% at 30K space tokens."
  - Masked tokens: "at least 7.9% for both models at 30K masked distraction tokens."
  - Worst case: Claude-3.5 on MMLU is −41.7 at 7,500 tokens and −67.6 at 30,000 tokens in the whitespace condition.
  - Mitigation: "recite-then-solve" gains up to 4% on QA2 at 32K.
- **Related distractors beat unrelated ones:** in GSM-IC, in-topic distractors give 70.8% micro / 23.5% macro accuracy, against 83.4% / 45.0% off-topic. "in-topic sentences with role name overlap and in-range numbers are generally more challenging" ([Shi et al., ar5iv 2302.00093](https://ar5iv.labs.arxiv.org/html/2302.00093)).
- **Stale versions of the same content:** "Retrieval accuracy declines log-linearly toward zero as interference accumulates." llama4-maverick scores "nearly 100% accuracy when tracking just two keys, but this drops below 5% when tracking 46 keys." Here, "context length has no significant effect (t = –0.144, p = 0.886)" ([PI-LLM, arXiv 2506.08184](https://ar5iv.labs.arxiv.org/html/2506.08184)). This is the closest analogue to an agent's context filling up with superseded file reads and tool outputs.
- **Hard negatives in retrieval:** "increasing the number of retrieved passages initially improves performance, but then leads to a sharp decline or plateau." "LLMs struggle more with hard negatives from stronger retrievers" ([Jin et al., ar5iv 2410.05983](https://ar5iv.labs.arxiv.org/html/2410.05983)). There are no exact deltas; the approximate figures are unquoted and left out.

### 3. Agentic workloads (settled; one sub-question unmeasured)

- **Multi-turn:** models show "an average drop of 39% across six generation tasks", made of "a minor loss in aptitude and a significant increase in unreliability". Also, "when LLMs take a wrong turn in a conversation, they get lost and do not recover" ([Laban et al., arXiv 2505.06120](https://arxiv.org/abs/2505.06120)).
- **Self-conditioning:** "models become more likely to make mistakes when the context contains their errors from prior turns. Self-conditioning does not reduce by just scaling the model size." Thinking mitigates it: "the Qwen3 thinking models do not self-condition" ([Sinha et al., arXiv 2509.09677](https://arxiv.org/html/2509.09677v3)).
- **SWE trajectories:** "failed trajectories are longer on average." For SWE-agent they are 12.6% longer on Lite and 18.5% longer on Verified; for OpenHands, 31.0% and 82.5% ([Majgaonkar, arXiv 2511.00197](https://arxiv.org/html/2511.00197)). This is a correlation that task difficulty confounds.
- **Reliability over repeats:** "Even for the best-performing gpt-4o function calling agent which has a >60% average task success, pass^8 drops to <25%" ([τ-bench, arXiv 2406.12045](https://arxiv.org/html/2406.12045)).
- **Counter-evidence:** "We find no clear correlation between failures and the point at which the model's context window becomes full." The Pearson r is 0.167, and "most runs consume around 25 million tokens" ([Vending-Bench, arXiv 2502.15840](https://arxiv.org/html/2502.15840v1)). In that benchmark, long-horizon derailment is not driven by the window filling.
- **Unmeasured:** no fetched source gives one agent's success rate by context-fill bucket (for example 50k vs 150k vs 500k). This is a "no source measures this" gap, not a failed fetch.

### 4. Mitigations with measured effect (settled; compaction alone unmeasured)

- **Observation masking vs LLM summarization, on SWE-bench Verified with SWE-agent** ([JetBrains Research blog](https://blog.jetbrains.com/research/2025/12/efficient-context-management/); [paper, arXiv 2508.21433](https://arxiv.org/abs/2508.21433)):
  - Masking: "observation masking boosted solve rates by 2.6% compared to leaving the context unmanaged, while being 52% cheaper on average" (Qwen3-Coder 480B).
  - Window: "keeping a window of the latest 10 turns gave us the best balance."
  - Summarization's side effect: it made agents run "an average of 52 turns, a whopping 15% longer than with observation masking", and the summary calls made up "more than 7% of the total cost per instance."
  - Hybrid: it cut costs "by 7% compared to pure observation masking and by 11% compared to using only LLM summarization."
  - **Winner: masking or the hybrid.**
- **OpenHands condenser:** "the context condensation strategy solves an average of 54% of instances, while the baseline agent only solves an average of 53%", at less than half the per-turn cost ([OpenHands blog](https://openhands.dev/blog), unquoted beyond the digger's quote).
- **Anthropic context editing and memory, on an internal agentic-search eval:**
  - "combining the memory tool with context editing improved performance by 39% over baseline"; "Context editing alone delivered a 29% improvement."
  - In a 100-turn web-search eval, context editing let workflows finish "that would otherwise fail due to context exhaustion—while reducing token consumption by 84%" ([Anthropic](https://claude.com/blog/context-management)).
  - These are vendor-internal evals with no dataset disclosed.
- **Sub-agent isolation:**
  - Anthropic: "a multi-agent system with Claude Opus 4 as the lead agent and Claude Sonnet 4 subagents outperformed single-agent Claude Opus 4 by 90.2%." "token usage by itself explains 80% of the variance"; multi-agent systems "use about 15× more tokens than chats" ([Anthropic multi-agent research](https://www.anthropic.com/engineering/built-multi-agent-research-system); internal eval).
  - **DISPUTED:** Cognition says multi-agent collaboration "only results in fragile systems" but gives no data ([Cognition](https://cognition.com/blog/dont-build-multi-agents)). That is a claim, not a measurement.
- **Learned or structured context management:**
  - ACON "reduces peak token usage by 26–54% while improving task success", with "up to 46% performance improvement" for smaller LMs ([arXiv 2510.00615](https://arxiv.org/abs/2510.00615)).
  - MEM1-7B "improves performance by 3.5× while reducing memory usage by 3.7×" against Qwen2.5-14B-Instruct ([arXiv 2506.15841](https://arxiv.org/abs/2506.15841)).
  - Context-Folding "matches or outperforms the ReAct baselines while using an active context 10× smaller and significantly outperforms … summarization-based context management" ([arXiv 2510.11967](https://arxiv.org/abs/2510.11967)).
- **Compaction quality compared:** on probes over "over 36,000 messages from production sessions", Factory scores 3.70, Anthropic 3.44 and OpenAI 3.35 out of 5. "Artifact tracking" is the weakest dimension, with a best of 2.45/5 ([Factory](https://factory.com/news/evaluating-compression)).
- **Compaction in launch posts:**
  - Opus 4.5: "the combination of all these techniques boosted Opus 4.5's performance on a deep research evaluation by almost 15 percentage points" ([Anthropic Opus 4.5](https://www.anthropic.com/news/claude-opus-4-5)). No per-technique split is given.
  - GPT-5.1-Codex-Max: "All evals were run with compaction enabled", with no compaction-off comparison ([OpenAI](https://openai.com/index/gpt-5-1-codex-max/)).
- **Unmeasured:** no fetched source isolates the effect of server-side compaction or Claude Code auto-compact. None measures summarize-in-place against a fresh-start handoff (clear the context and restart from a progress file). Anthropic's harness post describes that pattern with "zero quantitative metrics" ([Anthropic harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)).

### 5. Practical thresholds (partial)

- **Vendor defaults, which are configuration values and not findings:**

  | Setting | Trigger | Source |
  |---|---|---|
  | Anthropic API compaction | `{"type": "input_tokens", "value": 150000}` | [compaction docs](https://platform.claude.com/docs/en/build-with-claude/compaction-threshold) |
  | Anthropic context editing (`clear_tool_uses`) | 100,000 input tokens, keeps 3 tool uses | [context-editing docs](https://platform.claude.com/docs/en/build-with-claude/context-editing) |
  | Claude Code | `defaultThreshold = effectiveWindow - 13000; // ~83.5% of 200k`; `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` can only lower it | [claude-code#31806](https://github.com/anthropics/claude-code/issues/31806) |
  | Codex CLI | `effective_auto_compact_limit = min(user_config_limit, context_window * 90%)` | [openai/codex#11805](https://github.com/openai/codex/issues/11805) |
  | Gemini CLI | `COMPRESSION_TOKEN_THRESHOLD = 0.7`; keeps the latest 30% uncompressed | [gemini-cli#12068](https://github.com/google-gemini/gemini-cli/issues/12068) |
  | LangChain | no default trigger; `_DEFAULT_MESSAGES_TO_KEEP = 20` | [summarization.py](https://github.com/langchain-ai/langchain/blob/master/libs/langchain_v1/langchain/agents/middleware/summarization.py) |
  | JetBrains (measured) | mask all but the last 10 turns | [JetBrains](https://blog.jetbrains.com/research/2025/12/efficient-context-management/) |

- **Window size vs fixed triggers:** with a 1M window, the fixed 150K trigger "triggers compaction at only 15% of the available context." The issue was closed as not planned ([claude-code#34202](https://github.com/anthropics/claude-code/issues/34202)).
- **Practitioner guidance (a claim):** "keeping utilization in the 40-60% range", which is anecdotal ([humanlayer ACE](https://github.com/humanlayer/advanced-context-engineering-for-coding-agents/blob/main/ace-fca.md)). Anthropic's own context-engineering post gives no numeric threshold ([Anthropic](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)).
- **Absolute tokens vs fill fraction: UNSETTLED. No source measures the same absolute token count across windows of different size.** Indirect evidence points to absolute length and content mattering more than fraction:
  - The damage appears at 7.5K–32K, "well within the models' claimed lengths" (Du et al., NoLiMa, RULER).
  - Interference, not length, predicts PI-LLM failure.
  - At 1M the result depends on the model: 26.3% for Gemini 3.1 Pro against 76% for Opus 4.6.
- **Inference (not a source finding) for fleet thresholds:**
  - Trigger on absolute tokens and on content, not on a percentage of the window.
  - Clear or mask stale tool outputs early and cheaply: context editing at about 100k tokens, or keep the last ~10 turns of observations.
  - Reserve LLM summarization or a handoff for the ~150k-token region, whatever the window size.
  - Hand off to a fresh sub-agent sooner when the context holds the agent's own errors or superseded versions of files. Self-conditioning and proactive interference are the strongest measured harms.

## Coverage

- 1. Degradation curve: settled. The fraction of the window at which accuracy halves is not stated by any source.
- 2. Distractors vs filler: settled.
- 3. Agentic workloads: settled. Success by token-fill bucket is unmeasured.
- 4. Mitigations: settled. The effect of compaction alone and the handoff-vs-summarize comparison are unmeasured.
- 5. Practical thresholds: partial. Absolute tokens vs fill fraction is UNSETTLED because no source measures it.
- No sub-areas were added.
- Digging ended: every sub-area settled or reduced to a named "no source measures this" gap after round 3. The status block called E settled; this file records it as partial because of the absolute-vs-fraction gap.
- Diggers dispatched: 10; reports received: 10.

## Verification

- 41 statements were checked on 12 source pages. 37 were confirmed with quotes.
- One wording was corrected to its quote: Du et al.'s Claude-3.5 MMLU −41.7 / −67.6 comes from the **whitespace** condition, not from essay filler.
- 1 NOT ON PAGE: "the Opus 4.6 post ties a benchmark number to compaction". The page has none, which supports the "compaction alone unmeasured" gap.
- 3 UNCHECKED — HTTP 403: the GPT-4.1 MRCR 128k (57.2%), MRCR 1M (46.3%) and Graphwalks (61.7% → 19.0%) figures.
- Unquoted in the map: the OpenHands condenser figures (only the digger's quote).

## Open rabbit holes

- Gemini 2.5 Pro MRCR v2 at 58.0% (128k) vs 16.4% (1M): snippet only — dug, unsettled.
- Fiction.LiveBench multi-length table (the JS-rendered data in `mnismt/llms-long-context-benchmark`): dug, unsettled (fetch failed).
- Agent success rate by absolute context-fill bucket in a single controlled run: dug, unsettled (no source measures it).
- Compaction alone vs no compaction, and summarize vs fresh-start handoff with a progress file: dug, unsettled (no source measures it).
- Same absolute token count across windows of different size (fraction vs absolute): dug, unsettled.
- Sinha et al.'s horizon figures (GPT-5 thinking over 2,100 steps vs 432 for Claude-4 Sonnet): unquoted, dug, unsettled.
- Jin et al.'s exact hard-negative deltas (e5 vs BM25): dug, unsettled.
- Drift across repeated compaction cycles (5+ summaries): undug.
- Whether MRCR 1M failures cluster by needle position (a uniform wall or position-specific): undug.
- Cognition's later multi-agent post, reconciling it with "Don't Build Multi-Agents": undug.
- LongMemEval's reported "30% accuracy drop" over sustained interaction: snippet only, undug.
- The Complexity Trap per-model table (Qwen3-32B, Gemini 2.5 Flash rows): dug, unsettled.
