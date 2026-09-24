# RR — Stopping criteria and shipped defaults in open-source deep-research agents

Question: What stopping criteria do open-source deep-research agents (GPT Researcher, LangChain open_deep_research, Stanford STORM, and similar) use to end their search/iteration loops, and what defaults do they ship?

**Answer:** These agents stop mainly when they hit a fixed limit. That limit is an iteration count, a recursion depth, a turn count, a step count or a token budget. On top of it, some add a stop signal from the model: the model calls a "done" tool, stops calling tools, a simulated user signs off, or an answer evaluator passes. None of them has a semantic "enough evidence gathered" check apart from Jina's answer evaluation.

## Map

### LangChain open_deep_research
- Shipped defaults: `max_researcher_iterations` 6, `max_react_tool_calls` 10, `max_concurrent_research_units` 5, `max_structured_output_retries` 3 ([configuration.py](https://raw.githubusercontent.com/langchain-ai/open_deep_research/main/src/open_deep_research/configuration.py)).
- The supervisor stops when any one of three things happens: `if exceeded_allowed_iterations or no_tool_calls or research_complete_tool_call: return Command(goto=END...` Here `exceeded_allowed_iterations` means `research_iterations > max_researcher_iterations`, and the model can also call a `ResearchComplete` tool ([deep_researcher.py](https://github.com/langchain-ai/open_deep_research/blob/main/src/open_deep_research/deep_researcher.py)).
- A researcher sub-agent stops when `tool_call_iterations >= max_react_tool_calls` or when `ResearchComplete` is called: `if exceeded_iterations or research_complete_called: return Command(goto='compress_research'...` It also stops on a step that has no tool calls ([deep_researcher.py](https://github.com/langchain-ai/open_deep_research/blob/main/src/open_deep_research/deep_researcher.py)).

### GPT Researcher
- Shipped defaults: `"MAX_ITERATIONS": 3`, `"DEEP_RESEARCH_BREADTH": 3`, `"DEEP_RESEARCH_DEPTH": 2`, `"MAX_SEARCH_RESULTS_PER_QUERY": 5`, `"MAX_SUBTOPICS": 3`, `"DEEP_RESEARCH_CONCURRENCY": 4`, `"TOTAL_WORDS": 1200` ([default.py](https://raw.githubusercontent.com/assafelovic/gpt-researcher/master/gpt_researcher/config/variables/default.py)).
- Deep-research mode stops by counting depth down: `if depth > 1: new_breadth = max(2, breadth // 2); new_depth = depth - 1`. The digger found no use of `MAX_ITERATIONS` in this file. The only other bound is `MAX_CONTEXT_WORDS = 25000`, which trims gathered context after collection and never stops the loop ([deep_research.py](https://raw.githubusercontent.com/assafelovic/gpt-researcher/master/gpt_researcher/skills/deep_research.py)). Nobody fetched the code that actually uses `MAX_ITERATIONS` (see open rabbit holes).

### Stanford STORM and Co-STORM
- STORM defaults: `max_conv_turn` 3, `max_perspective` 3, `max_search_queries_per_turn` 3, `search_top_k` 3, `retrieve_top_k` 3, `max_thread_num` 10 ([engine.py](https://raw.githubusercontent.com/stanford-oval/storm/main/knowledge_storm/storm_wiki/engine.py)).
- Each simulated expert conversation runs `for _ in range(self.max_turn)`. It stops early when the simulated writer's reply starts with "Thank you so much for your help!" or is empty ([knowledge_curation.py](https://raw.githubusercontent.com/stanford-oval/storm/main/knowledge_storm/storm_wiki/modules/knowledge_curation.py)).
- Co-STORM caps a conversation with `total_conv_turn: int = field(default=20...` ([collaborative_storm/engine.py](https://raw.githubusercontent.com/stanford-oval/storm/main/knowledge_storm/collaborative_storm/engine.py)).

### Other agents
- dzhng/deep-research: the command-line tool defaults to breadth 4 and depth 2 ("recommended 2-10, default 4"; "recommended 1-5, default 2"). The research function recurses while `newDepth > 0` and halves the breadth at each level with `Math.ceil(breadth / 2)` ([run.ts](https://raw.githubusercontent.com/dzhng/deep-research/main/src/run.ts), [deep-research.ts](https://raw.githubusercontent.com/dzhng/deep-research/main/src/deep-research.ts)).
- Jina node-DeepResearch has a token budget: `tokenBudget: number = 1_000_000`, `maxBadAttempts: number = 2`, `const regularBudget = tokenBudget * 0.85;`, `while (context.tokenTracker.getTotalUsage().totalTokens < regularBudget) {`. It stops early once an answer passes evaluation. If the budget runs out first, it switches to "beast mode" and is forced to give a final answer ([agent.ts](https://raw.githubusercontent.com/jina-ai/node-DeepResearch/main/src/agent.ts)).
- Hugging Face smolagents open_deep_research: the search agent runs with `max_steps=20` and the manager CodeAgent with `max_steps=12` ([run.py](https://raw.githubusercontent.com/huggingface/smolagents/main/examples/open_deep_research/run.py)).

## Coverage
- open_deep_research: settled
- GPT Researcher: partial. The deep-research recursion is quoted, but the loop that uses `MAX_ITERATIONS` was never fetched.
- STORM / Co-STORM: settled
- Other agents: settled
- Digging ended: every sub-area was judged settled after round 1, so no further rounds ran. Later review downgraded GPT Researcher to partial.

## Verification
6 facts were checked on 2 pages: open_deep_research's two stop conditions and Jina's four budget facts. All 6 were confirmed by quoted lines. No facts were NOT ON PAGE or UNCHECKED. The remaining facts rest on verbatim quotes the diggers returned and were not rechecked.

## Open rabbit holes
- Which code uses GPT Researcher's `MAX_ITERATIONS` (the standard, non-deep research mode) — undug.
- Why these defaults were chosen, e.g. Co-STORM's 20 turns or open_deep_research's 10 tool calls — undug.
- How reliable STORM's string-prefix stop signal is — undug.
- How smolagents' `planning_interval` and `max_steps` interact — undug.
- Whether dzhng/deep-research or GPT Researcher cap tokens or cost across recursion levels — undug.
