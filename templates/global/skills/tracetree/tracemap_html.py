"""tracemap_html — the trace map as one self-contained page, written only on request:
`tracetree.py map RUN --html OUT`. The page is tracemap.html beside this file with the run's data inlined as JSON (every
`<` escaped, so no file text can close the script) and its <title> naming the repo; it loads nothing from the network.
When the data carries warnings, a red block of those lines opens the page's ledger, ahead of the map. Python 3 standard
library only.
"""
import html
import json
import os

TEMPLATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "tracemap.html")
SLOT = "/*TRACEMAP_DATA*/null"
TITLE = "<title>Trace map</title>"
WARNINGS = "<!--TRACEMAP_WARNINGS-->"


def write(data, out):
    """Write the page for data (tracetree's map data plus page data) to out; a template missing a slot is an error."""
    with open(TEMPLATE, encoding="utf-8") as f:
        page = f.read()
    for mark in (SLOT, TITLE, WARNINGS):
        if mark not in page:
            raise ValueError(f"{TEMPLATE} lacks its slot {mark}")
    title = data.get("title") or os.path.basename(str(data.get("root", "")).rstrip("/")) or "Trace"
    lines = data.get("warnings") or []
    block = ('<section id="warnings" role="alert" aria-label="Warnings">'
             + "".join(f"<p>{html.escape(l)}</p>" for l in lines) + "</section>") if lines else ""
    page = page.replace(TITLE, f"<title>{html.escape(title)} trace map</title>", 1).replace(WARNINGS, block, 1)
    blob = (json.dumps(data, ensure_ascii=False)
            .replace("<", "\\u003c").replace("\u2028", "\\u2028").replace("\u2029", "\\u2029"))
    page = page.replace(SLOT, blob, 1)
    with open(out, "w", encoding="utf-8") as f:
        f.write(page)
