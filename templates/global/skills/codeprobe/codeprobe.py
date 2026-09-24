#!/usr/bin/env python3
"""codeprobe — the computed half of `collector`, `tracer` and `mapper`.

Every line it prints from a file is that file's text, numbered by the script. Every
list it computes is complete or says what it left out. Every check names its own
failure. Python 3 standard library only; it reads the repo and writes only under
/tmp/{project}/codeprobe/.

  collect ROOT [DIR] --expect IDS   orders on stdin -> verbatim text in DIR/return.md, a manifest printed
  init ROOT [--map TARGET]    asks on stdin -> DIR, file classes, computed lists
  refs DIR NAME... [-C N] [--class C] [--path P]   every line naming NAME, with context
  absent DIR NAME [--in PATH]  a checked zero -> an [A#] row
  rows DIR [--replace]        rows on stdin -> appended, verified, return rendered
  render DIR                  render the return from the verified rows
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
RETURN_BYTES = 150_000
LIST_LINES = 150
RANGE_LINES = 12
ANCHOR_CHARS = 90
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
HEDGE_RE = re.compile(r"\b(probably|likely|unlikely|seems?|seemingly|appears? to|apparently|presumably|perhaps|possibly|"
                      r"maybe|might|i think|i believe|not sure|unclear|actually)\b", re.I)
EACH_RE = re.compile(r"\b(what|how)\b.{0,40}\beach\b|\beach\b.{0,40}\b(does|do|uses?|means?|with)\b|\bwhat (it|they) (does|do)\b|"
                     r"\bclassif\w*|\bwhich (kind|field|of them|of these)\b|\bwhether (it|each)\b", re.I)
FULL_RE = re.compile(r"\b(in full|whole|entire|verbatim|full (text|definition|file|body))\b", re.I)
PRINT_RE = re.compile(r"(print|Print|Fprint|Sprintf|Errorf|log\.|logger\.|slog\.|console\.|echo\b|warn\(|error\(|info\(|write\(|Write\(|raise\b|throw\b|panic\(|fmt\.|errors\.New)")
NOTE_IDENT_RE = re.compile(r"\b(?:[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+|[a-z]+[A-Z][A-Za-z0-9]*|[A-Z][a-z0-9]+[A-Z][A-Za-z0-9]*)\b")
UNIVERSAL_RE = re.compile(r"\b(single|sole|solely|exclusively|nowhere|no other|none|nothing|never|unused|no callers?|no tests?|"
                          r"not (?:used|called|imported|referenced|tested)|(?<![-\w])only(?!-))\b", re.I)
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


def variants(term):
    parts = [p for p in re.split(r"[_\-\s]+|(?<=[a-z0-9])(?=[A-Z])", term) if p]
    if len(parts) < 2:
        return [term]
    low = [p.lower() for p in parts]
    forms = [term, "_".join(low), "-".join(low), "_".join(low).upper(),
             low[0] + "".join(p.capitalize() for p in low[1:]), "".join(p.capitalize() for p in low)]
    return list(dict.fromkeys(forms))


def name_regex(spec, flags=0):
    """A census spec: a plain name is word-matched; /regex/ is used as written."""
    if len(spec) > 2 and spec.startswith("/") and spec.endswith("/"):
        return re.compile(spec[1:-1], flags)
    return re.compile(r"(?<![\w$])" + re.escape(spec) + r"(?![\w]|\$(?!\{))", flags)


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
    if "\\|" in pattern or "\\(" in pattern:
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
        header, body = grep_block(root, files, pos, opts)
        if " · 0 hits " in header and not body:
            raise Miss(f"no line matches /{pos[0]}/ in {' '.join(pos[1:]) or 'the whole repo'} (searched)")
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
    return (f"@ grep /{rx.pattern}/ in {' '.join(pos[1:]) or 'the whole repo'} · {n} hits in {len(hits)} files{ctx}{note}", body)


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


# ---------------------------------------------------------------- probe state


def new_dir(label, root):
    slug = re.sub(r"[^A-Za-z0-9]+", "-", label).strip("-")[:40].lower() or "probe"
    d = os.path.join(probe_base(root), f"{slug}-{time.strftime('%H%M%S')}-{os.getpid()}")
    os.makedirs(d, exist_ok=True)
    return d


def load(d):
    try:
        with open(os.path.join(d, "state.json")) as fh:
            return json.load(fh)
    except (OSError, json.JSONDecodeError) as err:
        die(f"no probe state in {d} ({err}); run init first")


def save(d, state):
    with open(os.path.join(d, "state.json"), "w") as fh:
        json.dump(state, fh)


MAP_ASKS = [
    ("M1", "DEFINITION — what defines it: declaration, schema and migrations, keys, constraints, indexes", ""),
    ("M2", "WRITERS — every writer, and what triggers each write", "census"),
    ("M3", "READERS — every reader, and where each read ends (returned, served, rendered, stored, sent)", "census"),
    ("M4", "TIMING — what runs on a clock, expires or retries it", ""),
    ("M5", "CONTROL — guards, validation, configuration and registries around it", ""),
    ("M6", "NEIGHBOURS — the entities it links to, or that change or are removed together with it", ""),
    ("M7", "TESTS — every test and fixture naming it", "tests"),
    ("M8", "CHECK — the check commands, a scoped variant, and whether two copies can run at once on one worktree", "checks"),
]


DIRECTIVES = ("census", "tests", "mentions", "checks")


def parse_asks(text):
    asks, cur = [], None
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        m = re.match(r"^=\s*([A-Z]?\d+[a-z]?\d*)[.:]?\s+(.+)$", line)
        if m:
            cur = {"id": m.group(1), "text": m.group(2).strip(), "dirs": [], "cmds": []}
            asks.append(cur)
            continue
        if cur is None:
            die(f"an ask opens with `= ID the caller's question`, its commands on the lines under it; got: {line}")
        w = line.split(None, 1)
        if w[0] in DIRECTIVES:
            names = [n.strip() for n in re.split(r"[,\s]+", w[1])] if len(w) > 1 else []
            cur["dirs"].append({"kind": w[0], "names": [n for n in names if n]})
        elif w[0] in VERBS:
            cur["cmds"].append(line)
        else:
            die(f"`{w[0]}` under {cur['id']} is no command: the lines under an ask are census NAME…, tests NAME…, "
                f"mentions NAME…, checks, or an extraction verb ({', '.join(VERBS)})")
    return asks


def run_cmd(state, cmd):
    """One extraction command of an ask: [(header, body)] or [("MISS", why)]."""
    try:
        parts = shlex.split(cmd)
        return run_verb(state["root"], state["files"], parts[0], parts[1:])
    except Miss as err:
        return [("MISS", str(err))]
    except (ValueError, IndexError) as err:
        return [("MISS", f"bad arguments ({err})")]


def cmd_init(args):
    target, expect = None, None
    if "--map" in args:
        k = args.index("--map")
        if k + 1 >= len(args):
            die("--map needs the target name")
        target = args[k + 1]
        args = args[:k] + args[k + 2:]
    budget = None
    if "--budget" in args:
        k = args.index("--budget")
        if k + 1 >= len(args) or not args[k + 1].isdigit():
            die("--budget needs the brief's word limit, e.g. 2500")
        budget = int(args[k + 1])
        args = args[:k] + args[k + 2:]
    if "--expect" in args:
        k = args.index("--expect")
        if k + 1 >= len(args):
            die("--expect needs the caller's question numbers, e.g. 1-7")
        expect = expand_ids(args[k + 1])
        args = args[:k] + args[k + 2:]
    if not args:
        die("usage: init ROOT [--map TARGET] [--expect 1-7] <<'EOF' = Q1a the caller's question ⏎ census NAME ⏎ def PATH NAME … EOF")
    root = os.path.abspath(args[0])
    if not os.path.isdir(root):
        die(f"no directory {root}")
    files, source = list_files(root)
    if not files:
        die(f"{source} listed no file under {root}")
    text = "" if sys.stdin.isatty() else sys.stdin.read()
    asks = parse_asks(text)
    if target:
        spell = []
        for v in variants(target):
            h, dt, _ = scan(root, files, name_regex(v))
            if h or dt:
                spell.append(v)
        spell = spell or [target]
        pre = [{"id": i, "text": t, "dirs": [{"kind": k, "names": spell}] if k else [], "cmds": []} for i, t, k in MAP_ASKS]
        pre[-1]["dirs"] = [{"kind": "checks", "names": []}]
        asks = pre + asks
    if not asks:
        die("no ask on stdin; each ask opens with `= Q1a the caller's question`, its commands on the lines under it")
    ids = [a["id"] for a in asks]
    dup = sorted({i for i in ids if ids.count(i) > 1})
    if dup:
        die(f"ask ids repeat: {', '.join(dup)}")
    if expect:
        num = lambda i: re.match(r"[A-Z]?(\d+)", i).group(1)
        lost = [e for e in expect if not any(num(i) == num(e) for i in ids)]
        if lost:
            die(f"the caller's questions {', '.join(expect)} include {', '.join(lost)}, and no ask carries "
                f"{'that number' if len(lost) == 1 else 'those numbers'}: split every question into asks under its own number and run again")
    d = new_dir(target or asks[0]["text"], root)
    state = {"root": root, "files": files, "asks": asks, "absent": [], "target": target, "source": source, "budget": budget}
    save(d, state)
    counts = {}
    for _, cls in files:
        counts[cls] = counts.get(cls, 0) + 1
    print(f"DIR {d}")
    print(f"FILES {len(files)} via {source}: " + " · ".join(f"{c} {n}" for c, n in sorted(counts.items())))
    print("ASKS " + " · ".join(a["id"] + ("[" + ", ".join(dv["kind"] + (" " + ",".join(dv["names"]) if dv["names"] else "")
                                                            for dv in a["dirs"]) + "]" if a["dirs"] else "") for a in asks))
    names = []
    for a in asks:
        for dv in a["dirs"]:
            if dv["kind"] == "census":
                names += [n for n in dv["names"] if n not in names]
    wants = [("census", r"\b(every|all|each)\b.*\b(callers?|call sites?|readers?|writers?|uses?|consumers?)\b|\bwho (calls|reads|writes)\b"),
             ("tests", r"\btests?\b|\bfixtures?\b|\basserted\b"),
             ("mentions", r"\b(renam\w*|remov\w*|delet\w*|drop\w*)\b"),
             ("checks", r"\b(command|make\b|check|lint|run the tests|how .* run)")]
    held = {dv["kind"] for a in asks for dv in a["dirs"]}
    for a in asks:
        for kind, rx in wants:
            if kind not in held and re.search(rx, a["text"], re.I):
                print(f"NOTE {a['id']} asks what `{kind}` computes, and no ask carries it: add it with the name, "
                      f"re-running init with the corrected asks")
                held.add(kind)
    for a in asks:
        parts = len(re.findall(r",|/|;| or ", re.sub(r"`[^`]*`|\([^)]*\)", "", a["text"]))) + 1
        if parts >= 4 and not a["id"].startswith("M"):
            print(f"NOTE {a['id']} names {parts} things in one ask: one fact per ask gives each its own status — split it and re-run init")
    for n in names:
        print_refs(state, n, 2, {"CODE"}, None, budget=260)
    tnames = []
    for a in asks:
        for dv in a["dirs"]:
            if dv["kind"] == "tests":
                tnames += [n for n in dv["names"] if n not in tnames]
    for n in tnames:
        hits = scan(root, files, name_regex(n), {"TEST"})[0]
        body = test_hits(root, hits)
        print(f"\nTESTS {n} — {sum(len(v) for v in hits.values())} lines in {len(hits)} test files, by test:")
        print("\n".join(body[:200]) + (f"\n(+{len(body) - 200} more; the return carries them all)" if len(body) > 200 else ""))
    for a in asks:
        for cmd in a.get("cmds", []):
            print(f"\nTEXT {a['id']} `{cmd}`")
            for header, body in run_cmd(state, cmd):
                if header == "MISS":
                    print(f"MISS {a['id']} `{cmd}`: {body}")
                    continue
                print(header)
                print("\n".join(body[:400]) + (f"\n(+{len(body) - 400} lines; the return carries them all)" if len(body) > 400 else ""))
    if any(dv["kind"] == "checks" for a in asks for dv in a["dirs"]):
        lines = check_lines(state)
        print("\n".join(lines[:200]) + (f"\n(+{len(lines) - 200} more check lines; the return carries them all)" if len(lines) > 200 else ""))


def print_refs(state, spec, c, classes, path, budget=400):
    root = state["root"]
    files = [f for f in state["files"] if not path or f[0] == path or f[0].startswith(path.rstrip("/") + "/")]
    hits, data, bad = scan(root, files, name_regex(spec), classes)
    if classes == {"CODE"}:
        hits = code_only(root, hits, imports=True)
    n = sum(len(v) for v in hits.values())
    by = {}
    for rel in hits:
        cls = dict(state["files"]).get(rel, "?")
        by.setdefault(cls, [0, 0])
        by[cls][0] += len(hits[rel])
        by[cls][1] += 1
    summary = " · ".join(f"{k} {v[0]} lines/{v[1]} files" for k, v in sorted(by.items())) or "none"
    print(f"\nREFS {spec} ({'/'.join(sorted(classes)) if classes else 'every class'}{', under ' + path if path else ''}) — {n} lines: {summary}"
          + (f" · DATA files naming it (never opened): {len(data)}" if data else "") + (f" · UNREAD: {'; '.join(bad)}" if bad else ""))
    out = []
    for rel in sorted(hits):
        lines, _ = read_lines(os.path.join(root, rel))
        out.append(f"== {rel} ({dict(state['files']).get(rel)} · {len(hits[rel])})")
        for w, (a, b) in enumerate(merge_windows(hits[rel], c, len(lines))):
            if w:
                out.append("  --")
            for k in range(a, b + 1):
                out.append(f"{k}{':' if k in hits[rel] else '-'}{lines[k - 1]}")
    if len(out) > budget:
        print(f"(excerpts are {len(out)} lines, over {budget}: files and hit lines only — run refs with --path for excerpts)")
        for rel in sorted(hits):
            print(f"== {rel}: {', '.join(map(str, hits[rel]))}")
    else:
        print("\n".join(out))


def cmd_refs(args):
    if len(args) < 2:
        die("usage: refs DIR NAME... [-C N] [--class CODE,TEST] [--path PREFIX]")
    state = load(args[0])
    c, classes, path, names, k = 2, None, None, [], 1
    while k < len(args):
        if args[k] == "-C":
            c, k = int(args[k + 1]), k + 2
        elif args[k] == "--class":
            classes, k = set(args[k + 1].upper().split(",")), k + 2
        elif args[k] == "--path":
            path, k = args[k + 1], k + 2
        else:
            names.append(args[k])
            k += 1
    for n in names:
        print_refs(state, n, c, classes, path)


CHECK_FILE_RE = re.compile(r"(^|/)(makefile|gnumakefile|[^/]+\.mk|package\.json|pyproject\.toml|tox\.ini|noxfile\.py|justfile|"
                           r"taskfile\.ya?ml|setup\.cfg|pytest\.ini|\.pre-commit-config\.yaml|[^/]*(dev|test|check|lint|ci|verify)[\w-]*\.sh)$", re.I)
WORKFLOW_RE = re.compile(r"(^|/)\.github/workflows/[^/]+\.ya?ml$|(^|/)\.gitlab-ci\.yml$")
RUN_RE = re.compile(r"\b(go (?:test|vet|build)|golangci-lint|pytest|py\.test|ruff|mypy|pyright|uv run|tox|nox|npm (?:run|test)|pnpm|yarn|"
                    r"vitest|jest|playwright|tsc|eslint|cargo (?:test|clippy|build)|make\b|bats|shellcheck|rumdl|markdownlint)")
HINT_RE = re.compile(r"(?<![\w.])(?:port\s*[=:]\s*\d{4,5}|:\d{4,5}\b|localhost:\d+|/tmp/[\w./${}-]+|-coverprofile[= ]\S+|--junitxml\S*|"
                     r"DATABASE_URL|TEST_DB\w*|createdb|dropdb|postgres(?:ql)?://\S+|flock|\.lock\b|lockfile|xdist|-n\s+auto|"
                     r"t\.Parallel\(\)|-parallel\b|-p\s+\d+|--runInBand|--maxWorkers|COMPOSE_PROJECT_NAME|container_name)", re.I)


def check_lines(state):
    """The repo's own check commands, verbatim, plus lines that decide whether two copies can run at once."""
    root, out = state["root"], ["CHECKS — the repo's own check commands, copied by script (path:LINE<TAB>text)"]
    hints = []

    def rank(rel):
        base = os.path.basename(rel.lower())
        if WORKFLOW_RE.search(rel.lower()):
            return 3
        if base.endswith(".sh"):
            return 2
        return 1 if base not in ("setup.cfg", ".pre-commit-config.yaml") else 2
    chosen = [(rel, cls) for rel, cls in state["files"] if rel.count("/") <= 3 and cls != "DATA"
              and (CHECK_FILE_RE.search(rel.lower()) or WORKFLOW_RE.search(rel.lower()))]
    for rel, cls in sorted(chosen, key=lambda f: (rank(f[0]), f[0].count("/"), f[0])):
        low = rel.lower()
        lines, _ = read_lines(os.path.join(root, rel))
        if not lines:
            continue
        base = os.path.basename(low)
        keep = []
        if base in ("makefile", "gnumakefile") or low.endswith(".mk"):
            recipe = 0
            for i, line in enumerate(lines):
                if re.match(r"^[A-Za-z0-9_.%/-]+\s*:(?!=)", line) and not line.startswith("."):
                    keep.append(i)
                    recipe = 0
                elif line.startswith("\t") and keep and (keep[-1] == i - 1 or lines[i - 1].startswith("\t")):
                    recipe += 1
                    if recipe <= 1 and not line.strip().startswith(("@#", "#")):
                        keep.append(i)
        elif base == "package.json":
            s = next((i for i, line in enumerate(lines) if re.match(r'^\s*"scripts"\s*:', line)), None)
            if s is not None:
                e = block_end(lines, s, rel) or s
                keep = list(range(s, e + 1))
        elif base == "pyproject.toml":
            on = False
            for i, line in enumerate(lines):
                if line.startswith("["):
                    on = bool(re.match(r"\[(tool\.(pytest|ruff|mypy|pyright|coverage|poe|hatch|tox|uv|pdm\.scripts)|project\.scripts)", line))
                if on and line.strip():
                    keep.append(i)
        else:
            for i, line in enumerate(lines):
                if line.lstrip().startswith("#"):
                    continue
                if RUN_RE.search(line) or re.match(r"^\s*-?\s*run\s*:", line):
                    keep.append(i)
        if keep:
            out.append(f"@ {rel}")
            out += [f"{i + 1}\t{lines[i][:200]}" for i in keep[:30]]
            if len(keep) > 30:
                out.append(f"(+{len(keep) - 30} more check lines in {rel})")
    for rel, cls in state["files"]:
        low = rel.lower()
        if cls == "DATA" or rel.count("/") > 4:
            continue
        if not (CHECK_FILE_RE.search(low) or WORKFLOW_RE.search(low) or re.search(r"(^|/)(conftest\.py|docker-compose[^/]*\.ya?ml|compose\.ya?ml|main_test\.go|setup_test\.go|jest\.config\.\w+|vitest\.config\.\w+|playwright\.config\.\w+)$", low)):
            continue
        lines, _ = read_lines(os.path.join(root, rel))
        for i, line in enumerate(lines or []):
            if HINT_RE.search(line):
                hints.append(f"{rel}:{i + 1}\t{line.strip()[:200]}")
    if len(out) == 1:
        out.append("(no Makefile, package.json scripts, pyproject tool section, CI workflow or dev/test script within 3 directories of the root)")
    out.append("CONCURRENCY LINES — fixed ports, fixed paths, shared databases, locks and parallelism flags in the check and test setup files:")
    out += hints[:30] or ["(none of those patterns in the check and test setup files)"]
    if len(hints) > 30:
        out.append(f"(+{len(hints) - 30} more)")
    return out


def cmd_absent(args):
    if len(args) < 2:
        die("usage: absent DIR NAME [--in PATH]")
    d, name = args[0], args[1]
    state = load(d)
    path = args[args.index("--in") + 1] if "--in" in args else None
    files = [f for f in state["files"] if not path or f[0] == path.rstrip("/") or f[0].startswith(path.rstrip("/") + "/")]
    if not files:
        die(f"no file under {path}: the zero would prove nothing")
    hits, data, bad = scan(state["root"], files, name_regex(name))
    n = sum(len(v) for v in hits.values())
    scope = path or "the whole repo"
    if n or data:
        print(f"NOT ABSENT — {name} is named {n} times in {scope}; no [A#] row was made:")
        for rel in sorted(hits):
            lines, _ = read_lines(os.path.join(state["root"], rel))
            for h in hits[rel][:20]:
                print(f"{rel}:{h}\t{lines[h - 1].strip()[:200]}")
        for rel, c in data.items():
            print(f"{rel} — {c} matches in a data file")
        return
    read = len(files) - len(bad)
    if read == 0:
        die(f"none of the {len(files)} files under {scope} could be read: {'; '.join(bad)}")
    aid = f"A{len(state['absent']) + 1}"
    row = {"id": aid, "name": name, "scope": scope, "files": read, "unread": bad}
    state["absent"].append(row)
    save(d, state)
    print(f"{aid} — `{name}`: 0 lines in {scope} ({read} files read" + (f"; UNREAD: {'; '.join(bad)}" if bad else "") + ")")


# ---------------------------------------------------------------- rows


def norm(s):
    return re.sub(r"\s+", " ", s).strip()


def parse_row(line):
    parts = [p.strip() for p in line.split("|", 3)]
    if len(parts) < 4:
        return None, "a row is `ASK | path:LINE | the verbatim line | what it means` (four fields)"
    return {"ask": parts[0], "loc": parts[1], "anchor": parts[2], "note": parts[3]}, None


def stems(state):
    if "_stems" not in state:
        state["_stems"] = {os.path.splitext(os.path.basename(f))[0] for f, _ in state["files"]}
    return state["_stems"]


def verify_row(state, row, cache):
    asks = {a["id"] for a in state["asks"]}
    if row["ask"] not in asks:
        return f"ask `{row['ask']}` is not one of {', '.join(sorted(asks))}"
    if HEDGE_RE.search(row["note"]):
        return f"hedged (`{HEDGE_RE.search(row['note']).group(0)}`): state what the code shows, or write an UNANSWERED row saying what is not settled"
    loc = row["loc"]
    if loc.upper() == "UNANSWERED":
        row["kind"] = "unanswered"
        return None
    if re.fullmatch(r"A\d+", loc):
        if loc not in {a["id"] for a in state["absent"]}:
            return f"{loc} is no absence row; run absent first"
        row["kind"] = "absent"
        return None
    first = loc.split()[0] if loc.split() else ""
    if first in ("def", "sig", "lines", "block", "defs"):
        try:
            blocks = run_verb(state["root"], state["files"], first, shlex.split(loc)[1:])
        except (Miss, ValueError, IndexError) as err:
            return f"extraction `{loc}` failed: {err}"
        missed = [b for h, b in blocks if h == "MISS"]
        if missed:
            return f"extraction `{loc}` failed: {missed[0]}"
        row["kind"], row["blocks"] = "extract", blocks
        return None
    star = re.fullmatch(r"(.+?):\*", loc)
    if star:
        rel, full = resolve_path(state["root"], star.group(1))
        if not os.path.isfile(full):
            same = [f for f, _ in state["files"] if f.endswith("/" + rel.lstrip("./"))]
            if len(same) != 1:
                return f"no file {rel}"
            rel = same[0]
        row["kind"], row["rel"], row["line"] = "file", rel, 0
        return None
    m = re.fullmatch(r"(.+?):(\d+)(?:[-,](\d+))?", loc)
    if not m:
        return f"location `{loc}` is not path:LINE, path:*, A#, UNANSWERED or an extraction (def/sig/lines/block PATH …)"
    rel, full = resolve_path(state["root"], m.group(1))
    if not os.path.isfile(full):
        tail = "/" + rel.lstrip("./")
        same = [f for f, _ in state["files"] if f.endswith(tail)]
        if len(same) == 1:
            rel, full = same[0], os.path.join(state["root"], same[0])
    if rel not in cache:
        cache[rel] = read_lines(full)[0] if os.path.isfile(full) else None
    lines = cache[rel]
    if lines is None:
        return f"no readable file {rel}"
    want = norm(row["anchor"].strip("`"))
    if len(want) < 8:
        return "the anchor is empty or too short to check; copy a piece of the line, a name or a call on it, 10 to 40 characters"
    if "…" in want or "..." in want and "..." not in "".join(lines):
        return "the anchor holds an ellipsis; copy the line whole, or a contiguous piece of it"
    num = int(m.group(2))
    if m.group(3):
        end = int(m.group(3))
        if end < num or end > len(lines):
            return f"range {num}-{end} is not inside {rel} ({len(lines)} lines)"
        if end - num + 1 > RANGE_LINES:
            return f"a range over {RANGE_LINES} lines is an extraction row: `lines {rel} {num} {end}`, anchor -"
        if not any(want in norm(x) for x in lines[num - 1:end]):
            return f"the anchor is not a line of {rel}:{num}-{end}; copy a piece of one of those lines"
        row["rel"], row["line"], row["end"], row["kind"], row["moved"] = rel, num, end, "range", False
        return None
    cands = [i + 1 for i, line in enumerate(lines) if want in norm(line)]
    if not cands:
        near = difflib.get_close_matches(want, [norm(x) for x in lines[max(0, num - 40):num + 40]], n=1, cutoff=0.5)
        return f"the anchor is not a line of {rel}" + (f"; the nearest line near {num} reads: {near[0][:160]}" if near else "")
    at = min(cands, key=lambda c: abs(c - num))
    row["rel"], row["line"], row["moved"] = rel, at, at != num
    row["kind"] = "row"
    enc = enclosing_def(lines, rel, at - 1)
    lo, hi = max(0, at - 41), at + 40
    if enc:
        lo, hi = min(lo, max(0, enc[1] - 40)), max(hi, enc[2] + 41)
    near = "\n".join(lines[lo:hi])
    allowed = " ".join(a["text"] for a in state["asks"]) + " " + (state.get("target") or "")
    low_near = near.lower().replace("_", "")
    plain = re.sub(r"\S+:\d+|[\w./-]+\.[A-Za-z]{1,5}\b", "", row["note"])
    for tok in NOTE_IDENT_RE.findall(plain):
        if tok.lower().replace("_", "") not in low_near and tok not in allowed and not (enc and tok == enc[0]) and tok not in stems(state):
            row.setdefault("suspect", []).append(tok)
    if "withheld" not in row:
        for num_tok in re.findall(r"(?<![\w.:/-])(\d{2,})(?![\w.])", re.sub(r"\S+:\d+|\blines? \d+(?:-\d+)?", "", plain)):
            if num_tok not in near and num_tok not in rel and num_tok.startswith("0"):
                row["withheld"] = f"it states {num_tok}, which {rel} does not show within 40 lines of line {at}"
                break
    bare = re.sub(r"`[^`]*`|\"[^\"]*\"|\bNone\b", "", row["note"])
    if UNIVERSAL_RE.search(bare) and not re.search(r"\bA\d+\b", row["note"]):
        row["unchecked"] = UNIVERSAL_RE.search(bare).group(0)
    return None


def cmd_rows(args):
    if not args:
        die("usage: rows DIR [--replace] <<'EOF' ASK | path:LINE | verbatim line | meaning … EOF")
    d = args[0]
    load(d)
    text = "" if sys.stdin.isatty() else sys.stdin.read()
    mode = "w" if "--replace" in args else "a"
    with open(os.path.join(d, "rows.txt"), mode) as fh:
        fh.write(f"#batch {time.time()}\n" + (text if text.endswith("\n") or not text else text + "\n"))
    render(d, verbose=True)


def cmd_render(args):
    if not args:
        die("usage: render DIR")
    render(args[0], verbose=True)


def render(d, verbose):
    state = load(d)
    path = os.path.join(d, "rows.txt")
    raw = open(path).read().splitlines() if os.path.isfile(path) else []
    root = state["root"]
    cache, good, bad, batch = {}, [], [], 0
    for line in raw:
        if line.startswith("#batch"):
            batch += 1
            continue
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        row, why = parse_row(line)
        if row:
            row["batch"] = batch
            why = verify_row(state, row, cache)
        if why:
            bad.append((line, why, row))
        else:
            good.append(row)
    others = {}
    for r in good:
        if r.get("suspect"):
            files_seen = {g.get("rel") for g in good if g.get("rel") and g.get("rel") != r["rel"]}
            for tok in r["suspect"]:
                key = tok.lower().replace("_", "")
                held_elsewhere = False
                for f in files_seen:
                    if f not in others:
                        others[f] = "\n".join(cache.get(f) or read_lines(os.path.join(root, f))[0] or []).lower().replace("_", "")
                    if key in others[f]:
                        held_elsewhere = True
                        break
                if not held_elsewhere:
                    r["withheld"] = (f"it names `{tok}`, which {r['rel']} does not show within 40 lines of line {r['line']} "
                                     "and no other file this probe anchored holds")
                    break
    last = {}
    for r in good:
        last[(r["ask"], r.get("rel"), r.get("line"), r["loc"] if r["kind"] not in ("row", "file", "range") else "")] = r
    rows = [r for r in good if last.get((r["ask"], r.get("rel"), r.get("line"), r["loc"] if r["kind"] not in ("row", "file", "range") else "")) is r]
    def superseded(r):
        if not r:
            return False
        rel = resolve_path(state["root"], r["loc"].split(":")[0])[0]
        return any(g["ask"] == r["ask"] and g.get("rel", rel) == rel and g["batch"] > r["batch"] for g in rows)
    quota, used, kept = max(RANGE_LINES * 2, (state.get("budget") or 2500) // 60), 0, []
    for r in rows:
        if r["kind"] == "range":
            n = r["end"] - r["line"] + 1
            if used + n > quota:
                bad.append((f"{r['ask']} | {r['loc']} | {r['anchor']} | {r['note']}",
                            f"the return's {quota} quoted range lines are spent; cite the line that matters as a path:LINE row", r))
                continue
            used += n
        kept.append(r)
    rows = kept
    bad = [(ln, why, r) for ln, why, r in bad if not superseded(r)]
    covered = {}
    for r in rows:
        if r["kind"] == "row":
            covered.setdefault(r["rel"], set()).update(range(r["line"] - 3, r["line"] + 4))
        elif r["kind"] == "range":
            covered.setdefault(r["rel"], set()).update(range(r["line"] - 3, r["end"] + 4))
        elif r["kind"] == "file":
            covered.setdefault(r["rel"], set()).update(range(0, 10 ** 6))
        elif r["kind"] == "extract":
            for header, _ in r["blocks"]:
                m = re.match(r"@ (\S+?):(\d+)-(\d+)", header)
                if m:
                    lines = cache.get(m.group(1)) or read_lines(os.path.join(root, m.group(1)))[0] or []
                    a = leading_start(lines, int(m.group(2)) - 1) + 1 if lines else int(m.group(2))
                    covered.setdefault(m.group(1), set()).update(range(a, int(m.group(3)) + 1))
    out, status, listed, census_open, blocks = [], {}, {}, {}, {}
    for a in state["asks"]:
        for dv in a["dirs"]:
            if dv["kind"] == "census":
                for spec in dv["names"]:
                    if spec not in census_open:
                        h = code_only(root, scan(root, state["files"], name_regex(spec), {"CODE"})[0])
                        census_open[spec] = sum(1 for rel in h for n in h[rel] if n not in covered.get(rel, set()))
                        if code_only(root, scan(root, state["files"], name_regex(spec), {"TEST"})[0]):
                            census_open[spec] = census_open[spec] or -1
    for a in state["asks"]:
        mine = [r for r in rows if r["ask"] == a["id"]]
        lines_out, computed, open_refs = [], False, 0
        each = EACH_RE.search(a["text"]) is not None
        backed = any(dv["kind"] == "census" and all(census_open.get(n, 1) == 0 for n in dv["names"]) for dv in a["dirs"])
        prev = None
        for r in mine:
            if r["kind"] in ("row", "range"):
                mark = f" [unchecked `{r['unchecked']}`: no absence row]" if r.get("unchecked") and not backed else ""
                note = f"(note withheld: {r['withheld']})" if r.get("withheld") else r["note"] + mark
                if r["rel"] != prev:
                    lines_out.append(f"{r['rel']}")
                prev = r["rel"]
                if r["kind"] == "row":
                    anchor = norm(cache[r["rel"]][r["line"] - 1])
                    lines_out.append(f"  :{r['line']} `{anchor[:ANCHOR_CHARS]}{'…' if len(anchor) > ANCHOR_CHARS else ''}` — {note}")
                else:
                    lines_out.append(f"  :{r['line']}-{r['end']} — {note}")
                    lines_out += [f"    {k}\t{cache[r['rel']][k - 1]}" for k in range(r["line"], r["end"] + 1)]
                continue
            prev = None
            if r["kind"] == "file":
                lines_out.append(f"- {r['rel']} (every line of it naming the target) — {r['note']}")
            elif r["kind"] == "extract":
                lines_out.append(f"- {r['note']}")
                for header, body in r["blocks"]:
                    lines_out.append("  " + header)
                    lines_out += body
            elif r["kind"] == "absent":
                ab = next(x for x in state["absent"] if x["id"] == r["loc"])
                lines_out.append(f"- ABSENT {ab['id']}: `{ab['name']}` — 0 lines in {ab['scope']} ({ab['files']} files read) — {r['note']}")
        for dv in a["dirs"]:
            if dv["kind"] == "checks":
                continue
            classes = {"census": {"CODE"}, "tests": {"TEST", "DATA"}, "mentions": None}[dv["kind"]]
            for spec in dv["names"]:
                if (dv["kind"], spec) in listed:
                    first = listed[(dv["kind"], spec)]
                    open_refs += max(census_open.get(spec, 0), 0) if dv["kind"] == "census" else 0
                    computed = computed or dv["kind"] != "census"
                    lines_out.append(f"{dv['kind']} `{spec}`: listed under {first}")
                    continue
                listed[(dv["kind"], spec)] = a["id"]
                hits, data, badf = scan(root, state["files"], name_regex(spec), classes)
                n = sum(len(v) for v in hits.values())
                if dv["kind"] == "census":
                    hits = code_only(root, hits)
                    n = sum(len(v) for v in hits.values())
                    loose = [(rel, h) for rel in sorted(hits) for h in hits[rel] if h not in covered.get(rel, set())]
                    open_refs += len(loose)
                    lines_out.append(f"census `{spec}` (code lines; comments and imports left out): {n} lines in {len(hits)} files — {n - len(loose)} beside a row above, "
                                     + (f"{len(loose)} unclassified" if each else f"{len(loose)} listed") + (":" if loose else ""))
                    lines_out += list_hits(root, loose, "UNCLASSIFIED " if each else "")
                    thits = code_only(root, scan(root, state["files"], name_regex(spec), {"TEST"})[0])
                    tested = next((b["id"] for b in state["asks"] for x in b["dirs"] if x["kind"] == "tests" and spec in x["names"]), None)
                    if thits and tested:
                        lines_out.append(f"census `{spec}` in tests: under `tests {spec}` in {tested}")
                    elif thits:
                        lines_out.append(f"census `{spec}` in tests: {sum(len(v) for v in thits.values())} lines in {len(thits)} test files, by test:")
                        lines_out += test_hits(root, thits)
                else:
                    label = "tests and fixtures" if dv["kind"] == "tests" else "every file"
                    computed = True
                    lines_out.append(f"{dv['kind']} `{spec}` ({label}): {n} lines in {len(hits)} files"
                                     + (f", and {len(data)} data or fixture-record files (never opened)" if data else "") + ":")
                    if dv["kind"] == "tests":
                        lines_out += test_hits(root, hits)
                    else:
                        lines_out += list_hits(root, [(rel, h) for rel in sorted(hits) for h in hits[rel]], "")
                    lines_out += [f"{rel} — {c} matches, not opened" for rel, c in sorted(data.items())]
                lines_out += [f"UNREAD {b}" for b in badf]
        unanswered = [r for r in mine if r["kind"] == "unanswered"]
        answered = [r for r in mine if r["kind"] != "unanswered"]
        if answered and re.search(r"\b(prints?|printed|logs?|logged|outputs?|emits?|shows?|displays?|says)\b", a["text"], re.I) and not any(
                r["kind"] in ("row", "range") and any(PRINT_RE.search(cache[r["rel"]][k - 1]) for k in range(r["line"], r.get("end", r["line"]) + 1))
                for r in answered):
            unanswered.append({"kind": "unanswered", "note": "the ask is about what is printed or logged, and no row anchors a line that prints, logs or formats a message"})
        if answered and FULL_RE.search(a["text"]) and not any(r["kind"] in ("extract", "range") for r in answered):
            unanswered.append({"kind": "unanswered", "note": "the ask wants text in full, and no extraction or range row carries it"})
        if not answered and not computed:
            status[a["id"]] = "not answered"
        elif unanswered or (open_refs and each):
            status[a["id"]] = "partial"
        else:
            status[a["id"]] = "with rows"
        blocks[a["id"]] = lines_out
        dirs = ", ".join(dv["kind"] + (" " + ",".join(dv["names"]) if dv["names"] else "") for dv in a["dirs"])
        out.append(f"\n## {a['id']} — {a['text'][:100]}{'…' if len(a['text']) > 100 else ''}" + (f"  [{dirs}]" if dirs else "") + f"  · {status[a['id']].upper()}")
        out += lines_out
        for r in unanswered:
            out.append(f"- NOT SETTLED: {r['note']}")
    na = [a for a in state["asks"] if status[a["id"]] != "with rows"]
    out.append("\n## NOT ANSWERED")
    if not na:
        out.append("- none: every ask has rows, and every census code line sits beside one")
    for a in na:
        why = "; ".join(r["note"] for r in rows if r["ask"] == a["id"] and r["kind"] == "unanswered")
        if status[a["id"]] == "not answered":
            out.append(f"- {a['id']} — no row" + (f": {why}" if why else ""))
        else:
            out.append(f"- {a['id']} — partial" + (f": {why}" if why else ": census lines left unclassified, listed above"))
    if bad:
        out.append(f"\n## REJECTED ROWS — {len(bad)}, not shown above")
        out += [f"- {ln.strip()[:200]}  ⟶ {why}" for ln, why, _ in bad]
    counts = {k: sum(1 for v in status.values() if v == k) for k in ("with rows", "partial", "not answered")}
    head = (f"PROBE {len(state['asks'])} asks — {counts['with rows']} with rows, {counts['partial']} partial, "
            f"{counts['not answered']} not answered · {len(rows)} rows, {len(bad)} rejected · root {root} · file {os.path.join(d, 'return.md')}\n"
            "Rows are `path:LINE `the line, re-read from the file by script` — meaning`; computed lists are complete for the name searched.")
    text = head + "\n" + "\n".join(out) + f"\n\nEND {len(state['asks'])} asks\n"
    with open(os.path.join(d, "return.md"), "w") as fh:
        fh.write(text)
    if verbose and bad:
        print(f"{len(bad)} ROWS REJECTED — fix each once (rows DIR with only the corrected rows), or leave it: it stays listed as rejected.")
        for ln, why, _ in bad:
            print(f"  {ln.strip()[:160]}\n    ⟶ {why}")
    held = [r for r in rows if r.get("withheld")]
    if verbose and held:
        print(f"{len(held)} NOTES WITHHELD — each names something the code near its anchor does not show; resend the row once "
              "with a note the lines show, or leave it withheld:")
        for r in held:
            print(f"  {r['ask']} | {r['rel']}:{r['line']} ⟶ {r['withheld']}")
    moved = [r for r in rows if r.get("moved")]
    if verbose and moved:
        print(f"{len(moved)} rows re-addressed to the line carrying their anchor.")
    ret = os.path.join(d, "return.md")
    size, words, budget = len(text.encode()), len(text.split()), state.get("budget")
    man = [f"PROBE {len(state['asks'])} asks — {counts['with rows']} with rows, {counts['partial']} partial, "
           f"{counts['not answered']} not answered · {len(rows)} rows, {len(bad)} rejected · {words} words"
           + (f" of the {budget} asked" if budget else "") + f" · {size // 1024 + 1} KB",
           f"Read {ret} — the answers under the caller's question numbers: each fact a path:LINE with its line re-read from "
           "the file by script, the computed caller and test lists, and the verbatim text of every quote asked."]
    for a in na:
        why = "; ".join(r["note"] for r in rows if r["ask"] == a["id"] and r["kind"] == "unanswered")
        man.append(f"{status[a['id']].upper()} {a['id']} — {a['text'][:100]}" + (f": {why[:200]}" if why else ""))
    man.append(f"END {len(state['asks'])} asks")
    with open(os.path.join(d, "manifest.txt"), "w") as fh:
        fh.write("\n".join(man) + "\n")
    if verbose and budget and words > budget:
        per = sorted(((len("\n".join(b).split()), i) for i, b in blocks.items()), reverse=True)[:4]
        print(f"OVER BUDGET — {words} words, the brief asks for {budget}. Largest asks: "
              + ", ".join(f"{i} {n} words" for n, i in per) + ". A quote of several lines is one range row "
              "(path:A-B); a row that restates another row's fact goes; a computed list needs a row per line only where the ask is what each line does.")
    if verbose:
        print(f"\nThe return is written: {ret} ({size} bytes). YOUR FINAL MESSAGE, when you are done, is the manifest below, "
              "from PROBE to END, copied whole — the caller reads the file it names.\n")
        print("\n".join(man))
    else:
        print(text, end="")


COMMENT_START = ("//", "#", "*", "/*", "--", "<!--")
IMPORT_RE = re.compile(r"^\s*(import\b|from\s+\S+\s+import\b|export\s+(\*|\{[^}]*\})\s+from\b|(const|let|var)\s+\{?[\w\s,]*\}?\s*=\s*require\(|use\s+[\w:]+|#include\b)")


TRIPLE_OPEN_RE = re.compile(r"""\s*[rRbBuUfF]{0,2}(\"\"\"|''')""")


def prose_lines(rel, lines):
    """Python lines inside a docstring or another string standing alone as a statement: prose, not code."""
    if not rel.endswith((".py", ".pyi")):
        return set()
    out, sc, alone = set(), Scanner(rel), False
    for i, line in enumerate(lines, 1):
        inside = sc.string in ('"""', "'''")
        if inside and alone:
            out.add(i)
        code = sc.feed(line)
        opens = TRIPLE_OPEN_RE.match(line) is not None
        if not inside and sc.string in ('"""', "'''"):
            alone = opens
        if not inside and opens and (alone or code.strip() == "s"):
            out.add(i)
    return out


def code_only(root, hits, imports=False):
    """Census lines: comments, docstrings and (unless imports) import lines left out — a use, not a reference."""
    out = {}
    for rel, nums in hits.items():
        lines = read_lines(os.path.join(root, rel))[0] or []
        prose = prose_lines(rel, lines)
        keep = [h for h in nums if h <= len(lines) and h not in prose and not lines[h - 1].lstrip().startswith(COMMENT_START)
                and (imports or not IMPORT_RE.match(lines[h - 1])) and not re.match(r"^\s*[\w$]+,?\s*$", lines[h - 1])]
        if keep:
            out[rel] = keep
    return out


def def_spans(lines, rel):
    spans = []
    for i, name, _ in all_defs(lines, rel):
        end = block_end(lines, i, rel)
        if end is not None:
            spans.append((i + 1, end + 1, name))
    return spans


def test_hits(root, hits):
    """Each test file's lines naming the target, grouped under the innermost definition holding them."""
    out = []
    for rel in sorted(hits):
        lines = read_lines(os.path.join(root, rel))[0] or []
        spans = def_spans(lines, rel)
        groups = {}
        for h in hits[rel]:
            inner = [sp for sp in spans if sp[0] <= h <= sp[1]]
            key = max(inner, key=lambda sp: sp[0]) if inner else (0, 0, "(top level)")
            groups.setdefault(key, []).append(h)
        parts = []
        for (a, b, name), hs in sorted(groups.items()):
            where = f"{name} {a}-{b}" if a else name
            parts.append(f"{where}: lines {', '.join(map(str, hs))}")
        out.append(f"{rel} — " + " · ".join(parts))
    return out


def list_hits(root, pairs, prefix):
    if len(pairs) > LIST_LINES:
        by = {}
        for rel, h in pairs:
            by.setdefault(rel, []).append(h)
        return [f"{prefix}{rel}: lines {', '.join(map(str, hs))}" for rel, hs in by.items()]
    out, cache = [], {}
    for rel, h in pairs:
        if rel not in cache:
            cache[rel] = read_lines(os.path.join(root, rel))[0] or []
        text = cache[rel][h - 1].strip() if h <= len(cache[rel]) else ""
        out.append(f"{prefix}{rel}:{h}\t{text[:90]}{'…' if len(text) > 90 else ''}")
    return out


def cmd_verbs(args):
    """Print SKILL.md's extraction verbs and collect sections: one source for the syntax."""
    path = os.path.join(os.path.dirname(os.path.realpath(__file__)), "SKILL.md")
    try:
        text = open(path).read()
    except OSError as err:
        die(f"cannot read {path}: {err}")
    m = re.search(r"^## Extraction verbs$.*?(?=^## Probe commands)", text, re.S | re.M)
    if not m:
        die(f"{path} has no § Extraction verbs followed by § Probe commands")
    print(m.group(0).rstrip())


def main():
    cmds = {"verbs": cmd_verbs, "collect": cmd_collect, "init": cmd_init, "refs": cmd_refs, "absent": cmd_absent,
            "rows": cmd_rows, "render": cmd_render}
    if len(sys.argv) < 2 or sys.argv[1] not in cmds:
        die("usage: codeprobe.py {" + "|".join(cmds) + "} … — " + (__doc__ or "").split("\n\n")[2].strip().replace("\n", " ; "))
    cmds[sys.argv[1]](sys.argv[2:])


if __name__ == "__main__":
    main()
