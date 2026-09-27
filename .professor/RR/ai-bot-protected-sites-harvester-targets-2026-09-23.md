# RR — Most aggressively AI-bot-protected public sites (2026) and a harvester test-target list

Question: map the public websites most aggressively protected against AI crawlers, bots and LLM fetchers as of 2026, and return a test-target list for our web harvester (direct HTTP, Chrome TLS impersonation, reader services like jina, Wayback, headless-then-headed real Chrome) — protection layers and who runs them, named sites per layer, evidence of AI-specific blocking, a ranked 30–40 URL table, open rabbit holes.

**Answer.** The hardest targets fall into three groups. Commerce, ticketing and UGC sites run commercial bot managers that fingerprint TLS, HTTP/2 and JS, like DataDome (Reddit, Tripadvisor), PerimeterX/HUMAN (Zillow), Kasada (Ticketmaster) and Akamai. News and academic publishers pair a restrictive robots.txt with Cloudflare or Fastly edge blocking. Open-source forges now run Anubis proof-of-work, which a real browser passes but plain HTTP, TLS impersonation and reader services do not. Vendor-to-site links are mostly **unquoted or secondary**. Only a few were confirmed on a first-party page: Wiley and Cloudflare, Stack Overflow and Cloudflare pay-per-crawl, Instacart and AWS WAF, the Anubis deployments, and Bloomberg's robots.txt.

## Map

### A. Cloudflare layers
- **Default AI block (July 2025).** Cloudflare says it became "the first Internet infrastructure provider to block AI crawlers ... by default", and pay-per-crawl launched alongside it. Sources: [press release](https://www.cloudflare.com/press/press-releases/2025/cloudflare-just-changed-how-ai-crawlers-scrape-the-internet-at-large/), [MIT Tech Review](https://www.technologyreview.com/2025/07/01/1119498/cloudflare-will-now-by-default-block-ai-bots-from-crawling-its-clients-websites/).
- **Sept 15, 2026 defaults.** "For all new domains onboarding to Cloudflare, the categories of Training and Agent will be blocked by default on the pages that display ads, while Search will remain allowed by default." Source: [Cloudflare blog](https://blog.cloudflare.com/content-independence-day-ai-options/). An agent-class fetcher like ours falls under "Agent".
- **Pay-per-crawl.** The site answers HTTP 402 with price details, and a paid follow-up request gets a 200. Across a broad crawl, "10.9% return HTTP 402" ([HasData](https://hasdata.com/blog/ai-crawler-block-index)). The 402 mechanics are *unquoted* from Cloudflare itself. Named partner: Stack Overflow ([SO blog](https://stackoverflow.blog/2026/02/19/stack-overflow-cloudflare-pay-per-crawl/)). The Condé Nast, TIME and StubHub partners are *unsourced*.
- **Turnstile and JS challenge.** "Cloudflare serves a Turnstile JavaScript challenge that only a browser client can execute", on 24.7% of GPTBot hits in HasData's sample ([HasData](https://hasdata.com/blog/ai-crawler-block-index)). Cloudflare also exposes JA3/JA4 TLS fingerprints to its rules ([CF docs](https://developers.cloudflare.com/bots/additional-configurations/ja3-ja4-fingerprint/)). The bot-score composition (TLS + HTTP/2 + JS + behaviour, scored 0–99) is *unquoted*, from a third party.
- **AI Labyrinth.** "When these links are followed, we know with high confidence that it's automated crawler activity, as human visitors and legitimate browsers would never see or click them." Also: "AI Crawlers generate more than 50 billion requests to the Cloudflare network every day" ([CF blog](https://blog.cloudflare.com/ai-labyrinth/)). *Confirmed.* The failure mode for us: a raw-HTML link follower walks into decoy pages.
- **Bot Fight Mode.** No specific source was gathered. *Open.*

### B. Other edge and bot-management vendors
- **Fastly.** "Fastly returns a 403 the moment GPTBot connects" (61.5% hard-block) ([HasData](https://hasdata.com/blog/ai-crawler-block-index)). Its bot signals label suspected and verified bots ([Fastly docs](https://www.fastly.com/documentation/guides/security/bot-management/about-bot-management/)). No named customer was found.
- **Akamai Bot Manager.** JA4 "is a client-focused fingerprint based on the client TLS handshake", used to "detect and mitigate bots" ([Akamai TechDocs](https://techdocs.akamai.com/application-security/reference/get-ja4-fingerprint-settings)). It splits between blocking and rate-limiting: 34.5% blocked and 19.5% rate-limited ([HasData](https://hasdata.com/blog/ai-crawler-block-index)). The JS-sensor and behaviour layers are *unquoted*.
- **AWS WAF Bot Control.** Its Targeted level uses "browser interrogation, fingerprinting, and behavior heuristics", and high-confidence bots are blocked by default ([AWS docs](https://docs.aws.amazon.com/waf/latest/developerguide/waf-bot-control-components.html)). Named customer: Instacart ([AWS case study](https://aws.amazon.com/solutions/case-studies/instacart-edge-services-case-study/)). Separately, "CloudFront passes 78.5% of GPTBot traffic without intervention" ([HasData](https://hasdata.com/blog/ai-crawler-block-index)).
- **DataDome.** Checks TLS/JA4, TCP/IP (p0f/JA4T) and ASN coherence, plus Picasso canvas/GPU device checks. *Unquoted*, because datadome.co returned 403 to our fetch. Customers per DataDome's own customer-story titles: Reddit and Tripadvisor/TheFork. Its stated customer list also includes the NYT. All of this is *unquoted* ([Reddit story](https://datadome.co/customers-stories/reddit-traffic-classification-accurate-billing/), [TheFork story](https://datadome.co/customers-stories/use-case-the-fork-tripadvisor/)).
- **PerimeterX/HUMAN.** Checks TLS, IP reputation, header order, a JS fingerprint (the `_px3` token) and behaviour. Customers named by a third-party aggregator: Zillow, Booking.com, Upwork, Wayfair, Fiverr, AutoZone. All *unquoted*.
- **Kasada.** Runs a TLS check, then the kas.js collector, then a proof-of-work worker, then a token check, with no CAPTCHA. Ticketmaster, PlayStation and Sony are named as users. All *unquoted* and secondary ([Kasada case studies](https://www.kasada.io/case-studies)).
- **Imperva ABP.** Claims "700 dimensions" and "200 device attributes". *Unquoted* ([datasheet](https://www.imperva.com/resources/datasheets/Datasheet-Advanced-Bot-Protection.pdf)). No named customer found.
- **F5/Shape.** Uses VM-obfuscated JS telemetry. *Unquoted* ([F5 DevCentral](https://community.f5.com/kb/technicalarticles/what-is-shape-security/284359)). No named customer found.

### C. Anubis (proof-of-work)
- Mechanism: the browser must find a nonce such that the SHA-256 hash has N leading zeros. The default of 5 zeros takes a browser seconds. The pass is kept as a signed cookie ([Wikipedia](https://en.wikipedia.org/wiki/Anubis_(software))). Since v1.20.0 there is also a non-JS "metarefresh challenge" ([LWN](https://lwn.net/Articles/1028558/)). *Confirmed.*
- Deployments. LWN: "the Linux Kernel Mailing List archive (lkml.org), sourcehut, FFmpeg, and others", and "Then GNOME started using the project" ([LWN](https://lwn.net/Articles/1028558/), *confirmed*). Wikipedia adds Wine, FreeCAD, ScummVM, UNESCO, Duke University digital archives and Codeberg. The UN, Arch Wiki, lore.kernel.org and freedesktop.org deployments are *unverified*. LWN also notes that sourcehut moved to "go-away" (per the digger).

### D. Evidence of AI-specific blocking
- **Bloomberg robots.txt** (fetched verbatim): `User-agent: GPTBot / Disallow: /` with narrow allows. The same pattern covers ClaudeBot, anthropic-ai, PerplexityBot and Google-Extended, and CCBot gets a bare `Disallow: /` ([robots.txt](https://www.bloomberg.com/robots.txt)).
- **NYT robots.txt** reportedly bans anthropic-ai, ClaudeBot, Claude-SearchBot, Claude-User and Claude-Web. *Unquoted* ([Substack](https://agentclaude.substack.com/p/blocked-by-name-five-times-at-the)). HasData counts nytimes, nbcnews, cnbc, thehill and bbc.co.uk as blocking 8 AI bots each, and linkedin.com 7 ([HasData](https://hasdata.com/blog/ai-crawler-block-index)).
- **Wiley** (*confirmed*). It blocks GPTBot, ClaudeBot, PerplexityBot and CCBot. It allows Claude-User and ChatGPT-User on "public content only". "We update controls on an ongoing basis through our partnership with Cloudflare" ([Wiley](https://www.wiley.com/en-no/solutions-partnerships/insights/a-guide-to-the-AI-bots-crawling-scholarly-content/)).
- **Our own fetch tool** could not retrieve robots.txt at nytimes.com, wsj.com, ft.com, reuters.com, theatlantic.com or datadome.co. That is an observed failure, not a sourced vendor claim.
- **Lawsuits:**
  - NYT v. Microsoft/OpenAI, Dec 2023 ([Wikipedia](https://en.wikipedia.org/wiki/The_New_York_Times_v._Microsoft_and_OpenAI)).
  - Reddit v. Anthropic, alleging it bypassed robots.txt ([The Register](https://www.theregister.com/2025/06/05/reddit_sues_anthropic_over_ai/)).
  - Reddit v. Perplexity/SerpApi/Oxylabs, Oct 2025, under the DMCA ([Search Engine Land](https://searchengineland.com/reddit-sues-perplexity-serpapi-scraping-google-463681)).
  - Dow Jones/NY Post v. Perplexity; the motion to dismiss was denied ([Loeb](https://www.loeb.com/en/insights/publications/2025/08/dow-jones-and-company-inc-v-perplexity-ai-inc)).
  - Not a lawsuit: Cloudflare's Aug 2025 report accused Perplexity of stealth crawling with a generic Chrome UA and de-listed it as a verified bot ([Neowin](https://www.neowin.net/news/perplexitys-stealth-crawlers-exposed-in-new-cloudflare-report/)).
- **Opt-out standards:**
  - TDMRep (W3C community-group final report, 10 May 2024) signals a reservation via `/.well-known/tdmrep.json`, a `tdm-reservation` HTTP header, a meta tag, or EPUB/PDF metadata ([W3C](https://www.w3.org/community/reports/tdmrep/CG-FINAL-tdmrep-20240510/)).
  - ai.txt (Spawning, 2023) has no standards-body adoption ([Spawning](https://spawning.substack.com/p/aitxt-a-new-way-for-websites-to-set)).
  - IETF aipref exists, but its text was not fetched.

### E. Academic publishers
- Wiley and Cloudflare, as in section D (*confirmed*).
- Vendor for Elsevier/ScienceDirect, IEEE Xplore, JSTOR, ResearchGate and Springer: **NOTHING FOUND** (three queries run).

## Test-target table (candidate)

The URLs are *candidates chosen for their shape*. None was fetched in this run, so an exact article path may have moved, and the harvester's first pass settles that. "Layer" cites the source above, or says *unsourced*. Difficulty runs from 1 (plain HTTP works) to 5 (only a headed real browser, or nothing).

| # | site | URL | protection layer (source) | shape | diff | "complete" oracle |
|---|---|---|---|---|---|---|
| 1 | Ticketmaster | https://www.ticketmaster.com/discover/concerts | Kasada (secondary, unquoted) | listing | 5 | event cards render; matches the Discovery API count for the same query |
| 2 | Zillow | https://www.zillow.com/homedetails/ (any listing page) | PerimeterX/HUMAN (aggregator, unquoted) | listing | 5 | price, beds and zpid present; no `px-captcha` block |
| 3 | Reddit | https://www.reddit.com/r/programming/top/?t=year | DataDome (DataDome story, unquoted) + robots/lawsuits (Register) | thread list | 4 | 25 posts; cross-check `.json` on the same path |
| 4 | Reddit thread | https://old.reddit.com/r/AskHistorians/comments/ (any top thread) | DataDome (unquoted) | thread | 4 | comment count in the header equals comments parsed |
| 5 | Tripadvisor | https://www.tripadvisor.com/Restaurant_Review-* (any) | DataDome (unquoted) | review | 5 | "N reviews" stated; first page shows 10–15 reviews |
| 6 | TheFork | https://www.thefork.com/restaurant/* | DataDome (unquoted) | review | 4 | rating and review count present |
| 7 | NYT article | https://www.nytimes.com/2023/12/27/business/media/new-york-times-open-ai-microsoft-lawsuit.html | robots AI block (Substack, unquoted; HasData) + DataDome (unquoted) + paywall | article | 5 | headline and byline plus the final paragraph; the paywall must be named, not bypassed |
| 8 | Bloomberg | https://www.bloomberg.com/news/articles/2025-10-22/reddit-sues-perplexity-others-over-alleged-data-scraping | robots.txt AI block (fetched) + edge bot wall (unsourced) | article | 5 | headline plus body; "Are you a robot" page named as a failure |
| 9 | WSJ | https://www.wsj.com/ (any article on the Perplexity suit) | our fetch refused robots.txt (observed); vendor unsourced | article | 5 | headline plus lede; paywall named |
| 10 | Reuters | https://www.reuters.com/ (any technology article) | fetch refused (observed); vendor unsourced | article | 4 | dateline and full body |
| 11 | FT | https://www.ft.com/content/ (any) | fetch refused (observed); unsourced | article | 5 | headline; paywall named |
| 12 | The Atlantic | https://www.theatlantic.com/technology/ (any article) | fetch refused (observed); unsourced | article | 4 | end-of-article marker |
| 13 | Stack Overflow | https://stackoverflow.com/questions/11227809 | Cloudflare pay-per-crawl (SO blog) | thread | 3 | answer count equals the SE API `answer_count` |
| 14 | Wiley Online Library | https://onlinelibrary.wiley.com/doi/10.1002/asi.24750 (any public abstract) | Cloudflare + robots (Wiley, confirmed) | article/abstract | 4 | title and abstract; matches the Crossref metadata |
| 15 | ScienceDirect | https://www.sciencedirect.com/science/article/pii/ (any open-access) | vendor NOTHING FOUND | article | 4 | abstract equals Crossref; OA full text reaches References |
| 16 | IEEE Xplore | https://ieeexplore.ieee.org/document/ (any) | NOTHING FOUND | article/abstract | 4 | abstract equals Crossref |
| 17 | JSTOR | https://www.jstor.org/stable/ (any open item) | NOTHING FOUND | article/PDF | 4 | stable-ID metadata; PDF page count |
| 18 | ResearchGate | https://www.researchgate.net/publication/ (any) | NOTHING FOUND (known Cloudflare challenge, unsourced) | article | 5 | title and abstract |
| 19 | GNOME GitLab | https://gitlab.gnome.org/GNOME/gtk/-/issues/ (any) | Anubis (LWN, confirmed) | thread | 3 | note count equals the GitLab API `user_notes_count` |
| 20 | GNOME GitLab file | https://gitlab.gnome.org/GNOME/glib/-/blob/main/README.md | Anubis (LWN) | doc | 3 | equals the raw file via the API |
| 21 | lkml.org | https://lkml.org/lkml/2025/1/1 (any day index) | Anubis (LWN, confirmed) | listing | 3 | message count on the day index |
| 22 | sourcehut | https://git.sr.ht/~sircmpwn/hare | Anubis, later go-away (LWN via digger) | doc | 3 | README plus log visible |
| 23 | FFmpeg trac/git | https://trac.ffmpeg.org/ticket/ (any) | Anubis (LWN, confirmed) | thread | 3 | comment count |
| 24 | Codeberg | https://codeberg.org/forgejo/forgejo/issues/ (any) | Anubis (Wikipedia) | thread | 3 | matches the Forgejo API comments |
| 25 | WineHQ bugs | https://bugs.winehq.org/show_bug.cgi?id=(any) | Anubis (Wikipedia) | thread | 3 | numbered comments reach the last one |
| 26 | lore.kernel.org | https://lore.kernel.org/lkml/ (any thread) | Anubis *unverified* | thread | 3 | thread message count |
| 27 | Instacart | https://www.instacart.com/categories/ (any public page) | AWS WAF Bot Control (AWS, quoted) | listing | 4 | product tiles present |
| 28 | Medium | https://medium.com/@(any)/(any post) | Cloudflare *unsourced* | article | 3 | clap count plus the end-of-post marker |
| 29 | Quora | https://www.quora.com/(any question) | Cloudflare *unsourced* | thread | 4 | stated answer count |
| 30 | Glassdoor | https://www.glassdoor.com/Reviews/ (any company) | Cloudflare (ScrapeOps, unquoted) | review | 5 | "N reviews" stated; first page shows 10 |
| 31 | Indeed | https://www.indeed.com/viewjob?jk=(any) | Cloudflare (ScrapeOps, unquoted) | listing | 5 | job title plus full description |
| 32 | Booking.com | https://www.booking.com/hotel/ (any) | PerimeterX (aggregator, unquoted) | listing | 5 | review score plus count |
| 33 | Wayfair | https://www.wayfair.com/ (any product) | PerimeterX (aggregator, unquoted) | product | 4 | price plus review count |
| 34 | Nike | https://www.nike.com/t/ (any product) | Akamai *unsourced* (only press quoting an Akamai person) | product | 4 | price plus sizes |
| 35 | Amazon | https://www.amazon.com/dp/B0(any) | vendor *unsourced*; CAPTCHA wall common | product | 4 | title, price and "N ratings" |
| 36 | LinkedIn | https://www.linkedin.com/pulse/ (any public article) | robots blocks 7 AI bots (HasData) + authwall | article | 5 | full body or a named authwall |
| 37 | X | https://x.com/(any)/status/(any) | robots AI rules (unverified) + login wall | post | 5 | post text; login wall named |
| 38 | Instagram | https://www.instagram.com/p/(any)/ | robots AI rules (unverified) | post | 5 | caption; login wall named |
| 39 | StubHub | https://www.stubhub.com/ (any event) | pay-per-crawl partner *unsourced* | listing | 4 | ticket listing count |
| 40 | Guardian | https://www.theguardian.com/technology/ (any article) | robots blocks 6 AI bots (HasData); low edge friction | article | 1 | matches the Content API body (control target) |

Gaps against the brief: Imperva, F5/Shape and Fastly have no named site, so they have no target. Bot Fight Mode has no target of its own. Airlines are **nothing found**.

## Coverage
- A. Cloudflare layers: settled, except Bot Fight Mode, which is open.
- B. Enterprise vendors' signals: partial (mostly unquoted; datadome.co blocked us).
- B2. Vendor-to-site attribution: partial (Imperva, F5, Fastly and airlines are open).
- C. Anubis: settled.
- D. AI-specific blocking evidence: partial (only Bloomberg's robots.txt was fetched verbatim).
- E. Academic publishers: partial (Wiley only).

## Verification
7 facts checked on 3 pages:
- LWN: 2 confirmed.
- Wiley: 3 confirmed.
- Cloudflare AI Labyrinth: 2 confirmed.

NOT ON PAGE: "Anubis uses SHA-256 PoW requiring JS" was not on LWN. The SHA-256 detail rests on Wikipedia instead, and LWN notes a non-JS metarefresh option. UNCHECKED: none.

## Open rabbit holes
- Self-attribute vendors from response evidence, such as DataDome, `_px*`, `x-kpsdk-*` and Akamai `_abck` cookies (these are *unsourced* signatures), on Yelp, LinkedIn, Glassdoor, Indeed, WSJ, Bloomberg, Reuters, Booking and the academic publishers.
- Primary confirmation of Kasada at Ticketmaster and PerimeterX at Zillow.
- Wayback snapshots of the robots.txt files at NYT, WSJ, FT, Reuters, Atlantic, Reddit, Medium, Quora and Amazon.
- Cloudflare's full pay-per-crawl partner list.
- Named customers for Fastly, Imperva and F5, and airline bot vendors.
- Anubis status at the UN, Arch Wiki, freedesktop and lore.kernel.org; the go-away, iocaine and Nepenthes alternatives.
- The IETF aipref draft text.
- A second round of digging was not run (effort cap), so the above stay open.
