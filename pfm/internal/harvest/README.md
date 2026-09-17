# Harvester

Harvester reads `harvester.config.json` beside `pfm.config.json`. CLI fetches and both MCP gateways use the same retrieval core. `pfm config show` is the operator view of effective settings. Long-running servers load settings at startup and need a restart after configuration changes.

## Scholarly providers

Open-access resolution runs first.

| Configuration | Retrieval and discovery |
| ---------------------------- | -------------------------------------------------------------------------------------- |
| `scholarly.contactEmail` | Enables the existing Unpaywall DOI API with the operator's real contact email. |
| `scholarly.googleScholarURL` | Google Scholar discovery, including linked document copies. |
| No extra setting | PubMed Central resolution through NCBI ID conversion, OA PDF links, and Europe PMC. |

Provider URLs are absolute HTTP(S) base URLs without credentials, query, or fragment. Public-network checks cover requests, redirects, and discovered file hosts. TLS verification remains enabled. HTML parsing and downloads have size and time limits; MD5-addressed copies are checked against their record digest.

Mirror availability and document coverage vary. A successful homepage response is insufficient: the selected record, download host, and conversion must all work. HTTP errors, malformed responses, challenges, and conversion failures remain failures. Interactive CAPTCHAs are not solved. A missing Unpaywall contact email disables its requests; no substitute identity is generated.

For an exact DOI, ISBN, PMID, or PMCID, call `fetch`. For a title, call `findWorks`, choose a result, and pass its fetch value unchanged to `fetch`. This preserves the selected work or version instead of guessing from an ambiguous title.

## Public results

CLI, MCP tools, the fetch prompt, and `harvest ask` return exported artifacts. Retrieval methods, mirror URLs, fallback traces, and internal cache filenames are kept out of those results. A direct discovered download URL becomes a persistent opaque `harvest:` handle. Bibliographic identity and article citations remain in the document.

Complete exported Markdown and binary artifacts live under `<cache>/public/` with hashed filenames and private filesystem permissions. Inline limits do not truncate the saved document. Cache search exports matching documents before returning a path or fetch handle. Internal cache entries, handle mappings, and telemetry cannot be fetched through public Harvester calls, including through symlinks. The authenticated external gateway confines local reads to exported artifacts.

Detailed retrieval diagnostics stay in the internal cache and process logs. Public errors distinguish failed retrieval, timeout, access refusal, challenge, conversion failure, and storage failure without including provider addresses. These output controls do not remove publisher attribution or citations contained in the original document, or replace operating-system access controls on logs.

## Conversion

The pinned local Python worker uses PyMuPDF4LLM for PDF, Trafilatura for HTML, Docling for DOCX/XLSX/PPTX, and Microsoft MarkItDown for EPUB/CSV. JSON uses the standard library; plain text passes through. Empty scanned PDFs can escalate to OCR. No LLM API is part of this conversion chain.

MarkItDown is pinned at 0.1.6. Its base installation supports HTML as well as the existing EPUB/CSV paths. PDF and DOCX require additional optional dependencies; its PDF converter extracts a text layer and does not replace OCR. Office/PDF fallbacks should declare their extras explicitly before depending on them.

## Implementation references

Harvester adapts the request protocols into its Go transport; it does not launch these projects or install their browser extensions.

| Provider | Inspected implementation |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unpaywall | [ourresearch/unpaywall-extension](https://github.com/ourresearch/unpaywall-extension), [ourresearch/oadoi](https://github.com/ourresearch/oadoi), [unpywall/unpywall](https://github.com/unpywall/unpywall) |
| Google Scholar | [scholarly-python-package/scholarly](https://github.com/scholarly-python-package/scholarly) |
| PubMed Central | [Bio.Entrez](https://github.com/biopython/biopython/tree/master/Bio/Entrez) |
| MarkItDown | [Microsoft MarkItDown 0.1.6](https://github.com/microsoft/markitdown/tree/v0.1.6) |
