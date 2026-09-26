# RR — Stopping criteria and shipped defaults of open-source deep-research agents

Question: What stopping criteria do open-source deep-research agents (GPT Researcher, LangChain open_deep_research, Stanford STORM, and similar) use to end their search/iteration loops, and what defaults do they ship?

**Answer:** Not one of these agents decides for itself that it has "enough" information. They stop in three ways. (1) A fixed countdown: depth and breadth in GPT Researcher and dzhng/deep-research, turn counts in STORM. (2) A hard cap on iterations, tool calls or steps: open_deep_research, smolagents. (3) A budget: tokens in Jina node-DeepResearch. Two of them also let the model stop on its own signal: open_deep_research's `ResearchComplete` tool call and STORM's "Thank you so much for your help!" line. The shipped defaults are small, mostly between 2 and 6 for depth, turns and iterations.

## Map

### LangChain open_deep_research
- Three things end the loops. The supervisor stops when the model calls `ResearchComplete`, or when `research_iterations > configurable.max_researcher_iterations`. Each researcher stops when `tool_call_iterations >= max_react_tool_calls`. Calling `think_tool` does not end anything. ([deep_researcher.py](https://github.com/langchain-ai/open_deep_research/blob/main/src/open_deep_research/deep_researcher.py))
- Defaults: `max_researcher_iterations` 6, `max_react_tool_calls` 10, `max_concurrent_research_units` 5, `max_structured_output_retries` 3. ([configuration.py](https://raw.githubusercontent.com/langchain-ai/open_deep_research/main/src/open_deep_research/configuration.py))

### GPT Researcher
- Defaults: `"MAX_ITERATIONS": 3`, `"MAX_SEARCH_RESULTS_PER_QUERY": 5`, `"DEEP_RESEARCH_BREADTH": 3`, `"DEEP_RESEARCH_DEPTH": 2`, `"MAX_SUBTOPICS": 3`. ([default.py](https://raw.githubusercontent.com/assafelovic/gpt-researcher/master/gpt_researcher/config/variables/default.py))
- In deep-research mode, recursion ends when depth runs out. The code recurses only `if depth > 1`, with `new_depth = depth - 1` and `new_breadth = max(2, breadth // 2)`. `MAX_CONTEXT_WORDS = 25000` trims the context but does not stop the loop. ([deep_research.py](https://raw.githubusercontent.com/assafelovic/gpt-researcher/master/gpt_researcher/skills/deep_research.py))

### Stanford STORM / Co-STORM
- Each simulated conversation ends in one of two ways. It breaks when the writer persona's line starts with "Thank you so much for your help!", or when `for _ in range(self.max_turn)` runs out. ([knowledge_curation.py](https://raw.githubusercontent.com/stanford-oval/storm/main/knowledge_storm/storm_wiki/modules/knowledge_curation.py))
- STORM defaults: `max_conv_turn` 3, `max_perspective` 3, `max_search_queries_per_turn` 3, `search_top_k` 3, `retrieve_top_k` 3, `max_thread_num` 10. ([engine.py](https://raw.githubusercontent.com/stanford-oval/storm/main/knowledge_storm/storm_wiki/engine.py))
- Co-STORM defaults: `total_conv_turn` 20, `max_num_round_table_experts` 2, `max_thread_num` 10. ([collaborative_storm/engine.py](https://raw.githubusercontent.com/stanford-oval/storm/main/knowledge_storm/collaborative_storm/engine.py))

### Other agents
- dzhng/deep-research: each level sets `newBreadth = Math.ceil(breadth / 2)` and `newDepth = depth - 1`, and the recursion returns once depth reaches 0 ([deep-research.ts](https://github.com/dzhng/deep-research/blob/main/src/deep-research.ts)). Its command-line defaults read "Enter research breadth (recommended 2-10, default 4)" and "Enter research depth (recommended 1-5, default 2)" ([run.ts](https://raw.githubusercontent.com/dzhng/deep-research/main/src/run.ts)).
- HuggingFace smolagents open_deep_research caps both agents by step count: the search agent has `max_steps=20` and the manager has `max_steps=12`. ([run.py](https://github.com/huggingface/smolagents/blob/main/examples/open_deep_research/run.py))
- Jina node-DeepResearch runs until its token budget is spent. Defaults are `tokenBudget: number = 1_000_000` and `maxBadAttempts: number = 2`. The loop may use up to `const regularBudget = tokenBudget * 0.85;`, and the other 15% is kept for a "beast mode" that forces an answer if the loop ends without one. ([agent.ts](https://raw.githubusercontent.com/jina-ai/node-DeepResearch/main/src/agent.ts))

## Coverage
- open_deep_research: settled
- GPT Researcher: partial. It is not clear what `MAX_ITERATIONS` controls outside deep-research mode.
- STORM / Co-STORM: settled
- Other agents (added): settled

## Verification
I re-checked 6 facts against their pages: the dzhng breadth and depth defaults, and Jina's token budget, bad-attempt limit, 0.85 multiplier and beast mode. All 6 were confirmed. None came back as NOT ON PAGE or UNCHECKED. An earlier dispute over dzhng's default breadth (4 or 6) is settled: run.ts says default 4.

## Open rabbit holes
- Where GPT Researcher's `MAX_ITERATIONS` is used in the standard (non-deep) research path.
- Co-STORM's warm-start and knowledge-base reorganization phases, which have their own stopping logic.
- Whether any of these agents stops when new sources stop adding information (convergence or saturation). None was found.
