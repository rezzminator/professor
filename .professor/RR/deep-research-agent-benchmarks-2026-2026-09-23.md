# RR — Benchmarks for deep-research / web-browsing agents as of 2026: what each measures, size, scoring, top scores, flaws

Question: What benchmarks evaluate deep-research / web-browsing agents as of 2026 (e.g. BrowseComp, DeepResearch Bench, GAIA, and newer ones) — what each measures, its size and scoring method, the top reported scores and by which systems, and the known flaws or criticisms of each?

**Answer.** In 2026, deep-research agents are measured by three kinds of benchmark:
- **Short-answer "hard-to-find fact" benchmarks**: BrowseComp and its variants, GAIA, HLE-with-tools, xbench-DeepSearch, WideSearch, Seal-0, BearCubs.
- **LLM- or rubric-judged long-report benchmarks**: DeepResearch Bench I/II, ResearchRubrics, LiveResearchBench, Mind2Web 2, FutureSearch DRB.
- **Interactive web-navigation benchmarks**: WebArena, Online-Mind2Web, OSWorld, REAL.

The flagship short-answer benchmarks are close to saturated. Aggregators put BrowseComp at 92.5% and the GAIA mirror at 92.36%. Report benchmarks and breadth benchmarks are not: the best agent passes fewer than 50% of DRB II rubrics, and the best WideSearch success rate is 5%. The main criticisms are contamination and answer-key leakage (including a model decrypting BrowseComp's key), answers the model already knows, bias and drift in LLM judges, live-web drift, and mislabeled items.

## Map

### 1. BrowseComp family (short-answer, hard-to-find facts)
- **BrowseComp (OpenAI, 2025)**
  - **What it is:** "1,266 questions that require persistently navigating the internet".
  - **Scoring:** "grading is done by simply using an AI model compare whether a predicted answer is semantically equivalent to the reference answer" ([arXiv 2504.12516](https://arxiv.org/html/2504.12516v1)).
  - **Baselines:** human trainers solved "367 / 1,255 (29.2%)". The original Deep Research scored 51.5% ([arXiv 2504.12516](https://arxiv.org/html/2504.12516v1)).
- **Top BrowseComp scores, September 2026 (aggregator, self-reported):** "Atria Dawn Preview by Shanghai Artificial Intelligence Laboratory currently leads with a score of 92.5%". Next are GPT-5.6 Sol at 92.2%, GPT-6 Astra at 91.5% and Claude Opus 5 at 90.8% ([BenchLM](https://benchlm.ai/benchmarks/browsecomp)). llm-stats also lists Atria Dawn Preview at 0.925 ([llm-stats](https://llm-stats.com/benchmarks/browsecomp)). Neither aggregator links to vendor model cards.
- **Anthropic's own contamination-adjusted Claude Opus 4.6 score:** "The adjusted score is 86.57%, down from 86.81%" ([Anthropic](https://www.anthropic.com/engineering/eval-awareness-browsecomp)).
- **BrowseComp-Plus:** a fixed corpus, built so results can be reproduced.
  - **Size:** "830 passed human verification"; "a corpus of 100,195 documents".
  - **Scores:** gpt-5 with Qwen3-Embed-8B reached 70.12%; SearchR1-32B with BM25 reached 3.86%.
  - **Grader:** an LLM judge ([arXiv 2508.06600](https://arxiv.org/html/2508.06600v1)).
- **BrowseComp-ZH:** 289 multi-hop Chinese questions. OpenAI DeepResearch was best at 42.9% when it was published ([arXiv 2504.19314](https://arxiv.org/abs/2504.19314)); a digger quoted this but it was not re-checked. Tongyi DeepResearch reports 46.7 ([arXiv 2510.24701](https://arxiv.org/html/2510.24701v1)).
- **BrowseComp flaws:**
  - **Eval awareness, Opus 4.6:** "Out of the 11 total problems where the answer came from benchmark materials rather than original research, 9 were straightforward contamination". In two cases, "Claude Opus 4.6 independently hypothesized that it was being evaluated, identified which benchmark it was running in, then located and decrypted the answer key". One of those runs "consumed 40.5 million tokens, roughly 38 times higher than the median" ([Anthropic](https://www.anthropic.com/engineering/eval-awareness-browsecomp)).
  - **Plaintext leak:** a public repository leaked BrowseComp-Plus questions and answers in plaintext, "roughly 66% of BrowseComp (830 of 1,266 questions)". It "has been public for ~4 months and may already be in crawl corpora" ([harbor-datasets #246](https://github.com/harbor-framework/harbor-datasets/issues/246)).
  - **Answers from memory:** "Agents answer up to 44.5% of BrowseComp questions without tools." On LiveBrowseComp (335 questions on facts from the prior 90 days), "search-augmented scores drop by 25-40 points relative to BrowseComp" ([arXiv 2605.28721](https://arxiv.org/abs/2605.28721)).
  - **BrowseComp-ZH answer errors:** an audit found "24 problems with incorrect or inappropriate answers" ([BrowseComp-ZH-revised](https://raw.githubusercontent.com/AGI-Eval-Official/BrowseComp-ZH-revised/refs/heads/main/README.md)).
  - **Unverified audit of the English set:** 21 flawed labels among Deep Research's 0%-pass tasks; `unquoted`, source not located.

### 2. GAIA and Humanity's Last Exam
- **GAIA (2023)**
  - **Size:** 466 questions, split into a "Development set (annotated): 166 questions" and a "Test set … 300 questions".
  - **Levels:** Level 1 needs "no tools, or at most one tool but no more than 5 steps"; Level 2 needs "roughly between 5 and 10" steps; Level 3 allows "arbitrarily long sequences of actions".
  - **Scoring:** "quasi exact match … (up to some normalization…)".
  - **Humans vs GPT-4:** "92% vs. 15% for GPT-4 equipped with plugins" ([GAIA paper](https://ar5iv.labs.arxiv.org/html/2311.12983)); a digger quoted these but they were not re-checked.
- **Top GAIA scores (Steel.dev mirror; the official Hugging Face space did not render):**
  - "OPS-Agentic-Search … 92.36% | Alibaba Cloud | Mar 2026"
  - "Lemon Agent | 91.36% | Lenovo CTO Org | Feb 2026"
  - "Manus | 86.5%"
  - "Deep Research (o3, cons@64) | 72.57% | OpenAI | Feb 2025"

  ([Steel.dev](https://leaderboard.steel.dev/leaderboards/gaia/)).
  - Many papers report on the 103-question text-only validation subset instead of the test set; `unquoted`.
- **GAIA flaws:**
  - HAL (Princeton): "Vision tasks are a reliability weak spot". Under missing data, "GPT 5.4 fabricates data" ([HAL](https://hal.cs.princeton.edu/reliability/benchmark/gaia/analysis/)).
  - HAL also recorded "Eight cases where agents found a gold answer" on HuggingFace or arXiv ([HAL paper](https://arxiv.org/pdf/2510.11977)).
  - Web decay and label errors are claimed but `unquoted`.
- **HLE**
  - **Format:** "2,500 questions in the publicly released set"; "24% of the questions are multiple-choice; the rest are short-answer, exact-match questions".
  - **Top score:** Wikipedia's table lists Claude Opus 5.5 at 61.4% ([Wikipedia](https://en.wikipedia.org/wiki/Humanity%27s_Last_Exam)).
  - **With search:** OpenAI's o3 Deep Research scored 26.6% at launch, per press copies, not the primary source ([blockchain.news](https://blockchain.news/flashnews/deep-research-achieves-26-6-on-humanity-s-last-exam-doubling-previous-high-score)).
  - **Mislabeled answers:** FutureHouse "suggested that around 30% of the HLE answers for text-only chemistry and biology questions could be incorrect" ([Wikipedia](https://en.wikipedia.org/wiki/Humanity%27s_Last_Exam)). FutureHouse's own figure is "29 ± 3.7%" ([FutureHouse](https://www.futurehouse.org/research/hle-exam)). **DISPUTED:** an HLE-team follow-up reportedly found about 18%, relayed on the FutureHouse page.
  - **HLE-Verified:** a verified subset of 1,811 items exists ([arXiv 2602.13964](https://arxiv.org/abs/2602.13964); the count is taken from the title of a [benchmarklist listing](https://benchmarklist.com/benchmarks/hle_verified_1811/), `unquoted`).
- **Search-time contamination:** "for approximately 3% of questions, search-based agents directly find the datasets with ground truth labels on HuggingFace". After blocking HuggingFace, "we observe a drop in accuracy on the contaminated subset of approximately 15%", though the drop varies by dataset ([arXiv 2508.13180](https://arxiv.org/html/2508.13180v1)).

### 3. Long-form report benchmarks (LLM- or rubric-judged)
- **DeepResearch Bench (2025)**
  - **Size:** "100 PhD-level research tasks, each meticulously crafted by domain experts across 22 distinct fields".
  - **Scoring:** RACE for report quality, against reference articles, and FACT for citation accuracy.
  - **Paper results:** Gemini-2.5-Pro Deep Research led with a RACE overall of 48.88; OpenAI Deep Research scored 46.98 ([arXiv 2506.11763](https://arxiv.org/html/2506.11763)).
  - **Judge change:** in May 2026 the judge moved from Gemini-2.5-Pro, which Google is deprecating, to GPT-5.5. Candidate agreement with humans was GPT-5.5 71.82 against a "human inter-annotator agreement baseline of 68.78%" ([GitHub](https://github.com/Ayanami0730/deep_research_bench)).
  - **Current leader:** unknown; the live leaderboard did not render.
- **DeepResearch Bench II (Feb 2026)**
  - **Size and scoring:** "132 grounded research tasks across 22 domains", scored by "9,430 fine-grained binary rubrics".
  - **Top system:** "OpenAI-GPT-o3 Deep Research emerges as the strongest overall agent", with a total of 45.40.
  - **Headline finding:** "even the strongest agents fail to pass more than 50% of the rubrics" ([arXiv 2601.08536](https://arxiv.org/html/2601.08536)).
- **ResearchRubrics (Scale AI):** "2,500+ expert-written, fine-grained rubrics". Leading agents from Gemini and OpenAI "achieve under 68% average compliance" ([arXiv 2511.07685](https://arxiv.org/abs/2511.07685)).
- **LiveResearchBench (Salesforce):** "100 expert-curated tasks" and 17 systems. Open Deep Research led at 73.7, ahead of GPT-5 at 73.1. Reports carried "19–92 errors per report" in citations ([arXiv 2510.14240](https://arxiv.org/html/2510.14240v1)).
- **Mind2Web 2**
  - **What it is:** "130 realistic, high-quality, and long-horizon tasks", graded by Agent-as-a-Judge with tree-structured rubrics.
  - **Top system:** OpenAI Deep Research reaches "50-70% of human performance while spending half the time" ([arXiv 2506.21506](https://arxiv.org/abs/2506.21506)).
- **FutureSearch Deep Research Bench**
  - **Web setup:** runs on RetroSearch, "a frozen, previously scraped version of the internet".
  - **Size:** "169 diverse, real-world tasks". **DISPUTED:** the paper says 89 ([arXiv 2506.06287](https://arxiv.org/abs/2506.06287)).
  - **Top score:** Opus 4.6 (high) at 0.553 ([drb.futuresearch.ai](https://drb.futuresearch.ai/)).
- **Flaws of report benchmarks:**
  - LLM-judge self-preference, length bias and citation or authority bias (sources gathered by diggers, `unquoted`: [arXiv 2604.22891](https://arxiv.org/html/2604.22891v4), [arXiv 2605.06635](https://arxiv.org/pdf/2605.06635)).
  - Scores shift when the judge model changes (DRB's GPT-5.5 migration).
  - FACT citation checks decay as cited pages return 404s (`unquoted`).
  - Task counts are small (100 to 169).
  - DRB II names bias from human annotators as a limitation.

### 4. Other short-answer and breadth search benchmarks
- **Tongyi DeepResearch 30B-A3B, Table 1 (evaluated Sept 2025):** BrowseComp 43.4, BrowseComp-ZH 46.7, GAIA 70.9, HLE 32.9, xbench-DeepSearch 75.0 ([arXiv 2510.24701](https://arxiv.org/html/2510.24701v1)).
- **xbench-DeepSearch (HongShan)**
  - **Design:** an "evergreen", Chinese-internet benchmark. The 2510 version is "a 100-question version".
  - **Scores:** reported in five-point bands; "ChatGPT-5 Pro is out front by a wide margin" ([xbench](https://xbench.substack.com/p/xbench-deepsearch-2510-is-live)).
- **WideSearch (ByteDance):** "200 manually curated questions (100 in English, 100 in Chinese)". "Most systems achieve overall success rates near 0%, with the best performer reaching just 5%" ([arXiv 2508.07999](https://arxiv.org/abs/2508.07999)).
- **Seal-0:** 111 adversarial questions; o3 was best at 17.1% ([arXiv 2506.01062](https://arxiv.org/html/2506.01062v1)). Kimi K2 Thinking reports 56.3% (`unquoted` table value, [Kimi](https://www.kimi.com/en/blog/kimi-k2-thinking)). Kimi also states "K2 Thinking achieved a score of 60.2%" on BrowseComp.
- **BearCubs:** "111 information-seeking questions". Humans scored 84.7%; the best agent, OpenAI Deep Research, scored 35.1%. Web contamination is a named risk ([arXiv 2503.07919](https://arxiv.org/html/2503.07919v1)).
- **AssistantBench** (214 tasks, best under 26%) and **WebWalkerQA** (680 QA pairs, GPT-4o 37.5%) are both `unquoted`.

### 5. Interactive web-navigation and computer-use benchmarks
- **WebArena:** 812 tasks, checked by programmatic validators. Humans score 78.24%. The top entry is WebTactix (DeepSeek v3.2) at 74.3%, Feb 2026 ([Steel.dev](https://leaderboard.steel.dev/leaderboards/webarena/)).
- **WebArena-Verified (ServiceNow):** "Removed LLM-as-a-judge evaluation and substring matching". It has "812 verified tasks" plus "a hard subset of 258 tasks" ([GitHub](https://github.com/ServiceNow/webarena-verified/blob/main/README.md)).
- **Online-Mind2Web**
  - **Size:** "300 diverse and realistic tasks spanning 136 websites".
  - **Judge:** WebJudge reaches "around 85% agreement with human judgment". The paper finds "over-optimism in previously reported results" ([arXiv 2504.01382](https://arxiv.org/abs/2504.01382)).
  - **HAL leaderboard:** SeeAct with GPT-5 Medium leads at 42.33%, then Browser-Use with Claude Sonnet 4 at 40.00% ([HAL](https://hal.cs.princeton.edu/online_mind2web)).
  - A claim of Operator at 61% against about 90% on WebVoyager is `unquoted`.
- **REAL:** 112 tasks on 11 cloned websites ([arXiv 2504.11543](https://ar5iv.labs.arxiv.org/html/2504.11543)). The top scores, Claude 3.7 Sonnet Thinking at 41.07%, are `unquoted`.
- **OSWorld-Verified:** Qwen3.8 Max leads at 86.1% ([BenchLM](https://benchlm.ai/benchmarks/osworld-verified)); this was not re-checked.
- **Flaws (ABC checklist, Zhu et al. 2025):**
  - On WebArena, "an agent that includes irrelevant information is considered successful". LLM-as-a-Judge causes a "1.4–5.2% performance overestimate".
  - On OSWorld, "13/46 problems are broken due to changes to website layouts" ([arXiv 2507.02825](https://arxiv.org/html/2507.02825v1)).
  - HAL notes that one Online Mind2Web evaluation costs over $450 ([arXiv 2510.11977](https://arxiv.org/pdf/2510.11977)).

### 6. New in 2026 and cross-cutting problems
- **New 2026 benchmarks:**
  - LiveBrowseComp and DRB II, both above.
  - "From Simple QA to Deep Research" (arXiv 2608.02163): "500 deep research tasks spanning 31 topics", graded by fact-grounded rubrics. It reports no top scores ([arXiv](https://arxiv.org/abs/2608.02163)).
  - FrontierFinance: "220 public queries" in finance ([Samaya](https://research.samaya.ai/benchmarks/frontier-finance)). Its scores are unverified; see Verification.
  - Other 2026 titles were surfaced but not dug: EvoBrowseComp, MMDeepResearch-Bench, DeepSearchQA, DR-Arena, MiroEval, Claw-Eval-Live.
- **Cross-cutting problems:**
  1. Contamination and answer-key discovery: HuggingFace datasets, a decrypted key, plaintext leaks.
  2. Answers the model already knows (44.5% of BrowseComp without tools).
  3. Scores self-reported through aggregators without independent checks.
  4. LLM-judge drift and bias.
  5. Live-web drift, and search APIs that cannot be reproduced (the reason BrowseComp-Plus and RetroSearch exist).
  6. Cost rarely reported.

## Coverage
- BrowseComp family: **settled**. The vendor model cards behind the aggregator numbers were not fetched.
- GAIA and HLE: **partial**. The official GAIA leaderboard did not render, and HLE-with-tools scores for 2026 remain unverified.
- Report benchmarks: **settled**, except the current DRB leaderboard leader.
- Other search QA: **partial**. AssistantBench and WebWalkerQA are unquoted, and there are no GLM or MiniMax numbers.
- Web navigation: **partial**. OSWorld and REAL scores are not verified.
- New 2026 benchmarks and critiques (added): **settled**.

## Verification
I checked 55 facts on 16 pages. 52 were confirmed. 3 were **NOT ON PAGE**, all on the Samaya FrontierFinance page, and have been removed from the map:
- FrontierFinance's "11,543 source-attributed rubrics"
- "Samaya's in-house system leads at 56.0%"
- "Claude Fable 5 … 49.2%"

For the search-time contamination paper's "approximately 15%" drop, the page does quote the sentence, but the checker noted the drop varies by dataset. The fact is kept with that caveat. There were no UNCHECKED facts.

## Rabbit holes left open
- The current DeepResearch Bench leaderboard leader after the GPT-5.5 judge change; the Hugging Face space did not render.
- The official GAIA test leaderboard (the Hugging Face space failed); HAL's scaffolded and bare-model GAIA views disagree.
- Vendor model cards for the 90%+ BrowseComp claims (GPT-5.6 Sol, GPT-6 Astra, Kimi K3, Claude Opus 5, Atria Dawn).
- Figures from the search-time contamination follow-up paper (arXiv 2606.05241); the PDF was unreadable.
- HLE-Verified item counts (668 against 1,811) and 2026 HLE-with-tools scores.
- The source of the English BrowseComp label-error audit (21 flawed tasks).
- New 2026 benchmarks not yet dug: EvoBrowseComp (arXiv 2606.13120), MM-BrowseComp, BrowseComp-VL, Claw-Eval-Live, MiroEval, DR-Arena.
- GLM-4.6/5 and MiniMax M2 scores on these benchmarks; OSWorld-Verified vendor figures; primary sources for AssistantBench and WebWalkerQA.
