"""segment — cuts a blast area into a SPINE and child segments from the request's seeds, before any code is read;
imported by tracetree.py.

  tracetree.py segment ROOT [--rev SHA] --seed SEED... [--seeds FILE] [--within PATH...] [--variant V]
                       [--run RUN] [--history N] [--floor LINES] [--read-max SHARE] [--max K] [--json]

A SEED is a symbol (`ReadLines`), a qualified symbol (`codexgen.Run`, `Row.RatesAt`), a wire key (`custom-title`) or
a tracked path (`transcript/lines.go`, `pfm/internal/codexgen/`, `tokens/claude.go:461`); `gone=PATH` is a file that
goes away or moves (its mentions join the blast), `use=NAME` a contract the change calls but never edits (a CONTRACT,
blasting none of its callers); `ROW=SEED` (or a `{row}:` line head in --seeds FILE, any name of letters, digits,
`_`, `.` and `-`) tags it with the requirement row it serves. A name under four characters or a common verb (`run`,
`load`) is refused as NOTFOUND, never searched.

  BLAST     each seed's definition site(s) and the tracked files naming it — one hop, never transitive; vendored,
            generated (`.gitattributes` linguist-generated/-vendored) and fixture trees (testdata, fixtures, ...) left out,
            and a path holding a newline; the work tree is read as git stores it (a symlink as its link text)
  CELLS     blast files by directory (a `test`/`tests` directory joins its parent) — the smallest unit with its own
            test home
  SPINE     every seed defined in the blast and named from two or more cells: its definition is the contract the
            parent writes and freezes first; the spine files and their cells' tests that name them stay with the parent
  SEGMENTS  components of the cell graph without the spine (variant contract: a cell naming a component another cell
            changes; cochange: plus cells that changed together in the last N commits; tracer: a RUN's verified links;
            package: one segment per top-level directory, no spine); a segment under --floor estimated lines, or whose
            ORDER costs the parent more than --read-max of its weight in lines read, folds back into the parent

Weight is the lines of the components holding a seed hit (a path seed: its whole file); READ is what the parent reads to
write a child's ORDER: the door lines (each hit, two lines either side) and the contracts' signatures.

Exit 0 with a VERDICT SPLIT or SOLO; exit 1 with VERDICT EMPTY when no seed reaches a file to change, every seed then
on a NOTFOUND line with its reason; exit 2 with `TRACETREE FAILED — {reason}`. Python 3 standard library only; git is read, never written.
"""
import fnmatch
import json
import math
import os
import re
import stat
import subprocess
import sys
import traceback
from collections import defaultdict

import tracemap

EXCLUDE_DIRS = {"vendor", "node_modules", "testdata", "fixtures", "__fixtures__", "__snapshots__", "third_party",
                "generated", ".git"}
EXCLUDE_SUFFIX = (".pb.go", "_gen.go", ".gen.go", ".min.js", ".lock", "go.sum", "package-lock.json", ".jsonl", ".svg",
                  ".png", ".jpg", ".gif", ".pdf", ".ico")
TEST_DIRS = {"test", "tests", "__tests__", "spec"}
TEST_FILE = re.compile(r"(_test\.(go|py|sh)|^test_.*\.py|\.(test|spec)\.[cm]?[jt]sx?|_test\.[cm]?js)$")
PATH_EXT = {"go", "py", "js", "mjs", "cjs", "ts", "tsx", "sh", "bash", "md", "json", "jsonc", "yml", "yaml", "toml",
            "tsv", "txt", "mk", "html", "css", "sql"}
IDENT = re.compile(r"[A-Za-z_$][\w$]*")
KEY = re.compile(r"[A-Za-z_][\w.-]*[\w]")
ROW = re.compile(r"[A-Z][A-Za-z]*-?\d+[a-z]?")
ROW_HEAD = re.compile(r"[A-Za-z0-9_.-]+:")  # a seeds-file line's `{row}:` head: never itself a seed
BROAD_FILES = 60
MIN_EDGE = 2
MIN_NAME = 4
NEW_FILE_LINES = 150  # the estimated weight of a file the request names that does not exist yet
DEF_CAP, REF_CAP, CONTEXT = 200, 120, 2
HISTORY_SWEEP = 40
VARIANTS = ("contract", "package", "cochange", "tracer")
WEAK_NAMES = {"main", "init", "run", "new", "test", "string", "error", "close", "open", "read", "write", "get", "set",
              "len", "add", "load", "save", "start", "stop"}


class SegmentError(Exception):
    """A request segment cannot answer: not a git tree, a revision naming no commit, a git that cannot say."""


# the variables that point git at another repository (a hook exports them): ROOT names the repository, never they
GIT_LOCATION_ENV = ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
                    "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX")


def git_env():
    return {k: v for k, v in os.environ.items() if k not in GIT_LOCATION_ENV}


def read_worktree(top, rel):
    """A tracked file's lines as the work tree holds them, read the way git stores it: a symlink as its link text,
    never followed; None when git would see it absent (deleted, a submodule's directory, or behind a directory the
    work tree turned into a symlink). Anything else that is no regular file (a FIFO, a device) is a SegmentError."""
    full = os.path.join(top, rel)
    try:
        if os.path.realpath(os.path.dirname(full)) != os.path.join(os.path.realpath(top), os.path.dirname(rel)).rstrip("/"):
            return None
        st = os.lstat(full)
        if stat.S_ISLNK(st.st_mode):
            return os.readlink(full).splitlines()
        if stat.S_ISDIR(st.st_mode):
            return None
        if not stat.S_ISREG(st.st_mode):
            raise SegmentError(f"cannot read {rel} in the work tree: not a regular file")
        fd = os.open(full, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
        with os.fdopen(fd, "rb") as f:
            if not stat.S_ISREG(os.fstat(f.fileno()).st_mode):
                raise SegmentError(f"cannot read {rel} in the work tree: not a regular file")
            return f.read().decode("utf-8", "replace").splitlines()
    except (FileNotFoundError, NotADirectoryError):
        return None
    except OSError as e:
        raise SegmentError(f"cannot read {rel} in the work tree: {e.strerror or e}") from e


# ---------------------------------------------------------------- the repository, read-only


class Repo:
    """ROOT's git tree at REV (a commit, read through git objects) or, with no REV, its tracked files as the work tree
    holds them."""

    def __init__(self, root, rev=None):
        root = os.path.abspath(root)
        if not os.path.isdir(root):
            raise SegmentError(f"{root} is not a directory")
        self.root = root
        self.top = self.git("rev-parse", "--show-toplevel", cwd=root).strip()
        if not self.top:
            raise SegmentError(f"{root} is not inside a git work tree")
        self.prefix = self.git("rev-parse", "--show-prefix", cwd=root).strip().rstrip("/")
        self.rev = None
        if rev:
            try:
                self.rev = self.git("rev-parse", "--verify", "--quiet", f"{rev}^{{commit}}").strip()
            except SegmentError:
                self.rev = ""
            if not self.rev:
                raise SegmentError(f"--rev {rev} names no commit in {self.top}")
        self.texts = {}

    def git(self, *args, cwd=None, data=None, ok=(0,)):
        try:
            done = subprocess.run(["git", "-C", cwd or self.top, *args], input=data, capture_output=True, timeout=120,
                                  env=git_env())
        except (OSError, subprocess.SubprocessError) as e:
            raise SegmentError(f"git {args[0]} could not run: {e}") from e
        if done.returncode not in ok:
            why = done.stderr.decode("utf-8", "replace").strip().splitlines()
            raise SegmentError(f"git {' '.join(args[:3])} failed: {why[-1] if why else f'exit {done.returncode}'}")
        return done.stdout.decode("utf-8", "replace")

    def tracked(self):
        if self.rev:
            out = self.git("ls-tree", "-r", "-z", "--full-tree", "--name-only", self.rev)
        else:
            out = self.git("ls-files", "-z")
        return [p for p in out.split("\0") if p]

    def grep(self, patterns, words, paths):
        """(path, line, text) of every tracked line holding one of the fixed patterns, as whole words with `words`."""
        if not patterns:
            return []
        args = ["grep", "-n", "-I", "-z", "--full-name", "--no-color", "-F"]
        if words:
            args.append("-w")
        for p in patterns:
            args += ["-e", p]
        if self.rev:
            args.append(self.rev)
        args += ["--", *(paths or ["."])]
        out = self.git(*args, ok=(0, 1))
        hits, head = [], f"{self.rev}:" if self.rev else ""
        for rec in out.split("\n"):
            parts = rec.split("\0", 2)
            if len(parts) != 3:
                continue
            path = parts[0][len(head):] if head and parts[0].startswith(head) else parts[0]
            try:
                hits.append((path, int(parts[1]), parts[2]))
            except ValueError:
                continue
        return hits

    def load(self, paths):
        """Each path's lines, read in one git call at REV or from the work tree (read_worktree); a path absent there
        reads empty."""
        want = [p for p in dict.fromkeys(paths) if p not in self.texts]
        if not want:
            return
        if not self.rev:
            for p in want:
                self.texts[p] = read_worktree(self.top, p) or []
            return
        try:
            done = subprocess.run(["git", "-C", self.top, "cat-file", "--batch"],
                                  input="".join(f"{self.rev}:{p}\n" for p in want).encode(), capture_output=True,
                                  timeout=120, env=git_env())
        except (OSError, subprocess.SubprocessError) as e:
            raise SegmentError(f"git cat-file could not run: {e}") from e
        if done.returncode:
            raise SegmentError(f"git cat-file failed: {done.stderr.decode('utf-8', 'replace').strip()}")
        buf, at = done.stdout, 0
        for p in want:
            nl = buf.index(b"\n", at)
            head = buf[at:nl].split()
            at = nl + 1
            if len(head) < 3 or head[1] != b"blob":
                self.texts[p] = []
                continue
            size = int(head[2])
            self.texts[p] = buf[at:at + size].decode("utf-8", "replace").splitlines()
            at += size + 1

    def lines(self, path):
        if path not in self.texts:
            self.load([path])
        return self.texts[path]

    def history(self, n):
        out = self.git("-c", "core.quotePath=false", "log", "--no-merges", "--name-only", "--format=%x1e", "-n", str(n), self.rev or "HEAD")
        return [set(l for l in chunk.splitlines() if l.strip()) for chunk in out.split("\x1e") if chunk.strip()]


def generated_patterns(repo):
    if repo.rev:
        text = repo.lines(".gitattributes")
    else:
        text = read_worktree(repo.top, ".gitattributes") or []
    out = []
    for line in text:
        parts = line.split()
        if len(parts) >= 2 and not parts[0].startswith("#") and any(
                a in ("linguist-generated", "linguist-vendored", "linguist-generated=true", "linguist-vendored=true")
                for a in parts[1:]):
            out.append(parts[0].lstrip("/"))
    return out


def excluded(path, gen):
    if "\n" in path:
        return True  # no line-based git output (grep, cat-file --batch) can carry the name
    parts = path.split("/")
    if any(p in EXCLUDE_DIRS for p in parts[:-1]) or path.endswith(EXCLUDE_SUFFIX):
        return True
    for pat in gen:
        if fnmatch.fnmatch(path if "/" in pat else parts[-1], pat) or fnmatch.fnmatch(path, pat.rstrip("/") + "/*"):
            return True
    return False


def cell_of(path):
    d = os.path.dirname(path)
    if os.path.basename(d) in TEST_DIRS:
        d = os.path.dirname(d)
    return d or "."


def top_of(path):
    return path.split("/", 1)[0] if "/" in path else "."


def is_test(path):
    return bool(TEST_FILE.search(os.path.basename(path))) or any(p in TEST_DIRS for p in path.split("/")[:-1])


# ---------------------------------------------------------------- seeds


def parse_seeds(raws, files_text):
    """[(row, text)] from --seed values (`ROW=SEED`) and --seeds files (`{row}: SEED SEED`, `#` comments)."""
    out = []
    for raw in raws:
        m = re.fullmatch(r"(" + ROW.pattern + r")=(.+)", raw.strip())
        out.append((m.group(1), m.group(2)) if m else (None, raw.strip()))
    for text in files_text:
        for line in text.splitlines():
            line = line.split(" #", 1)[0].strip()
            if not line or line.startswith("#"):
                continue
            row = None
            m = re.match(r"(" + ROW.pattern + r")\s*:\s+(.*)", line)
            head = line.split(None, 1)
            if m:
                row, line = m.group(1), m.group(2)
            elif ROW_HEAD.fullmatch(head[0]):
                row, line = head[0][:-1], head[1] if len(head) > 1 else ""
            for tok in line.split():
                out.append((row, tok))
    seen, uniq = set(), []
    for row, s in out:
        s = s.strip().strip("`'\",;")
        if s and (row, s) not in seen:
            seen.add((row, s))
            uniq.append((row, s))
    return uniq


def resolve(row, raw, files, dirs, basenames):
    seed = {"raw": raw, "row": row, "kind": None, "name": None, "pkgs": [], "types": [], "paths": [], "line": None,
            "defs": [], "refs": {}, "broad": False, "why": "", "gone": False}
    s, line = raw, None
    if s.startswith("gone="):
        s = s[5:]
        seed["gone"] = True
    elif s.startswith("use="):
        s = s[4:]
        seed["use"] = True
    m = re.fullmatch(r"(.+?):(\d+)(?:[-,/:]\d+)*", s)
    if m and ("/" in m.group(1) or "." in m.group(1)):
        s, line = m.group(1), int(m.group(2))
    slash = "/" in s  # `dir/` is a path even once its slash is trimmed
    s = s.rstrip("/")
    ext = s.rsplit(".", 1)[-1] if "." in os.path.basename(s) else ""
    if slash or ext in PATH_EXT:
        hits = resolve_path(s, files, dirs, basenames)
        seed.update(kind="path", name=s, line=line, paths=hits)
        if not hits:
            seed["why"] = "no tracked file or directory has this path"
        return seed
    parts = s.split(".")
    if all(IDENT.fullmatch(p) for p in parts):
        quals = parts[:-1]
        seed.update(kind="symbol", name=parts[-1], pkgs=[q for q in quals if q in dirs["names"]],
                    types=[q for q in quals if q not in dirs["names"]])
        return seed
    if KEY.fullmatch(s):
        seed.update(kind="key", name=s)
        return seed
    seed.update(kind="bad", name=s, why="not a symbol, a key or a path")
    return seed


def place_new(seeds, hits, dirs, inside, files):
    """A path seed with a file extension that matches nothing is a file the request will create: its directory is
    the existing one it names (or ends with), else a new directory beside the cells its sibling seeds reach."""
    parents = defaultdict(set)
    for p in hits:
        d = cell_of(p)
        parents[os.path.dirname(d)].add(d)
    parents = {x: len(c) for x, c in parents.items()}
    exts = defaultdict(set)
    for f in files:
        exts[os.path.dirname(f)].add(os.path.splitext(f)[1])
    new = {}
    for i, s in enumerate(seeds):
        if s["kind"] != "path" or s["paths"] or s["gone"] or s.get("use") or "." not in os.path.basename(s["name"]) or "/" not in s["name"]:
            continue
        d, base = os.path.split(s["name"])
        if d in dirs["all"]:
            full = s["name"]
        else:
            ext = os.path.splitext(base)[1]
            under = sorted(x for x in dirs["all"] if x.endswith("/" + d) and ext in exts.get(x, set()))
            if under:
                full = f"{max(under, key=lambda x: (parents.get(os.path.dirname(x), 0), -len(x)))}/{base}"
            else:
                top = d.split("/", 1)[0]
                homes = sorted((n, x) for x, n in parents.items() if x and not (x + "/" + top) in dirs["all"])
                if not homes:
                    continue
                full = f"{homes[-1][1]}/{s['name']}"
        if not inside(full):
            continue
        s["why"], s["new"], s["paths"] = None, True, [full]
        new[full] = i
    return new


def resolve_path(s, files, dirs, basenames):
    if s in files:
        return [s]
    if s in dirs["all"]:
        return sorted(f for f in files if f.startswith(s + "/"))
    tail = "/" + s
    hits = sorted(f for f in files if f.endswith(tail))
    if not hits and "/" not in s:
        hits = sorted(basenames.get(s, []))
    if hits:
        return hits
    under = sorted(d for d in dirs["all"] if d.endswith(tail))
    return sorted(f for f in files if any(f.startswith(d + "/") for d in under))


def path_tokens(seed, basenames, all_files):
    """The text that names a path seed elsewhere: a unique basename, else its last two (three) components."""
    toks = set()
    for p in seed["paths"][:50]:
        base = os.path.basename(p)
        parts = p.split("/")
        if seed["paths"] and len(seed["paths"]) > 1 and not seed["name"].endswith(base):
            # a directory seed: name the directory, never each file in it
            continue
        if len(basenames.get(base, ())) == 1 and len(base) >= 6:
            toks.add(base)
        else:
            toks.add("/".join(parts[-3:]) if len(parts) >= 3 and len(basenames.get(base, ())) > 3 else "/".join(parts[-2:]))
    if len(seed["paths"]) > 1 and not any(seed["name"].endswith(os.path.basename(p)) for p in seed["paths"][:1]):
        d = seed["name"].strip("/")
        if "/" not in d:
            d = os.path.dirname(seed["paths"][0])
        toks.add("/".join(d.split("/")[-2:]))
    return sorted(toks)


def def_patterns(name, lg, types):
    n = re.escape(name)
    recv = r"\([^)]*\b(?:" + "|".join(map(re.escape, types)) + r")\)" if types else r"\([^)]*\)"
    if lg == tracemap.GO:
        pats = [r"^func\s+" + recv + r"\s*" + n + r"\s*[\[(]"]
        if not types:
            pats += [r"^func\s+" + n + r"\s*[\[(]", r"^type\s+" + n + r"\b", r"^(?:const|var)\s+" + n + r"\b",
                     r"^\t" + n + r"(?:\s+[\w.*\[\]]+)?\s*=[^=]"]
        return [re.compile(p) for p in pats]
    if lg == tracemap.PY:
        return [re.compile(p) for p in (r"^\s*(?:async\s+)?def\s+" + n + r"\s*\(", r"^\s*class\s+" + n + r"\b",
                                         r"^" + n + r"\s*(?::[^=]*)?=[^=]")]
    if lg == tracemap.JS:
        return [re.compile(p) for p in (r"\bfunction\s*\*?\s*" + n + r"\s*\(",
                                         r"^\s*(?:export\s+)?(?:const|let|var)\s+" + n + r"\s*=",
                                         r"^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+" + n + r"\b",
                                         r"^\s*(?:export\s+)?(?:type|interface|enum)\s+" + n + r"\b",
                                         r"^\s+(?:static\s+)?(?:async\s+)?" + n + r"\s*\([^)]*\)\s*\{")]
    if lg == tracemap.SH:
        return [re.compile(p) for p in (r"^\s*(?:function\s+)?" + n + r"\s*\(\)", r"^\s*function\s+" + n + r"\b",
                                         r"^" + n + r"=")]
    if lg == tracemap.MAKE:
        return [re.compile(r"^" + n + r"\s*::?(?!=)")]
    return []


# ---------------------------------------------------------------- the cut


def word(name):
    return re.compile(r"(?<![\w$-])" + re.escape(name) + r"(?![\w$-])" if "-" in name or "." in name else
                      r"(?<![\w$])" + re.escape(name) + r"(?![\w$])")


def blast(repo, seeds, keep, files, basenames):
    """Fills each seed's defs and refs; returns {path: [(line, seed index, is_def)]}."""
    hits = defaultdict(list)
    sym = []
    for i, s in enumerate(seeds):
        if s["kind"] not in ("symbol", "key"):
            continue
        if len(s["name"]) < MIN_NAME or s["name"].lower() in WEAK_NAMES:
            s["why"] = (f"too short or common to search as a word ({s['name']!r}); name the symbol it means, "
                        "or its file by path")
            continue
        sym.append(i)
    scope = sorted(keep["within"]) or None
    if sym:
        rx = {i: word(seeds[i]["name"]) for i in sym}
        dot = {i: re.compile(r"\." + re.escape(seeds[i]["name"]) + r"(?![\w$])") for i in sym}
        found = repo.grep(sorted({seeds[i]["name"] for i in sym}), True, scope)
        defcache = {}
        lines_by = defaultdict(list)
        for path, ln, text in found:
            if keep["ok"](path):
                lines_by[path].append((ln, text))
        for path, rows in lines_by.items():
            lg = tracemap.lang(path)
            dirbase = os.path.basename(os.path.dirname(path))
            for ln, text in rows:
                for i in sym:
                    s = seeds[i]
                    if not rx[i].search(text):
                        continue
                    key = (i, lg)
                    if key not in defcache:
                        defcache[key] = def_patterns(s["name"], lg, s["types"])
                    is_def = (not s["pkgs"] or dirbase in s["pkgs"]) and any(p.search(text) for p in defcache[key])
                    if is_def:
                        s["defs"].append((path, ln, text.strip()))
                        hits[path].append((ln, i, True))
                    else:
                        s["refs"].setdefault(path, []).append((ln, text))
        for i in sym:
            s = seeds[i]
            if s["pkgs"] or s["types"]:
                home = {os.path.dirname(p) for p, _, _ in s["defs"]}
                for path in list(s["refs"]):
                    rows = [r for r in s["refs"][path] if os.path.dirname(path) in home or dot[i].search(r[1])]
                    if rows:
                        s["refs"][path] = rows
                    else:
                        del s["refs"][path]
            if len(s["refs"]) > BROAD_FILES:
                home = {cell_of(p) for p, _, _ in s["defs"]}
                s["broad"] = len(s["refs"])
                s["refs"] = {p: r for p, r in s["refs"].items() if cell_of(p) in home}
            if s["kind"] == "symbol" and not s["defs"]:
                s["kind"] = "key"
            for path, rows in s["refs"].items():
                hits[path] += [(ln, i, False) for ln, _ in rows]
            if not s["defs"] and not s["refs"] and s["broad"]:
                s["why"] = (f"BROAD: {s['broad']} files name it and none defines it, so no cell of its own is kept; "
                            "name it qualified or by path")
            elif not s["defs"] and not s["refs"]:
                s["why"] = "no tracked line outside vendored, generated and fixture trees defines or names it" + (" (qualified: " + ".".join(s["pkgs"] + s["types"]) + ")" if s["pkgs"] or s["types"] else "")
    pth = [i for i, s in enumerate(seeds) if s["kind"] == "path" and s["paths"]]
    for i in pth:
        s = seeds[i]
        for p in s["paths"]:
            if keep["ok"](p):
                s["defs"].append((p, s["line"] or 0, ""))
                hits[p].append((s["line"] or 0, i, True))
        if not s["defs"]:
            s["why"] = "every file it names is out of scope (excluded or outside --within)"
    # an edited file's mentions stay as they are; a file that goes away or moves (gone=PATH) takes them with it
    toks = {i: path_tokens(seeds[i], basenames, files) for i in pth if seeds[i]["gone"]}
    allt = sorted({t for ts in toks.values() for t in ts})
    if allt:
        found = repo.grep(allt, False, scope)
        bounded = {t: re.compile(r"(?<![\w-])" + re.escape(t) + r"(?![\w-])") for t in allt}
        for path, ln, text in found:
            if not keep["ok"](path):
                continue
            for i in toks:
                if path in seeds[i]["paths"]:
                    continue
                if any(bounded[t].search(text) for t in toks[i]):
                    seeds[i]["refs"].setdefault(path, []).append((ln, text))
                    hits[path].append((ln, i, False))
    return hits


def ranges_of(repo, path, hs, seeds):
    """The line ranges a file's hits touch (component extents), and its door ranges (each hit ± CONTEXT)."""
    lines = repo.lines(path)
    lg = tracemap.lang(path)
    fenced = tracemap.fences(lines) if lg == tracemap.MD else set()
    touched, doors, comps = [], [], {}
    for ln, i, is_def in hs:
        if seeds[i]["kind"] == "path" and is_def:
            touched.append((1, max(1, len(lines)) if lines or not seeds[i].get("new") else NEW_FILE_LINES))
            continue
        if ln < 1 or ln > len(lines):
            continue
        name, start, kind = tracemap.enclosing(lines, ln - 1, lg, fenced) if lg else (None, None, "file")
        if start is None:
            touched.append((max(1, ln - CONTEXT), min(len(lines), ln + CONTEXT)))
            doors.append(touched[-1])
            continue
        span = len(tracemap.extent(lines, start, lg, fenced))
        cap = DEF_CAP if is_def else REF_CAP
        end = start + span
        touched.append((start + 1, start + min(span, cap)))
        door = (max(start + 1, ln - CONTEXT), min(max(end, ln), ln + CONTEXT))
        if door[1] > touched[-1][1]:
            touched.append(door)
        # the parent reads the door line and its neighbours inside the component, never past it
        doors.append(door)
        if kind in ("func", "method", "type", "class", "target") and name:
            comps.setdefault(name, (start + 1, kind))
    return merge(touched), merge(doors), comps


def merge(rs):
    """(a, b) line ranges, overlapping or adjacent ones joined, in order."""
    out = []
    for a, b in sorted(rs):
        if out and a <= out[-1][1] + 1:
            out[-1][1] = max(out[-1][1], b)
        else:
            out.append([a, b])
    return [tuple(r) for r in out]


def span(rs):
    return sum(b - a + 1 for a, b in rs)


def signature(repo, path, ln):
    lines = repo.lines(path)
    if not (1 <= ln <= len(lines)):
        return "", 1
    lg = tracemap.lang(path)
    head = lines[ln - 1]
    if lg == tracemap.GO and head.startswith("type ") and "{" in head and "}" not in head:
        body = tracemap.extent(lines, ln - 1, lg, set())[:30]
        return " ".join(l.strip() for l in body)[:200], len(body)
    out, k = [], ln - 1
    while k < len(lines) and len(out) < 4:
        out.append(lines[k].strip())
        if "{" in lines[k] or ":" in lines[k].rstrip()[-1:] or lg not in (tracemap.GO, tracemap.JS):
            break
        k += 1
    return " ".join(out)[:200], len(out)


def segment(repo, raws, seed_files=(), within=(), variant="contract", run=None, history=300, floor=150,
            read_max=0.5, max_k=4, spine_off=False):
    if variant not in VARIANTS:
        raise SegmentError(f"--variant {variant}: one of {', '.join(VARIANTS)}")
    pairs = parse_seeds(raws, seed_files)
    if not pairs:
        raise SegmentError("no seed: give --seed SYMBOL|PATH (or --seeds FILE)")
    gen = generated_patterns(repo)
    scope = [w.strip("/") for w in within if w.strip("/")]
    if repo.prefix:
        scope = [f"{repo.prefix}/{w}" for w in scope] or [repo.prefix]
    tracked = repo.tracked()
    inside = (lambda p: any(p == w or p.startswith(w + "/") for w in scope)) if scope else (lambda p: True)
    files = [p for p in tracked if not excluded(p, gen)]
    fileset = set(files)
    keep = {"within": scope, "ok": lambda p: p in fileset and inside(p)}
    dirs = {"all": set(), "names": set()}
    basenames = defaultdict(list)
    for p in files:
        basenames[os.path.basename(p)].append(p)
        d = os.path.dirname(p)
        while d and d not in dirs["all"]:
            dirs["all"].add(d)
            dirs["names"].add(os.path.basename(d))
            d = os.path.dirname(d)
    seeds = [resolve(row, raw, fileset, dirs, basenames) for row, raw in pairs]
    hits = blast(repo, seeds, keep, files, basenames)
    hits = {p: sorted(set(h)) for p, h in hits.items() if keep["ok"](p)}
    use = {i for i, s in enumerate(seeds) if s.get("use")}
    if use:
        hits = {p: [h for h in hs if h[1] not in use] for p, hs in hits.items()}
        hits = {p: hs for p, hs in hits.items() if hs}
    new_files = place_new(seeds, hits, dirs, inside, files)
    for p, i in new_files.items():
        repo.texts[p] = []
        hits.setdefault(p, []).append((1, i, True))
    res = {"root": repo.top, "rev": repo.rev or "worktree", "variant": variant, "within": scope, "seeds": seeds,
           "blast": sorted(hits), "floor": floor, "read_max": read_max, "max": max_k}
    if not hits:
        for i, s in enumerate(seeds):
            if not s["why"]:
                s["why"] = ("it names a contract the segments call (use=); it changes no file of its own" if i in use
                            else "it reaches no file to change")
        res.update(verdict="EMPTY", reason=f"no seed reaches a file to change ({len(seeds)} seeds, each NOTFOUND above)",
                   cells={}, spine=[], parent={}, segments=[], files={})
        return res
    repo.load(sorted(hits))
    info = {}
    for p, hs in hits.items():
        touched, doors, comps = ranges_of(repo, p, hs, seeds)
        info[p] = {"cell": cell_of(p) if variant != "package" else top_of(p), "touched": touched, "doors": doors,
                   "comps": comps, "lines": len(repo.lines(p)), "seeds": sorted({i for _, i, _ in hs}), "hits": hs}
    # the spine: a defined seed named from two or more cells
    spine, spine_files = [], set()
    for i in sorted(use):
        s = seeds[i]
        for p, ln, text in s["defs"][:1]:
            if p not in repo.texts:
                repo.load([p])
            sig, n = signature(repo, p, ln)
            spine.append({"seed": i, "name": s["name"], "path": p, "line": ln, "signature": sig, "lines": n,
                          "cells": [], "use": True})
    for i, s in enumerate(seeds):
        if i in use:
            continue
        if s["kind"] not in ("symbol", "key") or variant == "package" or spine_off:
            continue
        if s["defs"]:
            for p, ln, text in s["defs"]:
                if p not in info:
                    continue
                cells = users(repo, s, p, text, info)
                if len(cells) < 2:
                    continue
                sig, n = signature(repo, p, ln)
                spine.append({"seed": i, "name": s["name"], "path": p, "line": ln, "signature": sig, "lines": n,
                              "cells": sorted(cells)})
                spine_files.add(p)
        else:
            cells = {info[p]["cell"] for p in s["refs"] if p in info and tracemap.lang(p) not in (tracemap.MD, None)}
            if len(cells) < 2:
                continue
            spine.append({"seed": i, "name": s["name"], "path": None, "line": 0, "signature": f"key {s['name']}",
                          "lines": 1, "cells": sorted(cells)})
    # a NEW contract: a symbol the request names that does not exist yet, in a row whose other seeds reach two or
    # more cells — every one of those cells will call it, so the parent writes it first
    if variant != "package" and not spine_off:
        row_cells = defaultdict(set)
        for p, x in info.items():
            for i in x["seeds"]:
                if seeds[i]["row"]:
                    row_cells[seeds[i]["row"]].add(x["cell"])
        for i, s in enumerate(seeds):
            if s["kind"] not in ("symbol", "key") or s["defs"] or s["refs"] or not s["row"] or len(s["name"]) < MIN_NAME \
                    or not IDENT.fullmatch(s["name"]):
                continue
            cells = row_cells.get(s["row"], set())
            if len(cells) < 2:
                continue
            home = next((t["raw"] for t in seeds if t["row"] == s["row"] and t["kind"] == "path"), None)
            spine.append({"seed": i, "name": s["name"], "path": None, "line": 0, "new": True,
                          "signature": f"NEW {s['name']} — the request names it; it does not exist yet"
                                       + (f" (home {home})" if home else ""), "lines": 1, "cells": sorted(cells)})
            s["why"] = None
            s["new"] = True
    spine_seeds = {x["seed"] for x in spine}
    spine_cells = {info[p]["cell"] for p in spine_files}
    parent_files = set(spine_files)
    for p in info:
        if p not in parent_files and is_test(p) and info[p]["cell"] in spine_cells and set(info[p]["seeds"]) <= spine_seeds:
            parent_files.add(p)
    cells = defaultdict(list)
    for p in info:
        if p not in parent_files:
            cells[info[p]["cell"]].append(p)
    edges = defaultdict(int)
    if variant in ("contract", "cochange"):
        edges = defaultdict(int, component_edges(repo, info, cells, seeds, spine_seeds))
    if variant == "cochange":
        for k, v in cochange_edges(repo, cells, history).items():
            edges[k] += v
    if variant == "tracer":
        edges = tracer_edges(repo, run, info, cells, spine)
    comps = components(sorted(cells), edges)
    segs = []
    for group in comps:
        fs = sorted(f for c in group for f in cells[c])
        segs.append(describe(repo, group, fs, info, seeds, spine, spine_seeds, files))
    if variant != "package":
        segs = pack(lambda group: describe(repo, group, sorted(f for c in group for f in cells[c]), info, seeds, spine,
                                           spine_seeds, files), segs, floor, max_k, info)
    segs.sort(key=lambda g: -g["weight"])
    kept, folded = [], []
    for g in segs:
        if g["weight"] < floor:
            g["folded"] = f"weight {g['weight']} under the floor {floor}"
        elif g["weight"] and g["read"] / g["weight"] > read_max:
            g["folded"] = f"its ORDER reads {g['read']} of its {g['weight']} lines ({pct(g['read'], g['weight'])}), over {pct(read_max, 1)}"
        (folded if g.get("folded") else kept).append(g)
    for n, g in enumerate(kept, 1):
        g["id"] = f"S{n}"
    pfiles = sorted(parent_files | {f for g in folded for f in g["files"]})
    parent = {"files": pfiles, "cells": sorted({info[p]["cell"] for p in pfiles}),
              "weight": sum(span(info[p]["touched"]) for p in pfiles),
              "spine": len(spine), "folded": [{"cells": g["cells"], "weight": g["weight"], "read": g["read"],
                                                "why": g["folded"]} for g in folded]}
    for x in spine:
        x["used_by"] = sorted({g["id"] for g in kept if x["seed"] in g["seed_ids"] or x["name"] in g["contracts"]} |
                              ({"PARENT"} if any(x["seed"] in info[p]["seeds"] for p in pfiles if p != x["path"]) else set()))
    if len(kept) >= 2:
        verdict, reason = "SPLIT", f"{len(kept)} segments share only the spine ({len(spine)} contracts)"
    elif len(kept) == 1:
        verdict, reason = "SOLO", (f"one segment ({', '.join(kept[0]['cells'])}) beside the parent's — no parallel work; "
                                   "a spawn re-reads what the parent holds")
    else:
        verdict, reason = "SOLO", "every segment folds into the parent (" + "; ".join(sorted({g["folded"].split(" (")[0].split(",")[0] for g in folded})) + ")" if folded else "the blast is the spine alone"
    res.update(verdict=verdict, reason=reason, k=len(kept) if verdict == "SPLIT" else 0, spine=spine, parent=parent,
               segments=kept, cells={c: sorted(fs) for c, fs in cells.items()}, files=info, edges=dict(edges))
    return res


def users(repo, s, path, text, info):
    """The code cells that use the definition of seed s at path: its own cell, and every other cell whose code reaches
    it — in Go only by `pkg.Name` (a method by `.Name`), never an unexported name; prose mentions are no use."""
    own = info[path]["cell"]
    out = {own}
    lg = tracemap.lang(path)
    if lg == tracemap.GO:
        method = text.lstrip().startswith("func (")
        if not s["name"][:1].isupper():
            return out
        pkg = go_package(repo, [path]) or os.path.basename(os.path.dirname(path))
        rx = re.compile((r"\." if method else r"\b" + re.escape(pkg) + r"\.") + re.escape(s["name"]) + r"\b")
        for p, rows in s["refs"].items():
            if p in info and p.endswith(".go") and info[p]["cell"] != own and any(rx.search(t) for _, t in rows):
                out.add(info[p]["cell"])
        return out
    for p in s["refs"]:
        if p in info and tracemap.lang(p) not in (tracemap.MD, None):
            out.add(info[p]["cell"])
    return out


def pct(a, b):
    return f"{round(100 * a / b) if b else 0}%"


def go_package(repo, files):
    for p in files:
        if p.endswith(".go"):
            for line in repo.lines(p)[:40]:
                m = re.match(r"package\s+(\w+)", line)
                if m:
                    return m.group(1)
    return None


def component_edges(repo, info, cells, seeds, spine_seeds):
    """cell A — cell B when a blast file of A names a component a seed hit sits in, in B (a Go one by `pkg.Name`)."""
    edges = defaultdict(int)
    names = []
    for c, fs in cells.items():
        pkg = go_package(repo, fs)
        for p in fs:
            for name, (ln, kind) in info[p]["comps"].items():
                if len(name) < 4 or name.lower() in WEAK_NAMES or any(seeds[i]["name"] == name for i in spine_seeds):
                    continue
                if p.endswith(".go"):
                    if pkg and name[:1].isupper() and not pkg.endswith("_test"):
                        names.append((c, re.compile(r"\b" + re.escape(pkg) + r"\." + re.escape(name) + r"\b"), None, name))
                elif tracemap.lang(p) in (tracemap.PY, tracemap.JS, tracemap.SH):
                    stem = os.path.splitext(os.path.basename(p))[0]
                    names.append((c, word(name), stem, name))
    for c, fs in cells.items():
        for p in fs:
            text = "\n".join(repo.lines(p))
            for owner, rx, stem, name in names:
                if owner == c or name not in text or (stem and stem not in text):
                    continue
                if rx.search(text):
                    edges[tuple(sorted((c, owner)))] += 1
    return {k: v for k, v in edges.items() if v >= MIN_EDGE}


def cochange_edges(repo, cells, n):
    edges = defaultdict(int)
    if not cells:
        return edges
    commits = repo.history(n)
    known = set(cells)
    co, alone = defaultdict(int), defaultdict(int)
    for paths in commits:
        if len(paths) > HISTORY_SWEEP:
            continue
        touched = {cell_of(p) for p in paths} & known
        for c in touched:
            alone[c] += 1
        t = sorted(touched)
        for a in range(len(t)):
            for b in range(a + 1, len(t)):
                co[(t[a], t[b])] += 1
    for (a, b), k in co.items():
        if k >= 2 and k / max(1, min(alone[a], alone[b])) >= 0.5:
            edges[(a, b)] += k
    return edges


def tracer_edges(repo, run, info, cells, spine):
    if not run:
        raise SegmentError("--variant tracer needs --run RUN (a tracetree run directory)")
    import tracetree
    edges = defaultdict(int)
    for one in run.split(","):
        for k, v in tracer_run_edges(repo, tracetree, one, info, cells, spine).items():
            edges[k] += v
    return edges


def tracer_run_edges(repo, tracetree, run, info, cells, spine):
    """The cell pairs a run's verified hops link; its files, relative to the run's ROOT, re-based onto the repository
    top. A run outside the repository, or one whose components sit in no file of the blast, is a SegmentError."""
    try:
        loaded = tracetree.load(run)
    except SystemExit as e:
        raise SegmentError(f"--run {run}: the kit could not load it (exit {e.code})") from e
    run_, root, names, files, nodes = loaded[:5]
    base = os.path.relpath(os.path.realpath(root), os.path.realpath(repo.top))
    if base == ".." or base.startswith("../"):
        raise SegmentError(f"--run {run}: its ROOT {root} is outside {repo.top}")
    g = tracemap.data(tracemap.build(run_, root, names, nodes, files), True)
    where = {}
    for c in g["components"]:
        f = c["file"].split(":", 1)[1] if re.match(r"[0-9a-f]{12}:", c["file"]) else c["file"]
        f = f if base == "." else os.path.normpath(os.path.join(base, f))
        if f in info:
            where[c["id"]] = info[f]["cell"]
    if not where:
        raise SegmentError(f"--run {run}: none of its {len(g['components'])} components sits in a file of the blast "
                           "— it links no file of the blast")
    spine_names = {x["name"] for x in spine}
    names_of = {c["id"]: c["name"] for c in g["components"]}
    edges = defaultdict(int)
    for e in g["edges"]:
        if e["kind"] in ("call", "names", "split") and e["from"] in where and e["to"] in where:
            a, b = where[e["from"]], where[e["to"]]
            if a != b and a in cells and b in cells and names_of.get(e["to"]) not in spine_names:
                edges[tuple(sorted((a, b)))] += 1
    return edges


CODE_LANGS = {"go", "py", "js", "mjs", "cjs", "ts", "tsx", "jsx"}


def prose(g):
    return not any(os.path.splitext(f)[1].lstrip(".") in CODE_LANGS for f in g["files"])


def affinity(a, b, use, rowuse):
    """How much two segments belong together, from the request alone: a seed both name (rarer counts more), a
    request row both serve, both prose/config, or one cell nested in the other."""
    score = sum(1.0 / max(1, use[i] - 1) for i in set(a["seed_ids"]) & set(b["seed_ids"]))
    score += 0.5 * sum(1.0 / max(1, rowuse[r] - 1) for r in set(a["rows"]) & set(b["rows"]))
    score /= math.sqrt(max(1, len(a["seed_ids"]) + len(a["rows"])) * max(1, len(b["seed_ids"]) + len(b["rows"])))
    if prose(a) and prose(b):
        score += 0.25
    if any(x.startswith(y + "/") or y.startswith(x + "/") for x in a["cells"] for y in b["cells"]):
        score += 0.5
    return score


def pack(regroup, segs, floor, max_k, info):
    """Merge components until every segment reaches the floor and at most max_k remain, always the lightest segment
    into its closest partner (so segments stay balanced): a light one under the floor that shares nothing stays alone
    (it folds into the parent); over max_k, a segment that shares nothing joins the lightest other."""
    alone = set()
    while len(segs) > 1:
        use = defaultdict(int)
        rowuse = defaultdict(int)
        for g in segs:
            for i in g["seed_ids"]:
                use[i] += 1
            for r in g["rows"]:
                rowuse[r] += 1
        over = bool(max_k) and len(segs) > max_k
        light = [g for g in segs if (g["weight"] < floor and id(g) not in alone) or over]
        if not light:
            break
        a = min(light, key=lambda g: g["weight"])
        b = max((g for g in segs if g is not a), key=lambda g: (affinity(a, g, use, rowuse), -g["weight"]))
        if affinity(a, b, use, rowuse) <= 0 and not over:
            alone.add(id(a))
            continue
        merged = regroup(sorted(a["cells"] + b["cells"]))
        segs = [g for g in segs if g is not a and g is not b] + [merged]
    return segs


def components(nodes, edges):
    up = {n: n for n in nodes}

    def find(x):
        while up[x] != x:
            up[x] = up[up[x]]
            x = up[x]
        return x
    for (a, b) in edges:
        if a in up and b in up:
            up[find(a)] = find(b)
    groups = defaultdict(list)
    for n in nodes:
        groups[find(n)].append(n)
    return [sorted(g) for g in groups.values()]


def describe(repo, group, fs, info, seeds, spine, spine_seeds, all_files):
    seed_ids = sorted({i for p in fs for i in info[p]["seeds"]})
    weight = sum(span(info[p]["touched"]) for p in fs)
    door = sum(span(info[p]["doors"]) for p in fs)
    rows = {seeds[i]["row"] for i in seed_ids if seeds[i]["row"]}
    contracts = [x for x in spine if x["seed"] in seed_ids or (x.get("new") and set(x["cells"]) & set(group))
                 or (x.get("use") and (seeds[x["seed"]]["row"] in rows or set(fs) & set(seeds[x["seed"]]["refs"])))]
    read = door + sum(x["lines"] for x in contracts)
    total = sum(info[p]["lines"] for p in fs)
    sites = defaultdict(list)
    for p in fs:
        lines = repo.lines(p)
        lg = tracemap.lang(p)
        for ln, i, is_def in info[p]["hits"]:
            comp = ""
            if lg and 1 <= ln <= len(lines) and not (seeds[i]["kind"] == "path" and is_def):
                name, start, kind = tracemap.enclosing(lines, ln - 1, lg, set())
                comp = name or ""
            sites[i].append((p, ln, comp, is_def))
    tests = test_homes(group, all_files)
    return {"cells": group, "files": fs, "seed_ids": seed_ids, "seeds": [seeds[i]["raw"] for i in seed_ids],
            "rows": sorted({seeds[i]["row"] for i in seed_ids if seeds[i]["row"]}), "weight": weight, "read": read,
            "total": total, "contracts": [x["name"] for x in contracts], "sites": {i: v for i, v in sites.items()},
            "tests": tests}


_TESTS = {}


def test_homes(group, all_files):
    key = (id(all_files), len(all_files))
    if key not in _TESTS:
        _TESTS.clear()
        by = defaultdict(list)
        for f in all_files:
            if is_test(f):
                by[cell_of(f)].append(f)
        _TESTS[key] = by
    out = []
    for c in group:
        ts = _TESTS[key].get(c, [])
        if not ts:
            continue
        exts = sorted({os.path.splitext(f)[1] for f in ts})
        out.append(f"{c}/*_test{exts[0]} ({len(ts)})" if len(exts) == 1 and all(f.endswith("_test" + exts[0]) for f in ts)
                   else f"{c}: {', '.join(os.path.basename(f) for f in ts[:3])}{' …' if len(ts) > 3 else ''}")
    return out or ["none"]


# ---------------------------------------------------------------- output


def order_line(g, seeds):
    parts = []
    for i in g["seed_ids"]:
        s = seeds[i]
        if s["kind"] == "path":
            own = [x for x in g["sites"][i] if x[3]]
            names = [x for x in g["sites"][i] if not x[3]]
            bits = []
            if own:
                bits.append(("create " if s.get("new") else "port/change ") + ", ".join(sorted({p for p, _, _, _ in own}))[:160])
            if names:
                bits.append(f"follow {len(names)} mention(s) in " + ", ".join(sorted({p for p, _, _, _ in names}))[:160])
            parts.append(f"{s['raw']}: " + "; ".join(bits))
            continue
        refs = [x for x in g["sites"][i] if not x[3]]
        defs = [x for x in g["sites"][i] if x[3]]
        where = sorted({f"{p}:{ln}{' ' + c if c else ''}" for p, ln, c, _ in refs})
        txt = f"{s['raw']}: switch {len(where)} site(s) " + ", ".join(where[:6]) + (f" +{len(where) - 6}" if len(where) > 6 else "")
        if defs:
            txt += " · owns its definition " + ", ".join(sorted({f"{p}:{ln}" for p, ln, _, _ in defs}))
        parts.append(txt)
    out = " | ".join(parts)
    if g["contracts"]:
        out += " · contracts " + ", ".join(sorted(set(g["contracts"])))
    if g["rows"]:
        out += " · rows " + ", ".join(g["rows"])
    return out


def escalate_line(g):
    bits = []
    if g["contracts"]:
        bits.append("a change to a spine contract (" + ", ".join(sorted(set(g["contracts"]))) + ")")
    bits.append("a site of its seeds outside " + ", ".join(g["cells"]))
    bits.append("a shape the ORDER or a contract does not hold")
    bits.append("a type, helper or fixture another segment would also need")
    return "; ".join(bits) + " — everything else it decides"


def render(res):
    seeds = res["seeds"]
    out = [f"SEGMENT {res['root']} · rev {res['rev'][:12]} · variant {res['variant']}"
           + (f" · within {', '.join(res['within'])}" if res["within"] else "")
           + f" · {len(seeds)} seeds · blast {len(res['blast'])} files in {len({cell_of(p) for p in res['blast']})} cells"]
    for s in seeds:
        if s["why"]:
            out.append(f"NOTFOUND {s['raw']} — {s['why']}")
            continue
        defs = ", ".join(sorted({f"{p}:{ln}" if ln else p for p, ln, _ in s["defs"]})[:4]) or "none"
        if len(s["defs"]) > 4:
            defs += f" +{len(s['defs']) - 4}"
        line = (f"SEED {s['raw']} · {s['kind']} · def {defs} · names {len(s['refs'])} files in "
                f"{len({cell_of(p) for p in s['refs']})} cells")
        if s["broad"]:
            line += f" · BROAD {s['broad']} files — kept only its own cells; name it qualified or by path"
        if s["row"]:
            line += f" · row {s['row']}"
        out.append(line)
    if res["verdict"] == "EMPTY":
        out.append(f"VERDICT EMPTY — {res['reason']}")
        return "\n".join(out) + "\n"
    sp = res["spine"]
    if sp:
        fs = sorted({x["path"] for x in sp if x["path"]})
        out.append(f"SPINE {', '.join(fs) or '(keys only)'} · {', '.join(sorted({x['name'] for x in sp}))}")
        for x in sp:
            at = f"{x['path']}:{x['line']}" if x["path"] else ("not written yet" if x.get("new") else "no definition (a key)")
            out.append(f"  CONTRACT {x['name']} · {at} · `{x['signature']}` · used by {', '.join(x['used_by']) or '-'}")
    else:
        out.append("SPINE none — no defined seed is named from two cells")
    p = res["parent"]
    line = (f"PARENT · {', '.join(p['cells']) or '-'} · {len(p['files'])} files · weight {p['weight']} · "
            f"spine {p['spine']}")
    for f in p["folded"]:
        line += f" · folded {', '.join(f['cells'])} (weight {f['weight']}, read {f['read']}: {f['why']})"
    out.append(line)
    for g in res["segments"]:
        out.append(f"SEGMENT {g['id']} · {', '.join(g['cells'])} · {len(g['files'])} files · seeds "
                   f"{', '.join(g['seeds'])} · tests {'; '.join(g['tests'])} · weight {g['weight']} · read "
                   f"{g['read']} of {g['total']} lines ({pct(g['read'], g['weight'])} of its weight)")
        out.append(f"  ORDER {g['id']} · {order_line(g, seeds)}")
        out.append(f"  ESCALATE {g['id']} · {escalate_line(g)}")
    if res["verdict"] == "SPLIT":
        out.append(f"VERDICT SPLIT {res['k']} — {res['reason']}")
    else:
        out.append(f"VERDICT SOLO — {res['reason']}")
    return "\n".join(out) + "\n"


def as_json(res):
    seeds = [{"raw": s["raw"], "row": s["row"], "kind": s["kind"],
              "defs": [f"{p}:{ln}" if ln else p for p, ln, _ in s["defs"]], "refs": sorted(s["refs"]),
              "broad": s["broad"], "notfound": s["why"] or None} for s in res["seeds"]]
    segs = [{k: v for k, v in g.items() if k not in ("sites", "seed_ids")} for g in res["segments"]]
    for g, src in zip(segs, res["segments"]):
        g["order"] = order_line(src, res["seeds"])
        g["escalate"] = escalate_line(src)
    return json.dumps({"root": res["root"], "rev": res["rev"], "variant": res["variant"], "within": res["within"],
                       "seeds": seeds, "blast": res["blast"],
                       "cells": res.get("cells", {}), "spine": res["spine"], "parent": res["parent"],
                       "segments": segs, "verdict": res["verdict"], "k": res.get("k", 0), "reason": res["reason"]},
                      indent=1, ensure_ascii=False, default=list)


USAGE = ("usage: tracetree.py segment ROOT [--rev SHA] --seed SEED... [--seeds FILE] [--within PATH...] "
         "[--variant contract|package|cochange|tracer] [--run RUN] [--history N] [--floor LINES] [--read-max SHARE] [--max K] "
         "[--json]")


def main(argv, fail):
    opts = {"--rev": None, "--run": None, "--history": "300", "--floor": "150", "--read-max": "0.5", "--max": "4",
            "--variant": "contract"}
    raws, files, within, roots, js, i = [], [], [], [], False, 0
    while i < len(argv):
        a = argv[i]
        if a == "--json":
            js, i = True, i + 1
        elif a in ("--seed", "--seeds", "--within") or a in opts:
            if i + 1 >= len(argv):
                fail(f"{a} needs a value — {USAGE}")
            v = argv[i + 1]
            if a == "--seed":
                raws.append(v)
            elif a == "--seeds":
                try:
                    with open(v, encoding="utf-8") as f:
                        files.append(f.read())
                except (OSError, UnicodeDecodeError) as e:
                    fail(f"--seeds {v}: {e}")
            elif a == "--within":
                within.append(v)
            else:
                opts[a] = v
            i += 2
        elif a.startswith("--"):
            fail(f"segment does not take {a!r} — {USAGE}")
        else:
            roots.append(a)
            i += 1
    if len(roots) != 1:
        fail(USAGE)
    try:
        history, floor, read_max = int(opts["--history"]), int(opts["--floor"]), float(opts["--read-max"])
        max_k = int(opts["--max"])
    except ValueError:
        fail("--history, --floor and --max take whole numbers, --read-max a share like 0.5")
    if history < 0 or floor < 0 or read_max < 0 or max_k < 0:
        fail("--history, --floor, --max and --read-max take values of 0 or more")
    try:
        repo = Repo(roots[0], opts["--rev"])
        res = segment(repo, raws, files, within, opts["--variant"], opts["--run"], history, floor, read_max, max_k)
        out = as_json(res) + "\n" if js else render(res)
    except SegmentError as e:
        fail(str(e))
    except Exception as e:  # a crash is a failed answer, never the EMPTY exit 1
        at = traceback.extract_tb(e.__traceback__)[-1]
        fail(f"segment crashed at {os.path.basename(at.filename)}:{at.lineno}: {type(e).__name__}: {e}")
    sys.stdout.write(out)
    return 1 if res["verdict"] == "EMPTY" else 0
