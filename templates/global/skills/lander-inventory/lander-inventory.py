#!/usr/bin/env python3
"""lander-inventory: threads a lander hands to sub-landers before reading the diff.

usage: lander-inventory.py WORKTREE BASE [HEAD] [--spec FILE ...]

LEFT  a name the diff removed everywhere it touched, still present in the tree at HEAD
LITERAL  a gone name that sat inside a string or key on a replaced code/data line
         (a matcher or stored format that may still meet the old spelling)
GONE  a file the diff deleted or renamed away, still named in the tree
PLAN  the spawn: hunters over production-diff clusters, spec walkers over numbered
      spec rows, sub-landers over the rows above by line number; the lander spawns
      exactly these rows
Exit 0 with the lists; exit 2 with FAILED and the cause when git or the input breaks.
"""
import re, subprocess, sys
from collections import defaultdict

TOKEN = re.compile(r"[A-Za-z_][A-Za-z0-9_\-]*[A-Za-z0-9_]")
HISTORY = re.compile(r"(^|/)(CHANGELOG[^/]*|releases/|docs/retro/|testdata/|\.professor/RR/)|retired", re.I)
HEX = re.compile(r"^[0-9a-f]{6,}$")
PROSE = re.compile(r"\.(md|txt|rst)$")
MAX_LEFT, MAX_LOC, MAX_SWAP = 60, 6, 120
TEST = re.compile(r"(_test\.go|\.test\.[mc]?[jt]s|_test\.py|test_[^/]*\.py|/tests?/)")
NOTCODE = re.compile(r"(\.(md|txt|rst|sum|lock)$|(^|/)(testdata|generated)/|package-lock\.json$)")
SPECROW = re.compile(r"^(\| *|[-*] +|#+ +)\**([A-Z]{1,3}[0-9]{1,3})\b")
HUNT_MIN, HUNT_SIZE, MAX_HUNT = 400, 600, 4
WALK_MIN, MAX_WALK, MAX_SUB = 10, 4, 6


OUT = []


def w(text):
    OUT.append(text)


def fail(text):
    sys.stdout.write("".join(OUT) + text)
    sys.exit(2)


def git(wt, *args):
    p = subprocess.run(["git", "-C", wt, *args], capture_output=True, text=True)
    if p.returncode not in (0, 1):
        fail(f"FAILED git {' '.join(args)[:120]}: {p.stderr.strip()[:300]}\n")
    return p.stdout


def namelike(t):
    # a name, not a word: _ , an inner capital, a digit, or 2+ hyphens (a key like a-b-c; "re-run" is prose)
    return (not HEX.match(t) and len(t) >= 6 and not t.startswith("--")
            and (("_" in t) or t.count("-") >= 2 or re.search(r"[a-z][A-Z]", t) or re.search(r"\d", t)))


def segments(t):
    out = {t}
    for part in t.split("__"):
        if part != t and namelike(part):
            out.add(part)
    return out


def main():
    argv, specs = sys.argv[1:], []
    if "--spec" in argv:
        i = argv.index("--spec")
        argv, specs = argv[:i], argv[i + 1:]
    if len(argv) < 2:
        sys.stdout.write(__doc__); sys.exit(2)
    wt, base = argv[0], argv[1]
    head = argv[2] if len(argv) > 2 else "HEAD"
    diff = git(wt, "diff", "--no-color", "-U0", "--no-ext-diff", f"{base}", f"{head}")
    if not diff.strip():
        fail(f"FAILED empty diff {base}..{head}\n")
    removed, added, added_in = defaultdict(set), set(), defaultdict(set)
    swaps = []
    path, newln, minus = None, 0, []
    for line in diff.splitlines():
        if line.startswith("+++ "):
            path = line[6:] if line.startswith("+++ b/") else None; minus = []; continue
        if line.startswith("--- ") or line.startswith("diff --git") or line.startswith("index "):
            continue
        if line.startswith("@@"):
            m = re.search(r"\+(\d+)", line); newln = int(m.group(1)) if m else 0; minus = []; continue
        if path is None:
            continue
        if line.startswith("-"):
            toks = {t for t in TOKEN.findall(line[1:]) if namelike(t)}
            for t in toks:
                removed[t].add(path)
            minus.append((toks, line[1:]))
        elif line.startswith("+"):
            toks = {t for t in TOKEN.findall(line[1:]) if namelike(t)}
            added_in[path].update(toks)
            if not HISTORY.search(path):
                added.update(toks)  # a name re-added only as history (a retired list) is still gone
            if minus and not PROSE.search(path):
                old, oldline = minus.pop(0)
                for t in old - toks:
                    if re.search(r"[\"'`][^\"'`]*\b" + re.escape(t) + r"\b[^\"'`]*[\"'`]", oldline):
                        swaps.append((path, newln, t))
            newln += 1
    gone = {t for t in removed if t not in added}
    pats = set()
    for t in gone:
        pats |= {s for s in segments(t) if s not in added}
    w(f"INVENTORY {base}..{head} · {len(removed)} names on removed lines · {len(gone)} gone from every added line\n")
    left = []
    if pats:
        p = subprocess.run(["git", "-C", wt, "grep", "-n", "-o", "-w", "-F", "-I", "-f", "/dev/stdin", head, "--"],
                           input="\n".join(sorted(pats)) + "\n", capture_output=True, text=True)
        if p.returncode not in (0, 1):
            fail(f"FAILED git grep: {p.stderr.strip()[:300]}\n")
        hits = defaultdict(list)
        for row in p.stdout.splitlines():
            parts = row.split(":", 3)
            if len(parts) < 4:
                continue
            _, f, ln, tok = parts
            hits[tok].append((f, ln))
        for tok, locs in hits.items():
            live = [(f, ln) for f, ln in locs if not HISTORY.search(f)]
            hist = len(locs) - len(live)
            left.append((len(live), tok, live, hist))
    left.sort(key=lambda x: (-(x[0] > 0), x[0]))
    shown = [l for l in left if l[0] > 0][:MAX_LEFT]
    w(f"LEFT {len([l for l in left if l[0] > 0])} names still in the tree outside history files (showing {len(shown)})\n")
    for n, tok, live, hist in shown:
        files = defaultdict(list)
        for f, ln in live:
            files[f].append(ln)
        loc = " ".join(f"{f}:{','.join(v[:3])}" for f, v in list(files.items())[:MAX_LOC])
        more = f" +{len(files) - MAX_LOC} files" if len(files) > MAX_LOC else ""
        w(f"LEFT {tok} · {n} hits in {len(files)} files · {hist} in history files · {loc}{more}\n")
    hist_only = [l for l in left if l[0] == 0]
    if hist_only:
        w(f"HISTORY-ONLY {len(hist_only)} names: {' '.join(sorted(l[1] for l in hist_only)[:30])}\n")
    # GONE: a file the diff deleted or renamed away, still named in the tree
    st = git(wt, "diff", "--name-status", "-M", "--diff-filter=DR", base, head)
    gone_files = []
    for row in st.splitlines():
        parts = row.split("\t")
        if parts and parts[0].startswith("D") and len(parts) >= 2:
            gone_files.append((parts[1], "deleted"))
        elif parts and parts[0].startswith("R") and len(parts) >= 3:
            gone_files.append((parts[1], "renamed to " + parts[2]))
    w(f"GONE {len(gone_files)} files the diff deleted or renamed away; rows below are those still named outside history files\n")
    for path, how in gone_files[:MAX_LEFT]:
        name = path.rsplit("/", 1)[-1]
        stem = name.rsplit(".", 1)[0] if "." in name[1:] else name
        if not re.search(r"[-_\d]|[a-z][A-Z]", stem):
            stem = name  # a plain word (palette) would match prose; keep the full file name
        if how.startswith("renamed to ") and how.rsplit("/", 1)[-1] == name:
            stem = "/".join(path.split("/")[-2:])  # moved, same name: only its old dir/name is stale
        if len(stem) < 5:
            continue
        # the stem catches prose and comments that name the thing without its extension
        p2 = subprocess.run(["git", "-C", wt, "grep", "-n", "-I", "-F", *([] if "/" in stem else ["-w"]), stem, head, "--"], capture_output=True, text=True)
        if p2.returncode not in (0, 1):
            fail(f"FAILED git grep {name}: {p2.stderr.strip()[:200]}\n")
        live = []
        for r in p2.stdout.splitlines():
            q = r.split(":", 3)
            if len(q) >= 3 and not HISTORY.search(q[1]):
                live.append(f"{q[1]}:{q[2]}")
        if live:
            more = f" +{len(live) - 2 * MAX_LOC}" if len(live) > 2 * MAX_LOC else ""
            w(f"GONE {stem} ({name}) · {how} · {len(live)} lines · {' '.join(live[:2 * MAX_LOC])}{more}\n")
    lit = defaultdict(list)
    for f, ln, t in swaps:
        if t not in added_in[f]:  # the file dropped that spelling; elsewhere it may live on
            lit[t].append(f"{f}:{ln}")
    for t in lit:  # the code that matches the format first; tests only pin it
        lit[t].sort(key=lambda loc: bool(TEST.search(loc)))
    testonly = sorted(t for t, locs in lit.items() if all(TEST.search(loc) for loc in locs))
    rows = sorted(((t, locs) for t, locs in lit.items() if t not in testonly),
                  key=lambda kv: sum(1 for loc in kv[1] if not TEST.search(loc)))
    w(f"LITERAL {len(rows)} names ({sum(len({l.rsplit(':', 1)[0] for l in v if not TEST.search(l)}) for _, v in rows)} name·file rows) a code/data file dropped from inside a string or key on a replaced line: a matcher or stored format there now knows only the new spelling (showing {min(len(rows), MAX_SWAP)})\n")
    if testonly:
        w(f"LITERAL-TESTS-ONLY {len(testonly)} names dropped only from tests: {' '.join(testonly[:30])}\n")
    # one row per (name, non-test file): a big row hides the one matcher that reads old text
    split = []
    for t, locs in rows:
        byfile = defaultdict(list)
        for loc in locs:
            if not TEST.search(loc):
                byfile[loc.rsplit(":", 1)[0]].append(loc)
        ntest = sum(1 for loc in locs if TEST.search(loc))
        for f, fl in byfile.items():
            split.append((t, fl, ntest))
    split.sort(key=lambda r: (len(r[1]), r[0]))
    for t, locs, ntest in split[:MAX_SWAP]:
        w(f"LITERAL {t} · {len(locs)} lines here, {ntest} in tests · {' '.join(locs[:MAX_LOC])}{' +' + str(len(locs) - MAX_LOC) if len(locs) > MAX_LOC else ''}\n")

    plan(wt, base, head, specs)
    sys.stdout.write("".join(OUT))


def plan(wt, base, head, specs):
    """The spawn, computed, so two runs on one input spawn alike."""
    rows = []  # PLAN lines, appended after the inventory so its line numbers stay put
    # hunters: production diff in path order, cut into equal clusters
    prod, tests = [], defaultdict(list)
    names = {f.rsplit("/", 1)[-1] for f in git(wt, "diff", "--name-only", "--no-renames", base, head).splitlines()}
    # deleted files have no code left to hunt (GONE rows cover their names)
    for row in git(wt, "diff", "--numstat", "--no-renames", "--diff-filter=d", base, head).splitlines():
        parts = row.split("\t")
        if len(parts) != 3 or parts[0] == "-":
            continue
        n, f = int(parts[0]) + int(parts[1]), parts[2]
        m = re.match(r"test-(.+)$", f.rsplit("/", 1)[-1])
        if TEST.search(f) or (m and m.group(1) in names):  # test-X.sh beside a changed X.sh tests it
            tests[f.rsplit("/", 1)[0] if "/" in f else "."].append(f)
        elif not NOTCODE.search(f) and not HISTORY.search(f):
            prod.append((f, n))
    prod.sort()
    total = sum(n for _, n in prod)
    if total <= HUNT_MIN:
        rows.append(f"PLAN HUNTERS none · production diff {total} lines <= {HUNT_MIN}: the whole hunt is yours\n")
    else:
        k = min(MAX_HUNT, -(-total // HUNT_SIZE))
        clusters, cur, acc = [], [], 0
        for f, n in prod:
            cur.append((f, n)); acc += n
            if len(clusters) < k - 1 and acc >= total * (len(clusters) + 1) / k:
                clusters.append(cur); cur = []
        if cur:
            clusters.append(cur)
        for i, c in enumerate(clusters, 1):
            dirs = {f.rsplit("/", 1)[0] if "/" in f else "." for f, _ in c}
            base_names = {"test-" + f.rsplit("/", 1)[-1] for f, _ in c}
            t = sorted({x for d in dirs for x in tests.get(d, [])}
                       | {x for xs in tests.values() for x in xs if x.rsplit("/", 1)[-1] in base_names})
            rows.append(f"PLAN HUNTER {i} · {sum(n for _, n in c)} lines · {' '.join(f for f, _ in c)}"
                        f" · tests {' '.join(t) if t else 'none'}\n")
    # spec walkers: numbered rows, the largest spec first, at most MAX_WALK ranges
    found = []
    for sp in specs:
        try:
            lines = open(sp, encoding="utf-8", errors="replace").read().splitlines()
        except OSError as e:
            rows.append(f"PLAN SPEC-UNREADABLE {sp} · {e.strerror}\n"); continue
        r = [(i, m.group(2)) for i, l in enumerate(lines, 1) if (m := SPECROW.match(l))]
        rows.append(f"PLAN SPEC {sp} · {len(r)} numbered rows{'' if len(r) >= WALK_MIN else f' < {WALK_MIN}: no walker'}\n")
        if len(r) >= WALK_MIN:
            found.append((sp, r))
    found.sort(key=lambda x: -len(x[1]))
    left = MAX_WALK
    for sp, r in found:
        want = min(left, 3 if len(r) > 60 else 2)
        if want <= 0:
            rows.append(f"PLAN WALKER-SKIPPED {sp} · walker cap {MAX_WALK} reached: its rows are yours\n"); continue
        left -= want
        size = -(-len(r) // want)
        for j in range(0, len(r), size):
            part = r[j:j + size]
            rows.append(f"PLAN WALKER {sp} rows {part[0][0]}-{part[-1][0]} · {len(part)} rows {part[0][1]}..{part[-1][1]}\n")
    # sub-landers: inventory rows by line number, LEFT+GONE apart from LITERAL
    near, lit = [], []
    for i, l in enumerate(OUT, 1):
        if re.match(r"^(LEFT|GONE) [^0-9]", l):
            near.append(i)
        elif re.match(r"^LITERAL [^0-9]", l):
            lit.append(i)
    nb = [-(-len(near) // 12), -(-len(lit) // 25)]
    while sum(nb) > MAX_SUB:
        nb[0 if nb[0] >= nb[1] else 1] -= 1
    for kind, ids, b in (("LEFT+GONE", near, nb[0]), ("LITERAL", lit, nb[1])):
        if not ids:
            continue
        size = -(-len(ids) // b)
        for j in range(0, len(ids), size):
            part = ids[j:j + size]
            rows.append(f"PLAN SUB-LANDER {kind} rows {part[0]}-{part[-1]} · {len(part)} rows\n")
    if not near and not lit:
        rows.append("PLAN SUB-LANDERS none · the inventory holds no rows\n")
    h = sum(1 for x in rows if x.startswith("PLAN HUNTER "))
    wk = sum(1 for x in rows if x.startswith("PLAN WALKER "))
    sb = sum(1 for x in rows if x.startswith("PLAN SUB-LANDER "))
    w(f"PLAN {h} hunters · {wk} spec walkers · {sb} sub-landers · spawn exactly these rows in one message\n")
    OUT.extend(rows)


if __name__ == "__main__":
    main()
