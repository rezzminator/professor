# RR — Which repository and interface properties make LLM coding agents cheaper and more accurate (2024-2026)
Question: map what is published (2024 to 2026) about which properties of a code repository, and of the interface an LLM coding agent reads it through, make the agent cheaper and more accurate — navigation cost, tokens, the ratio of reading to editing. Ranked factors with effect sizes or "opinion".

## Answer

Most measured evidence is about the reading interface, not the repository itself. A bounded file viewer beats both a narrow one and a whole-file one ([SWE-agent](https://arxiv.org/html/2405.15793)). The edit format moved one model from 20% to 61% ([Aider](https://aider.chat/docs/unified-diffs.html)). Context files such as AGENTS.md raised cost by over 20% with no general success gain ([Gloaguen et al.](https://arxiv.org/html/2602.11988v3)). Navigation takes about 30% of an agent's rounds against about 34% for fixing ([2604.22750](https://arxiv.org/html/2604.22750v3)). Code-structure properties have almost no controlled agent measurement. File count is the only one with a documented negative association with success ([2509.16941](https://arxiv.org/abs/2509.16941)).

## Map

### 1. Trajectory share: localization against editing
- Sonnet-4.5 on 500 SWE-bench Verified instances, as a share of total rounds: Setup 9.98%, Explore 30.37%, Fix 33.53%, Validate 16.59%, Closeout 9.53% ([2604.22750](https://arxiv.org/html/2604.22750v3)). "Cache reads remain the dominant cost contributor in every phase" (same source). Token shares by phase exist only in its Figure 8, an image.
- Action shares across agents: "Generate Fix (23%), Run tests (19%), Search (15%), and Explore (14%)" ([Bouzenia and Pradel 2506.18824](https://arxiv.org/html/2506.18824)).
- SHERLOC: "agents spend on average 18.5 turns (48% of total interaction) locating faults before their first patch, consuming over 320k tokens per instance" ([2606.24820](https://arxiv.org/html/2606.24820)).
- In a production agent, explore steps take 41% of tokens and verify steps 5% (X 41%, E 35%, P 19%, V 5%) ([2606.15579](https://arxiv.org/html/2606.15579v1)). The same paper's SWE-agent figure, "26.1% of its steps to verification" for Llama-405B, is DISPUTED internally against 20.2%.
- Nebius SWE-agent trajectories, resolved against unresolved: average steps 31.3 against 58.4, and "At least one correct file opened" 83% against 40% ([Nebius card](https://huggingface.co/datasets/nebius/SWE-agent-trajectories)). This is a correlation, not a cause.
- Splitting "finding the place" from "understanding the code": no measured split was found. 2606.17454 defines explore and localize phases, but its numbers sit only in figures ([2606.17454](https://arxiv.org/html/2606.17454v3)).

### 2. Agent-computer interface and repository aids
- File viewer window: "Either too little content (30 lines, 14.3% ↓ 3.7) or too much (entire file, 12.7% ↓ 5.3) lowers performance", against 18.0% at 100 lines ([SWE-agent](https://arxiv.org/html/2405.15793)).
- Lint guard on edits: "without linting, 15.0% ↓ 3.0" (same source).
- Search-result caps: no measured evidence found. SWE-agent's 50-result cap is a guardrail that was never swept ([SWE-agent PDF](https://arxiv.org/pdf/2405.15793)).
- Edit format: "Aider's new unified diff edit format raised the score to 61%", against a 20% baseline, and it "reduced laziness by 3X" on GPT-4 Turbo ([Aider](https://aider.chat/docs/unified-diffs.html)). The page gives no token cost.
- Structure-aware reads: "Removing readCode causes the largest performance degradation: −7.8 Pass@1 for Qwen3-32B and −5.2 for GPT-5-mini" ([CODESTRUCT 2604.05407](https://arxiv.org/pdf/2604.05407)).
- Code graphs: RepoGraph moves SWE-agent with GPT-4o from 18.33 to 20.33 on SWE-bench Lite ([RepoGraph](https://arxiv.org/html/2410.14684)). CodexGraph reaches 22.96 Pass@1, a tie with AutoCodeRover, and "increases token consumption" ([CodexGraph](https://arxiv.org/html/2408.03910)).
- LSP tools: "on symbol-named localization the LSP costs tokens (+6% Opus, +118% Sonnet)", and Haiku saves 26%. The authors call this preliminary: 6 issues, 3 rollouts ([2608.13568](https://arxiv.org/html/2608.13568v1)).
- Repo map: Aider's page gives no measured gain ([Aider repomap](https://aider.chat/docs/repomap.html)). On retrieval yield only, RepoMap scores 0.3788 and ranks first ([2607.24882](https://arxiv.org/html/2607.24882v1)).
- Semantic code search (vendor-internal): "on average 12.5% higher accuracy in answering questions (6.5%–23.5% depending on the model)", and the retention effect "increases to 2.6% on large codebases with 1,000 files or more" ([Cursor](https://cursor.com/blog/semsearch)).
- Retrieval tools: ColGREP cuts trajectory length by 29.1% ([2610.05099](https://arxiv.org/abs/2610.05099)), unquoted. CodeGrep lifts resolve rate by 1.2 points, from 25.8% to 27.0% ([2608.05886](https://arxiv.org/abs/2608.05886)), unquoted. Tool-output compression "reduced delivered tool-output tokens by 38.4% but increased billed cost by 6.8%" ([2607.12161](https://arxiv.org/abs/2607.12161)).
- Context files (AGENTS.md, CLAUDE.md): "providing context files does not generally improve task success rates", with inference cost up "by over 20% on average". They "increase the # steps in every setting, on average by 2.45 and 3.92" ([Gloaguen et al. 2602.11988](https://arxiv.org/html/2602.11988v3)). A two-agent ablation finds "Neither agent shows a statistically significant strategy effect" ([2607.27250](https://arxiv.org/html/2607.27250)). DISPUTED: whether developer-written files beat LLM-generated ones. The paper's introduction says by 7%; its body supports about 4.4 points.

### 3. Code structure
- File count: "Models maintain relatively stable performance for singlefile problems but exhibit sharp declines as file count increases" ([SWE-Bench Pro 2509.16941](https://arxiv.org/abs/2509.16941)).
- Patch churn: r = −0.197. After adjusting for hardness, it is not an independent predictor ([Kozuchi 2608.15579](https://arxiv.org/abs/2608.15579)), unquoted.
- Semantics-preserving transformations, renaming among them, cost "up to 6.7 percentage points mean resolve-rate drop", with significant drops in 6 of 16 configurations ([2608.18389](https://arxiv.org/abs/2608.18389)). Renaming is not isolated.
- Without an agent, obfuscation cuts class-level accuracy by 11 to 29 points ([2510.03178](https://arxiv.org/abs/2510.03178)).
- Test co-location, measured on code generation only: "Co-location dominates when capability is constrained" ([2604.19826](https://arxiv.org/abs/2604.19826)).
- Duplication, monorepo against multi-repo, generated or vendored code, file length, cohesion: no measured evidence found.

### 4. Long context, caching, and vendor guidance
- Lost in the middle: GPT-3.5-Turbo accuracy "can drop by more than 20%" when the relevant document sits mid-context ([Liu et al.](https://arxiv.org/abs/2307.03172)), unquoted. Chroma reports increasingly unreliable performance as input grows, with no numeric figure in its text and no code task ([Chroma](https://www.trychroma.com/research/context-rot)). No degradation study on coding agents was found.
- Caching prices: a 5-minute cache write costs 1.25x the base input price, a 1-hour write 2x, a read 0.1x ([Anthropic docs](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)), unquoted. One preprint measures 41-80% savings in agent sessions, with internal inconsistencies ([2601.06007](https://arxiv.org/abs/2601.06007)), unverified.
- Vendor guidance:
  - Anthropic says "Context ... must be treated as a finite resource with diminishing marginal returns" ([Anthropic](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)). Opinion.
  - Cursor's rules docs say "Keep rules under 500 lines" ([Cursor rules](https://cursor.com/docs/rules)). Opinion.
  - OpenAI Codex caps AGENTS.md at 32 KiB by default. Configuration fact, unquoted.
  - Sourcegraph reports time to first token falling from 30-40 s to about 5 s at 1 MB of context ([Sourcegraph](https://sourcegraph.com/blog/towards-infinite-context-for-code)). A latency demo only.

## Ranked factors

The ranking runs by size of measured effect; measured factors come first.
1. Edit format (unified diff against search/replace, GPT-4 Turbo): 20% to 61% (Aider).
2. Structure-aware read tool: removing it costs −7.8 / −5.2 Pass@1 (CODESTRUCT).
3. Bounded file viewer of about 100 lines, against whole-file reads: whole file −5.3, 30 lines −3.7 points (SWE-agent).
4. Repository and change scale (file count): sharp decline with file count (SWE-Bench Pro), with no coefficient given.
5. Semantics-preserving code changes, renaming included: up to −6.7 points resolve rate (2608.18389).
6. Context files (AGENTS.md / CLAUDE.md): no general success gain, cost +20% or more, steps +2.45 to +3.92 (ETH).
7. Edit-time lint guard: −3.0 points without it (SWE-agent).
8. Code graph (RepoGraph): +2.0 points. CodexGraph: a tie with more tokens.
9. Semantic search index: +12.5% QA accuracy (Cursor, vendor-internal).
10. Retrieval tooling (ColGREP, CodeGrep): −29.1% trajectory length; +1.2 points resolve.
11. LSP tools: tokens −26% to +118% depending on the model; preliminary.
12. Prompt caching: reads at 0.1x the base input price (pricing, not an effect study).
13. Long-context position and length: more than 20% drop in non-code QA (Liu et al.). Its transfer to code agents is opinion.
14. Search-result caps: opinion.
15. Small files, modularity and cohesion: opinion.
16. Greppable naming: opinion. Measured only pooled, or without an agent.
17. Deduplication: opinion.
18. Monorepo against multi-repo: opinion.
19. Keeping generated or vendored code out of the agent's view: opinion.
20. Test co-location: opinion for agents. Measured only in code generation.

## Coverage
- Trajectory share (empirical SE): partial. Step share is settled; token share by phase sits only in figures. Reports: sub-rr/01-trajectory-share.md, sub-rr/05-phase-tokens.md
- ACI and repository aids (HCI/ML ablation): settled. Reports: sub-rr/02-aci-repo-aids.md, sub-rr/06-aci-gaps.md
- Code structure (software metrics research): partial. Reports: sub-rr/03-code-structure.md, sub-rr/07-naming-monorepo.md
- Long context, caching and guidance (ML evaluation + vendor docs): partial. Reports: sub-rr/04-context-caching-guidance.md, sub-rr/08-vendor-guidance.md

Digging ended: round ceiling reached. Three round-1 diggers and one round-2 digger were refused by the harness concurrent-subagent limit, so they were re-run in later rounds.

## Verification
I checked 13 facts on 6 pages; 11 were confirmed.
- NOT ON PAGE: the SWE-bench-Live repo-size success rates (above 20% under 100 files, under 5% above 500 files) and the patch-size solve rates (48% / never solved). The abstract page https://arxiv.org/abs/2505.23419 does not carry them, so both left the map.

## Open rabbit holes
- Token share by phase, from Figure 8 of 2604.22750 and the figures of 2606.17454: dug, unsettled.
- SHERLOC Appendix F token composition; the FlowPlan claim of two-thirds of tokens after the first edit (2611.13241): undug.
- An ablation of search-result caps: dug, unsettled.
- A renaming-only experiment on agent resolve rate, e.g. HEJ-Robust: dug, unsettled.
- Monorepo against multi-repo, and generated or vendored code: dug, unsettled; no study exists.
- Degradation with context length for coding agents: dug, unsettled.
- Repo-size success rates in the SWE-bench-Live full text (2505.23419): failed verification on the abstract page; undug at full text.
- Units of CodexGraph's token figures; the conflicting SWE-Pruner token reductions: undug.
- Readability formatting against token budget (2508.13666); the coherence-debt working set (2608.16630): undug.
