# RR — Best library per legacy/odd Office format for the harvester's Python sidecar

Question: pick the best library (or tool) to convert each of these document formats to clean Markdown/text inside our harvester's Python sidecar, and return a ranked recommendation per format. Formats: legacy OLE2 Office — .doc, .xls, .ppt; Outlook .msg; RTF; OpenDocument .odt/.ods/.odp; Office macro/template variants (.docm/.xlsm/.pptm/.dotx/.xltx/.potx) — check whether markitdown 0.1.6 / python-docx / openpyxl / python-pptx already read these if the extension is mapped. (Scope change mid-run by user ruling: Outlook .msg dropped — download-only.)

**Answer.** Two pure-Python additions cover most of the list: **odfdo** (Apache-2.0, depends only on lxml) and **legacy-doc** (MIT). **xlrd** (BSD) covers .xls and **striprtf** (BSD-3) covers RTF. docling 2.107.0 already reads ODT/ODS/ODP, so ODF may need no new dependency at all. **.ppt has no acceptable library**: the only options are LibreOffice or catppt from the GPL catdoc suite. The macro and template variants can't go straight to their current readers. python-docx rejects .docm and .dotx by content type, and markitdown maps only .xlsx/.xls, so those files have to be routed or re-typed before parsing.

## Map by format

### .doc (Word 97-2003)
- **Recommended: legacy-doc.** It "does not use native binaries, LibreOffice, `antiword`, or third-party parsers". It "supports OLE Compound File Word 97-2003 `.doc` files". License MIT, 0.2.1 "Released: May 28, 2026". It "rejects encrypted, malformed, non-OLE, or unsafe files clearly". Output is plain text only, with no tables or headings. It is young, with a single maintainer (unverified). Its wheel coverage wasn't checked. [PyPI legacy-doc](https://pypi.org/project/legacy-doc/)
- **Runner-up:** docling's DOC path, which "requires LibreOffice" ([docling formats](https://docling-project.github.io/docling/usage/supported_formats/)). It keeps better structure, but means shipping a full LibreOffice install.
- **Rejected:** antiword and textract, which relies on antiword. Antiword is GPL and its "last stable release was version 0.37 from October 21, 2005" ([Wikipedia](https://en.wikipedia.org/wiki/Antiword), [textract #468](https://github.com/deanmalmgren/textract/issues/468)). wvWare is GPL ([Wikipedia wv](https://en.wikipedia.org/wiki/Wv_(software))). doc2txt bundles the antiword binary, so it is GPL as well (unquoted, [libraries.io](https://libraries.io/pypi/doc2txt)).
- markitdown doesn't read .doc: ".xls is supported, but Word and PowerPoint support looks to be .docx and .pptx, not legacy .doc and .ppt." ([discussion #1320](https://github.com/microsoft/markitdown/discussions/1320))

### .ppt (PowerPoint 97-2003): no acceptable library
- There's no pure-Python .ppt text reader, and python-pptx says ".ppt files from PowerPoint 2003 and earlier won't work" ([python-pptx docs](https://python-pptx.readthedocs.io/en/latest/user/presentations.html)).
- The remaining options:
  - catppt from catdoc: a GPL system binary, with an old symlink CVE (CAN-2003-0193) in the suite (unquoted) ([Debian catdoc](https://packages.debian.org/sid/catdoc)).
  - LibreOffice through docling.
  - Aspose: commercial, not evaluated (unverified).
- **Verdict:** treat .ppt as download-only, or accept LibreOffice as an optional external tool.

### .xls (Excel 97-2003)
- **Recommended: xlrd 2.0.2**, which markitdown 0.1.6 already wires in through the `markitdown[xls]` extra. Its XlsConverter accepts `application/vnd.ms-excel` ([source](https://raw.githubusercontent.com/microsoft/markitdown/main/packages/markitdown/src/markitdown/converters/_xlsx_converter.py)).
  - License "BSD License (BSD)", "Released: Jun 14, 2025".
  - "This library will no longer read anything other than .xls files."
  - It ships a pure wheel, `xlrd-2.0.2-py2.py3-none-any.whl`, so all four platforms are covered. [PyPI xlrd](https://pypi.org/project/xlrd/)
  - The output is Markdown tables.
  - Maintenance is effectively frozen (Snyk calls it possibly discontinued, unquoted). The format is frozen too, so that's acceptable.
- **Encryption:** use msoffcrypto-tool (MIT) to detect and decrypt, giving a clear error. Its legacy Excel support is "marked experimental" ([repo](https://github.com/nolze/msoffcrypto-tool)).
- **Runner-up:** docling with LibreOffice.

### RTF
- **Recommended: striprtf 0.0.33.** "Released: Aug 17, 2026", "License expression: BSD-3-Clause", a pure wheel `py3-none-any`. It converts "Rich Text Format (RTF) files to plain text files", so the result is plain text and tables and lists are lost. [PyPI striprtf](https://pypi.org/project/striprtf/)
- **Runner-up: pypandoc-binary 1.17**, released Mar 14, 2026.
  - It has wheels for macOS x86-64/ARM64 and Linux glibc/musl x86-64/ARM64. Each wheel is about 25-37 MB; the "Total release size: 234.7 MB" figure covers all 7 platform wheels. [PyPI pypandoc-binary](https://pypi.org/project/pypandoc-binary/)
  - The wrapper is MIT, but "Pandoc itself is available under the GPL2 license", which is a real redistribution concern.
  - Whether pandoc's RTF *reader* keeps tables and lists is unconfirmed (open).
- docling reads RTF, but "requires LibreOffice" ([docling formats](https://docling-project.github.io/docling/usage/supported_formats/)).
- RTFDE (LGPLv3) only unwraps HTML embedded in RTF; it was relevant to .msg, which is now dropped ([PyPI RTFDE](https://pypi.org/project/RTFDE/)). rtfparse wasn't evaluated (named gap).

### OpenDocument .odt/.ods/.odp
- **Recommended: docling 2.107.0, already pinned.** Its format list includes "ODT, ODS, ODP - OpenDocument Format for text documents, spreadsheets, and presentations" ([docling formats](https://docling-project.github.io/docling/usage/supported_formats/)). That means no new dependency, and docling's structured Markdown output. Its output quality on ODF wasn't tested.
- **Runner-up / lighter path: odfdo.**
  - "License: Apache License, Version 2.0"
  - "the only (non-development) dependency is `lxml`" (already present)
  - "The project is tested on Python 3.10 to 3.14 (Linux, Mac, Windows)."
  - Its "odfdo-markdown" utility converts "an ODF text or spreadsheet document to Markdown". That covers text and spreadsheets only, not .odp.
  - Its latest release date wasn't confirmed.
  - [GitHub odfdo](https://github.com/jdum/odfdo)
- **Rejected: odfpy.** It is multi-licensed Apache/GPL/LGPL, and its last release was 1.4.1 on January 18, 2020 (unquoted) ([PyPI odfpy](https://pypi.org/project/odfpy/)).
- **Rejected: pandoc.** It reads .odt only, not .ods/.odp (unquoted), and it's GPL.

### Macro/template variants (.docm/.xlsm/.pptm/.dotx/.xltx/.potx)
- **python-docx** rejects both by content type:
  - A .dotx raises "ValueError: file '.dotx' is not a Word file, content type is 'application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml'", with no workaround in the thread ([#1532](https://github.com/python-openxml/python-docx/issues/1532)).
  - A .docm fails the same way; that issue has been open since 2015 (unquoted) ([#212](https://github.com/python-openxml/python-docx/issues/212)).
  - A fix is to rewrite `[Content_Types].xml` in memory to the .docx main type before parsing, with the macro parts left inert (unverified; needs testing).
- **openpyxl** documents .xlsm/.xltx/.xltm and keeps VBA only as inert bytes: "If they are preserved they are still not editable" ([openpyxl tutorial](https://openpyxl.readthedocs.io/en/stable/tutorial.html)). Extension mapping alone should be enough (unquoted as to markitdown's routing).
- **python-pptx:** no documented rejection for .pptm/.potx, and no issues reporting one. It probably works through extension mapping (unverified; needs testing).
- **markitdown 0.1.6** maps only .xlsx and .xls in its Excel converter. Its docx and pptx converters' accepted MIME types weren't fetched (open).
- **docling** lists none of these variants.
- **Macros:** none of these Python libraries run macros. python-docx "does not execute macros" (unquoted, [#212](https://github.com/python-openxml/python-docx/issues/212)).
- **Verdict:** add the extensions to the mapping, and patch the content type for Word variants.

### Heavy external tools (all formats)
- **LibreOffice headless / unoserver:** LibreOffice is MPL-2.0 and unoserver is MIT. unoserver "needs to be installed by and run with the same Python installation that LibreOffice uses". It "does not try to restart LibreOffice if it crashes" ([unoserver](https://github.com/unoconv/unoserver)).
  - Security: CVE-2023-2255 let floating frames "fetch and display their linked document without prompt on loading the host document", fixed in "versions ≥7.4.7 and ≥7.5.3" ([advisory](https://www.libreoffice.org/about-us/security/advisories/cve-2023-2255/)). That external-reference fetch risk is exactly the concern with untrusted files.
  - **Verdict:** optional external tool at most, never bundled.
- **Apache Tika / tika-python:** Apache-2. It needs "Java 11+ installed on your system" ([tika-python](https://github.com/chrismattmann/tika-python)). Rejected for a self-contained runtime.

## Add-to-pyproject shortlist
1. `xlrd==2.0.2`: .xls, through markitdown's existing converter. BSD, pure wheel.
2. `legacy-doc==0.2.1`: .doc text. MIT, pure Python.
3. `striprtf==0.0.33`: RTF plain text. BSD-3, pure wheel.
4. `msoffcrypto-tool` (MIT): detects encrypted OLE/OOXML files and gives a clear error before parsing. Its wheel and deps weren't checked (it pulls `cryptography`, unverified).
5. ODF: none. Use docling first; add `odfdo` (Apache-2.0, lxml only) only if docling's ODF output falls short.
- The macro/template variants need code, not dependencies: extension mapping plus a Word content-type patch.
- .ppt: nothing acceptable; download-only.

## Coverage
- .doc: settled
- .ppt: settled (no acceptable library)
- .xls: settled
- RTF: partial (pandoc reader fidelity open)
- ODF: partial (docling ODF quality untested; odfdo latest release unknown)
- Macro/template variants: partial (markitdown docx/pptx MIME mapping and python-pptx .pptm unverified)
- Heavy tools (added): settled
- .msg: dropped by user ruling

Digging ran one round with 4 diggers; all 4 reported.

## Verification
I checked 22 facts on 6 pages: legacy-doc, docling formats, odfdo, xlrd, striprtf, python-docx #1532.
- Two came back **NOT ON PAGE**, so they are not claimed above:
  - how legacy-doc raises its encryption error (the page says only that it "rejects encrypted ... files clearly")
  - odfdo's latest release version and date
- Docling RTF support was corrected on verification: it requires LibreOffice.
- No UNCHECKED facts.

## Open rabbit holes
- Test docling 2.107.0 output quality on real .odt/.ods/.odp files.
- Check how legacy-doc performs on real-world .doc files, and which exception its encryption rejection raises.
- Does pandoc's RTF reader keep tables and lists? Resolving this decides striprtf vs pypandoc-binary.
- Read markitdown 0.1.6 `_docx_converter.py` / `_pptx_converter.py` for accepted MIME types and extensions.
- Does python-pptx open .pptm/.potx? Does the python-docx content-type patch work?
- msoffcrypto-tool: wheel coverage and `cryptography` footprint.
- CVE history for olefile and lxml under untrusted input.
- Aspose's commercial .ppt option wasn't evaluated. rtfparse wasn't evaluated.
