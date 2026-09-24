# RR — How faithful are LLM fetch-and-answer tools at verbatim quoting, what fails, what works?

Question: How faithful are LLM "fetch a page and answer" tools (Claude Code WebFetch, OpenAI/Perplexity browsing, Jina Reader-style summarizers) when asked to quote a page verbatim — what failure modes (paraphrased or fabricated quotes, truncation, summarization-model limits) are documented or measured, and what mitigations work?

**Answer.** Not faithful enough to trust without checking. No benchmark measures verbatim-quote fidelity for these fetch tools specifically, but neighbouring evidence is consistent:
- Generative search engines fully support only about half of their sentences.
- BBC testing found 13% of attributed quotes altered or absent from the source.
- Claude Code's WebFetch has three structural problems: it answers through a small model under a 125-character quote cap, it silently truncates long pages, and bug reports show it inventing content when a fetch fails.

What works is taking quote text out of the model's hands. Either the model cites spans that are looked up deterministically (Deterministic Quoting, Anthropic Citations or char-indexed web_fetch citations), or every quote is string-matched against raw page text.

## Map

### 1. How the tools work (and why that limits verbatim quoting)
- **Claude Code WebFetch** is a summarizer, not a reader. Per a reverse-engineering write-up:
  - "HTML is converted to Markdown using the Turndown library."
  - "The result is truncated to 100 KB of text with a warning if necessary."
  - "the tool never returns raw HTML or markdown content but answers a question about it."
  - The small model is labelled "Haiku 3.5".
  - The small model's instructions include "Enforce a strict 125-character maximum for quotes from any source document."
  - Source: [mikhail.io](https://mikhail.io/2025/10/claude-code-web-tools/). A verbatim request of more than 125 characters therefore conflicts with the tool's own instructions.
- **Claude's consumer system prompt** (leaked) sets a similar copyright cap: "Include only a maximum of ONE very short quote from original sources per response, where that quote (if present) MUST be fewer than 15 words long" ([Chatoptic](https://www.chatoptic.com/blog/claude-4-system-prompt-leak)). A Washington Post analysis reportedly says the same, but it returned a 403 and was never read (`unquoted`).
- **Anthropic API server-side web_fetch** works the opposite way:
  - "The API retrieves the full text content from the specified URL."
  - "If the fetched content exceeds this limit, the tool truncates it" (`max_content_tokens`).
  - "citations are optional for web fetch and disabled by default"; when enabled they carry `start_char_index`, `end_char_index` and `cited_text`.
  - It "currently does not support websites dynamically rendered with JavaScript."
  - Source: [Anthropic docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool).
- **OpenAI.** The web search guide gives a 128k search context: "For Responses API web search, the search context window is limited to 128k, even when the model context window is larger" ([OpenAI](https://developers.openai.com/api/docs/guides/tools-web-search)). The Model Spec states a principle but no quote length: "The assistant must respect creators, their work, and their intellectual property rights" ([Model Spec](https://model-spec.openai.com/2025-12-18.html)). A community thread claims a 90-word limit; it is anecdotal (`unquoted`, unverified).
- **Jina Reader** does conversion, not summarization. It works "by extracting the core content from a URL and converting it into clean, LLM-friendly text", with "Boilerplate such as navigation, headers, footers, and ads is stripped". It offers headless-browser rendering for JavaScript pages, and a token budget where "Exceeding this limit will cause the request to fail" ([jina.ai/reader](https://jina.ai/reader/)).
- **Perplexity Sonar/Agent API** returns `search_results` with `snippet` fields, sometimes empty, plus an optional `fetch_url` tool. That suggests the model often sees snippets, not full pages (`unquoted`, from [docs.perplexity.ai](https://docs.perplexity.ai/getting-started/quickstart)).

### 2. Measured faithfulness
- **Generative search engines (2023).** Bing Chat, NeevaAI, perplexity.ai and YouChat: "a mere 51.5% of generated sentences are fully supported by citations and only 74.5% of citations support their associated sentence" ([Liu et al., arXiv 2304.09848](https://arxiv.org/abs/2304.09848)).
- **Quote alteration (BBC, Feb 2025).** "13% of the quotes sourced from BBC articles were altered from the original source or were not there at all." Also: "51% of all AI answers to questions about the news had significant issues in some form" and "19% of AI answers that cited BBC content introduced factual errors" ([MediaPost](https://www.mediapost.com/publications/article/403342/the-bbc-skewers-ai-test-found-51-of-content-answ.html)). The same 51% figure appears at [Digital Content Next](https://digitalcontentnext.org/blog/2025/02/24/ai-assistants-error-prone-when-it-comes-to-news/); both report one BBC origin. BBC's own page could not be fetched.
- **BBC/EBU (Oct 2025).** "45% contained at least one significant issue"; "31% of all responses had significant problems with how they attributed information" ([Winbuzzer](https://winbuzzer.com/2025/10/22/ai-assistants-get-news-wrong-in-45-of-cases-landmark-bbc-ebu-study-finds-xcxwbn/), secondary).
- **Tow Center / CJR (Mar 2025).** Given verbatim excerpts, the 8 AI search engines "provided incorrect answers to more than 60 percent of queries."
  - "Perplexity answering 37 percent of the queries incorrectly".
  - Grok 3 answered "94 percent of the queries incorrectly".
  - "Out of the 200 prompts we tested for Grok 3, 154 citations led to error pages."
  - Source: [CJR](https://www.cjr.org/tow_center/we-compared-eight-ai-search-engines-theyre-all-bad-at-citing-news.php).
- **Deep-research agents (2026).** "Even the strongest frontier models maintain link validity above 94% and relevance above 80%, yet achieve only 39–77% factual accuracy." Accuracy drops as the number of tool calls grows, "from 79% to 17%" for GPT-5.4 ([arXiv 2605.06635](https://arxiv.org/html/2605.06635v1)). Separately, "3--13% of citation URLs are hallucinated" ([arXiv 2604.03173](https://arxiv.org/abs/2604.03173)).
- **Verbatim copying degrades with length.** "The mean match rate (averaged over all models) falls from 63.46% at N=100 to 15.65% at N=300 and 7.12% at N=500" ([arXiv 2601.03640](https://arxiv.org/html/2601.03640v1)). This test copies constants into code, not quotes from a web page.

### 3. Failure modes, with documented instances
- **Silent truncation.** On RFC 9110, WebFetch "reported returning 39,415 of 502,907 characters — 7.8% of the document." Also: "Nothing in the tool result carries that forward as a flag, so the calling model sees an answer about 7.8% of a document presented exactly like an answer about all of it" ([claude-code #95127](https://github.com/anthropics/claude-code/issues/95127)). The practical effect is that "not on the page" cannot be told apart from "not in the part I was given".
- **Fabrication despite explicit verbatim instructions.** The target was a "500 | Internal Server Error." page. Asked to "Quote the raw text content of this page verbatim, character for character", WebFetch still returned a full job posting: "Tool still fabricates/returns the full job posting" ([claude-code #87470](https://github.com/anthropics/claude-code/issues/87470)).
- **Fabrication on fetch failure.** "When WebFetch fails to access a URL (e.g., Reddit blocks the request), Claude Code fabricates an answer using search results and presents it as if it read the original content" ([claude-code #45070](https://github.com/anthropics/claude-code/issues/45070)). This and #87470 are independent reports of the same pattern.
- **Content lost before the model sees it.** HTML extraction is itself lossy. Trafilatura 2.2.0 scores "0.906 Precision" / "0.943 Recall", against readability-lxml at "0.826" F-score ([trafilatura benchmark](https://trafilatura.readthedocs.io/en/latest/evaluation.html)). JavaScript-rendered pages are unsupported by the API web_fetch tool (see section 1).
- **Long-context position effects.** Performance "significantly degrades when models must access relevant information in the middle" ([Liu et al. 2023, arXiv 2307.03172](https://arxiv.org/abs/2307.03172)).
- **Copyright caps.** Quote caps of 125 characters or 15 words (section 1) push models toward paraphrase even when the user asks for verbatim text.

### 4. Mitigations that work
- **Deterministic quoting: the model picks, code copies.**
  - Mechanism: "The actual quote text is discarded because it may contain hallucinations. The application looks up the unique reference string in the chunk index."
  - Result: 0 hallucinations inside the quote box against 12% in baseline RAG prose, on N=60 with a non-public dataset.
  - Limit: "The LLM can still choose an irrelevant (but still verbatim) quote."
  - Source: [Deterministic Quoting](https://mattyyeung.github.io/deterministic-quoting).
- **Anthropic Citations API and web_fetch citations.**
  - Source documents are chunked "into sentences".
  - Customer report from Endex: "we reduced source hallucinations and formatting issues from 10% to 0%".
  - Anthropic reports "increasing recall accuracy by up to 15%".
  - Source: [Anthropic](https://claude.com/blog/introducing-citations-api). The same char-indexed `cited_text` mechanism is available on web_fetch (section 1).
- **Verified-quote training and abstention (GopherCite).** Answers "found to be high-quality 80% of the time on a Natural Questions subset, and 67% of the time on the ELI5 subset", rising to 90% on NQ when the model abstains on its least-certain third ([arXiv 2203.11147](https://arxiv.org/abs/2203.11147)).
- **Prompting.** Useful, but it has not been measured.
  - "ask Claude to extract word-for-word quotes first before performing its task"
  - "If it can't find a quote, it must retract the claim"
  - "they don't eliminate them entirely"
  - Source: [Anthropic docs](https://platform.claude.com/docs/en/test-and-evaluate/strengthen-guardrails/reduce-hallucinations).
  - Issue #87470 shows that prompting alone did not stop fabrication inside WebFetch.
- **Architecture.** Fetch raw text instead of a summarizer's answer, then string-match each quote. Candidates are curl/Jina markdown, or API web_fetch with citations. Match exactly, or fuzzily at a high threshold, and surface truncation explicitly (the fix proposed in #95127). A 90% fuzzy threshold was reported in search synthesis only (`unquoted`). No head-to-head evaluation of raw-fetch against summarizer-fetch was found.

## Coverage
- 1. Tool mechanics: **settled** for Claude Code WebFetch, API web_fetch and Jina Reader. **Partial** for OpenAI (no official quote cap found) and Perplexity (unquoted).
- 2. Measured faithfulness: **partial**. Strong citation and attribution studies exist, but no benchmark measures verbatim-quote fidelity for fetch tools specifically.
- 3. Failure modes: **settled**.
- 4. Mitigations: **settled** for deterministic lookup, citations, abstention and prompting. **Partial** for a controlled raw-versus-summarizer comparison.

Diggers: 6 dispatched, 6 reports received.

## Verification
- 20 facts checked on 8 source pages. All 20 were confirmed:
  - #95127: 2
  - #87470: 2
  - #45070: 1
  - mikhail.io: 5
  - CJR: 4
  - arXiv 2304.09848: 3
  - Anthropic web_fetch docs: 3
  - MediaPost: 3
- 0 `NOT ON PAGE`. 0 `UNCHECKED`.
- The BBC's own primary page was unreachable (fetches blocked), so the 13% figure rests on MediaPost's report of it.

## Rabbit holes left open
- **A verbatim-quote fidelity benchmark for production fetch tools** (WebFetch, ChatGPT search, Perplexity, Jina+LLM): no source found. This is the biggest gap.
- **How often the WebFetch summarizer paraphrases** versus honouring the 125-character cap, and whether some documentation domains bypass the summarizer. Observed during verification, unverified: a fetch of platform.claude.com returned raw page markdown rather than an answer.
- **The full BBC report PDF** (the primary for the 13% figure), and a per-assistant breakdown of altered quotes.
- **Perplexity Sonar docs** on snippet versus full-page reading; **an official OpenAI quote-length rule**, if one exists.
- **How often Deterministic Quoting fails at the reference-ID level** (the model inventing IDs).
- **The detection rates of FActScore, ALCE and AttributionBench** against fabricated quotes.
- **Anthropic web_fetch dynamic filtering** (`web_fetch_20260209`+): does code-based filtering add its own fidelity loss?
- **Prompt injection through fetched pages** into the summarizer step.
