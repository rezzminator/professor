"""tracemap_html — the Tracer Loom and the swatch book as self-contained pages, written only on request:
`tracetree.py map RUN --html OUT` (write) and `tracetree.py book --html OUT` (write_book).

A page is its shell (loom.html, book.html) with its style (loom.css) and the data inlined as JSON — every `<` escaped, so no file text can close
the script — and its parts (loom_dc.html, the four views, book_view.html) spliced in; its <title> names the repo. It
loads nothing from the network: system font stacks, no remote script, style or font. When the data carries warnings, a
red block of those lines opens the page ahead of the views; its notes (a miscopied path the kit corrected) follow in a
quiet block. Python 3 standard library only.
"""
import html
import json
import os
import re

HERE = os.path.dirname(os.path.abspath(__file__))
RUNTIME = "loom_dc.html"
LOOM = ("loom.html", ("loom_wall.html", "loom_custody.html", "loom_hourglass.html", "loom_key.html"))
BOOK = ("book.html", ("book_view.html",))
PARTS = "<!--LOOM_PARTS-->"
WARNINGS = "<!--LOOM_WARNINGS-->"
STYLE = "/*LOOM_STYLE*/"


def read(name):
    with open(os.path.join(HERE, name), encoding="utf-8") as f:
        return f.read()


def blob(data):
    return (json.dumps(data, ensure_ascii=False)
            .replace("<", "\\u003c").replace("\u2028", "\\u2028").replace("\u2029", "\\u2029"))


def assemble(shell, views, marks):
    """The shell with each of its slots filled in one pass (what a slot receives is never scanned for another), the
    runtime and the views spliced in at PARTS; a shell missing a slot is an error."""
    page = read(shell)
    marks = dict(marks, **{PARTS: "".join(read(n) for n in (RUNTIME, *views)), STYLE: read("loom.css")})
    for mark in marks:
        if mark not in page:
            raise ValueError(f"{os.path.join(HERE, shell)} lacks its slot {mark}")
    return re.sub("|".join(re.escape(m) for m in marks), lambda m: marks[m.group(0)], page)


def ledger(lines, notes):
    block = ('<section id="warnings" role="alert" aria-label="Warnings">'
             + "".join(f"<p>{html.escape(l)}</p>" for l in lines) + "</section>") if lines else ""
    return block + (('<section id="kitnotes" aria-label="Kit notes">'
                     + "".join(f"<p>{html.escape(l)}</p>" for l in notes) + "</section>") if notes else "")


def put(page, out):
    with open(out, "w", encoding="utf-8") as f:
        f.write(page)


def write(model, out):
    """Write the Tracer Loom for a run's model (loom.model) to out."""
    shell, views = LOOM
    title = model.get("repo") or "Trace"
    put(assemble(shell, views, {"<title>Tracer Loom</title>": f"<title>{html.escape(title)} — Tracer Loom</title>",
                                WARNINGS: ledger(model.get("warnings") or [], model.get("notes") or []),
                                "/*LOOM_DATA*/null": blob(model)}), out)


def write_book(book, out):
    """Write the swatch book for {root, book, failed} to out."""
    shell, views = BOOK
    put(assemble(shell, views, {"/*BOOK_DATA*/null": blob(book)}), out)
