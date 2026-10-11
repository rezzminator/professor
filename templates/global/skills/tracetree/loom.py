"""loom — the data model the Tracer Loom page draws, built from what `map --html` already has: the component map
(tracemap.data), the page data (tracetree.page_data) and the incidents (tracetree.incidents).

model() returns one JSON-able dict: the run (id, repo, base, clock, counts, warnings, notes, questions), its files
(path, line count, hops, broken hops, and the text — whole, or sparse past the page's text budget, or `gone` when no
text could be read), its components and edges, one thread per node (its steps in order with the stitch each one is,
its answers graded against their cites, its open ends), and the Hourglass's `focal` component and `all` toggle.
Pure: no file, no process, nothing from the network."""
import os
import re

HOME = os.path.expanduser("~")
CITE = re.compile(r"\[([^\[\]\s]+?):(\d+)(?:-(\d+))?\]")
STRUCT = re.compile(r"^\s*(?:·|→|■)\s*(LEAD|NOT READ|SPLIT|END)\s*([\d.]*)\s*:?\s*(.*?)(?:\s+@\s+(\S+?):(\d+))?(?:\s{2,}\(.*\))?\s*$")
# check_answer's flag and move lines, each naming the address it grades: unbacked, stale (≈), a bad address, a file
# that does not exist, and a cite re-addressed (`↻ p:a→n`) or added (`↻ +p:n`)
FLAG_AT = re.compile(r"(unbacked|≈|✗ BAD ADDRESS) (\S+?):(\d+)(?: — |$)")
NO_FILE = re.compile(r"✗ no such file (\S+)$")
MOVED_TO = re.compile(r"↻ \+?(\S+?):(\d+)(?:→(\d+))?$")
STRUCT_KEY ={"LEAD": "lead", "NOT READ": "notread", "SPLIT": "split", "END": "end"}
NEAR_CITE = 30
TEXT = ("lines", "sparse")  # a file's own text, never cleaned


def clean(value, root):
    """A string with the run's root and the home directory taken out, so the page names files as the repo does; a
    path that only shares the root's prefix keeps it."""
    base = root.rstrip("/")
    if base:
        value = re.sub(re.escape(base) + r"(/|(?![\w.-]))", lambda m: "" if m.group(1) else ".", value)
    return re.sub(re.escape(HOME) + r"(?![\w.-])", "~", value) if HOME not in ("", "/") else value


def scrub(value, root):
    """Every string of the model cleaned, except the files' own text, shown as the file holds it."""
    if isinstance(value, str):
        return clean(value, root)
    if isinstance(value, list):
        return [scrub(v, root) for v in value]
    if isinstance(value, dict):
        return {k: v if k in TEXT else scrub(v, root) for k, v in value.items()}
    return value


def struct_items(lines):
    """The open ends of a node: its LEAD, NOT READ, SPLIT and END lines, each with the place it names."""
    out = {"lead": [], "notread": [], "split": [], "end": []}
    for line in lines:
        m = STRUCT.match(line)
        if not m:
            continue
        kind, child, text, path, at = m.groups()
        key = STRUCT_KEY[kind]
        item = {"t": text.strip()[:220], "at": f"{path}:{at}" if path else None}
        if key == "split":
            item["child"] = child or None
            item["spawned"] = "not spawned" not in line
        if key == "end":
            km = re.match(r"(\w+):\s*(.*)", text)
            if km:
                item["k"], item["t"] = km.group(1).lower(), km.group(2).strip()[:220]
        out[key].append(item)
    return out


def grade(text, flags, moves, broke, steps, files):
    """An answer's cites each graded by its own address (held, moved, drift, or unbacked — a cite no hop backs, no
    file holds or past its file's end), tied to the nearest step of its own file, and the answer's overall grade:
    broke (it rests on a node that broke the hop contract), missing, unbacked, drift, moved, held, or uncited."""
    bad, gone, moved = {}, set(), set()
    for fl in flags:
        m = FLAG_AT.match(fl)
        if m:
            bad[(m.group(2), int(m.group(3)))] = "drift" if m.group(1) == "≈" else "unbacked"
        elif m := NO_FILE.match(fl):
            gone.add(m.group(1))
    for mv in moves:
        m = MOVED_TO.match(mv)
        if m:
            moved.add((m.group(1), int(m.group(3) or m.group(2))))
    cites = []
    for cm in CITE.finditer(text):
        path, ln = cm.group(1), int(cm.group(2))
        g = ("unbacked" if path in gone else bad.get((path, ln)) or ("moved" if (path, ln) in moved else "held"))
        hit, best = -1, 10 ** 9
        for si, s in enumerate(steps):
            if files[s["f"]]["p"] == path:
                d = 0 if s["a"] <= ln <= s["b"] else min(abs(ln - s["a"]), abs(ln - s["b"]))
                if d < best:
                    best, hit = d, si
        cites.append({"p": path, "l": ln, "g": g, "s": hit if best <= NEAR_CITE else -1})
    missing = [fl for fl in flags if fl.startswith("?")]
    grades = {c["g"] for c in cites}
    overall = ("broke" if broke else "missing" if missing else "unbacked" if "unbacked" in grades
               else "drift" if "drift" in grades else "moved" if "moved" in grades else "held" if cites else "uncited")
    body = CITE.sub("", text).strip()
    return {"t": body[:400], "cites": cites, "g": overall}


def model(run_id, root, data, pd, inc, focal=None, show_all=False):
    t0 = pd["times"]["new"] or 0
    files, file_idx = [], {}

    def fidx(path):
        if path not in file_idx:
            file_idx[path] = len(files)
            files.append({"p": path, "n": 0, "hops": 0, "bad": 0})
        return file_idx[path]

    comp_idx = {c["id"]: i for i, c in enumerate(data["components"])}
    names = list(pd["nodes"])
    tindex = {name: i for i, name in enumerate(names)}
    threads = []
    for name in names:
        pn = pd["nodes"][name]
        steps = []
        for s in pn["steps"]:
            f = fidx(s["path"])
            files[f]["bad" if s["c"] is None else "hops"] += 1
            steps.append({"f": f, "c": comp_idx.get(s["c"], -1) if s["c"] else -1, "a": s["a"], "b": s["b"], "st": s["st"],
                          "ln": s["ln"], "note": s["note"][:200], "flag": s["flag"][:160]})
        answers = []
        for a in pn["answers"]:
            graded = grade(a["text"], a["flags"], a["moves"], a["broke"], steps, files)
            graded["why"] = [f[:200] for f in a["flags"]] + [f"⚠ node {b} broke the hop contract" for b in a["broke"]]
            answers.append(graded)
        st = struct_items(pn["struct"])
        st["split"] = [x for x in st["split"] if not x["t"].lower().startswith("none")]
        qs = list(pn["questions"])
        threads.append({
            "id": name, "q": qs[0] if qs else None, "qs": qs, "text": pn["thread"][:500], "from": pn["from"] or "",
            "s": round(pn["started"] - t0, 1) if pn["started"] else None,
            "w": round(pn["written"] - t0, 1) if pn["written"] else None,
            "refused": pn["refused"], "parent": name.rsplit(".", 1)[0] if "." in name else None,
            "kids": data["threads"].get(name, {}).get("children", []), "ok": pn["verified"], "steps": steps,
            "answers": answers, **st, "placed": pn["placed"], "miscopied": pn["miscopied"], "followed": pn["followed"]})
    by_id = {t["id"]: t for t in threads}
    for t in threads:  # a child's question is its root's
        p = t
        while t["q"] is None and p["parent"] in by_id:
            p = by_id[p["parent"]]
            t["q"] = p["q"]
    for c in data["components"]:
        fidx(c["file"])
    shown = {f["path"]: f for f in pd["files"]}
    for i, fl in enumerate(files):
        src = shown.get(fl["p"])
        if src:
            fl["n"] = src["count"]
            if src["lines"] is not None:
                fl["lines"] = src["lines"]
            else:
                fl["sparse"] = src["sparse"]
        else:
            fl["n"] = max([s["b"] for t in threads for s in t["steps"] if s["f"] == i] + [1])
            fl["gone"] = True
    comps = [{"id": c["id"], "f": file_idx[c["file"]], "name": c["name"], "kind": c["kind"], "a": c["line"], "z": c["end"],
              "hops": c["hops"], "th": [tindex[x] for x in c["threads"] if x in tindex],
              "ends": [{"k": e["kind"], "note": e["note"], "th": [tindex[x] for x in e["threads"] if x in tindex]}
                       for e in c["ends"]]} for c in data["components"]]
    edges = [[comp_idx[e["from"]], comp_idx[e["to"]], e["kind"], e["note"], [tindex[x] for x in e["threads"] if x in tindex]]
             for e in data["edges"] if e["from"] in comp_idx and e["to"] in comp_idx]
    done = pd["times"]["done"]
    out = {"id": run_id, "repo": pd["title"], "base": (pd["base"] or "")[:12], "t0": t0,
           "dur": round(done - t0, 1) if done else None, "counts": pd["counts"], "warnings": inc["lines"], "notes": inc["notes"],
           "questions": {k: v[:600] for k, v in pd["questions"].items()}, "files": files, "comps": comps, "edges": edges,
           "threads": threads, "missing": {k: round(v - t0, 1) if v else None for k, v in pd["missing"].items()},
           "focal": comp_idx.get(focal), "all": bool(show_all)}
    return scrub(out, root)
