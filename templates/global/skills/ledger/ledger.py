#!/usr/bin/env python3
"""ledger — the mechanical half of tracer and mapper.

A model reads code and writes fact rows; this script does everything that can
be computed instead of asked: the survey of where a target's spellings occur,
the check that every quoted line sits at its address, the widening grep over
outward names, the absence counts, and the lint of the final report.

Commands:
  survey  ROOT TARGET [SPELLING...]   build the inventory, buckets and ledger dir
  verify  DIR                         check every row's quote, rebuild ledger.txt
  widen   DIR                         grep outward names, cut next-round buckets
  absent  DIR NAME...                 count files naming NAME (word match)
  show    DIR [--by facet|file]       print the verified ledger
  lint    DIR REPORT                  flag unverified addresses and bare universals

Exit status is non-zero, with the reason on stderr, whenever a command could
not look; "0 hits" is only ever printed by a search that ran over a counted
file list.
"""
import json
import os
import re
import subprocess
import sys
import tempfile
import time

MAX_BYTES = 2_000_000
DATA_EXT = {".json", ".jsonl", ".ndjson", ".csv", ".tsv", ".dump", ".sql.gz", ".parquet"}
DATA_BYTES = 200_000
DATA_DIR_RE = re.compile(r"(^|/)(data|seeds?|seeding|snapshots?|dumps?|exports?|backups?)/")
DOC_EXT = {".md", ".mdx", ".rst", ".txt", ".adoc"}
TEST_RE = re.compile(r"(^|/)(tests?|__tests__|e2e|fixtures?|testdata)/|\.(test|spec|steps)\.|_test\.|(^|/)test_[^/]*$|\.feature$")
GEN_RE = re.compile(r"(^|/)(generated|__generated__|gen|artifacts)/|\.generated\.|\.fingerprint$|(^|/)[^/]*\.lock$|-lock\.")
FALLBACK_SKIP = {".git", "node_modules", "dist", "build", "vendor", ".next", "coverage", ".venv", "venv", "__pycache__", ".worktrees", "target", ".cache"}
BUCKET_FILES = 8
BUCKET_HITS = 45
CONTEXT = 28
FILE_LINES = 320
COMMON = 25

KINDS = {
    "DEFINES": "DEFINITION", "MIGRATES": "DEFINITION", "CITES-ABSENT": "DEFINITION",
    "WRITES": "WRITERS", "DELETES": "WRITERS", "TRIGGERS": "WRITERS",
    "READS": "READERS", "CALLS": "READERS", "SERVES": "READERS", "CONSUMES": "READERS",
    "RENDERS": "READERS", "PROMPTS": "READERS", "EGRESS": "READERS", "STORES": "READERS",
    "SCHEDULES": "TIMING", "EXPIRES": "TIMING",
    "GUARDS": "CONTROL", "VALIDATES": "CONTROL", "CONFIGURES": "CONTROL", "REGISTERS": "CONTROL", "AUDITS": "CONTROL",
    "RELATES": "NEIGHBOURS",
    "MENTIONS": "MENTIONS",
}
FACETS = ["DEFINITION", "WRITERS", "READERS", "TIMING", "CONTROL", "NEIGHBOURS", "MENTIONS"]
UNIVERSAL_RE = re.compile(r"\b(sole|solely|exclusively|never|none|nothing|no other|nowhere|(?<![-\w])only(?!-))\b", re.I)
ADDR_RE = re.compile(r"([\w./@\[\]()+-]+\.[A-Za-z0-9]+):(\d+)")


def die(msg):
    print(f"LEDGER FAILED — {msg}", file=sys.stderr)
    sys.exit(2)


def git_files(root):
    """Every file the repo authors: tracked plus untracked-not-ignored, nested repos included."""
    try:
        out = subprocess.run(["git", "-C", root, "ls-files", "-co", "--exclude-standard"],
                             capture_output=True, text=True, check=True).stdout.splitlines()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None
    files = []
    for rel in out:
        full = os.path.join(root, rel)
        if os.path.isdir(full):
            if os.path.exists(os.path.join(full, ".git")):
                nested = git_files(full)
            else:
                nested = walk_files(full)
            files += [os.path.join(rel.rstrip("/"), n) for n in nested or []]
            continue
        files.append(rel)
    return files


def walk_files(root):
    files = []
    for base, dirs, names in os.walk(root):
        dirs[:] = [d for d in dirs if d not in FALLBACK_SKIP]
        for n in names:
            files.append(os.path.relpath(os.path.join(base, n), root))
    return files


def list_files(root):
    files = git_files(root)
    source = "git ls-files (tracked + untracked, ignore file honoured)"
    if files is None:
        files = walk_files(root)
        source = "directory walk (no git), skipping " + ", ".join(sorted(FALLBACK_SKIP))
    return sorted(set(files)), source


def classify(rel, size):
    low = rel.lower()
    ext = os.path.splitext(low)[1]
    if ext in DATA_EXT and (size > DATA_BYTES or DATA_DIR_RE.search(low)):
        return "DATA"
    if TEST_RE.search(low):
        return "TEST"
    if GEN_RE.search(low):
        return "GENERATED"
    if ext in DOC_EXT:
        return "DOC"
    return "CODE"


def read_lines(path):
    try:
        with open(path, "rb") as fh:
            raw = fh.read(MAX_BYTES + 1)
    except OSError as err:
        return None, f"unreadable: {err}"
    if len(raw) > MAX_BYTES:
        return None, "larger than 2 MB"
    if b"\0" in raw[:4096]:
        return None, "binary"
    return raw.decode("utf-8", "replace").splitlines(), None


def variants(term):
    parts = [p for p in re.split(r"[_\-\s]+|(?<=[a-z0-9])(?=[A-Z])", term) if p]
    if len(parts) < 2:
        return [term]
    low = [p.lower() for p in parts]
    forms = [
        "_".join(low), "-".join(low), "_".join(low).upper(),
        low[0] + "".join(p.capitalize() for p in low[1:]),
        "".join(p.capitalize() for p in low),
    ]
    seen, out = set(), []
    for f in [term] + forms:
        if f not in seen:
            seen.add(f)
            out.append(f)
    return out


def scan(root, files, needles, word=False):
    """needle -> {rel: [line numbers]}; plus the list of files that could not be read."""
    pats = {n: re.compile((r"(?<![\w])" + re.escape(n) + r"(?![\w])") if word else re.escape(n)) for n in needles}
    hits = {n: {} for n in needles}
    skipped = []
    for rel in files:
        full = os.path.join(root, rel)
        try:
            size = os.path.getsize(full)
        except OSError:
            continue
        if classify(rel, size) == "DATA":
            with open(full, "rb") as fh:
                blob = fh.read(MAX_BYTES)
            for n in needles:
                if n.encode() in blob:
                    hits[n].setdefault(rel, [])
            continue
        lines, why = read_lines(full)
        if lines is None:
            if why != "binary":
                skipped.append(f"{rel} ({why})")
            continue
        text = "\n".join(lines)
        for n, pat in pats.items():
            if n not in text:
                continue
            nums = [i + 1 for i, line in enumerate(lines) if pat.search(line)]
            if nums:
                hits[n][rel] = nums
    return hits, skipped


def cut_buckets(entries, start):
    """entries: [(rel, [lines])] sorted; greedy buckets that never cross a top-level directory."""
    buckets, cur, cur_hits, cur_top = [], [], 0, None
    for rel, nums in entries:
        top = rel.split("/", 1)[0]
        if cur and (top != cur_top or len(cur) >= BUCKET_FILES or cur_hits + len(nums) > BUCKET_HITS):
            buckets.append(cur)
            cur, cur_hits = [], 0
        cur.append((rel, nums))
        cur_hits += len(nums)
        cur_top = top
    if cur:
        buckets.append(cur)
    merged = []
    for b in buckets:  # a bucket of one or two files rides with the previous small one
        if merged and len(b) <= 2 and len(merged[-1]) + len(b) <= BUCKET_FILES and len(merged[-1]) <= 4:
            merged[-1] = merged[-1] + b
        else:
            merged.append(b)
    return {start + i: b for i, b in enumerate(merged)}


def excerpt_ranges(nums, total):
    """Merged line ranges around the hit lines, capped per file."""
    ranges = []
    for n in nums:
        lo, hi = max(1, n - CONTEXT), min(total, n + CONTEXT)
        if ranges and lo <= ranges[-1][1] + 5:
            ranges[-1][1] = max(ranges[-1][1], hi)
        else:
            ranges.append([lo, hi])
    out, used = [], 0
    for lo, hi in ranges:
        if used + (hi - lo + 1) > FILE_LINES:
            hi = lo + max(0, FILE_LINES - used) - 1
        if hi >= lo:
            out.append((lo, hi))
            used += hi - lo + 1
    return out


def write_bucket(d, num, state, bucket, names):
    path = os.path.join(d, f"bucket-{num}.txt")
    with open(path, "w") as fh:
        fh.write(f"ROOT: {state['root']}\nTARGET: {state['target']}\nNAMES: {', '.join(names)}\n"
                 f"ROWS FILE: {os.path.join(d, f'rows-{num}.txt')}\n"
                 f"FILES: {len(bucket)} — each shown as numbered excerpts around the lines where a name occurs (marked >>)\n")
        for rel, nums in bucket:
            lines, why = read_lines(os.path.join(state["root"], rel))
            fh.write(f"\n===== {rel} (read it at {os.path.join(state['root'], rel)}) — hits at lines {','.join(map(str, nums[:60]))}\n")
            if lines is None:
                fh.write(f"(not shown: {why})\n")
                continue
            hit = set(nums)
            shown = excerpt_ranges(nums, len(lines))
            for lo, hi in shown:
                fh.write(f"--- lines {lo}-{hi} of {len(lines)}\n")
                for i in range(lo, hi + 1):
                    fh.write(f"{'>>' if i in hit else '  '}{i:>5}: {lines[i - 1][:400]}\n")
            left = [n for n in nums if not any(lo <= n <= hi for lo, hi in shown)]
            if left:
                fh.write(f"(hits not shown, read them yourself: {','.join(map(str, left[:40]))})\n")
    return path


def load(d):
    try:
        with open(os.path.join(d, "state.json")) as fh:
            return json.load(fh)
    except OSError as err:
        die(f"no ledger at {d}: {err}")


def save(d, state):
    with open(os.path.join(d, "state.json"), "w") as fh:
        json.dump(state, fh, indent=1)


def cmd_survey(args):
    if len(args) < 2:
        die("usage: survey ROOT TARGET [SPELLING...]")
    root = os.path.abspath(args[0])
    if not os.path.isdir(root):
        die(f"root is not a directory: {root}")
    target, extra = args[1], args[2:]
    names = []
    for term in [target] + extra:
        for v in variants(term):
            if v not in names:
                names.append(v)
    files, source = list_files(root)
    hits, skipped = scan(root, files, names)
    merged = {}
    for n in names:
        for rel, nums in hits[n].items():
            merged.setdefault(rel, set()).update(nums)
    classes = {}
    for rel in merged:
        classes[rel] = classify(rel, os.path.getsize(os.path.join(root, rel)))
    slug = re.sub(r"[^a-z0-9]+", "-", target.lower()).strip("-")[:40]
    d = os.path.join(tempfile.gettempdir(), "ledger", f"{slug}-{time.strftime('%H%M%S')}")
    os.makedirs(d, exist_ok=True)
    try:
        head = subprocess.run(["git", "-C", root, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip()
        dirty = len(subprocess.run(["git", "-C", root, "status", "--porcelain"], capture_output=True, text=True).stdout.splitlines())
        stamp = f"HEAD {head or 'none'}, {dirty} dirty"
    except FileNotFoundError:
        stamp = "no git"
    state = {"root": root, "target": target, "names": names, "stamp": stamp, "files_searched": len(files),
             "source": source, "inventory": {r: sorted(v) for r, v in merged.items()}, "classes": classes,
             "buckets": {}, "widened": [], "round": 1}
    code = sorted((r, sorted(v)) for r, v in merged.items() if classes[r] == "CODE")
    buckets = cut_buckets(code, 1)
    for num, b in buckets.items():
        write_bucket(d, num, state, b, names)
        state["buckets"][str(num)] = [r for r, _ in b]
    save(d, state)

    print(f"LEDGER DIR: {d}\nSTAMP: {stamp}\nSEARCHED: {len(files)} files via {source}")
    if skipped:
        print(f"NOT SEARCHED ({len(skipped)}): " + "; ".join(skipped[:8]))
    print("SPELLINGS (files naming each):")
    for n in names:
        print(f"  {n}  {len(hits[n])}")
    counts = {c: sum(1 for r in classes.values() if r == c) for c in ["CODE", "TEST", "DOC", "GENERATED", "DATA"]}
    print("INVENTORY: " + ", ".join(f"{c} {k}" for c, k in counts.items()))
    tops = sorted({r.split('/', 1)[0] for r in merged})
    alltops = sorted({f.split('/', 1)[0] for f in files if '/' in f})
    print("TOP-LEVEL DIRS WITH HITS: " + ", ".join(tops))
    print("TOP-LEVEL DIRS WITHOUT: " + ", ".join(t for t in alltops if t not in tops))
    print(f"BUCKETS (CODE only, {len(buckets)}):")
    for num, b in buckets.items():
        print(f"  bucket-{num}: " + ", ".join(f"{r}[{len(n)}]" for r, n in b))
    for cls in ["GENERATED", "TEST", "DOC", "DATA"]:
        rels = sorted(r for r, c in classes.items() if c == cls)
        if rels:
            note = " — never opened" if cls == "DATA" else ""
            print(f"{cls} ({len(rels)}){note}: " + ", ".join(rels))


KIND_ALIASES = {"UPDATES": "WRITES", "INSERTS": "WRITES", "CREATES": "WRITES", "REMOVES": "DELETES", "SELECTS": "READS",
                "TRUNCATES": "DELETES", "WIPES": "DELETES", "REGENERATES": "DELETES", "ANONYMISES": "DELETES", "ANONYMIZES": "DELETES", "EXPORTS": "EGRESS", "IMPORTS": "CONSUMES", "AUTHORIZES": "GUARDS", "LOGS": "AUDITS", "DECLARES": "DEFINES"}

DEF_RES = [
    re.compile(r"^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\*?\s+([A-Za-z_$][\w$]*)"),
    re.compile(r"^\s*(?:export\s+)?(?:abstract\s+)?(?:class|interface|enum|struct|trait|impl|module|object)\s+([A-Za-z_][\w]*)"),
    re.compile(r"^(?:export\s+(?:default\s+)?)?(?:const|let|var|val)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?="),
    re.compile(r"^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)"),
    re.compile(r"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)"),
    re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)"),
    re.compile(r"^\s*(?:(?:public|private|protected|internal|static|final|override|async|readonly|abstract|virtual|suspend|fun)\s+)*([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\([^;]*\)?\s*(?::\s*[^={]+)?\{?\s*$"),
    re.compile(r"^\s*([A-Za-z_$][\w$]*)\s*:\s*(?:async\s*)?(?:function\b|\([^)]*\)\s*(?::[^=]+)?=>|async\b)"),
    re.compile(r"^\s*([A-Za-z_$][\w$-]*)\s*\(\)\s*\{"),
]
NOT_NAMES = {"if", "for", "while", "switch", "catch", "return", "function", "else", "try", "do", "with", "await", "new", "throw",
             "constructor", "handler", "execute", "run", "main", "init", "setup", "get", "set", "save", "load", "index", "render",
             "describe", "it", "test", "expect", "then", "map", "filter", "forEach", "default", "async", "super", "values", "where", "select", "insert", "update", "delete", "from"}


def enclosing(lines, n):
    """Names of the definitions the line sits in: the nearest less-indented definition above, and the one above that."""
    def indent(s):
        return len(s) - len(s.lstrip())
    found, limit = [], None
    cur = lines[n - 1] if 0 < n <= len(lines) else ""
    limit = indent(cur) if cur.strip() else 10 ** 6
    for i in range(n - 1, -1, -1):
        line = lines[i]
        if not line.strip():
            continue
        ind = indent(line)
        if ind > limit or (ind == limit and i != n - 1):
            continue
        for rx in DEF_RES:
            m = rx.match(line)
            if m and m.group(1) not in NOT_NAMES and len(m.group(1)) >= 5:
                if m.group(1) not in found:
                    found.append(m.group(1))
                break
        if ind < limit:
            limit = ind
        if len(found) == 2 or limit == 0 and i != n - 1 and found:
            break
    return found


IDENT_RE = re.compile(r"[A-Za-z_$][\w$]*(?:[.-][A-Za-z_$][\w$]*)*")


def unbacked(note, backing):
    """Identifier-like words and numbers in a note that its row's quoted lines do not show."""
    names, nums = [], []
    low = backing.lower()
    for tok in IDENT_RE.findall(note):
        for part in re.split(r"[.-]", tok):
            codey = "_" in part or re.search(r"[a-z][A-Z]", part)
            if codey and part.lower() not in low and part not in names:
                names.append(part)
    for num in re.findall(r"(?<![\w.])\d{2,}(?![\w.])", note):
        if num not in backing and num not in nums:
            nums.append(num)
    return names, nums


def block_count(lines, n):
    """For a line that opens a literal it does not close: (closing line, entries), counted by bracket depth."""
    strip = lambda t: re.sub(r"//.*$", "", re.sub(r"\"(?:[^\"\\]|\\.)*\"|'(?:[^'\\]|\\.)*'", "s", t))
    base = sum(1 if ch in "[{(" else -1 for ch in strip(lines[n - 1]) if ch in "[{()}]")
    if base < 1 or not re.search(r"[\[{(]\s*$", strip(lines[n - 1]).rstrip()):
        return None
    depth, entries, pending, starts = base, 0, False, 0
    for i in range(n, min(len(lines), n + 1500)):
        code = strip(lines[i]).strip()
        if depth == base and code and code[0] not in "]})#" and not code.startswith(("/*", "*", '"""')):
            starts += 1
        for ch in strip(lines[i]):
            if ch in "[{(":
                depth += 1
                pending = pending or depth == base + 1
            elif ch in "]})":
                depth -= 1
                if depth < base:
                    seps = entries + (1 if pending else 0)
                    if i + 1 - n < 3:
                        return None
                    if seps >= starts - 1:
                        return (i + 1, seps)
                    return None  # items not separator-delimited: no count beats a wrong count
            elif depth == base and ch in ",;":
                entries += 1
                pending = False
            elif depth == base and not ch.isspace():
                pending = True
    return None


def parse_rows(path):
    rows, cur = [], None
    with open(path, encoding="utf-8", errors="replace") as fh:
        for raw in fh:
            line = raw.rstrip("\n")
            if line.startswith("@ "):
                cur = {"head": line[2:], "quote": None, "note": "", "src": os.path.basename(path)}
                rows.append(cur)
            elif cur is not None and line.startswith("> "):
                if cur["quote"] is None:
                    cur["quote"] = line[2:]
                else:
                    cur.setdefault("more", []).append(line[2:])
            elif cur is not None and line.startswith("# "):
                cur["note"] = (cur["note"] + " " + line[2:]).strip()
    return rows


def joined_lines(lines, quote, num):
    """A quote that glues several consecutive lines into one: ([start line], [the following line numbers]) when every character is in the file, in order."""
    q = re.sub(r"\s+", "", quote)
    best = None
    for i, line in enumerate(lines):
        first = re.sub(r"\s+", "", line)
        if len(first) < 8 or not q.startswith(first):
            continue
        acc, j = first, i + 1
        while j < len(lines) and j - i < 40 and len(acc) < len(q):
            nxt = re.sub(r"\s+", "", lines[j])
            if not q.startswith(acc + nxt):
                rest = q[len(acc):]
                if nxt.startswith(rest) and rest:
                    acc, j = q, j + 1
                break
            acc, j = acc + nxt, j + 1
        if len(acc) >= min(len(q), 60) and j - i >= 2 and (best is None or abs(i + 1 - num) < abs(best[0] - num)):
            best = (i + 1, list(range(i + 2, j + 1)))
    return ([best[0]], best[1]) if best else ([], [])

def norm(s):
    return re.sub(r"\s+", " ", s).strip()


def cmd_verify(args):
    d = args[0] if args else die("usage: verify DIR")
    state = load(d)
    root = state["root"]
    row_files = sorted((f for f in os.listdir(d) if re.fullmatch(r"rows-\d+(-\d+)?\.txt", f)),
                       key=lambda f: int(re.findall(r"\d+", f)[0]))
    good, bad, cache, stripped, fixed_syms = [], [], {}, 0, 0
    for rf in row_files:
        for row in parse_rows(os.path.join(d, rf)):
            parts = [p.strip() for p in row["head"].split("|")]
            m = re.fullmatch(r"(.+?):(\d+)(?:\s*[-,].*)?", parts[0]) if parts else None
            if not m or len(parts) < 3:
                bad.append((row, "malformed header — want `@ path:line | KIND | symbol | out: a, b`"))
                continue
            rel, num, kind = m.group(1), int(m.group(2)), parts[1].upper()
            kind = KIND_ALIASES.get(kind, kind)
            if kind not in KINDS:
                row["note"] = f"(the reader called this {kind}, which is no kind; filed as MENTIONS until you read it) " + row["note"]
                kind = "MENTIONS"
            if not row["quote"] or not norm(row["quote"]):
                bad.append((row, "no quoted line"))
                continue
            if rel not in cache:
                cache[rel] = read_lines(os.path.join(root, rel))[0]
            lines = cache[rel]
            if lines is None:
                bad.append((row, f"file not found or unreadable: {rel}"))
                continue
            want = norm(row["quote"])
            found = [i + 1 for i, line in enumerate(lines) if want and want in norm(line)]
            joined = []
            if not found:
                found, joined = joined_lines(lines, row["quote"], num)
            if not found:
                bad.append((row, "quote is not a line of that file"))
                continue
            at = min(found, key=lambda n: abs(n - num))
            more, lost, ptr = [f"{n2}: {lines[n2 - 1].strip()[:200]}" for n2 in joined], 0, at
            for extra in row.get("more", []):
                w = norm(extra)
                where = [i + 1 for i, line in enumerate(lines) if w and w in norm(line) and abs(i + 1 - at) <= 60]
                if where:
                    n2 = min(where, key=lambda n: (n < ptr, abs(n - ptr)))
                    ptr = n2
                    more.append(f"{n2}: {lines[n2 - 1].strip()[:200]}")
                else:
                    lost += 1
            if lost:
                bad.append((row, f"{lost} of its extra quoted lines are not within 60 lines of its address — row kept without them; one row, one block"))
            outs = []
            for p in parts[3:]:
                if p.lower().startswith("out:"):
                    outs = [o.strip() for o in p[4:].split(",") if o.strip() and o.strip() != "-" and o.strip().split(".")[-1] in "\n".join(lines)]
            encl = enclosing(lines, at)
            text_all = "\n".join(lines)
            sym_words = [w for w in re.findall(r"[A-Za-z_$][\w$]*", parts[2]) if len(w) >= 4]
            if sym_words and not any(w in text_all for w in sym_words):
                fixed_syms += 1
                parts[2] = encl[0] if encl else "(unnamed block)"
            if kind not in ("MENTIONS", "CITES-ABSENT"):
                for name in encl[:1]:
                    if name not in outs:
                        outs.append(name)
            near = lines[max(0, at - 41):at + 40]
            backing = " ".join(near + more + [parts[2], rel] + outs + state["names"])
            names, nums = unbacked(row["note"], backing)
            if names:
                stripped += 1
                row["note"] = f"(note withheld: it named {', '.join(names[:3])}, not shown near the quote)"
            elif nums:
                for bad_num in nums:
                    row["note"] = re.sub(rf"(?<![\w.]){bad_num}(?![\w.])", "(?)", row["note"])
                row["note"] += " [(?) = a number the quoted lines do not show, removed]"
            blk = block_count(lines, at) if kind in ("REGISTERS", "DEFINES", "CONFIGURES", "VALIDATES") else None
            if blk and blk[1] >= 2:
                row["computed"] = f"{blk[1]} items at the top level of the block this line opens (it closes at line {blk[0]})"
            good.append({"rel": rel, "line": at, "moved": at != num, "kind": kind, "symbol": parts[2],
                         "outs": outs, "quote": lines[at - 1].strip()[:240], "more": more, "note": row["note"][:300], "computed": row.get("computed", ""), "src": row["src"]})
    seen, rows = set(), []
    for g in good:
        key = (g["rel"], g["line"], KINDS[g["kind"]])
        if key not in seen:
            seen.add(key)
            rows.append(g)
    for i, g in enumerate(rows, 1):
        g["id"] = f"R{i}"
    state["rows"] = rows
    state["rejected"] = len(bad)
    save(d, state)
    write_ledger(d, state)
    covered = {g["rel"] for g in rows}
    missing_rows = [f"rows-{n}.txt" for n in state["buckets"] if not os.path.exists(os.path.join(d, f"rows-{n}.txt"))]
    uncovered = [r for n, rels in state["buckets"].items() for r in rels if r not in covered]
    print(f"VERIFIED: {len(rows)} rows ({sum(1 for g in rows if g['moved'])} re-addressed to the line the quote is really on)")
    print(f"SYMBOLS REPLACED: {fixed_syms} (the row named a symbol its file does not contain; the enclosing definition the script found stands in)")
    print(f"NOTES WITHHELD: {stripped} (a note naming something the code around its quote does not show is a guess; the row and its quotes stand)")
    print(f"REJECTED: {len(bad)}")
    for row, why in bad[:25]:
        print(f"  {row['src']}: {row['head'][:110]} — {why}")
    if missing_rows:
        print("BUCKETS WITH NO ROWS FILE (scribe failed or still running): " + ", ".join(missing_rows))
    print(f"BUCKET FILES WITH NO VERIFIED ROW ({len(uncovered)}): " + (", ".join(uncovered) or "none"))
    print("KINDS: " + ", ".join(f"{k} {sum(1 for g in rows if g['kind'] == k)}" for k in KINDS if any(g["kind"] == k for g in rows)))


def write_ledger(d, state):
    with open(os.path.join(d, "ledger.txt"), "w") as fh:
        for g in state.get("rows", []):
            fh.write(fmt_row(g) + "\n")
        for a in state.get("absences", []):
            fh.write(f"[{a['id']}] {a['text']}\n")


def fmt_row(g, brief=False):
    outs = f" | out: {', '.join(g['outs'])}" if g["outs"] else ""
    extra = g.get("more", [])
    if brief and len(extra) > 4:
        extra = [m[:140] for m in extra[:4]] + [f"(+{len(g['more']) - 4} more quoted lines in ledger.txt)"]
    more = "".join(f"\n     > {m}" for m in extra)
    comp = f"\n     = COUNTED BY THE SCRIPT: {g['computed']}" if g.get("computed") else ""
    return f"[{g['id']}] {g['rel']}:{g['line']} | {g['kind']} | {g['symbol']}{outs}\n     > {g['quote']}{more}{comp}\n     # {g['note']}"


def cmd_widen(args):
    d = args[0] if args else die("usage: widen DIR")
    state = load(d)
    rows = state.get("rows") or die("run verify first")
    root = state["root"]
    done = set(state["widened"]) | set(state["names"])
    names = [n for n in args[1:] if n not in done]
    for g in rows:
        for o in g["outs"]:
            if o not in done and o not in names and len(o) >= 4 and re.fullmatch(r"[\w./:\-@$]+", o):
                names.append(o)
    if not names:
        print("WIDEN: no new outward names — the frontier is closed.")
        return
    files, _ = list_files(root)
    hits, _ = scan(root, files, names, word=True)
    known = set(state["inventory"])
    own = {}
    for g in rows:
        for o in g["outs"]:
            own.setdefault(o, set()).add(g["rel"])
    fresh, absences = {}, state.get("absences", [])
    print(f"WIDEN round {state['round']}: {len(names)} outward names over {len(files)} files")
    for n in names:
        outside = {r: v for r, v in hits[n].items() if r not in own.get(n, set())}
        new = {r: v for r, v in outside.items() if r not in known}
        for r, v in hits[n].items():
            if r in own.get(n, set()) and state["classes"].get(r, "CODE") == "CODE":
                seen_lines = state["inventory"].get(r, [])
                unseen = [x for x in v if not any(abs(x - y) <= CONTEXT for y in seen_lines)]
                if unseen:
                    fresh.setdefault(r, set()).update(unseen)
        if not outside:
            aid = f"A{len(absences) + 1}"
            inside = {r: v for r, v in hits[n].items() if r in own.get(n, set())}
            uses = "; ".join(f"{r} lines {','.join(map(str, v[:12]))}" for r, v in sorted(inside.items()))
            absences.append({"id": aid, "name": n, "text": f"`{n}` — 0 files name it outside its own (word match over {len(files)} files); inside: {uses or 'no word match — check the spelling'}"})
            print(f"  {n}: 0 files outside its own → [{aid}]; used inside at {uses or '?'} — whoever uses it THERE may hand it onward under another name: that is a row to write, not a dead end")
        elif len(new) > COMMON:
            print(f"  {n}: {len(new)} new files — TOO COMMON to follow by name; qualify it (module path, receiver) and use `absent`/grep")
        else:
            code_new = {r: v for r, v in new.items() if classify(r, os.path.getsize(os.path.join(root, r))) == "CODE"}
            other = sorted(set(new) - set(code_new))
            print(f"  {n}: {len(outside)} files outside its own, {len(code_new)} new CODE" + (f"; non-code: {', '.join(other[:6])}" if other else ""))
            for r, v in code_new.items():
                fresh.setdefault(r, set()).update(v)
            for r in new:
                state["classes"][r] = classify(r, os.path.getsize(os.path.join(root, r)))
        state["widened"].append(n)
    state["absences"] = absences
    if fresh:
        start = max(map(int, state["buckets"])) + 1 if state["buckets"] else 1
        buckets = cut_buckets(sorted((r, sorted(v)) for r, v in fresh.items()), start)
        for num, b in buckets.items():
            write_bucket(d, num, state, b, names)
            state["buckets"][str(num)] = [r for r, _ in b]
            print(f"  NEW bucket-{num}: " + ", ".join(f"{r}[{len(v)}]" for r, v in b))
        for r, v in fresh.items():
            state["inventory"][r] = sorted(set(state["inventory"].get(r, [])) | set(v))
    else:
        print("  no new CODE files — the frontier is closed.")
    state["round"] += 1
    save(d, state)
    write_ledger(d, state)


def cmd_absent(args):
    if len(args) < 2:
        die("usage: absent DIR NAME...")
    d, names = args[0], args[1:]
    state = load(d)
    files, _ = list_files(state["root"])
    hits, _ = scan(state["root"], files, names, word=True)
    control = state["names"][0]
    chits, _ = scan(state["root"], files, [control], word=False)
    if not chits[control]:
        die(f"control spelling `{control}` found 0 files — the search cannot see; no absence may be claimed")
    absences = state.get("absences", [])
    print(f"SEARCHED {len(files)} files (control `{control}`: {len(chits[control])} files)")
    for n in names:
        found = hits[n]
        if found:
            by = {}
            for r in found:
                key = f"{r.split('/', 1)[0]} {classify(r, os.path.getsize(os.path.join(state['root'], r)))}"
                by[key] = by.get(key, 0) + 1
            print(f"  {n}: {len(found)} files [" + ", ".join(f"{k} {c}" for k, c in sorted(by.items())) + "] — "
                  + ", ".join(f"{r}:{v[0]}" if v else r for r, v in sorted(found.items())[:30]))
        else:
            aid = f"A{len(absences) + 1}"
            absences.append({"id": aid, "name": n, "text": f"`{n}` — 0 files (word match over {len(files)} files)"})
            print(f"  {n}: 0 files → [{aid}]")
    state["absences"] = absences
    save(d, state)
    write_ledger(d, state)


def served_unconsumed(rows):
    used = " ".join(g["quote"] + " " + " ".join(g.get("more", [])) for g in rows if g["kind"] in ("CONSUMES", "RENDERS", "PROMPTS", "EGRESS", "STORES"))
    return [g for g in rows if g["kind"] == "SERVES" and not any(o.split(".")[-1] in used for o in (g["outs"] or [g["symbol"]]))]


def cmd_show(args):
    d = args[0] if args else die("usage: show DIR [--by facet|file]")
    by = args[2] if len(args) > 2 and args[1] == "--by" else "facet"
    state = load(d)
    rows = state.get("rows") or die("run verify first")
    print(f"TARGET {state['target']} · {state['stamp']} · {len(rows)} verified rows · searched {state['files_searched']} files")
    if by == "file":
        for g in sorted(rows, key=lambda g: (g["rel"], g["line"])):
            print(fmt_row(g, True))
    else:
        for facet in FACETS:
            sel = [g for g in rows if KINDS[g["kind"]] == facet]
            print(f"\n## {facet} ({len(sel)})" + ("" if sel else " — no row; probe it or report it as not found, with the search"))
            for g in sel:
                print(fmt_row(g, True))
    orphans = served_unconsumed(rows)
    if orphans:
        print("\n## SERVED, NO CONSUMER ROW — a hop with no next reader yet, never a terminal: widen on the name, or report `served, no consumer row` with an `absent` count")
        for g in orphans:
            print(f"[{g['id']}] {g['rel']}:{g['line']} serves {', '.join(g['outs']) or g['symbol']}")
    print("\n## ABSENCES")
    for a in state.get("absences", []):
        print(f"[{a['id']}] {a['text']}")
    for cls in ["TEST", "DOC", "GENERATED", "DATA"]:
        rels = sorted(r for r, c in state["classes"].items() if c == cls)
        if rels:
            print(f"\n## {cls} files naming the target ({len(rels)}, not read): " + ", ".join(rels))
    covered = {g["rel"] for g in rows}
    un = [r for rels in state["buckets"].values() for r in rels if r not in covered]
    print(f"\n## UNREAD CODE FILES ({len(un)}): " + (", ".join(un) or "none"))


def cmd_lint(args):
    if len(args) < 2:
        die("usage: lint DIR REPORT")
    state = load(args[0])
    rows = state.get("rows") or []
    ids = {g["id"] for g in rows} | {a["id"] for a in state.get("absences", [])}
    addrs = {}
    for g in rows:
        addrs.setdefault(os.path.basename(g["rel"]), set()).add(g["line"])
        for m in g.get("more", []):
            addrs[os.path.basename(g["rel"])].add(int(m.split(":", 1)[0]))
    try:
        text = open(args[1], encoding="utf-8").read().splitlines()
    except OSError as err:
        die(f"cannot read report: {err}")
    flags, flagged = 0, []
    byid = {g["id"]: g for g in rows}
    orphan_ids = {g["id"] for g in served_unconsumed(rows)}
    for i, line in enumerate(text, 1):
        for cid in re.findall(r"\[([RA]\d+)\]", line):
            if cid not in ids:
                flagged.append(f"L{i}:"); print(f"L{i}: cites [{cid}] — no such row")
                flags += 1
        for path, num in ADDR_RE.findall(line):
            base = os.path.basename(path)
            if not any(abs(int(num) - n) <= 2 for n in addrs.get(base, ())):
                flagged.append(f"L{i}:"); print(f"L{i}: {base}:{num} — no verified row at that address; cite a row or drop the address")
                flags += 1
        for num in re.findall(r"(?<![\w.:/-])(\d{2,})\+?[- ](?:entr|operation|item|member|value|name|row|column|field|key|table|role|action|type)", line):
            sent = next((t for t in re.split(r"(?<=[.;])\s+(?=[A-Z`(])", line) if re.search(rf"(?<!\d){num}\+?[- ]", t)), line)
            cited_rows = [byid[c] for c in re.findall(r"\[(R\d+)\]", sent) if c in byid]
            shown = " ".join(g["quote"] + " " + " ".join(m.split(": ", 1)[-1] for m in g.get("more", [])) + " " + g.get("computed", "") for g in cited_rows)
            if not re.search(rf"(?<!\d){num}(?!\d)", shown):
                flagged.append(f"L{i}:"); print(f"L{i}: the count {num} is in no quoted line and no script count of the rows this line cites — use a COUNTED BY THE SCRIPT figure or drop the number")
                flags += 1
        if re.search(r"\b(?:terminal|RENDERS|rendered|displayed|shown to|to the client)\b", line):
            for cid in re.findall(r"\[(R\d+)\]", line):
                if cid in orphan_ids and not any(byid[c]["kind"] in ("CONSUMES", "RENDERS") for c in re.findall(r"\[(R\d+)\]", line) if c in byid):
                    flagged.append(f"L{i}:"); print(f"L{i}: [{cid}] serves a name no CONSUMES or RENDERS row picks up — a served field is a hop, not a terminal; say `served, no consumer row found`")
                    flags += 1
                    break
        if UNIVERSAL_RE.search(line) and not re.search(r"\[A\d+\]", line):
            word = UNIVERSAL_RE.search(line).group(0)
            flagged.append(f"L{i}:"); print(f"L{i}: “{word}” with no absence row — cite an [A#] from `absent`, or state the count you hold")
            flags += 1
    cited = set(re.findall(r"\[(R\d+)\]", "\n".join(text)))
    uncited = [g["id"] for g in rows if g["kind"] != "MENTIONS" and g["id"] not in cited]
    if uncited:
        print(f"UNCITED ({len(uncited)} verified rows the report never uses — each is a fact already paid for; place it or leave it): " + " ".join(uncited))
    if flags:
        shown = set()
        for i, line in enumerate(text, 1):
            if any(f"L{i}:" in m for m in flagged) and i not in shown:
                shown.add(i)
                print(f"   L{i} reads: {line[:300]}")
    print(f"LINT: {flags} flags over {len(text)} lines" + ("" if flags else " — clean"))
    covered = {g["rel"] for g in rows}
    unread = sum(1 for rels in state["buckets"].values() for r in rels if r not in covered)
    print(f"COUNTS (copy these): {len(rows)} verified rows, {state.get('rejected', 0)} rejected at the last verify, {unread} code files unread")


def cmd_wait(args):
    """Block until every bucket has a rows file (one call instead of a poll per reader)."""
    d = args[0] if args else die("usage: wait DIR BUCKET_NUMBER...")
    limit, want = 540, [x for x in args[1:] if x.isdigit()]
    import time
    state = load(d)
    def missing():
        have = {re.match(r"rows-(\d+)", f).group(1) for f in os.listdir(d) if re.match(r"rows-\d+(-\d+)?\.txt$", f)}
        return [n for n in (want or state["buckets"]) if str(n) not in have]
    start = time.time()
    while missing() and time.time() - start < limit:
        time.sleep(3)
    time.sleep(2)
    left = missing()
    print(f"WAITED {int(time.time() - start)}s — " + ("every bucket has a rows file" if not left else
          "STILL NO ROWS FILE for bucket " + ", ".join(map(str, left)) + " — its reader failed or is still running; wait once more, then respawn it"))


def main():
    cmds = {"survey": cmd_survey, "verify": cmd_verify, "widen": cmd_widen, "absent": cmd_absent, "show": cmd_show, "lint": cmd_lint, "wait": cmd_wait}
    if len(sys.argv) < 2 or sys.argv[1] not in cmds:
        die("usage: ledger.py {survey|wait|verify|widen|absent|show|lint} …")
    cmds[sys.argv[1]](sys.argv[2:])


if __name__ == "__main__":
    main()
