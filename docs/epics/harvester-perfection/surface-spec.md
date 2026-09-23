# Harvester surface redesign — spec

Part of the harvester-perfection epic (`manifest.md` § Surface redesign holds the user's decisions; this file turns them into build tasks). Every claim about today's code was mapped from the code on `wave/consent` at 58e9c2ba.

## Goal

Six tools whose names say what they do, one retrieval function behind every network read, typed results, and a remote server that exposes only MCP. Nothing of the old surface survives: no alias, no deprecated name, no reference.

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
- The binary guard lives in `Retrieve`: with `WantPage`, a body is converted only when it is text or a document format the converter reads (routed by magic bytes, never by extension); any other body ends in a `file` result that names its detected type and points at `download`. A body is never stored as page content when it is not text.
- File mode writes through `internal/atomicfile` (arch rule C6) into the binary cache, streams (never a whole body in memory), and caps at `harvest.maxDownloadBytes` (default 2 GiB); a body over the cap is a named failure with the size the server declared.
- No new `http.Client` construction (C24), no new `time.Now` or `os.Getenv` door (C22), logs through `obs` (C23).

## The six tools

Every tool returns typed output: `discardOutput` goes, each handler returns its output struct, the SDK (go-sdk v1.4.0) derives the output schema and sends `structuredContent`; the handler still fills `Content` with the same readable text it sends today, so a client that ignores structured content loses nothing. Output validation fails the call on a schema mismatch, so each output type gets a test that round-trips a real result through the SDK.

| Tool | Input | Output (per item) | Registered |
| --- | --- | --- | --- |
| `readPage` | `sources` (1–50 web URLs), `refresh`, `size_only` | `source`, `kind`, `title`, `method`, `status`, `partial` (the named reasons), `cached`, `chars`, `path` (local only), `content` (omitted with `size_only`), `error` | always |
| `parseLocalDocuments` | `paths` (1–50 local paths), `size_only` | as `readPage`, `method` = `local` | local server only, never on the remote gateway |
| `download` | `sources` (1–50 URLs of any kind) | `source`, `kind`, `content_type`, `bytes`, `sha256`, `method`, `status`, then `path` on the local server or `resource` (a `resource_link`) on the remote one, `error` | always |
| `findWorks` | `query`, `limit`, `kind` (`any` default, `paper`, `book`) | ranked candidates: `handle`, `title`, `authors`, `year`, `kind`, `ids` (DOI, arXiv, PMID, PMCID, ISBN), `open_access` | always |
| `readWork` | `works` (1–20: DOI, arXiv id, PMID, PMCID, ISBN, a paper or book landing URL, or a `findWorks` handle), `refresh`, `size_only` | as `readPage`, plus `ids` and the route it took (repository, mirror, OA copy) | always |
| `webSearch` | `query`, `count`, `lang`, `engines` | results: `title`, `url`, `snippet`, `engine` | only when SearXNG or Brave is configured; absent otherwise, and the server instructions do not name it |

- Wrong-tool input is a named error that names the right tool: a DOI given to `readPage` answers "this is a DOI; read it with `readWork`", a local path given to `readPage` names `parseLocalDocuments`, a URL given to `parseLocalDocuments` names `readPage`. A landing URL of a paper is valid for both `readPage` (the page) and `readWork` (the work).
- `size_only` stays on `readPage`, `parseLocalDocuments` and `readWork`.
- The MCP prompt named `fetch` is removed.
- `RegisteredToolNames` (`toolnames.go`) changes in the same edit as the registration; the daemon's `/status` reads it.
- `service.go` (979 lines, baseline 1052) is split by tool family so no file crosses its ceiling: `tools_read.go`, `tools_download.go`, `tools_works.go`, `tools_search.go`, each with its `_test.go`.

## Remote: only MCP

- The remote gateway (`remote.go`) registers `readPage`, `download`, `findWorks`, `readWork` and `webSearch` (when configured); never `parseLocalDocuments`, and no tool returns a server path.
- `download` on the remote server returns a `resource_link` with URI `harvest://download/{id}` (`id` = the content's sha256), its MIME type and size. The server registers the resource template `harvest://download/{id}`; `resources/read` answers with the bytes as a blob. The template's handler reads only files the download store holds under that id; any other id is `ResourceNotFound`.
- A blob larger than `harvest.maxResourceBytes` (default 25 MiB) is not sent: the read answers a named error with the size and the reason (the MCP transport carries it base64 in one message). The `download` result states the same limit up front when the file is over it.
- Binary is never put in a tool result. Images are not inlined as `ImageContent`.
- The gateway needs no route change: it passes every JSON-RPC method on `/mcp` after the bearer check, so `resources/read` reaches the SDK's registered-resource lookup, which is the access gate.

## Removed, with every reference

- `archive`: the tool, `Harvester.Archive`, `archiveList`, the zip, tar, 7z and rar member readers (`archive.go`), their tests, `PublicArchiveListing`, and the texts that send a caller to it. A zip is a `download`.
- `searchCache`: the tool, `Cache.Search`, `Harvester.SearchCache`, `SearchCachePublic`, `searchPrivateCache`, `publicSearchRegexp` (unless another caller is found; the build task enumerates the callers first), their tests.
- `fetchImage`: the tool; `FetchImage` becomes the file path of `Retrieve` behind `download`.
- Every user-facing string that names an old tool is rewritten to the new names: the inventory lists about 20 in `harvest.go`, `net.go`, `public.go`, `known_id.go`, `harvest_diagnostics.go`, the corpus manifest's pinned errors, and `pfm/cmd/pfm/harvest_ask_command_test.go`.

## Callers, same pass

| Where | Change |
| --- | --- |
| `templates/global/agents/rr.md`, `sub-rr.md` (through `/pcm`) | tools lists and steps name `readPage`, `readWork`, `findWorks`, `webSearch`, `download`; `searchCache` gone |
| `templates/global/commands/pfm.md` | the six tools and the CLI verbs |
| `workflows/deep-rr/engine/src/**` | `fetch` → `readPage` or `readWork` by what the step reads; `search` → `webSearch`; then `npm run build` regenerates `workflow.js` and the snapshots are updated; `SKILL.md` preflight uses `readPage` |
| `docs/dev/pfm-surface.md` | the tool table and CLI rows |
| `docs/dev/testing/landscape.md` | M21–M29, H10, H11 rewritten; the dangling `mcp.md` citation fixed to the file that holds the tools |
| `infra/fence/lanes/M.sh`, `map.tsv`, `beats.md` | M50's served-tool list; M21–M28 per tool; H10, H11 and the chat-fetch check use `readPage` with `size_only` and the `cached` field as the cache oracle instead of `searchCache` |
| `infra/readme-cards/deck.html` | the demo card: `findWorks` → `readWork(handle)` |
| `docs/design/RR/rr.md` | the `searchCache` design notes removed |
| `pfm/internal/harvest/README.md`, `pfm/cmd/pfm/mcp_serve_test.go` | new names |
| `.professor/retro.md` | the open `searchCache` entries are stamped resolved by removal |

Releases and demo inventories are history and stay as written. The `pfm archive` chat command is a different subject and is untouched.

## CLI

- `pfm harvest <url|doi|isbn|path>...` stays the universal read (it routes a source to the page, work or local path the way the tools do); `--refresh`, `--size-only`, `--json` stay.
- New: `pfm harvest download <url>...` prints the path, kind, type and size.
- `cmd/pfm` has one line of budget: the harvest verbs move their bodies into a package (`internal/harvestcli`, with tests) and `cmd/pfm` keeps the dispatch only.

## Build tasks (one at a time, each verified and landed before the next)

1. **R1 central retrieval**: `retrieve.go`, the four policies, file mode with the cap and the binary guard; `FetchImage`, archive bytes, inline images, the gateway and the DOI mirror routed through it. Proof: fixture tests per policy and per rung failure; a live sim run of 10 targets (page, PDF, image, audio, a zip, a paywalled page, a DOI through a provider) identical in outcome to today except where the binary guard now names a file.
2. **R2 tools**: the six tools, typed output, the wrong-tool errors, the removals in Go, the string rewrites, the service split, the remote resource template and its cap. Proof: SDK round-trip tests per tool; the remote gateway serves `resources/read` for a downloaded file and refuses an unknown id; a local `stdio` session lists exactly the six (five without search).
3. **R3 docs, lanes, CLI**: everything in the callers table outside `templates/` and `workflows/`, plus the CLI. Proof: the M lane passes in the fence; `grep` over the tree finds no old tool name outside releases and demo inventories.
4. **R4 deep-rr**: engine sources, build, snapshots, `SKILL.md`. Proof: the engine's test suite passes.
5. **R5 templates** (main loop, through `/pcm`): `rr.md`, `sub-rr.md`, `pfm.md`.

## Open risks, named

- Whether Claude Code and Codex follow a `resource_link` into `resources/read` is outside the SDK; R2 tests the server side, and the final sweep tries it from a real client.
- Removing the archive member readers drops a working feature (reading a document inside a zip); the user ruled it: a zip is a file.
