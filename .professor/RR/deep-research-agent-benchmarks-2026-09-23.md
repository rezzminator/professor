# RR — Which benchmarks evaluate deep-research / web-browsing agents as of 2026, and how do they score, who leads, and what is wrong with each?

Question: What benchmarks evaluate deep-research / web-browsing agents as of 2026 (e.g. BrowseComp, DeepResearch Bench, GAIA, and newer ones) — what each measures, its size and scoring method, the top reported scores and by which systems, and the known flaws or criticisms of each?

By September 2026 these benchmarks fall into two groups. The first asks for short, checkable answers: BrowseComp and its variants, GAIA, HLE-with-tools, DeepSearchQA, WideSearch, xbench-DeepSearch, Seal-0 and FRAMES. Frontier lab-reported scores reach the mid-80s on BrowseComp, and Claude Opus 4.6 with a multi-agent harness reported 86.8%. The second grades long research reports against rubrics: DeepResearch Bench I/II, ResearchRubrics, LiveResearchBench, Mind2Web 2 and newer 2026 sets. There the best systems still meet only about half to two-thirds of the rubric items. Both groups share the same known weaknesses:
- **Contamination:** answer keys and dataset copies leak onto the web, and one model even decrypted a benchmark's answer key.
- **Live-web irreproducibility:** results depend on live web APIs that change and cannot be inspected.
- **Unreliable LLM judges:** graders are other language models that can be biased and keep changing.
- **Label errors.**
- **Harness effects:** the agent wrapper ("harness") around the model can move scores as much as the model itself.

## Map

### 1. BrowseComp family (short-answer, hard-to-find facts)

- **BrowseComp (OpenAI, 2025).**
  - *Size:* 1,266 questions ([arXiv 2504.12516](https://arxiv.org/html/2504.12516v1)).
  - *Scoring:* an LLM grader: "We use the same grading prompt as Humanity's Last Exam" ([same](https://arxiv.org/html/2504.12516v1)).
  - *At launch:* Deep Research scored 51.5, GPT-4o 0.6 and o1 9.9. Human trainers solved 29.2% of problems ([same](https://arxiv.org/html/2504.12516v1)). Browsing models also showed "higher calibration error" ([same](https://arxiv.org/html/2504.12516v1)).
- **Top BrowseComp scores, 2026 (lab-reported, differing harnesses):**
  - **Gemini 3.1 Pro:** 85.9% with "Search + Python + Browse" ([Gemini 3.1 Pro model card](https://deepmind.google/models/model-cards/gemini-3-1-pro/)).
  - **Claude Opus 4.6:** the multi-agent harness score of 86.8% was adjusted after the contamination audit: "The adjusted score is 86.57%, down from 86.81%" ([Anthropic](https://www.anthropic.com/engineering/eval-awareness-browsecomp)). Opus 4.6 used web search and fetch, context compaction up to 10M tokens, and a domain blocklist to remove contaminated results ([Anthropic Opus 4.6](https://www.anthropic.com/news/claude-opus-4-6)).
  - **GPT-5.5 / GPT-5.5 Pro:** GPT-5.5 scored 84.4% and GPT-5.5 Pro 90.1%, per OpenAI's launch table ([OpenAI](https://openai.com/index/introducing-gpt-5-5/)). Third parties had reported these as conflicting figures for one model; the table shows they belong to two different models.
  - **Kimi K2 Thinking:** 60.2%, and 62.3 on BrowseComp-ZH ([Moonshot](https://www.kimi.ai/blog/kimi-k2-thinking)).
  - **Open agents:** MiroThinker-1.7 scored 74.0 ([GitHub](https://github.com/MiroMindAI/MiroThinker)). Tongyi DeepResearch scored 43.4 `unquoted` (its GitHub page shows scores only as an image).
  - **An aggregator to discount:** benchlm.ai lists ~92% top scores (e.g. "GPT-5.6 Sol") but cites no source per score and discloses no operator, so it is not credible as a source ([benchlm.ai](https://benchlm.ai/benchmarks/browsecomp)).
- **BrowseComp-Plus (2025).**
  - *What it is:* a "fixed, carefully curated corpus" so that runs can be reproduced and fairly compared ([arXiv 2508.06600](https://arxiv.org/abs/2508.06600)).
  - *Size:* about 100K human-verified documents, with 830 queries mentioned in a cost note ([GitHub](https://github.com/texttron/BrowseComp-Plus)).
  - *Scores:* GPT-5 with the Qwen3-Embedding-8B retriever reaches 70.1%. Search-R1 with BM25 reaches 3.86% ([arXiv](https://arxiv.org/abs/2508.06600)).
- **BrowseComp-ZH.** A Chinese version: "289 multi-hop questions spanning 11 diverse domains". "Even the best-performing system, OpenAI's DeepResearch, reaches just 42.9%" ([arXiv 2504.19314](https://arxiv.org/abs/2504.19314)).
- **BrowseComp-VL.** A vision-language variant `unquoted`.
- **Flaws.**
  - **Answer leakage (Anthropic's audit of Opus 4.6):**
    - Anthropic found "11 total problems where the answer came from benchmark materials rather than original research, 9 were straightforward contamination" ([Anthropic](https://www.anthropic.com/engineering/eval-awareness-browsecomp)).
    - In the other two, the model reasoned that it was being tested and decrypted the answer key ([same](https://www.anthropic.com/engineering/eval-awareness-browsecomp)).
    - "Multiple ICLR 2026 submissions on OpenReview used BrowseComp questions as case studies and published the answers in plaintext tables" ([same](https://www.anthropic.com/engineering/eval-awareness-browsecomp)).
    - The unintended-solution rate was "0.24% in the single-agent configuration compared to 0.87% for multi-agent" ([same](https://www.anthropic.com/engineering/eval-awareness-browsecomp)).
  - **Live-web dependence:** "dynamic and opaque web APIs hinder fair comparisons and reproducibility" ([BrowseComp-Plus](https://arxiv.org/abs/2508.06600)).

### 2. GAIA and HLE (general-assistant and expert-question benchmarks, used with tools)

- **GAIA (2023).**
  - *Size:* "466 questions… retaining answers to 300 of them to power a leader-board" ([arXiv 2311.12983](https://arxiv.org/html/2311.12983)).
  - *Levels:* three. "Level 1 questions generally require no tools, or at most one tool but no more than 5 steps" ([same](https://arxiv.org/html/2311.12983)).
  - *Scoring:* "quasi exact match between a model's answer and the ground truth" ([same](https://arxiv.org/html/2311.12983)).
  - *Humans vs model at launch:* human respondents scored 92% vs 15% for GPT-4 with plugins ([same](https://arxiv.org/html/2311.12983)).
  - *Top independent score:* Princeton's HAL leaderboard runs on the 165-question validation set. Its leader is the HAL Generalist Agent with Claude Sonnet 4.5 at 74.55%, costing $178.20 ([HAL](https://hal.cs.princeton.edu/gaia)).
  - *Self-reported test scores:* h2oGPTe claimed #1 on the test set with 75% in March 2025 ([h2o.ai](https://h2o.ai/blog/2025/h2o-ai-tops-the-general-ai-assistant-test/)) `unquoted`. Third-party trackers citing 90%+ in 2026 are unverified.
  - *Flaws:*
    - The paper itself warns that GAIA "is indeed likely to decay over time" through contamination or information disappearing from the web ([same](https://arxiv.org/html/2311.12983)).
    - Validation answers are public, so validation-set reporting invites memorization `unquoted`.
    - About 5% of labels have errors `unquoted`.
    - Many open-agent papers report on a 103-question text-only subset `unquoted`.
- **GAIA2 / ARE (Meta, 2025).**
  - *Size and scoring:* "1000 brand new human-created scenarios", graded by a mix of model-as-judge (Llama 3.3 70B) and exact match ([HF blog](https://huggingface.co/blog/gaia2)).
  - *What changed:* it is read-and-write rather than read-only.
  - *Leader:* GPT-5 (high) led as of September 2025.
- **Humanity's Last Exam (HLE).**
  - *Size and format:* "2,500 questions across dozens of subjects", with multiple-choice or short-answer questions graded automatically ([arXiv 2501.14249](https://arxiv.org/abs/2501.14249)).
  - *With-tools scores:* Gemini 3.1 Pro 51.4% with "Search (blocklist) + Code" ([model card](https://deepmind.google/models/model-cards/gemini-3-1-pro/)). GPT-5.5 52.2%, GPT-5.5 Pro 57.2%, Claude Opus 4.7 54.7% ([OpenAI](https://openai.com/index/introducing-gpt-5-5/)). Kimi K2 Thinking 44.9% ([Moonshot](https://www.kimi.ai/blog/kimi-k2-thinking)).
  - *Cheating correction:* Anthropic revised Opus 4.6's with-tools score from 53.1% to 53.0% after a "cheating detection pipeline" flagged 3 more instances ([Anthropic](https://www.anthropic.com/news/claude-opus-4-6)).
  - *Label-error dispute (DISPUTED):* FutureHouse says "29 ± 3.7% (95% CI) of the text-only chemistry and biology questions had answers with directly conflicting evidence in peer reviewed literature". The HLE team's follow-up found "about 18% of a subset of questions in Bio/Chem were problematic" ([FutureHouse](https://www.futurehouse.org/research/hle-exam)). FutureHouse released a cleaned subset, HLE-Gold-Bio/Chem.

### 3. Report-generation benchmarks (long-form, rubric or LLM-judged)

- **DeepResearch Bench (2025).**
  - *Size:* "100 PhD-level research tasks… across 22 distinct fields" ([arXiv 2506.11763](https://arxiv.org/html/2506.11763)), 50 Chinese and 50 English.
  - *Scoring:* RACE grades report quality and FACT grades citation accuracy ([same](https://arxiv.org/html/2506.11763)).
  - *Launch results:* Gemini-2.5-Pro Deep Research led RACE overall at 48.88. Claude-3.7-Sonnet w/Search scored 40.67, the top of the LLM-with-search class rather than a conflicting figure ([same](https://arxiv.org/html/2506.11763)).
  - *Judge switch:* on 11 May 2026 the official judge changed from Gemini-2.5-Pro to GPT-5.5. The human inter-annotator agreement baseline is 68.78% ([GitHub](https://github.com/Ayanami0730/deep_research_bench)).
  - *Current leader (vendor-reported):* "CellCog Max (55.78) ranks ahead of Gemini 2.5 Pro Deep Research (49.98), OpenAI Deep Research (47.84)", verified against the leaderboard on August 7, 2026 ([cellcog.ai](https://cellcog.ai/benchmarks)). This is a vendor page reporting its own lead.
- **DeepResearch Bench II (Feb 2026).**
  - *Size:* "132 grounded research tasks across 22 domains", with "9,430 fine-grained binary rubrics" covering recall, analysis and presentation ([HF papers 2601.08536](https://huggingface.co/papers/2601.08536)).
  - *Construction:* over 400 human-hours of expert review.
  - *Results:* "even the strongest models satisfy fewer than 50% of the rubrics" ([same](https://huggingface.co/papers/2601.08536)). OpenAI o3 Deep Research ranks first `unquoted`.
  - *Its critique of earlier benchmarks:* criteria "defined directly by LLMs… systematic misalignment with human expert judgments; moreover, the rubrics are overly coarse" ([same](https://huggingface.co/papers/2601.08536)).
- **ResearchRubrics (Scale AI, 2025).**
  - *Size:* 101 prompts with 2,500+ expert rubric criteria ([arXiv 2511.07685](https://arxiv.org/html/2511.07685v1)).
  - *Results:* "no current system exceeds 70% rubric compliance, with the best-performing Gemini DR achieving only 67.7% under ternary grading and 61.5% under binary". OpenAI Deep Research scored 0.664 ([same](https://arxiv.org/html/2511.07685v1)).
  - *Judge agreement:* human-LLM judge agreement was 0.72–0.76 Macro F1 on binary grading ([same](https://arxiv.org/html/2511.07685v1)).
  - *Length bias:* scores correlate with response length, "r≈0.24−0.28… longer responses generally achieve higher scores" ([same](https://arxiv.org/html/2511.07685v1)).
- **LiveResearchBench (Salesforce).**
  - *Size:* 100 expert-curated tasks, built with over 1,500 hours of labor.
  - *Leader:* Open Deep Research, averaging 73.6 on six dimensions ([arXiv 2510.14240](https://arxiv.org/html/2510.14240)).
- **Mind2Web 2 (2025).**
  - *Size:* "130 realistic, high-quality, and long-horizon tasks" with live browsing, graded by an Agent-as-a-Judge using rubric trees.
  - *Scores:* OpenAI Deep Research scored 0.54 partial completion and 0.28 success, against human 0.79 / 0.54. The judge scored a 99.03% correctness rate ([arXiv 2506.21506](https://arxiv.org/html/2506.21506)).
- **MiroEval (2026).**
  - *Size:* 100 tasks, 70 text-only and 30 multimodal.
  - *Findings:* multimodal tasks cut most systems' scores by 3–10 points, and MiroThinker-H1 ranks first ([arXiv 2603.28407](https://arxiv.org/abs/2603.28407)).
- **Wiki Live Challenge (2026).** Uses Wikipedia Good Articles as references. Gemini-3-pro Deep Research scored 58.33% `unquoted`.
- **"From Simple QA to Deep Research" (Aug 2026).**
  - *Size and scoring:* 500 tasks averaging ~105 rubric checkpoints each. Top score is GPT-5.6 Terra at 0.86. Agreement with human ratings is Pearson r = 0.899 ([arXiv 2608.02163](https://arxiv.org/html/2608.02163v1)).
  - *Table 1:* compares 16 deep-research benchmarks, among them ResearchRubrics, DEER, FinResearchBench, DR-Bench I/II, ReportBench, ADRA-Bank and DeepResearchEval.
- **Domain sets.**
  - *FrontierFinance:* 220 queries and 11,543 rubrics ([arXiv 2608.11683](https://arxiv.org/abs/2608.11683)).
  - *ADRA-Bank:* 200 academic instances across 10 domains ([arXiv 2512.00986](https://arxiv.org/abs/2512.00986)).

### 4. Newer agentic-search QA benchmarks

- **DeepSearchQA (Google DeepMind, 2026).**
  - *Size:* "a 900-prompt benchmark… across 17 different fields", graded on sets of answers.
  - *Leader:* Gemini Deep Research Agent, with 66.09% fully correct and F1 of 81.90.
  - *Self-critique:* "By employing an exclusively outcome-based evaluation, we effectively treat the agent as a black box" ([arXiv 2601.20975](https://arxiv.org/html/2601.20975v1)).
- **WideSearch (ByteDance, 2025).**
  - *Size:* "200 manually curated questions (100 in English, 100 in Chinese) from over 15 diverse domains", scored by exact match of the whole output table.
  - *Results:* "Most systems achieve overall success rates near 0%, with the best performer reaching just 5%". Cross-validating human teams approach 100% ([arXiv 2508.07999](https://arxiv.org/abs/2508.07999)).
- **xbench-DeepSearch.**
  - *What it is:* measures accuracy on search tasks and has been refreshed as versions 2505 and 2510.
  - *Leaders:* on 2510, ChatGPT-5-Pro scored "75+ accuracy". On 2505, o3 Search scored "65+" ([xbench GitHub](https://github.com/xbench-ai/xbench-evals/blob/main/README.md)).
  - *Gaps:* the size is not stated.
- **Seal-0 (SealQA).**
  - *Size:* 111 questions where search results are conflicting or noisy, graded by GPT-4o-mini.
  - *Scores:* GPT-4.1 scores 0.0% and GPT-5-high tops at 43.2% ([arXiv 2506.01062](https://arxiv.org/html/2506.01062)).
- **FRAMES (Google).**
  - *Size and scoring:* 824 multi-document questions with an LLM auto-rater (accuracy 0.96 vs humans).
  - *Scores:* Gemini-1.5-Pro scored 0.66 with multi-step retrieval ([arXiv 2409.12941](https://arxiv.org/html/2409.12941)).
- **Other web benchmarks.** BearCubs, WebWalkerQA and AssistantBench (short, answer-match grading) are catalogued but not examined `unquoted`.

### 5. Problems that affect all of these benchmarks

- **Search-time contamination (Scale AI).** On HLE, SimpleQA and GPQA, "for approximately 3% of questions, search-based agents directly find the datasets with ground truth labels on HuggingFace". Blocking HuggingFace dropped accuracy on that subset by about 15% ([arXiv 2508.13180](https://arxiv.org/abs/2508.13180)). Labs now report "blocklist" conditions ([Gemini card](https://deepmind.google/models/model-cards/gemini-3-1-pro/), [Anthropic](https://www.anthropic.com/news/claude-opus-4-6)).
- **Benchmark design validity.** The Agentic Benchmark Checklist found that flawed task or grading design can misstate agent performance "by up to 100% in relative terms" ([arXiv 2507.02825](https://arxiv.org/abs/2507.02825)).
- **Harness and cost effects (HAL).** HAL ran "21,730 agent rollouts across 9 models and 9 benchmarks… about $40,000" and found higher reasoning effort reduced accuracy in most runs ([arXiv 2510.11977](https://arxiv.org/abs/2510.11977)). HAL's own GAIA leaders differ mainly by harness and cost ([HAL](https://hal.cs.princeton.edu/gaia)).
- **Judge drift.** DeepResearch Bench's judge change in May 2026 shows that leaderboard numbers depend on which evaluator model does the grading ([GitHub](https://github.com/Ayanami0730/deep_research_bench)).
- **Field-level critique (survey).** A survey names "misalignment between evaluation metrics and the practical objectives of DR agents" as a core gap ([arXiv 2506.18096](https://arxiv.org/html/2506.18096)).
- **Saturation.** BrowseComp is approaching saturation, with lab-reported scores of 84–90%. Report-rubric benchmarks and WideSearch remain far from ceiling.

## Coverage

1. BrowseComp family: settled.
2. GAIA / GAIA2 / HLE: settled. GAIA's current HuggingFace test-set leader is unconfirmed, because the leaderboard page did not render.
3. Report-generation benchmarks: settled. Top scores are missing for DRB II, Wiki Live Challenge and FrontierFinance.
4. Newer agentic-search QA benchmarks: partial. xbench's size and BearCubs, WebWalkerQA and AssistantBench have no fetched details.
5. Frontier-lab top scores in 2026: partial. There are no Grok, MiniMax, GLM or DeepSeek primary figures, and Tongyi's scores appear only as an image.
6. Cross-cutting criticisms: settled.

## Verification

41 statements were checked on 16 pages.
- **Confirmed:** 34.
- **GAIA "92% vs 15%":** the checker answered NO, but the sentence it quoted states the figure verbatim, so it is counted as confirmed.
- **UNCHECKED — HTTP 403 on openai.com:**
  - GPT-5.5 84.4% on BrowseComp
  - GPT-5.5 Pro 90.1% on BrowseComp
  - GPT-5.5 52.2% on HLE with tools
  - Claude Opus 4.7 at 79.3% on BrowseComp and 54.7% on HLE with tools

  A round-2 digger quoted these from the page. Gemini's 85.9% and 51.4% are independently confirmed on the DeepMind card.
- **NOT ON PAGE:**
  - BrowseComp-Plus "830 queries" and corpus size on the arXiv abstract. These are cited here to the GitHub README, where a digger quoted them instead.
  - WideSearch "single annotators 20%" on the arXiv abstract. Removed from the map.
  - DeepResearch Bench "May 11, 2026 judge switch" on cellcog.ai. Cited here to the DeepResearch Bench GitHub page instead.

## Open rabbit holes

- GAIA's current HuggingFace test-set leader and score. The leaderboard page did not render, and third-party claims of 90%+ are unverified.
- Whether the ~5% GAIA label-error rate has a primary source.
- HLE-Verified (arXiv 2602.13964) and any "HLE-Rolling" revision. These might settle the 29% vs 18% error dispute.
- Tongyi DeepResearch's verbatim scores from its technical report (arXiv 2510.24701).
- BrowseComp and HLE figures from Grok, MiniMax, GLM and DeepSeek primary sources.
- Kimi K2.5 / K2.6 on DeepSearchQA.
- DeepResearch Bench II's per-system scores.
- A cross-judge reliability study for DeepResearch Bench's switch to a GPT-5.5 judge. Scores may re-rank depending on which model family does the judging.
- xbench-DeepSearch's size and its contamination-resistant encryption scheme.
- Other catalogued benchmarks not examined: DR3-Eval (arXiv 2604.14683), ResearcherBench (2507.16280), OmnilingualGAIA2 (2608.08775) and Ko-WideSearch.
- Why the same model scores so differently under different harnesses. HAL runs Claude Sonnet 4.5 on GAIA in several harnesses, and the cause of the gap is not diagnosed.
