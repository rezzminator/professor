# Epic: harvester-perfection

**Status:** IN_PROGRESS **Owner:** the main loop of the harvester session (god speed: resolve ambiguity, finish, report decisions at the end; stop only on failure). **Branch:** `wave/consent` in `.worktrees/consent`, fast-forwarded into `develop` item by item. Never pushed: publication waits for the user's in-turn ask.

## Goal

The harvester gets every target — page, document, paper, file — by every technique it has, loads everything the source serves (all comments, all pages, all reviews), and says by name whatever it could not load. No silent loss, no junk stored as success, no wall page stored as content.

## Rulings (the user's, binding)

- One item per brief, dispatched one at a time; every brief carries: "no flights-speccer, no flights-orchestrator, no gater — execute this yourself, start to finish; run only the affected tests in the fence, never the full suite."
- Merges to `develop` and host installs are authorized; push, tag and release are not.
- Only gitter writes git; never `git stash`; the user's uncommitted files in the main checkout are patch-preserved and their count checked before and after every fast-forward.
- No more reviewer agents on this wave.
- The browser opens headless first; a consent banner never blocks the page.
- Library choice is settled by measurement (bake-offs on real difficult documents), not by research alone; a converter for legacy Office formats is approved.

## Surface redesign (decided)

| Tool | Input | Output |
| --- | --- | --- |
| `readPage` | a web URL | Markdown; parses documents too (a PDF URL is parsed); typed result: kind, partial, method, status, path |
| `parseLocalDocuments` | local paths only | Markdown from the one document parser every source goes through |
| `download` | any URL (image, PDF, audio, video, zip, anything) | the local file path plus kind, content type and size — no parsing |
| `findWorks` | a title or bibliographic query | ranked candidates with a handle (papers and books merged) |
| `readWork` | DOI, arXiv, PMID/PMCID, ISBN, a paper or book landing URL, or a handle | full text through the scholarly path |
| `webSearch` | a query | results; registered only when a search backend is configured, never exposed when off |

- One central retrieval function: every tool that touches the network uses the full ladder (direct, Chrome impersonation, reader services, archive copies, the browser, consent and wall handling) for every kind, images and binaries included. Today `FetchImage` has its own two-rung ladder and the archive and scholarly paths their own.
- Remote callers: when the MCP server is exposed remotely, only MCP is exposed — never server files, never an HTTP file URL. `download` returns an MCP-native reference the client reads through the protocol; binary is never pushed into the model.
- `size_only` stays on the read tools (deep-rr budgets with it).
- Removed, with every reference: `archive` (a zip is a download), `searchCache`, `fetchImage` (folded into `download`).
- Every caller changes in the same pass: `rr` and `sub-rr` (and their `templates/global/` originals, through `/pcm`), deep-rr (workflow, prompts, config, snapshots), `templates/global/commands/pfm.md`, `docs/dev/pfm-surface.md`, the testing landscape rows, the server instructions, and the `pfm harvest` CLI verbs.
- Results are typed and declared with an output schema; today every tool returns plain text and discards its structured output.

## Formats

- Parsed (documents): HTML, PDF (with OCR for pages without a usable text layer), DOCX/XLSX/PPTX and their macro and template variants, legacy DOC and XLS, RTF, OpenDocument, EPUB, FB2, CSV and TSV, JSON, Jupyter notebooks, RSS/Atom/RDF feeds, WebVTT and SRT captions, MHTML, EML, Safari webarchive, JATS/NXML, BibTeX and RIS, single compressed documents (gz, bz2, xz, zst), config and code files fenced, plain text.
- Files only (download, never parsed): audio, video, images (HEIC included), fonts, executables, archives.
- Dropped to download-only with a named "not supported": CHM, Apple iWork, PostScript/EPS, Kindle MOBI/AZW3, Outlook MSG, legacy PPT (no acceptable library), DjVu (no acceptable library).
- The binary guard: a body is stored as text only when it is text; any other body is a file result or a named unsupported error — never raw bytes as content (today a `.wav`, an `.ogg` and a legacy `.doc` are stored as tens of thousands of binary characters marked successful).
- Cloud share links rewritten to their direct form: Dropbox, Box, Google Docs/Sheets/Slides export, Google Drive large-file confirm; SharePoint, OneDrive for Business, iCloud, MEGA and WeTransfer fail by name.
- OCR plan (research): docling with RapidOCR on onnxruntime set explicitly, a per-page prepass, a time cap plus a process kill, models staged at install; Hebrew needs an optional system Tesseract. Pending the bake-off.

## Bake-off winners (measured on real files in the sim)

- Feeds (RSS/Atom/RDF), JATS/NXML, FB2, Jupyter, WebVTT/SRT, RIS: hand-written parsers (lxml with entities off and no network, or stdlib json) — each tied or beat feedparser, docling, pubmed_parser, nbconvert, srt and rispy, at a fraction of the time; the JATS parser refuses entity bombs by name.
- MHTML: stdlib `email`. EPUB: markitdown (already pinned; ties docling on content, adds metadata, 6x less memory). BibTeX: bibtexparser 2.0.1, the only new dependency (1.4 aborts a whole file on one bad macro).
- Named follow-ups: JATS MathML flattens to linear text; feedparser as a fallback on a broken feed is unmeasured; RIS and BibTeX untested on messy exports.
- OCR: docling + RapidOCR (onnxruntime) is the default, one model per script (Latin, Chinese, Japanese, Arabic, Cyrillic; Cyrillic and Arabic need the PP-OCRv5 mobile models passed by hand); Hebrew goes through a system Tesseract when installed, otherwise "no Hebrew OCR" is named; EasyOCR rejected. A page is sent to OCR when it has no usable text layer or its text is over 2% control characters (fonts with no Unicode map); docling's document_timeout stops a long run and the process kill stays. First run stages 1.06 GB of layout and table models. Vertical CJK fails on every engine (named). Detail: the OCR bake-off report in the bench directory.
- Office: legacy DOC through legacy-doc (given the bytes; a size-ratio guard catches its silent truncation, and a named fallback keeps structure through a LibreOffice conversion where installed); XLS through xlrd; RTF through striprtf (CJK code pages right; tables lost, named); ODT through odfdo; ODP through docling; ODS needs a clamp on LibreOffice's million-row padding before any parser (docling and odfdo hang on it); macro and template variants through markitdown with mammoth, and a content-type rewrite for docling; encrypted files detected first with msoffcrypto-tool and named. New dependencies: legacy-doc, xlrd, striprtf, odfdo, msoffcrypto-tool, mammoth. Files are routed by their magic bytes, not their extension. Word files go through docling today, so the pinned environment's missing mammoth breaks nothing yet; it is needed before markitdown takes the macro and template variants.

## Landed

- Consent banners, 4xx never stored, wall markers, pagination and hash routes, images, the converter's block writer, paywall and login walls, stated gaps, the recall gate, hidden elements, the comments path.
- Site adapters: GitHub Discussions, Mastodon, Bluesky, GitLab, dev.to, Lemmy, Substack, YouTube, Steam, Slashdot, Reddit's loader cap, Notion, Product Hunt.
- Regression fixes from the second sweep: code blocks keep every line, inline code stays in place, share-word headings survive, Stack Exchange tag listings read from the API, `http_status` names the delivering rung, footnotes and article footer notes survive.
- Second wide sweep, 52 URLs: complete 23 → 30, gap named 2 → 14, silent loss 18 → 6, junk 4 → 0, silent failure 2 → 0.

## Queue (in order)

1. Land Product Hunt: `wave/consent` carries it rebased; verify, fast-forward `develop`, host install.
2. S10 IMDb reviews; S11 Quora answers (briefs written).
3. S12 Booking: the review list read page by page up to a bound, stated against loaded (today 30 of 1,389, silent).
4. G1 reader-rung checks: the wall, paywall and stated-gap checks run on reader-service output too (Bloomberg: 2 paragraphs of a paywalled article with no flag; Quora).
5. G2 stated against loaded on every rung, when the page states a count (Glassdoor: 1 of 103, silent).
6. G3 a redirect to a different page is named (a dead Booking hotel slug stored a city search page under the hotel's URL).
7. The surface redesign, built from `surface-spec.md` in five tasks (R1 central retrieval, R2 tools, R3 docs lanes CLI, R4 deep-rr, R5 templates). Moved ahead of the failure messages: R2 rewrites every error string that names an old tool, so F7 works on the new names once.
8. Failure messages (user ruling: hard-site testing is closed): every target the harvester cannot get ends in a meaningful, named error — what blocked it (challenge, login wall, paywall, rate limit, not found, unsupported format) and what the caller can do — never an empty or generic "retrieval failed".
9. Bake-offs: closed, winners recorded above.
10. Formats: the binary guard and file results, then the parsed formats with the bake-off winners, OCR, share links.
11. Final sweep (lanes A, B, C) and the report.

## Named gaps (known, not yet fixed)

- Reddit stops at its own rate limit (the remainder is named).
- Mastodon replies on other servers are not fetched; Bluesky and GitLab withhold some replies from signed-out readers.
- phpBB and Invision forum pages after the first are named, not followed.
- Tripadvisor answers with a challenge (a clear failure, correct).
- Amazon answered 404 to a product page; unproven whether it is a real 404 or a disguised refusal.
- Glassdoor served in Dutch for the fetching location; its page states "103 reviews" beside other companies' review counters and carries no countable review ids, so the stated-count check stays silent there (G2 rule: a noun whose labels disagree is not a stated count).
- Notion external-object mentions render as a placeholder.
- A reader-rung page whose HTML names no canonical address carries "a redirect to another page could not be ruled out" (the reader never reports where it landed); a shortlink that rewrites its path (`/q/123` to `/questions/123/slug`) on a site without an extractor is named as a different page.
