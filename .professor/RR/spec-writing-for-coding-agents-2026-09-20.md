# RR — How others turn a software job into executable specs for LLM coding agents, and which workflows are most novel and most efficient

Question: how do other people turn a software job into executable specs for LLM coding agents — the "spec" half of an AI software development lifecycle — and which of those workflows are the most novel and the most efficient (measured in tokens, dollars, wall-clock, or success rate)?

---

## Answer

The field has converged on one shape — a three-artifact pipeline (requirements → design → ordered task list) whose tasks carry an exact file path — and almost nobody measures it: every shipped SDD tool (Spec Kit, Kiro, BMAD, Taskmaster, Agent OS, OpenSpec) publishes templates and zero cost/success data, while all the hard numbers live in academic pipelines that decompose *without* a human-readable spec (Agentless at 27-32% of SWE-bench Lite for $0.34-0.70/bug). The design under judgement is well supported where it matters most — ambiguity is the single largest measured killer (GPT-4 loses ~29 points of pass@1 under ambiguous requirements; specification issues are 42% of all multi-agent failures) and narrow pre-selected context measurably beats wide context — but two of its choices cut against the evidence: replacing acceptance criteria with a one-line outcome (tests-in-prompt is worth ~14 points of pass@1), and deciding every open question unilaterally (ClarifyGPT's asking beats deciding by ~7-10 points).

---

## 1. The shipped spec-driven tools — what a task file actually carries

**GitHub Spec Kit** — Constitution → Specify → Plan → Tasks. `/speckit.tasks` emits `tasks.md` lines of the form `- [ ] [TaskID] [P] [Story] Description with exact file path`; the exact file path is **required** in every task description, `[P]` marks tasks touching different files with no pending dependencies, ordering is Tests→Models→Services→Endpoints→Integration per user story, and each field constraint from `data-model.md` is quoted verbatim into the task. One feature spawns ~8 files (spec, plan, tasks, data-model, research, API, contracts, components). ~132K GitHub stars after v1.0.0 (Aug 2026). No published measurement; the circulating "60-80% fewer rework cycles" is a community claim, not a measurement. [tasks.md template](https://github.com/github/spec-kit/blob/main/templates/commands/tasks.md) · [adoption](https://winbuzzer.com/2026/05/11/meet-github-spec-kit-an-open-source-toolkit-for-sp-xcxwbn/)

**Amazon Kiro** — `requirements.md` (user stories + EARS acceptance criteria: "WHEN a user submits a form with invalid data THE SYSTEM SHALL display validation errors…"), `design.md`, `tasks.md` (numbered atomic tasks, each an independently reviewable and revertable code change). Two genuinely novel mechanisms: **waves** — Kiro builds a dependency graph over tasks and groups independents into Wave 1, dependents into Wave 2, waves sequential and tasks within a wave concurrent; and **hybrid neuro-symbolic requirements analysis** — an LLM auto-formalizes EARS clauses into SMT-lib, multiple formalizations are clustered by logical equivalence with divergence above a threshold triggering user clarification (semantic entropy), and an SMT solver performs the actual contradiction/gap analysis. The decomposition algorithm from EARS clauses to tasks is **not documented**. Fowler observed Kiro oversizing a small bug fix into 4 user stories / 16 acceptance criteria. [Kiro deep spec analysis](https://kiro.dev/blog/deep-spec-analysis/) · [feature specs](https://kiro.dev/docs/specs/feature-specs/) · [Fowler](https://martinfowler.com/articles/exploring-gen-ai/sdd-3-tools.html)

**BMAD-METHOD** — The closest analogue to the design under judgement. A Scrum Master agent shards a PRD + architecture into **hyper-detailed, self-contained story files** carrying full context, rationale, embedded tests and source-doc links, explicitly so the dev agent need not re-read anything ("no guessing"); `devLoadAlwaysFiles` pins `source-tree.md` into every story. Crucially it enforces **section-level edit permissions**: the dev agent may touch only YAML frontmatter, Tasks/Subtasks checkboxes, the Dev Agent Record, File List, Change Log and Status — the requirements/context sections belong to the SM alone. That is the "only the spec writer edits task files" rule, already shipped. (Current-release verbatim rule text and the status list could not be confirmed — v5 paths now 404 after a v6 `bmm`-module restructure; treat the section list as search-indexed, partially unverified.) [issue #496](https://github.com/bmad-code-org/BMAD-METHOD/issues/496) · [overview](https://www.augmentcode.com/guides/bmad-method-ai-development)

**Taskmaster AI** — `tasks.json` with `id, title, description, status, dependencies[], priority, details, testStrategy, subtasks[]`. `analyze-complexity` rates each task 1-10 and recommends a subtask count against a `DEFAULT_SUBTASKS` threshold, writing `task-complexity-report.json`; `expand` then splits highest-complexity-first; `next` picks the highest-priority task whose dependencies are satisfied. `validate-dependencies` detects circular chains but documents no remediation. This is the only shipped **difficulty score** found. [task-structure.md](https://github.com/eyaltoledano/claude-task-master/blob/main/docs/task-structure.md)

**Agent OS v3** — `/shape-spec` writes `agent-os/specs/{date-slug}/` containing `plan.md` (sequenced tasks), `shape.md` (scope, decisions, context), `standards.md` (**full text** of each applicable standard injected per relevant task), and `references.md` (pointers to similar existing implementations: location, relevance, patterns to borrow). It uses `AskUserQuestion` at every decision point — the user, not the agent, resolves ambiguity. The `references.md` idea is the closest published analogue to "copy exact existing code shapes". [shape-spec.md](https://github.com/buildermethods/agent-os/blob/main/commands/agent-os/shape-spec.md)

**OpenSpec** — proposal → specs → design → tasks → implement, with **delta specs** (each change folder holds only the diff against the baseline spec, keeping docs compact) and explicit brownfield positioning. One head-to-head anecdote: same CRM-dashboard task in 12 min (OpenSpec) vs 90 min (Spec Kit) vs 5.5 hours (BMAD). Single anecdote, not a benchmark. [workflows.md](https://github.com/Fission-AI/OpenSpec/blob/main/docs/workflows.md) · [comparison](https://reenbit.com/bmad-vs-spec-kit-vs-openspec-choosing-your-spec-driven-ai-framework/)

**Cline Memory Bank** is *not* a task-breakdown system — six persistent-context markdown files read wholesale at session start. [memory-bank.md](https://github.com/cline/prompts/blob/main/.clinerules/memory-bank.md) **Tessl** keeps a 1:1 spec-file-to-code-file mapping with generated code marked `DO NOT EDIT`; Fowler found non-determinism persists across regenerations from an identical spec even at that granularity. **GSD** and **Spec Kitty** exist but neither publishes its literal task-file schema (NOT FOUND). **GitHub Copilot Workspace** had the cleanest spec artifact — a "Current Specification" and "Proposed Specification" as bulleted behavior lists, then a plan as a file-by-file list of edits/creates/deletes with steps per file, user-editable and regenerable — and was sunset May 2025 into Copilot Coding Agent. [Copilot Workspace manual](https://github.com/githubnext/copilot-workspace-user-manual/blob/main/overview.md)

## 2. Academic decomposition — where the measurements are

| System | Approach | Measured |
|---|---|---|
| **Agentless** | Fixed localize→repair→validate; LLM is never given action choice | 27.33% SWE-bench Lite at **$0.34/bug**; a later snapshot 32.00% at $0.70 — [paper](https://arxiv.org/abs/2407.01489) |
| **AutoCodeRover** | AST-aware structured code search, no free exploration | 37.3% Lite / 46.2% Verified, ~$0.45-0.70/issue — [paper](https://arxiv.org/pdf/2404.05427) |
| **SpecRover** | Iterative **specification/intent inference** before repair | 31% Lite, 51.6% Verified, ~$0.65/issue — [paper](https://arxiv.org/abs/2408.02232) |
| **SWE-agent** | Agentic with an Agent-Computer Interface | 18.00% Lite, up to $4/instance; the ACI alone gives +64% relative — [paper](https://arxiv.org/pdf/2405.15793) |
| **CodeR** | Predefined multi-agent **task graph** with a verifier agent | 28.33% Lite, one submission per issue — [paper](https://arxiv.org/abs/2406.01304) |
| **MetaGPT** | SOPs as a fixed role pipeline, each role hands off a structured artifact | 85.9% HumanEval / 87.7% MBPP; beats ChatDev on tokens-per-line — [paper](https://arxiv.org/pdf/2308.00352) |
| **ChatDev** | Role chat-chains | 19,292 tokens and 762s per SoftwareDev task, 248.9 tokens/line — [paper](https://arxiv.org/pdf/2307.07924) |
| **OpenHands** | Event stream, runtime `AgentDelegateAction`, no pre-baked plan | 53.0% Verified (CodeAct v2.1 + Sonnet 3.5); ~30-80K tokens/task; **no first-party $/instance found** — [SDK paper](https://arxiv.org/pdf/2511.03690) |

The through-line: **the cheapest, most reproducible systems are the least agentic.** Agentless resolves issues for pennies precisely by refusing to let the model choose its own next action — the strongest available argument for pre-deciding everything in the spec.

## 3. The evidence that bears directly on the design

**Ambiguity is the dominant failure.** The Orchid benchmark (1,304 function-level tasks, four ambiguity types) shows GPT-4 dropping ~29-30 points of pass@1 under ambiguity (45.24% clear → ~16-17%); Claude-Sonnet-4.5 48.8 → 38.2; GPT-5 52.5 → 41.1. Models answer without asking in >63% of ambiguous scenarios. [Orchid](https://arxiv.org/html/2604.21505) MAST (1,600+ traces, 7 frameworks, kappa 0.88) puts **42% of all multi-agent failures in the specification category**, 37% inter-agent misalignment, 21% verification; spec-relevant modes measured individually: Step Repetition 15.7%, Unaware of Termination Conditions 12.4%, Disobey Task Specification 11.8%, Loss of Conversation History 2.8%. [MAST](https://arxiv.org/pdf/2503.13657) → Self-contained task files with pre-resolved decisions target the largest measured bucket.

**But asking beats deciding.** ClarifyGPT — detect ambiguity, ask a targeted question, refine, then generate — lifts GPT-4 pass@1 on MBPP-sanitized from 70.96% to 80.80%, and the five-benchmark average from 62.43% to 69.60%. [ClarifyGPT](https://arxiv.org/abs/2310.10996) SpecFix repairs an ambiguous requirement autonomously via contrastive specification inference for +4.3 points pass@1. [SpecFix](https://arxiv.org/abs/2505.07270) A spec-writer that decides *everything* unilaterally forfeits this; the measured play is to ask on the few genuinely load-bearing ambiguities and decide the rest.

**Narrow, pre-selected context beats wide context.** AACR-Bench: Qwen3-Coder-480B falls 33.82% (diff-level context) → 22.59% (file-level) → 17.60% (repo-level), a decay hierarchy the paper reports for every model tested. [AACR-Bench](https://arxiv.org/abs/2601.19494) A white-box coding-agent study finds a clean 10,991-char context passing 8/10 runs vs 3/10 at 299,140 chars — and about **half the degradation occurs with fully relevant added context**, not just noise; requirement coverage stays high (0.933-0.949) while a few decisive requirements silently drop (n=10 per condition, small sample). [context rot in coding agents](https://arxiv.org/abs/2607.17937) Chroma across 18 models: monotonic F1 decay starting by 50k tokens on a 1M-token model, 30%+ lower accuracy for mid-context information. [Chroma](https://www.trychroma.com/research/context-rot)

**But context alone is not the bottleneck.** Original SWE-bench: BM25 retrieval gives Claude 2 1.97%; **oracle** retrieval — handed exactly the gold-patch files — gives 4.8%, and narrowing to modified lines +15 lines gives 5.93%. Perfect context still leaves resolve rate under 6% in that era; editing and reasoning dominate. Also: raising BM25 context 13K→50K raised recall but *lowered* resolve rate for every model. [SWE-bench](https://www.alphaxiv.org/abs/2310.06770) (numbers secondary-source-confirmed; PDF extraction failed)

**In-repo examples measurably help.** RepoCoder's iterative retrieval over the repo's own code beats the in-file baseline by >10 points across all RepoEval settings. [RepoCoder](https://aclanthology.org/2023.emnlp-main.151/) This is the evidence for "copy exact code shapes from the code, never from memory."

**Tests in the spec are worth ~14 points.** "Tests as Prompt": GPT-4 pass@1 rises 67.13% → 80.88% when tests/acceptance criteria are included in the prompt, and models do **not** need a failing-test feedback loop — the up-front test specification captures most of the benefit. [Tests as Prompt](https://arxiv.org/pdf/2505.09027) This is the sharpest measured strike against replacing acceptance with a one-line outcome.

**Granularity.** ADaPT decomposes *only on executor failure* rather than up front, framing the tradeoff explicitly ("too coarse → insufficient effectiveness; too fine → reduced efficiency") and measuring up to **33% success-rate improvement** over fixed-granularity baselines. [ADaPT](https://arxiv.org/abs/2311.05772) A low-vs-high-autonomy prompt study (low-autonomy = schema/path-specific briefs) resolved 4/12 vs 2/12 tasks and raised checkpoint pass rate ~49% → 69.4% at near-equal API cost — but it is a **finance-workflow** benchmark (Edinburgh MSc thesis), not coding; adjacent evidence only. [thesis](https://arxiv.org/abs/2512.02230) No parametric granularity/cost curve exists for repo-level coding agents.

## 4. Context economics — the "many fresh executors" cost model

- Anthropic: agents use ~4x the tokens of chat, multi-agent ~15x; token usage alone explains **80% of performance variance** on their BrowseComp eval. Anthropic also states plainly that multi-agent is *less* suited to coding — "fewer truly parallelizable tasks than research." Vendor eval, **no independent replication, no coding-regime equivalent found**. [Anthropic](https://www.anthropic.com/engineering/multi-agent-research-system)
- Measured subagent overhead (one practitioner, ~2 weeks, one codebase): **~54,154 tokens** fixed cold-start per Claude Code subagent (an earlier viral 436k figure was a double-counting artifact); ~97% of the cold-start context is static boilerplate and only ~3% (~950 tokens) is the unique task; break-even vs reading inline is ~30-50k tokens of file reading. [measurement](https://dev.to/rulestack/what-a-claude-code-subagent-actually-costs-measuring-the-436k-token-fixed-overhead-46g6)
- Caching changes the arithmetic: cache writes cost 1.25x base input (5-min TTL) or 2x (1-hour), cache reads **0.1x** — a write pays for itself after ~2 reuses. A 95-session / 1,777-subagent / 6.8B-token dataset found only 16% of a subagent's static content cache-hitting on spawn, and that reordering static-before-dynamic content cut subagent prompt cost ~14% (system spend ~8%). A long cached session amortizes its system prompt far better than many fresh spawns. [pricing](https://platform.claude.com/docs/en/build-with-claude/prompt-caching) · [issue #74318](https://github.com/anthropics/claude-code/issues/74318)
- Cognition's counter-argument: "actions carry implicit decisions, and conflicting decisions carry bad results" — one subagent builds a Mario background, another an incompatible bird. **Entirely argument-based, no measurement.** [Cognition](https://cognition.com/blog/dont-build-multi-agents)
- TinyFish adds the "re-discovery tax": each fresh subagent re-finds the same files and re-reaches the same conclusions. Qualitative, no numbers. [TinyFish](https://www.tinyfish.ai/blog/agentic-coding-context-vs-tokens-subagents)
- Model choice swamps decomposition: on 616 paired SWE-bench Pro instances, Sonnet 4.5 (44.5%) and GPT-5 (42.5%) resolve near-equally, but cost differs by a **median 6.33x** for only 1.15x more tokens. [nilenso](https://blog.nilenso.com/blog/2026/04/08/checking-my-model-vibes-against-swe-bench-pro/)
- Spec-workflow costs in practice: BMAD ~31,667 tokens/workflow run and $800-2,000+/developer/month; one feature modelled at ~720k input + ~149k output tokens (~$3.70-7.30), with a claimed 70-100x ROI the author himself flags as not field-validated. [BMAD costs](https://reenbit.com/bmad-method-token-budget-context-engineering-roi/)

**Net: the design's cost premise is directionally right on quality (context rot, repo-context decay) but the pure token argument is weaker than stated** — caching makes a long session cheap to re-send, and the fixed ~54k cold-start means a task file must justify its own spawn. The break-even figure is the one to design against.

## 5. Contention and sizing

Two confirmed empirical papers. Xu et al. (arXiv 2607.04697, AIDev-pop, 33,596 PRs / 2,807 repos): replaying three-way merges on 747 co-active pairs gives **41.7% textual conflict cross-agent vs 19.8% intra-agent** (non-overlapping 95% CIs), 84.4% of conflicted files source code, ~42% of conflicts structural; notably only 0.5% of co-active PR pairs were cross-agent at all. AgenticFlict (arXiv 2604.03551, 142K+ agentic PRs / 59K+ repos, 107K+ merge-simulated): **27.67% overall conflict rate**, ~4.36 files and ~500 conflicting lines per conflicting PR. [Xu et al.](https://arxiv.org/abs/2607.04697) · [AgenticFlict](https://arxiv.org/abs/2604.03551) Mitigation in practice is planner-ahead, not merge-time: slice tasks by non-overlapping file ownership, have agents claim file scopes before starting, merge sequentially with rebasing, run `git merge-tree` hooks. [worktree playbook](https://www.mindstudio.ai/blog/parallel-agentic-development-git-worktrees)

Sizing: practitioner guidance converges on "under 30 minutes of human effort, 1-3 files, ~100-150 lines net diff, one describable behavior change, no new abstractions" — prescriptive, **no data behind it**. [Taim.io](https://www.taim.io/agentic-coding/planning-a-task-the-agent-can-actually-finish) The real data: agentic PR merge rate 71.48% overall (Codex 82.59% vs Copilot 43.04%; docs 84%, bug fixes 64%, performance 55%), with unmerged PRs skewing toward larger, more invasive diffs — **PR size is a documented negative predictor of merge success**, which cuts against "join by default". [failure study](https://www.alphaxiv.org/abs/2601.15195) (abstract-level only)

## 6. Ranking

**By novelty**
1. **Kiro's neuro-symbolic requirements analysis** — LLM formalizes EARS into SMT-lib, equivalence-clustering flags semantic entropy for clarification, a solver proves contradictions. Nothing else in the field does formal verification of a spec.
2. **SpecFix's contrastive specification inference** — cluster candidate program behaviors, then rewrite the requirement so only the intended one is admissible. Automated spec repair with a measured effect.
3. **Tessl's spec-as-source with DO-NOT-EDIT generated code** — the only design that makes the spec the sole artifact of record.
4. **Kiro's dependency waves** and **Spec Kit's `[P]` marker** — the only shipped contention-aware scheduling primitives.
5. **ADaPT's decompose-on-failure** — granularity as a runtime decision, not a planning one.
6. **BMAD's section-level edit permissions** — the shipped form of "only the spec writer edits the spec."

**By measured efficiency** (only these have numbers at all)
1. **Agentless** — 27-32% Lite at $0.34-0.70/bug. Best cost-per-resolution on record, achieved by removing agency.
2. **SpecRover** — 51.6% Verified at ~$0.65/issue. Best accuracy-per-dollar, and it earns it with spec inference.
3. **AutoCodeRover** — 46.2% Verified at ~$0.45-0.70.
4. **Tests-in-prompt** — +13.75 points pass@1 for the cost of pasting tests. Highest measured return per token spent in the whole map.
5. **ClarifyGPT** — +7-10 points for one clarifying question.
6. **Every shipped SDD tool** — unranked; **none publishes a measurement.**

## 7. The five ideas most worth adopting

1. **Put the acceptance in the task file, not a one-line outcome.** +13.75 points of pass@1 measured, no feedback loop required. Not a scripted command — the assertions the outcome implies, written out. This is the single best-evidenced change to the design.
2. **Ask on the few load-bearing ambiguities; decide the rest.** Ambiguity costs ~29 points and is 42% of multi-agent failures, but unilateral deciding forfeits ClarifyGPT's measured +7-10. Adopt Kiro's trigger rule: when independent formalizations of a requirement diverge past a threshold, escalate; otherwise rule and record.
3. **Make the index contention-aware by file path, not just dependency-aware.** Cross-agent conflict rate is 41.7% vs 19.8% intra-agent. Spec Kit's rule — every task names its exact file paths, `[P]` only for disjoint file sets — plus Kiro's waves is the shipped pattern; no tool found combines dependencies, contention and difficulty in one index, so this is genuinely open ground.
4. **Keep the copied-shapes rule and keep task context narrow.** AACR-Bench's diff > file > repo decay, RepoCoder's >10 points, and the coding-agent rot study (half the damage from *relevant* extra context) all say the same thing: paste the minimum exact shape, not the surrounding file. Add Agent OS's `references.md` idea — point at a similar existing implementation and name the pattern to borrow.
5. **Size against the spawn's break-even, and re-examine "join by default".** ~54k tokens fixed cold-start per subagent, cache reads at 0.1x making long sessions cheap to continue, and larger diffs predicting lower merge success together argue for a stated size band per task (the practitioner consensus is 1-3 files / ~100-150 net lines) rather than joining by default and cutting only under pressure.

---

## Rabbit holes left open

**Failed or unverifiable fetches**
- BMAD's current-release `story-tmpl.yaml` and `review-story.md` — v5 paths 404 after the v6 `bmm` restructure; the verbatim edit-permission rule and status list are search-indexed, not read from source.
- The original SWE-bench PDF — oracle-vs-BM25 numbers are secondary-source-confirmed, not read from the tables.
- AACR-Bench's full per-model table — only the Qwen row confirmed; whether the decay is universal or Qwen-shaped is unconfirmed.
- The arXiv "Runtime-Structured Task Decomposition" PDF — results tables did not extract; no numbers obtained.
- "Where Do AI Coding Agents Fail?" (2601.15195) — abstract only; the size-vs-merge-rate regression table was not read.

**Empty categories — searched, nothing found**
- No tool combines dependency graph + file contention + difficulty in one index (`dg` has deps only; Kiro's waves deps only; Taskmaster complexity only).
- No published protocol where an executor reports the **spec** is wrong and a separate planner rewrites it. SpecFix (autonomous spec repair) and Self-Healing Agentic Orchestrators (failure-class → recovery-action formalism) are adjacent, not the pattern.
- No shipped product (Devin, Cursor background agents, Factory, Jules, SWE-smith) publishes a dependency-graph-plus-difficulty planning artifact.
- No measurement comparing one long-context coding agent against many short fresh ones on the same coding benchmark. **This is the design's central premise and it is untested in the literature.**
- No independent replication of Anthropic's 4x/15x or 80%-variance figures, and no coding-regime equivalent.
- No parametric granularity/cost curve for repo-level coding agents.
- No task-file schema published for GSD or Spec Kitty; OpenSpec's literal task template not retrieved (lives under `schemas/workflow/templates/`).
- No brownfield/longitudinal case data for any SDD tool — every tutorial is greenfield.

**Claims with no measurement behind them (do not treat as evidence)**
- Spec Kit's "60-80% fewer rework cycles"; BMAD's 70-100x ROI (author-flagged as not field-validated); the 12-min/90-min/5.5-hour OpenSpec-vs-Spec-Kit-vs-BMAD anecdote (n=1); Cognition's entire anti-multi-agent argument; TinyFish's re-discovery tax; Kiro's oversizing critique (single reviewer).

**Worth a next dig**
- Kiro's EARS-clause-to-task decomposition algorithm — the single biggest documented gap in the most sophisticated shipped tool.
- ClarifyCodeBench (2607.00711) — a newer interactive benchmark likely holding head-to-head ask-vs-decide numbers.
- CodeR's verifier-agent precision/recall — bears directly on what acceptance in a task file buys.
- Norheim et al. (MIT/Airbus) on weak LLM requirement-formalization results — cited as contradicting Kiro's neuro-symbolic claims.
- Whether the 54k subagent cold-start figure replicates, and how it moves with cache-breakpoint ordering.
