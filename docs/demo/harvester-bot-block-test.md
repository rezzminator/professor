# Harvester vs. real bot-blocking sites — a live test

Method: confirmed each site's actual robots.txt disallow rules for AI crawlers via `curl` (public, unauthenticated — no bypass involved), then fetched the live homepage with `refresh: true` (cache bypassed) on 2026-09-13. Harvester never declares itself as GPTBot/ClaudeBot/etc. — it fetches under a Chrome-impersonation fingerprint, so robots.txt's AI-bot rules don't even apply to it; the real test is the site's technical bot-management (Akamai/Cloudflare/PerimeterX), not the honor-system file.

## robots.txt confirmed (curl, live)

| Site | Disallows |
| --- | --- |
| nytimes.com | GPTBot, ClaudeBot, CCBot, Google-Extended, PerplexityBot, Bytespider — all `Disallow: /` |
| reuters.com | `Disallow: /` for all agents |
| bloomberg.com | GPTBot, ClaudeBot, CCBot, Google-Extended, PerplexityBot — `Disallow: /`, `Allow: /professional` only |
| washingtonpost.com | same pattern — `Disallow: /` for all AI bots |
| linkedin.com | GPTBot, ClaudeBot, CCBot, Google-Extended, PerplexityBot — `Disallow: /` |
| glassdoor.com | GPTBot, Google-Extended, Amazonbot, ClaudeBot, Perplexity, CCBot — mostly disallowed |
| wsj.com | robots.txt itself returned **HTTP 403** from an Akamai bot-block page (curl, no JS) — the block is enforced before the file is even served |
| theguardian.com | CCBot, Bytespider, PerplexityBot, ClaudeBot, Claude-SearchBot disallowed |

## Harvester fetch results

| Site | Result | Evidence |
| --- | --- | --- |
| **nytimes.com** | **PASS** — full real front page | 2,930 chars, real headlines ("Celine Dion Returns to the Stage", live sports/politics copy) |
| **reuters.com** | **BLOCKED, reported honestly** | `ERROR: The source is protected by an access challenge. Choose another copy.` — no silent junk, no fake success |
| **bloomberg.com** | **DEGRADED** | 50KB returned, but it's the corporate footer/nav shell (Terminal demo links, support numbers) — not the news homepage; the JS-rendered headline layer wasn't captured |
| **washingtonpost.com** | **PASS** | Real title, real bylines (Cat Zakrzewski, Violet Jira, Nitasha Tiku), live content |
| **linkedin.com** | **LOGIN WALL** (not a bot-block — an unauthenticated browser sees the same page) | "LinkedIn: Log In or Sign Up" — expected, not a failure |
| **glassdoor.com** | **PASS** | Real job-listing content, geo-localized to Dutch |
| **wsj.com** | **DEGRADED** | Only 750 bytes — section headers (Top Stories, AI, Homes) with no article text; the same Akamai layer that 403'd the bare robots.txt request appears to be stripping content on the homepage too |
| **theguardian.com** | **PASS** | 43KB, full real front page, live headlines |

## Read on this

Four clean passes on sites that explicitly disallow every AI crawler in robots.txt (NYT, WaPo, Guardian, Glassdoor) — the honor-system file doesn't stop a fetch that never claims to be an AI bot. One honest, explicit failure on Reuters — the app-shell/challenge detector said so instead of returning a blank page as if it were content, which is the "an error never renders as absence" rule working as designed.

Nothing here used the mirror-provider rungs (DOI mirror/DOI viewer/MD5 catalog/IPFS catalog) — those are scholarly-paper rungs, not general news sites, and are config-gated behind mirror URLs this install doesn't have configured.

## Follow-up: does the browser rung fix Bloomberg and WSJ?

`fetch.browser` is already `true` on this install (`${XDG_CONFIG_HOME:-$HOME/.config}/harvester/harvester.config.json`). Ground truth pulled straight from `.cache/stats.jsonl` — the harvester's own per-attempt scoreboard, which records which rung actually served each result (`"detail"` field), redacted out of the public MCP/CLI response but visible on disk:

| Site | Rung that actually served the result | Verdict |
| --- | --- | --- |
| `wsj.com` | `browser-chrome` — the real headless-Chrome render **already ran** | **No fix available** — this IS the browser rung's output. A real Chrome render of the WSJ homepage returns section headers only ("Top Stories", "Artificial Intelligence", "Homes") with no teaser text; that's what the page serves an unauthenticated visitor, browser or not. The ceiling is the site's paywall structure, not a rendering gap. |
| `bloomberg.com` | `jina` — on both the homepage and `/news`, every retry | **Browser rung never fires** — `jina`'s proxy read returns enough word count (the corporate footer/nav block) to pass the ladder's `usableContent()` check, so the ladder returns before ever trying `defuddle` or `browser`. Confirmed by re-running against `/news` too: identical jina-sourced footer both times. |

Read on this: WSJ is a closed case — Harvester's best tool already ran and that's the real page. Bloomberg is an open one — a genuine gap in the fetch ladder's "is this good enough" heuristic, which accepts a legitimately non-empty but wrong page (nav chrome, not news content) as success and never escalates to the render that would likely get the real homepage. Worth a `pfm` bug: `usableContent()` (or the landing-page detector beside it) should recognize a nav/footer-shaped result as insufficient the same way it already recognizes an empty app-shell, so the ladder keeps climbing past `jina` toward `browser` when the story content itself is missing. Not a fix for tonight — logged for a post-demo wave.

## Improvement candidates, ranked (found live, this session — grounded in code, not speculation)

1. **`usableContent()` has almost no bar.** `plugins/harvester/internal/harvest/content.go:35-42` — for HTML/txt it's `len(strings.TrimSpace(content)) >= 1`. Any non-empty response wins, including Bloomberg's corporate footer nav. This is the exact, sole cause of today's Bloomberg miss. Fix: a minimum word-count / link-density threshold so a nav-shaped page doesn't out-rank an unrun `browser` attempt.
2. **The rung that served a result is fully hidden from the caller**, even for legitimate methods. `.cache/stats.jsonl`'s `detail` field (`direct`/`jina`/`browser-chrome`/…) is the only place it's recorded, and `public.go`'s redaction strips it from every public/MCP/CLI response — the same treatment as the mirror-provider rungs, which SHOULD stay hidden. Diagnosing today's Bloomberg/WSJ split took a direct read of that private file. Splitting the redaction — keep the DOI mirror/MD5 catalog/IPFS catalog/DOI viewer providers concealed, surface `direct/jina/defuddle/browser/wayback/ocr` as a `method:` field — turns a 10-minute private-file dig into a visible fact.
3. **No per-domain rung memory.** Every fetch restarts the ladder from `direct`. A host the ladder has already learned needs `browser` (or already learned is fine at `direct`) pays the same jina-then-fail tax every single time — and on Bloomberg, keeps landing on the same false-positive `jina` result forever, never escalating. A small "last known good rung per host" cache would fix the *recurrence* of today's bug even before #1 lands.
4. **Sequential short-circuit, no race.** For a `browser`-eligible fetch, only one rung ever runs — the first to clear the (weak) bar. A bounded race between `jina`/`defuddle` and `browser`, keeping the denser result, would catch cases like Bloomberg without abandoning the fast path for the common case where `direct` is already right.
5. **No content-confidence signal reaches the caller.** WSJ's header-only page and NYT's full front page currently look structurally identical to a downstream caller (`harvester ask` included) — both are just "success". A coarse `content_confidence: low|medium|high` (word count, link density, presence of a story body) would let a caller know to treat a thin result skeptically instead of quoting it as if it were the full article.
6. **The mirror-provider rung is entirely dark on this install.** `${XDG_CONFIG_HOME:-$HOME/.config}/harvester/harvester.config.json` has no mirror providers configured. Every fetch tonight ran the legit ladder only (OA fan-out + direct/jina/defuddle/browser/wayback/ocr). Don't claim the mirror rung works live on this machine before configuring it — and `doi_mirror.go` itself (385 lines) was never deep-read by any tracer, so its correctness is unverified even if it were turned on.

Deliberately NOT recommended: anything that solves an *interactive* challenge (CAPTCHA, Turnstile puzzle). The code comment at `harvest.go:404-408` states the boundary explicitly — the browser rung "never solves anything interactive" — and that's the correct line to hold, not a gap to close.
