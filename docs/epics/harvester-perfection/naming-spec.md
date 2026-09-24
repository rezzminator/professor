# Harvester tool naming — spec (the user's rulings, this chat)

Worktree: `.worktrees/naming` (branch wave/naming, cut from develop at 14a306b1). Copy this file into the worktree as docs/epics/harvester-perfection/naming-spec.md in the first task, and update surface-spec.md § The six tools to point at it (the six-tool table is replaced, not kept beside).

## Goal
Four tools whose names say what they do, one reader that takes every kind of source in separate typed arrays, and one set of parameter and field names across the tools. No alias, no deprecated name, no reference to the old names outside releases/ and docs/demo/ (history).

## The tools

| New | Replaces | Registered |
| --- | --- | --- |
| `read` | `readPage`, `parseLocalDocuments`, `readWork` | always; on the remote gateway its schema has no `files` field |
| `download_file` | `download` | always |
| `search_literature` | `findWorks` | always |
| `search_web` | `webSearch` | only when SearXNG or Brave is configured |

### `read` — input (the user's ruling: separate fields, each an array, all in one call)
- `urls`: web URLs (http/https), each read as a page (a PDF or document URL is parsed).
- `files`: local paths or file:// URLs, parsed (local server only; the remote schema has no such field, and a remote call that sends one is refused by name).
- `publications`: DOI, arXiv id, PMID, PMCID, ISBN, a `search_literature` handle, or a paper/book landing URL — read as the full text through the scholarly path. A landing URL in `urls` reads the page; in `publications` it reads the work: that settles the old overlap with no mode switch.
- At least one item overall; at most 50 items in total, of which at most 20 publications (named errors on breach).
- Shared options: `refresh`, `include_content` (default true; false = read and cache, return no body — replaces `size_only`), `ocr_language` (latin, zh, ja, ar, ru, he — replaces `ocr_lang`), `headers` (applies to `urls` and to `publications`' landing origin only, as today; never to files).
- A misplaced item is a per-item named error naming the right field: an identifier in `urls` → "put it in publications"; a local path in `urls` → `files`; a URL in `files` → `urls`. The item fails, the rest of the call proceeds.
- All items run concurrently under today's per-item limits and concurrency.

### `read` — output
Structured content mirrors the input: `{ "urls": [item…], "files": [item…], "publications": [item…] }`, each array in the input's order, empty arrays omitted. Item fields: `source`, `kind`, `title`, `via` (replaces `method` and the work route), `status`, `gaps` (a list of named reasons — replaces the `partial` string; empty when complete), `cached`, `chars`, `tokens`, `path` (local server only), `content` (absent when include_content is false), `retry_after`, `error`; publications add `ids`. The text Content renders the three groups under headings in the same order.

### `download_file`
Input: `urls` (1–50), `headers`. Output: today's download item (absolute `path` locally; `id`, `url`, `expires`, `resource_link` remotely), with `method` renamed `via`.

### `search_literature`
Input: `query`, `limit` (default 8, max 25), `type` (`any`, `paper`, `book` — replaces `kind`). Output: `candidates` (field `kind` stays on a candidate: it is the candidate's type… rename it `type` too, for one word per concept) and `sources` as today.

### `search_web`
Input: `query`, `limit` (replaces `count`; default 8, max 20), `lang`, `engines`. Output as today.

## Everything else, same pass
- Go: registration, input/output structs, RegisteredToolNames, server instructions (name the four tools and when to use each), tool descriptions, every user-facing string and error that names a tool or a renamed field (harvest, harvestmcp, harvestcli, failure_text.go and the rest — grep), the corpus manifest's pinned texts, tests. The wrong-tool machinery shrinks to the per-item misplaced-field errors above; delete what no longer has a caller.
- CLI (internal/harvestcli): `pfm harvest <url|path|identifier>...` stays the universal read (it routes each argument into the right field); `pfm harvest download-file <url>...` replaces `download`; `pfm harvest search <query> [--type] [--limit]` is new for search_literature; flags `--include-content=false` replaces `--size-only` (keep the flag set minimal and consistent), `--ocr-language`, `--header`, `--refresh`, `--json`.
- deep-rr (workflows/deep-rr/**): engine prompts and code call `read` with the right field (identifiers → publications, web → urls), `search_literature`, `search_web`; `include_content: false` where it used size_only; the scheduler reads `gaps`/`via`; `npm run build` in the fence regenerates workflow.js; snapshots; SKILL.md preflight; design.md.
- Agent templates (templates/global/agents/rr.md, sub-rr.md, collector-rr.md, and templates/global/agents/variants.json where a variant lists harvester tools — heavy-rr, super-rr): frontmatter `tools:` lists become mcp__harvester__read, mcp__harvester__search_literature, mcp__harvester__search_web (and download_file where the agent downloads); bodies name the new tools and fields. These are shipped prompt code: read `.claude/commands/quality/prompt.md` first and edit surgically, no restructuring.
- templates/global/commands/pfm.md, docs/dev/pfm-surface.md, docs/dev/testing/landscape.md (harvester rows), docs/design/RR/{collector-rr,heavy-rr,sub-rr,super-rr}.md, infra/fence/lanes/{M.sh,beats.md,map.tsv}, infra/readme-cards/deck.html, pfm/internal/harvest/README.md, docs/epics/harvester-perfection/{surface-spec.md,manifest.md} (manifest: a line recording the rename and the user's ruling).
- Untouched history: releases/**, CHANGELOG.md, docs/demo/**, .professor/RR/**.

## Done when
- SDK round-trip tests: tools/list shows exactly the four (three without search); the remote `read` schema has no `files`; one `read` call with 2 publications + 4 urls + 1 file returns three groups in order with typed items; each misplaced-field case is a per-item named error while the rest succeed; the batch limits are named errors.
- `grep -rnE 'readPage|readWork|findWorks|parseLocalDocuments|webSearch|size_only|ocr_lang\b|"partial"|\bdownload\b tool'` over the worktree finds nothing outside releases/, CHANGELOG.md, docs/demo/, .professor/RR/ (show the command, its output and its exit status).
- Changed Go packages' tests in the fence, the harvestpy tests in the sim when the corpus manifest changes, the deep-rr engine suite in the fence; `./.claude/scripts/dev.sh iso verify pfm` "all steps passed"; all after the last edit, rc echoed inside the quoted command.
- Live in the sim over stdio: the 2 publications (10.1038/nature14539, arXiv:1706.03762) + 4 urls (example.com, a Wikipedia article, an arXiv PDF URL, a GitHub README) + 1 local file (a small PDF) in ONE read call — record per group each item's via, status, chars, gaps; then include_content false; then search_literature and download_file once each.
