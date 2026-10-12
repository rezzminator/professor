# RR — Which official TypeSafe/Jev builder tooling exists, how Jev works, and is it safe for classifying private transcripts?

Question: Find the skill (or plugin, MCP server or SDK) for BUILDING decision models with TypeSafe's Jev, and judge whether it is safe to install. (Full numbered brief: tooling inventory; builder vs `typesafe-ai` skill; API shape/limits/pricing/auth/retention; fit for an exchange-classifier with per-class probabilities that sets reasoning effort; publisher and install-time behaviour.)

## Answer

TypeSafe's only official builder tooling is the docs-only `typesafe` plugin, whose one skill is [`typesafe-ai`](https://github.com/typesafe-ai/skills). It ships no hooks, scripts or MCP config ([git tree](https://api.github.com/repos/typesafe-ai/skills/git/trees/main?recursive=1)), and no dedicated "Jev builder" or eval skill exists from TypeSafe, so the plugin is safe to install. Jev itself fits the classifier: a Choice question takes a map of [labels with descriptions and returns a probability for every option](https://docs.typesafe.ai/primitives/choice.md). Sending private transcripts is the real risk. The provider promises [no training on Input](https://typesafe.ai/legal/privacy-policy), but the [MCA](https://typesafe.ai/legal/mca) keeps a perpetual right to derive "Telemetry" (including classifications) from Customer Data, and no retention period is published.

## Map

### 1. Official tooling
- **Plugin and skill.** The marketplace `typesafe-ai` (owner "TypeSafe AI", typesafe.ai) lists one plugin, `typesafe` (source `./`). Its plugin.json gives v0.5.7, MIT, repo `typesafe-ai/skills` (read locally, `.claude-plugin/marketplace.json` and `plugin.json`). The marketplace offers no other skills, commands or agents.
- **Repo contents.** The repo tree holds only `.claude-plugin/{marketplace,plugin}.json`, `LICENSE`, `README.md` and `skills/typesafe-ai/{SKILL.md,LICENSE}`. There is no `hooks/`, `.mcp.json`, `commands/`, `agents/`, `scripts/` or `package.json` ([tree API](https://api.github.com/repos/typesafe-ai/skills/git/trees/main?recursive=1)). The repo is MIT, has "2 Commits" and was "Updated Sep 12, 2026" ([repo](https://github.com/typesafe-ai/skills), [org](https://github.com/typesafe-ai)).
- **Install commands.** For Claude Code: `claude plugin marketplace add typesafe-ai/skills`, then `claude plugin install typesafe@typesafe-ai`. For other agents: `npx skills add typesafe-ai/skills --skill typesafe-ai` ([agent-skill](https://docs.typesafe.ai/agent-skill.md)).
- **SDKs.**
  - Python: PyPI `typesafe-sdk` 0.7.4, "Released: Oct 9, 2026", MIT, Python >=3.10 ([PyPI](https://pypi.org/project/typesafe-sdk/)). The repo is `typesafe-ai/typesafe-sdk-python`.
  - JS: npm `@typesafe-ai/sdk` 0.6.0, published 2026-09-15, MIT ([npm](https://registry.npmjs.org/@typesafe-ai/sdk)).
  - A PyPI `typesafe-ai` 0.1.0 placeholder was seen only in a search snippet (unverified). Install `typesafe-sdk`.
- **CLI and MCP.** No official CLI or MCP server appears among the org's visible repos ([org](https://github.com/typesafe-ai)).

### 2. Builder skill
- **Installed skill.** `typesafe-ai` is a pointer to the live docs: "The live TypeSafe docs are the source of truth. Read them as part of the task." (local SKILL.md). It covers design: Choice, Noul and Score, fan-out, and the advice to "validate their performance in the target domain". It provides no eval harness.
- **No official builder or eval skill.** The docs' agent-skill and coding-agents pages describe only this one skill ([agent-skill](https://docs.typesafe.ai/agent-skill.md), [coding-agents](https://docs.typesafe.ai/introduction/coding-agents.md)). This is a finding.
- **Community alternatives (unofficial, 0–1 stars):**
  - [Nostoi/jev-expert](https://github.com/Nostoi/jev-expert): MIT. Ships a skill, a reviewer agent and `question_eval.py`. Its [evaluation reference](https://github.com/Nostoi/jev-expert/blob/main/skills/jev-engineering/references/evaluation.md) advises a confusion matrix and the Brier score for Choice.
  - [KHAEntertainment/jev-skill](https://github.com/KHAEntertainment/jev-skill): MIT. Includes a PreToolUse hook.
  - Two plugins ship hooks that send session content to OpenRouter: [yinjs/claude-jev](https://github.com/yinjs/claude-jev) ("each prompt with the assistant reply it answers") and [yusupsupriyadi/jev-skill](https://github.com/yusupsupriyadi/jev-skill).
- **Recommendation.** Install the official `typesafe` plugin, which is already installed. Optionally read jev-expert's evaluation method rather than installing it.

### 3. How a call works
- **Endpoint.** `POST https://api.typesafe.ai/v1/systemone`. The request carries `state`, `model` and `questions`; the response carries `answers`, `model` and `usage` ([API](https://docs.typesafe.ai/api.md); the endpoint is shown in a code block, unquoted).
- **Models.** `jev-latest` → `jev-1.13.0` ([models](https://docs.typesafe.ai/models)).
- **Context limit.** "64k tokens per request; 32k tokens for `state` plus the longest question." ([models](https://docs.typesafe.ai/models))
- **Pricing.** $0.042 per Mtok: "Charged per input token. Output tokens are free." ([models](https://docs.typesafe.ai/models))
- **Rate limits.** "100K tokens per second / 80 requests per second" ([models](https://docs.typesafe.ai/models)). DISPUTED: a third-party GitHub issue quotes the same page as 40 req/s ([issue](https://github.com/kellystuard/jev-gmail-classifier/issues/314)). The live page reads 80 today, and the page also says limits "are adjusting dynamically".
- **Latency.** "End-to-end response time is 70ms-500ms for TypeSafe." ([launch post](https://typesafe.ai/blog/introducing-system-one-models-and-jev)) The SDK's default timeout is 10.0 s ([constants](https://docs.typesafe.ai/sdk/python/api/constants.md)).
- **Authentication.** The SDK reads `API_KEY_ENV = 'TYPESAFE_API_KEY'` ([constants](https://docs.typesafe.ai/sdk/python/api/constants.md)). Your key file's variable is `KEY`, so it must be exported as `TYPESAFE_API_KEY` or passed explicitly to the client.
- **Retention and training.**
  - No training on input: "We will not train or fine tune any artificial intelligence or machine learning models on your prompts or other Input." ([privacy](https://typesafe.ai/legal/privacy-policy)), and "Jev is not trained on customer requests or responses." ([models](https://docs.typesafe.ai/models)).
  - Retention: "as long as reasonably necessary" ([privacy](https://typesafe.ai/legal/privacy-policy)). No period is published, and the gated SOC 2 report was not read.
  - Telemetry: the MCA grants "in perpetuity, any Customer Data (i) to derive and generate Telemetry". Telemetry includes "summary statistics and classifications, metrics, and learnings related to Customer's use", which "TypeSafe may Process ... without restriction, including to improve the Services" ([MCA](https://typesafe.ai/legal/mca)).
  - Hosting: "The Services are hosted in the United States" ([privacy](https://typesafe.ai/legal/privacy-policy)).
  - Subprocessors: AWS stores live request data, and Modal, Nebius and CoreWeave process prompts "but not stored" ([subprocessors](https://trust.typesafe.ai/subprocessors)).
  - ZDR: "for enterprise customers" only, via sales ([legal](https://docs.typesafe.ai/legal)).

### 4. Fit for the effort classifier
- **Custom labels with descriptions:** yes. "Each key is an option name and each value is a description of that option." Up to 255 options ([choice](https://docs.typesafe.ai/primitives/choice.md)).
- **Multi-class probabilities:** yes. "The full probability distribution across every option. The sum of all values is 1." ([choice](https://docs.typesafe.ai/primitives/choice.md)) Confidence is reported separately ([confidence](https://docs.typesafe.ai/confidence)).
- **Few-shot:** no examples parameter. Examples go inside structured instructions or criteria, for example "The example tells the model what each option does and does *not* cover." ([advanced](https://docs.typesafe.ai/primitives/advanced.md))
- **Calibration or fine-tuning on a labelled set:** not offered. No doc describes it. The closest route is the [feature-discovery cookbook](https://docs.typesafe.ai/cookbooks/autoresearch_feature_discovery.md), which trains a downstream model on Jev outputs (unquoted).
- **Batch:** many questions per call, not many states. "batching every question into one TypeSafe call is 12.2x cheaper and 10.0x faster" ([parallel questions](https://docs.typesafe.ai/cookbooks/parallel_questions.md)). Each call takes one `state`, so many exchanges mean many calls.
- **Closest documented patterns:**
  - [Intent routing](https://docs.typesafe.ai/patterns/intent-routing.md) classifies a message and routes it to a handler.
  - The [classification using confidence](https://docs.typesafe.ai/cookbooks/classification_using_confidence.md) cookbook ("The confident half is right 90% of the time; the other half, 40%") backs off to a broader class when confidence is low. This maps to "default effort when unsure".
  - [Confidence-gated routing](https://docs.typesafe.ai/patterns/confidence-routing.md) does the same with confidence as a second axis.

### 5. Publisher and install safety
- **Publisher.** A real company: "TypeSafe AI ... has emerged from stealth with $40 million in seed funding led by DCVC", founded by Diogo Almeida (ex-OpenAI), Erik Gafni and Sasha Sheng ([HPCwire](https://www.hpcwire.com/aiwire/2026/09/16/typesafe-ai-emerges-from-stealth-with-40m-in-funding-with-new-model-for-composable-ai/)). DISPUTED funding: [Investing.com/Bloomberg](https://www.investing.com/news/stock-market-news/typesafe-ai-raises-870-million-led-by-andreessen-horowitz--bloomberg-93CH-4941303) reports about $870M at a $7.5B valuation. The npm maintainer `diogo149` is consistent with the founder. The SDKs were updated in Sept–Oct 2026, so the project is maintained.
- **Install-time behaviour.** None. The plugin is Markdown and JSON only, with no hooks, scripts or MCP ([tree](https://api.github.com/repos/typesafe-ai/skills/git/trees/main?recursive=1)). The skill does tell the agent to fetch docs.typesafe.ai at use time, which is a read-only network call. The community plugins with hooks (yinjs, yusupsupriyadi, KHAEntertainment) are the ones to avoid in a framework repo.
- **Access.** Jev is "waitlisted" in early access ([HPCwire](https://www.hpcwire.com/aiwire/2026/09/16/typesafe-ai-emerges-from-stealth-with-40m-in-funding-with-new-model-for-composable-ai/)).

## Coverage
- Official tooling: settled
- Builder skill: settled (no official builder exists)
- API mechanics: settled (rate limit DISPUTED 80 vs 40 req/s)
- Data retention: partial (no published retention period; the SOC 2 report is gated; the OpenRouter Jev data-policy page returned 404)
- Use-case fit: settled
- Install safety: settled

Digging ended: after round 2, by the lead's judgment. The only remaining gap (retention period) sits behind a gated SOC 2 report and enterprise ZDR terms that no public page states. This is not one of the procedure's stop conditions.

## Verification
I checked 18 facts on 5 pages: models, MCA, privacy policy, SDK constants and Choice. All 18 were confirmed. None was NOT ON PAGE and none was UNCHECKED. The 40 req/s figure was not on the live models page, so it stays DISPUTED and is attributed only to the GitHub issue.

## Open rabbit holes
- **Retention period:** whether ZDR covers request inputs and what logging remains (dug, unsettled).
- **SOC 2 Type II 2026 report:** gated (undug).
- **OpenRouter `typesafe/jev-1.13` data policy:** the page returned 404 (dug, unsettled).
- **Nostoi `question_eval.py` source:** does it take labelled data (undug).
- **arXiv 2609.24052 calibration paper:** "Calibrated Decisions at Scale" (undug).
- **Independent Jev p50/p95 latency:** no measurement found (dug, unsettled).
- **PyPI `typesafe-ai` placeholder and the 0.7.0 vs 0.7.4 release history:** (undug).
- **aaddrick/building-with-typesafe-jev:** an unofficial builder skill (undug).
