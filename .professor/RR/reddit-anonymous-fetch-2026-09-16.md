# RR — How to reliably fetch a public Reddit thread's full content anonymously in 2026

Question: how to reliably fetch a public Reddit thread's full content (post + comments) anonymously in 2026, for a Go document-retrieval tool (harvester, pfm/internal/harvest — rungs: direct HTTP → Chrome TLS impersonation → jina/defuddle reader proxies → real Chrome headless/headed → Wayback → OCR). Return a cited answer with a ranked list of working techniques and their failure modes.

## Answer

**There is no reliable anonymous door to a full Reddit thread in September 2026.** Reddit closed the unauthenticated `.json` surface, blocked logged-out `old.reddit.com`, and — decisively for a tool like ours — closed *self-serve* OAuth app registration behind a manual "Responsible Builder Policy" approval in November 2025, so the official API is no longer a rung you can just take. What remains are partial, flaky doors (redlib instance rotation, PullPush for archived content, real-browser rendering) plus one rung whose reddit-specific evidence is genuinely thin but promising (fingerprint-evading browsers/TLS clients). The harvester's honest best design is: try cheap doors, expect failure, and **degrade to a named, explicit "Reddit: blocked, login required" outcome rather than pretending a rung works.**

## Load-bearing facts

### 1. The endpoints we probed are confirmed dead, today, from this residential host

Live probes run during this research (2026-09-17) against `www.reddit.com/r/ClaudeAI/comments/1kzb8ez/...`:

| Endpoint | UA | Status | Body |
|---|---|---|---|
| `…/.json` | none / `darwin:pfm-harvester:v1 (by /u/{handle})` / Chrome | **403 (all three)** | identical block page |
| `…/.rss` | none | 403 | block page |
| `…/.rss` | custom UA / Chrome UA (retry) | **429** | rate-limited after first probe |
| `old.reddit.com/…/.json` | none / custom | **302** | → `old.reddit.com/login/?reason=lor2` |
| `www.reddit.com/svc/shreddit/comments/...` | none | 403 | same block page |
| `embed.reddit.com/r/…` | — | **403** | "blocked by network security" |
| `reddit.com/oembed?url=…` | — | 200 | title/author/iframe only — **never comments, by design** |
| `api.pullpush.io/reddit/search/comment/?link_id=1kzb8ez` | — | 200 | `data: []` — nothing indexed |
| `archive.org/wayback/available?url=…` | — | **429 both tries** | — |
| `archive.ph/newest/<url>` | — | **404** | no snapshot; archive.today does not crawl on demand |
| redlib `redlib.catsarch.com` | — | 429 | — |
| redlib `red.artemislena.eu` | — | 200 | **Anubis** "Making sure you're not a bot" JS challenge, not content |
| redlib `redlib.cow.rip` | — | 403 | — |

**The descriptive User-Agent makes no measurable difference to anonymous access.** `platform:appid:version (by /u/name)` is an *API-terms* requirement on the OAuth path, not a key to the anonymous path — three UA variants, identical 403.

Response headers on the `.json` path: `server: snooserv`, `via: 1.1 varnish`, **no `cf-ray`** — so that content path is Reddit's own edge (Fastly/Varnish), even though the block page is styled Cloudflare-ish. Which vendor serves the "Prove your humanity" interstitial is **unverified**.

### 2. Which signal fired — best evidence, and it is not what you'd guess

The strongest reddit-specific diagnosis comes from gallery-dl's open issues, where the same "blocked by network security / prove your humanity" wall is an active, unresolved 2026 problem ([#8641](https://github.com/mikf/gallery-dl/issues/8641), [#8838](https://github.com/mikf/gallery-dl/issues/8838)). Community diagnosis there points at **IP/network reputation plus request-rate and auth-state anomalies**, not browser fingerprinting — user `hrxn`: "heuristics ... differentiate ... by IP address ranges, and VPN providers are often caught that way"; user `michealespinola`: reddit treats the connection as anonymous non-OAuth "regardless if using cookies", with "10 requests p/m for non-OAuth clients, 100 requests p/m for OAuth clients". Notably, gallery-dl is **not a browser at all**, so a headless-fingerprint explanation cannot be what trips it there. One user's actual fix was a regression where gallery-dl 1.31+ stopped sending a `user-agent` header unless `extractor.reddit.api: oauth` was set — i.e. missing UA + OAuth misconfig, not cookies.

Caveat for our own observations: our headless-Chrome result (`Prove your humanity`) is a *different* surface from the `.json` 403, and no primary source ties reddit's interstitial to a specific vendor or to the `navigator.webdriver`/headless fingerprint. That attribution is **unverified for reddit specifically**.

**Cookies are not a confirmed workaround** — in both gallery-dl threads users report the block occurring *with* fresh cookies and vanishing *without* them. "yt-dlp documents cookies as the required reddit workaround" is **unverified / not found**; yt-dlp's [FAQ](https://github.com/yt-dlp/yt-dlp/wiki/FAQ) cookie guidance is generic and YouTube-aimed.

### 3. Old Reddit's logged-out block is official and deliberate

Reddit blocked logged-out access to `old.reddit.com` to combat "abusive scraping and automated traffic", with admins warning old.reddit "can't be promised to be around forever" and Huffman describing plans to "limit access, migrate important uses, or rebuild it" ([Engadget](https://www.engadget.com/2230544/old-reddit-could-be-the-next-casualty-of-reddits-war-on-ai-scraping/)). This matches our 302-to-login exactly.

The widely-repeated specific dates for the `.json` shutdown ("May 28/30, 2026") appear **only in SEO content** (crawlora.net, prowlo.com, studioglobal.ai and similar). The *effect* is verified by our probes; the *date and the announcement* are **unverified**.

### 4. The official API is no longer self-serve — this is the decisive finding

Reddit's [Responsible Builder Policy](https://support.reddithelp.com/hc/en-us/articles/42728983564564-Responsible-Builder-Policy) and [API wiki](https://www.reddit.com/wiki/api/) now state: "Approval is required: You must request access and get explicit approval before accessing any Reddit data through our API." New developers are routed to a support ticket instead of creating an app at `/prefs/apps`. Announced in [r/redditdev, "Introducing the Responsible Builder Policy"](https://www.reddit.com/r/redditdev/comments/1oug31u/introducing_the_responsible_builder_policy_new/) (~Nov 14 2025). Developer reports through Feb 2026 are largely denials or dead loops — [struggling to get API approval](https://www.reddit.com/r/redditdev/comments/1r2ukkb/), [only seeing the Responsible Builder page](https://www.reddit.com/r/redditdev/comments/1sppn38/), [Apollo-Reborn #82 "Cannot create new Reddit API keys"](https://github.com/Apollo-Reborn/Apollo-Reborn/issues/82) — one quoted denial: "we cannot grant approval because the submission is not in compliance with Reddit's Responsible Builder Policy".

The **"developer token"** named in Reddit's own block message has **no documented self-serve product** on any official page found. That phrase is currently a dead pointer.

What the API would give you if approved: `GET /comments/{article}` → post + nested comment tree with `more` stubs, resolved via `POST /api/morechildren` ([reddit-archive API wiki](https://github.com/reddit-archive/reddit/wiki/API)). That wiki states **60 requests/minute** for OAuth2 clients with `X-Ratelimit-*` headers; the widely-cited **100 QPM figure appears only in secondary blogs and is unverified** against a reachable primary doc. Data API Terms constraints (delete-when-done, no re-identifying removed content, no ML/AI training without permission, paid agreement for commercial use) are **unverified** — `redditinc.com/policies/data-api-terms` would not load for us.

### 5. Third-party mirrors: one alive, the rest not

- **redlib** (maintained libreddit fork) is the healthiest door — [repo](https://github.com/redlib-org/redlib), live [instances.json](https://github.com/redlib-org/redlib-instances/blob/main/instances.json) with an uptime page, and it implements OAuth-token spoofing and header mimicking specifically to survive Reddit's blocking ([PrivacyTools](https://privacytools.io/app/redlib)). Failure mode is **per-instance and constant**: our three probes gave 429, an Anubis JS challenge, and 403. Usable only with live instance rotation off the published list, never a single hardcoded host.
- **teddit**: dead — shut down 2023, unmaintained since ~Oct 2024 ([HN](https://news.ycombinator.com/item?id=36742748), [repo](https://github.com/teddit-net/teddit)).
- **PullPush** (`api.pullpush.io`) is the de facto Pushshift successor, ~1000 req/hour, with coverage gaps since it lost privileged ingestion in 2023 ([writeup](https://www.redditapis.com/pushshift-alternative)). Good for old/archived threads; returned **zero comments** for our target.
- **Wayback** rate-limits hard (429 on a single retry) and mirrors only initial SSR HTML, not lazy-loaded comment trees. **archive.today** won't snapshot on demand (404).

### 6. TLS impersonation / stealth browsers — promising, but single-sourced

The one 2026 reddit-specific data point found: a September 2026 multi-tool benchmark where **all seven** configurations tested (vanilla headless Chrome, patchright, CloakBrowser, camoufox, rebrowser-patches, nodriver, curl_cffi) returned "ok" against reddit.com across three runs ([ianlpaterson.com benchmark](https://ianlpaterson.com/blog/anti-detect-browser-benchmark-patchright-nodriver-curl-cffi/)). That directly contradicts our own vanilla-headless observation, so **treat it as single-source and unverified** — the difference is plausibly the benchmark hitting a subreddit listing rather than a thread, or a different IP. No corroborating issue on curl-impersonate, curl_cffi, nodriver, or camoufox trackers was found. Generally (not reddit-pinned), TLS-fingerprint impersonation alone is reported insufficient against modern JS-challenge defenses ([Scrapfly](https://scrapfly.io/blog/posts/how-to-bypass-cloudflare-anti-scraping)).

### 7. Probe-target caveat

Both PullPush and Reddit's own oEmbed resolved base36 id `1kzb8ez` to a **real but different post in r/wifejerk**, not the r/ClaudeAI thread named. Two independent sources agreeing suggests the ID in the brief may be wrong or the slug mismatched — oEmbed does not validate that a URL's subreddit matches its ID. Worth re-verifying the ID before drawing conclusions about content availability for that specific thread.

## Recommended rung order for a `reddit.com` URL

Given no self-serve API key, ranked by expected yield per unit of effort:

1. **Real Chrome, persistent profile, headed or `--headless=new` with a warmed profile, residential IP** — renders the shreddit page including comments if it clears the interstitial. Failure mode: "Prove your humanity" (what we saw with a *fresh* profile). Rewrite the URL to the canonical `https://www.reddit.com/r/<sub>/comments/<id>/` and let the page settle; comments load lazily, so wait for `shreddit-comment` elements, not `load`.
2. **redlib instance rotation** — fetch the live [instances.json](https://github.com/redlib-org/redlib-instances/blob/main/instances.json), try 3-5 instances in random order, path `/r/<sub>/comments/<id>`. Accept only a body containing real comment markup; treat 403/429 and an Anubis challenge page as instance-level failure and move on, not as "thread unavailable". This is the only rung that is both anonymous and returns post+comments in one document.
3. **PullPush** for older threads: `api.pullpush.io/reddit/search/submission/?ids=<id>` + `/search/comment/?link_id=<id>`. Failure mode: empty `data: []` for recent threads, ~1000 req/hr.
4. **Wayback** — only for threads with an existing snapshot; expect 429 and SSR-only HTML (post, top comments at best). Back off aggressively.
5. **Stealth browser stack** (patchright / nodriver / camoufox) as an experimental rung above #1 — single-source evidence only; gate it behind a probe before trusting it.
6. **Approved OAuth API** — the correct long-term rung, but it requires a Responsible Builder approval ticket and is not anonymous. If approval lands: `oauth.reddit.com/comments/<id>` with `Authorization: bearer <token>` and `User-Agent: darwin:pfm-harvester:0.1 (by /u/<name>)`, expanding `more` stubs via `POST /api/morechildren`.

**Dead rungs to stop spending on for reddit.com:** the `.json` suffix, the `.rss` feed, `old.reddit.com`, `embed.reddit.com`, `/svc/shreddit/*`, `r.jina.ai` and reader proxies (they relay the 403 from datacenter IPs), plain `python-requests`/default-curl UAs, and archive.today. Each of these should short-circuit with a named reason, not a generic fetch error.

Legality/ToS: the API path is governed by the Data API Terms (contents unverified — page would not load); the Responsible Builder Policy requires approval before "accessing any Reddit data through our API". Using a personal logged-in account's cookies for scripted bulk retrieval is generally contrary to the User Agreement — that is our inference, **not** stated by any source fetched.

## Open questions

- **Which vendor and which signal serves "Prove your humanity"** — Cloudflare, DataDome, or in-house. No primary source found. Determines whether fingerprint evasion or IP reputation is the lever.
- **Why the Sept 2026 benchmark says vanilla headless Chrome passes reddit.com while our probe got the interstitial.** Unresolved; likely a listing-vs-thread or IP-reputation difference, untested.
- **Current official free-tier rate limit** — 60 rpm (old wiki) vs 100 QPM (blogs only). Reddit's current rate-limit doc 403'd on every attempt.
- **Data API Terms text** — `redditinc.com/policies/data-api-terms` would not load; retention and AI-training clauses unverified.
- **Whether a Responsible Builder ticket for a personal research/retrieval tool has any realistic approval odds.** Reports skew to denial, but we read only titles and snippets — the r/redditdev thread pages themselves returned the network-security wall on direct fetch, so their comment bodies were never read. Named gap.
- **The `1kzb8ez` ID mismatch** — two sources resolve it to r/wifejerk. Unresolved.
- **`gateway.reddit.com` / `/svc/shreddit/*` semantics** — beyond a 2018 EasyList issue confirming gateway.reddit.com exists and serves async fragments ([easylist#1684](https://github.com/easylist/easylist/issues/1684)), no documentation of their auth requirements was found; our own probe of a guessed `/svc/shreddit/comments/...` shape returned 403, but the shape was unconfirmed so that is not a clean negative.

Rounds run: 2. Diggers dispatched: 6 (4 + 2), reports received: 6.

## Live-probe addendum (2026-09-17, residential macOS host, no login, no proxy)

The wall is Reddit's own `Prove your humanity` page: a reCAPTCHA v2 checkbox (sitekey `6LeTlV4oAAAAAGioktuFt-KvUtwKRJRfc8A7UJws`) posting to `/comments/<id>/?captcha=1`. Headed Chrome, headless Chrome, Patchright (new_context and launch_persistent_context), and a warmed profile all receive it; a real user in incognito does not. One humanized Patchright run (random pointer moves + scroll) passed at 28.5 s, two later runs did not in 60 s — behavioral scoring, not a switch.

**The door: a `Referer` header.** `curl -A <browser or python UA> -e https://www.google.com/ <thread URL>` → HTTP 200, the full SSR page, 25 `<shreddit-comment>` elements. Any referrer value opens it (Google, DuckDuckGo, `example.com`); curl's default UA stays 403 with or without it; `Sec-Fetch-*` alone returns a shell page. Also opens `/r/<sub>/` listings and other threads.

Still closed with the Referer: `.json` (403), `old.reddit.com` (redirects to the front page), `/svc/shreddit/more-comments/...` (403). A thread ships only its first SSR page of comments (25 of 115 here; depth ≤ 2), `?limit=` is ignored.

Conversion gap: trafilatura keeps the post body and drops every `<shreddit-comment>`; a Reddit extractor must walk `shreddit-comment[author,depth,score,created,permalink]` → `div[slot=comment] .md` bodies.

Harvester gaps found on the way: `isChallenge` (`pfm/internal/harvest/net.go`) lacks "blocked by network security", "blocked due to a network policy" and "prove your humanity", so the wall entered the cache as a 420-byte success.

### Full comment tree (2026-09-17, second probe round)

- Hand-replaying `/svc/shreddit/more-comments/...` (with the first page's cookie jar, same-origin Referer, partial-HTML Accept) answers HTTP 200 "Page not found" — the page JS builds something the static URL lacks. Not worth reverse-engineering.
- Patchright **headless** arriving with `referer=https://www.google.com/` → HTTP 403 wall: the native headless UA is `…HeadlessChrome/152…`; that token alone is the tell.
- Patchright headless + `user_agent` set to a stock Chrome UA + Google Referer + scroll/click-more loop → landed at 25, grew to **82 comments in ~40 s** and plateaued; headed run gave the identical 82. Declared `comment-count` is 115; the remainder is deleted/collapsed/deep-thread leaves (accounting in the probe log).
- Control: Hacker News threads keep their comments through the converter (flattened) — the Reddit loss is the `<shreddit-comment>` custom elements, not a general converter defect.
- Recommended automated rung order for reddit.com: (1) direct GET + Referer + browser UA — SSR page, first ~25 comments, ~1 s; (2) headless Chrome + stock UA + Referer + scroll-until-stable when the caller asks for the full thread; then the Reddit tree extractor over whichever DOM won.
