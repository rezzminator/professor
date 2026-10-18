# RR — How multi-agent frameworks hand a sub-agent's large output back to its orchestrator
Question: learn how current multi-agent frameworks hand a sub-agent's large output back to its orchestrator — inline in the return, through a file or artifact store, or through shared state — so we can judge our design, where each research digger writes its full cited report to a file and returns only short findings plus the path.

All six frameworks hand a sub-agent's result back inline: Claude Code returns ["only the summary"](https://code.claude.com/docs/en/sub-agents), deepagents returns the ["last message text as-is"](https://docs.langchain.com/oss/python/deepagents/subagents), and LangGraph and ADK can also return through [shared state](https://docs.langchain.com/oss/python/langgraph/use-subgraphs) or [`output_key`](https://adk.dev/sessions/state/). Writing to storage and returning a reference is recommended only in Anthropic's research post, which says it ["reduces token overhead"](https://www.anthropic.com/engineering/multi-agent-research-system), so your design follows that pattern. Condensing does lose information: one study measured summary handoff dropping feasibility from [0.88 to 0.48](https://arxiv.org/html/2607.18265). The documented failures of file handoff are parallel workers [overwriting each other's files](https://github.com/anthropics/claude-code/issues/101461) and [silent missing outputs](https://github.com/anthropics/claude-code/issues/61383), and both argue for unique per-worker paths and an existence check.

## Map

### 1. Framework return channels
- **LangGraph** uses shared state keys or a wrapper node: "When the parent graph and subgraph **share state keys**, you can pass a compiled subgraph directly to `add_node`." ([docs](https://docs.langchain.com/oss/python/langgraph/use-subgraphs)) The docs give no size cap. unquoted on cap
- **OpenAI Agents SDK** `as_tool` returns the final output: "If not provided, the final output is used." ([ref](https://openai.github.io/openai-agents-python/ref/agent/)) A non-string output becomes `str(output)`. The SDK has no file channel. Relayed from source, unquoted.
- **CrewAI** passes the default `raw` string ("By default, the `TaskOutput` will only include the `raw` output." [docs](https://docs.crewai.com/v1.15.28/en/concepts/tasks)). Downstream context joins `output.raw` in memory, and `output_file` is written but never read back, so the file is a sink and never a handoff. That is a source extraction, unquoted.
- **AutoGen** returns a `TaskResult`, or concatenated messages when `return_value_as_string` is set ([ref](https://microsoft.github.io/autogen/stable//reference/python/autogen_agentchat.tools.html)). **Microsoft Agent Framework**: "The outer agent sees the inner agent's final text response" ([learn.microsoft.com](https://learn.microsoft.com/en-us/agent-framework/journey/agents-as-tools)). The red team could not reconfirm this wording on another page, and it was not checked in step 6.
- **Google ADK** writes the final text into state through `output_key` ([state](https://adk.dev/sessions/state/)). The docs say "Session state is generally not optimized for storing large amounts of data" and point to Artifacts ([artifacts](https://adk.dev/artifacts/)). No numeric artifact cap was found. AgentTool returns the last event's text and forwards `state_delta`, not `artifact_delta` (relayed from source, unquoted). An artifact reference therefore reaches the parent only inside the returned text.
- **Claude Code / Agent SDK**: "the subagent does that work in its own context and returns only the summary." ([docs](https://code.claude.com/docs/en/sub-agents)) The docs state no output cap. DISPUTED: user issues report a cap of ["32000 output token maximum"](https://github.com/anthropics/claude-code/issues/25569) versus "Subagents are capped at 8000 output tokens" (cited in [#81838](https://github.com/anthropics/claude-code/issues/81838)). #81838 also says only the second message is delivered after a max_tokens split. [#58109](https://github.com/anthropics/claude-code/issues/58109) says the parent receives the last `text` block before the terminal tool_use. These are user reports, unconfirmed by Anthropic.

### 2. Write-to-storage, return-a-reference patterns
- Anthropic: "Subagents call tools to store their work in external systems, then pass lightweight references back to the coordinator." The pattern "reduces token overhead from copying large outputs through conversation history." The appendix heading is "Subagent output to a filesystem to minimize the 'game of telephone.'" Its only figures compare multi-agent with chat: "multi-agent systems use about 15× more tokens than chats." ([post](https://www.anthropic.com/engineering/multi-agent-research-system))
- LangChain Deep Agents: "Subagents can write results to files; the main agent reads what it needs." ([overview](https://docs.langchain.com/oss/python/deepagents/overview)) This is framed as a backstop. The default is an inline reply, "Keep your response under 500 words to maintain clean context", and the harness offloads automatically above about 20,000 tokens ([subagents](https://docs.langchain.com/oss/python/deepagents/subagents)). unquoted
- Anthropic context engineering: sub-agents return "a condensed, distilled summary of its work (often 1,000-2,000 tokens)" ([post](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)). This is a typical range with no stated method.
- Measured, single-agent (not an orchestrator handoff): writing output to a file and returning an outline "matches or exceeds raw-output accuracy by 0.5–2.5 pp at 11–57% input-token reductions" on top-tier models, while "The mid tier (GPT-5.1, GPT-5.2) regresses by 7.5–9.5 pp at similar token savings." ([arXiv 2607.00436](https://arxiv.org/html/2607.00436))
- Counterpoint: Cognition, "share full agent traces, not just individual messages" ([blog](https://cognition.ai/blog/dont-build-multi-agents)). unquoted
- No source measures a sub-agent's file-path return against an inline return to an orchestrator.

### 3. Information lost when condensing
- "narrative summarization, despite producing the smallest compressed hand-off payload, degrades feasibility to 0.48". The uncompressed control scored 0.88, and "The evaluation set contained 50 goal instances." ([arXiv 2607.18265](https://arxiv.org/html/2607.18265))
- "intermediate summarization fabrication frequently precedes incorrect close-ended outcomes" ([arXiv 2601.22984](https://arxiv.org/html/2601.22984)).
- MAST lists "withholding crucial information (FM-2.4, 0.85%)" ([arXiv 2503.13657](https://arxiv.org/html/2503.13657)).

### 4. Failure modes of file handoff
- **Silent missing or non-persistent output:** [#61383](https://github.com/anthropics/claude-code/issues/61383) asks that Claude Code "should not report or imply successful completion for a subagent whose required output file was never created". [#9458](https://github.com/anthropics/claude-code/issues/9458) says sub-agent writes "don't actually persist to the parent session's filesystem". DISPUTED rate: "100% failure rate" vs "only 2 or 3 of the 5". [#4462](https://github.com/anthropics/claude-code/issues/4462) is open, with a reproduction on v2.1.90.
- **Bare pointer, report absent:** "a bare pointer — text to the effect of "report delivered above" — with no report anywhere" ([#88822](https://github.com/anthropics/claude-code/issues/88822)).
- **Report-named files refused:** "Subagents should return findings as text, not write report files." ([#44657](https://github.com/anthropics/claude-code/issues/44657)) The cause is DISPUTED in the thread. This is a direct risk to `.md` report names.
- **Parallel path collisions:** parallel subagents sharing a cwd "clobber each other's artifacts" ([#101461](https://github.com/anthropics/claude-code/issues/101461)). With an absolute output path, "both write to the same absolute path and **overwrite each other**" ([pi-subagents #220](https://github.com/nicobailon/pi-subagents/issues/220)). In parallel tool calls, "Whichever call's `write` finishes last wins" and "The dropped call still reports `success: true`" ([zeroclaw #11136](https://github.com/zeroclaw/zeroclaw/issues/11136)). In deepagents the files reducer is right-overwrites-left (source extraction, unquoted).
- **Stale files:** no incident was found of a parent reading a file left by an earlier run. The nearest is CrewAI's `output_file`, which "always receives the raw pre-guardrail result" ([PR #4543](https://github.com/crewAIInc/crewAI/pull/4543)).
- **Mitigations documented:** unique per-lane or per-issue paths, such as `<scratchpad>/issue-<N>/` ([loom #11636](https://github.com/loom/loom/issues/11636)) and the [pi-lens #3850](https://github.com/pi-lens/pi-lens/issues/3850) proposals. LangChain: "each written to its own file so they don't overwrite each other." No source documents a framework-enforced existence or content check before marking a worker done. unquoted (links relayed by digger)

## Coverage
- Framework return channels (software documentation analysis): settled. Reports: sub-rr/01-framework-return-channels.md, sub-rr/05-framework-limits.md
- Storage-reference patterns (systems engineering): settled. Reports: sub-rr/02-storage-reference-patterns.md, sub-rr/06-reference-costs.md
- Condensation info loss (empirical ML evaluation): settled. Report: sub-rr/03-condensation-info-loss.md
- File-handoff failures (distributed systems failure analysis): settled. Reports: sub-rr/04-file-handoff-failures.md, sub-rr/07-overwrite-collisions.md
- Red team: none disputed beyond the disputes the diggers recorded. It flagged the 1,000-2,000-token figure as unmeasured and the Microsoft Agent Framework wording as unreconfirmed. Report: sub-rr/08-red-team.md
- Digging ended: every sub-area settled

## Verification
Four pages, ten facts checked, all confirmed: four on the Anthropic post, three on arXiv 2607.18265, two on arXiv 2607.00436, and the summary-only return on the Claude Code docs. The Claude Code docs check also found no output cap, and that page was read only to character 100,000. NOT ON PAGE: none. UNCHECKED: none.

## Rabbit holes left open
- Claude Code subagent output cap, 8K vs 32K, and the CHANGELOG (#78460, #10738): dug, unsettled
- Which block reaches the parent, #58109 vs #20190: dug, unsettled
- #44657 report-filename refusal mechanism (`Write.validateInput`) on current versions: dug, unsettled
- A measured inline vs file-reference return at an orchestrator handoff: dug, unsettled (none exists)
- Stale-file read from an earlier run: dug, unsettled (no incident found)
- deepagents batch order for parallel same-path writes: dug, unsettled
- ADK ForwardingArtifactService and whether sub-agent artifacts reach the parent: undug
- The pith.science critique of arXiv 2607.18265: undug
- Claude Code #72332 subagent output scanner altering returned paths: undug
- Microsoft Agent Framework AsAIFunction serialization: undug
