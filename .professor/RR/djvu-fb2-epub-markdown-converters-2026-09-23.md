# RR — Best sidecar converters to Markdown for DjVu, FB2 and EPUB (scope narrowed)

Question: pick the best library (or tool) to convert each of these formats to clean Markdown/text inside our harvester's Python sidecar, and return a ranked recommendation per format. (Scope narrowed mid-run by user ruling: DjVu, .fb2 and the EPUB quality check only; .mobi/.azw3, .chm, .ps/.eps and iWork are now download-only.)

**Answer:** EPUB: keep what's already pinned. Docling is first choice and markitdown second; ebooklib isn't needed and is AGPL. FB2: PyMuPDF, which is already pinned, is the only option found, and nobody has tested its output quality. DjVu: no acceptable option. The only Python binding is GPL-2 and archived, and the djvulibre tools are GPL with a 2025 memory-corruption CVE. Treat DjVu as download-only, or shell out only in a sandbox if the licence can be accepted.

## EPUB
- **Recommended: docling (already pinned).** It lists "EPUB - Electronic Publication format for e-books" as an input format ([docling supported formats](https://docling-project.github.io/docling/usage/supported_formats/)). It reads the book in spine order through its HTML backend. Known flaw: "Anchor targets are not preserved", so footnote links render but don't resolve ([docling EPUB example](https://docling-project.github.io/docling/_generated/examples/epub_conversion/)) — unquoted-by-lead.
- **Runner-up: markitdown 0.1.6 (already pinned).** It opens the EPUB as a zip itself (zipfile + defusedxml), follows the OPF spine and converts each chapter with its HTML converter ([_epub_converter.py](https://raw.githubusercontent.com/microsoft/markitdown/main/packages/markitdown/src/markitdown/converters/_epub_converter.py)). The code shows no footnote handling and only basic metadata.
- **Not recommended: pymupdf4llm.** For e-books, "the full document is treated as one large page", so chapter structure is lost ([pymupdf4llm API](https://pymupdf.readthedocs.io/en/latest/pymupdf4llm/api.html)).
- **Rejected: ebooklib.** Its licence is "GNU Affero General Public License v3 or later (AGPLv3+)", latest release 0.20 on Oct 26, 2025 ([PyPI EbookLib](https://pypi.org/project/EbookLib/)). Neither docling nor markitdown needs it.
- pandoc can read EPUB, but the pandoc page the digger fetched covers only writing EPUB, so read quality is unverified.

## FB2
- **Recommended: PyMuPDF (already pinned through pymupdf4llm).** Its format list includes FB2 ([PyMuPDF about](https://pymupdf.readthedocs.io/en/latest/about.html); [opening files](https://pymupdf.readthedocs.io/en/latest/how-to-open-a-file.html)). No source documents how good the Markdown is. It will probably flatten the book the same way it does EPUB (an inference, not verified).
- Docling doesn't list FB2 (checked on its format page). No markitdown or pandoc FB2 route was confirmed.
- **Runner-up (not researched, unverified): a small lxml or defusedxml walker.** FB2 is a single XML file (`<section>`, `<title>`, `<p>`, `<emphasis>`), so a hand parser is cheap, permissive-licensed and has no binary dependency.
- Licence note: PyMuPDF is AGPL or commercial ([file2markdown](https://www.file2markdown.ai/blog/is-pymupdf-free-for-commercial-use)). You already ship it, so FB2 adds no new licence exposure.

## DjVu
- **No acceptable option.**
- python-djvulibre: "This repository was archived by the owner on Oct 3, 2022", "GPL-2.0 license" ([jwilk/python-djvulibre](https://github.com/jwilk/python-djvulibre)). No wheels for the four target platforms were found (unquoted).
- djvulibre CLI (djvutxt for the text layer, ddjvu to render pages for OCR): GPL and a system binary. It has had repeated parser memory bugs. GHSL-2025-055 is "an out-of-bounds write vulnerability which can cause memory corruption", "Tested Version: 3.5.28", fixed when "DjVuLibre version 3.5.29 released" on 2025-07-03 ([GitHub Security Lab](https://securitylab.github.com/advisories/GHSL-2025-055_DjVuLibre/)). CVE-2021-3500 is a stack overflow on a crafted file ([Debian tracker](https://security-tracker.debian.org/tracker/CVE-2021-3500)).
- PyMuPDF and docling don't open DjVu; neither lists it (checked).
- If DjVu is ever needed: run djvutxt ≥3.5.29 as a separate process in a sandbox. That keeps the GPL out of your code, since you'd only be shipping the binary alongside it, and contains the CVE risk. For books without a text layer, render with ddjvu, then OCR with docling's OCR path. This is a design suggestion, not researched.

## Add-to-pyproject shortlist
- Nothing new. EPUB is handled by docling, with markitdown as fallback. FB2 is handled by PyMuPDF, or later by a small in-house lxml parser. lxml is probably already there through docling or trafilatura (unverified).

## What PyMuPDF alone covers
- From this scope, only FB2 and EPUB, and EPUB with worse structure than docling. It does not cover DjVu. It also lists XPS, MOBI, CBZ and SVG.

## Coverage
- EPUB: settled.
- FB2: partial (PyMuPDF output quality untested).
- DjVu: settled (negative).
- mobi, chm, ps and iWork: dropped by user ruling. A digger had already returned data on them; it is not reported here.

## Verification
- Checked 9 facts on 4 pages, all confirmed:
  - docling lists EPUB, not FB2 and not DjVu.
  - python-djvulibre was archived on Oct 3, 2022 and is GPL-2.
  - GHSL-2025-055 is an out-of-bounds write; tested version 3.5.28; fixed in 3.5.29 on 2025-07-03.
  - ebooklib is AGPLv3+; latest version 0.20.
- NOT ON PAGE: none. UNCHECKED: none.

## Open rabbit holes
- Real FB2 → Markdown output from PyMuPDF, compared with a hand-written lxml parser.
- Docling vs markitdown on real EPUBs with footnotes and images.
- Whether docling's EPUB path drops the cover image and metadata.
- Pandoc's EPUB read quality.
- Whether a maintained python-djvulibre fork with wheels exists.
- Any permissively licensed DjVu text-layer parser.
