# RR — Anubis proof-of-work bot wall: mechanics, deployers, visitor cost, and the documented way a legitimate non-browser fetcher gets through

Question: Anubis, the open-source proof-of-work "bot wall" (TecharoHQ/anubis) now fronting many sites — how it works, who deploys it, what it costs a visitor, and how a legitimate non-browser fetcher (a CLI document harvester, a feed reader, an archiver) is expected to get through it without cheating: the documented allow-paths (user-agent rules, robots/policy files, allow-lists, API or token routes), the challenge mechanics and their default difficulty, and what its maintainers say about non-browser clients.

## Answer

Anubis challenges clients that *look like browsers* — its shipped catch-all rule fires only on a User-Agent containing `Mozilla|Opera` — so the sanctioned way for an honest CLI harvester, feed reader or archiver to get through is the mundane one: send a distinctive, non-`Mozilla` User-Agent identifying the tool and an operator contact URL, and it falls through every shipped rule to `weight <= 0` → `ALLOW` without solving anything. Beyond that there is no bot-facing token, API key or cryptographic identity route in shipped Anubis: everything else (path allow-lists, header/API-key matches, IP-range crawler entries) is configured by the *site operator*, and Techaro's Web Bot Auth implementation `samesame` is a 12-commit stub with a one-line README.

## The map

### A. Challenge mechanics and default difficulty

The proof of work is plain SHA-256 over challenge-plus-nonce: "you take some base value (the "challenge") and a constantly incrementing number (the "nonce"), so the thing you end up hashing is this: `const hash = await sha256(\`${challenge}${nonce}\`);`", and verification is cheap — "The server then only has to do one sha256 operation: the one that confirms that the challenge (generated from request metadata) and the nonce (provided by the client) match the difficulty number of leading zeroes." ([why-proof-of-work.mdx](https://raw.githubusercontent.com/TecharoHQ/anubis/main/docs/docs/design/why-proof-of-work.mdx))

The Go source sets `const DefaultDifficulty = 4`, `var CookieName = "techaro.lol-anubis"`, and `const CookieDefaultExpirationTime = 7 * 24 * time.Hour` ([anubis.go](https://raw.githubusercontent.com/TecharoHQ/anubis/main/anubis.go)) — i.e. one solve buys a **week** of access. This resolves the earlier cookie-name dispute in favour of `techaro.lol-anubis` on current `main`; issue titles referencing `techaro.lol-anubis-auth` remain, so the *auth* cookie may be a separate name — left as an open question rather than settled.

Cost per difficulty, from LWN quoting the docs: "The default is 4, which is noticeable but only takes a few seconds. A difficulty of 6, on the other hand, can take minutes to complete." ([LWN](https://lwn.net/Articles/1028558/))

A no-JavaScript path exists: "With 1.20.0, administrators can use the metarefresh challenge...This method is currently off by default, as it is still considered somewhat experimental." ([LWN](https://lwn.net/Articles/1028558/)) That concerns the *default challenge method*; metarefresh does appear in the shipped threshold ladder at the lowest suspicion band (below).

### B. The policy engine — how the decision is actually made

Actions, from the admin docs: **ALLOW** "Bypass all further checks and send the request to the backend."; **DENY** "Deny the request and send back an error message that scrapers think is a success."; **WEIGH** "Change the request weight for this request." Rules match with "Policy rules are matched using Go's standard library regular expressions package", and the governing default is: **"If no rules match the request, it is allowed through."** ([policies.mdx](https://raw.githubusercontent.com/TecharoHQ/anubis/main/docs/docs/admin/policies.mdx))

The shipped threshold ladder ([botPolicies.yaml](https://raw.githubusercontent.com/TecharoHQ/anubis/main/data/botPolicies.yaml)):

| Threshold | Weight | Action |
| --- | --- | --- |
| `minimal-suspicion` | `weight <= 0` | ALLOW |
| `mild-suspicion` | `> 0` and `< 10` | CHALLENGE, `metarefresh`, difficulty 1 |
| `moderate-suspicion` | 10–20 | CHALLENGE, `fast`, difficulty 2 |
| `mild-proof-of-work` | 20–30 | CHALLENGE, `fast`, difficulty 4 |
| `extreme-suspicion` | `>= 30` | CHALLENGE, `fast`, difficulty 6 |

The only catch-all rule is `name: generic-browser`, `user_agent_regex: Mozilla|Opera`, `action: WEIGH`, `adjust: 10` — confirmed present, and confirmed to be the *sole* generic rule. Weight is what makes a browser challengeable at all; a client not pretending to be a browser never accrues it from this rule.

### C. How a legitimate non-browser fetcher gets through — the load-bearing finding

**The rule chain** (digger-traced, each file quoted): `_deny-pathological.yaml` is an import aggregator whose only UA-based denials are named headless-browser signatures — `lightpanda`, `HeadlessChrome`, `HeadlessChromium`, `hyperbrowser` — with no rule for an empty UA and no generic "bot" substring match ([_deny-pathological.yaml](https://raw.githubusercontent.com/TecharoHQ/anubis/main/data/bots/_deny-pathological.yaml), [headless-browsers.yaml](https://raw.githubusercontent.com/TecharoHQ/anubis/main/data/bots/headless-browsers.yaml)). `ai-block-aggressive.yaml` is an exhaustive *named* list (`AI2Bot|...|Bytespider|Claude-Web|...|YouBot`, action DENY), not a heuristic ([ai-catchall.yaml](https://raw.githubusercontent.com/TecharoHQ/anubis/main/data/bots/ai-catchall.yaml)). No shipped rule keys on a missing User-Agent or missing Accept-Language. A polite `MyHarvester/1.0 (+https://example.org/bot)` therefore matches nothing, skips the `Mozilla|Opera` weight, and lands in `minimal-suspicion` → ALLOW. Verified for the threshold and generic-browser rules; the deny-list contents are digger-quoted but were not in my own verification pass — marked `unquoted`.

**Paths allowed for everyone**, from [keep-internet-working.yaml](https://raw.githubusercontent.com/TecharoHQ/anubis/main/data/common/keep-internet-working.yaml), all `action: ALLOW`: `^/\.well-known/.*$`, `^/robots\.txt$`, `^/favicon\.(?:ico|png|gif|jpg|jpeg|svg)$`, `^/sitemap\.xml$`. Robots.txt is reachable without solving anything — a crawler can always read the policy it is meant to obey.

**The shipped good-crawler allow-list** (`data/crawlers/_allow-good.yaml`) covers Google, Apple, Bing, DuckDuckGo, Qwant, Internet Archive, Kagi, Marginalia, Mojeek, Common Crawl, Wikimedia Citoid, Yandex and arquivo.pt. Entry is by *published IP range*, not by claimed UA — the archiver entry is `- name: internet-archive` / `action: ALLOW` / `remote_addresses: ["207.241.224.0/20", "208.70.24.0/21", ...]` with **no** `user_agent_regex` at all ([internet-archive.yaml](https://raw.githubusercontent.com/TecharoHQ/anubis/main/data/crawlers/internet-archive.yaml)). The docs state the principle: "you can allow a search engine to connect if and only if its IP address matches the ones they published" ([policies.mdx](https://raw.githubusercontent.com/TecharoHQ/anubis/main/docs/docs/admin/policies.mdx)). No documented application process for adding a new crawler was found — an open gap, not a confirmed absence.

**Feed readers are an operator's job.** The docs acknowledge the category — "Some things that look like bots may actually be fine (IE: RSS readers)." ([policies.mdx](https://raw.githubusercontent.com/TecharoHQ/anubis/main/docs/docs/admin/policies.mdx)) — but `botPolicies.yaml` ships **no** RSS/atom/feed rule (verified). When Thunderbird's reader was blocked (issue #721), Xe's answer was to ask the site admin to add `- name: rss-feed / path_regex: ^/index.xml$ / action: ALLOW`, and the reporter confirmed "server admin updated his config and that fixed the issue" ([issue #721](https://github.com/TecharoHQ/anubis/issues/721)). A later commenter reports the predictable consequence — once the feed path is unchallenged, bot traffic became "90% or more of the specific hits on feeds" — unresolved.

**API routes.** Wikimedia hit the classic failure: "anubis is enabled on the API (which means as you see below that it responds with html when asked for json which is terrible)", root-caused as "Anubis is likely offering a challenge because the user-agent is looking like a browser rather than some api client"; the fix was excluding API endpoints from Anubis, closed Feb 2026 ([Phabricator T407499](https://phabricator.wikimedia.org/T407499)). The community-documented operator recipe is a `path_regex`/header `ALLOW` rule — "If your API callers use a specific header (e.g., `X-API-Key` or a custom `User-Agent`), you can also match those" ([Discussion #1543](https://github.com/TecharoHQ/anubis/discussions/1543)). That is a shared secret an operator grants, not a route a bot author can take unilaterally.

**Cryptographic identity is not yet real.** Techaro's [samesame](https://github.com/TecharoHQ/samesame) README is, in full, "The Web Bot Auth implementation for Anubis." — 12 commits, no releases, no integration docs. Nothing in Anubis's shipped policy config references Web Bot Auth, `Signature-Agent` or RFC 9421. The underlying spec, `draft-meunier-web-bot-auth-architecture`, is an individual Internet-Draft (draft-05, March 2026), marked expired/archived, not working-group adopted ([IETF datatracker](https://datatracker.ietf.org/doc/html/draft-meunier-web-bot-auth-architecture)); Cloudflare uses it for verified bots ([Cloudflare docs](https://developers.cloudflare.com/bots/reference/bot-verification/web-bot-auth/)).

### D. Who deploys it

Origin: created by Xe Iaso after an AI crawler — Wikipedia names Amazon's — overloaded her Git server, ignoring robots.txt; announced January 2025 under the Techaro org ([Wikipedia](https://en.wikipedia.org/wiki/Anubis_(software)), [LWN](https://lwn.net/Articles/1028558/)). Named deployers: GNOME's GitLab, the Linux kernel mailing-list archives and Git server, sourceware.org, FFmpeg, Wine, UNESCO, FreeCAD, ScummVM, SourceHut, OpenWRT, Enlightenment, Xeno-canto, Duke University Digital Repositories ([Wikipedia](https://en.wikipedia.org/wiki/Anubis_(software)), [LWN](https://lwn.net/Articles/1028558/)). Codeberg runs it selectively — "only for expensive routes like viewing code history at a specific commit, or filtering issues" ([Forgejo discussion #319](https://codeberg.org/forgejo/discussions/issues/319)). LWN itself declined to deploy it. Adoption counts (≈200,000 downloads per 404 Media; ~9,600 sites per Wappalyzer) are `unquoted` — not verified in my pass.

Business model: the anime-girl mascot is commissioned art and "Altering the branding is an enterprise feature"; Xe has written "you're kindly asked to support Anubis financially if you intend to remove or replace the character" ([Discussion #143](https://github.com/TecharoHQ/anubis/discussions/143)) — both `unquoted`. Thoth, the paid reputation service, is explicitly advisory: "Thoth cannot and will not arbitrarily block requests... Thoth is there to inform Anubis and influence the weight of requests" ([Thoth docs](https://techarohq-anubis.mintlify.app/integrations/thoth)) — `unquoted`.

### E. What it costs a visitor, and whether it works

Duke's pilot: Anubis "blocked more than 4 million unwanted HTTP requests per day—while still allowing real users and well-behaved bots", and the human cost was small but real — "12 people reported having a problem with Anubis in one week, but that was usually due to having cookies disabled, according to the report." ([LWN](https://lwn.net/Articles/1028558/))

The economic critique, from Tavis Ormandy: "So (11508 websites * 2^16 sha256 operations) / 2^21, that's about 6 minutes to mine enough tokens for every single Anubis deployment in the world."; "That means the cost of unrestricted crawler access to the internet for a week is approximately $0."; "I don't think we reach a single cent per month in compute costs until several million sites have deployed Anubis." ([lock.cmpxchg8b.com/anubis.html](https://lock.cmpxchg8b.com/anubis.html)) No published Xe Iaso rebuttal to Ormandy was found — searched, nothing found; not a failed lookup.

LWN's own caveat — "there is no guarantee that Anubis will maintain its edge against scrapers in the long run. Today, Anubis works very well at blocking unwanted visitors, but tomorrow?" ([LWN](https://lwn.net/Articles/1028558/)) — was answered a month later: Codeberg posted "It seems like the AI crawlers learned how to solve the Anubis challenges" ([The Register, Aug 2025](https://www.theregister.com/software/2025/08/16/codeberg-beset-by-ai-bots-that-now-bypass-anubis-defense/1023101)) — `unquoted`.

## Coverage

| Sub-area | Status |
| --- | --- |
| A. Challenge mechanics & defaults | settled |
| B. Policy/config surface | settled |
| C. Documented non-browser allow-paths | settled |
| D. Deployers & adoption scale | settled (adoption *counts* partial) |
| E. Visitor cost | partial — no measured figure for old/low-power hardware |
| F. Maintainer statements on non-browser clients | partial — only per-issue advice, no bot-author-facing policy |
| G. Cryptographic bot identity (Web Bot Auth) — *added* | settled: exists as a stub, not usable |

## Verification

25 statements checked across 8 source pages; 24 confirmed with quoted sentences.

- **NOT ON PAGE:** "Anubis will completely block users who turn off JavaScript" — LWN answered NO; the page instead says 1.20.0 lets administrators use metarefresh "in addition to, or in place of, the JavaScript proof-of-work challenge". Removed from the answer.
- **UNCHECKED:** none — no fetch failed in the verification pass.
- Marked `unquoted` above (digger-sourced, not re-verified by me): the deny-list file contents, adoption counts, branding/Thoth quotes, the Codeberg bypass quote.

## Rabbit holes left open

- **`techaro.lol-anubis` vs `techaro.lol-anubis-auth`** — `main` defines the former; live issue titles use the latter. Likely two different cookies (test vs auth); unresolved.
- **No documented process to get a crawler into `data/crawlers/`** — the single most useful thing for an honest harvester author, and nothing found. Whether an unwritten PR norm exists is unknown.
- **No Anubis documentation addressed to bot authors at all** — every page is written for site admins. Searched; nothing found.
- **Maintainer stance on scripted PoW solving** — the only maintainer word found is Xe's "I'm aware of this. Please send future such things to security@techaro.lol" on the bypass-extension thread ([Discussion #663](https://github.com/TecharoHQ/anubis/discussions/663)). No stated policy on whether a non-browser client solving the PoW honestly is acceptable.
- **`samesame` wiring** — no issue/PR traced showing when or whether Web Bot Auth reaches shipped Anubis.
- **The regex-dialect trap in `botPolicies.yaml`** — issue #884 shows docs using unescaped `.` where escaping is required; a live source of operator misconfiguration, unresolved in-thread.
- **Feed allow-listing backfires** — the #721 report of feed paths becoming 90%+ bot traffic once exempted has no maintainer-endorsed mitigation.
- **Old-device cost** — no source gives a wall-clock or battery figure for genuinely low-power hardware at difficulty 4.
- **TLS/JA3 fingerprinting** — if Anubis weighs TLS fingerprints outside the YAML rules, a Go/Python HTTP client could be flagged by a path none of these files describe. Untraced.
- **Failed fetch, named:** `https://anubis.techaro.lol/docs/admin/policies` served me an Anubis denial page (error `9e4edb5b6b850c41`, v1.28.0-pre1) instead of the docs. All docs content in this map came from `raw.githubusercontent.com` mirrors instead.
