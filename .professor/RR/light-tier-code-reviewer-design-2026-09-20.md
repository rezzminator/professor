# RR — The LIGHT tier of a two-tier LLM code reviewer: cheapest accurate design for a small diff, ADOPT vs DISTILL-AND-BUILD

Question: the LIGHT tier of a two-tier LLM code reviewer — the cheapest design that reviews a SMALL diff accurately. North star: least token price AND most accuracy, judged together as accuracy per token. Decide ADOPT (take one existing open implementation and adapt it in one place) versus DISTILL-AND-BUILD (take the proven parts of several and write our own).

---

## Answer

**DISTILL-AND-BUILD.** No existing open implementation is adoptable into our harness without rewriting the part that matters: PR-Agent (MIT, the best-documented light design) is a Python service that fetches GitHub PRs and emits YAML — what is portable is its *prompt text and context policy*, not its code; Claude Code `/code-review` has no cheap path (4 parallel agents + revalidation, and it is the sibling heavy tier); Codex's `/review` prompt survives only as an unlicensed third-party gist; Kodus is AGPL-3.0, which is disqualifying for a repo that ships templates. The distilled design is **one Haiku 4.5 call, diff plus enclosing-function context, behind a deterministic pre-pass**, at a COMPUTED **~$0.005 and ~2,950 tokens per review** — with the honest caveat that the single most load-bearing number in the whole case (F1 0.657 under 10 lines collapsing to 0.043 over 150) rests on one unreviewed preprint that no independent benchmark corroborates, so the bake-off at the end is not optional.

---

## Map

### 1. Candidate matrix

Every cell carries its source. "COMPUTED" means I derived it from a stated prompt size plus a stated diff size; "UNVERIFIED" means no primary source confirmed it and names where I looked.

| Candidate | Model(s) | Tokens/review (in/out) | $/review | Latency | Measured accuracy | Licence / portability | Checks / deliberately skips |
|---|---|---|---|---|---|---|---|
| **PR-Agent `/review`** | Configurable (GPT/Claude) | In: UNVERIFIED — maintainers state they lack the telemetry ([issue #281](https://github.com/qodo-ai/pr-agent/issues/281)). Prompt file alone ~34,000 chars ≈ **6,500–8,500 tok COMPUTED** ([toml](https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/pr_reviewer_prompts.toml)) | $0.02–0.10 UNVERIFIED (search-snippet only) | ~30s ([repo](https://github.com/The-PR-Agent/pr-agent)) | c-CRAB pass rate 23.1% ([arXiv 2603.23448](https://arxiv.org/html/2603.23448v1)) | **MIT.** Python service, GitHub-coupled. Portable part = prompt text + context defaults | Bugs/security/tests on `+` lines only; skips docstrings, type hints, comments, unused imports |
| **PR-Agent `/improve`** | Same | ~6,500 chars ≈ **1,600–1,900 tok COMPUTED** ([toml](https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/code_suggestions/pr_code_suggestions_prompts.toml)) | as above | ~30s | — | MIT | `focus_only_on_problems` flag toggles "critical bugs only"; explicit empty-list permission |
| **Claude Code `/code-review`** | Haiku gate → Sonnet summary → 2× Sonnet + 2× Opus → revalidation | ~3,200 chars instructions ≈ **800 tok COMPUTED**, but ×7+ calls | UNVERIFIED | UNVERIFIED | c-CRAB 32.1%, best of four agents ([arXiv 2603.23448](https://arxiv.org/html/2603.23448v1)) | Anthropic OSS. **No cheap single-agent path exists** — confirmed by direct read | Flags only compile failures, definitive logic errors, unambiguous CLAUDE.md violations; never style, input-dependent issues, pre-existing bugs, linter-catchable issues |
| **Codex `/review`** | `review_model` configurable, default unnamed ([docs](https://learn.chatgpt.com/docs/code-review?surface=app)) | UNVERIFIED | UNVERIFIED | UNVERIFIED | c-CRAB 20.1% | **Prompt only via an unlicensed third-party [gist](https://gist.github.com/cbh123/ce4893a10ed2b87a89d9114b08118a08) — authenticity UNVERIFIED.** Not safely adoptable | P0 = "Drop everything to fix… only for universal issues that do not depend on any assumptions about the inputs"; P1 = "Urgent" |
| **Copilot code review "Lite"** | Undisclosed "carefully tuned mix" ([docs](https://docs.github.com/en/copilot/concepts/agents/code-review)) | Undisclosed | **$0.05–$1 in AI credits** (Balanced $0.25–$5) — the only vendor publishing any $ band | ~30s (prior survey) | 72.9% comment resolution ([arXiv 2607.21997](https://arxiv.org/abs/2607.21997)) | Closed | Lite = "fast, targeted feedback on common issues"; Balanced routes to a higher-reasoning model for complex/security/cross-service |
| **Cursor Bugbot Low** | Undisclosed | Undisclosed | Undisclosed | Low = "cheaper reviews that take longer" ([docs](https://cursor.com/docs/bugbot)) | **Low tier unpublished**; Default 0.7 bugs/run, High 0.95 ([changelog](https://cursor.com/changelog/05-11-26)) | Closed | — |
| **Qodo "Fast"** | "a single pass on a light model" ([blog](https://www.qodo.ai/blog/building-an-adaptive-router-for-code-review-depth/)) | Undisclosed | Undisclosed | Router-wide −27% avg runtime | **Fast-alone F1 never isolated**; router aggregate matches all-Deep F1 at −21% cost | Closed | Routes on hunk count; uncertainty escalates one-way |
| **CodeRabbit "chill"** | Undisclosed (says Claude Agent SDK, version unstated) | Undisclosed | Undisclosed | Undisclosed | 2.3% FPR third-party claim; 45% under independent re-run of a rival benchmark | Closed | Profiles vary *rule strictness* (chill = "real bugs only"), not model or cost ([docs](https://docs.coderabbit.ai/reference/configuration)) |
| **Greptile standard** | Undisclosed | Undisclosed | ~$0.60/review COMPUTED (1 credit; $30/seat ÷ 50 credits); T-Rex = 3 credits | Undisclosed | Own benchmark 82% recall-only; **45% on independent re-run** | Closed | — |
| **Kodus** | Undisclosed | Undisclosed | Undisclosed | Undisclosed | Severity floor **demonstrably not holding**: 67.8% of suggestions tagged high/critical vs 0.7% low ([issue #1948](https://github.com/kodustech/kodus-ai/issues/1948)) | **AGPL-3.0 — disqualifying for a template-shipping repo** | Custom "Kody Rules" |
| **shippie** | Configurable | UNVERIFIED | UNVERIFIED | — | — | **MIT.** Agentic tool-calling loop; prompt file not located | `CUSTOM_INSTRUCTIONS` appended to base prompt |
| **"Bigger Isn't Always Better" single-pass prompt** | Haiku 4.5 / Sonnet 4.6 / GPT-5.4 mini / Minimax M2.7 / GLM-5 Turbo | Out (measured): **Haiku 451, Sonnet 346, GPT-5.4 mini 198, Minimax 919, GLM 841**. Input never reported | **Haiku $0.003, Sonnet $0.010** | — | Haiku F1 0.365 / Sonnet 0.343; Haiku recall +18.1%, Sonnet precision better (0.558 vs 0.486). **100 of 150 samples synthetic** | Prompt text **NOT RECOVERABLE** — see failed lookups | JSON with type, severity, file, lines, description, concrete failing input, citation |

### 2. Question 1 — does one extra cheap verify call pay for itself at small diff size?

**An ablation exists, and it shows a trade rather than a gain.** In the only pipeline that ablates a validator stage ([arXiv 2505.17928](https://arxiv.org/html/2505.17928), 45 real industrial C++ merge requests tied to production incidents, avg 94.54 lines), adding stages moves Left Flow slicing from **KBI 37.04 / CPI-1 9.77** (single reviewer) → **31.11 / 17.51** (+Meta Reviewer) → **20.00 / 22.07** (+Validator). KBI is recall of labelled critical bugs; CPI-1 is a precision-weighted blend. The validator **more than doubles precision-weighted score while nearly halving recall** — it deletes findings, it never adds them. The same inversion holds for every slicing strategy in the table.

Corroborating direction, not magnitude: BitsAI-CR's ReviewFilter takes offline precision **57.03% → 65.59%** (Table 2), with an 18-week online Go deployment trending **35.6% → 75.0%** ([arXiv 2501.15134](https://arxiv.org/html/2501.15134v1)). *(Note: the prior survey's "+8.5pp" is the Table 2 delta; a "30.92% → 65.59% Go-specific" framing circulating in my own digging is misattributed — the 65.59% is the offline overall figure.)*

**The cost half of the question is unanswered by every source.** Neither 2505.17928, nor SWE-Review, nor LLM4PFA, nor BitsAI-CR states verify-pass cost as a fraction of find-pass cost in tokens, dollars, or latency. BitsAI-CR gives only inference time by reasoning pattern (Conclusion-First 1.7s/sample shipped, Reasoning-First 31.0s). Anthropic's security-review filter runs **one API call per finding, sequentially, unbatched** ([findings_filter.py](https://github.com/anthropics/claude-code-security-review/blob/main/claudecode/findings_filter.py)) — so its cost scales with finding count, not as a fixed multiplier. **No source stratifies the validator's effect by diff size**, so the specific question "does verify pay at <50 lines" has no published answer. I looked; none exists.

**COMPUTED for our design:** a verify pass over findings only (~450 tok findings + ~600 tok prompt + ~800 tok diff re-sent in, ~200 out) costs ~$0.003 against a ~$0.005 find pass — **roughly +60%**. Verdict: single-pass at this tier, with verify as a flag the bake-off settles, because the measured recall cost is large and this tier's job is cheap recall on small diffs where precision is already at its best.

### 3. Question 2 — how much context, and what does each added token buy?

This is the best-evidenced question in the map, and one source is **Go-specific**.

[arXiv 2606.01859](https://arxiv.org/html/2606.01859v1) (1,438 Go review instances, DeepSeek-V3, gopls), Table III, RefineEM:

| Context strategy | RefineEM | Delta | Avg added lines |
|---|---|---|---|
| No context (diff only) | 21.83% | — | 0 |
| List-Nbr (neighbouring lines) | 23.62% | +1.79pp (+25.7 cases) | 66.8 |
| List-Sem (LSP-based) | 23.71% | +1.88pp | 81.4 |
| List-Sim (IR co-change) | 23.99% | +2.16pp | 83.2 |
| **List-Nbr \| Sim (best)** | **25.59%** | **+3.76pp** | ~149.0 |
| Three-way combination | 24.87% | *below* the two-way | more |

So: **one hop of context pays; a second hop pays less; a third reverses.** Independently confirmed in two other shapes — in [arXiv 2505.17928](https://arxiv.org/html/2505.17928) "Left Flow" matches or beats the larger "Full Flow" on CPI-1 under stricter pipelines (+Validator: 22.07 vs 20.97) despite fewer tokens, attributed to distraction; and in [CodeFuse-CR-Bench](https://arxiv.org/html/2509.14856) GPT-5 swings **49.87 (top-3 retrieval) → 40.17 (top-5)**, a 9.7-point *loss* from two more chunks, while Gemini 2.5 Pro stays flat within ~0.9 points. There is no universally safe context volume; it is model-dependent.

PR-Agent's shipped defaults encode the same instinct, and are worth copying verbatim ([configuration.toml](https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/configuration.toml)): `patch_extra_lines_before = 5`, `patch_extra_lines_after = 1`, `allow_dynamic_context = true`, `max_extra_lines_before_dynamic_context = 10` — "will try to include up to 10 extra lines before the hunk in the patch, until we reach an enclosing function or class". Note `patch_extension_skip_types = [".md", ".txt"]` — PR-Agent does not expand context for markdown, which matters for our repo.

One caveat pointing the other way: [SWE-Review](https://arxiv.org/abs/2607.06065) finds agentic review beats single-turn in *both* diff-only and diff+context settings with the model fixed, and argues "the main advantage is not merely having more context, but being able to adaptively gather the correct evidence." Our harness has Read/Grep/Bash and *can* be agentic — but SWE-Review's only token figure compares model sizes, not agentic vs single-turn, so the ROI is unpriced. Figure-read approximate values only (DA ~75.6% agentic vs ~65% single-turn diff+context for one model); exact table cells were not extractable.

### 4. Question 3 — which finding classes to keep, and does a deterministic pre-pass beat LLM tokens?

**Drop at this tier, with evidence:** performance bugs (**0.0% recall for four of five models**; the diff simply lacks execution/data-scale context), best-practice (0.0–6.7%), and design/maintainability/documentation (c-CRAB 7.9–27.0%, needs repo-convention knowledge). **Keep:** security (69.6–71.1% recall and commoditized — every model scores alike), logic bugs (12.7–24.5%), functional correctness and robustness (c-CRAB's strongest, verifiable from the diff).

**The deterministic gate for our languages.** golangci-lint's default set is small — **errcheck, govet, ineffassign, staticcheck, unused** ([docs](https://golangci-lint.run/docs/linters/)). `prealloc`, `perfsprint`, `gosec` are bundled but off by default; **`nilaway` is not in golangci-lint at all** and must run standalone; **`go test -race` is not a linter** but a `go test` flag — concurrency races are a separate CI step, not a static pass. For the rest of the repo: **ShellCheck** (+`shfmt`) for shell, **markdownlint** and **Vale** for markdown — neither markdown tool catches semantic defects like contradictions or dangling references, which is exactly the defect class our prompt-file-heavy repo cares about.

**The load-bearing number for this design is missing.** No paper reports what fraction of LLM review findings a linter would already have caught. The closest, [arXiv 2502.06633](https://arxiv.org/html/2502.06633v1), filtered 7,884 samples to 1,245 where both a static and a learning-based system commented — about 16% co-occurrence — but treats that purely as a data-balancing step and never measures redundancy or type agreement. Likewise **no ablation exists on whether feeding linter output *into* the review prompt helps or hurts**; the entire adjacent literature (LLM4SA, LLM4FPM, LLM4PFA) runs the *reverse* direction, using LLMs to suppress static-analyzer false positives at 72–96% filtering with 0.93 recall. I searched both directions and found none.

Static analyzers' own FP burden is the reason the gate must be narrow: an industrial case found **328 of 433 warnings were false positives (76%)**, against LLM review precision of 0.49–0.56 — so a deterministic pre-pass is justified for the *classes LLMs score ~0 on*, not as a general-purpose filter.

### 5. Question 4 — finder model and the real effect of caching

**Haiku 4.5 is the finder.** F1 0.365 vs Sonnet 4.6's 0.343, recall +18.1%, at **$0.003 vs $0.010 (3.2×)**; Sonnet wins precision (0.558 vs 0.486) — the small model is the better finder, the large model the better filter. This is the one headline claim with **genuinely independent corroboration**: Qodo's benchmark on **400 real GitHub PRs** found Haiku 4.5 beat non-thinking Sonnet 4 (6.55 vs 6.20 suggestion quality, 55%/45% win rate) and in thinking mode outscored Sonnet 4.5 (7.29 vs 6.60, 58%), and Qodo consequently ships Haiku 4.5 as a default reviewer ([benchmark](https://www.qodo.ai/blog/thinking-vs-thinking-benchmarking-claude-haiku-4-5-and-sonnet-4-5-on-400-real-prs/), [defaults](https://www.qodo.ai/blog/qodos-default-reviewers-why-we-picked-gpt-5-2-gemini-2-5-pro-and-claude-haiku-4-5/)).

**Prompt caching buys our design nothing, and this is confirmed at two primary sources.** Haiku 4.5's **minimum cacheable prompt is 4,096 tokens** (Sonnet: 1,024) ([prompt-caching docs](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)) — a distilled reviewer prompt sits *below* that floor. And Claude Code documents: *"A subagent starts its own conversation with its own system prompt and tool set, separate from the parent's. Its first request doesn't read the parent's cache, because the two prefixes differ, and it warms a cache of its own across its turns. Subagents fall outside the main-conversation TTL bucket, so they get five minutes"* ([Claude Code caching](https://code.claude.com/docs/en/prompt-caching)). A **one-shot sub-agent invocation therefore gets zero cache benefit regardless of TTL settings** — there is no second read to hit. Overrides exist (`subagentPromptCacheTtl`, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL`, frontmatter `experimental.cacheTtl`) but they change TTL, not this.

Confirmed prices, Haiku 4.5 ([pricing](https://platform.claude.com/docs/en/about-claude/pricing)): input **$1/MTok**, output **$5/MTok**, 5m cache write $1.25, 1h cache write $2, cache read **$0.10 (0.1×)**. **Batch API halves both ($0.50/$2.50) and stacks with caching** — *"These multipliers stack with other pricing modifiers, including the Batch API discount"* — at best-effort latency (typically under an hour, 24h SLA). Batch is viable for a nightly sweep, not for a blocking pre-merge gate.

Tool-use overhead is published per model: Haiku 4.5 adds **496 tokens** for `tool_choice: auto`, 588 for `any`/`tool`, plus per-tool definitions (bash +244). The **total base overhead of a minimal Claude Code sub-agent invocation is undocumented** — a "10K–20K tokens" figure appeared only in a search snippet and is UNVERIFIED.

### 6. Question 5 — prompt techniques, measured vs merely believed

This is where consensus and evidence part company most sharply.

- **Chain-of-thought: measured, and it HURT.** On static bug detection, explicit CoT moved GPT-4o **0.50 → 0.49** and Claude-Opus-4 only **0.40 → 0.42**, the authors concluding CoT can "paradoxically degrade performance" for models that already reason internally ([arXiv 2601.18844](https://arxiv.org/html/2601.18844v1)). **Do not add a reasoning preamble instruction.** No study tests reasoning-effort levels (low/medium/high) on code review specifically.
- **"Require a concrete triggering input": the most-recommended noise control, and NEVER MEASURED.** The CoT paper above tests basic prompts, CoT, few-shot and bug-type augmentation but nothing requiring a proof-of-concept or triggering input. I searched specifically for it and found no controlled measurement anywhere — only practitioner advice. This is a genuine hole under the single most-cited technique.
- **Few-shot vs zero-shot for code review: no controlled ablation found.** Adjacent tasks only (requirements-defect prediction reaching ~0.70 precision at k=12; MPI error detection). None report the token cost of the examples. Searched "few-shot prompting code review defect detection ablation" and "in-context learning code review comment generation."
- **Severity floors are not self-enforcing.** Kodus, with a stated policy reserving high/critical for "broken functionality, crashes, data loss or security," tagged **67.8% of suggestions high/critical and 0.7% low** over 30 days ([issue #1948](https://github.com/kodustech/kodus-ai/issues/1948)) — though the same issue traces part of it to a parse-failure fallback bug, muddying model-miscalibration vs pipeline-defect. No other study measures LLM severity self-labelling calibration. A severity floor is a filter you must enforce in code, not a sentence you can trust the model to obey.
- **Inline suggestion format: measured, and the strongest lever on uptake.** Across **54,791 AI review comments on 342 repos**, presence of an inline code suggestion was the strongest predictor of resolution; length/complexity predicted negatively ([arXiv 2607.21997](https://arxiv.org/abs/2607.21997)). Crucially the paper treats resolution as a **weak proxy** — of 470 unresolved discussions, the commonest causes were "incorrect suggestions" *and* "intentional design decisions", so resolution conflates correctness with developer agreement.
- **Zero-findings permission: universal in prompts, unmeasured in effect.** Only **SWR-Bench** ([arXiv 2509.01494](https://arxiv.org/pdf/2509.01494)) builds clean-PR negative controls (500 Clean vs 500 Change) explicitly to measure false alarms — and a full-text read could not recover any clean-PR-specific FP number; tables did not survive extraction. **WebFixBench** defines a "clean-control false-alarm rate" as a first-class metric with 4 of 15 cases bug-free ("the correct review of them is silence") but states plainly that **no model results are published yet**. So: **no populated clean-diff false-positive rate exists in the literature.**

### 7. Question 6 — the upper boundary of this tier

The published boundary is the F1-vs-size curve: **0.657 under 10 lines, the 10–50 line bucket the sweet spot, 0.043–0.070 above 50–150 lines** — with the paper's own bucket boundaries stated inconsistently between abstract (10 vs 150+) and §4.5 (10, 10–50, >50). Its own top recommendation is that pre-chunking large diffs is "the highest-leverage intervention", i.e. the right response to a big diff is decomposition, not a bigger model.

Beyond that: Qodo routes on **hunk count, not line count**, refined by an LLM classifier over logic complexity, dispersion, uncertainty and blast radius, with **uncertainty as a one-way escalation** — but publishes no numeric threshold (its blog's only numbers are dataset-inclusion criteria: 3+ files, 50–15,000 lines). **No documented hard diff-size or file-count cap was found for Copilot, CodeRabbit or Cursor.** JIT-defect-prediction features (churn, change size, complexity) are well-established for change-risk scoring but **no paper wires them into an LLM-review router** — that is open ground.

**Practical boundary for us, evidence-based but not independently corroborated: escalate above ~50 changed lines, or on hunk count / dispersion, or on a path-risk hit, or on stated model uncertainty (one-way).**

### 8. Question 7 — where light review demonstrably misses

- **Cross-file bugs.** Best available proxy: **29.31%–69.56% of bugs across six projects involve multiple buggy files** ([bug-localization study](https://weiqin-zou.github.io/papers/IST2025.pdf)). This measures fixes touching multiple files, not "undetectable from the diff", so treat as a proxy — but it means a diff-scoped reviewer is structurally blind to roughly a third to two-thirds of real defects.
- **Performance bugs: 0.0% recall for four of five models.** Not a tier problem — a universal blind spot.
- **Real vs synthetic.** F1 **0.847 synthetic → 0.066 real** (Haiku, 92% drop; others 94–99%), driven by "20× larger diffs, formatting noise, and multi-file interactions absent from synthetic datasets." Any vendor number built on injected mutations should be discounted accordingly.
- **Repo-convention classes** (design, maintainability, documentation): c-CRAB 7.9–27.0%.
- **Concurrency/race bugs and API-contract breaks: UNVERIFIED** — neither category is directly measured in the sources reached.
- **Prompt/markdown instruction files as a defect class: NOTHING FOUND.** No tooling, no benchmark, no study of LLMs reviewing natural-language instruction files for contradictions, dangling references or ambiguity. The one adjacent result is a security paper on hidden-comment injection in markdown Skill files ([arXiv 2602.10498](https://arxiv.org/html/2602.10498v1)) — about attacks, not defect detection. **For a repo whose primary artifact is agent prompt files, the light tier's largest blind spot is the one with no literature behind it at all.**

---

## Pros and cons, weighed against each other

- **PR-Agent** — *Pro:* MIT, single call, the only candidate publishing its context-expansion defaults and a compression algorithm, prompt readable in full. *Con:* prompt is ~34K chars of GitHub-shaped YAML scaffolding we would mostly delete; Python service architecture is irrelevant to us; c-CRAB 23.1% is mid-pack; no token telemetry even from its maintainers.
- **Claude Code `/code-review`** — *Pro:* best-in-class skip-list and "never flag" criteria, already Claude-native, c-CRAB best at 32.1%. *Con:* architecturally heavy by design with no cheap path; it *is* the sibling heavy tier; borrowing its fan-out would defeat this tier's purpose.
- **Codex `/review`** — *Pro:* the cleanest severity definition found (P0 "only for universal issues that do not depend on any assumptions about the inputs" is exactly the right floor). *Con:* text available only via an unlicensed gist of unverified authenticity — re-author the idea, never copy the words.
- **Copilot Lite / Bugbot Low / Qodo Fast / CodeRabbit / Greptile** — *Pro:* they prove the tiering concept commercially and Qodo proves the routing objective (max F1 − λ·cost). *Con:* all closed; not one publishes tokens per review; Bugbot's Low tier has *no* published numbers at all; Qodo never isolates Fast-alone F1. Nothing here is adoptable, only imitable.
- **Kodus** — *Pro:* open, real production data. *Con:* **AGPL-3.0**, and its own tracker documents its severity floor failing in production.
- **shippie** — *Pro:* MIT, agentic loop closest in shape to our harness. *Con:* prompt file never located; no cost or call-count data.
- **The benchmark single-pass prompt** — *Pro:* the only design with measured F1 *and* $/review at small diff sizes, and its schema (concrete failing input + citation) is well-judged. *Con:* **the literal prompt is unrecoverable**, and the paper is an unreviewed preprint with zero independent citations.

## Recommendation — DISTILL-AND-BUILD

Take, from each source, only the part that source actually evidences:

1. **Shape** — one Haiku 4.5 call, diff-scoped. *From PR-Agent's one-call-per-tool design; supported by the ensemble result that two models UNION to worse F1 (0.365 → 0.333).*
2. **Context policy** — expand each hunk to its enclosing function, capped at ~10 lines before / few after; **one hop only, never stacked**; skip expansion for `.md`. *From PR-Agent's shipped defaults + the Go ablation's +1.79→+3.76pp-then-reversal curve.*
3. **Skip-list** — never flag pre-existing issues, linter-catchable style, lint-silenced code, or input-dependent speculation. *Verbatim-in-spirit from Claude Code `/code-review`, the strongest c-CRAB performer.*
4. **Severity floor** — report only defects that are wrong "regardless of input", re-authored in our own words from Codex's P0 definition — and **enforced in the harness by dropping anything below the floor**, not trusted to the model. *From the Kodus 67.8% failure.*
5. **Output** — file:line, the verbatim quoted line, a concrete triggering input, and an inline suggestion when the fix is ≤6 lines. *Suggestion format is the one output choice with measured uptake effect; the triggering-input requirement is adopted on judgement, explicitly flagged as never having been measured.*
6. **No chain-of-thought instruction, no few-shot examples.** *CoT measured to hurt; few-shot unmeasured for this task and costs tokens.*
7. **Deterministic pre-pass** — golangci-lint (defaults + gosec, prealloc, perfsprint), `go test -race`, ShellCheck, markdownlint — run first via Bash, its output *excluded* from the prompt, with the prompt instructed not to report those classes. *Justified for the classes LLMs score ~0 on; note the linter-output-in-prompt question has no ablation either way.*
8. **Single pass, verify behind a flag.** *The one validator ablation halves recall to double precision; no source prices it; the bake-off decides.*
9. **Escalate** above ~50 lines, on hunk count/dispersion, on path-risk, or on stated uncertainty (one-way).

### Expected cost — COMPUTED, with inputs stated

Inputs: distilled prompt ~1,500 tok; 30-line diff with function expansion ~1,000 tok (at 2.5–3.5 chars/token, an industry ratio, not Anthropic-sourced — Anthropic publishes only ~4 chars/token for English prose); tool definitions ~450 tok; output 451 tok (the *measured* Haiku average). Haiku 4.5 at $1/$5 per MTok.

- **Input ≈ 2,950 tok → $0.00295; output 451 tok → $0.00226. Total ≈ $0.005 per review**, ~2,950 in / ~450 out.
- Consistent with the only published figure for a comparable design ($0.003, Haiku, no context expansion).
- **Caching saves nothing** (prompt below Haiku's 4,096-token floor; one-shot sub-agent never hits a warm cache). Batch API would halve it to **~$0.0026** if reviews can run non-blocking.
- Adding the verify pass: **+~$0.003, roughly +60%.**
- **Excluded and UNVERIFIED:** the Claude Code sub-agent's own base system-prompt overhead, which is undocumented and could dominate all of the above. Measure it before trusting this figure.

### The smallest bake-off that would confirm it

Vendor numbers are soft and the central diff-size claim is uncorroborated, so confirm on our own labelled history:

1. **Build labels from git.** Find fix commits (`git log --grep`, revert commits, commits whose message references an issue); `git blame` the lines each fix touched to identify the commit that *introduced* the defect. That introducing commit's range is a **labelled positive** with a known ground-truth finding. Take **30 positives under 50 changed lines** — this tier's actual domain.
2. **Add 30 negative controls** — small diffs no later fix ever touched (doc edits, mechanical refactors). This is the clean-diff FP measurement the entire literature is missing, and it is cheap for us to produce.
3. **Four arms, same 60 diffs:** (A) Haiku single-pass, diff only; (B) Haiku single-pass + enclosing-function context; (C) B + verify pass; (D) Sonnet single-pass.
4. **Two metrics only:** recall of the known injected-by-history bug, and **findings per clean diff** (the noise proxy). Report accuracy per dollar.
5. **Cost:** 240 reviews ≈ **$1.20–2.00 COMPUTED** — an afternoon. A/B decides context expansion, B/C decides the verify flag, B/D re-tests Haiku-vs-Sonnet on *our* code rather than on someone's preprint.

---

## Rabbit holes left open

**Failed lookups (named):**
- **The literal "VibeOps production review system prompt"** behind the 0.657 small-diff F1 — the paper cites a `vibeops-mcp/evals/` repo that could not be found on GitHub under any search; its only other citation is a marketing site. The single most adoptable artifact in the field is unreachable.
- **Martian's full leaderboard with cost/latency columns** — the live dashboard returned title-only on repeated fetches across two diggers; the repo stores results as JSON/xlsx artifacts, not a rendered table. Its Haiku-vs-Sonnet replication is therefore attested **only inside the paper that cites it** — circular.
- **SWR-Bench's clean-PR false-positive rate** — full-text read recovered no such number; tables did not survive PDF extraction. Cannot distinguish "not reported" from "lost in conversion."
- **SWE-Review's exact tables** — figure-read approximations only; agentic-vs-single-turn numbers are indicative, not exact.
- **Kodus and shippie prompt files** — never located in either repo.
- **golangci-lint's own false-positives doc page** — linked by their docs, not retrievable this pass.

**Unverified numbers, with where I looked:** tokens per review for every commercial light tier (no vendor publishes it; PR-Agent and Claude Code maintainers both state they lack the telemetry); Bugbot Low's bugs/run, cost and latency; Qodo Fast's isolated F1; CodeRabbit's and Greptile's models and prices; Codex's default review model and its prompt's authenticity; the Claude Code sub-agent base token overhead; the code chars-per-token ratio (industry rule of thumb, not Anthropic-sourced).

**Questions no source settled:**
- **The verify-pass cost ratio** — precision gains are published by four systems; not one prices the pass. Directly blocks the accuracy-per-token calculus this query is built on.
- **Whether a validator helps more or less on SMALL diffs** — no source stratifies it.
- **What fraction of LLM findings a linter would already catch** — the load-bearing number for the deterministic pre-pass; measured nowhere.
- **Whether linter output in the prompt helps or hurts** — no ablation in either direction.
- **Any measured effect of requiring a concrete triggering input** — the most-recommended noise control in the field, never isolated in a benchmark.
- **Few-shot vs zero-shot for code review** — no controlled ablation.
- **A populated clean-diff false-positive rate** — WebFixBench defines the metric and has published no results; SWR-Bench has the controls and does not report the number.
- **Independent corroboration of F1-stratified-by-diff-size** — the routing rule's foundation rests on one unreviewed 7-page preprint with 13 references, three industry authors with no prior code-review publications, zero citing works, and an unlocatable artifact repo. Its model names all check out as real dated releases and its Haiku-beats-Sonnet finding *is* independently corroborated by Qodo's 400-PR benchmark — but **the diff-size collapse itself is corroborated by nothing.** Diff-XYZ and AACR-Bench were identified as candidate independent sources and not retrieved.
- **Reviewing prompt/instruction files as code** — no tooling, no benchmark, no literature. Our repo's dominant artifact type is the field's blankest spot.
