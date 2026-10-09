"""tracemap_html — the trace map as one self-contained page, written only on request:
`tracetree.py map RUN --html OUT`. The page is tracemap.html beside this file with the map's data inlined; it loads
nothing from the network. When the data carries warnings, a red banner of those lines opens the page. Python 3 standard library only.
"""
import html
import json
import os

TEMPLATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "tracemap.html")
SLOT = "/*TRACEMAP_DATA*/null"
BODY = "<body>"
BANNER = """<style>
:root { --wb: 0px; }
#warnings { position: fixed; top: 0; left: 0; right: 0; z-index: 20; max-height: 38vh; overflow: auto; box-sizing: border-box;
  padding: 10px 16px; background: #601410; color: #ffdad6; border-bottom: 3px solid #ff5449; font: 13px/1.5 ui-monospace, Menlo, Consolas, monospace; }
#warnings div:first-child { font-weight: 700; font-size: 14px; }
@media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) #warnings { background: #b3261e; color: #fff; border-bottom-color: #410002; } }
:root[data-theme="light"] #warnings { background: #b3261e; color: #fff; border-bottom-color: #410002; }
header, #threads, #panel { margin-top: var(--wb); }
</style>
<div id="warnings" role="alert">{lines}</div>
<script>(function () { var b = document.getElementById("warnings"); function f() { document.documentElement.style.setProperty("--wb", b.offsetHeight + "px"); } f(); addEventListener("resize", f); })();</script>
"""


def write(data, out):
    """Write the page for data (tracemap.data's shape) to out; a template without its data slot is an error."""
    with open(TEMPLATE, encoding="utf-8") as f:
        page = f.read()
    if SLOT not in page:
        raise ValueError(f"{TEMPLATE} lacks its data slot {SLOT}")
    blob = json.dumps(data, ensure_ascii=False).replace("</", "<\\/")
    page = page.replace(SLOT, blob, 1)
    if data.get("warnings"):
        if BODY not in page:
            raise ValueError(f"{TEMPLATE} lacks its {BODY} tag, so the warnings banner has no place")
        lines = "".join(f"<div>{html.escape(l)}</div>" for l in data["warnings"])
        page = page.replace(BODY, BODY + "\n" + BANNER.replace("{lines}", lines), 1)
    with open(out, "w", encoding="utf-8") as f:
        f.write(page)
