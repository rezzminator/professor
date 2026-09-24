# RR — Which OCR approach should the harvester's Python sidecar use for scanned PDFs?

Question: choose the OCR approach for scanned PDFs (and scanned pages inside other documents) in our harvester's Python sidecar, today OCR runs only on one scholarly path. Evaluate docling's OCR engines (EasyOCR, Tesseract/tesserocr, RapidOCR/ONNX, ocrmac, newer ones in 2.10x), model downloads, accuracy (English, CJK, Arabic, Hebrew, Cyrillic), CPU speed, memory, licenses, wheels on 4 platforms, system Tesseract; also ocrmypdf, PyMuPDF OCR, surya/marker, docTR, PaddleOCR; and how to detect pages that need OCR cheaply. Output a ranked recommendation with config, comparison table, first-run/offline implications, open rabbit holes.

## Answer

Keep OCR inside docling and set the engine explicitly to **RapidOCR on its default onnxruntime backend**. It installs with pip alone (no system binary), its code is Apache-2.0 and it covers Latin, CJK, Arabic and Cyrillic models. Before docling runs, do a cheap **per-page** check with PyMuPDF and send only the flagged pages to OCR. Cap time twice: docling's `document_timeout` plus a hard kill of the sidecar process, because open issues say the timeout does not always fire. Hebrew is the gap: no bundled engine was shown to handle it, so Hebrew needs an optional system Tesseract through docling's `tesseract` (CLI) engine. Evidence on speed and accuracy per engine is thin, so treat it as unverified.

## Ranked recommendation

1. **docling + `RapidOcrOptions` (onnxruntime), set explicitly.** Don't rely on the default `OcrAutoOptions`, which picks an engine from whatever is installed at runtime, so the result depends on the machine. Pick the RapidOCR language model per document (it runs one language per conversion). Pre-stage the models, because of the cache bug below.
2. **Optional fallback: `TesseractCliOcrOptions`, only when a system `tesseract` is on PATH.** Use it for Hebrew and for scripts RapidOCR lacks. Don't pin `tesserocr`: it needs libtesseract and leptonica on the machine.
3. **macOS only, optional: `OcrMacOptions` (Apple Vision).** Korean and Japanese were added in macOS 13. Arabic, Hebrew and Cyrillic support is unverified. Using it would make output differ by platform.
4. **Avoid:** EasyOCR (pulls in torch; torch 2.6+ has no Intel-Mac wheels; slowest on CPU in docling's report), surya/marker (model-weights license), PaddleOCR (paddlepaddle macOS arm64 issue), ocrmypdf (needs external binaries), docTR/OnnxTR (no Arabic model), Nemotron (Linux x86_64 + CUDA only).

**Concrete configuration**
- Trigger, per page (PyMuPDF prepass, cheap). A page needs OCR if any of these holds:
  - `get_text()` is empty or near-empty;
  - an image covers most of the page area;
  - more than 10% of characters are U+FFFD (pymupdf4llm's `chars_bad` rule);
  - a high share of `(cid:NN)` escapes, following docling issue #2963's ">50% CID" proposal.
  - Pages whose only text is invisible (render mode 3) already carry an OCR layer. Keep that layer unless it trips the garbage test.
- Then run docling on the flagged pages with full-page OCR mode, and on the rest with `do_ocr=False`.
- Caps:
  - `document_timeout` (docling suggests 90-120 s for production). Scale it with the number of flagged pages, e.g. N × a per-page budget. **The per-page budget is not settled by evidence**; docling's report shows about 3 s/page on x86 CPU for the whole pipeline.
  - A process-level wall-clock kill in the sidecar, because the timeout is disputed (below).
  - `ocr_batch_size` stays at 4 or lower for memory.

## Map

### Docling OCR engines (2.10x)
- Engines: EasyOCR, Nemotron OCR, Tesseract, Tesseract CLI, OcrMac, RapidOCR, OnnxTR (plugin). Tesseract "must be installed on your system"; Nemotron is "Supported only on Linux x86_64 with Python 3.12 and CUDA 13.x" ([install docs](https://docling-project.github.io/docling/getting_started/installation/)).
- The default is `ocr_options ... = OcrAutoOptions()`, which "probes the runtime environment at pipeline initialization and selects the best available OCR engine" ([pipeline_options.py](https://raw.githubusercontent.com/docling-project/docling/main/docling/datamodel/pipeline_options.py)). The order it probes engines in was not found (open).
- RapidOCR's default is `backend ... = "onnxruntime"`, and `ocr_batch_size ... = 4` (same source).
- "RapidOCR runs a **single** language per conversion", while EasyOCR takes several at once. Docling "never quietly substitutes a different recognizer" when an engine cannot serve the language ([OCR concepts](https://docling-project.github.io/docling/concepts/OCR/)).
- Mac Intel: "Newer PyTorch versions (2.6.0+) no longer provide wheels for Intel-based Macs." This affects EasyOCR ([install docs](https://docling-project.github.io/docling/getting_started/installation/)).
- tesserocr "Requires libtesseract (>=3.04) and libleptonica (>=1.71)" and is MIT-licensed ([tesserocr](https://github.com/sirfz/tesserocr)).

### Models, first run and offline
- `docling-tools models download rapidocr` puts the models in `~/.cache/docling`. But "At runtime with `DocumentConverter`, the `rapidocr.RapidOCR` construction is not using `~/.cache/docling`. Instead a fresh model is downloaded to site packages' `rapidocr/models/`". The issue is still **Open** ([docling #2500](https://github.com/docling-project/docling/issues/2500)). Consequence: the first OCR run needs the network unless the sidecar points RapidOCR at the staged model paths itself.
- EasyOCR models go under `~/.EasyOCR/model` and download automatically (unquoted).

### Accuracy and languages
- RapidOCR PP-OCRv5 has `arabic`, `cyrillic` and `korean` recognition models. **Hebrew is not listed.** The page states no license for the models ([RapidOCR model list](https://github.com/RapidAI/RapidOCRDocs/blob/main/docs/model_list.md)).
- "Docling currently does not provide out-of-the-box Cyrillic OCR support for several OCR backends: EasyOCR, Tesseract, RapidOCR". Such PDFs "are recognized poorly or fail completely unless users manually patch models and configurations". The issue is open ([docling #3433](https://github.com/docling-project/docling/issues/3433)).
- Apple Vision added Korean and Japanese at WWDC22 ([WWDC22 10024](https://developer.apple.com/videos/play/wwdc2022/10024/)). Arabic, Hebrew and Russian support is unverified.
- EasyOCR covers "80+ supported languages... Latin, Chinese, Arabic, Devanagari, Cyrillic". Hebrew is not named ([EasyOCR](https://github.com/JaidedAI/EasyOCR)).
- tessdata is licensed Apache-2.0 ([tessdata README](https://github.com/tesseract-ocr/tessdata/blob/main/README.md)).
- docTR: "We don't have any rrecognition model trained on the arabic vocab atm" ([doctr #1434](https://github.com/mindee/doctr/discussions/1434)).
- No independent head-to-head accuracy benchmark of these engines was found.

### Speed and memory (thin)
- "On average, processing a page took 481 ms on the L4 GPU, 3.1 s on the x86 CPU and 1.26 s on the M3 Max SoC"; "applying OCR is the most expensive operation." The same page puts EasyOCR at 13 s on the x86 CPU (per the verification read; that sentence was not quoted) ([Docling report v4](https://arxiv.org/html/2408.09869v4)). The v1 report said EasyOCR was "upwards of 30 seconds per page" on CPU ([v1](https://arxiv.org/html/2408.09869v1)).
- Disabling OCR "saves 60% of runtime on the x86 CPU and the M3 Max SoC" ([discussion #2729](https://github.com/docling-project/docling/discussions/2729)).
- Memory: a report says v2.60 "sky rockets" versus v2.16 on a 7,000-page PDF ([docling #2786](https://github.com/docling-project/docling/issues/2786)). No figures per engine were found.
- The Tesseract/RapidOCR/EasyOCR latency numbers seen in search snippets are unverified and not used here.

### Detection and time caps
- `document_timeout`: "When exceeded, the pipeline stops processing and returns partial results with PARTIAL_SUCCESS status" ([pipeline_options.py](https://raw.githubusercontent.com/docling-project/docling/main/docling/datamodel/pipeline_options.py)).
- **DISPUTED.** Issue #2610 says `StandardPdfPipeline._build_document` "doesn't respect `document_timeout`" since 2.60, and issue #2381 says the timeout does not stop docling-parse on a page with 40k XObjects (titles only, unquoted).
- `force_full_page_ocr` is now a property, true when `mode is OcrMode.FULL_PAGE` (same source).
- pymupdf4llm's page analysis:
  - It flags `"chars_bad": more than 10% of all characters are illegible (i.e. Replacement Unicode characters)`.
  - Its engine preference is rapidtess_api, then paddletess_api, rapidocr_api, paddleocr_api, tesseract_api.
  - With no engine installed, "no OCR will be performed at all". `force_ocr=True` raises an error; otherwise it warns ([pymupdf4llm OCR plugins](https://pymupdf.readthedocs.io/en/latest/pymupdf4llm/ocr-plugins.html)).
- PyMuPDF heuristics: "'ignore-text' - indicates hidden text (3 Tr), a strong indicator for OCRed text" ([PyMuPDF #1853](https://github.com/pymupdf/PyMuPDF/discussions/1853)).
- ocrmypdf `--redo-ocr` "cannot distinguish this type of OCR text from real text" when the OCR text is disguised as ordinary text ([ocrmypdf advanced](https://ocrmypdf.readthedocs.io/en/latest/advanced.html)).

### Alternatives outside docling
- ocrmypdf: "Changed in version 17.0.0: Ghostscript is no longer strictly required. OCRmyPDF can use pypdfium2 for rasterization", and "`--rasterizer auto` ... prefers pypdfium2 when available" ([intro](https://ocrmypdf.readthedocs.io/en/latest/introduction.html)). It still needs a Tesseract binary. Its MPL-2.0 license rests on [#600](https://github.com/ocrmypdf/OCRmyPDF/issues/600) (unquoted). Its default Tesseract timeout is 180 s per page ([advanced](https://ocrmypdf.readthedocs.io/en/latest/advanced.html)).
- PyMuPDF / pymupdf4llm: "PyMuPDF Layout is licensed under the GNU AGPL v3" ([blog](https://pymupdf.io/blog/open-source-all-the-way-down-pymupdf4llm-goes-fully-agpl)). The sidecar already carries that exposure. PyMuPDF's own OCR needs Tesseract plus `TESSDATA_PREFIX`.
- surya/marker weights: the free-use exception is lost if the entity "generated more than five million US Dollars ($5,000,000) in gross revenue in the prior year" or "raised more than five million US dollars ($5,000,000) in total equity or debt funding" ([surya MODEL_LICENSE](https://github.com/datalab-to/surya/blob/master/MODEL_LICENSE)). Surya's code is GPL-3.0 (unquoted). Excluded.
- PaddleOCR: "License: Apache License 2.0" ([PyPI](https://pypi.org/project/paddleocr/3.1.1/)). The paddlepaddle macOS arm64 problem rests on [Paddle #78542](https://github.com/PaddlePaddle/Paddle/issues/78542) (unquoted; the docs on M-series support contradict each other).

## Comparison table

| Engine | Install / system deps | License (code / weights) | Non-Latin | CPU speed | Verdict |
|---|---|---|---|---|---|
| RapidOCR (docling, onnxruntime) | pip only; models auto-download (cache bug [#2500](https://github.com/docling-project/docling/issues/2500)) | Apache-2.0 code (unquoted); weights license not stated on [model list](https://github.com/RapidAI/RapidOCRDocs/blob/main/docs/model_list.md) | CJK, Korean, Arabic, Cyrillic; no Hebrew; one language per run | no verified per-engine figure | **1st** |
| Tesseract CLI (docling) | system `tesseract` + tessdata | Apache-2.0 (tessdata) | 100+ languages incl. heb (unquoted); RTL quality unverified | "fast" (qualitative, unverified) | **2nd, optional fallback** |
| tesserocr | libtesseract + leptonica | MIT | same as Tesseract | same | avoid (build risk) |
| ocrmac | macOS only, pyobjc | MIT (unquoted) | Korean/Japanese since macOS 13; others unverified | unverified | optional on Mac |
| EasyOCR | torch (no Intel-Mac wheels for 2.6+) | Apache-2.0 (unquoted) | 80+ incl. Arabic, Cyrillic, CJK | 13 s/page x86 CPU (v4 report, verification read) / >30 s (v1) | avoid |
| Nemotron OCR | Linux x86_64 + CUDA 13 | not checked | not checked | GPU | out of scope |
| OnnxTR / docTR | pip | Apache-2.0 (unquoted) | no Arabic model | 0.12-0.17 s/page (unquoted) | avoid for multilingual |
| PaddleOCR 3.x | paddlepaddle (macOS arm64 issue, unquoted) | Apache-2.0 | 109 languages (unquoted) | unverified | avoid |
| surya / marker | pip, torch | GPL-3.0 code (surya) / RAIL-M weights with $5M cap | multilingual | unverified | excluded (license) |
| ocrmypdf | tesseract binary; Ghostscript optional since v17 | MPL-2.0 (unquoted) | via Tesseract | 180 s/page default cap | avoid (external binaries) |

## Coverage
- Docling engine options: settled (except the probe order of `auto`).
- Model downloads and offline behaviour: partial (RapidOCR cache bug confirmed; model sizes unverified).
- Licenses: partial (the RapidOCR/PP-OCR weights license was never read from a LICENSE file).
- Wheels on the 4 platforms: partial (onnxruntime Intel-Mac and Linux aarch64 Python wheels not confirmed).
- Accuracy per script: partial (no benchmark found; support lists only).
- CPU speed and memory: open (thin).
- Detection heuristics: settled.
- Time caps: partial (added sub-area; the timeout's reliability is disputed).

## Verification
23 facts checked on 8 pages.
- NOT ON PAGE (dropped):
  - `bitmap_area_threshold` default 0.05 (the field is not in current `pipeline_options.py`);
  - EasyOCR "upwards of 30 s" on the v4 page (v4 gives 13 s; the v1 figure is kept, attributed to v1);
  - the 216 dpi claim on v4;
  - ocrmypdf's MPL-2.0 license on its intro page (kept as unquoted via #600);
  - a Hebrew model on RapidOCR's list;
  - a fix or fix version for #2500 (the issue is still open).
- UNCHECKED: none.

## Open rabbit holes
- The engine probe order inside docling's `OcrAutoOptions` (the ocr factory source).
- How to point RapidOCR at a pre-staged model directory in 2.107 (the #2500 workaround, e.g. model path fields on RapidOcrOptions).
- The actual LICENSE for the RapidOCR/PP-OCR ONNX weights.
- PyPI `onnxruntime` wheels for cp311 on macOS x86_64 and Linux aarch64 in current releases.
- The bodies of docling #2610 and #2381: is `document_timeout` enforced in 2.107?
- Apple Vision's full supported-language list on macOS 14/15.
- Tesseract Hebrew/Arabic quality with tessdata_fast vs tessdata_best.
- A reproducible CPU per-page and memory benchmark of RapidOCR vs Tesseract vs ocrmac inside docling. None exists publicly; measure it in-house.
