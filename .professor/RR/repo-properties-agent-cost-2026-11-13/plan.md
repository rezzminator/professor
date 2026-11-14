# RR — Which repository and interface properties make LLM coding agents cheaper and more accurate (2024-2026)
Question: map what is published (2024 to 2026) about which properties of a code repository, and of the interface an LLM coding agent reads it through, make the agent cheaper and more accurate — navigation cost, tokens, the ratio of reading to editing. Ranked factors with effect sizes or "opinion".

1. Trajectory share (localization vs editing) — settled when >=2 papers give a measured step/token share by phase
Lens: empirical SE — method: trajectory log analysis; sources: SWE-bench/OpenHands trajectory datasets, arXiv papers
2. Agent-computer interface + repository aids (window size, search caps, edit formats, repo maps, code graphs, LSP, AGENTS.md) — settled when ablation numbers are quoted from papers
Lens: HCI/ML ablation — method: controlled ablation; sources: SWE-agent, RepoGraph, CodexGraph, AGENTS.md evaluation papers
3. Code structure (file size, modularity, naming, duplication, monorepo) — settled when measured studies found or absence documented
Lens: software metrics research — method: correlational regression of code metrics vs agent success; sources: arXiv empirical studies
4. Long-context degradation and prompt-caching economics + vendor guidance — settled when context-rot/lost-in-middle numbers and vendor pricing/guidance pages are quoted
Lens: ML evaluation + vendor docs — method: benchmark measurement, pricing docs; sources: Chroma report, Liu et al., Anthropic/OpenAI/Cursor/Sourcegraph docs
