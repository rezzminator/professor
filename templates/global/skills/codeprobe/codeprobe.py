#!/usr/bin/env python3
"""codeprobe — the computed half of `collector`.

Every line it prints from a file is that file's text, numbered by the script. Every
list it computes is complete or says what it left out. Every check names its own
failure. Python 3 standard library only; it reads the repo and writes only under
/tmp/{project}/codeprobe/.

  verbs                             the extraction verbs and the collect syntax, from SKILL.md
  collect ROOT [DIR] --expect IDS   orders on stdin -> verbatim text in DIR/return.md, a manifest printed
"""
import difflib
import json
import os
import re
import shlex
import subprocess
import sys
import time


def probe_base(root):
    project = re.sub(r"^\.+", "", os.path.basename(os.path.realpath(root))) or "probe"
    return os.path.join("/tmp", project, "codeprobe")
MAX_BYTES = 2_000_000
BLOCK_LINES = 3000
PART_CHARS = 50_000  # the Read tool refuses over 25,000 tokens; code runs ~2.8 chars/token
DATA_EXT = {".json", ".jsonl", ".ndjson", ".csv", ".tsv", ".dump", ".gz", ".parquet", ".sqlite", ".db", ".xlsx", ".pkl"}
RECORD_EXT = DATA_EXT | {".html", ".htm", ".xml", ".txt", ".eml", ".pdf"}
DATA_BYTES = 200_000
BINARY_EXT = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp", ".tiff", ".svgz", ".pdf", ".woff", ".woff2", ".ttf", ".otf",
              ".eot", ".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".jar", ".class", ".so", ".dylib", ".dll", ".exe", ".o", ".a",
              ".mp3", ".mp4", ".mov", ".avi", ".webm", ".wav", ".ogg", ".sqlite", ".db", ".parquet", ".pkl", ".bin", ".wasm", ".pyc"}
DATA_DIR_RE = re.compile(r"(^|/)(data|seeds?|seeding|snapshots?|dumps?|exports?|backups?)/")
FIXTURE_RE = re.compile(r"(^|/)(fixtures?|testdata|__fixtures__|cassettes?)/")
DOC_EXT = {".md", ".mdx", ".rst", ".txt", ".adoc"}
TEST_RE = re.compile(r"(^|/)(tests?|__tests__|e2e|spec|fixtures?|testdata)/|\.(test|spec|steps)\.|_test\.|(^|/)test_[^/]*$|(^|/)conftest\.py$|\.feature$")
GEN_RE = re.compile(r"(^|/)(generated|__generated__|gen|artifacts)/|\.generated\.|\.pb\.go$|_pb2\.py$|\.min\.js$|(^|/)[^/]*\.lock$|-lock\.|(^|/)go\.sum$")
SKIP_DIRS = {".git", "node_modules", "dist", "build", "vendor", ".next", "coverage", ".venv", "venv", "__pycache__",
             ".worktrees", "target", ".cache", ".mypy_cache", ".pytest_cache", ".ruff_cache"}
VERBS = ("lines", "file", "def", "sig", "defs", "grep", "block", "consts")


def die(msg):
    print(f"CODEPROBE FAILED — {msg}", file=sys.stderr)
    sys.exit(2)


# ---------------------------------------------------------------- files


def git_files(root):
    try:
        out = subprocess.run(["git", "-C", root, "ls-files", "-co", "--exclude-standard"],
                             capture_output=True, text=True, check=True).stdout.splitlines()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None
    files = []
    for rel in out:
        full = os.path.join(root, rel)
        if os.path.isdir(full):
            nested = git_files(full) if os.path.exists(os.path.join(full, ".git")) else walk_files(full)
            files += [os.path.join(rel.rstrip("/"), n) for n in nested or []]
        else:
            files.append(rel)
    return files


def walk_files(root):
    files = []
    for base, dirs, names in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        files += [os.path.relpath(os.path.join(base, n), root) for n in names]
    return files


def list_files(root):
    files = git_files(root)
    source = "git ls-files (tracked + untracked, ignore file honoured)"
    if files is None:
        files, source = walk_files(root), "directory walk (no git), skipping " + ", ".join(sorted(SKIP_DIRS))
    out = []
    for rel in sorted(set(files)):
        try:
            size = os.path.getsize(os.path.join(root, rel))
        except OSError:
            continue
        out.append([rel, classify(rel, size)])
    return out, source


def classify(rel, size):
    low = rel.lower()
    ext = os.path.splitext(low)[1]
    if ext in DATA_EXT and (size > DATA_BYTES or DATA_DIR_RE.search(low)):
        return "DATA"
    if ext in RECORD_EXT and FIXTURE_RE.search(low):
        return "DATA"
    if ext in DOC_EXT and ext != ".txt":
        return "DOC"
    if TEST_RE.search(low):
        return "TEST"
    if GEN_RE.search(low):
        return "GENERATED"
    if ext in DOC_EXT:
        return "DOC"
    return "CODE"


def read_lines(path):
    if os.path.splitext(path.lower())[1] in BINARY_EXT:
        return None, "binary"
    try:
        with open(path, "rb") as fh:
            raw = fh.read(MAX_BYTES + 1)
    except OSError as err:
        return None, f"unreadable: {err}"
    if b"\0" in raw[:4096]:
        return None, "binary"
    if len(raw) > MAX_BYTES:
        return None, "larger than 2 MB"
    return raw.decode("utf-8", "replace").splitlines(), None


def resolve_path(root, path):
    """A path relative to ROOT, or absolute under it -> (rel, full)."""
    full = path if os.path.isabs(path) else os.path.join(root, path)
    full = os.path.normpath(full)
    return os.path.relpath(full, root), full


def scan(root, files, rx, classes=None):
    """-> ({rel: [line numbers]}, {rel: count} for DATA files, [unreadable]) over files of the given classes."""
    hits, data, bad = {}, {}, []
    for rel, cls in files:
        if classes and cls not in classes and not (cls == "DATA" and "DATA" in classes):
            continue
        full = os.path.join(root, rel)
        if cls == "DATA":
            try:
                with open(full, "rb") as fh:
                    blob = fh.read(MAX_BYTES).decode("utf-8", "replace")
            except OSError:
                continue
            n = len(rx.findall(blob))
            if n:
                data[rel] = n
            continue
        lines, why = read_lines(full)
        if lines is None:
            if why != "binary":
                bad.append(f"{rel} ({why})")
            continue
        nums = [i + 1 for i, line in enumerate(lines) if rx.search(line)]
        if nums:
            hits[rel] = nums
    return hits, data, bad


# ---------------------------------------------------------------- code structure

HASH_EXT = {".py", ".pyi", ".yaml", ".yml", ".sh", ".bash", ".zsh", ".toml", ".rb", ".pl", ".r", ".mk", ".ini", ".cfg",
            ".tf", ".dockerfile", ""}
NO_SLASH_EXT = {".py", ".pyi", ".yaml", ".yml", ".toml", ".sh", ".bash", ".zsh", ".sql", ".ini", ".cfg", ".rb", ".r", ""}
CHAR_QUOTE_EXT = {".go", ".rs", ".c", ".h", ".cc", ".cpp", ".hpp", ".java", ".kt", ".scala", ".cs", ".swift"}
BACKTICK_EXT = {".go", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}
CONTINUES = (",", "=", "(", "[", "{", "=>", "+", "-", "*", "/", "&&", "||", ".", "?", "|", "&", "\\", "->", ":")
NEXT_CONTINUES = (".", "?.", "&&", "||", "|", "?", ":", "+", "->", "=>")


def lang(rel):
    base = os.path.basename(rel).lower()
    ext = os.path.splitext(base)[1]
    if base in ("makefile", "gnumakefile") or ext == ".mk":
        return "make"
    if ext in (".py", ".pyi", ".yaml", ".yml"):
        return "indent"
    if ext == ".sql":
        return "sql"
    if ext in (".md", ".mdx", ".rst"):
        return "md"
    if ext in (".toml", ".ini", ".cfg"):
        return "ini"
    return "brace"


class Scanner:
    """Feeds lines, returns each line's code with strings and comments blanked; keeps multi-line state."""

    def __init__(self, rel):
        base = os.path.basename(rel).lower()
        ext = os.path.splitext(base)[1] if base not in ("makefile", "dockerfile") else ""
        self.hash = ext in HASH_EXT
        self.slash = ext not in NO_SLASH_EXT
        self.dash = ext == ".sql"
        self.triple = ext in (".py", ".pyi")
        self.backtick = ext in BACKTICK_EXT
        self.escapes_in_backtick = ext != ".go"
        self.charq = ext in CHAR_QUOTE_EXT
        self.dollar = ext == ".sql"
        self.string = None
        self.comment = False

    def feed(self, line):
        out, i, n = [], 0, len(line)
        while i < n:
            if self.comment:
                j = line.find("*/", i)
                if j < 0:
                    return "".join(out)
                self.comment, i = False, j + 2
                continue
            if self.string:
                d = self.string
                if d in ('"""', "'''", "$$"):
                    j = line.find(d, i)
                    if j < 0:
                        return "".join(out)
                    self.string, i = None, j + len(d)
                    out.append("s")
                    continue
                j = i
                while j < n and line[j] != d:
                    j += 2 if line[j] == "\\" and (d != "`" or self.escapes_in_backtick) else 1
                if j >= n:
                    if d != "`":
                        self.string = None
                    return "".join(out)
                self.string, i = None, j + 1
                out.append("s")
                continue
            c = line[i]
            if self.triple and line.startswith(('"""', "'''"), i):
                self.string, i = line[i:i + 3], i + 3
                continue
            if self.dollar and line.startswith("$$", i):
                self.string, i = "$$", i + 2
                continue
            if c in "\"'":
                if c == "'" and self.charq:
                    m = re.match(r"'(?:\\.[^']*|[^\\'])'", line[i:])
                    i += m.end() if m else 1
                    out.append("s" if m else "")
                    continue
                self.string, i = c, i + 1
                continue
            if c == "`" and self.backtick:
                self.string, i = "`", i + 1
                continue
            if self.slash and line.startswith("//", i):
                break
            if self.slash and line.startswith("/*", i):
                self.comment, i = True, i + 2
                continue
            if self.hash and c == "#" and (i == 0 or line[i - 1] in " \t;("):
                break
            if self.dash and line.startswith("--", i):
                break
            out.append(c)
            i += 1
        if self.string in ('"', "'"):
            self.string = None
        return "".join(out)


def depth_of(code):
    return sum(1 if ch in "([{" else -1 for ch in code if ch in "([{)]}")


def indent_of(s):
    return len(s) - len(s.lstrip())


def next_code_line(lines, i):
    for j in range(i + 1, min(len(lines), i + 4)):
        if lines[j].strip():
            return lines[j].strip()
    return ""


def header_end(lines, start, sc):
    """End of the statement that opens at start: brackets closed, strings closed, no continuation."""
    depth = 0
    for i in range(start, min(len(lines), start + BLOCK_LINES)):
        code = sc.feed(lines[i])
        depth += depth_of(code)
        if depth <= 0 and not sc.string and not sc.comment and not code.rstrip().endswith("\\"):
            return i, code.rstrip()
    return None, ""


def indent_block_end(lines, start, rel, sc):
    hend, last_code = header_end(lines, start, sc)
    if hend is None:
        return None
    yaml = rel.lower().endswith((".yaml", ".yml"))
    if not last_code.endswith(":") and not yaml:
        return hend
    base, last = indent_of(lines[start]), hend
    for j in range(hend + 1, min(len(lines), start + BLOCK_LINES)):
        was_string = bool(sc.string)
        sc.feed(lines[j])
        if was_string:
            last = j
            continue
        if not lines[j].strip():
            continue
        if indent_of(lines[j]) <= base:
            break
        last = j
    return last


def brace_block_end(lines, start, sc):
    depth, opened = 0, False
    for i in range(start, min(len(lines), start + BLOCK_LINES)):
        code = sc.feed(lines[i])
        for ch in code:
            if ch in "([{":
                depth, opened = depth + 1, True
            elif ch in ")]}":
                depth -= 1
                if depth < 0:
                    return i
        if depth > 0 or sc.string or sc.comment:
            continue
        tail = code.rstrip()
        nxt = next_code_line(lines, i)
        if tail.endswith(CONTINUES) or nxt.startswith("{") or nxt.startswith(NEXT_CONTINUES):
            continue
        if opened or tail:
            return i
    return None


def sql_block_end(lines, start, sc):
    depth = 0
    for i in range(start, min(len(lines), start + BLOCK_LINES)):
        code = sc.feed(lines[i])
        depth += depth_of(code)
        if depth <= 0 and not sc.string and ";" in code:
            return i
    return None


def block_end(lines, start, rel):
    """0-based index of the last line of the block that opens at start, or None."""
    mode = lang(rel)
    if mode == "md":
        m = re.match(r"^(#+)\s", lines[start])
        if not m:
            return start
        level, last = len(m.group(1)), start
        for j in range(start + 1, len(lines)):
            h = re.match(r"^(#+)\s", lines[j])
            if h and len(h.group(1)) <= level:
                break
            last = j
        return trim_blank(lines, start, last)
    if mode == "make":
        last = start
        for j in range(start + 1, len(lines)):
            if lines[j].startswith("\t"):
                last = j
            elif lines[j].strip():
                break
        return last
    if mode == "ini" and lines[start].lstrip().startswith("["):
        last = start
        for j in range(start + 1, len(lines)):
            if lines[j].startswith("["):
                break
            last = j
        return trim_blank(lines, start, last)
    sc = Scanner(rel)
    if mode == "indent" or mode == "ini":
        return indent_block_end(lines, start, rel, sc)
    if mode == "sql":
        return sql_block_end(lines, start, sc)
    return brace_block_end(lines, start, sc)


def trim_blank(lines, start, last):
    while last > start and not lines[last].strip():
        last -= 1
    return last


def sig_end(lines, start, rel):
    """0-based index of the last line of the signature that opens at start."""
    sc = Scanner(rel)
    if lang(rel) in ("indent", "ini"):
        end, _ = header_end(lines, start, sc)
        return start if end is None else end
    depth = 0
    for i in range(start, min(len(lines), start + 60)):
        code = sc.feed(lines[i])
        for ch in code:
            if ch in "([":
                depth += 1
            elif ch in ")]":
                depth -= 1
            elif ch == "{" and depth == 0:
                return i
        tail = code.rstrip()
        if depth <= 0 and (tail.endswith((";", ":")) or (tail and not tail.endswith(CONTINUES)
                                                           and not next_code_line(lines, i).startswith(("{",) + NEXT_CONTINUES))):
            return i
    return start


def leading_start(lines, idx):
    """Doc comments and decorators directly above a definition."""
    j = idx
    while j > 0:
        prev = lines[j - 1].strip()
        if prev.startswith(("//", "#", "@", "/*", "*", "--", "\"\"\"")) and not prev.startswith(("#!", "# %%")):
            j -= 1
            continue
        break
    return j


def def_patterns(name):
    n = re.escape(name)
    mods = r"(?:(?:export|default|public|private|protected|internal|static|final|abstract|sealed|data|open|override|async|readonly|virtual|suspend|pub(?:\([^)]*\))?|unsafe|const|extern(?:\s+\"[^\"]*\")?)\s+)*"
    return [
        (rf"^\s*{mods}function\*?\s+{n}\b", 0),
        (rf"^\s*{mods}(?:class|interface|enum|struct|trait|module|object|record|protocol|union|impl)\s+{n}\b", 0),
        (rf"^\s*{mods}(?:declare\s+)?type\s+{n}\b", 0),
        (rf"^\s*(?:async\s+)?def\s+{n}\b", 0),
        (rf"^\s*{mods}fn\s+{n}\b", 0),
        (rf"^func\s+(?:\([^)]*\)\s*)?{n}\b", 0),
        (rf"^\s*(?:export\s+)?(?:const|let|var|val|static|final)\s+(?:mut\s+)?{n}\b", 0),
        (rf"^\s*{n}\s*(?::\s*[^=]+)?=(?!=)", 0),
        (rf"^\s*{n}\s+[\w.*\[\]<>, ]+?\s*=(?!=)", 0),
        (rf"^\s*{mods}(?:get\s+|set\s+)?{n}\s*(?:<[^>]*>)?\s*\(.*\)\s*(?::\s*[^;{{]+)?\s*\{{\s*$", 0),
        (rf"^\s*(?:function\s+)?{n}\s*\(\)\s*\{{", 0),
        (rf"^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:UNLOGGED\s+|TEMP(?:ORARY)?\s+)?(?:TABLE|VIEW|MATERIALIZED\s+VIEW|UNIQUE\s+INDEX|INDEX|FUNCTION|TYPE|TRIGGER|SEQUENCE)\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:[\w\"]+\.)?\"?{n}\b", re.I),
        (rf"^{n}\s*:(?!=)", 0),
        (rf"^\s*-?\s*{n}\s*:", 0),
        (rf"^\s*\[{n}\]", 0),
        (rf"^#+\s+{n}\b", 0),
    ]


def find_defs(lines, name, lo=0, hi=None):
    """0-based definition lines of name within [lo, hi): the first pattern that matches wins, least indented kept."""
    hi = len(lines) if hi is None else hi
    for pat, flags in def_patterns(name):
        rx = re.compile(pat, flags)
        found = [i for i in range(lo, hi) if rx.search(lines[i])]
        if found:
            least = min(indent_of(lines[i]) for i in found)
            return [i for i in found if indent_of(lines[i]) == least]
    return []


def find_named(lines, rel, name):
    """Definitions of name; `A.B` is B inside A (a Go receiver, or a class body). -> ([idx], how it was read)."""
    if "." not in name:
        return find_defs(lines, name), None
    outer, inner = name.rsplit(".", 1)
    outer = outer.split(".")[-1]
    rx = re.compile(rf"^func\s+\(\s*\w*\s*\*?{re.escape(outer)}(?:\[[^\]]*\])?\s*\)\s*{re.escape(inner)}\b")
    found = [i for i, line in enumerate(lines) if rx.search(line)]
    if found:
        return found, None
    for o in find_defs(lines, outer):
        end = block_end(lines, o, rel)
        if end is not None:
            inside = find_defs(lines, inner, o + 1, end + 1)
            if inside:
                return inside, None
    alone = find_defs(lines, inner)
    return alone, (f"read as `{inner}`: no `{inner}` inside a `{outer}` here" if alone else None)


GENERIC_DEF = [
    re.compile(r"^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\*?\s+([A-Za-z_$][\w$]*)"),
    re.compile(r"^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:class|interface|enum|struct|trait|protocol)\s+([A-Za-z_]\w*)"),
    re.compile(r"^\s*(?:export\s+)?type\s+([A-Za-z_]\w*)\b"),
    re.compile(r"^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)"),
    re.compile(r"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)"),
    re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)"),
    re.compile(r"^(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*(?::[^=]+)?=>"),
]


def all_defs(lines, rel):
    out = []
    go = rel.endswith(".go")
    for i, line in enumerate(lines):
        for rx in GENERIC_DEF:
            m = rx.match(line)
            if m:
                out.append((i, m.group(1), go))
                break
    return out


def nested_in_function(lines, defs_here, i):
    """True when the definition at i sits inside a function body, not at top level or in a class."""
    ind = indent_of(lines[i])
    if ind == 0:
        return False
    for j, _, _ in reversed([d for d in defs_here if d[0] < i]):
        if indent_of(lines[j]) < ind:
            return not re.match(r"^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:class|interface|struct|trait|protocol|impl|object)\b", lines[j])
    return False


def enclosing_def(lines, rel, idx):
    """(name, start, end) of the innermost definition holding 0-based line idx, or None."""
    best = None
    for i, name, _ in all_defs(lines, rel):
        if i > idx:
            break
        end = block_end(lines, i, rel)
        if end is not None and i <= idx <= end and (best is None or i >= best[1]):
            best = (name, i, end)
    return best


# ---------------------------------------------------------------- collect


def numbered(lines, a, b, mark=None):
    return [f"{n}{'*' if mark and n in mark else ''}\t{lines[n - 1]}" for n in range(a, b + 1)]


def bre_or_re(pattern):
    """A caller's grep pattern may be BRE (`a\\|b`, `f(`); read it the way grep would. -> (regex, how)."""
    if "\\|" not in pattern and "\\(" not in pattern:
        try:
            return re.compile(pattern), None
        except re.error:
            pass
    out, i = [], 0
    while i < len(pattern):
        c = pattern[i]
        if c == "\\" and i + 1 < len(pattern):
            nxt = pattern[i + 1]
            out.append({"|": "|", "(": "(", ")": ")", "{": "{", "}": "}", "+": "+", "?": "?"}.get(nxt, "\\" + nxt))
            i += 2
            continue
        out.append("\\" + c if c in "()|{}+?" else c)
        i += 1
    try:
        return re.compile("".join(out)), "read as grep BRE"
    except re.error:
        pass
    try:
        return re.compile(pattern), None
    except re.error:
        return re.compile(re.escape(pattern)), "not a valid regex; matched literally"


class Miss(Exception):
    pass


def load_file(root, path):
    rel, full = resolve_path(root, path)
    if rel.startswith(".."):
        raise Miss(f"{path} is outside the repo root")
    if not os.path.isfile(full):
        near = difflib.get_close_matches(os.path.basename(rel), [f for f in os.listdir(os.path.dirname(full))]
                                         if os.path.isdir(os.path.dirname(full)) else [], n=3)
        raise Miss(f"no file {rel}" + (f" · same directory holds: {', '.join(near)}" if near else ""))
    if classify(rel, os.path.getsize(full)) == "DATA":
        raise Miss(f"{rel} is a data or fixture-record file; it is never opened")
    lines, why = read_lines(full)
    if lines is None:
        raise Miss(f"{rel} is {why}")
    return rel, lines


def cut_note(lines, rel, a, b):
    """Name a definition a line range cuts through, so a partial order is visible."""
    notes = []
    for edge, idx in (("starts", a - 1), ("ends", b - 1)):
        enc = enclosing_def(lines, rel, idx)
        if not enc:
            continue
        name, s, e = enc
        if edge == "starts" and s < idx:
            notes.append(f"starts inside `{name}` ({s + 1}-{e + 1})")
        if edge == "ends" and e > idx:
            notes.append(f"ends inside `{name}` ({s + 1}-{e + 1})")
    return " · CUT: range " + " and ".join(notes) if notes else ""


def run_verb(root, files, verb, args):
    """-> list of (header, [body lines]) sub-blocks; raises Miss."""
    opts = {"C": 0, "w": False, "i": False, "public": False, "containing": None, "n": 1, "F": False}
    pos, k = [], 0
    while k < len(args):
        a = args[k]
        if a in ("-C", "--context"):
            opts["C"], k = int(args[k + 1]), k + 2
        elif re.fullmatch(r"-C\d+", a):
            opts["C"], k = int(a[2:]), k + 1
        elif a in ("-w", "-i", "--public"):
            opts[a.lstrip("-")], k = True, k + 1
        elif verb == "grep" and a in ("-A", "-B"):
            opts["C"], k = max(opts["C"], int(args[k + 1])), k + 2
        elif verb == "grep" and a == "-F":
            opts["F"], k = True, k + 1
        elif verb == "grep" and re.fullmatch(r"-[nrRHEsI]+", a):
            k += 1
        elif a == "--containing":
            opts["containing"], k = args[k + 1], k + 2
        elif a == "-n":
            opts["n"], k = int(args[k + 1]), k + 2
        else:
            pos.append(a)
            k += 1
    if verb == "grep":
        header, body, how = grep_block(root, files, pos, opts)
        if " · 0 hits " in header and not body:
            read = f", {how}" if how else ""
            raise Miss(f"no line matches /{pos[0]}/ in {' '.join(pos[1:]) or 'the whole repo'} (searched{read})")
        return [(header, body)]
    if verb == "consts":
        return [consts_block(root, files, pos)]
    if not pos:
        raise Miss(f"{verb} needs a file path")
    rel, lines = load_file(root, pos[0])
    total = len(lines)
    if verb == "file":
        if total > BLOCK_LINES:
            raise Miss(f"{rel} has {total} lines, over {BLOCK_LINES}; order line ranges")
        return [(f"@ {rel}:1-{total} · whole file · {total} lines", numbered(lines, 1, total))]
    if verb == "lines":
        if len(pos) < 3:
            raise Miss("lines needs PATH FROM TO")
        a = int(pos[1])
        b = total if pos[2] in ("$", "end", "END") else int(pos[2])
        if a < 1 or a > total:
            raise Miss(f"{rel} has {total} lines; line {a} does not exist")
        clipped = f" · file ends at {total}" if b > total else ""
        b = min(b, total)
        return [(f"@ {rel}:{a}-{b} · {b - a + 1} lines{clipped}{cut_note(lines, rel, a, b)}", numbered(lines, a, b))]
    if verb in ("def", "sig"):
        if len(pos) < 2:
            raise Miss(f"{verb} needs PATH NAME")
        out = []
        for name in pos[1:]:
            where, wlines = rel, lines
            idxs, how = find_named(lines, rel, name)
            if not idxs:
                found = elsewhere(root, files, rel, name)
                if len(found) != 1:
                    near = nearest_names(lines, rel, name)
                    if "." in name:
                        meth = name.rsplit(".", 1)[1]
                        owners = [f"{f}:{i + 1} `{wl[i].strip()[:80]}`" for f, c in files if os.path.dirname(f) == os.path.dirname(rel)
                                  for wl in [read_lines(os.path.join(root, f))[0] or []] for i in find_defs(wl, meth)][:4]
                        near += f" · `{meth}` is defined here instead: {'; '.join(owners)}" if owners else ""
                    other = f" · defined in several other files: {', '.join(f'{r}:{i + 1}' for r, _, ix, _ in found for i in ix[:1])}" if found else ""
                    out.append(("MISS", f"no definition named `{name}` in {rel}{near}{other}"))
                    continue
                where, wlines, idxs, how = found[0]
                how = f"not in {rel}; defined in {where}" + (f" ({how})" if how else "")
            for idx in idxs:
                if verb == "sig":
                    end = sig_end(wlines, idx, where)
                    a = idx
                else:
                    end = block_end(wlines, idx, where)
                    if end is None:
                        out.append(("MISS", f"`{name}` at {where}:{idx + 1} opens a block the script could not close within {BLOCK_LINES} lines; order a line range"))
                        continue
                    a = leading_start(wlines, idx)
                note = f" · {how}" if how else ""
                many = f" · {len(idxs)} definitions" if len(idxs) > 1 else ""
                out.append((f"@ {where}:{a + 1}-{end + 1} · {verb} {name}{many} · {end - a + 1} lines{note}",
                            numbered(wlines, a + 1, end + 1)))
        return out
    if verb == "block":
        if len(pos) < 2:
            raise Miss("block needs PATH REGEX")
        rx, how = bre_or_re(pos[1])
        hits = [i for i, line in enumerate(lines) if rx.search(line)]
        if len(hits) < opts["n"]:
            raise Miss(f"no line matching /{pos[1]}/ in {rel}" if not hits else
                       f"/{pos[1]}/ matches {len(hits)} lines in {rel}, not {opts['n']}")
        idx = hits[opts["n"] - 1]
        end = block_end(lines, idx, rel)
        if end is None:
            raise Miss(f"the block at {rel}:{idx + 1} does not close within {BLOCK_LINES} lines; order a line range")
        more = f" · pattern matches {len(hits)} lines, block of match {opts['n']}" if len(hits) > 1 else ""
        note = f" · {how}" if how else ""
        return [(f"@ {rel}:{idx + 1}-{end + 1} · block /{pos[1]}/{more}{note} · {end - idx + 1} lines",
                 numbered(lines, idx + 1, end + 1))]
    if verb == "defs":
        rx = bre_or_re(opts["containing"])[0] if opts["containing"] else None
        body, count = [], 0
        defs_here = all_defs(lines, rel)
        for i, name, go in defs_here:
            if opts["public"] and (name.startswith("_") or (go and not name[:1].isupper()) or nested_in_function(lines, defs_here, i)):
                continue
            send = sig_end(lines, i, rel)
            if rx:
                end = block_end(lines, i, rel) or send
                hits = [j + 1 for j in range(send + 1, end + 1) if rx.search(lines[j])]
                if not hits and not any(rx.search(lines[j]) for j in range(i, send + 1)):
                    continue
                body += numbered(lines, i + 1, send + 1) + [f"{j}*\t{lines[j - 1]}" for j in hits]
            else:
                body += numbered(lines, i + 1, send + 1)
            count += 1
        if not count:
            raise Miss(f"no {'public ' if opts['public'] else ''}definition in {rel}" +
                       (f" whose body matches /{opts['containing']}/" if rx else ""))
        what = f" whose body matches /{opts['containing']}/ (matching lines marked *)" if rx else ""
        return [(f"@ {rel} · {count} {'public ' if opts['public'] else ''}definitions{what} · signatures", body)]
    raise Miss(f"unknown verb `{verb}`; the verbs are {', '.join(VERBS)}")


def elsewhere(root, files, rel, name):
    """Files other than rel defining name: the same directory first, then every code file. -> [(rel, lines, idxs, how)]"""
    key = name.split(".")[-1]
    here = os.path.dirname(rel)
    tiers = [[f for f, c in files if os.path.dirname(f) == here and f != rel and c in ("CODE", "TEST")],
             [f for f, c in files if os.path.dirname(f) != here and c == "CODE"]]
    for tier in tiers:
        found = []
        for other in tier:
            lines, _ = read_lines(os.path.join(root, other))
            if not lines or key not in "\n".join(lines):
                continue
            idxs, how = find_named(lines, other, name)
            if idxs and not how:
                found.append((other, lines, idxs, how))
        if found:
            return found
    return []


def consts_block(root, files, pos):
    """Every constant or variable declared with type TYPE under PATH (Go `Name TYPE = …`, `Name = TYPE(…)`, and the iota run after them)."""
    if len(pos) < 2:
        raise Miss("consts needs PATH TYPE")
    typ = re.escape(pos[1])
    rx = re.compile(rf"^\s*([A-Za-z_]\w*)\s+{typ}\s*=|^\s*([A-Za-z_]\w*)\s*(?:{typ}\s*)?=\s*{typ}\(")
    rel0, _ = resolve_path(root, pos[0])
    pre = rel0.rstrip("/")
    body, n = [], 0
    for rel, cls in files:
        if not (rel == pre or rel.startswith(pre + "/")) or cls not in ("CODE", "GENERATED"):
            continue
        lines, _ = read_lines(os.path.join(root, rel))
        for i, line in enumerate(lines or []):
            if rx.search(line):
                j = i
                body.append(f"{rel}:{i + 1}\t{line}")
                n += 1
                if "iota" in line:
                    while j + 1 < len(lines) and re.match(r"^\s*[A-Za-z_]\w*\s*(//.*)?$", lines[j + 1]):
                        j += 1
                        body.append(f"{rel}:{j + 1}\t{lines[j]}")
                        n += 1
    if not n:
        raise Miss(f"no constant of type {pos[1]} under {pos[0]}")
    return (f"@ consts {pos[1]} under {pos[0]} · {n} declarations", body)


def nearest_names(lines, rel, name):
    names = sorted({n for _, n, _ in all_defs(lines, rel)})
    near = difflib.get_close_matches(name.split(".")[-1], names, n=4, cutoff=0.5)
    low = [n for n in names if name.split(".")[-1].lower() in n.lower() and n not in near][:3]
    cand = near + low
    return f" · definitions with near names here: {', '.join(cand)}" if cand else " · (no definition in this file has a near name)"


def grep_block(root, files, pos, opts):
    if not pos:
        raise Miss("grep needs a pattern")
    rx, how = (re.compile(re.escape(pos[0])), "fixed string") if opts.get("F") else bre_or_re(pos[0])
    if opts["i"]:
        rx = re.compile(rx.pattern, re.I)
    if opts["w"]:
        rx = re.compile(r"(?<![\w$])(?:" + rx.pattern + r")(?![\w$])", rx.flags)
    scope = []
    for p in pos[1:] or ["."]:
        rel, full = resolve_path(root, p)
        if rel.startswith(".."):
            raise Miss(f"{p} is outside the repo root")
        pre = "" if rel == "." else rel.rstrip("/")
        chosen = [f for f in files if pre == "" or f[0] == pre or f[0].startswith(pre + "/")]
        if not chosen:
            raise Miss(f"no file under {p}")
        scope += chosen
    hits, data, bad = scan(root, scope, rx)
    n = sum(len(v) for v in hits.values())
    body = []
    for rel in sorted(hits):
        lines, _ = read_lines(os.path.join(root, rel))
        if opts["C"]:
            body.append(f"@ {rel}")
            shown = merge_windows(hits[rel], opts["C"], len(lines))
            for w, (a, b) in enumerate(shown):
                if w:
                    body.append("--")
                body += numbered(lines, a, b, set(hits[rel]))
        else:
            body += [f"{rel}:{h}\t{lines[h - 1]}" for h in hits[rel]]
    for rel, c in sorted(data.items()):
        body.append(f"{rel} — {c} matches in a data or fixture-record file, not printed")
    for b in bad:
        body.append(f"UNREAD {b}")
    note = f" · {how}" if how else ""
    ctx = f" · ±{opts['C']} lines, hits marked *" if opts["C"] else ""
    return (f"@ grep /{rx.pattern}/ in {' '.join(pos[1:]) or 'the whole repo'} · {n} hits in {len(hits)} files{ctx}{note}", body, how)


def merge_windows(nums, c, total):
    out = []
    for h in nums:
        a, b = max(1, h - c), min(total, h + c)
        if out and a <= out[-1][1] + 1:
            out[-1] = (out[-1][0], max(out[-1][1], b))
        else:
            out.append((a, b))
    return out


def parse_plan(text):
    orders, cur = [], None
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        m = re.match(r"^=\s*(\S+)\s*(.*)$", line)
        if m:
            cur = {"id": m.group(1).rstrip(".:"), "text": m.group(2), "cmds": []}
            orders.append(cur)
            continue
        if cur is None:
            die(f"the plan's first line must open an order (`= 1 the caller's order`); got: {line}")
        cur["cmds"].append(line)
    if not orders:
        die("the plan on stdin holds no order; each order opens with `= ID text`, then its commands")
    return orders


def cmd_collect(args):
    expect = None
    if "--expect" in args:
        k = args.index("--expect")
        expect = expand_ids(args[k + 1]) if k + 1 < len(args) else die("--expect needs the caller's order ids, e.g. 1-9")
        args = args[:k] + args[k + 2:]
    if not args:
        die("usage: collect ROOT [DIR] --expect IDS <<'EOF' … EOF")
    root = os.path.abspath(args[0])
    if not os.path.isdir(root):
        die(f"no directory {root}")
    d = args[1] if len(args) > 1 else new_dir("collect", root)
    os.makedirs(d, exist_ok=True)
    plan = sys.stdin.read()
    orders = parse_plan(plan)
    if expect is not None:
        got = [o["id"] for o in orders]
        if sorted(set(got)) != sorted(set(expect)):
            die(f"the caller's orders are {', '.join(expect)}; the plan's are {', '.join(got)}. Put every command of one caller "
                "order under that order's own `= ID` line — several commands under one ID — and run again")
    hist_path = os.path.join(d, "history.json")
    history = json.load(open(hist_path)) if os.path.isfile(hist_path) else {}
    runs_path = os.path.join(d, "runs")
    runs = int(open(runs_path).read()) if os.path.isfile(runs_path) else 0
    if runs >= 2 and os.path.isfile(os.path.join(d, "manifest.txt")):
        print("RETRY SPENT — this DIR had its run and its retry; the manifest below is final and is your final message.\n")
        print(open(os.path.join(d, "manifest.txt")).read(), end="")
        return
    with open(runs_path, "w") as fh:
        fh.write(str(runs + 1))
    with open(os.path.join(d, "plan.txt"), "w") as fh:
        fh.write(plan)
    files, _ = list_files(root)
    out, manifest, misses, total_lines, run = [], [], 0, 0, {}
    for gone in [i for i in history if i not in {o["id"] for o in orders}]:
        orders.append({"id": gone, "text": history[gone]["text"], "cmds": [], "dropped": history[gone]["cmds"]})
    for o in orders:
        out.append(f"### {o['id']} — {o['text']}")
        manifest.append(f"### {o['id']} — {o['text']}")
        run[o["id"]] = {"text": o["text"], "cmds": {}}
        for c, why in history.get(o["id"], {}).get("cmds", {}).items():
            if c not in o["cmds"] and why != "ok":
                o.setdefault("carried", []).append((c, why))
        if o.get("dropped") is not None:
            for c, why in o["dropped"].items():
                line = f"MISS {o['id']} — `{c}`: dropped from the plan on retry" + (f"; it had missed: {why}" if why != "ok" else "")
                out.append(line)
                manifest.append(line)
                misses += 1
            if not o["dropped"]:
                out.append(f"MISS {o['id']} — dropped from the plan on retry")
                manifest.append(out[-1])
                misses += 1
            continue
        for c, why in o.get("carried", []):
            line = f"MISS {o['id']} — `{c}`: {why} (the retry dropped this command)"
            out.append(line)
            manifest.append(line)
            misses += 1
        if not o["cmds"] and not o.get("carried"):
            out.append(f"MISS {o['id']} — the plan gave this order no command")
            manifest.append(out[-1])
            misses += 1
            continue
        got = []
        for c in o["cmds"]:
            def miss(why):
                line = f"MISS {o['id']} — `{c}`: {why}"
                out.append(line)
                manifest.append(line)
                run[o["id"]]["cmds"][c] = why
            try:
                parts = shlex.split(c)
            except ValueError as err:
                miss(f"cannot split the command ({err})")
                misses += 1
                continue
            try:
                ok = True
                for header, body in run_verb(root, files, parts[0], parts[1:]):
                    if header == "MISS":
                        miss(body)
                        misses += 1
                        ok = False
                        continue
                    out.append(header)
                    out += body
                    manifest.append(header)
                    total_lines += len(body)
                if ok:
                    run[o["id"]]["cmds"][c] = "ok"
            except Miss as m:
                miss(str(m))
                misses += 1
            except (ValueError, IndexError) as err:
                miss(f"bad arguments ({err})")
                misses += 1
    with open(hist_path, "w") as fh:
        json.dump({**history, **run}, fh)
    bad_orders = len({line.split()[1] for line in manifest if line.startswith("MISS ")})
    ret = os.path.join(d, "return.md")

    def build(parts):
        head = (f"COLLECTED {len(orders)} orders — {len(orders) - bad_orders} complete, {bad_orders} with a MISS · "
                f"{total_lines} lines of verbatim text in {ret}{parts}")
        text = (head + "\nEach text line is `LINE<TAB>text` (grep hits: `path:LINE<TAB>text`), copied by script from the file its "
                f"@ header names; root {root}.\n\n" + "\n".join(out) + f"\n\nEND {len(orders)} orders · {total_lines} lines\n"
                "(This file is the caller's copy. The collector's final message is the manifest collect printed; retyping this text corrupts it.)\n")
        return head, text

    def parts_hint(text):
        text_lines = text.splitlines()
        n = len(text_lines)
        spans, start, chars = [], 0, 0
        for i, line in enumerate(text_lines):
            ln = len(line) + 1
            if chars and chars + ln > PART_CHARS:
                spans.append((start, i))
                start, chars = i, 0
            chars += ln
        spans.append((start, n))
        if len(spans) <= 1:
            return ""
        return " — Read it in parts: " + ", ".join(f"offset {a + 1} limit {b - a}" for a, b in spans)

    _, text0 = build("")
    parts = parts_hint(text0)
    head, text = build(parts)
    if parts:
        # inserting the hint on line 1 can shift boundaries by at most its own length; recompute once
        parts = parts_hint(text)
        head, text = build(parts)
    with open(ret, "w") as fh:
        fh.write(text)
    summary = (head + "\nRead that file for the text; below, each order under the caller's number, the @ header of every "
               "block the file holds for it, and each MISS.\n" + "\n".join(manifest) + f"\nEND {len(orders)} orders\n")
    with open(os.path.join(d, "manifest.txt"), "w") as fh:
        fh.write(summary)
    if misses and not history:
        print(f"DIR {d}\n{misses} MISS — correct each MISS command from the names it offers and run collect once more with the "
              "WHOLE plan and this DIR. Whatever that retry prints is final.\n")
    else:
        print("YOUR FINAL MESSAGE is the manifest below, copied whole from COLLECTED to END.\n")
    print(summary, end="")


def expand_ids(spec):
    out = []
    for part in spec.split(","):
        m = re.fullmatch(r"(\d+)-(\d+)", part.strip())
        out += [str(n) for n in range(int(m.group(1)), int(m.group(2)) + 1)] if m else [part.strip()]
    return [x for x in out if x]


# ---------------------------------------------------------------- run directory


def new_dir(label, root):
    slug = re.sub(r"[^A-Za-z0-9]+", "-", label).strip("-")[:40].lower() or "probe"
    d = os.path.join(probe_base(root), f"{slug}-{time.strftime('%H%M%S')}-{os.getpid()}")
    os.makedirs(d, exist_ok=True)
    return d


def cmd_verbs(args):
    """Print SKILL.md's extraction verbs and collect sections: one source for the syntax."""
    path = os.path.join(os.path.dirname(os.path.realpath(__file__)), "SKILL.md")
    try:
        text = open(path).read()
    except OSError as err:
        die(f"cannot read {path}: {err}")
    m = re.search(r"^## Extraction verbs$.*", text, re.S | re.M)
    if not m or not re.search(r"^## collect\b", m.group(0), re.M):
        die(f"{path} has no § Extraction verbs followed by § collect")
    print(m.group(0).rstrip())


def main():
    cmds = {"verbs": cmd_verbs, "collect": cmd_collect}
    if len(sys.argv) < 2 or sys.argv[1] not in cmds:
        die("usage: codeprobe.py {" + "|".join(cmds) + "} … — " + (__doc__ or "").split("\n\n")[2].strip().replace("\n", " ; "))
    cmds[sys.argv[1]](sys.argv[2:])


if __name__ == "__main__":
    main()
