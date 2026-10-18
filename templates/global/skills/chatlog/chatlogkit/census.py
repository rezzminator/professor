"""The `census` verb: one row per transcript across many runs (a type's runs, a session and its sub-agents,
or explicit paths), each computed by the kit's own engines, labels and cost module, a TOTAL row, and the
spawn tree on --tree."""
import os
import re
import sys
import time
import traceback
from collections import Counter

from . import cli, cost, digest, labels, locate
from .cost.fmt import jround
from .digest import Request
from .engines import Failure, opencode
from .model import FINAL, one_line
from .rows import die, opt

USAGE = "usage: chatlog.py census --type T [--since 30d] [--project SUBSTR] | --session S | PATH...  [--tree] [--out F]"
COLS = ("id engine agent_type description start_utc wall_s calls errors denials compactions spawns usd final "
        "parent").split()
DESC_CLIP, FINAL_CLIP = 60, 120
STAMP = "%Y-%m-%dT%H:%M:%SZ"
RUN_USD = re.compile(r" · results >20KB \d+ · (\$[0-9]+\.[0-9]{4}|n/a)")
USD_CELL = re.compile(r"\$([0-9]+)\.([0-9]{4})")
TOOL_USE_ID = re.compile(rb'"id"\s*:\s*"([^"\\]+)"')
GAPS_LEAD = "data gaps: "
READ_ERRORS = re.compile(r"(?:^| · )\d+ READ ERRORS: ")


def field(v):
    """A meta value as one cell: whitespace collapsed (no tab or newline), '-' when absent or empty."""
    text = one_line(v if isinstance(v, str) else ("" if v is None or v is False else str(v)))
    return text or "-"


def run_usd(head, path):
    """(the usd cell, the reason it failed or ''): the run total the timeline's first line prints."""
    if head.startswith("TIMELINE"):
        m = RUN_USD.search(head)
        if m:
            return m.group(1), ""
        return "FAILED", f"the timeline head carries no total: {head[:200]}"
    if head.startswith("NO CALLS"):
        return "$0.0000", ""
    lead = f"UNREADABLE — {path}: "
    return "FAILED", head[len(lead):] if head.startswith(lead) else head[:200]


def usd_units(cell):
    """A priced cell as ten-thousandths of a dollar, None for n/a or FAILED."""
    m = USD_CELL.fullmatch(cell)
    return int(m.group(1)) * 10000 + int(m.group(2)) if m else None


def usd_sum(units, unpriced, failed=0):
    """The dollars of units, then how many cells were n/a and how many FAILED, each counted apart."""
    return (f"${units // 10000}.{units % 10000:04d}" + (f" +{unpriced} n/a" if unpriced else "")
            + (f" +{failed} FAILED" if failed else ""))


def usd_count(cells):
    """(units, n/a cells, FAILED cells) of usd cells."""
    units = [usd_units(c) for c in cells]
    return (sum(u for u in units if u is not None), sum(1 for c, u in zip(cells, units) if u is None and c != "FAILED"),
            sum(1 for c in cells if c == "FAILED"))


def own_gaps(tl, book_gaps):
    """The data gaps of one timeline that are its own, '' when none: its `data gaps:` line less the price
    book's gaps, which every transcript of a census shares and the census prints once."""
    last = tl[-1] if tl and tl[-1].startswith(GAPS_LEAD) else GAPS_LEAD + "none"
    text = last[len(GAPS_LEAD):]
    if text == "none" or text == book_gaps:
        return ""
    if book_gaps and text.startswith(book_gaps + " · "):
        return text[len(book_gaps) + 3:]
    return text


def start_key(row):
    return (row["t0"] is None, row["t0"].timestamp() if row["t0"] else 0.0, row["id"])


def claude_who(path, hint):
    """(who, failures) of a Claude transcript: a session by its file stem, a sub-agent by its agent id with the
    type, description and spawning call id its .meta.json beside it names."""
    base = os.path.basename(path)
    stem = base[:-len(".jsonl")] if base.endswith(".jsonl") else base
    sub = base.startswith("agent-") or os.path.basename(os.path.dirname(path)) == "subagents"
    who = dict(id=stem[len("agent-"):] if stem.startswith("agent-") else stem, agent_type="main",
               description="-", pid="", sub=sub, tuid="")
    fails = []
    if sub:
        meta, mp = hint, os.path.join(os.path.dirname(path), stem + ".meta.json")
        if meta is None and os.path.isfile(mp):
            meta, why = locate.read_meta(mp)
            if meta is None:
                fails.append((mp, why))
        meta = meta or {}
        tuid = meta.get("toolUseId")
        who.update(agent_type=field(meta.get("agentType")), description=field(meta.get("description")),
                   tuid=tuid if isinstance(tuid, str) else "")
    return who, fails


def codex_who(path, p):
    head, fails = locate.rollout_header(path), []
    if head is None:
        fails.append((path, "the session_meta header cannot be read"))
        head = dict(id="", parent="", role="", nickname="", child=False)
    stem = os.path.basename(path)[:-len(".jsonl")]
    return dict(id=head["id"] or p.meta.get("session") or stem, agent_type=field(head["role"]) if head["child"]
                else "main", description=field(head["nickname"]), pid=head["parent"], sub=False, tuid=""), fails


def opencode_who(path, p):
    child = bool(p.meta.get("agent"))
    return dict(id=opencode.split_locator(path)[1], agent_type=field(p.meta.get("agent_type")) if child
                else "main", description=field(p.title), pid=p.meta.get("session", "") if child else "",
                sub=False, tuid="")


class Reader:
    """What a census builds once for all its transcripts: the shared cost walk, the price book, the home. A
    session's transcripts share their session ids, so its walk builds no UUID index a second id never asks."""

    def __init__(self, form):
        self.shared = cost.Shared(eager_index=form != "session")
        self.book = cost.prices.load(cli.KIT_DIR)
        self.home = os.path.expanduser("~")
        book = cost.Gaps(self.book, cost.claude_cost.Scan()).lines()[1][len(GAPS_LEAD):]
        self.book_gaps = "" if book == "none" else book

    def measure(self, path, hint=None):
        """(row, failures) of one transcript: row None when it cannot be parsed, failures [(subject, reason)]."""
        try:
            return self.read(path, hint)
        except (Failure, ValueError) as e:
            return None, [(path, one_line(str(e))[:600])]
        except Exception as e:  # one transcript's bug must not end a census of hundreds: named, exit 1
            traceback.print_exc()
            return None, [(path, f"internal error {type(e).__name__}: {e}")]

    def read(self, path, hint):
        p = cli.parse(path, path)
        raw, spans = digest.build(p, Request(""), self.home)
        dig = [digest.clean(x) for x in raw]
        mech, tools, _ = labels.mechanical(dig, spans)
        tl, _ = cost.timeline(path, p, cli.KIT_DIR, self.shared, self.book)
        if p.engine == "claude":
            who, fails = claude_who(path, hint)
        elif p.engine == "codex":
            who, fails = codex_who(path, p)
        else:
            who, fails = opencode_who(path, p), []
        usd, why = run_usd(tl[0], path)
        if why:
            fails.append((path, why))
        gaps = own_gaps(tl, self.book_gaps)
        if READ_ERRORS.search(gaps):
            # a file the ownership walk could not read may hold this transcript's calls: its dollars are unsure
            fails.append((path, GAPS_LEAD + gaps))
            gaps = ""
        n = Counter(r[1] for r in mech)
        last = next((one_line(ev.text)[:FINAL_CLIP] for ev, _, _ in reversed(spans) if ev.kind == FINAL), "")
        row = dict(who, engine=p.engine, calls=sum(tools.values()), errors=n["ERROR"], denials=n["DENIAL"],
                   compactions=n["COMPACTION"], spawns=n["SPAWN"], usd=usd, gaps=gaps,
                   final="UNFINISHED" if n["UNFINISHED"] else last or "-", path=path, t0=p.t0,
                   start_utc=p.t0.strftime(STAMP) if p.t0 else "-",
                   wall_s=jround((p.t1 - p.t0).total_seconds()) if p.t0 else "-")
        return row, fails


def holders(rows):
    """({spawning call id: index of the Claude row whose transcript holds it}, failures): the call ids the
    sub-agents' metas name, looked up by a pattern over the bytes of the rows that spawned anything."""
    wanted = {r["tuid"].encode(): r["tuid"] for r in rows if r["tuid"]}
    held, fails = {}, []
    if not wanted:
        return held, fails
    for i, r in enumerate(rows):
        if r["engine"] != "claude" or not r["spawns"]:
            continue
        try:
            with open(r["path"], "rb") as f:
                data = f.read()
        except OSError as e:
            fails.append((r["path"], f"cannot read it to find its sub-agents' parents: {e}"))
            continue
        for found in set(TOOL_USE_ID.findall(data)) & wanted.keys():
            held.setdefault(wanted[found], i)
    return held, fails


def link(rows, root):
    """(parents, failures): per row the index of its parent row (None for a root), and the parent column set.
    Codex and OpenCode name the parent's id; a Claude sub-agent names the call that spawned it, found in the
    transcript that holds it. With a `root` (a session census) a Claude sub-agent whose parent is not found
    hangs under it, the parent column suffixed `?`."""
    by_id = {}
    for i, r in enumerate(rows):
        by_id.setdefault(r["id"], i)
    held, fails = holders(rows)
    parents = []
    for i, r in enumerate(rows):
        j, text = None, "-"
        if r["pid"]:
            j, text = by_id.get(r["pid"]), r["pid"]
        elif r["sub"]:
            j = held.get(r["tuid"])
            if j is not None:
                text = rows[j]["id"]
            elif root is not None:
                j, text = root, rows[root]["id"] + "?"
        if j == i:
            j, text = None, "-"
        r["parent"] = text
        parents.append(j)
    return parents, fails


def children(rows, parents):
    kids = {}
    for i, par in enumerate(parents):
        if par is not None:
            kids.setdefault(par, []).append(i)
    for v in kids.values():
        v.sort(key=lambda i: start_key(rows[i]))
    return kids


def walk(roots, kids, n):
    """[(index, depth, the index it was reached from)] in tree order from the roots; a parent cycle's members,
    reached from no root, start trees of their own."""
    out, seen = [], set()
    for start in list(roots) + list(range(n)):
        stack = [(start, 0, None)]
        while stack:
            i, depth, via = stack.pop()
            if i in seen:
                continue
            seen.add(i)
            out.append((i, depth, via))
            stack.extend((k, depth + 1, i) for k in reversed(kids.get(i, [])))
    return out


def cells(row, clip):
    return [str(row["description"][:DESC_CLIP] if clip and c == "description" else row[c]) for c in COLS]


def total_row(rows):
    sums = {c: sum(r[c] for r in rows) for c in ("calls", "errors", "denials", "compactions", "spawns")}
    starts = [r["t0"] for r in rows if r["t0"]]
    units, unpriced, failed = usd_count([r["usd"] for r in rows])
    return dict(id="TOTAL", engine="-", agent_type=f"{len(rows)} transcripts", description="-",
                start_utc=min(starts).strftime(STAMP) if starts else "-",
                wall_s=sum(r["wall_s"] for r in rows if isinstance(r["wall_s"], int)), **sums,
                usd=usd_sum(units, unpriced, failed),
                final="-", parent="-", path="-")


def tree_lines(rows, order):
    """The TREE lines: every node with its own calls and USD and its branch's (itself and all descendants)."""
    branch = [[r["calls"], *usd_count([r["usd"]])] for r in rows]
    for i, _, via in reversed(order):
        if via is not None:
            for k in range(4):
                branch[via][k] += branch[i][k]
    return [f"{'  ' * depth}{rows[i]['id']} {rows[i]['agent_type']} · calls {rows[i]['calls']} "
            f"(branch {branch[i][0]}) · {rows[i]['usd']} (branch {usd_sum(*branch[i][1:])})"
            for i, depth, _ in order]


def file_text(rows, total, notes):
    """The --out file: the header and every row whole, a path column last, then the FAILED and GAPS lines, so
    the file alone shows a census that missed a transcript or holds an unsure dollar."""
    body = [cells(r, False) + [r["path"]] for r in rows + [total]]
    return "\n".join(["\t".join(r) for r in [COLS + ["path"]] + body] + notes) + "\n"


def select(args):
    """(form, targets, hints, failures, tree, out): what the arguments name. `targets` are transcripts, or the
    PATH arguments themselves for the paths form; `hints` maps a target to the meta the selection read."""
    tree = "--tree" in args
    args[:] = [a for a in args if a != "--tree"]
    out = opt(args, "--out")
    typ, sess = opt(args, "--type"), opt(args, "--session")
    since, proj = opt(args, "--since"), opt(args, "--project")
    if any(a.startswith("--") for a in args) or (typ is not None) + (sess is not None) + bool(args) != 1 \
            or (typ is None and (since is not None or proj is not None)):
        die(USAGE, 2)
    if typ is not None:
        found, fails = locate.type_runs(typ, since or "30d", proj or "")
        return "type", [jp for _, jp, _ in found], {jp: meta for _, jp, meta in found}, fails, tree, out
    if sess is not None:
        try:
            members, fails = locate.session_members(locate.resolve(sess))
        except (locate.NotFound, opencode.StoreError) as e:
            die(f"FAILED {sess} — {str(e)[:600]}")
        return "session", members, {}, fails, tree, out
    return "paths", args, {}, [], tree, out


def census(args):
    began = time.time()
    form, targets, hints, fails, tree, out = select(list(args))
    reader, rows, seen = Reader(form), [], set()
    for t in targets:
        if form == "paths":
            try:
                t = locate.resolve(t)
            except locate.NotFound as e:
                fails.append((t, one_line(str(e))[:600]))
                continue
        key = locate.real(t) if os.path.exists(t) else t
        if key in seen:
            continue  # one row per real transcript, whatever root or alias named it
        seen.add(key)
        row, bad = reader.measure(t, hints.get(t))
        fails += bad
        if row:
            rows.append(row)
    root = next((i for i, r in enumerate(rows) if r["path"] == targets[0]), None) if form == "session" else None
    parents, bad = link(rows, root)
    fails += bad
    kids = children(rows, parents)
    roots = [i for i in range(len(rows)) if parents[i] is None]
    if form == "session":
        roots.sort(key=lambda i: (i != root, start_key(rows[i])))
        order = [i for i, _, _ in walk(roots, kids, len(rows))]
    elif form == "type":
        order = sorted(range(len(rows)), key=lambda i: start_key(rows[i]))
    else:
        order = list(range(len(rows)))
    total = total_row(rows)
    lines = ["\t".join(COLS)] + ["\t".join(cells(rows[i], True)) for i in order] + ["\t".join(cells(total, True))]
    notes = ([f"GAPS prices — {GAPS_LEAD}{reader.book_gaps}"] if reader.book_gaps and rows else []) \
        + [f"GAPS {rows[i]['path']} — {GAPS_LEAD}{rows[i]['gaps']}" for i in order if rows[i]["gaps"]]
    wrote = ""
    if out:
        try:
            with open(out, "w") as f:
                f.write(file_text([rows[i] for i in order], total,
                                  [f"FAILED {subject} — {reason}" for subject, reason in fails] + notes))
            wrote = f"OUT {out}"
        except OSError as e:
            fails.append((out, f"cannot write the census file: {e}"))
    lines += [f"FAILED {subject} — {reason}" for subject, reason in fails] + notes
    if tree:
        lines += ["TREE"] + tree_lines(rows, walk([i for i in order if parents[i] is None], kids, len(rows)))
    if wrote:
        lines.append(wrote)
    lines.append(f"# census {len(rows)} transcripts · {len(fails)} FAILED · {time.time() - began:.1f}s")
    print("\n".join(lines))
    if fails:
        sys.exit(1)
