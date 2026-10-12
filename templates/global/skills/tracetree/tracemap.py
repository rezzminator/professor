"""tracemap — the component map of a trace run, built from its verified hops; imported by tracetree.py.

A component is the function, method, type, shell function, make target or markdown section that holds a verified
hop, or the file itself for a hop outside any. Every link is read off the files, never off a node's prose:

  A → B    call: B's name, in call shape, is in A's body, and the thread moved between them
  A ⇢ B    names: B's file name is in A's body, and the thread moved between them
  A ⇉ B    split: A's node handed the thread to a child whose first verified hop is in B
  A ■ kind end: the thread leaves the code in A — returned, written, sent, executed, printed, dropped
  A · B    then: consecutive hops with no such link — walk order only, shown on request

A rejected hop is no evidence and builds nothing. Python 3 standard library only.
"""
import os
import re

BODY_CAP = 800
WINDOW = 20
NOTE_CAP = 80
HOPS_SHOWN = 10
MATCH_CAP = 6

GO, PY, JS, SH, MAKE, MD = "go", "py", "js", "sh", "make", "md"
EXT = {".go": GO, ".py": PY, ".js": JS, ".mjs": JS, ".cjs": JS, ".jsx": JS, ".ts": JS, ".tsx": JS, ".mts": JS,
       ".sh": SH, ".bash": SH, ".zsh": SH, ".mk": MAKE, ".md": MD}
MAKEFILES = {"Makefile", "makefile", "GNUmakefile"}
# (pattern, kind): the component's name is the pattern's last group.
DEFS = {
    GO: [(re.compile(r"^func\s+\([^)]*\)\s*([A-Za-z_]\w*)"), "method"),
         (re.compile(r"^func\s+([A-Za-z_]\w*)"), "func"),
         (re.compile(r"^type\s+([A-Za-z_]\w*)"), "type")],
    PY: [(re.compile(r"^(\s*)(?:async\s+)?def\s+([A-Za-z_]\w*)"), "func"),
         (re.compile(r"^(\s*)class\s+([A-Za-z_]\w*)"), "class")],
    JS: [(re.compile(r"^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)"), "func"),
         (re.compile(r"^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s+)?"
                     r"(?:function\b|\([^)]*\)\s*=>|[A-Za-z_$][\w$]*\s*=>)"), "func"),
         (re.compile(r"^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][\w$]*)"), "class"),
         (re.compile(r"^\s+(?:static\s+)?(?:async\s+)?(?!(?:if|for|while|switch|catch|return|function)\b)"
                     r"([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*\{"), "func")],
    SH: [(re.compile(r"^\s*(?:function\s+)?([A-Za-z_][\w:-]*)\s*\(\)"), "func"),
         (re.compile(r"^\s*function\s+([A-Za-z_][\w:-]*)"), "func")],
    MAKE: [(re.compile(r"^([A-Za-z0-9_][A-Za-z0-9_./%-]*)(?:\s+[A-Za-z0-9_./%-]+)*\s*::?(?!=)"), "target")],
    MD: [(re.compile(r"^(#{1,6})\s+(.+?)\s*#*\s*$"), "section")],
}
COMMENT = {GO: "//", JS: "//", PY: "#", SH: "#", MAKE: "#"}
ARROW = {"call": "→", "names": "⇢", "split": "⇉", "then": "·"}
BACK = {"call": "←", "names": "⇠", "split": "⇇", "then": "·"}
ORDER = {"split": 0, "call": 1, "names": 2, "then": 3}
KEY = ("Key: A → B A's body calls B · A ⇢ B A's body names B's file · A ⇉ B A's node split the thread to a child "
       "whose first hop is in B · ■ kind the thread leaves the code there · A · B walk order, no name link · "
       "hops: verified hop lines, re-read from disk")


class MapError(Exception):
    """A map request that cannot be answered: no component matches a name, or too many do."""


def lang(path):
    base = os.path.basename(path)
    return MAKE if base in MAKEFILES else EXT.get(os.path.splitext(base)[1].lower())


def indent(line):
    return len(line) - len(line.lstrip())


def fences(lines):
    """The 0-based lines inside fenced code blocks of a markdown file, fence lines included."""
    inside, out, mark = False, set(), None
    for i, line in enumerate(lines):
        s = line.lstrip()
        if s.startswith(("```", "~~~")):
            if not inside:
                inside, mark = True, s[:3]
                out.add(i)
                continue
            if s.startswith(mark):
                inside = False
                out.add(i)
                continue
        if inside:
            out.add(i)
    return out


def enclosing(lines, i, lg, fenced, back=None):
    """(name, 0-based start line, kind) of the component holding line i; (None, None, "file") at file level. With
    `back`, a component opening more than that many lines above i is not looked for: file level."""
    defs = DEFS.get(lg)
    if not defs:
        return None, None, "file"
    stop = -1 if back is None else max(-1, i - back - 1)
    if lg == PY:
        return py_enclosing(lines, i, stop)
    for j in range(i, stop, -1):
        if j in fenced:
            continue
        line = lines[j]
        for rx, kind in defs:
            m = rx.match(line)
            if m:
                return m.groups()[-1].strip()[:60], j, kind
        if j < i and (line.startswith("}") if lg in (GO, JS, SH) else
                      lg == MAKE and line.strip() and not line.startswith(("\t", "#"))):
            return None, None, "file"
    return None, None, "file"


def py_enclosing(lines, i, stop=-1):
    """The nearest def or class indented less than line i (or on line i itself), above `stop`; file level past an
    unindented statement."""
    want = indent(lines[i]) if lines[i].strip() else None
    for j in range(i, stop, -1):
        s = lines[j]
        if not s.strip():
            continue
        for rx, kind in DEFS[PY]:
            m = rx.match(s)
            if m and (j == i or want is None or len(m.group(1)) < want):
                return m.group(2), j, kind
        if j < i and indent(s) == 0 and not s.startswith(("#", "@", ")", "]", "}")):
            return None, None, "file"
        if j == i and want == 0:
            return None, None, "file"
    return None, None, "file"


def extent(lines, start, lg, fenced):
    """The lines of the component opening at start: to its closing brace, its dedent, its next sibling heading or
    target — at most BODY_CAP lines."""
    end = min(len(lines), start + BODY_CAP)
    head = lines[start]
    if lg in (GO, JS, SH):
        if "{" in head and head.count("{") == head.count("}"):
            return lines[start:start + 1]
        if lg == GO and head.startswith("type ") and "{" not in head:
            return lines[start:start + 1]
        at = indent(head)
        for k in range(start + 1, end):
            if indent(lines[k]) == at and lines[k].lstrip().startswith("}"):
                return lines[start:k + 1]
    elif lg == PY:
        at = indent(head)
        for k in range(start + 1, end):
            s = lines[k]
            if s.strip() and indent(s) <= at and not s.lstrip().startswith("#"):
                return lines[start:k]
    elif lg == MAKE:
        for k in range(start + 1, end):
            if lines[k].strip() and not lines[k].startswith("\t"):
                return lines[start:k]
    elif lg == MD:
        level = len(head) - len(head.lstrip("#"))
        for k in range(start + 1, end):
            if k not in fenced and DEFS[MD][0][0].match(lines[k]) and len(lines[k]) - len(lines[k].lstrip("#")) <= level:
                return lines[start:k]
    return lines[start:end]


def code(lines, lg):
    """Lines as text, whole-line comments dropped: a name in a comment calls nothing."""
    mark = COMMENT.get(lg)
    return "\n".join(l for l in lines if not (mark and l.lstrip().startswith(mark)))


def call_shape(c, caller):
    """The pattern that finds component c called from caller's body. A Go function is called bare inside its own
    package and as `pkg.Name(` from another, so a same-named method call (`cmd.Run()`) is no call to it."""
    n, lg = re.escape(c["name"]), c["_lang"]
    if c["kind"] in ("type", "class"):
        return re.compile(rf"(?<![\w$]){n}(?![\w$])")
    if lg == GO and c["kind"] == "func":
        here = os.path.dirname(c["file"])
        if os.path.dirname(caller["file"]) == here:
            return re.compile(rf"(?<![\w.$]){n}\s*\(")
        return re.compile(rf"(?<![\w$]){re.escape(os.path.basename(here))}\.{n}\s*\(")
    if lg in (GO, PY, JS):
        return re.compile(rf"(?<![\w$]){n}\s*\(")
    if lg == SH:
        return re.compile(rf"(?:^|[\s;&|(`{{]){n}(?=$|[\s;&|)`])", re.M)
    if lg == MAKE:
        return re.compile(rf"(?:\bmake\b|\$[({{]MAKE[)}}])[^\n]*?(?<![\w./-]){n}(?![\w./-])"
                          rf"|^[\w./%-]+[^:=\n]*::?[^=\n]*?(?<![\w./-]){n}(?![\w./-])", re.M)
    return re.compile(re.escape(c["name"]), re.I)


class Builder:
    def __init__(self, run, root, names, nodes, files):
        self.files, self.root = files, root
        self.rank = {name: i for i, name in enumerate(names)}
        self.g = {"run": run, "root": root, "rank": self.rank, "components": {}, "edges": {}, "threads": {}}
        self.meta, self.bodies, self.splits, self.back = {}, {}, [], {}
        for name in names:
            self.walk(name, nodes[name])
        for parent, cid, child in self.splits:
            entry = self.g["threads"].get(child, {}).get("entry")
            if entry:
                self.g["threads"][parent]["children"].append(child)
                self.edge(cid, entry, "split", f"split to {child}", parent)

    def file_meta(self, key):
        if key not in self.meta:
            lines = self.files.lines(key) or []
            lg = lang(key)
            self.meta[key] = (lines, lg, fences(lines) if lg == MD else set())
            # a file the kit searches only near an address: no component is looked for further up than one body
            self.back[key] = BODY_CAP if self.files.reach(key) else None
        return self.meta[key]

    def locate(self, key, line, make):
        """The component holding key:line (1-based), created when make is set; None for a line out of range."""
        lines, lg, fenced = self.file_meta(key)
        if not 1 <= line <= len(lines):
            return None
        name, start, kind = enclosing(lines, line - 1, lg, fenced, self.back[key])
        rel = os.path.relpath(key, self.root)
        cid = f"{rel}::{name}" if name else rel
        comps = self.g["components"]
        if cid not in comps and make:
            comps[cid] = {"id": cid, "file": rel, "name": name, "kind": kind,
                          "line": start + 1 if start is not None else None,
                          "end": start + len(extent(lines, start, lg, fenced)) if start is not None else None,
                          "hops": set(), "threads": set(), "ends": [], "_key": key, "_lang": lg, "_start": start}
        return comps.get(cid)

    def body(self, c, line):
        """The text a link test reads in c: its whole body, or a window around line for a file-level component."""
        lines, lg, fenced = self.file_meta(c["_key"])
        if c["_start"] is None:
            return code(lines[max(0, line - 1 - WINDOW):line + WINDOW], lg)
        if c["id"] not in self.bodies:
            self.bodies[c["id"]] = code(extent(lines, c["_start"], lg, fenced), lg)
        return self.bodies[c["id"]]

    def names_file(self, a, aline, b):
        base = re.escape(os.path.basename(b["file"]))
        return re.search(rf"(?<![\w.-]){base}(?![\w-])", self.body(a, aline)) is not None

    def link(self, p, pline, pnote, c, cline, cnote, tid):
        """Record the link between consecutive components p and c; returns its kind: call, names or then."""
        if c["name"] and call_shape(c, p).search(self.body(p, pline)):
            self.edge(p["id"], c["id"], "call", cnote, tid)
            return "call"
        if p["name"] and call_shape(p, c).search(self.body(c, cline)):
            self.edge(c["id"], p["id"], "call", pnote, tid)
            return "call"
        if p["file"] != c["file"] and self.names_file(p, pline, c):
            self.edge(p["id"], c["id"], "names", cnote, tid)
            return "names"
        if p["file"] != c["file"] and self.names_file(c, cline, p):
            self.edge(c["id"], p["id"], "names", pnote, tid)
            return "names"
        self.edge(p["id"], c["id"], "then", cnote, tid)
        return "then"

    def edge(self, a, b, kind, note, tid):
        if a == b:
            return
        e = self.g["edges"].setdefault((a, b, kind), {"from": a, "to": b, "kind": kind, "note": "", "threads": set()})
        e["threads"].add(tid)
        if note and not e["note"]:
            e["note"] = note[:NOTE_CAP]

    def walk(self, name, n):
        steps = []
        self.g["threads"][name] = {"thread": re.sub(r"^THREAD\s+[\w.]+:\s*", "", n["head"]), "entry": None,
                                   "children": [], "walk": steps}
        prev = None  # (component, line, note)
        for w in n["walk"]:
            if w["t"] == "hop":
                c = self.locate(w["key"], w["line"], True)
                if c is None:
                    continue
                c["hops"].add(w["line"])
                c["threads"].add(name)
                kind = None if prev is None else "same" if prev[0] is c else \
                    self.link(prev[0], prev[1], prev[2], c, w["line"], w["note"], name)
                steps.append({"c": c["id"], "line": w["line"], "b": w.get("b", w["line"]), "note": w["note"],
                              "status": w.get("status", "verified"), "flag": w.get("flag", ""), "link": kind})
                prev = (c, w["line"], w["note"])
                continue
            # SPLIT and END addresses are not quote-checked: they land on a component a verified hop built,
            # else on the node's last verified hop
            c = self.locate(w["key"], w["line"], False) or (prev[0] if prev else None)
            if c is None:
                continue
            if w["t"] == "split":
                self.splits.append((name, c["id"], w["child"]))
            else:
                for e in c["ends"]:
                    if e["kind"] == w["kind"] and e["note"] == w["note"]:
                        e["threads"].add(name)
                        break
                else:
                    c["ends"].append({"kind": w["kind"], "note": w["note"][:NOTE_CAP], "threads": {name}})
        self.g["threads"][name]["entry"] = steps[0]["c"] if steps else None


def build(run, root, names, nodes, files):
    """The map of a loaded run: names in node order, nodes as tracetree.verify returns them (each with its walk)."""
    return Builder(run, root, names, nodes, files).g


def by_rank(g, ids):
    return sorted(ids, key=lambda t: g["rank"].get(t, len(g["rank"])))


def shown_edges(g, show_all):
    return [e for e in g["edges"].values() if show_all or e["kind"] != "then"]


def tally(g, show_all):
    comps = g["components"].values()
    kinds = {}
    for e in g["edges"].values():
        kinds[e["kind"]] = kinds.get(e["kind"], 0) + 1
    return dict(components=len(g["components"]), files=len({c["file"] for c in comps}), call=kinds.get("call", 0),
                names=kinds.get("names", 0), split=kinds.get("split", 0), then=kinds.get("then", 0),
                end=sum(len(c["ends"]) for c in comps), threads=len(g["threads"]))


def summary(g):
    t = tally(g, False)
    return (f"{t['components']} components in {t['files']} files, {t['call']} calls, {t['names']} file names, "
            f"{t['split']} splits, {t['end']} ends" + hub(g))


def hub(g):
    """The component crossed by the most threads (ties: more hops, then id), as a MAP-line suffix; empty with no components."""
    if not g["components"]:
        return ""
    c = min(g["components"].values(), key=lambda c: (-len(c["threads"]), -len(c["hops"]), c["id"]))
    return f" · hub {c['id']} ({len(c['threads'])} threads, {len(c['hops'])} hops)"


def hop_list(hops):
    hs = sorted(hops)
    more = f",…+{len(hs) - HOPS_SHOWN}" if len(hs) > HOPS_SHOWN else ""
    return ",".join(str(h) for h in hs[:HOPS_SHOWN]) + more if hs else "-"


def where(c):
    """The component's own line: its definition, else its first hop."""
    return c["line"] or min(c["hops"] or {0})


def text(g, show_all=False):
    t = tally(g, show_all)
    hidden = f" · {t['then']} walk-order links hidden (--all shows them)" if t["then"] and not show_all else ""
    out = [f"MAP {g['run']} · repo {g['root']} · {t['components']} components in {t['files']} files · "
           f"links: {t['call']} call, {t['names']} names, {t['split']} split, {t['end']} end"
           + (f", {t['then']} then" if show_all else "") + f"{hidden} · {t['threads']} threads", KEY, "", "THREADS"]
    for tid, th in g["threads"].items():
        out.append(f"  {tid}: {th['thread'][:100]} — enters at {th['entry'] or '(no verified hop)'}")
    outs, by_file = {}, {}
    for e in shown_edges(g, show_all):
        outs.setdefault(e["from"], []).append(e)
    for c in g["components"].values():
        by_file.setdefault(c["file"], []).append(c)
    for f in sorted(by_file, key=lambda f: (-sum(len(c["hops"]) for c in by_file[f]), f)):
        out += ["", f]
        for c in sorted(by_file[f], key=where):
            at = f" :{c['line']}" if c["line"] else ""
            out.append(f"  {c['name'] or '(file level)'}{at} · hops {hop_list(c['hops'])} · "
                       f"threads {','.join(by_rank(g, c['threads']))}")
            for e in sorted(outs.get(c["id"], []), key=lambda e: (ORDER[e["kind"]], e["to"])):
                out.append(f"    {ARROW[e['kind']]} {e['to']}" + (f" — {e['note']}" if e["note"] else ""))
            for end in c["ends"]:
                out.append(f"    ■ {end['kind']}" + (f" — {end['note']}" if end["note"] else ""))
    return "\n".join(out) + "\n"


def resolve(g, name):
    """The component ids a name picks: an exact id, else every component so named, else every component of a file
    so named, else every id holding it (case-insensitive)."""
    comps = g["components"]
    if name in comps:
        return [name]
    for test in (lambda c: c["name"] == name, lambda c: c["file"] == name or c["file"].endswith("/" + name),
                 lambda c: name.lower() in c["id"].lower()):
        hits = sorted(c["id"] for c in comps.values() if test(c))
        if hits:
            break
    if not hits:
        raise MapError(f"no component matches {name} — `map RUN` lists all {len(comps)}")
    if len(hits) > MATCH_CAP:
        raise MapError(f"{name} matches {len(hits)} components ({', '.join(hits[:MATCH_CAP])}, …) — name one by its id")
    return hits


def adjacency(g, direction, show_all):
    adj = {}
    for e in shown_edges(g, show_all):
        a, b = (e["from"], e["to"]) if direction == "from" else (e["to"], e["from"])
        adj.setdefault(a, []).append((b, e["kind"]))
    return adj


def closure(g, starts, direction, show_all):
    adj, seen, todo = adjacency(g, direction, show_all), set(starts), list(starts)
    while todo:
        for b, _ in adj.get(todo.pop(), []):
            if b not in seen:
                seen.add(b)
                todo.append(b)
    return seen


def label(c):
    return f"{c['name'] or '(file level)'}  {c['file']}:{where(c)}"


def tree(g, name, direction, show_all=False):
    """Each matching component with every component below it (--from: what it reaches) or above it (--to: what
    reaches it), one branch per link; a component met twice is printed once, then marked (above)."""
    comps, adj = g["components"], adjacency(g, direction, show_all)
    arrows = ARROW if direction == "from" else BACK
    blocks = []
    for start in resolve(g, name):
        reach = closure(g, [start], direction, show_all)
        head = "below" if direction == "from" else "above"
        lines, seen = [f"{direction.upper()} {start} — {len(reach) - 1} components {head}", label(comps[start])], {start}

        def grow(cid, prefix):
            if direction == "from":
                for end in comps[cid]["ends"]:
                    lines.append(f"{prefix}■ {end['kind']}" + (f" — {end['note']}" if end["note"] else ""))
            kids = sorted(adj.get(cid, []), key=lambda k: (ORDER[k[1]], k[0]))
            for i, (other, kind) in enumerate(kids):
                last = i == len(kids) - 1
                conn = "└─ " if last else "├─ "
                if other in seen:
                    lines.append(f"{prefix}{conn}{arrows[kind]} {label(comps[other])}  (above)")
                    continue
                seen.add(other)
                lines.append(f"{prefix}{conn}{arrows[kind]} {label(comps[other])}")
                grow(other, prefix + ("   " if last else "│  "))

        grow(start, "")
        blocks.append("\n".join(lines))
    return "\n\n".join(blocks) + "\n"


def data(g, show_all=False, keep=None):
    """The map as plain data — components (each with its last line, `end`), links, threads with their walks, each
    step's verified range `line`–`b`, its `status` (verified, moved, placed, followed), the check's `flag` and its `link`
    from the step before it (call, names, then, same within one component; null on the first) — restricted to keep
    when given."""
    def public(c):
        return {"id": c["id"], "file": c["file"], "name": c["name"], "kind": c["kind"], "line": c["line"], "end": c["end"],
                "hops": sorted(c["hops"]), "threads": by_rank(g, c["threads"]),
                "ends": [{"kind": e["kind"], "note": e["note"], "threads": by_rank(g, e["threads"])} for e in c["ends"]]}
    comps = [public(c) for cid, c in sorted(g["components"].items()) if keep is None or cid in keep]
    edges = [{"from": e["from"], "to": e["to"], "kind": e["kind"], "note": e["note"], "threads": by_rank(g, e["threads"])}
             for e in sorted(shown_edges(g, show_all), key=lambda e: (e["from"], e["to"], ORDER[e["kind"]]))
             if keep is None or (e["from"] in keep and e["to"] in keep)]
    threads = {tid: {"thread": th["thread"], "entry": th["entry"], "children": th["children"],
                     "walk": [s for s in th["walk"] if keep is None or s["c"] in keep]}
               for tid, th in g["threads"].items()}
    return {"run": g["run"], "root": g["root"], "components": comps, "edges": edges, "threads": threads}


def focus(g, start_from, start_to, show_all):
    """The component ids --from and --to keep: each match with everything below (from) or above (to) it."""
    keep = set()
    for name, direction in ((start_from, "from"), (start_to, "to")):
        if name:
            keep |= closure(g, resolve(g, name), direction, show_all)
    return keep or None
