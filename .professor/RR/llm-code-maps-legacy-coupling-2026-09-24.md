# RR — Do compact code maps / formal representations cut LLM cost safely on coupled legacy codebases?

Question: produce a cited research map that tests a set of claims (C1–C6) about using compact code "maps" / formal representations to cut LLM token cost on legacy codebases, with counter-evidence weighted equally.

## Answer

Evidence mostly supports the position. The one-way "declaration map + cheap model + rule check" approach is well precedented: Agentless skeletons, Aider repo map and architect/editor mode, CodePlan, and ArchUnit/dependency-cruiser. Two-way model-driven approaches have a weak industrial record. The strongest counter-evidence is on C2 and C3: grammar prompting partly closes the DSL gap, and signature-level contracts help weak models a lot but matter little for frontier models. C4 and C6 are supported mostly by classic pre-LLM literature, and some key numbers could not be quoted.

## Map

### C1 — Round-trip / MDA / 4GL (verdict: SUPPORTED, with a narrow-domain caveat; thin on LLM-era evidence)
- Petre, ICSE 2013, interviewed 50 professional engineers. Adoption breakdown: "No UML: 35, Selective use: 11, Automated code generation: 3, Retrofit: 1, Wholehearted adoption: 0". Source: the neverworkintheory.org summary, fetched by a digger (unquoted: it is a secondary summary).
- Whittle, Hutchinson & Rouncefield, "State of Practice in MDE" ([eprints.lancs](https://eprints.lancs.ac.uk/id/eprint/69765/1/SO_SW_2012_12_0188.R1_Whittle.pdf)), a survey of 450 practitioners plus 22 interviews:
  - "most companies seem to experience productivity increases of between 20-30%", and these are "not considered significant enough to drive an MDE adoption effort".
  - "code certification costs rose by a factor of eight".
  - Practitioners "rarely use it to generate whole systems; rather, they apply it to develop key parts of a system often using domain-specific modeling languages".
  - This is counter-evidence in part: MDE does deliver in narrow DSL areas. (UNCHECKED — the verification fetch returned an unreadable PDF; the digger quoted these lines from the same URL.)
- Lifting legacy code into models is unsolved. The MoDisco paper page on modeling-languages.com says generic MDRE solutions "are still missing or incomplete" (unquoted in verification).
- No LLM-era paper on round-trip code-to-model editing was found; the digger searched and found nothing.
- Simulink/SCADE success in automotive and avionics is a plausible counter-case. It rests on search snippets only, so it is listed under open rabbit holes.

### C2 — LLMs worse on low-resource languages/DSLs (verdict: SUPPORTED; MIXED on remedies)
- Cassano et al., MultiPL-T ([arXiv 2308.09895](https://arxiv.org/abs/2308.09895)): Code LLMs "struggle with low-resource languages that have limited training data available" (confirmed).
- MultiPL-E pass@1 for StarCoderBase-15B, from a digger's full-text read of arXiv 2308.09895: OCaml 6.9, Racket 11.8, R 10.2, Julia 21.1, Lua 26.6, against Python 30.6.
- Counter-evidence, same paper: fine-tuning closes much of the gap. CodeLlama-34B Racket went from 15.9 to 29.1 and Julia from 31.8 to 43.5.
- Counter-evidence: Wang et al., Grammar Prompting, NeurIPS 2023 ([HF page for arXiv 2305.19234](https://huggingface.co/papers/2305.19234)):
  - Supplying the DSL grammar in the prompt raised SMCalFlow program accuracy from 46.4% to 52.4%.
  - GeoQuery execution accuracy rose from 81.5% to 88.9%.
  - The oracle-grammar ceiling was 83.6%, so a large gap remains.
- Not found: a direct measure of how small and large models differ on DSLs.

### C3 — One-way maps exist and cut tokens (verdict: SUPPORTED as prior art; MIXED on "as good as full context")
- Aider repo map ([docs](https://aider.chat/docs/repomap.html), [blog](https://aider.chat/2023/10/22/repomap.html)):
  - A tree-sitter map of "the most important classes and functions along with their types and call signatures".
  - Default budget: --map-tokens 1k.
  - Aider publishes no measured savings.
- Aider architect/editor mode ([aider.chat](https://aider.chat/2024/09/26/architect.html)), all confirmed:
  - "Using o1-preview as the Architect with either DeepSeek or o1-mini as the Editor produced the SOTA score of 85%."
  - o1-preview plus Sonnet as editor scored 82.7%.
  - Solo baselines: o1-preview 79.7%, claude-3.5-sonnet 77.4%.
- Agentless ([arXiv 2407.01489](https://arxiv.org/html/2407.01489v1)):
  - Skeleton format: "we provide only the headers of the classes and functions in the file ... (signatures only)", which is "a much more concise representation".
  - File localization 77.7%; 27.33% solved on SWE-bench Lite.
- LongCodeZip ([arXiv 2510.00446](https://arxiv.org/abs/2510.00446)):
  - "up to a 5.6× compression ratio without degrading task performance".
  - RepoQA: 75.3 at 5.3× compression versus 38.3 uncompressed.
- Counter-evidence: Walczak et al. ([arXiv 2507.14256](https://arxiv.org/abs/2507.14256)):
  - Full implementation context improved compile success by 6.84pp and 6.34pp over signature plus docstring.
  - Mutation score rose 4.33pp and 4.00pp.
  - Signature plus docstring still reached 93.50% branch coverage against 91.50% for full context. So interface-only context loses some quality, but not much.
- Rule checkers: dependency-cruiser ([GitHub](https://github.com/sverweij/dependency-cruiser)) "validates them against (your own) rules". ArchUnit ([archunit.org](https://www.archunit.org/getting-started)) expresses rules as `ArchRule`s.
- Counter/nuance: SeeRepo ([arXiv 2606.14061](https://arxiv.org/html/2606.14061v4)):
  - A compact visual map as a supplement cut input tokens by 25% and cost by 26% on GPT-5-mini, with Pass@1 at 55.4% (+0.4).
  - Using the map alone dropped accuracy from 55.0% to 41.4%.
  - Takeaway: a map works as an addition to text, not a replacement.

### Q2 — The specific technique published? (verdict: close analogues, no exact match)
- CodePlan ([arXiv 2309.12499](https://arxiv.org/abs/2309.12499)), confirmed:
  - "a novel combination of an incremental dependency analysis, a change may-impact analysis and an adaptive planning algorithm".
  - "5/6 repositories to pass the validity checks ... whereas the baselines (without planning but with the same type of contextual information as CodePlan) cannot get any".
  - Only 6 repositories were tested.
- "Architecture as Capability Equalizer for Coding Agents" ([arXiv 2608.21747](https://arxiv.org/html/2608.21747)), confirmed:
  - "On non-frontier models, format produces spreads of 0.83–2.42 points, with code-proximate formats recovering most of the capability gap."
  - "TypeScript contracts triple API route coverage for the weakest model (33%→100%)."
  - "On the strongest models (Sonnet 4.6, GPT-5), format barely matters (quality spread 0.17–0.92)."
  - This directly supports the colleague's cheap-model-plus-declarations idea.
- ICSE 2026 workshop paper on architectural documents plus implementation plans ([ACM](https://dl.acm.org/doi/10.1145/3786152.3788588)): from the abstract only, documentation "substantially improves conformance". The full paper returned 403, so no numbers.
- No paper was found applying this pipeline to early-2000s legacy code with dependency-rule checks. CoSTAR ([arXiv 2609.11332](https://arxiv.org/abs/2609.11332)) is legacy work, but it uses NL summaries rather than skeletons: an 8B model beat Qwen3-235B by 4.35% on accuracy.

### C4 — Maps miss hidden coupling (verdict: SUPPORTED qualitatively; the key share-missed figure is unquoted)
- Gall, Hajek, Jazayeri, ICSM 1998 ([PDF](http://turingmachine.org/~dmg/dchurch/icsm98.pdf)): structural "measures do not reveal all dependencies (e.g. dynamic relations)... some dependencies are not written down either in documentation or in the code". The case study was a 10 MLOC telecom system.
- Wiese et al. 2015 ([Springer](https://doi.org/10.1007/978-3-319-17837-0_1)): change coupling "reveals relationships between software artifacts that cannot be found by scanning code or documentation". They predicted 45.7% of defects where strong change couplings recurred.
- Li, Tan, Xue, Java reflection ([arXiv 1706.04567](https://arxiv.org/abs/1706.04567)): with reflection unhandled, "much of the codebase will be rendered invisible for static analysis". The study examined 1,423 reflective call sites in 16 programs.
- The Oliva & Gerosa 2011 figure (about 91% of co-change not explained by structural dependency) comes from snippets only and is under open rabbit holes. Database-mediated coupling (Meurice/Cleve) is behind a paywall.

### C5 — Agents fail on repo-level/cross-file changes (verdict: SUPPORTED; attribution specifically to hidden coupling is thin)
- Wang et al., "Are 'Solved Issues' in SWE-bench Really Solved Correctly?" ([arXiv 2503.15223](https://arxiv.org/html/2503.15223v1)), confirmed:
  - "on average, 7.8% of plausible patches are incorrect, leading to an absolute drop of the issue resolution rate of 4.5%".
  - "on average 29.6% of the plausible patches are identified as suspicious".
- CrossCodeEval (Ding et al. 2023): "extremely challenging when the relevant cross-file context is absent", with clear gains when cross-file context is added.
- ChainSWE ([arXiv 2607.02606](https://arxiv.org/abs/2607.02606)): performance drops "up to 70%" as chains of dependent issues grow.
- Failure taxonomy ([arXiv 2509.13941](https://arxiv.org/abs/2509.13941)) from 150 failures: "the majority of agentic failures stemming from flawed reasoning and cognitive deadlocks", not missing context. This is partial counter-evidence on the cause.

### C6 — Effect analysis exposes coupling (verdict: SUPPORTED as a known technique; thin; no LLM-era evidence)
- Chianti, Ren et al., OOPSLA 2004 ([PDF](https://prolangs.cs.vt.edu/refs/docs/oopsla04.pdf)) decomposes changes into atomic changes and reports affected tests. On Daikon, "on average, 52% of Daikon's unit tests are affected ... each affected unit test, on average, is affected by only 3.95% of the atomic changes."
- The Lucassen & Gifford 1988 effect systems and Banning 1979 MOD/REF primary sources are paywalled and were not quoted.
- No paper feeding side-effect summaries to LLMs was found; the diggers searched and found nothing.

## Coverage
- C1: settled
- C2: settled
- C3: settled
- C4: partial (share-missed figure unquoted)
- C5: settled
- C6: partial (primaries paywalled)
- Q2 (the specific technique): settled

Digging ended: round 3 ceiling reached.

## Verification
- 16 facts checked on 6 pages.
- 15 were confirmed on Aider architect, arXiv 2503.15223, 2308.09895, 2608.21747 and 2309.12499.
- One wording was corrected to the page: "recovering most of the capability gap".
- UNCHECKED: 4 Whittle et al. facts (20–30% productivity, not enough to drive adoption, 8x certification, key parts via DSLs). The WebFetch returned an undecodable PDF.
- The figures in the table below come from digger fetches that were not re-checked in verification.
- Not re-checked in the verification step: the Petre figures (from a secondary summary) and the MultiPL-E per-language numbers.

## Open rabbit holes
- Oliva & Gerosa 2011, "about 91% of co-change not structural": dug, unsettled (paywalled or mis-resolved).
- Ajienka/Capiluppi/Counsell 2018 overlap percentage: dug, unsettled (edgehill PDF returned 403).
- Meurice/Cleve on database-mediated coupling in Java: dug, unsettled (Springer auth wall).
- Lucassen & Gifford 1988 and Banning 1979 primary text: dug, unsettled (ACM 403).
- Simulink/SCADE model-based success as a C1 counter-case: dug, unsettled (snippets only).
- Full numbers for the ICSE 2026 architectural-documents paper: dug, unsettled (ACM 403).
- LLM-era code-to-model round-trip: dug, unsettled (nothing found).
- LLMs given side-effect/effect summaries: dug, unsettled (nothing found).
- Small-vs-large model gap specifically on DSLs: undug.
- EvoGraph COBOL→Java "93% functional equivalence" and XMainframe: undug (snippets only).
- Aider repo-map measured token savings: dug, unsettled (none published).
