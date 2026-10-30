"""The reader's verbs over a prepared directory: read, find, note, report, verify, prompts, writes.
A prepared directory holds kit.json, digest.txt, timeline.txt, mech.tsv (the kit's rows) and notes.tsv
(the reader's). Rows are tab-separated: id (D{n} digest line, M{n} timeline line), text, label, ref."""
import json
import os
import re
import sys

from .digest import EVENT_LINE as EVENT
from .writes import file_state, resolve_writes

READER = {"CORRECTION", "CLAIM", "BEARS", "WRITE"}


def die(msg, code=1):
    print(msg)
    sys.exit(code)


def opt(args, name, default=None):
    if name in args:
        i = args.index(name)
        if i + 1 >= len(args):
            die(f"KIT FAILED — {name} needs a value")
        v = args[i + 1]
        del args[i:i + 2]
        return v
    return default


def load(dirp):
    try:
        with open(os.path.join(dirp, "kit.json")) as f:
            kit = json.load(f)
        with open(os.path.join(dirp, "digest.txt"), errors="replace") as f:
            dig = f.read().split("\n")
    except OSError as e:
        die(f"KIT FAILED — {dirp} is not a prepared directory: {e}")
    if dig and dig[-1] == "":
        dig.pop()
    return kit, dig


def read_rows(path):
    rows = []
    try:
        with open(path, errors="replace") as f:
            for ln in f:
                ln = ln.rstrip("\n")
                if ln:
                    rows.append((ln.split("\t") + ["-", "-", "-"])[:4])
    except FileNotFoundError:
        pass
    return rows


# ---------------------------------------------------------------- read / find

def labels_by_line(dirp):
    lab = {}
    for f in ("mech.tsv", "notes.tsv"):
        for r in read_rows(os.path.join(dirp, f)):
            if r[0].startswith("D"):
                lab.setdefault(r[0], []).append(r[1] if r[2] in ("-", "") else f"{r[1]}>{r[2]}")
    return lab


def show(dig, lab, a, b):
    for n in range(max(a, 1), min(b, len(dig)) + 1):
        t = lab.get(f"D{n}")
        print(f"D{n}" + (f" ‹{','.join(t)}›" if t else "") + " " + dig[n - 1])


def read(args):
    """A window (N), digest lines (A-B, D-prefixed or bare) or transcript lines (LA-LB, mapped to the
    digest lines that render them, result tails included); a selector outside the digest is FAILED."""
    usage = "usage: chatlog.py read DIR N|A-B|DA-DB|LA-LB"
    if len(args) != 2:
        die(usage, 2)
    kit, dig = load(args[0])
    sel = args[1].strip()
    m = re.fullmatch(r"([DdLl]?)(\d+)(?:-[DdLl]?(\d*))?", sel)
    if not m:
        die(f"KIT FAILED — read {sel!r}: not a window, D range or L range ({usage})")
    kind, a, b = m.group(1).upper(), int(m.group(2)), m.group(3)
    if kind == "" and b is None:
        n = len(kit["windows"])
        if not 1 <= a <= n:
            die(f"KIT FAILED — window {a}: there are {n}")
        a, b = kit["windows"][a - 1]
    elif kind == "L":
        b = int(b) if b else a
        ev = [(i, int(x.group(1))) for i, x in ((i, re.match(r"L(\d+) ", l)) for i, l in enumerate(dig, 1)) if x]
        hit = [i for i, n in ev if a <= n <= b]
        if not hit:
            span = f"L{ev[0][1]}-L{ev[-1][1]}" if ev else "no L lines"
            die(f"KIT FAILED — read L{a}-L{b}: no digest line renders those transcript lines: dropped or collapsed records (the DROPPED rows count them; the raw record: pfm transcript show PATH --lines {a}-{b} --results full), digest covers {span}")
        nxt = [i for i, _ in ev if i > hit[-1]]
        a, b = hit[0], (nxt[0] - 1 if nxt else len(dig))
    else:
        b = int(b) if b else (a if b is None else len(dig))
        if not 1 <= a <= len(dig) or b < a:
            die(f"KIT FAILED — read D{a}-D{b}: the digest has D1-D{len(dig)}")
        b = min(b, len(dig))
    show(dig, labels_by_line(args[0]), a, b)


def find(args):
    ctx = int(opt(args, "-C", "0"))
    if len(args) != 2:
        die("usage: chatlog.py find DIR REGEX [-C N]", 2)
    _, dig = load(args[0])
    try:
        rx = re.compile(args[1], re.I)
    except re.error as e:
        die(f"KIT FAILED — bad regex: {e}")
    lab, last, hits = labels_by_line(args[0]), 0, 0
    for n, ln in enumerate(dig, 1):
        if rx.search(ln):
            hits += 1
            a = max(n - ctx, last + 1)
            if ctx and a > last + 1 and last:
                print("--")
            show(dig, lab, a, n + ctx)
            last = n + ctx
    print(f"# hits {hits}")


# ---------------------------------------------------------------- note / report / verify

def parse_row(s):
    s = s.strip()
    if "\t" in s:
        p = (s.split("\t") + ["-", "-", "-"])[:4]
    else:
        t = s.split(" ", 3)
        p = (t + ["-", "-", "-"])[:4]
    return [x if x else "-" for x in p]


def note(args):
    if not args:
        die("usage: chatlog.py note DIR ROW... (or rows on stdin)", 2)
    dirp, raw = args[0], args[1:]
    if not raw:
        raw = [x for x in sys.stdin.read().split("\n") if x.strip()]
    _, dig = load(dirp)
    have = {(r[0], r[1]) for r in read_rows(os.path.join(dirp, "notes.tsv"))}
    ok, bad = [], []
    for s in raw:
        line, label, ref, anchor = parse_row(s)
        why = None
        m = re.fullmatch(r"D(\d+)", line)
        if not m or not 1 <= int(m.group(1)) <= len(dig):
            why = f"line {line} is not a digest line D1..D{len(dig)}"
        elif label not in READER and not re.fullmatch(r"O[0-9][0-9]?", label):
            why = f"label {label} is not CORRECTION, CLAIM, BEARS, WRITE or O{{n}}"
        elif ref != "-" and not (re.fullmatch(r"D(\d+)", ref) and 1 <= int(ref[1:]) <= len(dig)):
            why = f"ref {ref} is not a digest line"
        elif label == "BEARS" and ref == "-":
            why = "BEARS needs the D{n} of the CLAIM it bears on"
        elif anchor != "-" and anchor not in dig[int(m.group(1)) - 1]:
            why = f"anchor not found in {line}: copy it exactly from that line"
        if why:
            bad.append(f"REFUSED {s.strip()} — {why}")
        elif (line, label) not in have:
            have.add((line, label))
            ok.append("\t".join([line, label, ref, anchor]))
    if ok:
        with open(os.path.join(dirp, "notes.tsv"), "a") as f:
            f.write("\n".join(ok) + "\n")
    print("\n".join(bad))
    print(f"# noted {len(ok)} · refused {len(bad)}")
    sys.exit(1 if bad else 0)


def report(args):
    out = opt(args, "--out")
    if len(args) != 1:
        die("usage: chatlog.py report DIR [--out FILE]", 2)
    dirp = args[0]
    kit, dig = load(dirp)
    try:
        with open(os.path.join(dirp, "timeline.txt"), errors="replace") as f:
            tl = f.read().splitlines()
    except OSError as e:
        die(f"KIT FAILED — cannot read required timeline {os.path.join(dirp, 'timeline.txt')}: {e}")
    out = out or os.path.join(dirp, "report.tsv")
    rows, seen, counts = [], set(), {}
    for r in read_rows(os.path.join(dirp, "mech.tsv")) + read_rows(os.path.join(dirp, "notes.tsv")):
        idx, label, ref, anchor = r
        if (idx, label) in seen:
            continue
        seen.add((idx, label))
        if not re.fullmatch(r"[DM][1-9][0-9]*", idx):
            die(f"KIT FAILED — invalid evidence row {idx!r} in {dirp}")
        src = dig if idx[0] == "D" else tl
        n = int(idx[1:])
        if n > len(src) or not src[n - 1].strip():
            source = "digest.txt" if idx[0] == "D" else "timeline.txt"
            die(f"KIT FAILED — missing evidence for {idx} in {os.path.join(dirp, source)}")
        s = src[n - 1]
        if anchor not in ("-", "") and anchor in s:
            s = s[s.index(anchor):]
        s = s[:200].split("\t")[0]
        rows.append(((0 if idx[0] == "D" else 1), n, idx, s, label, ref))
        counts[label] = counts.get(label, 0) + 1
    rows.sort()
    dd = os.path.abspath(dirp)
    with open(out, "w") as f:
        f.write(f"# target {kit['target']}\n# path {kit['path']}\n# lines {kit['lines']}\n"
                f"# digest {dd}/digest.txt\n# timeline {dd}/timeline.txt\n")
        f.write("# tools " + " · ".join(f"{k} {v}" for k, v in sorted(kit["tools"].items(), key=lambda x: -x[1])) + "\n")
        for r in rows:
            f.write("\t".join(str(x) for x in r[2:]) + "\n")
        for k in sorted(counts):
            f.write(f"# count {k} {counts[k]}\n")
        f.write(f"# rows {len(rows)}\n")
    print(f"READ {out} · {len(rows)} rows")


def verify(args):
    pr = "--print" in args
    args = [a for a in args if a != "--print"]
    files = []
    for p in args:
        if os.path.isdir(p):
            for dp, _, fs in os.walk(p):
                top = dp == p
                files += sorted(os.path.join(dp, f) for f in fs
                                if f == "report.tsv" or (top and f.endswith(".tsv") and f not in ("mech.tsv", "notes.tsv")))
        else:
            files.append(p)
    if not files:
        die("FAILED — no report given or found", 1)
    tok = tbad = fails = 0
    for fp in files:
        try:
            with open(fp, errors="replace") as f:
                lines = f.read().rstrip("\n").split("\n")
        except OSError as e:
            print(f"FAILED {fp} — {e}")
            fails += 1
            continue
        hdr = {l[2:].split(" ", 1)[0]: l[2:].split(" ", 1)[1] for l in lines if l.startswith("# ") and " " in l[2:]}
        srcs = {}
        for k, key in (("D", "digest"), ("M", "timeline")):
            try:
                with open(hdr[key], errors="replace") as f:
                    srcs[k] = f.read().split("\n")
            except (KeyError, OSError) as e:
                srcs[k] = None
                srcs[k + "err"] = f"{key} unreadable: {e}"
        ok = bad = 0
        kept = []
        for l in lines:
            if not l or l.startswith("#"):
                continue
            c = l.split("\t")
            if len(c) < 2 or not re.fullmatch(r"[DM]\d+", c[0]):
                print(f"BAD {fp} {c[0]} — not a row")
                bad += 1
                continue
            src = srcs.get(c[0][0])
            if src is None:
                print(f"FAILED {fp} {c[0]} — {srcs.get(c[0][0] + 'err')}")
                fails += 1
                continue
            n = int(c[0][1:])
            if 1 <= n <= len(src) and c[1] in src[n - 1]:
                ok += 1
                kept.append(l)
            else:
                print(f"BAD {fp} {c[0]} — text not in that source line")
                bad += 1
        print(f"VERIFY {fp} · OK {ok} · BAD {bad}")
        tok, tbad = tok + ok, tbad + bad
        if pr:
            print("\n".join(kept))
    print(f"# verified {len(files)} reports · OK {tok} · BAD {tbad} · FAILED {fails}")
    sys.exit(1 if tbad or fails else 0)


# ---------------------------------------------------------------- prompts / writes / runs

def prompts(args):
    if len(args) != 1:
        die("usage: chatlog.py prompts DIR", 2)
    _, dig = load(args[0])
    last, n = None, 0
    for i, ln in enumerate(dig, 1):
        m = EVENT.match(ln)
        if not m:
            continue
        k = m.group(3)
        if k.startswith("PROMPT"):
            n += 1
            if last:
                print(f"  before D{last} {dig[last - 1][:240]}")
            print(f"D{i} {ln[:600]}")
        elif k in ("SAY", "FINAL"):
            last = i
    print(f"# prompts {n}")


def writes(args):
    if len(args) != 1:
        die("usage: chatlog.py writes DIR", 2)
    kit, _ = load(args[0])
    seen = resolve_writes(kit)
    for ap, ds in seen.items():
        if ap.endswith("\tUNRESOLVED"):
            print(f"{ap}\t{' '.join(ds)}")
            continue
        state = file_state(ap)
        print(f"{ap}\t{state}\t{' '.join(ds)}")
    print(f"# files {len(seen)}")
