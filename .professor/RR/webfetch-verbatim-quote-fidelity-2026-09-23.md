# RR — How faithful are LLM fetch-and-answer tools when asked to quote a page verbatim?

Question: How faithful are LLM "fetch a page and answer" tools (Claude Code WebFetch, OpenAI/Perplexity browsing, Jina Reader-style summarizers) when asked to quote a page verbatim — what failure modes (paraphrased or fabricated quotes, truncation, summarization-model limits) are documented or measured, and what mitigations work?

**Answer.** Not faithful enough to trust as verbatim. Claude Code's WebFetch and ChatGPT browsing are built not to return verbatim text: a small summarizer model sits between the page and your model, and quote caps are written into its prompt. Documented failures include silent truncation, fabricated content when the page is an error page or a PDF, stale training-data text spliced into live pages, and misattributed quotes. Independent audits put wrong-source rates at 37–94% across AI search engines. What works is to take the summarizer out of the path: fetch the raw text (the API web_fetch tool, curl, or the MCP fetch server's raw mode with paging), use citation APIs that return character-offset spans, and check every quote programmatically against the fetched text.

## Map

### 1. Claude Code WebFetch — how it is built to paraphrase
- **Pipeline.** HTML goes through Turndown to markdown, then is cut to 100 KB. The mikhail.io teardown says: "The result is truncated to 100 KB of text with a warning if necessary". A small model then answers the prompt against the page: "A small, fast model runs with an empty system prompt" ([mikhail.io](https://mikhail.io/2025/10/claude-code-web-tools/)). That model is Haiku: "A secondary model (Haiku) summarizes/extracts based on a prompt" ([trevorfox.com](https://trevorfox.com/2026/04/how-claude-code-search-actually-works/)).
- **Quote cap in the summarizer prompt.** Non-preapproved domains get "Enforce a strict 125-character maximum for quotes from any source document". "119 preapproved domains" instead get "Provide a concise response based on the content above. Include relevant details, code examples, and documentation excerpts as needed." ([trevorfox.com](https://trevorfox.com/2026/04/how-claude-code-search-actually-works/), corroborated by [mikhail.io](https://mikhail.io/2025/10/claude-code-web-tools/)). The prompt also says "any language outside of the quotation should never be word-for-word the same" ([mikhail.io](https://mikhail.io/2025/10/claude-code-web-tools/)). So paraphrase is the design, not an accident.
- **The answering model never sees the page.** "Your main model never sees the raw text—it only sees somebody else's notes on it" ([ai.plainenglish.io](https://ai.plainenglish.io/why-claude-codes-webfetch-might-be-quietly-lying-to-you-87a3a5f43230)). `unquoted`: the digger's fetch got a 403 and took this line from a search summary.

### 2. Documented failure modes, from bug reports
- **Silent truncation.** On RFC 9110 (a long IETF spec), the tool's own boundary note said it returned "39,415 of 502,907 characters — 7.8% of the document". The issue adds that "nothing in the result indicates that 92% was missing". As a result, "a fetch reporting 'the page doesn't mention X' is indistinguishable from the page genuinely not mentioning X" ([#95127](https://github.com/anthropics/claude-code/issues/95127)).
- **Fabrication on error pages.** A URL returned a bare 500 error, yet WebFetch returned "a full, detailed, internally-consistent job posting". This held even when the prompt said "Quote the raw text content of this page verbatim, character for character, exactly as it appears". The tool answered "This is a legitimate, live job listing—not an error page" and invented a salary of "$108,450–$173,550 USD" ([#87470](https://github.com/anthropics/claude-code/issues/87470)).
- **PDFs.** "the summarizer model cannot extract actual text content from the PDF. Instead, it hallucinates plausible-sounding answers". It returned the title "Claude 4 System Card" where the real title is "System Card: Claude Opus 4 & Claude Sonnet 4". PDFs over 10 MB fail with `maxContentLength size of 10485760 exceeded` ([#23694](https://github.com/anthropics/claude-code/issues/23694)).
- **Stale training data spliced into a live fetch.** "Roughly 80% of the returned content was correct and near-verbatim." The issue suggests the summarizer "reconciled the retrieved document against memorized priors about that specific URL". It re-inserted outdated plan figures "presented unhedged. No error, warning, or uncertainty was surfaced" ([#86581](https://github.com/anthropics/claude-code/issues/86581)).

### 3. ChatGPT, Perplexity and other AI search — measured
- **Quote caps in OpenAI's prompt.** A leaked GPT-5.4 prompt says: "You may not quote more than 25 words verbatim from any single non-lyrical source, unless the source is reddit." It also sets a per-source budget: "If omitted, the word limit is 200 words." ([system_prompts_leaks](https://github.com/asgeirtj/system_prompts_leaks/blob/main/OpenAI/gpt-5.4-thinking.md)). This is a leaked prompt, not an official document.
- **Tow Center 2024, ChatGPT Search.** Given 200 block quotes, "ChatGPT returned partially or entirely incorrect responses on a hundred and fifty-three occasions, though it only acknowledged an inability to accurately respond to a query seven times" ([CJR](https://www.cjr.org/tow_center/how-chatgpt-misrepresents-publisher-content.php)).
- **Tow Center 2025, eight engines.** "sixteen hundred queries (twenty publishers times ten articles times eight chatbots)". "Collectively, they provided incorrect answers to more than 60 percent of queries". Perplexity was best at "37 percent" wrong and Grok 3 worst at "94 percent". "ChatGPT, for instance, incorrectly identified 134 articles, but signaled a lack of confidence just fifteen times out of its two hundred responses" ([CJR](https://www.cjr.org/tow_center/we-compared-eight-ai-search-engines-theyre-all-bad-at-citing-news.php)).
- **BBC/EBU 2025, four assistants.** "45% of responses contained at least one significant issue". The study documents altered and invented quotes, for example "Quotes attributed to the Unite union and Birmingham City Council are not in the sources cited for them and appear to be made up" ([EBU report](https://www.ebu.ch/Report/MIS-BBC/NI_AI_2025.pdf); [Parsd](https://parsd.com/when-45-wrong-isnt-good-enough-lessons-from-the-ebu-bbc-ai-study/)). `unquoted`: the PDF could not be read, so these lines come from secondary coverage.
- **Perplexity.** Forbes reported, citing an AP report, that Perplexity Pages was "inventing fake quotes from real people" ([Forbes](https://www.forbes.com/sites/rashishrivastava/2024/06/11/the-prompt-perplexitys-plagiarism-problem/)). `unquoted`: not checked against the page.

### 4. Academic measurements
- **Generative search engines.** "a mere 51.5% of generated sentences are fully supported by citations and only 74.5% of citations support their associated sentence" ([Liu et al. 2023](https://arxiv.org/abs/2304.09848)).
- **ALCE.** "even the best models lack complete citation support 50% of the time" on ELI5, a long-form question-answering dataset ([Gao et al. 2023](https://arxiv.org/abs/2305.14627)).
- **Correct is not the same as faithful.** "current attributed answers often lack citation faithfulness (up to 57 percent of the citations)". The authors call this "post-rationalization": the model aligns with its prior beliefs and then cites a source for them ([Wallat et al. 2024](https://arxiv.org/abs/2412.18004)).
- **DeepTRACE audit.** "citation accuracy ranging from 40–80% across systems" ([arXiv 2509.04499](https://arxiv.org/abs/2509.04499)).
- **Summarizing long documents.** The FABLES dataset has 3,158 annotated claims from summaries of 26 books. Among the LLM auto-raters it tested for faithfulness, "none correlates strongly with human annotations" ([arXiv 2404.01261](https://arxiv.org/abs/2404.01261)).

### 5. Why quotes drift
- **Position in the context.** Performance "significantly degrades when models must access relevant information in the middle of long contexts" ([Lost in the Middle, TACL](https://aclanthology.org/2024.tacl-1.9/)).
- **Conflict between retrieved text and memory.** "LLMs are susceptible to adopting incorrect retrieved content, overriding their own correct prior knowledge over 60% of the time", across "over 1200 questions across six domains" ([ClashEval, NeurIPS 2024](https://proceedings.neurips.cc/paper_files/paper/2024/hash/3aa291abc426d7a29fb08418c1244177-Abstract-Datasets_and_Benchmarks_Track.html)). Issue #86581 shows the opposite direction: memory overriding the retrieved page.
- **Exact copying fails even with the source in hand.** When models transcribed 500 numbers, "the number of perfect runs is zero … with a mean match rate of 7.12%" ([arXiv 2601.03640](https://arxiv.org/html/2601.03640)).
- **Claude's own copyright rules.** Claude.ai's system prompt reportedly limits quotes to one per response, "fewer than 15 words" ([simonwillison.net](https://simonwillison.net/2025/May/25/claude-4-system-prompt/)). `unquoted`: this wording comes from a search summary.

### 6. Converters in the Jina Reader style
- **ReaderLM-v2.** ROUGE-L "0.84" on HTML-to-markdown conversion. Jina reports that the v1 model suffered "degeneration, particularly in the form of repetition and looping after generating long sequences" ([Jina](https://jina.ai/news/readerlm-v2-frontier-small-language-model-for-html-to-markdown-and-json/)). This means an LLM-based converter can corrupt text before any summarizer sees it.
- **Rule-based extractors also lose text.** Trafilatura 2.2.0 scores precision 0.906, recall 0.943 and F-score 0.924, against 0.826 for readability-lxml ([Trafilatura eval](https://trafilatura.readthedocs.io/en/latest/evaluation.html)). About 5% of main text is lost even by the best of them.
- **Exa** returns text "copied from the page" as highlights, and full text can be capped with `maxCharacters` ([Exa docs](https://exa.ai/docs/reference/contents-retrieval)).

### 7. Mitigations that work
- **Take the summarizer out of the path.**
  - The API web_fetch tool: "The API retrieves the full text content from the specified URL". Citations there are "optional for web fetch and disabled by default". When enabled, they return `cited_text` with `start_char_index`/`end_char_index`. Errors come back explicitly, e.g. `url_not_accessible`. Truncation is under the caller's control through `max_content_tokens` ([platform.claude.com](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool)).
  - The MCP fetch server: "by using the `start_index` argument … models read a webpage in chunks". `max_length` has "default: 5000". There is a `raw` option ([MCP fetch README](https://github.com/modelcontextprotocol/servers/blob/main/src/fetch/README.md)).
  - curl through Bash. Issue #95127 shows curl returning all 502,907 characters in one call.
- **Citation APIs.** Endex "reduced source hallucinations and formatting issues from 10% to 0%". Anthropic says its built-in citations "outperform most custom implementations, increasing recall accuracy by up to 15%" ([Anthropic](https://claude.com/blog/introducing-citations-api)). These are vendor claims with no published method.
- **Prompting.** "For tasks involving long documents (>20k tokens), ask Claude to extract word-for-word quotes first". "If it can't find a quote, it must retract the claim." Also "Explicitly give Claude permission to admit uncertainty" ([Anthropic docs](https://platform.claude.com/docs/en/test-and-evaluate/strengthen-guardrails/reduce-hallucinations)). "According to …" prompting improves QUIP-Score, a metric of how much of an answer appears verbatim in the source ([Weller et al.](https://arxiv.org/abs/2305.13252)). Quote-Tuning "increases verbatim quotes … by up to 130% relative to base models" ([Zhang et al.](https://arxiv.org/abs/2404.03862)).
- **Check quotes mechanically.** The strongest guard in these sources is a string match of each quote against raw fetched text, backed by character offsets (the Citations API design above). A quote from WebFetch should count as unverified until it matches. Note that #87470 shows a stricter prompt alone does not stop fabrication.

## Coverage
1. Claude Code WebFetch architecture: **settled**
2. Documented failure modes (bug reports): **settled**
3. ChatGPT/Perplexity/AI search measurements: **settled** (BBC/EBU scope figures are partial)
4. Academic measurements: **settled**
5. Why quotes drift (root causes): **partial**
6. Jina Reader-style converters: **partial**
7. Mitigations: **settled** (independent measurements of the mitigations are partial)

## Verification
- **Scope.** I checked 35 statements against 12 pages. 33 are confirmed.
- **NOT ON PAGE:**
  - That the Citations API *guarantees* cited text is extracted from the source. The blog says only that it minimizes hallucinations. The API docs show the `cited_text` and offset fields but make no guarantee claim. This claim was removed.
  - The MCP fetch `max_length` default of 50000 from the digger's report. The page says 5000, and that value is used above.
  - ClashEval's "1,294 questions". The page says "over 1200", used above.
- **UNCHECKED:** none. The facts marked `unquoted` above were not sampled in this pass.

## Rabbit holes left open
- No benchmark compares verbatim-quote fidelity across tools (Claude Code WebFetch, API web_fetch, ChatGPT, Perplexity, Jina) against the true page text. No source found fills this gap.
- The full BBC/EBU report PDF could not be read, so its scope and sourcing figures are unconfirmed.
- The exact wording of Claude.ai's "15 words" quote clause was not confirmed against a primary source.
- No exact figures were found for the Lost in the Middle position curve, Bevendorff 2023's extractor comparison, or ReaderLM-v2's Token Error Rate.
- #90416, #59882 and #51783: other Claude Code issues cited inside #95127. No digger found their pages.
- The fidelity of API web_fetch's code-based dynamic filtering is not documented.
- Whether Perplexity, ChatGPT and others fabricate content for 500-error pages the way #87470 shows for WebFetch.
