# RR — Stdlib, library or hand-written, per format and share-link shape, for the harvester sidecar

Question: Choose the approach (stdlib, small library, or hand-written) per format/link shape for the harvester (mht/mhtml, eml, ipynb, RSS/Atom/RDF, subtitles, tsv, single compressed files, JATS/NXML + .bib/.ris, cloud share links), ranked, with license, dependency, pitfalls, untrusted-input safety; sidecar is Python ==3.11 pinning docling 2.107.0, markitdown 0.1.6, pymupdf4llm, trafilatura 2.1.0, lxml.

**Answer.** Use the standard library or hand-written code for eight of the nine items. Use the libraries you already pin where they fit: docling for JATS, and markitdown only as a fallback. Only two new dependencies earn a place: **bibtexparser 2.0.1** (MIT, stable) for .bib, and **zstandard** (BSD-3) for .zst. rispy is optional. pysubs2 is out, because its current release needs Python 3.12 or newer and the sidecar is pinned to 3.11.

## Per item

**1. .mht/.mhtml.** Recommended: stdlib `email` (`policy.default`) to walk the multipart/related parts, then hand the HTML part to trafilatura. To handle inlined resources, map each part's Content-Location (and any `cid:`) to its part, and drop or keep binary parts as attachments. Runner-up: `pimht` (MIT, 0.6.0, Jul 2026) or MHTMLExtractor (MIT, stdlib-only). Neither documents how it resolves cid/Content-Location references ([pimht](https://pypi.org/project/pimht/), [MHTMLExtractor](https://github.com/AScriver/MHTMLExtractor), `unquoted`). Adds no dependency. MHTML is multipart/related MIME with Content-Location/Content-Base headers ([Wikipedia](https://en.wikipedia.org/wiki/MHTML), `unquoted`). markitdown does not list .mht ([markitdown formats](https://mintlify.wiki/microsoft/markitdown/formats/overview), `unquoted`); docling support was not found. Safety: cap the part count and the decoded size of each part, and never fetch Content-Location URLs over the network.

**2. .eml.** Recommended: stdlib `email.message_from_bytes(..., policy=policy.default)`, then `get_body(preferencelist=('html','plain'))` (HTML goes through trafilatura) and `iter_attachments()` to list filename, MIME type and size ([Python docs](https://docs.python.org/3/library/email.message.html)). Runner-up: none needed. markitdown supports only Outlook .msg (via olefile); .eml support is still an open issue ([markitdown#89](https://github.com/microsoft/markitdown/issues/89)). Adds no dependency. Charset pitfalls: unknown charsets reportedly fall back to ascii with surrogateescape and record a defect (`unquoted`). Wrap `get_content()` in a handler for LookupError/UnicodeDecodeError that falls back to `errors='replace'`, and decode RFC 2047 headers through the policy. Safety: cap nesting depth (message/rfc822 inside message/rfc822) and total size.

**3. Jupyter .ipynb.** Recommended: a hand-written walk over the notebook JSON with stdlib `json`. Emit markdown cells, code cells in fences, and outputs: `stream` text, `execute_result`/`display_data` (prefer text/markdown, then text/plain; replace images with a placeholder), and `error` (ename, evalue, traceback with ANSI codes stripped). markitdown 0.1.6's IpynbConverter emits only cell sources, no outputs. The PR that would render outputs (#2534) is unmerged ([PR #2534](https://github.com/microsoft/markitdown/pull/2534), `unquoted`). Runner-up: nbformat (BSD-3), which adds validation at the cost of more dependencies (jsonschema and others). nbconvert is too heavy. Adds no dependency. Safety: cap output size per cell and skip base64 image payloads.

**4. RSS/Atom/RDF.** Recommended: **feedparser 6.0.14** (Jul 30 2026, BSD-2-Clause, [PyPI](https://pypi.org/project/feedparser/), `unquoted`), which covers RSS 0.9x/1.0 (RDF)/2.0 and Atom. It earns its place only if RDF and messy real-world feeds matter. Otherwise the recommendation is hand-written parsing with lxml (`resolve_entities=False, no_network=True`), which is the runner-up. markitdown's RSS converter is not good enough on its own: it uses defusedxml minidom, handles only RSS/Atom (no RDF), and has a CDATA truncation bug ([issue #2461](https://github.com/microsoft/markitdown/issues/2461), `unquoted`). feedparser's XXE posture in 6.x is unverified; versions before 5.1.2 had an entity-expansion DoS ([GHSA-hjf3-r7gw-9rwg](https://github.com/advisories/GHSA-hjf3-r7gw-9rwg), `unquoted`). Adds a dependency (feedparser, plus its sgmllib3k dependency) only if chosen.

**5. Subtitles .vtt/.srt/.ass.** Recommended: hand-written parsing for SRT and WebVTT. Both are simple formats: split on blank lines, parse the timing line, strip tags, collapse duplicate lines. ASS is optional: parse the `[Events]` section's `Dialogue:` lines and strip `{\...}` override tags. Runner-up: `srt` (MIT, 3.5.3, Mar 2023, [PyPI](https://pypi.org/project/srt/), `unquoted`). Rejected libraries:
- pysubs2 1.9.0 (Aug 16 2026, MIT) "Python >=3.12" ([PyPI](https://pypi.org/project/pysubs2/)). It is incompatible with the 3.11 pin unless an older release is pinned.
- pysrt is GPLv3 and last released in 2020 ([PyPI](https://pypi.org/project/pysrt/), `unquoted`).
- webvtt-py 0.5.1 (May 2024, MIT) is stale (`unquoted`).

Adds no dependency.

**6. .tsv and other delimited text.** Recommended: choose the delimiter from the file extension (.tsv means a tab, .csv means a comma), then read with stdlib `csv` and render a Markdown table. Use `csv.Sniffer` only on a bounded sample and only for unknown extensions, restricted to a candidate delimiter list ([csv docs](https://docs.python.org/3/library/csv.html)). Sniffer is unreliable: about 84% success in one benchmark, and it can hang or backtrack on single-column input ([CSVsniffer](https://github.com/ws-garcia/CSVsniffer), `unquoted`). markitdown lists only CSV (`unquoted`). Runner-up: clevercsv (not researched). Adds no dependency. Safety: cap rows and columns, and raise `csv.field_size_limit` deliberately rather than without bound.

**7. Single compressed documents.** Recommended: stdlib `gzip`, `bz2` and `lzma` for .gz, .bz2 and .xz, with streaming decompression under a hard cap on output bytes and on the decompression ratio. `LZMADecompressor` and `BZ2Decompressor` document the needed parameter: "If max_length is nonnegative, returns at most max_length bytes of decompressed data" ([lzma](https://docs.python.org/3/library/lzma.html), [bz2](https://docs.python.org/3/library/bz2.html)). For gzip, use `zlib.decompressobj(wbits=31)` with `max_length`, or read in bounded chunks. `gzip.decompress` has no limit (`unquoted`). For .zst: stdlib `compression.zstd` is "Added in version 3.14" ([docs](https://docs.python.org/3.14/library/compression.zstd.html)), so on 3.11 add **zstandard 0.25.0** (BSD-3; cp311 wheels for macOS arm64 and manylinux aarch64, [PyPI](https://pypi.org/project/zstandard/)). Stream it through `stream_reader` with your own cap, because `read_size` only sets the input chunk size ([docs](https://python-zstandard.readthedocs.io/en/latest/decompressor.html)). Runner-up for .zst: pyzstd (BSD-3, `unquoted`). Pitfalls: multi-member gzip, and concatenated xz/zstd frames.

**8. Scholarly formats.**
- **JATS/NXML.** Recommended: docling's `JatsDocumentBackend` (already pinned). It builds its parser with `etree.XMLParser(resolve_entities=False, load_dtd=False, no_network=True, dtd_validation=False)`, detects JATS from the DTD system URL, and handles abstract, sec, table-wrap, fig, ref-list and citations ([jats_backend.py](https://raw.githubusercontent.com/docling-project/docling/main/docling/backend/xml/jats_backend.py), `unquoted`). Its scope is PMC-centric per [issue #893](https://github.com/DS4SD/docling/issues/893), `unquoted`. A security report is **DISPUTED**: SentinelOne's CVE-2026-31247 claims the backend expands entities, while the fetched source sets `resolve_entities=False`. Runner-up: hand-written lxml using the same parser flags. Also rejected: pubmed_parser (MIT, last release 0.5.1 in Aug 2024, [libraries.io](https://libraries.io/pypi/pubmed-parser), `unquoted`). No general jats2md tool exists. lxml 5.0 changed the default: "The new default is resolve_entities='internal'" ([lxml 5.0 changes](https://lxml.de/5.0/changes-5.0.0.html), `unquoted`). Pass `False` explicitly anyway.
- **.bib.** Recommended: **bibtexparser 2.0.1**, released Sep 10 2026, "Python >=3.10", MIT license ([PyPI](https://pypi.org/project/bibtexparser/)). v1 is in maintenance mode ([README](https://github.com/sciunto-org/python-bibtexparser/blob/main/README.md), `unquoted`). Runner-up: hand-written parsing, which is fragile because of nested braces, @string macros and LaTeX escapes. Adds a dependency.
- **.ris.** Recommended: hand-written parsing. RIS is tagged lines (`TY  - ` through `ER  - `) and is trivial to parse. Runner-up: rispy 0.10.0 (MIT, May 2025, [PyPI](https://pypi.org/project/rispy/), `unquoted`). Its own docs say "there is not one single source of RIS truth". No evidence was found that docling or markitdown read .bib or .ris (unverified).

**9. Cloud share links.** Recommended: a hand-written URL rewriter (no dependency), ranked by feasibility:
- **Dropbox** works anonymously with no JS: "you can use 'dl=1' as a query parameter" and "use 'raw=1'" ([Dropbox Help](https://help.dropbox.com/share/force-download)). Keep `rlkey` on `/scl/fi/` links.
- **Box** works anonymously when downloads are allowed. Use the `/shared/static/` form; the Box API's `download_url` field is "A URL that can be used to download the file" ([Box API](https://developer.box.com/reference/put-files-id--add-shared-link), `unquoted`).
- **Google Docs/Sheets/Slides** need a public "anyone with link" share, and then no JS. Use `/export?format=pdf|docx|txt|md` (Docs, [youneedawiki](https://youneedawiki.com/blog/posts/google-doc-url-parameters.html), `unquoted`), `/export?format=csv&gid=` (Sheets, first sheet only by default; `gid` is unofficial) and pptx (Slides). "Exported content is limited to 10 MB" ([Drive guide](https://developers.google.com/drive/api/guides/manage-downloads), `unquoted`).
- **Google Drive files**: `drive.usercontent.google.com/download?id=…&export=download&confirm=t` gets past the large-file virus-scan page. This behaviour is community-documented only (`unquoted`).
- **OneDrive personal**: the u! share token is built as "use base64 encode the URL… Append u! to be beginning of the string" ([Graph shares-get](https://learn.microsoft.com/en-us/onedrive/developer/rest-api/api/shares_get?view=odsp-graph-online)). Anonymous redemption through the consumer endpoint is reportedly flaky (`unquoted`). SharePoint and OneDrive for Business: "the Shares API always requires authentication and can't be used to access anonymously shared content without a user context" (same page). Try appending `download=1` to SharePoint links (unverified). Otherwise treat them as needing login.
- **iCloud Drive** is not feasible. The link opens an interstitial download page with no documented direct form ([Apple](https://support.apple.com/guide/icloud/share-files-and-folders-mm708256356b/icloud), `unquoted`).
- **MEGA** is not feasible without reimplementing its client-side AES decryption; the key sits in the URL fragment (`unquoted`).
- **WeTransfer** is not feasible. Its API is upload-only, and downloads go through a JS-driven flow or an unofficial POST endpoint (`unquoted`).

## New dependencies
1. zstandard 0.25.0 (BSD-3) for .zst.
2. bibtexparser 2.0.1 (MIT) for .bib.
3. Optional: feedparser 6.0.14 (BSD-2) if RDF and messy feeds matter.
4. Optional: rispy 0.10.0 (MIT) instead of a hand-written .ris parser.

None are GPL. pysrt (GPLv3) is rejected, and pysubs2 is rejected because it needs Python 3.12 or newer.

## Coverage
- 1 mht: partial (cid rewrite not sourced).
- 2 eml: settled (charset fallback `unquoted`).
- 3 ipynb: settled.
- 4 feeds: partial (feedparser XXE posture unverified).
- 5 subtitles: settled.
- 6 tsv: settled.
- 7 compression: settled.
- 8 scholarly: partial (CVE dispute).
- 9 links: partial (Google gid/confirm, OneDrive consumer and iCloud are community-sourced).

## Verification
10 facts checked on 4 pages, all confirmed:
- pysubs2: version 1.9.0, release date, Python >=3.12, MIT license.
- Microsoft Graph: the u! encoding, and authentication required for SharePoint.
- bibtexparser: version 2.0.1 stable, Python >=3.10, MIT license.
- Dropbox: dl=1 and raw=1.

None were NOT ON PAGE and none were UNCHECKED.

## Open rabbit holes
- CVE-2026-31247 compared with docling's actual parser flags; whether `huge_tree` or deep nesting is the real DoS.
- feedparser 6.0.x entity handling (its security docs page returned 404).
- Which pysubs2 release was the last to support Python 3.11.
- An official source for Google's `gid=` export parameter and the `confirm=t` behaviour.
- The state of OneDrive's api.onedrive.com consumer endpoint in 2026, and whether SharePoint `download=1` works anonymously.
- Rewriting MHTML cid/Content-Location references (RFC 2557).
- Whether markitdown PR #2534 (notebook outputs) merges.
