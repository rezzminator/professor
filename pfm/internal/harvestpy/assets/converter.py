#!/usr/bin/env python3
"""Pinned Harvester conversion worker.

The worker is intentionally a small JSON-lines process.  Go owns process
lifetime and filesystem policy; this file owns the exact Python conversion
implementation.  No network operation is performed here.
"""

from __future__ import annotations

import contextlib
import json
import lzma
import os
import pathlib
import re
import shutil
import subprocess
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
    _keep_extensionless_images()
    # Local HTML follows the old dispatch path, which decodes malformed bytes
    # with errors ignored before trafilatura sees the document.
    raw = path.read_bytes().decode("utf-8", errors="ignore")
    tree = load_html(raw)
    if tree is not None:
        _drop_hidden(tree)
        _drop_repeated_excerpts(tree)
        _unwrap_layout_tables(tree)
        _keep_notes(tree)
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


# A comment or card that shows its text in full may also carry an excerpt of
# that same text, which the stylesheet shows only in the collapsed state
# (Tildes' <div class="comment-excerpt"> in every comment header). Without the
# stylesheet both are on the page and the first line is written twice. An
# excerpt whose text is repeated within its nearest enclosing blocks is that
# preview and is dropped; an excerpt standing alone (a blog index teaser) is
# the only copy and stays.
_EXCERPT_CLASS = "//*[contains(@class, 'excerpt')]"
_EXCERPT_ANCESTORS = 4


def _drop_repeated_excerpts(tree) -> None:
    for element in tree.xpath(_EXCERPT_CLASS):
        if not any("excerpt" in token for token in (element.get("class") or "").split()):
            continue
        text = " ".join(element.text_content().split()).rstrip(".… ")
        if len(text) < 20:
            continue
        ancestor = element.getparent()
        for _ in range(_EXCERPT_ANCESTORS):
            if ancestor is None:
                break
            if " ".join(ancestor.text_content().split()).count(text) >= 2:
                element.drop_tree()
                break
            ancestor = ancestor.getparent()


# trafilatura keeps an <img> only when its src ends in an image file extension
# (utils.is_image_file), tested twice: the text-node probe
# (htmlprocessing.is_image_element) and the image handler
# (main_extractor.handle_image). An image served through a proxy or a CDN
# endpoint has none — PyPI rewrites every README image to a pypi-camo URL — so
# a README's badges and its closing row of linked logos were dropped whole. An
# image the author described (a non-empty alt) with a web src passes both; an
# undescribed extensionless src (a tracking pixel) still does not.
_EXTENSIONLESS_IMAGES_KEPT = False
_WEB_SRC = re.compile(r"^(?:https?:)?//\S+$")


def _described_web_src(element) -> str:
    if element is None or not " ".join((element.get("alt") or "").split()):
        return ""
    return next((element.get(attr, "") for attr in ("data-src", "src") if _WEB_SRC.match(element.get(attr, ""))), "")


def _keep_extensionless_images() -> None:
    global _EXTENSIONLESS_IMAGES_KEPT
    if _EXTENSIONLESS_IMAGES_KEPT:
        return
    from lxml.etree import Element
    from trafilatura import htmlprocessing, main_extractor

    keep = main_extractor.handle_image
    probe = getattr(htmlprocessing, "is_image_element", None)
    if "is_image_file" not in keep.__code__.co_names:
        raise RuntimeError("trafilatura's handle_image no longer tests is_image_file; re-check the image hook")
    if probe is None or "is_image_element" not in htmlprocessing.handle_textnode.__code__.co_names:
        raise RuntimeError("trafilatura's handle_textnode no longer tests is_image_element; re-check the image hook")

    def handle_image(element, options=None):
        kept = keep(element, options)
        src = _described_web_src(element)
        if kept is not None or not src:
            return kept
        graphic = Element(element.tag)
        graphic.set("src", "https:" + src if src.startswith("//") else src)
        graphic.set("alt", " ".join(element.get("alt").split()))
        if title := element.get("title"):
            graphic.set("title", title)
        graphic.tail = element.tail
        return graphic

    main_extractor.handle_image = handle_image
    htmlprocessing.is_image_element = lambda element: probe(element) or bool(_described_web_src(element))
    _EXTENSIONLESS_IMAGES_KEPT = True


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


# trafilatura's tree cleaning deletes every <aside> and <footer> before it
# selects the main content. A footnote or endnote is the text's own (the
# DPUB-ARIA roles, or a class token footnote…/endnote…: Sphinx's
# <aside class="footnote" role="doc-footnote">), and so is an article's own
# footer written as prose — its correction note ("This article was amended
# on …") — when no list, nav or form control sits in it and links are under
# half its text. Each becomes a <div>; every other aside and footer (a
# newsletter promotion, a share bar, the site footer) is cleaned as before.
_NOTE_ROLES = frozenset({"doc-footnote", "doc-endnote", "doc-endnotes"})
_NOTE_CLASSES = ("footnote", "endnote")
_FOOTER_CONTROLS = ".//nav|.//ul|.//ol|.//form|.//button|.//input|.//select|.//textarea|.//iframe"


def _keep_notes(tree) -> None:
    for element in list(tree.iter("aside", "footer")):
        classes = (element.get("class") or "").casefold().split()
        if element.get("role") in _NOTE_ROLES or any(token.startswith(_NOTE_CLASSES) for token in classes):
            element.tag = "div"
        elif element.tag == "footer" and _is_article_note(element):
            element.tag = "div"


def _is_article_note(footer) -> bool:
    if footer.xpath("not(ancestor::article)") or footer.xpath(_FOOTER_CONTROLS):
        return False
    if not any("".join(paragraph.itertext()).strip() for paragraph in footer.iter("p")):
        return False
    text = len("".join("".join(footer.itertext()).split()))
    linked = sum(len("".join("".join(link.itertext()).split())) for link in footer.iter("a"))
    return linked * 2 < text


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
        _drop_repeated_excerpts(tree)
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


def _pymupdf4llm_markdown(path: pathlib.Path, layout: bool, pages: list[int] | None = None) -> str:
    import pymupdf4llm

    if hasattr(pymupdf4llm, "use_layout"):
        try:
            pymupdf4llm.use_layout(layout)
        except Exception as exc:
            print(f"pymupdf4llm.use_layout({layout}) failed: {exc}", file=sys.stderr)
            # The old converter logs this warning and continues with the
            # library's current layout mode.
    kwargs = {} if pages is None else {"pages": pages}
    with _quiet_stdout():
        result = pymupdf4llm.to_markdown(str(path), **kwargs)
    return result if isinstance(result, str) else ""


def convert_pdf(path: pathlib.Path, request: dict) -> tuple[str, dict]:
    import pymupdf

    # Env flags snapshot at process start set the DEFAULTS; a request carrying
    # ocr=True forces OCR on EVERY page of THIS document (the dispatch
    # escalation rung for a PDF whose text layer converted empty). Otherwise
    # the prepass (rule R3) flags the pages without a usable text layer and
    # only those are OCR'd; the rest keep their text layer.
    layout = _PDF_LAYOUT or bool(request.get("ocr"))
    forced = _PDF_OCR or bool(request.get("ocr"))
    with pymupdf.open(str(path)) as document:
        flagged = [index for index, page in enumerate(document) if forced or ocr_page_needed(page_features(page))]
        if not flagged:
            return _pymupdf4llm_markdown(path, layout), {
                "ocr": "not-needed",
                "layout": "enabled" if layout else "disabled",
                "models": "not-requested",
            }
        script, reason = choose_ocr_script(document, str(request.get("ocr_lang") or ""))
        ocr_pages, limits = _ocr_pages(document, flagged, script)
        page_count = document.page_count
    parts = [_note(f"OCR read page(s) {_page_list(flagged)} of {page_count} as {_SCRIPT_NAMES[script]} ({reason})")]
    parts += [_note(limit) for limit in limits]
    for index in range(page_count):
        if index in ocr_pages:
            parts.append(ocr_pages[index])
        elif index not in flagged:
            parts.append(_pymupdf4llm_markdown(path, layout, [index]))
    if not any(text.strip() for text in ocr_pages.values()) and len(flagged) == page_count and limits:
        # Nothing was readable and a limit says why: a failure, never a
        # document that "converted" to its own notes.
        raise OCRUnavailable("; ".join(limits))
    return "\n\n".join(part for part in parts if part.strip()), {
        "ocr": "enabled",
        "layout": "enabled" if layout else "disabled",
        "models": "staged",
    }


# ---------------------------------------------------------------------------
# OCR — the bake-off's verdict (docling + RapidOCR on onnxruntime, one model
# per script, Hebrew through a system tesseract). Models are staged by
# `pfm install` under the pfm state dir (HARVESTPY_MODEL_ROOT) and the reader
# runs offline: a model that is not staged is a named failure, never a
# download in the middle of a read.

_MODEL_ROOT = os.environ.get("HARVESTPY_MODEL_ROOT", "")
if _MODEL_ROOT:
    # Before any docling/huggingface import: the layout and table models live
    # in the staged cache, and outside staging the hub is never contacted.
    os.environ["HF_HOME"] = os.path.join(_MODEL_ROOT, "hf")
    if os.environ.get("HARVESTPY_MODEL_STAGING") != "1":
        os.environ["HF_HUB_OFFLINE"] = "1"

OCR_SCRIPTS = ("latin", "zh", "ja", "ar", "ru", "he")
_SCRIPT_NAMES = {
    "latin": "Latin",
    "zh": "Chinese",
    "ja": "Japanese",
    "ar": "Arabic",
    "ru": "Cyrillic",
    "he": "Hebrew",
}
# PDF /Lang (BCP 47 primary subtag) → the script model that reads it.
_LANG_SCRIPTS = {
    "zh": "zh", "ja": "ja", "ar": "ar", "fa": "ar", "ur": "ar",
    "ru": "ru", "uk": "ru", "be": "ru", "he": "he", "iw": "he", "yi": "he",
}
OCR_CONTROL_SHARE = 0.02
# The Latin default's reason: the document stated no script. The harvester
# matches this phrase (harvest.ocrAssumption) to flag the result partial.
OCR_LATIN_ASSUMED = (
    "the document names no language; Latin by default — pass ocr_lang to read it in another script"
    " (script detection before OCR is unmeasured)"
)


class OCRUnavailable(RuntimeError):
    """A page needs OCR and no staged engine can read it (named limit)."""


class OCRModelsNotStaged(RuntimeError):
    """An OCR model is not in the staged cache; the reader never fetches it."""


def page_features(page) -> dict:
    """The prepass's measurements for one page (bake-off prepass.py)."""
    import unicodedata

    import pymupdf

    text = page.get_text().strip()
    chars = len(text)
    area = abs(page.rect)
    cover = 0.0
    for info in page.get_image_info():
        cover += abs(pymupdf.Rect(info["bbox"]) & page.rect)
    cover = min(cover / area, 1.0) if area else 0.0
    invisible = total = 0
    try:
        for span in page.get_texttrace():
            total += len(span["chars"])
            if span.get("type") == 3:
                invisible += len(span["chars"])
    except Exception as exc:
        print(f"texttrace failed on page {page.number + 1}: {type(exc).__name__}: {exc}", file=sys.stderr)
    control = sum(1 for ch in text if unicodedata.category(ch) == "Cc" and ch not in "\n\t\r")
    return {
        "chars": chars,
        "image_cover": cover,
        "fffd": text.count("�") / chars if chars else 0.0,
        "cid": len(re.findall(r"\(cid:\d+\)", text)) * 8 / chars if chars else 0.0,
        "invisible_share": invisible / total if total else 0.0,
        "control": control / chars if chars else 0.0,
    }


def ocr_page_needed(features: dict) -> bool:
    """Rule R3: no usable text layer (near-empty, image-dominated, U+FFFD or
    (cid:NN) garbage) — unless the page already carries an invisible OCR layer
    that is not garbage — or control characters above 2% (a font with no
    Unicode map emits shifted letters plus U+0003)."""
    no_layer = (
        features["chars"] < 20
        or features["image_cover"] >= 0.5
        or features["fffd"] > 0.10
        or features["cid"] > 0.5
    )
    ocr_layer = features["invisible_share"] > 0.9 and features["fffd"] <= 0.10 and features["chars"] >= 20
    return (no_layer and not ocr_layer) or features["control"] > OCR_CONTROL_SHARE


def _script_of_text(text: str) -> str | None:
    counts = dict.fromkeys(("ja", "zh", "ar", "ru", "he", "latin"), 0)
    for ch in text:
        code = ord(ch)
        if 0x3040 <= code <= 0x30FF:
            counts["ja"] += 1
        elif 0x4E00 <= code <= 0x9FFF or 0x3400 <= code <= 0x4DBF:
            counts["zh"] += 1
        elif 0x0600 <= code <= 0x06FF:
            counts["ar"] += 1
        elif 0x0400 <= code <= 0x04FF:
            counts["ru"] += 1
        elif 0x0590 <= code <= 0x05FF:
            counts["he"] += 1
        elif ch.isascii() and ch.isalpha():
            counts["latin"] += 1
    if counts["ja"] and counts["ja"] + counts["zh"] >= 2:
        return "ja"  # kana marks Japanese; kanji alone reads as Chinese
    best = max(counts, key=counts.get)
    return best if counts[best] >= 2 else None


def choose_ocr_script(document, requested: str = "") -> tuple[str, str]:
    """One script per conversion (RapidOCR loads one language). Detection
    before OCR is unmeasured, so the choice comes from what the PDF states:
    its text layer, then its /Lang, then its title/subject/keywords — else
    Latin. The caller's ocr_lang (one of OCR_SCRIPTS) overrides all of it.
    The reason travels into the output; the Latin default's reason is the
    note the harvester raises to the result's partial flag."""
    if requested:
        if requested not in OCR_SCRIPTS:
            raise OCRUnavailable(f"ocr_lang {requested!r} is not one of {', '.join(OCR_SCRIPTS)}")
        return requested, f"ocr_lang {requested!r} was requested"
    layer ="".join(page.get_text() for page in document)
    script = _script_of_text(layer)
    if script and sum(ch.isalpha() for ch in layer) >= 20:
        return script, f"script of the PDF's text layer"
    lang = ""
    try:
        kind, value = document.xref_get_key(document.pdf_catalog(), "Lang")
        if kind == "string":
            lang = value.strip().casefold()
    except Exception as exc:
        print(f"PDF /Lang unreadable: {type(exc).__name__}: {exc}", file=sys.stderr)
    if lang:
        primary = lang.replace("_", "-").split("-")[0]
        return _LANG_SCRIPTS.get(primary, "latin"), f"the PDF's /Lang is {lang!r}"
    metadata = document.metadata or {}
    stated = " ".join(str(metadata.get(key) or "") for key in ("title", "subject", "keywords"))
    script = _script_of_text(stated)
    if script:
        return script, "script of the PDF's title/subject metadata"
    return "latin", OCR_LATIN_ASSUMED


def ocr_timeout(flagged_pages: int) -> float:
    """docling's document_timeout: 12 s per flagged page, never under 30 s."""
    return float(max(30, 12 * flagged_pages))


def _page_list(indexes: list[int]) -> str:
    return ", ".join(str(index + 1) for index in indexes)


def _model_root() -> pathlib.Path:
    if not _MODEL_ROOT:
        raise OCRModelsNotStaged(
            "no OCR model directory is configured (HARVESTPY_MODEL_ROOT unset) — run `pfm install` to stage the OCR models"
        )
    return pathlib.Path(_MODEL_ROOT)


def _staged_marker(root: pathlib.Path, name: str) -> pathlib.Path:
    return root / f"staged-{name}.json"


def require_staged(root: pathlib.Path, name: str) -> None:
    """A model set is usable only when its staging marker exists and every
    file it recorded is still on disk."""
    marker = _staged_marker(root, name)
    missing = ""
    try:
        files = json.loads(marker.read_text(encoding="utf-8"))["files"]
        missing = next((file for file in files if not (root / file).is_file()), "")
    except FileNotFoundError:
        missing = marker.name
    except (OSError, ValueError, KeyError, TypeError) as exc:
        missing = f"{marker.name} ({type(exc).__name__})"
    if missing:
        raise OCRModelsNotStaged(
            f"the OCR models for {name} are not staged (missing {missing}) — run `pfm install` to stage them"
            " (the first run downloads about 1.06 GB); the harvester never downloads a model mid-read"
        )


def _is_staged(root: pathlib.Path, name: str) -> bool:
    try:
        require_staged(root, name)
    except OCRModelsNotStaged:
        return False
    return True


def _ocr_options(script: str, root: pathlib.Path):
    from docling.datamodel.pipeline_options import RapidOcrOptions, TesseractCliOcrOptions

    if script == "he":
        return TesseractCliOcrOptions(lang=["heb"], force_full_page_ocr=True)
    from rapidocr.utils.typings import LangRec, ModelType, OCRVersion

    params = {"Global.model_root_dir": str(root / "rapidocr")}
    versions = {
        "ja": (LangRec.JAPAN, OCRVersion.PPOCRV4),
        "ar": (LangRec.ARABIC, OCRVersion.PPOCRV5),
        "ru": (LangRec.ESLAV, OCRVersion.PPOCRV5),
    }
    if script in versions:
        lang_type, version = versions[script]
        params.update({"Rec.lang_type": lang_type, "Rec.ocr_version": version, "Rec.model_type": ModelType.MOBILE})
    # latin: the english model; zh: the bundled PP-OCRv6 default.
    lang = ["english"] if script == "latin" else ["chinese"]
    return RapidOcrOptions(lang=lang, backend="onnxruntime", force_full_page_ocr=True, rapidocr_params=params)


def _hebrew_limit() -> str:
    tesseract = shutil.which("tesseract")
    if not tesseract:
        return "no Hebrew OCR: `tesseract` is not on PATH (install tesseract with its Hebrew data to read Hebrew scans)"
    try:
        langs = subprocess.run([tesseract, "--list-langs"], capture_output=True, text=True, timeout=30).stdout
    except (OSError, subprocess.SubprocessError) as exc:
        return f"no Hebrew OCR: `tesseract --list-langs` failed ({type(exc).__name__})"
    if "heb" not in langs.split():
        return "no Hebrew OCR: tesseract is installed without its Hebrew data (heb)"
    return ""


def _script_limit(script: str) -> str:
    """RapidOCR reorders Arabic output through python-bidi, pinned because the
    bake-off measured Arabic with it; an environment without it (a stale
    provision) names Arabic unavailable, never reads it into garbage or a crash."""
    import importlib.util

    if script == "ar" and importlib.util.find_spec("bidi") is None:
        return "no Arabic OCR: RapidOCR's Arabic model needs python-bidi, which this environment does not include (re-provision it)"
    return ""


def _docling_ocr_converter(script: str, root: pathlib.Path, timeout: float | None):
    from docling.datamodel.accelerator_options import AcceleratorOptions
    from docling.datamodel.base_models import InputFormat
    from docling.datamodel.pipeline_options import PdfPipelineOptions
    from docling.document_converter import DocumentConverter, PdfFormatOption

    options = PdfPipelineOptions(
        do_ocr=True,
        ocr_options=_ocr_options(script, root),
        do_table_structure=True,
        accelerator_options=AcceleratorOptions(device="cpu", num_threads=4),
    )
    if timeout is not None:
        options.document_timeout = timeout
    return DocumentConverter(format_options={InputFormat.PDF: PdfFormatOption(pipeline_options=options)})


def _vertical_page(document, page_no: int) -> bool:
    """Tall, narrow text boxes dominate: vertical columns (vertical CJK),
    which no engine in the bake-off read."""
    tall = boxes = 0
    for item in getattr(document, "texts", []):
        for prov in getattr(item, "prov", []):
            if prov.page_no != page_no:
                continue
            box = prov.bbox
            width, height = abs(box.r - box.l), abs(box.t - box.b)
            boxes += 1
            tall += height > 2 * width
    return boxes >= 3 and tall * 2 >= boxes


def _ocr_pages(source, flagged: list[int], script: str) -> tuple[dict[int, str], list[str]]:
    import tempfile
    import time

    import pymupdf

    limit = _hebrew_limit() if script == "he" else _script_limit(script)
    if limit:
        return {}, [f"{limit}; page(s) {_page_list(flagged)} were not read"]
    root = _model_root()
    require_staged(root, "docling")
    if script != "he":
        require_staged(root, "latin")  # the shared detection/classifier models
        require_staged(root, script)
    timeout = ocr_timeout(len(flagged))
    limits: list[str] = []
    with tempfile.TemporaryDirectory() as scratch:
        subset = pathlib.Path(scratch) / "ocr.pdf"
        with pymupdf.open() as copy:
            for index in flagged:
                copy.insert_pdf(source, from_page=index, to_page=index)
            copy.save(str(subset))
        started = time.monotonic()
        with _quiet_stdout():
            result = _docling_ocr_converter(script, root, timeout).convert(str(subset), raises_on_error=False)
        elapsed = time.monotonic() - started
    status = str(getattr(result.status, "value", result.status))
    document = result.document
    pages: dict[int, str] = {}
    unread: list[int] = []
    for position, index in enumerate(flagged, start=1):
        text = document.export_to_markdown(page_no=position) if document is not None else ""
        pages[index] = text
        if not text.strip():
            unread.append(index)
        if document is not None and _vertical_page(document, position):
            limits.append(
                f"page {index + 1} is set in vertical columns (vertical CJK): no OCR engine reads it — its text is unreliable"
            )
    if status != "success":
        stopped = " at its time limit" if elapsed >= timeout else ""
        limits.append(
            f"OCR ended {status}{stopped} ({elapsed:.0f} s of a {timeout:.0f} s limit = max(30, 12 x {len(flagged)} page(s)))"
        )
    if unread:
        limits.append(f"OCR found no text on page(s) {_page_list(unread)}")
    return pages, limits


def stage_models(root: pathlib.Path) -> dict:
    """`pfm install`'s staging: warm every model the reader loads — docling's
    layout+table models and one RapidOCR model per script — by converting a
    one-page PDF with each script's exact configuration, then record the files
    each set needs (require_staged checks them before every read)."""
    import tempfile

    import pymupdf

    root.mkdir(parents=True, exist_ok=True)

    def files_under(base: pathlib.Path) -> set[str]:
        return {str(p.relative_to(root)) for p in base.rglob("*") if p.is_file()} if base.exists() else set()

    staged = {}
    with tempfile.TemporaryDirectory() as scratch:
        sample = pathlib.Path(scratch) / "stage.pdf"
        with pymupdf.open() as document:
            document.new_page(width=300, height=120).insert_text((20, 60), "harvestpy OCR staging 2024", fontsize=18)
            document.save(str(sample))
        for script in [s for s in OCR_SCRIPTS if s != "he"]:
            if _script_limit(script):
                staged[script] = {"skipped": _script_limit(script)}
                continue
            # A set an earlier run staged keeps its marker: re-staging it would
            # record an empty file list (its files predate `before`) over the
            # real one. Only the sets missing a marker (Arabic, once python-bidi
            # was pinned) are converted — plus one, if docling's marker is gone.
            was_staged = _is_staged(root, script)
            if was_staged and (_is_staged(root, "docling") or "docling" in staged):
                staged[script] = {"status": "already staged"}
                continue
            before = files_under(root / "rapidocr")
            with _quiet_stdout():
                result = _docling_ocr_converter(script, root, None).convert(str(sample), raises_on_error=True)
            files = sorted(files_under(root / "rapidocr") - before)
            if "docling" not in staged:
                # The first conversion also fetched docling's layout+table models.
                docling_files = sorted(files_under(root / "hf"))
                _staged_marker(root, "docling").write_text(json.dumps({"files": docling_files}), encoding="utf-8")
                staged["docling"] = len(docling_files)
            if was_staged:
                staged[script] = {"status": "already staged"}
                continue
            _staged_marker(root, script).write_text(json.dumps({"files": files}), encoding="utf-8")
            staged[script] = {"files": len(files), "status": str(getattr(result.status, "value", result.status))}
    # The hub cache links snapshots to blobs: count each real file once.
    size = sum(p.stat().st_size for p in root.rglob("*") if p.is_file() and not p.is_symlink())
    return {"ok": True, "staged": staged, "bytes": size, "hebrew": _hebrew_limit() or "tesseract with heb"}


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


def convert_text(path: pathlib.Path) -> str:
    return path.read_text(encoding="utf-8", errors="replace")


def _html(path: pathlib.Path, request: dict) -> tuple[str, dict]:
    return (convert_html_full(path) if request.get("full_dom") else convert_html(path)), {}


def _plain(convert_one):
    return lambda path, _request: (convert_one(path), {})


# The text formats below are the bake-off winners (the formats bake-off's
# hand-written parsers, each tied or beat feedparser, docling, pubmed_parser,
# nbconvert, srt and rispy on real files): lxml with entities never expanded
# and no network, or stdlib json/email/plistlib; BibTeX through bibtexparser 2.
# Each writes blocks joined by a blank line, and a follow-up the bake-off left
# open is named in the output as a "Converter note" where it applies.


class EntityBombRefused(ValueError):
    """An XML document whose entities expand other entities (billion laughs,
    quadratic blowup): refused by name, never expanded and never read."""


_NESTED_ENTITY = re.compile(rb"<!ENTITY\s+(?:%\s*)?[\w.:-]+\s+(?:\"[^\"]*&[\w.:-]+;|'[^']*&[\w.:-]+;)")
_BOMB_REFUSAL = (
    "refused: the XML declares entities that expand into other entities (an entity bomb); "
    "the harvester never expands XML entities"
)


def _note(text: str) -> str:
    return f"_Converter note: {text}_"


def _xml_root(path: pathlib.Path, recover: bool):
    from lxml import etree

    raw = path.read_bytes()
    if _NESTED_ENTITY.search(raw[:262144]):
        raise EntityBombRefused(_BOMB_REFUSAL)
    parser = etree.XMLParser(
        resolve_entities=False, no_network=True, load_dtd=False, dtd_validation=False, huge_tree=False, recover=recover
    )
    try:
        root = etree.fromstring(raw, parser)
    except etree.XMLSyntaxError as exc:
        if "amplification" in str(exc) or "entity" in str(exc).casefold() and "loop" in str(exc).casefold():
            raise EntityBombRefused(_BOMB_REFUSAL) from exc
        raise
    errors = [e for e in parser.error_log if e.level >= etree.ErrorLevels.ERROR]
    if any("amplification" in e.message for e in errors):
        raise EntityBombRefused(_BOMB_REFUSAL)
    if root is None:
        raise ValueError("the XML has no root element: " + (errors[0].message.strip() if errors else "empty document"))
    return root, errors


def _lname(element) -> str:
    tag = element.tag
    if not isinstance(tag, str):
        return ""
    return tag.split("}", 1)[1] if "}" in tag else tag


def _itxt(element) -> str:
    return " ".join("".join(element.itertext()).split()) if element is not None else ""


def _child(element, *names):
    for child in element:
        if _lname(child) in names:
            return child
    return None


def _html_text(markup: str) -> str:
    import lxml.html

    markup = markup.strip()
    if not markup:
        return ""
    try:
        return re.sub(r"[ \t]+", " ", lxml.html.fromstring(markup).text_content()).strip()
    except Exception as exc:
        # Not HTML after all (lxml.etree.ParserError on bare text): the
        # literal text is the content.
        print(f"feed body is not HTML; keeping it literal: {type(exc).__name__}", file=sys.stderr)
        return markup


def convert_feed(path: pathlib.Path) -> str:
    from lxml import etree

    root, errors = _xml_root(path, recover=True)

    def link(item) -> str:
        for child in item:
            if _lname(child) == "link":
                if child.get("href") and child.get("rel", "alternate") == "alternate":
                    return child.get("href")
                if (child.text or "").strip():
                    return child.text.strip()
        return ""

    shape = _lname(root)
    if shape == "feed":
        feed, items = root, [c for c in root if _lname(c) == "entry"]
    else:
        channel = _child(root, "channel")
        feed = channel if channel is not None else root
        items = [c for c in root.iter() if _lname(c) in ("item", "entry")]
    title = _itxt(_child(feed, "title"))
    if not items and not title:
        raise ValueError(
            "the feed parsed to no title and no items: a broken or empty feed"
            + (f" ({errors[0].message.strip()})" if errors else "")
            + "; a feedparser fallback on a broken feed is unmeasured"
        )
    label = {"feed": "Atom", "rss": "RSS"}.get(shape.casefold(), "RDF/RSS 1.0")
    blocks = [f"# {title}", f"{label} feed · {len(items)} items"]
    for item in items:
        body = None
        for name in ("encoded", "content", "description", "summary"):
            body = _child(item, name)
            if body is not None and _itxt(body):
                break
        if body is None:
            text = ""
        elif len(body):
            text = _html_text(etree.tostring(body, encoding=str, method="html"))
        else:
            text = _html_text("".join(body.itertext()))
        enclosures = " ".join(
            x.get("url") or x.get("href", "")
            for x in item
            if _lname(x) == "enclosure" or (_lname(x) == "link" and x.get("rel") == "enclosure")
        )
        date = _itxt(_child(item, "pubDate", "date", "published", "updated"))
        heading = _html_text(_itxt(_child(item, "title"))) or "(untitled item)"
        blocks += [f"## {heading}", link(item), date, f"Enclosure: {enclosures}" if enclosures else "", text]
    if errors:
        blocks.append(
            _note(
                f"the feed is malformed ({errors[0].message.strip()}, line {errors[0].line}); "
                f"{len(items)} items were recovered and items past the break may be missing; "
                "a feedparser fallback on a broken feed is unmeasured"
            )
        )
    return "\n\n".join(b for b in blocks if b)


def convert_jats(path: pathlib.Path) -> str:
    root, _errors = _xml_root(path, recover=False)
    blocks: list[str] = []
    flattened = [0]

    def math(element) -> str:
        tex = next((x for x in element.iter() if _lname(x) == "tex-math"), None)
        if tex is not None:
            return _itxt(tex)
        annotation = next(
            (x for x in element.iter() if _lname(x) == "annotation" and "tex" in (x.get("encoding") or "").casefold()),
            None,
        )
        if annotation is not None:
            return _itxt(annotation)
        mathml = next((x for x in element.iter() if _lname(x) == "math"), None)
        if mathml is not None:
            flattened[0] += 1
        return _itxt(mathml if mathml is not None else element)

    def inline(element) -> str:
        text = element.text or ""
        for child in element:
            name = _lname(child)
            if name == "inline-formula":
                text += f" ${math(child)}$ "
            elif name in ("fn", "table-wrap", "fig", "disp-formula"):
                pass
            elif name == "xref" and child.get("ref-type") == "bibr":
                text += f"[{_itxt(child)}]"
            else:
                text += inline(child)
            text += child.tail or ""
        return " ".join(text.split())

    def label_of(element) -> str:
        found = next((x for x in element.iter() if _lname(x) == "label"), None)
        return _itxt(found)

    def table(wrap) -> None:
        caption = _itxt(next((x for x in wrap.iter() if _lname(x) == "caption"), None))
        blocks.append(f"**{label_of(wrap)}** {caption}".strip())
        rows = [x for x in wrap.iter() if _lname(x) == "tr"]
        lines = []
        for index, row in enumerate(rows):
            cells = [c for c in row if _lname(c) in ("td", "th")]
            lines.append("| " + " | ".join(inline(c).strip().replace("|", "\\|") for c in cells) + " |")
            if index == 0:
                lines.append("|" + "---|" * max(1, len(cells)))
        if lines:
            blocks.append("\n".join(lines))
        for foot in (x for x in wrap.iter() if _lname(x) == "table-wrap-foot"):
            blocks.append(_itxt(foot))

    def listing(element) -> str:
        return "\n".join("- " + _itxt(item) for item in element if _lname(item) == "list-item")

    def references(ref_list) -> str:
        # One rendering for a ref-list wherever it sits: <back> (PMC) or
        # inside a body section (Europe PMC's fullTextXML has no <back>).
        return "\n".join("- " + _itxt(ref) for ref in ref_list.iter() if _lname(ref) == "ref")

    def footnote(fn) -> str:
        label = _itxt(_child(fn, "label"))
        text = " ".join(inline(p) for p in fn if _lname(p) == "p") or _itxt(fn)
        return "- " + (f"{label} {text}" if label else text)

    def walk(element, depth: int) -> None:
        for child in element:
            name = _lname(child)
            if name == "title":
                blocks.append("#" * min(depth, 6) + " " + inline(child).strip())
            elif name == "sec":
                walk(child, depth + 1)
            elif name == "p":
                blocks.append(inline(child).strip())
                for nested in child:
                    if _lname(nested) in ("table-wrap", "fig", "disp-formula", "list"):
                        walk_one(nested, depth)
            elif name == "list":
                blocks.append(listing(child))
            else:
                walk_one(child, depth)

    def walk_one(child, depth: int) -> None:
        name = _lname(child)
        if name == "table-wrap":
            table(child)
        elif name == "fig":
            caption = _itxt(next((x for x in child.iter() if _lname(x) == "caption"), None))
            blocks.append(f"**{label_of(child)}** {caption}".strip())
        elif name == "disp-formula":
            blocks.append(f"$$ {math(child)} $$")
        elif name == "list":
            blocks.append(listing(child))
        elif name in ("boxed-text", "app", "app-group", "ack", "fn-group", "notes", "sec"):
            walk(child, depth + 1)
        elif name == "ref-list":
            heading = _child(child, "title")
            if heading is not None:
                blocks.append("#" * min(depth + 1, 6) + " " + inline(heading).strip())
            blocks.append(references(child))
        elif name == "fn":
            blocks.append(footnote(child))

    title = next((x for x in root.iter() if _lname(x) == "article-title"), None)
    blocks.append("# " + _itxt(title))
    authors = []
    for contrib in (x for x in root.iter() if _lname(x) == "contrib" and x.get("contrib-type") == "author"):
        parts = [_itxt(x) for x in contrib.iter() if _lname(x) in ("given-names", "surname")]
        if parts:
            authors.append(" ".join(parts))
    if authors:
        blocks.append("Authors: " + ", ".join(authors))
    for abstract in (x for x in root.iter() if _lname(x) == "abstract"):
        blocks.append("## Abstract")
        if len(abstract):
            walk(abstract, 2)
        else:
            blocks.append(_itxt(abstract))
    body = _child(root, "body")
    if body is not None:
        walk(body, 1)
    back = _child(root, "back")
    if back is not None:
        for child in back:
            if _lname(child) == "ref-list":
                blocks.append("## References")
                blocks.append(references(child))
            else:
                walk_one(child, 1)
    for floats in (x for x in root.iter() if _lname(x) == "floats-group"):
        for child in floats:
            walk_one(child, 1)
    if flattened[0]:
        blocks.append(_note(f"{flattened[0]} MathML formulas carry no TeX and are flattened to linear text"))
    return "\n\n".join(b for b in blocks if b.strip())


def convert_fb2(path: pathlib.Path) -> str:
    root, _errors = _xml_root(path, recover=True)
    blocks: list[str] = []

    def href(element) -> str:
        return next((v for k, v in element.attrib.items() if k.endswith("href")), "")

    def inline(element) -> str:
        text = element.text or ""
        for child in element:
            name, inner = _lname(child), inline(child)
            if name == "emphasis":
                text += f"*{inner}*"
            elif name == "strong":
                text += f"**{inner}**"
            elif name == "a" and child.get("type") == "note":
                text += f"[^{href(child).lstrip('#')}]"
            else:
                text += inner
            text += child.tail or ""
        return " ".join(text.split())

    def block(element, depth: int) -> None:
        for child in element:
            name = _lname(child)
            if name == "title":
                blocks.append("#" * min(depth, 6) + " " + " ".join(inline(x) for x in child if _lname(x) == "p"))
            elif name == "section":
                block(child, depth + 1)
            elif name == "p":
                blocks.append(inline(child))
            elif name == "subtitle":
                blocks.append(f"**{inline(child)}**")
            elif name in ("epigraph", "cite"):
                blocks.append(
                    "\n".join("> " + inline(x) for x in child.iter() if _lname(x) in ("p", "v", "text-author"))
                )
            elif name == "poem":
                blocks.append("\n".join(inline(x) for x in child.iter() if _lname(x) in ("v", "title")))
            elif name == "image":
                blocks.append(f"![image]({href(child)})")
            elif name == "table":
                blocks.append(
                    "\n".join(
                        "| " + " | ".join(inline(td) for td in row) + " |" for row in child if _lname(row) == "tr"
                    )
                )

    info = next((x for x in root.iter() if _lname(x) == "title-info"), None)
    if info is not None:
        blocks.append("# " + _itxt(_child(info, "book-title")))
        for author in (x for x in info if _lname(x) == "author"):
            names = [_itxt(x) for x in author if _lname(x) in ("first-name", "middle-name", "last-name")]
            blocks.append("Author: " + " ".join(n for n in names if n))
        annotation = _child(info, "annotation")
        if annotation is not None:
            blocks.append(_itxt(annotation))
    for body in (x for x in root if _lname(x) == "body"):
        if body.get("name") == "notes":
            blocks.append("## Notes")
            for section in (x for x in body.iter() if _lname(x) == "section" and x.get("id")):
                text = " ".join(inline(x) for x in section.iter() if _lname(x) in ("p", "v", "subtitle", "text-author"))
                blocks.append(f"[^{section.get('id')}]: {text}")
        else:
            block(body, 1)
    return "\n\n".join(b for b in blocks if b.strip())


_ANSI = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
_NOTEBOOK_OUTPUT_CAP = 20000


def convert_ipynb(path: pathlib.Path) -> str:
    notebook = json.loads(path.read_bytes().decode("utf-8-sig"))
    if not isinstance(notebook, dict) or not isinstance(notebook.get("cells"), list):
        raise ValueError("the notebook has no cells list: not a Jupyter notebook")
    language = ((notebook.get("metadata") or {}).get("language_info") or {}).get("name", "python")

    def joined(value) -> str:
        return "".join(value) if isinstance(value, list) else (value or "")

    def fenced(text: str) -> str:
        return "```text\n" + text[:_NOTEBOOK_OUTPUT_CAP] + "\n```"

    blocks = []
    for cell in notebook["cells"]:
        source = joined(cell.get("source"))
        kind = cell.get("cell_type")
        if kind in ("markdown", "raw"):
            blocks.append(source)
            continue
        if kind != "code":
            continue
        blocks.append(f"```{language}\n{source}\n```")
        for output in cell.get("outputs", []):
            shape = output.get("output_type")
            if shape == "stream":
                blocks.append(fenced(_ANSI.sub("", joined(output.get("text")))))
            elif shape == "error":
                trace = "\n".join(output.get("traceback", []))
                blocks.append(fenced(_ANSI.sub("", f"{output.get('ename')}: {output.get('evalue')}\n{trace}")))
            elif shape in ("execute_result", "display_data"):
                data = output.get("data", {})
                images = [k for k in data if k.startswith("image/")]
                blocks += [f"![output]({k})" for k in images]
                if "application/vnd.jupyter.widget-view+json" in data:
                    blocks.append("*[interactive widget]*")
                if "text/markdown" in data:
                    blocks.append(joined(data["text/markdown"])[:_NOTEBOOK_OUTPUT_CAP])
                elif "text/plain" in data and not images:
                    blocks.append(fenced(joined(data["text/plain"])))
                elif "text/html" in data and "text/plain" not in data:
                    blocks.append(_html_text(joined(data["text/html"]))[:_NOTEBOOK_OUTPUT_CAP])
    return "\n\n".join(blocks)


_BOMS = (
    (b"\xef\xbb\xbf", "utf-8-sig"),
    (b"\xff\xfe\x00\x00", "utf-32"),
    (b"\x00\x00\xfe\xff", "utf-32"),
    (b"\xff\xfe", "utf-16"),
    (b"\xfe\xff", "utf-16"),
)


def _decode_text(raw: bytes) -> str:
    for bom, encoding in _BOMS:
        if raw.startswith(bom):
            return raw.decode(encoding)
    try:
        return raw.decode("utf-8")
    except UnicodeDecodeError:
        return raw.decode("cp1252", errors="replace")


def convert_captions(path: pathlib.Path) -> str:
    import html

    text = _decode_text(path.read_bytes()).replace("\r\n", "\n").replace("\r", "\n")
    lines, cues, recent = [], 0, []
    for cue in re.split(r"\n\s*\n", text.strip()):
        rows = cue.split("\n")
        timing = next((i for i, row in enumerate(rows) if "-->" in row), None)
        if timing is None:
            continue  # the WEBVTT header, NOTE, STYLE, REGION blocks
        cues += 1
        for row in rows[timing + 1 :]:
            line = html.unescape(re.sub(r"\{\\[^}]*\}", "", re.sub(r"<[^>]+>", "", row))).strip()
            # Rolling captions repeat the previous lines; a line seen in the
            # last three is the same caption still on screen.
            if not line or line in recent:
                continue
            lines.append(line)
            recent = (recent + [line])[-3:]
    if not cues:
        raise ValueError("the captions file has no timed cues (no '-->' line)")
    return f"Captions: {cues} cues\n\n" + "\n".join(lines)


_RIS_LINE = re.compile(r"^([A-Z][A-Z0-9])  -(?: (.*))?$")
_RIS_TITLE = ("TI", "T1", "CT", "BT")
_RIS_NOTE = "RIS and BibTeX parsing is untested on messy exports"


def convert_ris(path: pathlib.Path) -> str:
    entries, current, last = [], None, None
    for line in _decode_text(path.read_bytes()).splitlines():
        match = _RIS_LINE.match(line.rstrip())
        if match:
            tag, value = match.group(1), (match.group(2) or "").strip()
            if tag == "TY":
                current, last = [("TY", value)], None
            elif tag == "ER":
                if current is not None:
                    entries.append(current)
                current = None
            elif current is not None:
                current.append((tag, value))
                last = tag
        elif current is not None and last and line.strip():
            tag, value = current[-1]
            current[-1] = (tag, value + " " + line.strip())
    unterminated = current is not None
    if unterminated:
        entries.append(current)
    if not entries:
        raise ValueError("the RIS file has no records (no 'TY  - ' line)")
    blocks = [f"RIS: {len(entries)} records"]
    for entry in entries:
        title = next((v for t, v in entry if t in _RIS_TITLE), "") or "(untitled record)"
        blocks.append(f"## {title}")
        blocks.append("\n".join(f"- {t}: {v}" for t, v in entry if t not in _RIS_TITLE and v))
    if unterminated:
        blocks.append(_note("the last record has no ER line; it is kept as read"))
    blocks.append(_note(_RIS_NOTE))
    return "\n\n".join(blocks)


def convert_bibtex(path: pathlib.Path) -> str:
    import bibtexparser

    library = bibtexparser.parse_string(_decode_text(path.read_bytes()))
    entries, failed = library.entries, library.failed_blocks
    if not entries and not failed:
        raise ValueError("the BibTeX file has no entries")
    blocks = [f"BibTeX: {len(entries)} entries"]
    for entry in entries:
        fields = {field.key.casefold(): str(field.value) for field in entry.fields}
        blocks.append(f"## {fields.get('title') or '(untitled entry)'}")
        rows = [f"- key: {entry.key}", f"- type: {entry.entry_type}"]
        rows += [f"- {name}: {' '.join(value.split())}" for name, value in fields.items() if name != "title"]
        blocks.append("\n".join(rows))
    if failed:
        where = ", ".join(f"line {getattr(b, 'start_line', '?')}" for b in failed[:10])
        blocks.append(_note(f"{len(failed)} blocks could not be parsed and are not in this text ({where})"))
    blocks.append(_note(_RIS_NOTE))
    return "\n\n".join(blocks)


def _html_body(markup: str, request: dict) -> str:
    import tempfile

    with tempfile.TemporaryDirectory() as scratch:
        page = pathlib.Path(scratch) / "page.html"
        page.write_text(markup, encoding="utf-8")
        return _html(page, request)[0]


def _resource_list(title: str, resources: list[str]) -> str:
    if not resources:
        return ""
    shown = resources[:50]
    more = f"\n- … and {len(resources) - len(shown)} more" if len(resources) > len(shown) else ""
    return f"## {title}\n\n" + "\n".join(f"- {r}" for r in shown) + more


def convert_mhtml(path: pathlib.Path, request: dict) -> tuple[str, dict]:
    import email
    from email import policy

    message = email.message_from_bytes(path.read_bytes(), policy=policy.default)
    parts = [p for p in message.walk() if not p.is_multipart()]
    page = next((p for p in parts if p.get_content_type() == "text/html"), None)
    if page is None:
        raise ValueError(f"the MHTML archive has no text/html part ({len(parts)} parts)")
    rest = [
        f"{p.get('Content-Location') or p.get('Content-ID') or '(unnamed)'} ({p.get_content_type()})"
        for p in parts
        if p is not page
    ]
    body = _html_body(page.get_content(), request)
    return "\n\n".join(b for b in (body, _resource_list("Archived resources, not inlined", rest)) if b), {}


def convert_eml(path: pathlib.Path, request: dict) -> tuple[str, dict]:
    import email
    from email import policy

    message = email.message_from_bytes(path.read_bytes(), policy=policy.default)
    blocks = [f"# {message.get('Subject') or '(no subject)'}"]
    blocks.append(
        "\n".join(f"- {name}: {message.get(name)}" for name in ("From", "To", "Cc", "Date") if message.get(name))
    )
    body = message.get_body(preferencelist=("plain", "html"))
    if body is not None:
        content = body.get_content()
        blocks.append(_html_body(content, request) if body.get_content_type() == "text/html" else content.strip())
    attachments = []
    for part in message.iter_attachments():
        payload = part.get_payload(decode=True) or b""
        attachments.append(f"{part.get_filename() or '(unnamed)'} ({part.get_content_type()}, {len(payload)} bytes)")
    blocks.append(_resource_list("Attachments, not inlined", attachments))
    return "\n\n".join(b for b in blocks if b), {}


def convert_webarchive(path: pathlib.Path, request: dict) -> tuple[str, dict]:
    import plistlib

    archive = plistlib.loads(path.read_bytes())
    main = archive.get("WebMainResource") if isinstance(archive, dict) else None
    if not isinstance(main, dict) or not isinstance(main.get("WebResourceData"), bytes):
        raise ValueError("the web archive has no WebMainResource data")
    mime = str(main.get("WebResourceMIMEType") or "")
    encoding = str(main.get("WebResourceTextEncodingName") or "utf-8")
    try:
        text = main["WebResourceData"].decode(encoding, errors="replace")
    except LookupError:
        text = main["WebResourceData"].decode("utf-8", errors="replace")
    if mime in ("text/html", "application/xhtml+xml"):
        body = _html_body(text, request)
    elif mime.startswith("text/"):
        body = text.strip()
    else:
        raise ValueError(f"the web archive's main resource is {mime or 'untyped'}, not a page the harvester reads")
    rest = [
        f"{r.get('WebResourceURL', '(unnamed)')} ({r.get('WebResourceMIMEType', 'untyped')})"
        for r in archive.get("WebSubresources", [])
        if isinstance(r, dict)
    ]
    frames = archive.get("WebSubframeArchives", [])
    rest += [f"subframe {(f.get('WebMainResource') or {}).get('WebResourceURL', '(unnamed)')}" for f in frames]
    return "\n\n".join(b for b in (body, _resource_list("Archived resources, not inlined", rest)) if b), {}


# The Office family, per the office bake-off's verdicts: legacy DOC through
# legacy-doc given the BYTES, XLS through xlrd, RTF through striprtf, ODT and
# ODS through odfdo (ODS clamped first), ODP through docling, the macro and
# template OOXML variants through markitdown (mammoth installed) after the
# [Content_Types].xml main-type rewrite. Every Office container is checked for
# a password with msoffcrypto-tool before any reader opens it. Each loss the
# bake-off measured is named in the output as a converter note.


class PasswordProtected(ValueError):
    """An encrypted Office document: named, never read as binary characters."""


class UnsupportedOfficeFormat(ValueError):
    """An Office format no pinned reader opens (Word 95 and older)."""


_PASSWORD_REFUSAL = (
    "password-protected: the document is encrypted and the harvester has no password; "
    "open it with its password, save an unprotected copy and read that copy"
)
_NO_LIBREOFFICE = "no LibreOffice (soffice) on PATH for a structure-keeping fallback"


def _refuse_encrypted(path: pathlib.Path) -> None:
    import msoffcrypto

    try:
        with path.open("rb") as handle:
            encrypted = msoffcrypto.OfficeFile(handle).is_encrypted()
    except Exception as exc:
        # Not a container msoffcrypto knows, or a corrupt one: the reader
        # decides and names its own failure; this check only refuses what it
        # PROVED encrypted.
        print(f"password pre-check could not read the container: {bounded_error(exc, path)}", file=sys.stderr)
        return
    if encrypted:
        raise PasswordProtected(_PASSWORD_REFUSAL)


def _refuse_encrypted_odf(path: pathlib.Path) -> None:
    import zipfile

    with zipfile.ZipFile(path) as archive:
        try:
            manifest = archive.read("META-INF/manifest.xml")
        except KeyError:
            return
    if b"encryption-data" in manifest:
        raise PasswordProtected(_PASSWORD_REFUSAL)


@contextlib.contextmanager
def _named_copy(path: pathlib.Path, suffix: str):
    """The document under a name whose suffix the library routes by."""
    import shutil
    import tempfile

    with tempfile.TemporaryDirectory(prefix="harvestpy-office-") as scratch:
        target = pathlib.Path(scratch) / f"document.{suffix}"
        shutil.copyfile(path, target)
        yield target


def _markitdown(path: pathlib.Path) -> str:
    from markitdown import MarkItDown

    with _quiet_stdout():
        return MarkItDown().convert(str(path)).text_content or ""


def _libreoffice_markdown(path: pathlib.Path) -> str | None:
    """LibreOffice → DOCX → markitdown, the bake-off's structure-keeping
    fallback for legacy DOC; None when no soffice is on PATH."""
    import shutil
    import subprocess

    soffice = shutil.which("soffice") or shutil.which("libreoffice")
    if not soffice:
        return None
    with _named_copy(path, "doc") as source:
        scratch = source.parent
        run = subprocess.run(
            [
                soffice,
                "--headless",
                "--norestore",
                f"-env:UserInstallation=file://{scratch}/profile",
                "--convert-to",
                "docx",
                "--outdir",
                str(scratch / "out"),
                str(source),
            ],
            capture_output=True,
            timeout=180,
            check=False,
        )
        converted = scratch / "out" / "document.docx"
        if not converted.exists():
            raise RuntimeError(f"LibreOffice produced no .docx (exit {run.returncode})")
        return _markitdown(converted)


def _word_fib(path: pathlib.Path) -> tuple[int, int]:
    """(nFib, ccpText) from the WordDocument stream's FIB: the version and the
    count of characters the document itself declares for its main text."""
    import olefile

    with olefile.OleFileIO(str(path)) as ole:
        fib = ole.openstream("WordDocument").read(0x50)
    if len(fib) < 0x50:
        return (int.from_bytes(fib[2:4], "little") if len(fib) >= 4 else 0), 0
    return int.from_bytes(fib[2:4], "little"), int.from_bytes(fib[0x4C:0x50], "little")


# Word 97 writes nFib 0xC1 (193); Word 6.0/95 write 101-105 and keep no
# 0Table/1Table stream, which legacy-doc requires.
_WORD97_NFIB = 0xC1
# legacy-doc answered 34 characters for a 6 MB spec as success: a result
# shorter than this share of the characters the FIB declares is a truncation.
_DOC_TRUNCATION_RATIO = 0.5


def convert_doc(path: pathlib.Path) -> str:
    _refuse_encrypted(path)
    import legacy_doc

    try:
        nfib, declared = _word_fib(path)
    except Exception as exc:
        print(f"Word FIB unreadable, no size guard: {bounded_error(exc, path)}", file=sys.stderr)
        nfib, declared = _WORD97_NFIB, 0
    if 0 < nfib < _WORD97_NFIB:
        fallback = _libreoffice_markdown(path)
        if fallback is None:
            raise UnsupportedOfficeFormat(
                f"a Word 95 (or older) document (nFib {nfib}): the legacy DOC reader opens Word 97-2003 only "
                f"and there is {_NO_LIBREOFFICE}; save it as DOCX and read that copy"
            )
        return fallback + "\n\n" + _note("a Word 95 (or older) document, read through LibreOffice")
    result = legacy_doc.extract_text(path.read_bytes())
    text = result if isinstance(result, str) else getattr(result, "text", str(result))
    got = len(text.strip())
    if declared and got < declared * _DOC_TRUNCATION_RATIO:
        fallback = _libreoffice_markdown(path)
        if fallback is not None:
            return fallback + "\n\n" + _note(
                f"the legacy DOC reader returned {got} of the {declared} characters the document declares; "
                "this text is LibreOffice's conversion"
            )
        return text + "\n\n" + _note(
            f"TRUNCATED: the legacy DOC reader returned {got} of the {declared} characters the document "
            f"declares, and there is {_NO_LIBREOFFICE}; the rest of the document is not in this text"
        )
    return text + "\n\n" + _note(
        "plain text only: the legacy DOC reader keeps no tables, headings or lists "
        "(table cells run together as text)"
    )


def _md_cell(value: object) -> str:
    return "" if value is None else str(value).replace("|", "\\|").replace("\n", " ")


def _md_table(rows: list[list]) -> str:
    rows = [[_md_cell(c) for c in row] for row in rows]
    rows = [row for row in rows if any(cell.strip() for cell in row)]
    if not rows:
        return ""
    width = max(len(row) for row in rows)
    rows = [row + [""] * (width - len(row)) for row in rows]
    lines = ["| " + " | ".join(rows[0]) + " |", "|" + "---|" * width]
    lines += ["| " + " | ".join(row) + " |" for row in rows[1:]]
    return "\n".join(lines)


def convert_xls(path: pathlib.Path) -> str:
    _refuse_encrypted(path)
    import xlrd

    book = xlrd.open_workbook(file_contents=path.read_bytes())
    parts = []
    for sheet in book.sheets():
        parts.append("## " + sheet.name)
        parts.append(_md_table([sheet.row_values(i) for i in range(sheet.nrows)]) or "_(empty sheet)_")
    parts.append(
        _note("number and date formats are not applied: cells show stored values (a date is its serial number)")
    )
    return "\n\n".join(parts)


_RTF_ESCAPED = re.compile(r"\\[\\{}]")


def convert_rtf(path: pathlib.Path) -> str:
    from striprtf.striprtf import rtf_to_text

    raw = path.read_text(encoding="latin-1", errors="replace")
    text = rtf_to_text(raw, errors="ignore")
    bare = _RTF_ESCAPED.sub("", raw)
    depth = bare.count("{") - bare.count("}")
    notes = []
    if depth > 0 or not raw.rstrip().endswith("}"):
        notes.append(
            f"TRUNCATED: the RTF ends with {max(depth, 1)} group(s) never closed; "
            "the text after the cut is not in this document"
        )
    rows = raw.count("\\trowd")
    if rows:
        notes.append(f"tables are lost: {rows} table row(s) are flattened into plain lines")
    notes.append("headings and lists are flattened to plain text (the RTF reader keeps text only)")
    return "\n\n".join([text] + [_note(n) for n in notes])


def _odfdo_markdown(path: pathlib.Path) -> str:
    from odfdo import Document

    document = Document(str(path))
    result = document.to_markdown()
    return "\n".join(result) if isinstance(result, list) else (result or "")


def convert_odt(path: pathlib.Path) -> str:
    _refuse_encrypted_odf(path)
    return _odfdo_markdown(path)


# LibreOffice pads a sheet with ~1,048,000 repeated empty rows and ~16,000
# repeated columns; docling and odfdo expand them and hang. Any repeat count
# past this cap is clamped to 1 in content.xml BEFORE a parser opens the file.
_ODS_REPEAT_CAP = 1000
_ODS_REPEAT = re.compile(rb'table:number-(rows|columns)-repeated="(\d+)"')


def _clamp_ods(path: pathlib.Path, target: pathlib.Path) -> int:
    import zipfile

    clamped = 0

    def clamp(match: re.Match) -> bytes:
        nonlocal clamped
        if int(match.group(2)) <= _ODS_REPEAT_CAP:
            return match.group(0)
        clamped += 1
        return b'table:number-' + match.group(1) + b'-repeated="1"'

    with zipfile.ZipFile(path) as source, zipfile.ZipFile(target, "w") as out:
        for item in source.infolist():
            data = source.read(item.filename)
            if item.filename == "content.xml":
                data = _ODS_REPEAT.sub(clamp, data)
            method = zipfile.ZIP_STORED if item.filename == "mimetype" else zipfile.ZIP_DEFLATED
            out.writestr(item, data, compress_type=method)
    return clamped


def convert_ods(path: pathlib.Path) -> str:
    _refuse_encrypted_odf(path)
    from odfdo import Document

    with _named_copy(path, "ods") as copy:
        clamped_path = copy.with_name("clamped.ods")
        clamped = _clamp_ods(copy, clamped_path)
        document = Document(str(clamped_path))
        parts = []
        for table in document.body.get_tables():
            parts.append("## " + (table.name or "Sheet"))
            parts.append(_md_table(table.get_values()) or "_(empty sheet)_")
    if clamped:
        parts.append(
            _note(
                f"{clamped} row/column repeat(s) over {_ODS_REPEAT_CAP} (LibreOffice's sheet padding) were read "
                "once each; a genuinely repeated row or column that long appears once"
            )
        )
    parts.append(_note("number and date formats are not applied: cells show stored values"))
    return "\n\n".join(parts)


def convert_odp(path: pathlib.Path) -> str:
    _refuse_encrypted_odf(path)
    with _named_copy(path, "odp") as copy, _quiet_stdout():
        return _docling().convert(str(copy)).document.export_to_markdown() or ""


_OOXML_MAIN = {
    "docx": b"application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml",
    "xlsx": b"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml",
    "pptx": b"application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml",
}
_OOXML_BASE = {
    "docm": "docx",
    "dotx": "docx",
    "dotm": "docx",
    "xlsm": "xlsx",
    "xltx": "xlsx",
    "xltm": "xlsx",
    "pptm": "pptx",
    "potx": "pptx",
    "potm": "pptx",
    "ppsx": "pptx",
    "ppsm": "pptx",
}
_OOXML_VARIANT_MAIN = re.compile(
    rb"application/vnd\.(?:ms-word|ms-excel|ms-powerpoint|openxmlformats-officedocument\."
    rb"(?:wordprocessingml|spreadsheetml|presentationml))\.(?:document\.macroEnabled\.main|template\.main|"
    rb"template\.macroEnabled\.main|template\.macroEnabledTemplate\.main|macroEnabledTemplate\.main|"
    rb"sheet\.macroEnabled\.main|slideshow\.macroEnabled\.main|slideshow\.main|presentation\.macroEnabled\.main)\+xml"
)


def _ooxml_variant(kind: str):
    def convert_variant(path: pathlib.Path) -> str:
        import zipfile

        _refuse_encrypted(path)
        base = _OOXML_BASE[kind]
        with _named_copy(path, kind) as copy:
            rewritten = copy.with_name("document." + base)
            with zipfile.ZipFile(copy) as source, zipfile.ZipFile(rewritten, "w", zipfile.ZIP_DEFLATED) as out:
                for item in source.infolist():
                    data = source.read(item.filename)
                    if item.filename == "[Content_Types].xml":
                        data = _OOXML_VARIANT_MAIN.sub(_OOXML_MAIN[base], data)
                    out.writestr(item, data)
            text = _markitdown(rewritten)
        if kind.endswith("m"):
            text += "\n\n" + _note("the document's macros are not read or run")
        return text

    return convert_variant


def convert_encrypted(path: pathlib.Path) -> str:
    """An OLE EncryptedPackage (an encrypted OOXML file)."""
    _refuse_encrypted(path)
    raise PasswordProtected(_PASSWORD_REFUSAL)


def _office_checked(convert_one):
    """The OOXML kinds docling reads also pass the password check first."""

    def convert_checked(path: pathlib.Path) -> str:
        _refuse_encrypted(path)
        return convert_one(path)

    return convert_checked


# The one dispatch point: a kind, routed by the Go side from the body's magic
# bytes (pfm/internal/harvest/format_detect.go), maps to its converter. The
# text formats and the web archives arrive under their own kind and take the
# bake-off winners above, and the Office family its office readers.
_CONVERTERS = {
    "html": _html,
    "htm": _html,
    "pdf": convert_pdf,
    "docx": _plain(_office_checked(convert_office)),
    "xlsx": _plain(_office_checked(convert_office)),
    "pptx": _plain(_office_checked(convert_office)),
    "doc": _plain(convert_doc),
    "xls": _plain(convert_xls),
    "rtf": _plain(convert_rtf),
    "odt": _plain(convert_odt),
    "ods": _plain(convert_ods),
    "odp": _plain(convert_odp),
    "encrypted": _plain(convert_encrypted),
    **{kind: _plain(_ooxml_variant(kind)) for kind in _OOXML_BASE},
    "csv": _plain(convert_csv),
    "epub": _plain(convert_epub),
    "json": _plain(convert_json),
    "feed": _plain(convert_feed),
    "jats": _plain(convert_jats),
    "fb2": _plain(convert_fb2),
    "ipynb": _plain(convert_ipynb),
    "vtt": _plain(convert_captions),
    "srt": _plain(convert_captions),
    "ris": _plain(convert_ris),
    "bibtex": _plain(convert_bibtex),
    "eml": convert_eml,
    "mhtml": convert_mhtml,
    "webarchive": convert_webarchive,
}


def convert(request: dict) -> dict:
    path = pathlib.Path(request["path"])
    declared = str(request.get("kind", ""))
    kind = _kind(path, declared)
    try:
        converter = _CONVERTERS.get(kind)
        if converter is None:
            raise ValueError(f"unsupported conversion kind: {kind!r}")
        markdown, features = converter(path, request)
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


class DecompressionBomb(ValueError):
    """A compressed document that inflates past the caller's cap: named, never written."""


def inflate(request: dict) -> dict:
    """Decompress one xz document with the stdlib lzma (the Go side carries no
    xz decoder) into request["out"], at most request["limit"] bytes; past the
    limit it is a DecompressionBomb. Go routes the inner bytes."""
    codec = str(request.get("codec", ""))
    if codec != "xz":
        raise ValueError(f"unsupported inflate codec: {codec!r}")
    limit = int(request["limit"])
    with lzma.open(request["path"], format=lzma.FORMAT_XZ) as stream:
        inner = stream.read(limit + 1)
    if len(inner) > limit:
        raise DecompressionBomb(f"the xz document decompresses to more than {limit} bytes")
    pathlib.Path(request["out"]).write_bytes(inner)
    return {"ok": True, "bytes": len(inner)}


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
            if request.get("op") == "smoke":
                result = smoke()
            elif request.get("op") == "inflate":
                result = inflate(request)
            elif request.get("op") == "stage_models":
                result = stage_models(_model_root())
            else:
                result = convert(request)
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
