# Harvester surface redesign — spec

Part of the harvester-perfection epic (`manifest.md` § Surface redesign holds the user's decisions; this file turns them into build tasks). Every claim about today's code was mapped from the code on `wave/consent` at 58e9c2ba.

## Goal

Four tools whose names say what they do, one retrieval function behind every network read, typed results, and a remote server that exposes only MCP. Nothing of the old surface survives: no alias, no deprecated name, no reference.

## Today (the four ladders)

| Path | Rungs | Where |
| --- | --- | --- |
| Page fetch, identifier candidates, OA pivots, Wayback | direct, Chrome impersonation, jina, defuddle, browser (headless first), OA pivots, Wayback, OCR retry | `fetchURLWithPolicy`, `harvest.go` |
| Scholarly providers, DOI mirror | direct, Chrome, headless browser, headed browser | `gatewayFetch`, `gateway.go` |
| `FetchImage`, archive bytes | direct, Chrome | `media.go` |
| Images inside a page | one client, with a Referer | `LocalizeImages`, `images.go` |

Resolver metadata lookups (OpenAlex, Crossref and the rest) and web-search backends are API calls, not retrieval of a target; they stay plain HTTP.

## The central retrieval function

`Harvester.Retrieve(ctx, target string, want Want, policy Policy) (Retrieved, error)` in a new `retrieve.go` (with `retrieve_test.go`, arch rule C13).

- `Want` is `WantPage` (Markdown through the converter, with every check that exists today) or `WantFile` (the bytes, unparsed, streamed to the binary cache).
- `Policy` names the rungs a caller may use and their budget. The default policies:
  - `PolicyPage`: today's `fetchURLWithPolicy` ladder unchanged. `Retrieve` with `WantPage` calls it; the ladder's body does not move (`harvest.go` is at its ceiling).
  - `PolicyFile`: direct → Chrome impersonation → Wayback raw copy (`id_` form) → browser download (the browser rung captures a download or the response body of the navigation). Reader rungs never run: they return text, not bytes.
  - `PolicyInlineImage`: direct → Chrome impersonation, with the page as Referer. A page with 60 images must not start 60 browsers; the narrower policy is named in the code and in the page's image note when an image is skipped.
  - `PolicyGateway`: direct → Chrome → headless browser → headed browser, for the scholarly providers and the DOI mirror.
- `FetchImage`, `fetchArchiveBytes`, `LocalizeImages`' client call, `providerFetch`/`providerDownload` and the DOI mirror all call `Retrieve`. The gateway's rung code becomes the browser step of `Retrieve`; `gatewayFetch` stops existing as a separate ladder.
- The binary guard lives in `Retrieve`: with `WantPage`, a body is converted only when it is text or a document format the converter reads (routed by magic bytes, never by extension); any other body ends in a `file` result that names its detected type and points at `download_file`. A body is never stored as page content when it is not text.
- File mode writes through `internal/atomicfile` (arch rule C6) into the binary cache, streams (never a whole body in memory), and caps at `harvest.maxDownloadBytes` (default 2 GiB); a body over the cap is a named failure with the size the server declared.
- No new `http.Client` construction (C24), no new `time.Now` or `os.Getenv` door (C22), logs through `obs` (C23).

## The four tools

Every tool returns typed output: `discardOutput` goes, each handler returns its output struct, the SDK (go-sdk v1.4.0) derives the output schema and sends `structuredContent`; the handler still fills `Content` with the same readable text it sends today, so a client that ignores structured content loses nothing. Output validation fails the call on a schema mismatch, so each output type gets a test that round-trips a real result through the SDK.

The tools, their inputs and their output fields are named by `naming-spec.md` (the user's naming ruling; it replaces this section's earlier six-tool table):

| Tool | Registered |
| --- | --- |
| `read` — `urls`, `files`, `publications` as separate arrays in one call | always; on the remote gateway its schema has no `files` field |
| `download_file` | always |
| `search_literature` | always |
| `search_web` | only when SearXNG or Brave is configured; absent otherwise, and the server instructions do not name it |

- A misplaced item is a per-item named error naming the right field (`naming-spec.md` § `read` — input). A landing URL of a paper in `urls` reads the page; in `publications` it reads the work.
- `include_content` (default true) is on `read`.
- The MCP prompt named `fetch` is removed.
- `RegisteredToolNames` (`toolnames.go`) changes in the same edit as the registration; the daemon's `/status` reads it.
- `service.go` (979 lines, baseline 1052) is split by tool family so no file crosses its ceiling: `tools_read.go`, `tools_download.go`, `tools_works.go`, `tools_search.go`, each with its `_test.go`.

## Caller headers (user request, after R2)

`read` (for `urls`, and for `publications` the landing origin; never `files`) and `download_file` take an optional `headers` object (name → value) that extends the request headers sent to the target. `search_literature` and `search_web` do not: their requests go to metadata and search APIs, not to a target.

- Scope: the headers go only to the target's origin — direct, Chrome impersonation and the browser (scoped by a route to that origin, never to subresources or redirects on another origin). Reader services (jina, defuddle), Wayback and resolver APIs never receive them: a header may carry a credential. A result the ladder reached through a rung that ran without the caller's headers says so in `gaps`.
- Validation at entry, a named error per breach: names are HTTP tokens; values carry no CR or LF; at most 32 headers and 8 KiB in total; hop-by-hop and framing headers (`Host`, `Content-Length`, `Transfer-Encoding`, `Connection`, `Upgrade`, `TE`, `Trailer`, `Keep-Alive`, `Proxy-*`) are refused. A caller header overrides the harvester's default of the same name (User-Agent, Accept-Language, Referer).
- Cache: the cache key includes a hash of the sorted header set, so a page read with a credential is never served to a call without it, nor the other way round. Values never appear in logs, receipts, errors or the typed output; names may.
- CLI: `pfm harvest --header 'Name: value'` (repeatable), and on `pfm harvest download-file`.

## Remote: only MCP, and signed file links

- The remote gateway (`remote.go`) registers `read`, `download_file`, `search_literature` and `search_web` (when configured); its `read` schema has no `files` field, and no tool returns a server path.
- `download_file` on the local server (stdio and the loopback daemon) returns `path`, the stored file's absolute path, and no url: the caller takes the file from there.
- `download_file` on the remote server returns per item `id` (the content's sha256), `url`, `expires` (RFC 3339 UTC), `bytes`, `content_type`, `sha256`, and a `resource_link` with URI `harvest://download/{id}`, its MIME type and size — never a server path. The server registers the resource template `harvest://download/{id}`; `resources/read` answers with the bytes as a blob. The template's handler reads only files the download store holds under that id; any other id is `ResourceNotFound`.
- File transfer is the one exception to "only MCP": `url` is `{publicURL}/files/{sha256}?exp={unix}&sig={hex HMAC-SHA256(key, sha256 + "." + exp)}`, fetched by the caller with `curl -fL -o <file> <url>` (then checking the sha256), never read into context. The key is 32 random bytes per server process, never written or logged, so a restart invalidates every link; a link lives 10 minutes (`downloadLinkTTL`). A service with no public URL says so by name in the item's note and carries only the `resource_link`.
- `GET|HEAD /files/{sha256}` on the gateway's listener sits outside the bearer check (the signature is the access control; `/mcp` stays behind the bearer). The id must match `^[0-9a-f]{64}$` and be held by the download store, so nothing else is reachable and nothing is listed. The signature is checked in constant time, then the expiry: bad or missing signature 403, expired 410, unknown or malformed id 404, each body naming the cause. The file streams through `http.ServeContent` (Range, If-Modified-Since, HEAD) with its stored type, `Content-Disposition: attachment`, `Cache-Control: private, no-store` and `X-Content-Type-Options: nosniff`, uncapped; each request logs the id prefix, status and bytes, never the signature.
- A blob larger than `harvest.maxResourceBytes` (default 25 MiB) is not sent: the read answers a named error with the size and the reason (the MCP transport carries it base64 in one message). The `download_file` result states the same limit up front when the file is over it.
- Binary is never put in a tool result. Images are not inlined as `ImageContent`.
- `/mcp` passes every JSON-RPC method after the bearer check, so `resources/read` reaches the SDK's registered-resource lookup, which is the access gate.

## Removed, with every reference

- `archive`: the tool, `Harvester.Archive`, `archiveList`, the zip, tar, 7z and rar member readers (`archive.go`), their tests, `PublicArchiveListing`, and the texts that send a caller to it. A zip is a `download_file`.
- `searchCache`: the tool, `Cache.Search`, `Harvester.SearchCache`, `SearchCachePublic`, `searchPrivateCache`, `publicSearchRegexp` (unless another caller is found; the build task enumerates the callers first), their tests.
- `fetchImage`: the tool; `FetchImage` becomes the file path of `Retrieve` behind `download_file`.
- Every user-facing string that names an old tool is rewritten to the new names: the inventory lists about 20 in `harvest.go`, `net.go`, `public.go`, `known_id.go`, `harvest_diagnostics.go`, the corpus manifest's pinned errors, and `pfm/cmd/pfm/harvest_ask_command_test.go`.

## Callers, same pass

| Where | Change |
| --- | --- |
| `templates/global/agents/rr.md`, `sub-rr.md` (through `/pcm`) | tools lists and steps name `read`, `search_literature`, `search_web`, `download_file`; `searchCache` gone |
| `templates/global/commands/pfm.md` | the four tools and the CLI verbs |
| `workflows/deep-rr/engine/src/**` | `fetch` → `read` with `urls` or `publications` by what the step reads; `search` → `search_web`; then `npm run build` regenerates `workflow.js` and the snapshots are updated; `SKILL.md` preflight uses `read` |
| `docs/dev/pfm-surface.md` | the tool table and CLI rows |
| `docs/dev/testing/landscape.md` | M21–M29, H10, H11 rewritten; the dangling `mcp.md` citation fixed to the file that holds the tools |
| `infra/fence/lanes/M.sh`, `map.tsv`, `beats.md` | M50's served-tool list; M21–M28 per tool; H10, H11 and the chat-fetch check use `read` with `include_content: false` and the `cached` field as the cache oracle instead of `searchCache` |
| `infra/readme-cards/deck.html` | the demo card: `search_literature` → `read` with the handle in `publications` |
| `docs/design/RR/rr.md` | the `searchCache` design notes removed |
| `pfm/internal/harvest/README.md`, `pfm/cmd/pfm/mcp_serve_test.go` | new names |
| `.professor/retro.md` | the open `searchCache` entries are stamped resolved by removal |

Releases and demo inventories are history and stay as written. The `pfm archive` chat command is a different subject and is untouched.

## CLI

- `pfm harvest <url|path|identifier>...` stays the universal read (it routes each argument into `read`'s `urls`, `files` or `publications`): `pfm harvest [--refresh] [--include-content=false] [--ocr-language latin|zh|ja|ar|ru|he] [--json] [--header 'Name: value']... <url|path|identifier>...`.
- `pfm harvest download-file [--json] [--header 'Name: value']... <url>...` prints the path, kind, type and size.
- `pfm harvest search [--type any|paper|book] [--limit N] [--json] <query>...` runs `search_literature`.
- `cmd/pfm` has one line of budget: the harvest verbs move their bodies into a package (`internal/harvestcli`, with tests) and `cmd/pfm` keeps the dispatch only.

## Build tasks (one at a time, each verified and landed before the next)

1. **R1 central retrieval**: `retrieve.go`, the four policies, file mode with the cap and the binary guard; `FetchImage`, archive bytes, inline images, the gateway and the DOI mirror routed through it. Proof: fixture tests per policy and per rung failure; a live sim run of 10 targets (page, PDF, image, audio, a zip, a paywalled page, a DOI through a provider) identical in outcome to today except where the binary guard now names a file.
2. **R2 tools**: the four tools, typed output, the misplaced-field errors, the removals in Go, the string rewrites, the service split, the remote resource template and its cap. Proof: SDK round-trip tests per tool; the remote gateway serves `resources/read` for a downloaded file and refuses an unknown id; a local `stdio` session lists exactly the four (three without search).
3. **R3 docs, lanes, CLI**: everything in the callers table outside `templates/` and `workflows/`, plus the CLI. Proof: the M lane passes in the fence; `grep` over the tree finds no old tool name outside releases and demo inventories.
4. **R4 deep-rr**: engine sources, build, snapshots, `SKILL.md`. Proof: the engine's test suite passes.
5. **R5 templates** (main loop, through `/pcm`): `rr.md`, `sub-rr.md`, `pfm.md`.

## Open risks, named

- Whether Claude Code and Codex follow a `resource_link` into `resources/read` is outside the SDK; R2 tests the server side, and the final sweep tries it from a real client.
- Removing the archive member readers drops a working feature (reading a document inside a zip); the user ruled it: a zip is a file.
