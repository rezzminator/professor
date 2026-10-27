"""The cost side of a prep: the timeline of one transcript (a row per model call, priced), and the
mechanical rows (TOTALS, MODEL-WAIT, COSTLIEST) read from it."""
import re

from . import claude as claude_cost, prices
from .claude import Shared  # noqa: F401  (a census builds one)
from .fmt import NOT_PRICED, num

ROW = re.compile(r"^  #\d+ \S+ after (\S+) · ctx \S+ · out \d+ · (\$[0-9.]+|n/a)")


class Gaps:
    """The data gaps one timeline run found."""

    def __init__(self, book, scan, extra=()):
        self.book, self.scan, self.extra = book, scan, list(extra)

    def lines(self):
        book, s, loud = self.book, self.scan, []
        refresh = book.doc.get("refresh") if isinstance(book.doc.get("refresh"), dict) else {}
        if not book.loaded:
            loud.append(f"price table unavailable ({book.failure or 'empty'}) — every dollar is n/a")
        elif book.failure:
            loud.append(f"price refresh failed ({book.failure}) — served the cached table fetched "
                        f"{book.doc.get('fetched_at', '')}")
        elif refresh.get("status") == "failed":
            loud.append(f"price refresh failed ({refresh.get('error') or 'no cause given'}) — served the table "
                        f"fetched {book.doc.get('fetched_at', '')}")
        ov = book.doc.get("override")
        if isinstance(ov, dict) and ov:
            loud.append(f"price override active: {num(ov.get('rows', 0))} rows from {ov.get('path', '')}")
        for n, text in ((s.bad, " malformed lines"), (s.no_stamp, " calls without a timestamp (dropped)"),
                        (s.synthetic, " synthetic/zero-usage calls (dropped: the harness billed nothing for them)")):
            if n:
                loud.append(f"{n}{text}")
        if s.unpriced:
            import json
            loud.append(f"{s.unpriced} UNPRICED calls " + json.dumps(s.unpriced_models, separators=(",", ":"),
                        ensure_ascii=False) + ' — tokens counted, dollars "n/a"; check exact IDs and available '
                        "rates with pfm model-cost --json --all")
        if s.unknown_iter:
            import json
            loud.append(f"{sum(s.unknown_iter.values())} usage iterations of a type pfm does not price apart "
                        + json.dumps(s.unknown_iter, separators=(",", ":")) + " — the response's own counts stand")
        for n, text in ((s.tier_unknown, " cache writes with no 5m/1h split (priced as 5m)"),
                        (s.identity, " assistant records without message.id or requestId — per-record billing may "
                                     "include streamed copies"),
                        (s.copied, " calls copied from another transcript (forked/resumed session) — not billed here")):
            if n:
                loud.append(f"{n}{text}")
        loud += self.extra
        if s.read_errors:
            loud.append(f"{len(s.read_errors)} READ ERRORS: " + " | ".join(s.read_errors[:3]))
        return [book.line(), "data gaps: " + (" · ".join(loud) if loud else "none")]


def timeline(path, p, kit_dir, shared=None, book=None):
    """(timeline lines, state): the lines `timeline.txt` holds, the state one word or the first line's reason.
    `shared` is a claude_cost.Shared and `book` a prices.load(kit_dir) a census passes for all its transcripts."""
    if book is None:
        book = prices.load(kit_dir)
    try:
        if p.engine == "claude":
            head, rows, scan = claude_cost.run(path, book, shared)
            body = None if head is None else [head] + claude_cost.render(rows)
            extra = ()
        else:
            import importlib
            mod = importlib.import_module("." + p.engine, __package__)
            body, scan, extra = mod.run(path, p, book)
    except (claude_cost.Unreadable, OSError) as e:
        return [f"UNREADABLE — {path}: {e}"], f"UNREADABLE — {path}: {e}"[:200]
    if body is None:
        body = [f"NO CALLS — {path}"]
    lines = body + Gaps(book, scan, extra).lines()
    return lines, ("ok" if lines[0].startswith("TIMELINE") else lines[0][:200])


def rows(tl):
    """The M rows (TOTALS, MODEL-WAIT, COSTLIEST) the timeline lines give, ref `-` as the digest rows."""
    out, usd = [], []
    for j, t in enumerate(tl, 1):
        if j == 1 and t.startswith("TIMELINE"):
            out.append((f"M{j}", "TOTALS", "-"))
        elif re.match(r"^(pricing|data gaps):", t):
            out.append((f"M{j}", "TOTALS", "-"))
        else:
            m = ROW.match(t)
            if not m:
                continue
            if m.group(1) != "-" and float(m.group(1)[:-1]) >= 60:
                out.append((f"M{j}", "MODEL-WAIT", "-"))
            if m.group(2) != NOT_PRICED:
                usd.append((float(m.group(2)[1:]), j))
    for _, j in sorted(usd, reverse=True)[:10]:
        out.append((f"M{j}", "COSTLIEST", "-"))
    return out
