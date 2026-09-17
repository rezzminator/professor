# Inventory — the Harvester

Tracer report, 2026-09-13, HEAD `00da35b5`, clean tree. Raw map, no verdicts. Telemetry: 8 successful dispatches → 8 reports received (one wave-1 thread failed on the concurrency cap and was re-dispatched; one redundant duplicate run produced no conflicting claim). 51 non-test source files dispositioned: 40 EDGE, 2 RED-HERRING, 2 NOT-MINE, 0 file-level FRONTIER.

## 1. Capabilities (48)

| capability | what it does | evidence | why it matters for a blocked/paywalled source |
| --- | --- | --- | --- |
| OA source fan-out | 12 concurrent open-access providers (unpaywall, openalex, semanticscholar, europepmc, openaire, zenodo, elife, plos, nber, crossref, core, doaj), merged/priority-sorted | `pfm/internal/harvest/resolver.go:121-222` | Tries every free legal copy before anything riskier |
| Identifier detection | Classifies/normalizes DOI, ISBN, PMID, PMCID from raw input | `harvest/identifiers.go:9-167` | One paste reaches the right resolver |
| Known-ID dispatch | Cache check, then identifier-routed provider call | `harvest/known_id.go:10-60` | Single entry point for any ID kind |
| FindWorks bibliographic search | Concurrent multi-provider title/author search returning a fetch handle, no download | `harvest/find_works.go:16-207` | Finds a paper/book by title when no ID is known |
| URL fetch ladder | direct → chrome-impersonation → jina → defuddle → browser → wayback → ocr, sequential | `harvest/harvest.go:167-633` | Seven techniques before giving up on a page |
| Web search | SearXNG or Brave API ranked results | `harvest/search.go:45-204` | Finds candidate URLs when no direct link exists |
| Publisher metadata pivot | Extracts `citation_pdf_url`/DOI from blocked HTML, triggers OA resolution | `harvest/identifiers.go:34-47` | Turns a paywalled landing page into an OA lookup |
| ISBN book resolution | Google Books API + publisher metadata | `harvest/books.go:14+` | Resolves a book ISBN to a fetchable record |
| DOI viewer DOI + PDF-viewer extraction | Fetches DOI viewer DOI page, extracts embedded PDF viewer link | `harvest/mirror_providers.go` | Last-resort mirror when the OA chain has nothing |
| MD5 catalog DOI→MD5→download | Catalog search → MD5 → ads.php/get.php fallback | `harvest/mirror_providers.go` | Mirror-provider recovery |
| IPFS catalog MD5 + IPFS | Catalog search → keyless IPFS CID, dweb.link/ipfs.io fallback | `harvest/mirror_providers.go` | Keyless mirror access |
| DOI mirror DOI/PMID rung | POSTs a DOI/PMID to the configured mirror, extracts the embedded PDF link from the HTML, downloads the PDF; config-gated | `harvest/doi_mirror.go`; `pfm/internal/harvest/README.md:12`; `known_id.go:39` | Classic paywall rung, opt-in |
| JS app-shell rejection | Fingerprints two random routes; rejects if visible text matches (shell, not content) | `harvest/appshell.go:43-118` | Never "succeeds" on a blank SPA shell |
| Headless-first browser rung | Renders headless first; on failure or shell-detected retries headed | `harvest/harvest.go:414-431`, `appshell.go:126-148` | JS-rendered content without a visible browser |
| Chrome-fingerprint TLS transport | tls-client Chrome_146 impersonation | `harvest/net_chrome_transport.go:62-93` | Evades TLS-fingerprint bot blocks |
| Mirror SSRF safety | `assertFetchable`/`normalizeDOIMirrorURL` scheme/host validation, no private ranges | `harvest/net.go:20-80`, `harvest/doi_mirror.go` | Mirror config cannot become an internal pivot |
| Content-addressed cache with TTL | Hash-keyed entries; per-kind TTL (volatile kinds expire, image/archive do not) | `harvest/cache.go:123-177` | No repeat hits on a paywall/mirror |
| Negative-result cache | Failure caching, transient (15s) vs default (120s) | `harvest/cache.go:296-331` | Fails fast instead of hammering a blocking site |
| Public artifact export + redaction | Strips mirror URLs, cache filenames, fallback traces | `harvest/public.go:259-267,225-257` | The user never sees which mirror/rung was used |
| Opaque `harvest:` handle | SHA256(source)-derived persistent handle for a discovered download URL | `harvest/public_handle.go:77-107` | Re-fetch without re-exposing the raw mirror URL |
| Archive browsing | zip/tar(.gz/.bz2/.xz)/7z/rar list/extract with zip-bomb guards (1000 members, 1 GiB, 100× ratio) | `harvest/archive.go:77-130,385-401` | Opens a downloaded archive in place |
| Embedded image localization | Regex-scans markdown, parallel-fetches ≤50 images, rewrites to cache paths | `harvest/images.go:23-99` | Figures come with the article |
| SearchCache | Regex search over fetched documents, public-exported | `harvest/public_search.go:22-160` | Avoids re-fetching known content |
| Local file confinement | Denies /proc, /sys, /etc, `.ssh`, credentials, key files, symlink escapes | `harvest/local.go:37-94` | Protects the host from a "local path" source |
| Wayback snapshot resolution | Resolves a URL through archive.org | `harvest/mirror.go:108` | Recovers a removed/blocked page |
| PMC OA PDF direct download | Rewrites dead FTP hrefs to live HTTPS OA paths | `harvest/mirror.go:88` | The legitimate OA copy from PubMed Central |
| DOI↔PMID↔PMCID conversion | NCBI idconv wrapper | `harvest/mirror.go:14-19` | One lookup style across identifier families |
| Landing-page detection | Distinguishes bibliographic/library landing pages from document content | `harvest/content.go:45` | Never caches a paywall teaser as the paper |
| Converter dispatch | Calls the harvestpy Converter for format conversion | `harvest/content.go:21` | Any fetched format → Markdown |
| Dual-rung image/archive fetch | Direct then chrome-impersonation for binaries | `harvest/media.go:46-62,143-186` | Recovers blocked images/archives |
| Fetch-outcome telemetry | JSONL scoreboard of attempts (method/success/error) | `harvest/stats.go:32-84` | Internal only |
| Stdio MCP transport | Serves the harvester over stdio | `harvestmcp/service.go:400-403`, `cmd/pfm/harvest_command.go:60-70` | Agent clients without a daemon |
| Authenticated external HTTP gateway | OAuth 2.1 + Bearer, PKCE consent | `harvestmcp/remote.go:51-83`, `auth.go:399-448` | Remote clients without exposing the cache |
| Loopback internal HTTP gateway | Unconfined local reads, no auth, same machine | `harvestmcp/service.go:407-415` | Full-power local access |
| Per-tool MCP surface | fetch / findWorks / search / fetchImage / archive / searchCache | `harvestmcp/service.go:429-458` | The verbs an LLM client calls |
| Cache-confined export for remote callers | `LocalRoots = [cache/public]` | `harvestmcp/remote.go:66` | Remote caller sees only exported artifacts |
| Static-token fast path | Pre-shared bearer hash before OAuth | `harvestmcp/auth.go:147-149,440` | Automation without a consent dance |
| PDF→Markdown | PyMuPDF4LLM, OCR escalation for scanned PDFs | `harvestpy/assets/converter.py:102-130` | Reads scanned/locked PDFs |
| HTML→Markdown | Trafilatura + metadata | `converter.py:64-78` | Strips chrome/ads |
| DOCX/XLSX/PPTX→Markdown | Docling | `converter.py:133-135` | Office docs behind share links |
| EPUB/CSV→Markdown | MarkItDown pinned 0.1.6 | `converter.py:138-149`, `assets/pyproject.toml:7` | Ebooks/data exports |
| JSON/plain-text passthrough | stdlib formatting | `converter.py:152-159` | API/data responses |
| Pinned Python env provisioning | uv-managed, SHA256-verified against `targets.json`/`uv.lock` | `harvestpy/provision.go:82-99`, `digest.go:19-84` | Reproducible, no supply-chain drift |
| Post-install smoke/inventory check | Verifies interpreter, hashes, dependency closure, live conversion | `harvestpy/check.go:29` | A broken install is caught, not silent |
| Headless browser worker (Patchright) | Long-lived Python worker, JSON-lines protocol, SSRF-ask before every navigated URL | `harvestpy/browserworker.go:46-256`, `assets/browser/browser.py:57-210` | JS-gated pages with per-URL SSRF re-check |
| Process-group kill on cancel | `syscall.Kill(-pid, SIGKILL)` for python + Chrome children (unix only) | `harvestpy/browserworker_unix.go:21-26` | No leaked Chrome per cancelled fetch |
| Asset embedding | `go:embed` bundles converter.py/browser.py/pyproject/uv.lock/targets.json/size-report.json | `harvestpy/embed.go:11-13` | One binary ships the whole toolchain |
| `harvest ask` | Feeds full fetched artifacts to Claude/Codex to answer a prompt, only from harvested sources | `cmd/pfm/harvest_command.go:143-263` | Ask a question of a paywalled doc once fetched |

## 2. Fallback chain, in order tried

A. Identifier-keyed path (`known_id.go`, then `fetchOA` for DOI):

1. Cache lookup (`cache.go`) — short-circuits everything below if fresh
2. Identifier classification (DOI/ISBN/PMID/PMCID) — `identifiers.go`
3. DOI → OA fan-out, concurrent, priority-merged: `unpaywall → openalex → semanticscholar → europepmc → openaire → zenodo → elife → plos → nber → crossref → core → doaj` — `resolver.go:121-222`, `oa_sources.go`
4. DOI mirror (when configured) — `known_id.go:224-230`, `harvest/doi_mirror.go`
5. Mirror chain (`fetchDOIMirrors`): DOI viewer → MD5 catalog, each when configured — `mirror_providers.go`
6. Google Scholar mirror (if `googleScholarURL`) — `known_id.go:241-248`
7. ISBN path: Google Books — `books.go`
8. PMID path: NCBI idconv → PMCID → PMC OA / Europe PMC — `mirror.go:14-19,88`
9. IPFS catalog — via MD5 lookup (FindWorks/catalog), not DOI-keyed — `mirror_providers.go`

B. Generic URL path (`harvest.go` ladder, sequential):

1. `direct` + `chrome-impersonation` transport — `harvest.go:253-254`, `net_chrome_transport.go`
2. `jina` (keyless reader proxy) — `harvest.go:364`
3. `defuddle` (keyless) — `harvest.go:390`
4. `browser` rung, opt-in (`fetch.browser=true`): headless first, app-shell check, headed fallback — `harvest.go:412,414-431`, `appshell.go:43-148`
5. `wayback` (public URLs only) — `harvest.go:535`, `mirror.go:108`
6. `ocr` (empty/scanned PDF only) — `harvest.go:557`
7. DOI extracted from URL/page after ladder failure → pivots into chain A step 3 — `harvest.go:513-531`

MCP tool surface, verbatim (`harvestmcp/service.go`):

| Tool | Line | Description |
| --- | --- | --- |
| `fetch` | :431 | "Retrieves 1–50 documents as Markdown, input order kept. Call fetch{sources:[…]} — a URL/path, DOI, ISBN, PMID/PMCID, or a handle from findWorks/searchCache…" |
| `findWorks` | :435 | "Finds scholarly papers and books by TITLE or bibliographic query — 'find the paper about X', 'is there a PDF of ‹title›'. No download…" |
| `search` | :440 | "Searches the web — 'search for X', 'find pages about X' — ranked titles, URLs, and snippets, never the page itself…" (conditional on `!DisableSearch`) |
| `fetchImage` | :445 | "Fetches images as local files for vision — 'get this figure / photo / scanned page'. Call fetchImage{sources:[…]}, 1–50 URLs…" |
| `archive` | :449 | "Browses a compressed archive — 'open / list / what is in this .zip, .tar.gz, .7z, .rar', then 'extract member X from it'…" |
| `searchCache` | :453 | "Greps the local cache of already-fetched documents — 'did we already fetch X', 'which cached pages mention Y'. Call searchCache{pattern:…}…" |
| `fetch` (prompt) | :457 | "Fetch a URL or local path and convert its contents to markdown" |

Pins: markitdown 0.1.6, docling 2.107.0, pymupdf4llm 1.27.2.3, trafilatura 2.1.0, Python 3.11 (`harvestpy/assets/pyproject.toml`); patchright 1.62.1 (`assets/browser/pyproject.toml`); uv 0.11.32 + Python 3.11.15 for 4 platforms (`assets/targets.json`); install size ≈5.8 GB managed env + ≈142 MB browser env (`assets/size-report.json`).

## 3. Dead-end ledger (per file)

- `harvest/appshell.go` EDGE ← `harvest.go:340-342` · `archive.go` EDGE ← MCP `archive` + `content.go` · `books.go` EDGE ← ISBN path · `cache.go` EDGE ← every fetch · `content.go` EDGE ← `harvest.go`, `archive.go`, `doi_mirror.go` · `detect.go` RED-HERRING (kind sniffing) · `find_works.go` EDGE ← MCP `findWorks` · `google_scholar.go` EDGE ← `find_works.go`, `known_id.go` · `harvest.go` EDGE ← `harvestmcp.NewHarvester`/`FetchPublic` · `identifiers.go` EDGE · `images.go` EDGE · `known_id.go` EDGE · `local.go` EDGE · `media.go` EDGE but `FetchImage` has 0 external call sites found — AMBIGUOUS · `metadata_failure.go` NOT-MINE · `mirror.go` EDGE; `PMIDToPMCID`, `EuropePMCFiguresURL`, `EuropePMCFulltextXML` DEAD-END (0 callers) · `net.go` EDGE · `net_chrome_transport.go` EDGE · `oa_sources.go` EDGE · `public.go` EDGE · `public_handle.go` EDGE · `public_images.go` EDGE · `public_search.go` EDGE · `public_store.go` EDGE · `resolver.go` EDGE · `result.go` RED-HERRING · `scholarly_sources.go` EDGE · `doi_mirror.go` EDGE ← `known_id.go:39` · `search.go` EDGE · `mirror_providers.go` EDGE · `stats.go` EDGE; `SummarizeStats` DEAD-END (test-only) · `types.go` EDGE.
- `harvestmcp/auth.go` EDGE ← `remote.go` · `remote.go` EDGE ← `mcp_serve_command.go` (FRONTIER) · `service.go` EDGE ← `harvest_command.go`.
- `harvestpy/browserworker.go` EDGE (wiring to `harvest.go`'s `BrowserFetcher` never quoted — FRONTIER: `grep -rn "BrowserFetcher" pfm/internal/harvest pfm/internal/harvestmcp`) · `browserworker_unix.go` EDGE, no `_windows.go` (platform gate) · `check.go` EDGE, 56 callers outside boundary · `converter.go` EDGE · `digest.go` EDGE · `embed.go` EDGE · `provision.go` EDGE (callers outside boundary) · `provision_browser.go` EDGE · assets (`converter.py`, `pyproject.toml`, `uv.lock`, `browser/browser.py`, `browser/pyproject.toml`, `browser/uv.lock`, `targets.json`, `size-report.json`) EDGE.
- `.cache/public/` — flat, 7 hashed files (`.png`, `.md`), no subdirectories; matches `pfm/internal/harvest/README.md`.

## 4. Named frontiers

1. `BrowserFetcher` → `harvestpy.BrowserWorker` concrete wiring site.
2. `harvestpy/check.go` callers, `provision.go` callers, `harvestmcp/remote.go` daemon mount in `mcp_serve_command.go` — outside boundary.
3. `media.go` `FetchImage` real caller — AMBIGUOUS.

## Named holes from the second pass (resume greps)

- `pfm/internal/harvest/doi_mirror.go` (385 lines) — the core DOI mirror implementation was never deep-read by any tracer; resume with a full Read.
- `pfm/internal/harvestpy/assets/uv.lock` (top-level, non-browser) — never read; `grep -n "name = "` lists the pinned closure.
- `pfm/internal/harvest/metadata_failure.go` — classifies paywall/timeout/challenge/conversion/storage failures without leaking provider addresses; dispositioned NOT-MINE without a quote — under-evidenced.
- `content.go` — one pass called it RED-HERRING while quoting its live `Converter.Convert()` call at :21; it is the Go→Python converter bridge.
- Caller-count discrepancies between two independent passes over `harvestmcp` (`NewConfigured` 3 vs 25, `NewRemote` 1 vs 13, `RunStdio` 2 vs 7) — unreconciled.
