# RR — Stopping criteria and shipped defaults of open-source deep-research agents

Question: What stopping criteria do open-source deep-research agents (GPT Researcher, LangChain open_deep_research, Stanford STORM, and similar) use to end their search/iteration loops, and what defaults do they ship?

**Answer:** None of these agents measures whether it has found enough. Each stops at a fixed count: iterations, tool calls, conversation turns, or a recursion depth. The count is small, typically 2-10. LangChain open_deep_research and STORM also let the LLM end the loop early with a signal of its own.

## Map

### LangChain open_deep_research
- **Supervisor stops** when any one of three things happens: it passes the iteration cap, its reply has no tool calls, or it calls a `ResearchComplete` tool. The code reads `exceeded_allowed_iterations = research_iterations > configurable.max_researcher_iterations` and `if exceeded_allowed_iterations or no_tool_calls or research_complete_tool_call: return Command(goto=END...)`. [deep_researcher.py](https://github.com/langchain-ai/open_deep_research/blob/main/src/open_deep_research/deep_researcher.py)
- **Each sub-researcher stops** at its tool-call cap or when it calls `ResearchComplete`, and then goes to a compression step: `tool_call_iterations >= configurable.max_react_tool_calls`, followed by `goto='compress_research'`. [deep_researcher.py](https://github.com/langchain-ai/open_deep_research/blob/main/src/open_deep_research/deep_researcher.py)
- **Defaults** [configuration.py](https://github.com/langchain-ai/open_deep_research/blob/main/src/open_deep_research/configuration.py):

  | Setting | Default |
  |---|---|
  | `max_researcher_iterations` | 6 |
  | `max_react_tool_calls` | 10 |
  | `max_concurrent_research_units` | 5 |
  | `max_structured_output_retries` | 3 |

### Stanford STORM
- **The conversation loop** runs `for _ in range(self.max_turn):`. It ends early if the simulated writer says `"Thank you so much for your help!"`, or if the writer returns an empty reply (`user_utterance == ""`). [knowledge_curation.py](https://github.com/stanford-oval/storm/blob/main/knowledge_storm/storm_wiki/modules/knowledge_curation.py)
- **Defaults**, from `STORMWikiRunnerArguments` [engine.py](https://github.com/stanford-oval/storm/blob/main/knowledge_storm/storm_wiki/engine.py):

  | Setting | Default |
  |---|---|
  | `max_conv_turn` | 3 |
  | `max_perspective` | 3 |
  | `max_search_queries_per_turn` | 3 |
  | `search_top_k` | 3 |
  | `retrieve_top_k` | 3 |
  | `max_thread_num` | 10 |

### GPT Researcher
- **Deep-research recursion** subtracts one from the depth at each level (`new_depth = depth - 1`) and halves the breadth, never going below 2 (`new_breadth = max(2, breadth // 2)`). [deep_research.py](https://github.com/assafelovic/gpt-researcher/blob/master/gpt_researcher/skills/deep_research.py)
- **Accumulated context is capped** at `MAX_CONTEXT_WORDS = 25000`. [deep_research.py](https://github.com/assafelovic/gpt-researcher/blob/master/gpt_researcher/skills/deep_research.py)
- **Defaults** in the global config [default.py](https://github.com/assafelovic/gpt-researcher/blob/master/gpt_researcher/config/variables/default.py):

  | Setting | Default |
  |---|---|
  | `MAX_ITERATIONS` | 3 |
  | `DEEP_RESEARCH_DEPTH` | 2 |
  | `DEEP_RESEARCH_BREADTH` | 3 |
  | `DEEP_RESEARCH_CONCURRENCY` | 4 |
  | `MAX_SEARCH_RESULTS_PER_QUERY` | 5 |
  | `MAX_SUBTOPICS` | 3 |

- **DISPUTED: the default breadth.** The global config sets breadth to 3 ([default.py](https://github.com/assafelovic/gpt-researcher/blob/master/gpt_researcher/config/variables/default.py)). The deep-research module falls back to 4 if the setting is missing: `getattr(researcher.cfg, 'deep_research_breadth', 4)` ([deep_research.py](https://github.com/assafelovic/gpt-researcher/blob/master/gpt_researcher/skills/deep_research.py)). The two are unreconciled; probably 3 applies whenever the global config loads.
- What `MAX_ITERATIONS` actually counts was not traced in the code. unquoted: the only source is a search summary saying it caps processes like query expansion.

### dzhng/deep-research
- **Recursion** works like GPT Researcher's: `newBreadth = Math.ceil(breadth / 2)` and `newDepth = depth - 1`. It recurses only `if (newDepth > 0)`; otherwise it returns `{ learnings, visitedUrls }`. [deep-research.ts](https://github.com/dzhng/deep-research/blob/main/src/deep-research.ts)
- **Defaults** are breadth 4 and depth 2, set by the command-line prompts. unquoted, not verified. [run.ts](https://github.com/dzhng/deep-research/blob/main/src/run.ts)

### Pattern across all four
- **LangChain open_deep_research and STORM** use a hard cap on iterations or turns, and the LLM can stop earlier: a `ResearchComplete` tool call or a reply with no tool calls in LangChain, the "Thank you so much for your help!" phrase or an empty reply in STORM.
- **GPT Researcher and dzhng/deep-research** use recursion that stops at a fixed depth. Breadth is halved at each level.
- **None of the four** stops because it detects new results adding little, or because it judges the question covered. Stopping on saturation or diminishing returns is absent from all four.

## Coverage
- LangChain open_deep_research: settled
- STORM: settled
- GPT Researcher: partial (what `MAX_ITERATIONS` controls is unclear, and the default breadth conflicts)
- dzhng/deep-research: settled (the defaults are unquoted)
- Similar agents (smolagents open_deep_research, Jina node-DeepResearch): open, not dug

## Verification
I checked 11 facts on 4 source pages, and all 11 were confirmed: the LangChain exit conditions, the STORM loop and its two early exits, GPT Researcher's recursion rules, its fallback defaults and its 25,000-word cap, and dzhng's recursion rules. None came back NOT ON PAGE, and none are UNCHECKED. The default values were quoted from the opening fetches but not re-verified.

## Open rabbit holes
- The default `max_steps` in smolagents open_deep_research.
- How Jina node-DeepResearch uses a token budget and a limit on bad attempts to stop.
- What GPT Researcher's `MAX_ITERATIONS` actually controls.
- How the `allow_clarification` default interacts with the supervisor loop in LangChain open_deep_research.
- Whether dzhng/deep-research or GPT Researcher caps the total number of queries or the cost, since breadth times depth grows quickly.
