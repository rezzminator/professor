#!/usr/bin/env python3
"""chatlog kit: the mechanics of reading one transcript, so a reader only reads.

  prep TARGET [--lines A-B] [--dir DIR] [--window BYTES] [--reader-max BYTES]
      digest, clean, label every mechanical line, plan the windows; prints the plan
  read DIR N|A-B          window N (or digest lines A-B), each line D{n} with its labels
  find DIR REGEX [-C N]   digest lines matching REGEX (case-insensitive), with context
  note DIR ROW...         add reader rows, validated; ROW = "D41 CLAIM" | "D37 BEARS D41"
                          | "D88 CORRECTION - exact words"; rows on stdin when none given
  report DIR [--out F]    the report: header, every row verbatim, counts
  verify PATH... [--print]  every row of each report (or dir of *.tsv) against its source
  prompts DIR             every user prompt, beside the assistant line before it
  writes DIR              every file the run wrote, and its state on disk now
  runs --type T [--since 7d] [--project SUBSTR] | --session S
                          runs of one agent type across projects, or a session's sub-agents

TARGET is anything `pfm transcript locate` resolves. Rows are tab-separated:
id (D{n} digest line, M{n} timeline line), text, label, ref.
"""
import json, os, re, subprocess, sys, tempfile, time

MECH = ("BRIEF PROMPT FINAL SAY-CLAIM NOTE NOTICE COMPACTION COMPACT-SUMMARY COMPACT-CALL HANDOFF "
        "SPAWN ERROR ERROR-OUTPUT DENIAL AFTER-ERROR NO-RESULT UNFINISHED WAIT WRITE REPEAT "
        "TOTALS MODEL-WAIT COSTLIEST").split()
READER = {"CORRECTION", "CLAIM", "BEARS", "WRITE"}
DENY = re.compile(r"denied|rejected|doesn't want to proceed|not allowed|blocked|refused|permission", re.I)
EVENT = re.compile(r"^L(\d+) (\S+) (\S+)")
WAIT = re.compile(r"→ [^‹]*?· (?:(\d+)m)?(\d+(?:\.\d+)?)s\b")
WRITE_TOOLS = {"Write", "Edit", "MultiEdit", "NotebookEdit", "apply_patch", "edit", "write", "patch"}
SPAWN_TOOLS = {"Agent", "Task", "spawn_agent", "task"}
SHELL_TOOLS = {"Bash", "exec", "shell", "bash", "exec_command"}
NOTIF = re.compile(r"<task-notification>.*?(?:<status>(.*?)</status>)?.*?(?:<summary>(.*?)</summary>)?", re.S)
REMINDER = re.compile(r"<system-reminder>.*?(</system-reminder>|$)", re.S)
KIT_DIR = "/tmp/chatlog-kit"


def die(msg, code=1):
    print(msg)
    sys.exit(code)


def run(cmd):
    p = subprocess.run(cmd, capture_output=True, text=True)
    return p.returncode, p.stdout, p.stderr


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


# ---------------------------------------------------------------- prep

def clean(line):
    """The LLM digest line: noise collapsed, the L number and kind kept."""
    m = EVENT.match(line)
    if not m:
        return line
    if "<task-notification>" in line:
        n = NOTIF.search(line)
        st = (n.group(1) or "?") if n else "?"
        sm = ((n.group(2) or "") if n else "").strip()
        return f"L{m.group(1)} {m.group(2)} NOTE task-notification · {st} · {sm[:200]}"
    line = REMINDER.sub("", line)
    if " COMPACT SUMMARY " in line:
        return line[:400] + ("…" if len(line) > 400 else "")
    return line


PATHLIKE = re.compile(r"^[\w./~@+$-][\w./~@+:=${}-]*$")


def shell_writes(cmd):
    """Files a shell command writes: redirections, tee, sed/perl -i, cp/mv/install targets.
    Heredoc bodies and quoted scripts are dropped first, so code text never reads as a path.
    A leading `cd DIR` in a chain prefixes the relative paths after it."""
    s = cmd
    while True:
        h = re.search(r"<<-?\s*['\"]?(\w+)['\"]?", s)
        if not h:
            break
        end = re.search(r"(?:^|\s)" + re.escape(h.group(1)) + r"(?=\s|$)", s[h.end():])
        s = s[:h.start()] + " " + (s[h.end() + end.end():] if end else "")
    s = re.sub(r"'[^']*'", " '' ", s)
    s = re.sub(r'"([^"]*)"', lambda m: m.group(1) if PATHLIKE.match(m.group(1)) else " '' ", s)
    out, base, env = [], "", {}

    def expand(p):
        p = re.sub(r"\$\{?(\w+)\}?", lambda m: env.get(m.group(1), m.group(0)), p)
        return p

    for seg in re.split(r"&&|\|\||;|\|", s):
        toks = seg.split()
        if not toks:
            continue
        while toks and re.fullmatch(r"\w+=\S*", toks[0]):
            k, v = toks[0].split("=", 1)
            env[k] = expand(v.strip("'\""))
            toks = toks[1:]
            seg = seg.split(None, 1)[1] if len(seg.split(None, 1)) > 1 else ""
        if not toks:
            continue
        if toks[0] == "cd" and len(toks) > 1:
            toks[1] = expand(toks[1])
            base = toks[1] if toks[1].startswith(("/", "~", "$")) else os.path.join(base, toks[1])
            continue
        got = []
        for m in re.finditer(r"(?<![0-9&<>=-])>>?\s*([^\s<>]+)", seg):
            got.append(m.group(1))
        cmd0 = os.path.basename(toks[0])
        rest = [t for t in toks[1:] if t != "''"]
        if cmd0 == "tee":
            got += [t for t in rest if not t.startswith("-")]
        elif cmd0 in ("sed", "gsed", "perl") and any(re.fullmatch(r"-[a-zA-Z]*i\S*", t) for t in toks[1:]):
            got += [t for t in rest if not t.startswith("-") and t not in ("-e",)]
        elif cmd0 in ("cp", "mv", "install", "ln") and len([t for t in rest if not t.startswith("-")]) >= 2:
            got.append([t for t in rest if not t.startswith("-")][-1])
        for p in got:
            if p.startswith("&") or p.startswith("/dev/") or not PATHLIKE.match(p):
                continue
            p = expand(p)
            if base and not p.startswith(("/", "~", "$")):
                p = os.path.join(base, p)
            out.append(p)
    return list(dict.fromkeys(out))


def prep(args):
    lines = opt(args, "--lines", "")
    dirp = opt(args, "--dir")
    window = int(opt(args, "--window", "40000"))
    rmax = int(opt(args, "--reader-max", "250000"))
    if len(args) != 1:
        die("usage: chatlog.py prep TARGET [--lines A-B] [--dir DIR] [--window BYTES] [--reader-max BYTES]", 2)
    target = args[0]
    rc, path, err = run(["pfm", "transcript", "locate", target])
    if rc != 0:
        die(f"FAILED {target} — {(path + err).strip()}")
    path = path.strip().split("\n")[0]
    if not dirp:
        os.makedirs(KIT_DIR, exist_ok=True)
        dirp = tempfile.mkdtemp(prefix="run.", dir=KIT_DIR)
    os.makedirs(dirp, exist_ok=True)
    # A --dir already holding another digest (two readers handed one Dir:) gets a fresh
    # subdirectory, so no reader overwrites another's rows; the same target and range reuse it.
    kj = os.path.join(dirp, "kit.json")
    if os.path.exists(kj) or os.path.exists(os.path.join(dirp, "digest.txt")):
        try:
            with open(kj) as f:
                prev = json.load(f)
            same = prev.get("path") == path and prev.get("lines") == (lines or "all")
        except (OSError, ValueError):
            same = False
        if not same:
            dirp = tempfile.mkdtemp(prefix="reader.", dir=dirp)
    sl = ["--lines", lines] if lines else []
    rc, narrow, err = run(["pfm", "transcript", "show", path, *sl, "--results", "brief"])
    if rc != 0:
        die(f"FAILED {target} — {(narrow + err).strip()[:600]}")
    rc, wide, err = run(["pfm", "transcript", "show", path, *sl, "--results", "brief", "--width", "0", "--text", "0"])
    if rc != 0:
        die(f"FAILED {target} — wide digest: {(wide + err).strip()[:600]}")
    full = {}  # L -> (kind, full text of the event line)
    for ln in wide.split("\n"):
        m = EVENT.match(ln)
        if m:
            full[int(m.group(1))] = (m.group(3), ln)

    dig = [clean(x) for x in narrow.rstrip("\n").split("\n")]
    with open(os.path.join(dirp, "digest.txt"), "w") as f:
        f.write("\n".join(dig) + "\n")

    tl, tl_state = [], "skipped (a slice past line 1)"
    if not lines or lines.startswith("1-"):
        # --root narrowed to the transcript's project: the default scans every project (~50 s)
        d = os.path.dirname(path)
        if os.path.basename(d) == "subagents":
            root = os.path.dirname(d)
        elif os.path.isdir(path[:-6]) and path.endswith(".jsonl"):
            root = path[:-6]
        else:
            root = d
        prices = os.path.join(KIT_DIR, "prices.json")
        try:
            fresh = time.time() - os.path.getmtime(prices) < 86400
        except OSError:
            fresh = False
        if not fresh:
            os.makedirs(KIT_DIR, exist_ok=True)
            prc, pout, _ = run(["pfm", "model-cost", "--json", "--all"])
            if prc == 0:
                with open(prices, "w") as f:
                    f.write(pout)
        extra = ["--prices", prices] if os.path.exists(prices) else []
        rc, out, err = run(["pfm", "tokens", "--timeline", path, "--root", root, *extra])
        tl = (out + err).rstrip("\n").split("\n")
        tl_state = "ok" if rc == 0 and tl and tl[0].startswith("TIMELINE") else (tl[0][:200] if tl else f"FAILED rc {rc}")
    with open(os.path.join(dirp, "timeline.txt"), "w") as f:
        f.write("\n".join(tl) + ("\n" if tl else ""))

    rows, seen_calls, tools, writes = [], {}, {}, []
    after_err, err_at, tail = None, None, []
    briefed = False

    def flush_tail():
        if err_at and tail:
            picks = [tail[0]] + tail[-2:] if len(tail) > 3 else tail
            for t in dict.fromkeys(picks):
                rows.append((f"D{t}", "ERROR-OUTPUT", f"D{err_at}"))
            if any(DENY.search(dig[t - 1]) for t in tail):
                rows.append((f"D{err_at}", "DENIAL", "-"))

    for i, ln in enumerate(dig, 1):
        if ln.startswith("    | "):
            if err_at:
                tail.append(i)
            continue
        flush_tail()
        err_at, tail = None, []
        if ln.startswith("UNFINISHED"):
            rows.append((f"D{i}", "UNFINISHED", "-"))
            continue
        m = EVENT.match(ln)
        if not m:
            continue
        L, kind = int(m.group(1)), m.group(3)
        fk, ftext = full.get(L, (kind, ln))
        if kind.startswith("PROMPT"):
            rows.append((f"D{i}", "PROMPT" if briefed else "BRIEF", "-"))
            briefed = True
            continue
        if kind == "FINAL":
            rows.append((f"D{i}", "FINAL", "-"))
            continue
        if kind == "NOTE":
            if "COMPACTED" in ln:
                rows.append((f"D{i}", "COMPACTION", "-"))
            elif "COMPACT SUMMARY" in ln:
                rows.append((f"D{i}", "COMPACT-SUMMARY", "-"))
            elif "task-notification" in ln:
                rows.append((f"D{i}", "NOTICE", "-"))
            else:
                rows.append((f"D{i}", "NOTE", "-"))
            continue
        if kind == "SAY":
            continue
        # a tool call
        tools[kind] = tools.get(kind, 0) + 1
        if after_err:
            rows.append((f"D{i}", "AFTER-ERROR", f"D{after_err}"))
            after_err = None
        if "→ NO RESULT" in ln:
            rows.append((f"D{i}", "NO-RESULT", "-"))
        if "→ ERR" in ln:
            rows.append((f"D{i}", "ERROR", "-"))
            err_at = after_err = i
        w = WAIT.search(ln)
        if w and (int(w.group(1) or 0) * 60 + float(w.group(2))) >= 60:
            rows.append((f"D{i}", "WAIT", "-"))
        low = kind.lower()
        if "compact" in low and "handoff" not in low:
            rows.append((f"D{i}", "COMPACT-CALL", "-"))
        if "handoff" in low:
            rows.append((f"D{i}", "HANDOFF", "-"))
        if kind in SPAWN_TOOLS:
            rows.append((f"D{i}", "SPAWN", "-"))
        body = re.sub(r"^L\d+ \S+ \S+ ", "", ftext)
        body = body.split(" → ")[0]
        wp = []
        if kind in WRITE_TOOLS:
            wp = [body.split(" ")[0]]
        elif kind in SHELL_TOOLS:
            wp = shell_writes(body)
        if wp:
            rows.append((f"D{i}", "WRITE", "-"))
            writes.append({"D": i, "L": L, "tool": kind, "paths": wp})
        key = kind + " " + body
        if key in seen_calls:
            rows.append((f"D{i}", "REPEAT", f"D{seen_calls[key]}"))
        else:
            seen_calls[key] = i
    flush_tail()

    usd = []
    for j, t in enumerate(tl, 1):
        if j == 1 and t.startswith("TIMELINE"):
            rows.append(("M1", "TOTALS", "-"))
        elif re.match(r"^(pricing|data gaps):", t):
            rows.append((f"M{j}", "TOTALS", "-"))
        elif re.match(r"^  #\d+ ", t):
            a = re.search(r"after (\d+(?:\.\d+)?)s", t)
            if a and float(a.group(1)) >= 60:
                rows.append((f"M{j}", "MODEL-WAIT", "-"))
            d = re.search(r"\$(\d+(?:\.\d+)?)", t)
            if d:
                usd.append((float(d.group(1)), j))
    for _, j in sorted(usd, reverse=True)[:10]:
        rows.append((f"M{j}", "COSTLIEST", "-"))

    with open(os.path.join(dirp, "mech.tsv"), "w") as f:
        for r in rows:
            f.write("\t".join(r) + "\t-\n")
    open(os.path.join(dirp, "notes.tsv"), "a").close()

    # windows over the digest, cut at event lines
    wins, start, size = [], 1, 0
    for i, ln in enumerate(dig, 1):
        if size >= window and EVENT.match(ln):
            wins.append((start, i - 1))
            start, size = i, 0
        size += len(ln.encode()) + 1
    if start <= len(dig):
        wins.append((start, len(dig)))
    split, s0, size = [], None, 0
    for ln in dig:
        m = EVENT.match(ln)
        if m:
            L = int(m.group(1))
            if s0 is None:
                s0 = L
            if size > rmax:
                split.append(f"{s0}-{L - 1}")
                s0, size = L, 0
        size += len(ln.encode()) + 1
    if split:
        split.append(f"{s0}-")

    head = dig[:4]
    cwd = ""
    for h in head:
        c = re.search(r"· cwd (\S+)", h)
        if c:
            cwd = c.group(1)
    kit = {"target": target, "path": path, "lines": lines or "all", "cwd": cwd, "windows": wins,
           "timeline": tl_state, "tools": tools, "writes": writes, "made": time.time()}
    with open(os.path.join(dirp, "kit.json"), "w") as f:
        json.dump(kit, f)

    counts = {}
    for r in rows:
        counts[r[1]] = counts.get(r[1], 0) + 1
    nbytes = sum(len(x.encode()) + 1 for x in dig)
    print(f"DIR {dirp}")
    print("\n".join(h[:240] for h in head))
    print(f"DIGEST {nbytes} B · {len(dig)} lines · windows {len(wins)}: " + " ".join(f"{a}-{b}" for a, b in wins))
    print("TIMELINE " + tl_state)
    print("TOOLS " + (" · ".join(f"{k} {v}" for k, v in sorted(tools.items(), key=lambda x: -x[1])) or "none"))
    print("MECH " + (" · ".join(f"{k} {counts[k]}" for k in MECH if k in counts) or "none"))
    if split and not lines:
        print("SPLIT " + target + f" · {nbytes} B · Lines: " + " ".join(split))


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
    if len(args) != 2:
        die("usage: chatlog.py read DIR N|A-B", 2)
    kit, dig = load(args[0])
    sel = args[1]
    if "-" in sel:
        a, b = sel.split("-", 1)
        a, b = int(a), int(b or len(dig))
    else:
        k = int(sel)
        if not 1 <= k <= len(kit["windows"]):
            die(f"KIT FAILED — window {k}: there are {len(kit['windows'])}")
        a, b = kit["windows"][k - 1]
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
        elif label not in READER and not re.fullmatch(r"O[1-9][0-9]?", label):
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
            tl = f.read().rstrip("\n").split("\n")
    except OSError:
        tl = []
    out = out or os.path.join(dirp, "report.tsv")
    rows, seen, counts = [], set(), {}
    for r in read_rows(os.path.join(dirp, "mech.tsv")) + read_rows(os.path.join(dirp, "notes.tsv")):
        idx, label, ref, anchor = r
        if (idx, label) in seen:
            continue
        seen.add((idx, label))
        src = dig if idx[0] == "D" else tl
        n = int(idx[1:])
        s = src[n - 1] if 1 <= n <= len(src) else ""
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
    cwd = kit.get("cwd") or ""
    seen = {}
    for w in kit["writes"]:
        for p in w["paths"]:
            ap = os.path.expanduser(p.replace("$HOME", "~", 1) if p.startswith("$HOME") else p)
            if "$" in ap:
                seen.setdefault(ap + "\tUNRESOLVED", []).append(f"D{w['D']}")
                continue
            if not os.path.isabs(ap) and cwd:
                ap = os.path.normpath(os.path.join(cwd, ap))
            seen.setdefault(ap, []).append(f"D{w['D']}")
    for ap, ds in seen.items():
        if ap.endswith("\tUNRESOLVED"):
            print(f"{ap}\t{' '.join(ds)}")
            continue
        if os.path.exists(ap):
            st = os.stat(ap)
            state = f"exists {st.st_size} B · mtime {time.strftime('%Y-%m-%d %H:%M', time.localtime(st.st_mtime))}"
            d = ap if os.path.isdir(ap) else os.path.dirname(ap)
            rc, out, _ = run(["git", "-C", d, "status", "--porcelain", "--", ap])
            if rc == 0:
                state += " · git " + (out.strip()[:2].strip() or "clean")
        else:
            state = "GONE"
        print(f"{ap}\t{state}\t{' '.join(ds)}")
    print(f"# files {len(seen)}")


def runs(args):
    sess = opt(args, "--session")
    if sess:
        rc, out, err = run(["pfm", "transcript", "agents", sess])
        print((out + err).rstrip())
        sys.exit(rc)
    typ = opt(args, "--type")
    since = opt(args, "--since", "30d")
    proj = opt(args, "--project", "")
    if not typ:
        die("usage: chatlog.py runs --type T [--since 7d] [--project SUBSTR] | --session S", 2)
    m = re.fullmatch(r"(\d+)([hd])", since)
    if not m:
        die(f"KIT FAILED — --since {since}: use Nh or Nd")
    cutoff = time.time() - int(m.group(1)) * (3600 if m.group(2) == "h" else 86400)
    root = os.path.realpath(os.path.join(os.environ.get("CLAUDE_CONFIG_DIR", os.path.expanduser("~/.claude")), "projects"))
    found = []
    try:
        projs = os.listdir(root)
    except OSError as e:
        die(f"KIT FAILED — {root}: {e}")
    for pd in projs:
        if proj and proj not in pd:
            continue
        for dp, _, fs in os.walk(os.path.join(root, pd)):
            if not dp.endswith("subagents"):
                continue
            for f in fs:
                if not f.endswith(".meta.json"):
                    continue
                mp = os.path.join(dp, f)
                try:
                    if os.path.getmtime(mp) < cutoff:
                        continue
                    with open(mp) as fh:
                        meta = json.load(fh)
                except (OSError, ValueError) as e:
                    print(f"UNREADABLE {mp} — {e}")
                    continue
                if meta.get("agentType") != typ:
                    continue
                jp = mp[: -len(".meta.json")] + ".jsonl"
                sz = os.path.getsize(jp) if os.path.exists(jp) else -1
                found.append((os.path.getmtime(mp), sz, meta.get("model") or "-", meta.get("description") or "", jp))
    for t, sz, mo, de, jp in sorted(found):
        print(f"{time.strftime('%m-%d %H:%M', time.localtime(t))}\t{sz}\t{mo}\t{de[:90]}\t{jp}")
    print(f"# runs {len(found)} of {typ} since {since}")


def main():
    verbs = {"prep": prep, "read": read, "find": find, "note": note, "report": report,
             "verify": verify, "prompts": prompts, "writes": writes, "runs": runs}
    if len(sys.argv) < 2 or sys.argv[1] not in verbs:
        die(__doc__, 2)
    verbs[sys.argv[1]](sys.argv[2:])


if __name__ == "__main__":
    main()
