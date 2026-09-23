#!/usr/bin/env python3
"""Pinned Harvester conversion worker.

The worker is intentionally a small JSON-lines process.  Go owns process
lifetime and filesystem policy; this file owns the exact Python conversion
implementation.  No network operation is performed here.
"""

from __future__ import annotations

import contextlib
import json
import os
import pathlib
import sys


def _quiet_stdout():
    return contextlib.redirect_stdout(sys.stderr)


def _clean(value: object) -> str:
    if not value:
        return ""
    if isinstance(value, (list, tuple)):
        value = ", ".join(str(x) for x in value if x)
    return " ".join(str(value).split()).strip()


_JUNK_AUTHORS = frozenset(
    {
        "username", "user name", "user", "login", "log in", "sign in",
        "signin", "sign up", "signup", "register", "log out", "logout",
        "admin", "administrator", "guest", "search", "menu", "subscribe",
        "newsletter",
    }
)


def html_metadata(raw: str) -> str:
    from trafilatura.metadata import extract_metadata

    try:
        meta = extract_metadata(raw)
    except Exception as exc:
        print(f"metadata extraction failed: {exc}", file=sys.stderr)
        return ""
    if meta is None:
        return ""
    author = _clean(getattr(meta, "author", ""))
    if author.casefold() in _JUNK_AUTHORS:
        author = ""
    fields = (
        ("Title", _clean(getattr(meta, "title", ""))),
        ("Authors", author),
        ("Published", _clean(getattr(meta, "date", ""))),
        ("Source", _clean(getattr(meta, "sitename", ""))),
        ("License", _clean(getattr(meta, "license", ""))),
    )
    lines = [f"**{label}:** {value}" for label, value in fields if value]
    return "\n".join(lines) + "\n\n---\n\n" if lines else ""


def convert_html(path: pathlib.Path) -> str:
    import trafilatura
    from trafilatura.utils import load_html

    _keep_linked_blocks()
    _extract_comments_as_content()
    _keep_share_word_headings()
    # Local HTML follows the old dispatch path, which decodes malformed bytes
    # with errors ignored before trafilatura sees the document.
    raw = path.read_bytes().decode("utf-8", errors="ignore")
    tree = load_html(raw)
    if tree is not None:
        _drop_hidden(tree)
        _unwrap_layout_tables(tree)
        _flatten_code_lines(tree)
        _phrasing_paragraphs_as_text(tree)
        _inline_code_as_text(tree)
    with _quiet_stdout():
        document = trafilatura.bare_extraction(
            tree if tree is not None else raw,
            favor_recall=True,
            include_formatting=True,
            include_links=True,
            include_images=True,
            with_metadata=False,
        )
    body = ""
    if document is not None:
        part = document.get if isinstance(document, dict) else lambda name: getattr(document, name, None)
        blocks = [_render_blocks(part("body")), _render_blocks(part("commentsbody"))]
        body = "\n\n".join(block for block in blocks if block)
    return tidy_markdown(html_metadata(raw) + body)


# trafilatura prunes the main-content subtree it selected by link density:
# a div, list, paragraph or table whose text is mostly links is deleted as
# boilerplate. Inside the main content that is the content itself — an awesome
# list, a See also list, a wikitable of linked names, a GitHub heading wrapper
# whose only link is a textless permalink anchor, a "See https://react.dev/"
# paragraph, a comment that is one prose link ("here is the repo for the
# replication of the issue"). Site navigation is dropped before this point
# (tree cleaning and the discard XPaths), so no block inside the main content
# keeps the link-density test.
_LINKED_BLOCKS_KEPT = False


def _keep_linked_blocks() -> None:
    global _LINKED_BLOCKS_KEPT
    if _LINKED_BLOCKS_KEPT:
        return
    from lxml.etree import XPath
    from trafilatura import main_extractor
    from trafilatura.xpaths import regexpNS

    prune = main_extractor.delete_by_link_density

    def delete_by_link_density(tree, tagname, backtracking=False, favor_precision=False):
        if tagname in ("div", "list", "p"):
            return tree
        return prune(tree, tagname, backtracking=backtracking, favor_precision=favor_precision)

    main_extractor.delete_by_link_density = delete_by_link_density
    main_extractor.link_density_test_tables = lambda element: False
    # The discard pattern's "next-" (a next-post link) is a substring test over
    # the whole class attribute, so it also deletes a block whose class token
    # merely contains it mid-word — GitHub's every nested reply
    # ("discussion-primer-next-nested-comment-timeline-item"). Only a class
    # token that starts with "next-" names the link.
    discard = main_extractor.OVERALL_DISCARD_XPATH
    pattern = discard[0].path
    if "|next-|" not in pattern:
        raise RuntimeError("trafilatura's discard pattern no longer holds '|next-|'; re-check the class anchor")
    discard[0] = XPath(pattern.replace("|next-|", r"|(?:^|\s)next-|"), namespaces={"re": regexpNS})
    _LINKED_BLOCKS_KEPT = True


# trafilatura's comment handler flattens the comments section it found: it
# strips every link before extraction and appends every descendant block on its
# own, so a <code> inside a comment's paragraph is moved out of it (a fenced
# block mid-sentence), and its discard list drops a <pre>. The section below is extracted by
# the main-content element handlers instead — links, inline code and code
# blocks as in the article — and still leaves the main tree, as before, so it is
# written once, after the article. Turning the comment handler off does not
# keep the comments: the main-content pass selects the article, and a comments
# section beside it is never reached.
_COMMENTS_AS_CONTENT = False


def _extract_comments_as_content() -> None:
    global _COMMENTS_AS_CONTENT
    if _COMMENTS_AS_CONTENT:
        return
    from lxml.etree import Element, strip_tags
    from trafilatura import core, main_extractor

    if getattr(core, "extract_comments", None) is not main_extractor.extract_comments:
        raise RuntimeError("trafilatura's core no longer calls main_extractor.extract_comments; re-check the comment hook")
    handle = main_extractor.handle_textelem
    prune = main_extractor.prune_unwanted_nodes
    catalog = main_extractor.TAG_CATALOG
    is_code = main_extractor.is_code_block_element

    def extract_comments(tree, options):
        comments_body = Element("body")
        potential_tags = set(catalog)
        if options.tables:
            potential_tags.update(["table", "td", "th", "tr"])
        if options.images:
            potential_tags.add("graphic")
        if options.links:
            potential_tags.add("ref")
        for expr in main_extractor.COMMENTS_XPATH:
            subtree = next((s for s in expr(tree) if s is not None), None)
            if subtree is None:
                continue
            # The comment discard list drops every quote (a quoted reply), and
            # a <pre> is a quote by then: a code block is renamed out of reach.
            for quote in subtree.iter("quote"):
                if is_code(quote):
                    quote.tag = "code"
            subtree = prune(subtree, main_extractor.COMMENTS_DISCARD_XPATH)
            strip_tags(subtree, "span")
            if "ref" not in potential_tags:
                strip_tags(subtree, "a", "ref")
            comments_body.extend(
                element for element in (handle(e, potential_tags, options) for e in subtree.xpath(".//*")) if element is not None
            )
            if len(comments_body) > 0:
                main_extractor.delete_element(subtree, keep_tail=False)
                break
        text = " ".join(comments_body.itertext()).strip()
        return comments_body, text, len(text), tree

    core.extract_comments = extract_comments
    _COMMENTS_AS_CONTENT = True


# An element the reader never sees is never written. trafilatura's own discard
# pattern already drops aria-hidden="true" and an inline display:none or
# visibility:hidden, and its tree cleaning drops <noscript>; it strips a
# <template> tag but keeps the inert markup inside, and the HTML `hidden`
# attribute is in neither — GitHub's "Uh oh! There was an error while loading"
# box beside every comment. hidden="until-found" is text a find-in-page
# reveals: kept, and so is a tab set's inactive panel (role="tabpanel" — a
# docs page's TypeScript beside its JavaScript), one click away.
def _drop_hidden(tree) -> None:
    for element in tree.xpath("//template|//*[@hidden]"):
        if element.tag != "template" and element.get("role") == "tabpanel":
            continue
        if element.tag == "template" or (element.get("hidden") or "").strip().casefold() != "until-found":
            element.drop_tree()


# A table whose role is presentation or none is layout by its author's own
# declaration (WAI-ARIA), not data: GitHub wraps every comment body in one.
# trafilatura's cell handler keeps a cell's paragraph only up to its first
# inline child, so each body was cut at its first link and written as a
# one-cell table. Its rows and cells become plain blocks; a data table nested
# inside a cell stays a table.
def _unwrap_layout_tables(tree) -> None:
    for table in tree.xpath('//table[@role="presentation" or @role="none"]'):
        rows = table.xpath("./tr|./thead/tr|./tbody/tr|./tfoot/tr")
        for element in [table, *table.xpath("./thead|./tbody|./tfoot"), *rows]:
            element.tag = "div"
        for row in rows:
            for cell in row.xpath("./td|./th"):
                cell.tag = "div"


# trafilatura drops any text node that reads as a share button — "Email",
# "Print", "PDF", "Twitter" alone on its line. A heading with that text is a
# section title (an awesome list's "Email" category), not a button: kept.
_SHARE_WORD_HEADINGS_KEPT = False


def _keep_share_word_headings() -> None:
    global _SHARE_WORD_HEADINGS_KEPT
    if _SHARE_WORD_HEADINGS_KEPT:
        return
    from trafilatura import htmlprocessing

    if not callable(getattr(htmlprocessing, "textfilter", None)):
        raise RuntimeError("trafilatura's htmlprocessing no longer calls textfilter; re-check the heading hook")
    textfilter = htmlprocessing.textfilter

    def keep_headings(element) -> bool:
        if element.tag == "head" and element.text and not element.text.isspace():
            return False
        return textfilter(element)

    htmlprocessing.textfilter = keep_headings
    _SHARE_WORD_HEADINGS_KEPT = True


# A line-per-element highlighter writes each code line as its own element
# inside the <pre>: Prism (Docusaurus) a <div class="token-line"> ending in
# <br>, Expressive Code a <div class="ec-line">, highlight.js line numbers a
# table row. trafilatura keeps only the first such line, so a <pre> holding a
# block element is reduced to its rendered text first — a <br> and the end of
# each line element are a newline, all other whitespace kept — inside one
# <code> carrying the original's attributes.
_CODE_LINE_TAGS = frozenset({"div", "p", "li", "tr", "table", "tbody", "thead", "ol", "ul"})


def _flatten_code_lines(tree) -> None:
    for pre in list(tree.iter("pre")):
        if not any(element.tag in _CODE_LINE_TAGS for element in pre.iterdescendants()):
            continue
        code = pre.find(".//code")
        parts: list[str] = []
        _pre_lines(pre, parts)
        text = "".join(parts).strip("\n")
        for child in list(pre):
            pre.remove(child)
        pre.text = None
        flat = pre.makeelement("code", dict(code.attrib) if code is not None else {})
        flat.text = text
        pre.append(flat)


def _pre_lines(element, parts: list[str]) -> None:
    parts.append(element.text or "")
    for child in element:
        if isinstance(child.tag, str):
            if child.tag == "br":
                parts.append("\n")
            else:
                _pre_lines(child, parts)
                if child.tag in _CODE_LINE_TAGS and not next((part for part in reversed(parts) if part), "\n").endswith("\n"):
                    parts.append("\n")
        parts.append(child.tail or "")


# One-line inline code (MDN's <a><code>slice()</code></a>, Sphinx's
# <a><code><span>-E</span></code></a>, a <code> in a paragraph whose link
# holds an <em>) is lifted out by trafilatura: the paragraph splits there, the
# code lands after it as its own fenced block and the link takes the following
# words as its text. Code outside a <pre> on one line is written as its
# markdown text in place, so it stays inline at its place in the sentence.
# Texinfo's <samp>, <kbd> and <tt> are the same literal: left to trafilatura, a
# <var> inside one nests a second code span and the paragraph's words after it
# can vanish. An exponent (<sup>) inside a literal keeps a visible "^".
def _inline_code_as_text(tree) -> None:
    for code in list(tree.iter(*_CODE_PHRASING)):
        ancestors = [ancestor.tag for ancestor in code.iterancestors()]
        if code.getparent() is None or "pre" in ancestors or _CODE_PHRASING.intersection(ancestors):
            continue
        text = _literal_text(code)
        if "\n" in text.strip():
            continue
        text = " ".join(text.split())
        if text:
            _replace_with_text(code, _code_literal(text))


def _literal_text(element) -> str:
    parts = [element.text or ""]
    for child in element:
        if isinstance(child.tag, str):
            inner = _literal_text(child)
            if child.tag == "sup" and inner.strip():
                power = " ".join(inner.split())
                inner = f"^({power})" if " " in power else f"^{power}"
            parts.append(inner)
        parts.append(child.tail or "")
    return "".join(parts)


def _code_literal(text: str) -> str:
    longest = max((len(run) for run in "".join(c if c == "`" else " " for c in text).split()), default=0)
    ticks = "`" * (longest + 1)
    pad = " " if text.startswith("`") or text.endswith("`") else ""
    return f"{ticks}{pad}{text}{pad}{ticks}"


def _replace_with_text(element, literal: str) -> None:
    parent = element.getparent()
    previous = element.getprevious()
    if previous is not None:
        previous.tail = (previous.tail or "") + literal + (element.tail or "")
    else:
        parent.text = (parent.text or "") + literal + (element.tail or "")
    parent.remove(element)


# trafilatura writes a list item's paragraph child by child and trims every
# text and tail: inside an <li>, <dt> or <dd> (Sphinx's function definitions)
# the spaces around a link or code vanish ("variable[`PYTHONCASEOK`](…)is
# now"), and a link holding <strong> loses its text to the item's end. Any
# paragraph whose link holds markup (MDN's <a><em>array-like object</em></a>)
# is split the same way: its later inline code becomes fenced blocks. Such a
# paragraph holding only phrasing content is written as its markdown
# text in place — links, code and emphasis inline, every space kept.
_LIST_ITEM_TAGS = frozenset({"li", "dt", "dd"})
_PHRASING = frozenset(
    {"a", "abbr", "b", "br", "cite", "code", "del", "dfn", "em", "i", "ins", "kbd", "label", "mark", "q", "s", "samp"}
    | {"small", "span", "strong", "sub", "sup", "tt", "u", "var", "wbr"}
)
_EMPHASIS = {"b": "**", "strong": "**", "i": "*", "em": "*", "var": "*"}
_CODE_PHRASING = frozenset({"code", "kbd", "samp", "tt"})


def _phrasing_paragraphs_as_text(tree) -> None:
    for paragraph in list(tree.iter("p")):
        in_item = any(ancestor.tag in _LIST_ITEM_TAGS for ancestor in paragraph.iterancestors())
        if not in_item and not any(len(link) for link in paragraph.iter("a")):
            continue
        if any(isinstance(node.tag, str) and node.tag not in _PHRASING for node in paragraph.iterdescendants()):
            continue
        text = _phrasing_markdown(paragraph)
        for child in list(paragraph):
            paragraph.remove(child)
        paragraph.text = text


def _phrasing_markdown(element) -> str:
    parts = [element.text or ""]
    for child in element:
        if isinstance(child.tag, str):
            parts.append(_phrasing_child(child))
        parts.append(child.tail or "")
    return "".join(parts)


def _phrasing_child(element) -> str:
    if element.tag == "br":
        return " "
    if element.tag in _CODE_PHRASING:
        text = " ".join(_literal_text(element).split())
        return _code_literal(text) if text else ""
    inner = _phrasing_markdown(element)
    text = " ".join(inner.split())
    if not text:
        return inner
    lead = " " if inner[:1].isspace() else ""
    trail = " " if inner[-1:].isspace() else ""
    if element.tag == "a" and element.get("href"):
        return f"{lead}[{text}]({element.get('href')}){trail}"
    mark = _EMPHASIS.get(element.tag, "")
    if element.tag == "sup":
        return f"{lead}^{text}{trail}"
    return f"{lead}{mark}{text}{mark}{trail}"


# trafilatura's own markdown writer loses block boundaries: a heading after a
# trailing inline link is glued onto that line, a heading whose text sits in a
# link child gets no "#", and a block inside a table cell breaks its row. The
# writer below renders its extracted tree block by block, each block on its own
# line with a blank line between blocks.
_HI = {"#b": "**", "#i": "*", "#u": "__", "#t": "`"}
_BLOCK_TAGS = frozenset({"head", "p", "list", "table", "quote", "code", "div", "graphic", "main", "body", "doc", "comments"})


def _render_blocks(element) -> str:
    if element is None:
        return ""
    return "\n\n".join(_blocks(element))


def _blocks(element) -> list[str]:
    """The children of element as markdown blocks; loose inline content
    between block children becomes a paragraph of its own."""
    out: list[str] = []
    run: list[str] = [element.text or ""]

    def flush() -> None:
        text = " ".join("".join(run).split())
        if text:
            out.append(text)
        run.clear()

    for child in element:
        if child.tag in _BLOCK_TAGS and not (child.tag == "code" and _inline_code(child)):
            flush()
            block = _block(child)
            if block:
                out.append(block)
            run.append(child.tail or "")
        else:
            run.append(_inline(child, False))
    flush()
    return out


def _inline_code(element) -> bool:
    return "\n" not in (element.text or "") and element.find(".//lb") is None and element.getparent() is not None and element.getparent().tag not in {"main", "body", "div", "doc", "quote"}


def _block(element) -> str:
    tag = element.tag
    if tag == "head":
        text = _inline_content(element, True)
        if not text:
            return ""
        rend = element.get("rend") or ""
        level = int(rend[1]) if len(rend) == 2 and rend[1].isdigit() else 2
        return "#" * min(max(level, 1), 6) + " " + text
    if tag == "p":
        if any(child.tag in _BLOCK_TAGS for child in element):
            return "\n\n".join(_blocks(element))
        return _inline_content(element, False)
    if tag == "list":
        return _list(element)
    if tag == "table":
        return _table(element)
    if tag == "quote":
        inner = "\n\n".join(_blocks(element))
        return "\n".join(("> " + line).rstrip() for line in inner.splitlines())
    if tag == "code":
        return "```\n" + _code_text(element).strip("\n") + "\n```"
    if tag == "graphic":
        return _graphic(element)
    return "\n\n".join(_blocks(element))


def _list(element) -> str:
    ordered = (element.get("rend") or "").lower() in {"ol", "#ol"}
    lines: list[str] = []
    number = 0
    for item in element:
        if item.tag != "item":
            continue
        number += 1
        marker = f"{number}. " if ordered else "- "
        body = "\n".join(_blocks(item)).splitlines() or [""]
        lines.append((marker + body[0]).rstrip())
        lines.extend(("   " if ordered else "  ") + line if line else "" for line in body[1:])
    return "\n".join(lines)


def _table(element) -> str:
    rows: list[list[str]] = []
    header = False
    for row in element.iter("row"):
        cells = [_inline_content(cell, False).replace("|", "\\|") for cell in row if cell.tag == "cell"]
        if not cells:
            continue
        if not rows and any(cell.get("role") == "head" for cell in row if cell.tag == "cell"):
            header = True
        rows.append(cells)
    if not rows:
        return ""
    width = max(len(row) for row in rows)
    rows = [row + [""] * (width - len(row)) for row in rows]
    if not header:
        rows.insert(0, [""] * width)
    lines = ["| " + " | ".join(row) + " |" for row in rows]
    lines.insert(1, "|" + "---|" * width)
    return "\n".join(lines)


def _graphic(element) -> str:
    alt = " ".join(f"{element.get('title', '')} {element.get('alt', '')}".split())
    src = element.get("src", "")
    return f"![{alt}]({src})" if src else ""


def _code_text(element) -> str:
    parts = [element.text or ""]
    for child in element:
        parts.append("\n" if child.tag == "lb" else _code_text(child))
        parts.append(child.tail or "")
    return "".join(parts)


def _inline_content(element, in_head: bool) -> str:
    """element's text and inline children on one line; a block nested where
    only inline content fits (a paragraph inside a cell) joins with a space."""
    parts = [element.text or ""]
    for child in element:
        parts.append(_inline(child, in_head))
    return " ".join("".join(parts).split())


def _inline(element, in_head: bool) -> str:
    tag = element.tag
    tail = element.tail or ""
    if tag == "lb":
        return " " + tail
    if tag == "graphic":
        return " " + _graphic(element) + " " + tail
    if tag == "code":
        return "`" + " ".join(_code_text(element).split()) + "`" + tail
    inner = _inline_content(element, in_head)
    if tag == "ref":
        target = element.get("target") or ""
        if not inner:
            return tail
        # A heading's own permalink is the heading text, not a link; a
        # permalink glyph beside the text ("#", "¶") is dropped.
        if in_head and target.startswith("#"):
            return (inner if any(char.isalnum() for char in inner) else "") + tail
        if not target:
            return inner + tail
        return f"[{inner}]({target})" + tail
    if tag == "hi" and inner:
        mark = _HI.get(element.get("rend") or "", "")
        return f"{mark}{inner}{mark}" + tail
    if tag == "del" and inner:
        return f"~~{inner}~~" + tail
    return " " + inner + " " + tail


def convert_html_full(path: pathlib.Path) -> str:
    """The WHOLE DOM as Markdown, boilerplate included: the recall gate's
    fallback (Go's harvest.FullDOMConverter) when main-content extraction kept
    too little of the page's visible text. An element the reader never sees is
    dropped first (_drop_hidden), the same as on the main-content path."""
    import tempfile

    import lxml.html
    from markitdown import MarkItDown
    from trafilatura.utils import load_html

    raw = path.read_bytes().decode("utf-8", errors="ignore")
    tree = load_html(raw)
    if tree is None:
        source = raw
    else:
        _drop_hidden(tree)
        source = lxml.html.tostring(tree, encoding="unicode")
    with tempfile.NamedTemporaryFile("w", suffix=".html", encoding="utf-8") as visible:
        visible.write(source)
        visible.flush()
        with _quiet_stdout():
            body = MarkItDown().convert(visible.name).text_content or ""
    return tidy_markdown(html_metadata(raw) + body)


def tidy_markdown(markdown: str) -> str:
    lines = [line.rstrip() for line in markdown.splitlines()]
    output: list[str] = []
    index = 0
    while index < len(lines):
        if lines[index] == "":
            end = index
            while end < len(lines) and lines[end] == "":
                end += 1
            output.extend([""] if end - index >= 3 else [""] * (end - index))
            index = end
        else:
            output.append(lines[index])
            index += 1
    while output and output[0] == "":
        output.pop(0)
    while output and output[-1] == "":
        output.pop()
    return "\n".join(output) + "\n" if output else ""


def convert_pdf(path: pathlib.Path, request: dict) -> tuple[str, dict]:
    import pymupdf4llm

    # Env flags snapshot at process start set the DEFAULTS; a request carrying
    # ocr=True forces one OCR pass for THIS document (the dispatch escalation
    # rung for scanned PDFs whose text layer converts empty).
    layout = _PDF_LAYOUT
    ocr = _PDF_OCR
    if request.get("ocr"):
        ocr = True
        layout = True
    if hasattr(pymupdf4llm, "use_layout"):
        try:
            pymupdf4llm.use_layout(layout)
        except Exception as exc:
            print(f"pymupdf4llm.use_layout({layout}) failed: {exc}", file=sys.stderr)
            # The old converter logs this warning and continues with the
            # library's current layout mode.
    kwargs = {"use_ocr": ocr} if layout else {}
    with _quiet_stdout():
        try:
            result = pymupdf4llm.to_markdown(str(path), **kwargs)
        except TypeError:
            result = pymupdf4llm.to_markdown(str(path))
    return result if isinstance(result, str) else "", {
        "ocr": "enabled" if ocr else "disabled",
        "layout": "enabled" if layout else "disabled",
        "models": "requested" if (layout or ocr) else "not-requested",
    }


def convert_office(path: pathlib.Path) -> str:
    with _quiet_stdout():
        return _docling().convert(str(path)).document.export_to_markdown() or ""


def convert_epub(path: pathlib.Path) -> str:
    from markitdown import MarkItDown

    with _quiet_stdout():
        return MarkItDown().convert(str(path)).text_content or ""


def convert_csv(path: pathlib.Path) -> str:
    from markitdown import MarkItDown

    with _quiet_stdout():
        return MarkItDown().convert(str(path)).text_content or ""


def convert_json(path: pathlib.Path) -> str:
    try:
        value = json.loads(path.read_text(encoding="utf-8", errors="ignore"))
        pretty = json.dumps(value, indent=2, ensure_ascii=False)
    except Exception as exc:
        # bounded_error, not the raw exception: an OSError here names the
        # document's FULL path, and Go splices this stderr tail into the error
        # a caller reads.
        print(f"JSON parse failed; passing through raw text: {bounded_error(exc, path)}", file=sys.stderr)
        pretty = path.read_text(encoding="utf-8", errors="ignore")
    return "```json\n" + pretty.strip() + "\n```\n"


def _flag(name: str) -> bool:
    return os.environ.get(name, "").casefold() in {"1", "true", "yes", "on"}


_PDF_OCR = _flag("HARVESTER_PDF_OCR")
_PDF_LAYOUT = _flag("HARVESTER_PDF_LAYOUT")
_DOCLING = None


def _docling():
    global _DOCLING
    if _DOCLING is None:
        from docling.document_converter import DocumentConverter

        _DOCLING = DocumentConverter()
    return _DOCLING


# A conversion exception travels to a tool answer an agent reads, so the
# message it carries is bounded before it leaves this process: library
# exceptions routinely embed the document's FULL path (pymupdf, docling,
# OSError) and can quote a slice of the document itself.
_MAX_ERROR_CHARS = 300


def bounded_error(exc: BaseException, path: pathlib.Path) -> str:
    """Render one exception as "CLASS: message" with the document's directory
    stripped back to its basename and the message truncated to
    _MAX_ERROR_CHARS.  Never the document, never a path beyond the basename."""
    message = " ".join(str(exc).split())
    parent = str(path.parent)
    if parent not in {"", ".", os.sep}:
        message = message.replace(parent + os.sep, "").replace(parent, "")
    if len(message) > _MAX_ERROR_CHARS:
        message = message[:_MAX_ERROR_CHARS] + "…"
    return f"{type(exc).__name__}: {message}" if message else type(exc).__name__


def _kind(path: pathlib.Path, declared: str) -> str:
    if declared:
        return declared.casefold().lstrip(".")
    suffixes = [s.casefold().lstrip(".") for s in path.suffixes]
    if suffixes[-2:] == ["tar", "gz"]:
        return "tar"
    return suffixes[-1] if suffixes else ""


def convert(request: dict) -> dict:
    path = pathlib.Path(request["path"])
    declared = str(request.get("kind", ""))
    kind = _kind(path, declared)
    try:
        if kind in {"html", "htm"}:
            markdown = convert_html_full(path) if request.get("full_dom") else convert_html(path)
            features = {}
        elif kind == "pdf":
            markdown, features = convert_pdf(path, request)
        elif kind in {"docx", "xlsx", "pptx"}:
            markdown, features = convert_office(path), {}
        elif kind == "csv":
            markdown, features = convert_csv(path), {}
        elif kind == "epub":
            markdown, features = convert_epub(path), {}
        elif kind == "json":
            markdown, features = convert_json(path), {}
        else:
            raise ValueError(f"unsupported conversion kind: {kind!r}")
    except Exception as exc:
        # A crashed pipeline is a FAILED conversion, never an empty document.
        # The old EMPTY-text contract answered ok:true here, so a docling/
        # pymupdf/markitdown exception, an OOM, missing model weights and a
        # corrupt input all rendered as "this document converted to nothing" —
        # an error shown as absence. ok:false with the exception CLASS is the
        # honest answer; a genuinely blank document still answers ok:true with
        # empty markdown, which the Go side reports as its own EMPTY outcome.
        # Never leak a library traceback as the protocol error.
        summary = bounded_error(exc, path)
        print(f"conversion failed for kind={kind!r}: {summary}", file=sys.stderr)
        return {
            "ok": False,
            "kind": kind,
            "error_class": type(exc).__name__,
            "error": summary,
        }
    markdown = tidy_markdown(markdown)
    return {"ok": True, "markdown": markdown, "kind": kind, "features": features}


def smoke() -> dict:
    modules = ("trafilatura", "pymupdf4llm", "docling", "markitdown")
    imports = {}
    for module in modules:
        try:
            loaded = __import__(module)
            imports[module] = {"ok": True, "version": str(getattr(loaded, "__version__", ""))}
        except Exception as exc:
            imports[module] = {"ok": False, "error": f"{type(exc).__name__}: {exc}"}
    conversion = ""
    if all(item["ok"] for item in imports.values()):
        import tempfile

        with tempfile.NamedTemporaryFile("w", suffix=".html", encoding="utf-8") as fixture:
            fixture.write("<html><body><h1>harvestpy smoke</h1><p>ok</p></body></html>")
            fixture.flush()
            conversion = convert_html(pathlib.Path(fixture.name))
    return {
        "ok": all(item["ok"] for item in imports.values()),
        "python": sys.version.split()[0],
        "imports": imports,
        "conversion": {"ok": bool(conversion), "chars": len(conversion), "marker": "harvestpy smoke" if "harvestpy smoke" in conversion else ""},
        "features": {"ocr": "disabled", "layout": "disabled", "models": "not-requested"},
    }


def main() -> int:
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            request = json.loads(line)
            result = smoke() if request.get("op") == "smoke" else convert(request)
        except Exception as exc:
            # A malformed request or a crash outside convert() answers with the
            # same named shape convert()'s own failure branch uses, so the Go
            # side reads ONE failure contract rather than two.
            print(
                json.dumps(
                    {"ok": False, "error_class": type(exc).__name__, "error": f"{type(exc).__name__}: {exc}"}
                ),
                flush=True,
            )
            continue
        print(json.dumps(result, ensure_ascii=False), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
