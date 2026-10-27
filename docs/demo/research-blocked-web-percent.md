# What percentage of the web is blocked to bots and AI crawlers? (research, 2026-09-14)

## Contents

- [(a) Every quantified figure, 2023–2026](#a-every-quantified-figure-20232026)
  - [A. robots.txt disallow — general / top-N web](#a-robotstxt-disallow--general--top-n-web)
  - [B. robots.txt disallow — news publishers](#b-robotstxt-disallow--news-publishers)
  - [C. Training-corpus token share (the only whole-corpus population)](#c-training-corpus-token-share-the-only-whole-corpus-population)
  - [D. Cloudflare-managed blocking and footprint](#d-cloudflare-managed-blocking-and-footprint)
  - [E. On-the-wire enforcement (what a fetcher actually gets)](#e-on-the-wire-enforcement-what-a-fetcher-actually-gets)
  - [F. Bot share of traffic (NOT a blocking measure)](#f-bot-share-of-traffic-not-a-blocking-measure)
  - [G. Adjacent "closed to a plain fetcher" populations](#g-adjacent-closed-to-a-plain-fetcher-populations)
- [(b) Which figures can carry "X% of the internet is blocked for bots"](#b-which-figures-can-carry-x-of-the-internet-is-blocked-for-bots)
- [(c) The defensible claims](#c-the-defensible-claims)
- [Gaps](#gaps)

**Answer in two sentences.** No measured figure supports "X% of the internet is blocked for bots" as a whole-web claim; the closest honest numbers are that **about 1 in 10 of the world's top 10,000 sites tells AI crawlers to stay out in robots.txt** (10.3%, HasData, July 2026; 8.92% under Cloudflare's managed block in June 2024), while **more than half of news publishers do** (56.4% robots.txt; only 54.2% will even serve a page to a bot that announces itself as GPTBot, versus 83.8% for a browser from the same IP). Whole-web "bot" percentages that circulate (53% of traffic is bots, 52.7% on Cloudflare) measure who is *sending* traffic, not who is *blocked*, and cannot be used for the stage line.

Method: 1 lead search + 5 Haiku diggers (round 1) + 2 verification diggers (round 2); three sources re-read from disk by the lead (HasData, Calvano, Consent in Crisis abstract). 7 dispatched / 7 received. Harvester `search` backend was down (502) for the lead's map step; WebSearch was the fallback.

## (a) Every quantified figure, 2023–2026

Sources marked **[verified]** were read from the fetched artifact by the lead; the rest are relayed from a digger that fetched them.

### A. robots.txt disallow — general / top-N web

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| **10.3%** block ≥1 AI crawler in robots.txt; **7.9%** block GPTBot | Tranco top 10,000 (9,746 domains) | Jul 2026 | [HasData AI Crawler Block Index](https://hasdata.com/blog/ai-crawler-block-index) [verified] |
| **15%** block ≥1 AI crawler | whole 10,894-domain sample (top-10k + 1,148 publishers) | Jul 2026 | same [verified] |
| **19.1%** block ≥1 AI crawler; GPTBot 16.6%, ClaudeBot 15.8%, Google-Extended 14.8%, PerplexityBot 10.8%; a further **8.9%** block at the edge/CDN with no robots.txt rule | 1,530 reachable of Tranco top 2,000 | 2 Aug 2026 | [dev.to / Mira Voss](https://dev.to/miravoss/19-of-the-top-2000-sites-block-ai-crawlers-and-136-more-block-them-without-knowing-59n5) |
| **~21%** have GPTBot rules in robots.txt (allow or disallow) | top 1,000 sites, HTTP Archive; 94% of 12M sites have any robots.txt | Jul 2025 | [Paul Calvano](https://paulcalvano.com/2025-08-21-ai-bots-and-robots-txt/) [verified] |
| **7.8%** disallow GPTBot; 5.6% Google-Extended; <5% each ClaudeBot/PerplexityBot/Bytespider; only 37% of these domains even have a robots.txt | top 10,000 domains on Cloudflare Radar | 1 Jul 2025 | [Cloudflare blog](https://blog.cloudflare.com/control-content-use-for-ai-training/) |
| **5.52%** GPTBot, 5.08% CCBot, 4.88% ClaudeBot, 4.44% Google-Extended, 4.23% Bytespider DISALLOW | 4,047 robots.txt files on Cloudflare's network | Q1 2026 (30 Mar) | [TechnologyChecker.io](https://technologychecker.io/blog/robots-txt-ai-crawlers-blocking-report) |
| **22.6%** block ≥1 AI crawler | top 10,000 | 2026 | [Chris Humphrey](https://chrishumphrey.ai/research/ai-crawler-blocking-2026) — surfaced by lead's WebSearch only, **not fetched**; unverified |

### B. robots.txt disallow — news publishers

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| **56.4%** block ≥1 AI crawler; **50.5%** block GPTBot | 1,148 news publishers (Palewire set) | Jul 2026 | [HasData](https://hasdata.com/blog/ai-crawler-block-index) [verified] |
| **49.7%** block OpenAI; 44.5% Google AI; 49.8% Common Crawl | 1,156 news homepages | Sep 2026 (live tracker) | [Palewire](https://palewi.re/docs/news-homepages/openai-gptbot-robotstxt.html) |
| GPTBot **62%**, ClaudeBot 69%, CCBot 75%, PerplexityBot 67%, Google-Extended 46%; 79% block ≥1 training bot, 71% ≥1 retrieval bot | 100 top news sites (50 US + 50 UK) | 2025 data, pub. Apr 2026 | [BuzzStream](https://www.buzzstream.com/blog/publishers-block-ai-study/) |
| **48%** block OpenAI, 24% block Google AI; US 79%, Mexico/Poland 20% | 150 news sites, 10 countries | Dec 2023 | [Reuters Institute](https://reutersinstitute.politics.ox.ac.uk/how-many-news-websites-block-ai-crawlers) |

### C. Training-corpus token share (the only whole-corpus population)

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| **~5%+ of all C4 tokens** fully restricted by robots.txt; **28%+** of the most actively maintained, critical sources; **45%** of C4 under Terms-of-Service crawl restrictions | 14,000 web domains underlying C4 / RefinedWeb / Dolma | 2023–Jul 2024 | [Consent in Crisis, arXiv 2407.14933](https://arxiv.org/abs/2407.14933) [verified] |

### D. Cloudflare-managed blocking and footprint

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| **~20%** of websites are behind Cloudflare; **>1 million** customers opted to block AI crawlers; AI-crawler block is **default for new domains** since 1 Jul 2025; pay-per-crawl (HTTP 402) launched | Cloudflare network | 1 Jul 2025 | [Cloudflare press](https://www.cloudflare.com/press/press-releases/2025/cloudflare-just-changed-how-ai-crawlers-scrape-the-internet-at-large/), [Content Independence Day](https://blog.cloudflare.com/content-independence-day-no-ai-crawl-without-compensation/) |
| **40%** of top-10, **8.92%** of top-10k, **2.98%** of top-1M sites use the one-click AI block | Cloudflare-ranked sites | Jun 2024 | [Declare your AIndependence](https://blog.cloudflare.com/declaring-your-aindependence-block-ai-bots-scrapers-and-crawlers-with-a-single-click/) |
| **26.0%** of top-10k and 22.4% of publishers sit behind Cloudflare's proxy; the Sept 15 2026 default block (Cloudflare + ad-tech) covers **8.5%** of the top web / 13.6% of publishers | Tranco top 10k + publishers | Jul 2026 | [HasData](https://hasdata.com/blog/ai-crawler-block-index) [verified] |

### E. On-the-wire enforcement (what a fetcher actually gets)

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| Open web: GPTBot UA served 200 **67.8%** vs browser UA 72.3%; publishers: **54.2%** vs 83.8% (same datacenter IP) | 2,096-domain subset (top-1,000 open web + all publishers), ~15% proxy errors excluded | Jul 2026 | [HasData](https://hasdata.com/blog/ai-crawler-block-index) [verified] |
| Among the 592 sites that disallow GPTBot: 47.1% still served 200 (**39.5%** "paper-only bans"), **22.2%** hard block (403/401/451), 10.9% HTTP 402, 5.0% JS challenge, 2.4% 429, 2.3% CAPTCHA; **8.2%** of all enforcement measurements were a Turnstile/JS interstitial; 115 sites (5.5%) block at CDN with silent robots.txt | same subset | Jul 2026 | same [verified] |
| By CDN, GPTBot served OK: Cloudflare 51.3% (16.9% hard-block, 24.7% challenge), Fastly 27.3% (61.5% hard-block), CloudFront 78.5% | same subset | Jul 2026 | same [verified] |
| **8.56%** of AI-bot requests got 403 (Q2 2026) vs 3.63% (Q2 2025); AI-bot 4xx 12.85% vs 7.53%; all-crawler 4xx **35.79%** (Jul 2026) vs 14.04% (Jul 2025) | requests on Cloudflare's network | Sep 2026 update | [TechnologyChecker.io](https://technologychecker.io/blog/robots-txt-ai-crawlers-blocking-report) |
| GPTBot 34.82% and Claude 34.16% of fetches hit **404** (Googlebot 8.22%) — broken URLs, not blocks | 569M / 370M / 4.5B requests on Vercel's network | Dec 2024 | [Vercel](https://vercel.com/blog/the-rise-of-the-ai-crawler) |
| **71%** robots.txt compliance among 7 data crawlers (Bytespider non-compliant); 5.7% of top-10k Cloudflare sites had the AI block on; 14% of top-10k actively block | academic crawl, 2024 | IMC 2025 | [arXiv 2411.15091](https://arxiv.org/abs/2411.15091) |

### F. Bot share of traffic (NOT a blocking measure)

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| Bots **52.7%** of HTML requests (AI bots 4.2% ex-Googlebot, Googlebot 4.5%, other bots 44%), humans 47% | Cloudflare Radar | 2 Dec 2025 | [Radar 2025 Year in Review](https://blog.cloudflare.com/radar-2025-year-in-review/) |
| Bots **53%** of traffic, bad bots 40% (2025); 51% / 37% (2024) | Imperva/Thales customer network | Apr 2026 / Apr 2025 | [Imperva 2026](https://www.imperva.com/blog/bad-bot-report-2026-bots-agentic-age/), [2025](https://www.imperva.com/resources/resource-library/reports/2025-bad-bot-report/) |
| 52% of crawler requests are for AI training (22% in spring 2025) | Cloudflare network | Jun 2026 | [Agentic Internet Bot Report](https://blog.cloudflare.com/agentic-internet-bot-report/) |
| AI bots ~1% of bot traffic, +300% YoY; 47.9% of AI-bot activity hits commerce | Akamai | Nov 2025 / Jul 2026 | [Akamai](https://www.akamai.com/newsroom/press-release/akamai-research-ai-bots-threaten-foundation-of-web-based-business-models/) |
| ClaudeBot crawl:refer 70,900:1 (Jun 2025) / 50,000:1; GPTBot 887:1; Perplexity 118:1; Googlebot 5–19:1 | Cloudflare Radar | 2025 | [Cloudflare](https://blog.cloudflare.com/ai-search-crawl-refer-ratio-on-radar/), [by purpose](https://blog.cloudflare.com/ai-crawler-traffic-by-purpose-and-industry/) |

### G. Adjacent "closed to a plain fetcher" populations

| Figure | Population | Date | Source |
| --- | --- | --- | --- |
| **50%** of scholarly articles closed/paywalled, 50% accessible in some form | 66M journal articles + conference papers, 2010–2024 | COKI dashboard, Aug 2026 | [open.coki.ac](https://open.coki.ac/) |
| 27.9% OA overall; 45% of 2015 articles OA | 66.56M Crossref-DOI articles | 2017 | [Piwowar et al. 2018](https://doi.org/10.7717/peerj.4375) |
| Median page's rendered content exceeds raw HTML by **13.6%** (mobile) / 17.5% (desktop); 94.5% of pages served dynamically | HTTP Archive (~16M sites) | Nov 2024 | [Web Almanac SEO](https://almanac.httparchive.org/en/2024/seo), [Jamstack](https://almanac.httparchive.org/en/2024/jamstack) |
| 42% of JS-rendered content never indexed; 83% of React SPAs have critical content invisible without JS | 6,000 sites (method unstated) | Feb 2026 | [Onely](https://www.onely.com/blog/seo-tips-for-dynamic-content-and-dynamic-sites/) — weakly sourced |
| Googlebot rendered 100% of 37,000+ JS pages | Vercel/MERJ, Next.js-heavy sites | Apr 2024 | [Vercel](https://vercel.com/blog/how-google-handles-javascript-throughout-the-indexing-process) |

Dropped as unverified: a round-1 digger reported "10.6% of the top 1 million block GPTBot / 9.1% ClaudeBot / 9.5% CCBot" attributed to Calvano; the lead read the article and none of those numbers appear in it. Also relayed but not verifiable here: Rutgers/Wharton "publishers blocking AI lost 23.1% of visits" (digger saw it only in a search snippet).

## (b) Which figures can carry "X% of the internet is blocked for bots"

**Can support a line of that shape (with the population named):**

- **10.3% of the top 10,000 sites** block at least one AI crawler in robots.txt (HasData, Jul 2026) — corroborated by Cloudflare's own 8.92% (managed block, top-10k, 2024) and 7.8% GPTBot (robots.txt, top-10k, Jul 2025), and by 19.1% on the narrower top-2,000. Honest form: "about one in ten of the top 10,000 sites".
- **56.4% of news publishers** (HasData) / 49.7% (Palewire, live) / 62–79% of top US-UK news sites (BuzzStream). Honest form: "more than half of news publishers".
- **~20% of websites are behind Cloudflare, which blocks AI crawlers by default for new domains since July 2025** (Cloudflare's own statements). Honest form: a fifth of the web now defaults to closed.
- **On the wire, an honest AI bot is served 54% of the time by publishers vs 84% for a browser** (HasData) — the strongest "blocked for bots specifically" number, because it is a paired-request measurement.
- **~5% of C4 tokens / 28% of its critical sources / 45% under ToS** (Consent in Crisis) — the only whole-corpus population, but it is 2024 data and "training corpus", not "the internet".

**Cannot support it:**

- **53% / 52.7% bot traffic** (Imperva, Cloudflare Radar): share of *requests made by bots*, says nothing about blocking. Using it as "half the web is blocked" is a category error.
- **22.2% hard block** (HasData): denominator is the 592 sites that *already* disallow GPTBot — a share of a share. Same for 39.5%, 5.0%, 2.3%.
- **8.56% of AI-bot requests get 403** (TechnologyChecker): a request-share on Cloudflare's network, not a site-share; and 4xx includes 404s (Vercel shows ~34% of AI-crawler fetches are simply bad URLs).
- **robots.txt disallow ≠ hard block**: 39.5% of sites that disallow GPTBot still serve it (HasData); Bytespider ignores robots.txt (IMC 2025). A robots.txt figure is "asked to stay out", not "blocked".
- **Top-N vs whole web**: every site-share figure is top-2k to top-10k; Cloudflare's only top-1M number is 2.98% (2024, managed rule only). Nothing measures the long tail's blocking rate.
- **Cloudflare's "20% of websites"** is a footprint, not a block rate; HasData finds only 26% of the top-10k behind Cloudflare and only 8.5% in scope of the Sept-2026 default block.
- **50% paywalled scholarship, 13.6% JS content gap**: adjacent populations ("closed to a plain fetcher"), not "blocked for bots".

## (c) The defensible claims

**Most defensible (one sentence, one citation):** "About one in ten of the world's top 10,000 websites now tells AI crawlers to stay out in robots.txt — and among news publishers it is more than half." — HasData AI Crawler Block Index, July 2026 (10.3% and 56.4%), <https://hasdata.com/blog/ai-crawler-block-index>. Corroborated by Cloudflare's own top-10k figures (8.92% in 2024, 7.8% GPTBot in 2025).

**Punchier, still honest:** "Announce yourself as an AI bot and nearly half of news sites shut the door: from the same IP, publishers served a browser 84% of the time and GPTBot 54%." — HasData, July 2026, paired-request measurement, same URL.

**Also usable:** "A fifth of the web sits behind Cloudflare, and since July 2025 Cloudflare blocks AI crawlers by default." — Cloudflare, 1 Jul 2025, <https://blog.cloudflare.com/content-independence-day-no-ai-crawl-without-compensation/> and the press release above.

**Do not say:** "X% of the internet is blocked for bots" with any X — no measurement of that population exists in 2023–2026. "Half the web's traffic is bots" is true (Imperva 53%, Cloudflare 52.7%) but is a different sentence.

## Gaps

- **Harvester `search` failed (502 / "Retrieval failed")** for the lead's map query; WebSearch substituted. Diggers used WebFetch for most pages, so only three sources were re-read from disk.
- **Chris Humphrey "22.6% of top 10,000"** surfaced in the lead's search but no digger fetched it — unverified, listed only for completeness.
- **Cloudflare Radar AI Insights page** (<https://radar.cloudflare.com/ai-insights>) returned 403 to the digger; the live robots.txt disallow share for 2026 is unread. Cloudflare's "37% of top-10k domains have a robots.txt" (relayed) sits in tension with Calvano's 94% of 12M sites having one; not reconciled.
- **No whole-web (long-tail) blocking rate exists** in any source found; the nearest is Cloudflare's 2.98% of top-1M (2024, managed rule only).
- **Calvano per-bot percentages** live in charts the fetch did not render; only the "almost 21% of top 1,000 have GPTBot rules" sentence is verifiable, and it counts allow+disallow rules.
- **Rutgers/Wharton 23.1% traffic-loss figure** was seen only in a search snippet, never fetched.
- **JS-dependence**: no study measures what share of pages *require* JS for main content; the 13.6%/17.5% HTTP Archive content gap is a median-page delta, and the Onely 42%/83% figures lack a stated method.
- **Consent in Crisis** figures are 2023–2024; no 2025–2026 update of the token-share audit was found.
