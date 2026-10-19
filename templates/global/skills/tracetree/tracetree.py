#!/usr/bin/env python3
"""tracetree — the run directory and the quote check behind tracer and sub-tracer.

  tracetree.py new ROOT [--seek REGEX] [--questions [--recall RUN]...]
                                        create a run directory under /tmp/{project}/tracer/, print its path;
                                        --questions reads numbered questions (`1.`, `1)`, `Q1`) from stdin to EOF into
                                        RUN/questions (stdin is read only with it); --seek lists, after the path, every
                                        verified hop of every earlier run on this ROOT (running ones included) whose
                                        path or current line matches REGEX, then one PRIOR: line; in a git work tree
                                        RUN/BASE holds the commit ROOT is at (no file otherwise). With --questions the
                                        recall of them over every earlier run of ROOT follows: written to
                                        RUN/recall.txt, a `RECALL: RUN/recall.txt` line, then the recall; each --recall
                                        RUN is read first, in order; a recall that fails prints `RECALL FAILED — …`
  tracetree.py recall ROOT [--before RUN] [--recall RUN]... < numbered questions
                                        earlier runs' answer lines on the questions' anchors, re-checked now: a line
                                        whose node's walked files and cited files all hash as at its `write` (sidecar
                                        sha256) is FRESH with no git asked; else placed and witnessed against git. Per
                                        question a TRACE line, and a REWALK line per earlier node with a line not
                                        FRESH: `REWALK Q{n} · {run} node {x} · FROM {its FROM} · changed: {paths}`,
                                        or `· files unchanged; re-walk for: {why}` when none changed; past the 12 KB
                                        cap REWALK lines fold into one `REWALK Q{n} · … cut by the cap` line per
                                        question before any FRESH line goes
  tracetree.py start RUN ID PATH:LINE [QUESTION]
                                        register node ID as running (RUN/ID.started holds `PATH:LINE<tab>QUESTION`),
                                        print PATH from LINE-3 to LINE+40, numbered; a QUESTION of the form Q{n}
                                        prints question n of RUN/questions above them
  tracetree.py write RUN ID < FILE      store node ID's hops file once; an incomplete file is refused unstored. Every
                                        hop is then placed against the files now: the first write with a hop that does
                                        not hold (no such file, quote nowhere, a directory, a quote elided with `...`)
                                        stores nothing, prints TRACETREE REFUSED and one line per bad hop with a hint
                                        (the file's lines at the address, the files with that basename, or none), and
                                        leaves RUN/ID.refused; the next write stores the file with those hops as they
                                        are. A stored file has each placed hop at its real path:line, and RUN/ID.write.json
                                        holds one entry per hop in order: given (path:line as written), path, line, end,
                                        how, status (verified | moved | rejected), sha256 of the file at write. A third
                                        write is refused; a refused node is still running for `wait`.
  tracetree.py wait RUN ID...           block until those nodes and every child they split off wrote their files,
                                        then print the answers view and an OPEN block (every NOT READ and LEAD line of
                                        the shown nodes, tied to its node; a path:line no verified hop holds is marked
                                        grep only) (--timeout S, default 180); a named node not started after --start S
                                        (default 120) is reported, never waited for; the full view is always written to
                                        RUN/answers.txt and over 28000 characters only a trimmed view is printed
  tracetree.py check RUN                each node's ANSWER lines, each cite checked against the run's verified hops
  tracetree.py check RUN --hops [ID]    every hop, one line each (one node and its subtree with ID)
  tracetree.py check RUN --full [ID]    every hop with every line of its quoted range
  tracetree.py read RUN FILE:A-B [FILE:A-B ...]
                                        the lines A-B of FILE (relative to the run's ROOT), numbered; a block over 30
                                        lines, a block one verified hop holds whole or a missing range is refused on
                                        its own line; one read call per round (RUN/.reads, reset by `wait`)
  tracetree.py map RUN [--from NAME | --to NAME] [--all] [--json | --html OUT]
                                        the component map built from the verified hops (tracemap.py): a text
                                        declaration, a tree below (--from) or above (--to) a component, the
                                        map as JSON, or on request the Tracer Loom (loom.py, tracemap_html.py): one
                                        self-contained page of four views — the wall of the files' true text with each
                                        reader sewn across it, each reader's chain of custody, the hourglass around
                                        one component (--from/--to pick it, --all sets its toggle) and the stitch key
                                        — with the questions, the run's clock, each node's checked answers and the
                                        text of the files its hops sit in; `wait` also writes the text map to
                                        RUN/map.txt
  tracetree.py book [--html OUT]        one swatch row per run under /tmp/{project}/tracer, newest first (id, repo,
                                        questions, time, nodes, hops, held, moved, worn, holes, claims, flagged); a run
                                        the kit cannot load is listed with its reason; --html OUT writes the swatch-book
                                        page (sort by newest, most worn or most flagged) instead of the table

A hop's quote is re-read from the file: found at its address it is verified, found within 30 lines or elsewhere
in the file it is re-addressed, found nowhere it is REJECTED. A hop whose path places no file is resolved, in
order, by: the normalised path (`..` collapsed); the one file with the path's basename holding the quote within 30
lines (several: the one holding it at the cited line) — `basename and quote`; the one sibling file of the cited
directory holding it within 2 lines — `sibling file`; the one file under ROOT with that whole line, of at least
24 non-space characters and the same extension — `unique line`. Each prints `(path resolved from {given}: {how})`.
`check` (every view), `write`, the answers view and the map share that one resolver.
A rejected hop that held when walked is DRIFTED, never REJECTED: its sidecar says it was verified or moved and the file's
sha256 now differs or the file is gone; or, with no sidecar (an older run), the commit the run started from (RUN/BASE,
else the reflog of HEAD at RUN/ROOT's mtime) holds its first quote line near the cited line. COUNTS gains
`drifted=N` after `rejected=`; the map leaves drifted hops out; an answer cite backed only by drifted hops is marked
`≈`. A git command that fails or takes over 10 s leaves the hop REJECTED and prints one `DRIFT UNKNOWN — {reason}`.
The printout always shows the file's own text.
A file past 1 MB is searched only within 30 lines of a hop's address and of an answer cite's: a quote further off is
REJECTED (`quote not within 30 lines of the address`) and a cite's quoted text is not moved there. A file past 128 MB is
not read: its hops are REJECTED (`file is N MB, over the 128 MB read limit`); a path that is no regular file is not
opened (`not a regular file`). Each such file a hop, cite or FROM was checked in is named once, by a `·` note after the
WARNINGS block (the JSON's `notes`) and by `start`, so no file makes a command search or read without bound.
A hop, a `start` FROM, a `read` block and an answer cite can be pinned to a git revision of ROOT: `{rev}:{path}:{line}`.
The revision is resolved to its commit (`git rev-parse`), the file is that commit's (`git show`, read-only: nothing is
checked out), and the address is stored and printed as `{commit, 12 hex}:{path}`. A pinned hop is checked like any hop
(its quote at the line, else within the file, else REJECTED), never placed in the working tree, never DRIFTED, and breaks
no contract by being pinned; a path the commit lacks or holds as a directory, a file there past 128 MB, or a revision
naming no commit is REJECTED with that reason, and a git that cannot answer with `git could not say: {reason}`. `write`
stores every pinned address, cite and NOT READ or LEAD range under its commit, never touching `>` and `=` lines.
A node that broke the hop contract is named without drilling, and in the answers view every answer line of that node, and
every line whose cite only that node's verified hops back, ends ` ⚠ node {ID} broke the hop contract`. `wait` (right after the WAIT line) and every `check` view
(first) print a WARNINGS block, from the sidecar's `how` and `status` per hop, the `ID.refused` marker, and the check-time
resolution and drift: per node, hops the resolver placed by `basename and quote`, `sibling file` or `unique line` (plain
tail resolution is none), a write refused once and stored with hops that still do not hold, hops REJECTED; then one
run line for hops DRIFTED. A placed hop whose quote sits at the very line it cited only miscopied its path: it breaks
no contract and prints a `· node {ID}: the kit corrected a miscopied path …` note after the block, or after
`WARNINGS: none`. Flagged answer lines stay in COUNTS alone, so a clean walk never warns. A run with none prints
`WARNINGS: none`; a sidecar that cannot be read prints a could-not-look line instead. COUNTS gains `placed=N` (hops), `followed=N` (hops) and `refused=N` (nodes)
after `drifted=`.
A hop is placed only when its given path never held the quote. One the resolver re-addresses at check time whose given
path held the quote when walked is FOLLOWED, a file moved since: with a sidecar, an entry stored as verified or moved
that was no placement when written; with none, the commit the run started from (the same RUN/BASE or reflog commit as
drift) holds the first quote line within 30 lines of the cited line at the given path. A given path that commit lacks
or that lacks the quote, and a commit git cannot name (`DRIFT UNKNOWN`), leave the hop placed. A followed hop keeps its
verified or moved status at the new path, flagged `followed a file moved since the walk`, and backs answers and the map;
it prints no node line and, beside other warnings only, one run line `⚠ run: {k} hops followed files moved since the walk
({given} → {path}, …)`; alone it leaves `WARNINGS: none`.
The MAP line and the first line of the map text end ` · ⚠ {n} nodes broke the hop contract` and ` · ⚠ {k} drifted` when
they apply; `map --json` carries `"warnings": [lines]` and `"notes": [lines]`, `map --html` a red block of the warnings
and a quiet one of the notes, both above the views. `write` prints the
same two findings to stderr for its own node, exit 0.
A failure prints `TRACETREE FAILED — {reason}` to stderr and exits 2. Python 3 standard library only.
"""
import contextlib
import glob
import hashlib
import io
import json
import os
import random
import re
import stat
import subprocess
import sys
import time

import tracemap

NEAR = 30
SIBLING_NEAR = 2
UNIQUE_CHARS = 24
SEARCH_MAX_BYTES = 1 << 20
READ_MAX_BYTES = 128 << 20
PIN_ABBREV = 12
GIT_TIMEOUT = 10
NOTE_SPAN = 40
CITE_SLACK = 3
PREFIX = re.compile(r"^\s*\d+(?:\t|→|:\s|\s{2,}|\|\s)")
IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]{2,}")
BACKTICK = re.compile(r"`([^`]+)`")
ADDR = re.compile(r"@\s*(\S+?):(\d+)(?:-(\d+))?\s*$")
CITE = re.compile(r"\[([^\[\]\s]+?):(\d+)(?:-(\d+))?\]")
XREF = re.compile(r"((?:[0-9a-f]{12}:)?[\w./-]+\.\w+):(\d+)")
# a line range a NOT READ or LEAD line names: `path:A-B`, or `path lines A-B, C and D-E`
SPANS = re.compile(r"((?:[0-9a-f]{12}:)?[\w./-]+\.\w+)(?::(\d+)-(\d+)|\s+lines?\s+(\d+(?:-\d+)?(?:(?:\s*,\s*|\s+and\s+)\d+(?:-\d+)?)*))")
PINNED = re.compile(r"[0-9a-f]{%d}:" % PIN_ABBREV)
# `{rev}:{path}` followed by `:LINE` or ` lines A-B`, the way a hop, cite, NOT READ or LEAD line names a pinned range
PINREF = re.compile(r"(?<![\w:~^])([\w.~^/-]+:[\w./-]+\.\w+)(?=:\d|\s+lines?\s+\d)")
PLACEHOLDER = re.compile(r"\{[^{}]*\}")
NODE_ID = re.compile(r"[0-9]+(?:\.[0-9]+)*")
GAPS = re.compile(r"…|\.\.\.|<[^<>]*>|\{[^{}]*\}")
KINDS = ("THREAD", "SPLIT", "LEAD", "END", "NOT READ")
KEYWORDS = {"func", "return", "error", "string", "true", "false", "nil", "none", "self", "this",
            "const", "var", "def", "class", "import", "from", "with", "for", "the", "and", "not"}


def megabytes(size):
    return f"{size / (1 << 20):.1f} MB"


def fail(msg):
    sys.stderr.write(f"TRACETREE FAILED — {msg}\n")
    sys.exit(2)


def project_name(root):
    """The repository's directory name minus any leading dot, the same for the main checkout and every worktree;
    the root's own basename when git cannot say."""
    try:
        done = subprocess.run(["git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                              capture_output=True, text=True, timeout=10)
        common = os.path.normpath(done.stdout.strip()) if done.returncode == 0 else ""
    except (OSError, subprocess.SubprocessError):
        common = ""
    if common:
        # a bare repository prints no .git component: its own directory is the name
        name = os.path.basename(os.path.dirname(common) if os.path.basename(common) == ".git" else common)
    else:
        name = os.path.basename(os.path.normpath(root))
    return name.lstrip(".") or "root"


QSTART = re.compile(r"^\s*(?:[Qq](\d+)(?:[.:)]\s*|\s+|$)|(\d+)[.)](?:\s+|$))(.*)$")
PLACED_HOW = ("basename and quote", "sibling file", "unique line")
FOLLOWED_HOW = "followed a file moved since the walk"
PAIRS_SHOWN = 3
SEEK_SHOWN = 30
PRIOR_CUT = 160
CEILING = 28000


def parse_questions(text):
    """The numbered questions of a text: {n: text}. A line starting `n.`, `n)` or `Qn` begins question n and the
    lines after it continue it; lines before the first number belong to no question."""
    got, cur = {}, None
    for raw in text.splitlines():
        m = QSTART.match(raw)
        if m:
            cur = int(m.group(1) or m.group(2))
            got[cur] = [m.group(3).strip()]
        elif cur is not None and raw.strip():
            got[cur].append(raw.strip())
    return {n: "\n".join(p for p in parts if p) for n, parts in got.items() if any(parts)}


def counters(names):
    return dict(nodes=len(names), hops=0, verified=0, moved=0, rejected=0, drifted=0, placed=0, followed=0, refused=0, partial=0, unseen=0,
                answers=0, flagged=0, recited=0, missing=0, badaddr=0, noend=0)


def seek_prior(run, root, rx):
    """Every earlier run on this root, running ones included: every verified hop whose path or current line matches rx,
    the most recently modified run first. What it could not read is counted in the summary, never dropped."""
    parent = os.path.dirname(run)
    try:
        entries = os.listdir(parent)
    except OSError as e:
        print(f"PRIOR: could not look — cannot list {parent}: {e}")
        return
    found, scanned, unreadable = [], [], 0
    for name in entries:
        other = os.path.join(parent, name)
        if other == run or not os.path.isdir(other):
            continue
        try:
            stamp = os.path.getmtime(other)
            with open(os.path.join(other, "ROOT"), encoding="utf-8") as f:
                if f.read().strip() != root:
                    continue
        except OSError as e:
            unreadable += 1
            sys.stderr.write(f"PRIOR: cannot read run {other}: {e}\n")
            continue
        found.append((stamp, name, other))
    files, hits = Files(root), []
    for _, name, other in sorted(found, reverse=True):
        try:
            names = sorted((f[:-5] for f in os.listdir(other) if f.endswith(".hops") and NODE_ID.fullmatch(f[:-5])),
                           key=node_key)
        except OSError as e:
            unreadable += 1
            sys.stderr.write(f"PRIOR: cannot list run {other}: {e}\n")
            continue
        scanned.append(other)
        nodes, _ = verify(other, files, names, counters(names), drift=False)
        if any(nodes[x]["struct"] and nodes[x]["struct"][0].startswith("UNREADABLE") for x in names):
            unreadable += 1
            sys.stderr.write(f"PRIOR: a hops file of run {other} could not be read\n")
        for x in names:
            for w in nodes[x]["walk"]:
                if w["t"] != "hop":
                    continue
                text = files.lines(w["key"])[w["line"] - 1]
                if rx.search(w["shown"]) or rx.search(text):
                    hits.append((other, x, w["shown"], w["line"], text.strip()))
    for other, x, path, line, text in hits[:SEEK_SHOWN]:
        shown = text if len(text) <= PRIOR_CUT else text[:PRIOR_CUT - 3] + "..."
        print(f"PRIOR {other} node {x}: {path}:{line}  {shown}")
    tail = f", {unreadable} runs unreadable" if unreadable else ""
    if not hits:
        print(f"PRIOR: none in {len(scanned)} earlier runs on this root{tail}")
        return
    hidden = f", {len(hits) - SEEK_SHOWN} not shown" if len(hits) > SEEK_SHOWN else ""
    print(f"PRIOR: {len(hits)} hops in {len({h[0] for h in hits})} runs{hidden} — "
          f"tt check {hits[0][0]} --hops {hits[0][1]}{tail}")


def write_base(run, root):
    """RUN/BASE: the commit ROOT is at, for `check` to tell a hop the code moved out from under from one that never held.
    A root that is no git work tree (or has no commit) gets no file; a git that cannot answer is named, never fatal."""
    try:
        done = subprocess.run(["git", "-C", root, "rev-parse", "HEAD"], capture_output=True, text=True,
                              timeout=GIT_TIMEOUT)
        if done.returncode == 0 and done.stdout.strip():
            with open(os.path.join(run, "BASE"), "w") as f:
                f.write(done.stdout.strip() + "\n")
    except (OSError, subprocess.SubprocessError) as e:
        sys.stderr.write(f"BASE not written for {run}: {type(e).__name__}: {e}\n")


def cmd_new(root, seek=None, questions=False, recalls=()):
    """Open a run and print its path; with seek, the PRIOR lines; with questions, the recall of them over every earlier
    run of ROOT (the `recalls` runs first), written to RUN/recall.txt and printed after a `RECALL:` line naming it."""
    root = os.path.abspath(root)
    if not os.path.isdir(root):
        fail(f"repo root {root} is not a directory")
    try:
        rx = re.compile(seek) if seek is not None else None
    except re.error as e:
        fail(f"--seek {seek!r} is not a regular expression: {e}")
    asked = None
    if questions:
        asked = sys.stdin.read()
        if not parse_questions(asked):
            fail("--questions read no numbered question from stdin: start each with `1.`, `1)` or `Q1`")
    try:
        run = open_run(root, asked)
    except OSError as e:
        fail(f"cannot create the run directory: {e}")
    print(run)
    if rx is not None:
        seek_prior(run, root, rx)
    if asked is not None:
        recall_into(run, root, parse_questions(asked), recalls)


def recall_into(run, root, asked, recalls):
    """The recall of a new run's questions over every earlier run of ROOT, this run excluded: written to RUN/recall.txt,
    the `RECALL: {path}` line, then the recall itself. A recall that raises is the `RECALL FAILED` line, never an empty
    store; a recall.txt that cannot be written is named and the recall still printed."""
    try:
        lines = recall(root, asked, exclude=run, recalls=recalls)
    except Exception as e:  # the run stays open whatever recall hits; the failure is the printout
        sys.stderr.write(f"recall of {run} failed: {type(e).__name__}: {e}\n")
        lines = [f"RECALL FAILED — {type(e).__name__}: {e}: trace every question"]
    path = os.path.join(run, "recall.txt")
    try:
        with open(path, "w", encoding="utf-8") as f:
            f.write("\n".join(lines) + "\n")
        print(f"RECALL: {path}")
    except OSError as e:
        print(f"RECALL NOT SAVED — cannot write {path}: {e}")
    print("\n".join(lines))


def open_run(root, asked):
    """A new run directory for ROOT under /tmp/{project}/tracer/, holding ROOT, BASE and, when given, the questions
    text; its path. An OSError names the directory it could not write."""
    run = os.path.join("/tmp", project_name(root), "tracer",
                       time.strftime("%Y%m%d-%H%M%S") + f"-{random.randrange(16**4):04x}")
    try:
        os.makedirs(run)
        with open(os.path.join(run, "ROOT"), "w") as f:
            f.write(root + "\n")
        if asked is not None:
            with open(os.path.join(run, "questions"), "w", encoding="utf-8") as f:
                f.write(asked)
    except OSError as e:
        raise OSError(f"{run}: {e}") from e
    write_base(run, root)
    return run


def loose(s):
    return " ".join(s.split())


def candidates(q):
    """The quote as written, without a pasted line-number prefix, without a backslash escaping its indentation
    (`\\    def f():`), without wrapping backticks."""
    out = [q]
    m = PREFIX.match(q)
    if m:
        out.append(q[m.end():])
    for c in list(out):
        s = c.lstrip()
        if s.startswith("\\") and s[1:2].isspace():
            out.append(s[1:])
    for c in list(out):
        s = c.strip()
        if len(s) > 2 and s.startswith("`") and s.endswith("`"):
            out.append(s[1:-1])
    return out


def same(q, line):
    a, b = q.strip(), line.strip()
    if not b:
        return None
    if a == b:
        return "exact"
    if loose(a) == loose(b):
        return "loose"
    if len(a) >= 12 and a in line:
        return "fragment"
    return None


SKIP_DIRS = {".git", "node_modules", ".worktrees", ".venv", "__pycache__"}
RANK = {"exact": 0, "loose": 1, "fragment": 2}


class Files:
    def __init__(self, root):
        self.root = root
        self.cache = {}
        self.all = None
        self.bases = None
        self.index = None
        self.alias = {}
        self.drifts = []
        self.readdressed = []  # (key, given line, given end, line it holds at): hops the kit moved off their reader's address
        self.unknown = {}
        self.sidecars = {}
        self.git_dead = None
        self.git_root = None
        self.starts = {}
        self.old_text = {}
        # files past SEARCH_MAX_BYTES loaded / searched near an address only / past READ_MAX_BYTES / no regular file /
        # git could not read; pinned addresses, revisions resolved and revisions git could not resolve
        self.large, self.big, self.unread, self.not_file, self.git_errors = {}, {}, {}, {}, {}
        self.pins, self.revs, self.rev_errors = {}, {}, {}

    def pin(self, path):
        """(commit or None, path from ROOT, revision) for an address pinned to a git revision, `{rev}:{path}` — a name
        that is no file under ROOT and holds a colon; the commit is None when the revision names none. None for any
        other path. A pinned address is the file as that commit holds it, read with git alone."""
        if ":" not in path:
            return None
        if path not in self.pins:
            spec = path
            if os.path.isabs(path):
                spec = path[len(self.root) + 1:] if path.startswith(self.root + os.sep) else ""
            rev, _, rel = spec.partition(":")
            rel = os.path.normpath(rel) if rel else ""
            if (not rev or not rel or rel.startswith("..") or os.path.isabs(rel) or rev.startswith("-")
                    or any(c.isspace() for c in rev) or os.path.lexists(os.path.join(self.root, spec))):
                self.pins[path] = None
            else:
                if rev not in self.revs:
                    try:
                        code, out, _ = git_out(self, ["rev-parse", "--verify", "-q", rev + "^{commit}"])
                        self.revs[rev] = out.strip() if code == 0 and out.strip() else None
                    except GitUnknown as e:
                        self.unknown[str(e)] = self.unknown.get(str(e), 0) + 1
                        self.revs[rev], self.rev_errors[rev] = None, str(e)
                self.pins[path] = (self.revs[rev], rel, rev)
        return self.pins[path]

    def key(self, path):
        pin = self.pin(path)
        if pin and pin[0]:
            return os.path.join(self.root, f"{pin[0][:PIN_ABBREV]}:{pin[1]}")
        p = path if os.path.isabs(path) else os.path.join(self.root, path)
        return os.path.normpath(p)

    def lines(self, path):
        p = self.key(path)
        if p not in self.cache:
            pin = self.pin(path)
            self.cache[p] = self.revision_lines(p, pin) if pin and pin[0] else self.file_lines(p)
        return self.cache[p]

    def file_lines(self, p):
        """The lines of the regular file at p, read up to READ_MAX_BYTES; None when it cannot be read, is no regular
        file (named in `not_file`) or is past READ_MAX_BYTES (named in `unread`). A file past SEARCH_MAX_BYTES is named
        in `large`."""
        try:
            st = os.stat(p)
            if not stat.S_ISREG(st.st_mode):
                if not stat.S_ISDIR(st.st_mode):
                    self.not_file[p] = "not a regular file"
                return None
            if st.st_size > READ_MAX_BYTES:
                self.unread[p] = st.st_size
                return None
            with open(p, encoding="utf-8", errors="replace") as f:
                text = f.read(READ_MAX_BYTES + 1)
        except OSError:
            return None
        size = max(st.st_size, len(text))
        if size > READ_MAX_BYTES:
            self.unread[p] = size
            return None
        if size > SEARCH_MAX_BYTES:
            self.large[p] = size
        return text.split("\n")

    def revision_lines(self, p, pin):
        """The lines of a pinned file; None when its commit lacks the path, holds a directory there, holds a file past
        READ_MAX_BYTES (named in `unread`, never read) or git cannot say (named in `git_errors`, counted in `unknown`)."""
        try:
            code, out, err = git_out(self, ["ls-tree", "-l", pin[0], "--", pin[1]])
            if code != 0:
                raise GitUnknown(f"git ls-tree {pin[0][:PIN_ABBREV]} {pin[1]} failed: {err.strip()[:120]}")
            entry = out.split("\n")[0].split("\t")[0].split()
            if not entry:
                return None
            if entry[1] != "blob":
                self.not_file[p] = "a directory, not a file" if entry[1] == "tree" else f"a {entry[1]}, not a file"
                return None
            if entry[3].isdigit() and int(entry[3]) > READ_MAX_BYTES:
                self.unread[p] = int(entry[3])
                return None
            lines = text_at(self, pin[0], pin[1])
        except GitUnknown as e:
            self.unknown[str(e)] = self.unknown.get(str(e), 0) + 1
            self.git_errors[p] = str(e)
            return None
        if lines is not None:
            size = sum(len(l) + 1 for l in lines)
            if size > SEARCH_MAX_BYTES:
                self.large[p] = size
        return lines

    def reach(self, path):
        """NEAR for a file past SEARCH_MAX_BYTES, the only distance from an address it is searched, and the file joins
        `big`, the files named in the size notes; None for the rest."""
        k = self.key(path)
        if k not in self.large:
            return None
        self.big[k] = self.large[k]
        return NEAR

    def why_unread(self, path, found=None):
        """Why `path` (found as the file `found`, when a search found one) places no file: git could not say, a
        revision that names no commit, past READ_MAX_BYTES, no regular file, a commit lacking the path, else absent."""
        pin = self.pin(path)
        if pin and not pin[0]:
            why = self.rev_errors.get(pin[2])
            return f"git could not say: {why}" if why else f"no commit named {pin[2]}"
        k = found or self.key(path)
        if k in self.git_errors:
            return f"git could not say: {self.git_errors[k]}"
        if k in self.unread:
            return f"file is {megabytes(self.unread[k])}, over the {READ_MAX_BYTES >> 20} MB read limit"
        if k in self.not_file:
            return self.not_file[k]
        return f"no such file at {pin[0][:PIN_ABBREV]}" if pin else "no such file"

    def listing(self):
        """Every file under ROOT, from one listing per process: `git ls-files` (tracked and untracked, not ignored)
        in a git work tree, else one walk of the tree."""
        if self.all is None:
            names = None
            try:
                done = subprocess.run(["git", "-C", self.root, "ls-files", "-z", "--cached", "--others",
                                       "--exclude-standard"], capture_output=True, timeout=GIT_TIMEOUT)
                if done.returncode == 0 and done.stdout:
                    names = [os.path.join(self.root, x) for x in
                             done.stdout.decode("utf-8", "replace").split("\0") if x]
            except (OSError, subprocess.SubprocessError):
                names = None
            if names is None:
                names = []
                for d, subdirs, files in os.walk(self.root):
                    subdirs[:] = [x for x in subdirs if x not in SKIP_DIRS]
                    names += [os.path.join(d, x) for x in files]
            self.all = sorted(p for p in names if not SKIP_DIRS.intersection(p[len(self.root):].split(os.sep))
                              and os.path.isfile(p))
        return self.all

    def ending(self, tail):
        """Every file under ROOT whose path ends in tail."""
        return [p for p in self.listing() if p.endswith(tail)]

    def named(self, base):
        """Every file under ROOT whose basename is base."""
        if self.bases is None:
            self.bases = {}
            for p in self.listing():
                self.bases.setdefault(os.path.basename(p), []).append(p)
        return self.bases.get(base, [])

    def long_lines(self):
        """{loose line: [(file, 0-based line)]} for every line of at least UNIQUE_CHARS non-space characters of every
        listed text file, read once per process."""
        if self.index is None:
            self.index = {}
            for p in self.listing():
                try:
                    if os.path.getsize(p) > SEARCH_MAX_BYTES:
                        continue
                    with open(p, "rb") as f:
                        raw = f.read()
                except OSError:
                    continue
                if b"\0" in raw[:8192]:
                    continue
                for i, text in enumerate(raw.decode("utf-8", "replace").split("\n")):
                    if len(text) >= UNIQUE_CHARS and len("".join(text.split())) >= UNIQUE_CHARS:
                        self.index.setdefault(loose(text), []).append((p, i))
        return self.index

    def find(self, ref, quote=None, at=0, prefer=()):
        """A file a hop, note or answer names: its exact path, else the one read file ending in it, else the one
        file under ROOT ending in it. Several files ending in it: the one holding quote, nearest line at, or the
        one among prefer — a node that wrote `server.go` for `pfm/internal/mcpserv/server.go` is still checked."""
        got = self.lines(ref)
        if got:
            return self.key(ref), got
        tail = "/" + ref.lstrip("./")
        hits = [p for p, v in self.cache.items() if v and p.endswith(tail)]
        if not hits:
            # a path written relative to a subdirectory of ROOT (pfm/internal/x.go cited as internal/x.go)
            hits = sorted({os.path.normpath(p) for depth in ("*", "*/*") for p in
                           glob.glob(os.path.join(glob.escape(self.root), depth, ref.lstrip("./")))
                           if os.path.isfile(p)})
        if not hits:
            hits = self.ending(tail)
        if len(hits) > 1 and quote:
            scored = []
            for p in hits:
                i, want = find_first(self.lines(p) or [], quote, at, self.reach(p))
                if i is not None:
                    scored.append((RANK[want], abs(i - at), p))
            hits = [min(scored)[2]] if scored else []
        elif len(hits) > 1 and prefer:
            hits = [p for p in hits if p in prefer]
        if len(hits) != 1:
            aliased = self.alias.get(ref, ())
            return (next(iter(aliased)), self.lines(next(iter(aliased)))) if len(aliased) == 1 else (None, None)
        return hits[0], self.lines(hits[0])

    def shown(self, key, ref):
        """The path to print for a file found from ref: ref itself when it names the file from ROOT."""
        rel = os.path.relpath(key, self.root)
        return ref if os.path.normpath(ref) == rel else rel


def find_first(lines, quote, at, reach=None):
    """The 0-based line holding quote and how: nearest `at` within NEAR lines, else anywhere in the file; with
    `reach`, only within that many lines of `at`."""
    span = NEAR if reach is None else reach
    order = [at] + [at + d for k in range(1, span + 1) for d in (-k, k)]
    order = [i for i in order if 0 <= i < len(lines)]
    seen = set(order)
    rest = [] if reach is not None else [i for i in range(len(lines)) if i not in seen]
    for want in ("exact", "loose", "fragment"):
        for scope in (order, rest):
            for i in scope:
                for q in candidates(quote):
                    if same(q, lines[i]) == want:
                        return i, want
    return None, None


def match_from(lines, start, quote):
    i = start
    while i < len(lines) and not lines[i].strip() and quote.strip():
        i += 1
    if i < len(lines) and any(same(q, lines[i]) for q in candidates(quote)):
        return i
    return None


def hit_at(key, lines, quote, at, reach, how):
    """A resolution of a hop into the file `key`, if its text holds the quote within `reach` lines of `at`."""
    i, tag = find_first(lines or [], quote, at, reach)
    return None if i is None else dict(key=key, lines=lines, first=i, tag=tag, how=how)


def resolve_elsewhere(files, path, ln, quote, found):
    """Where a hop's quote is when its path does not place it, in order: the one file with the path's basename holding
    the quote within NEAR lines (several: the one holding it at the cited line); when the path names a real file
    (`found`), the one sibling in its directory holding it within SIBLING_NEAR lines; the one file of the same
    extension that is the only file under ROOT with that whole line, if it is UNIQUE_CHARS non-space characters long.
    A dict with key, lines, first, tag and how, or None."""
    at, base = ln - 1, os.path.basename(os.path.normpath(path))
    hits = [h for p in files.named(base) if p != found
            for h in [hit_at(p, files.lines(p), quote, at, NEAR, "basename and quote")] if h]
    if len(hits) > 1:
        hits = [h for h in hits if h["first"] == at]
    if len(hits) == 1:
        return hits[0]
    if found:
        here = os.path.dirname(found)
        sibs = [h for p in files.listing() if p != found and os.path.dirname(p) == here
                for h in [hit_at(p, files.lines(p), quote, at, SIBLING_NEAR, "sibling file")] if h]
        if len(sibs) == 1:
            return sibs[0]
    ext = os.path.splitext(path)[1]
    for cand in candidates(quote):
        spots = files.long_lines().get(loose(cand), [])
        if len({p for p, _ in spots}) == 1 and os.path.splitext(spots[0][0])[1] == ext:
            p, i = min(spots, key=lambda x: abs(x[1] - at))
            lines = files.lines(p)
            if lines is not None and i < len(lines):
                return dict(key=p, lines=lines, first=i, tag=same(cand, lines[i]) or "exact", how="unique line")
    return None


def has_gap(quote):
    return "..." in quote or "…" in quote


def place_hop(files, path, ln, quotes):
    """One hop placed against the files as they are now, the one resolver `check` and `write` share. A dict:
    status verified | moved | rejected | badaddr; reason for the last two; key and lines of the file it was placed in
    (of the file the path names when only the quote failed); got, the 0-based lines its quote lines match; tag; how,
    the resolution beyond today's exact path and tail (basename and quote, sibling file, unique line; else empty);
    broke, the index of the quote line that stopped matching; elided, a rejected or broken quote with `...`/`…`."""
    got = dict(status="rejected", reason="no such file", key=None, lines=None, got=[], tag="", how="", broke=None,
               elided=False)
    quote = quotes[0] if quotes else None
    key, lines = files.find(path, quote, ln - 1)
    norm = os.path.normpath(path)
    if lines is None and norm != path:
        key, lines = files.find(norm, quote, ln - 1)
    if lines is None and os.path.isdir(files.key(path)):
        got["reason"] = "a directory, not a file"
        return got
    if lines is not None:
        got["key"], got["lines"] = key, lines
    if not quotes:
        if lines is None:
            got["reason"] = files.why_unread(path, key)
            return got
        if 1 <= ln <= len(lines):
            return dict(got, status="verified", got=[ln - 1], tag="unquoted")
        return dict(got, status="badaddr", reason=f"the file has {len(lines)} lines")
    first, tag, how = None, "", ""
    if lines is not None:
        first, tag = find_first(lines, quote, ln - 1, files.reach(key))
    if first is None:
        # a pinned hop is never placed in the working tree: another revision's file is not the one it names
        there = None if files.pin(path) else resolve_elsewhere(files, path, ln, quote, key)
        if there is None:
            got["reason"] = files.why_unread(path, key) if lines is None else \
                f"quote not within {NEAR} lines of the address" if files.reach(key) else "quote not in the file"
            got["elided"] = has_gap(quote)
            return got
        key, lines, first, tag, how = there["key"], there["lines"], there["first"], there["tag"], there["how"]
    lit = [first]
    for i, q in enumerate(quotes[1:], 1):
        nxt = match_from(lines, lit[-1] + 1, q)
        if nxt is None:
            got["broke"], got["elided"] = i, has_gap(q)
            tag += f", quote broke after {len(lit)} line(s)"
            break
        lit.append(nxt)
    return dict(got, status="moved" if lit[0] + 1 != ln else "verified", key=key, lines=lines, got=lit, tag=tag, how=how)


def parse(text):
    recs, cur, in_answer = [], None, False
    for at, raw in enumerate(text.split("\n")):
        line = raw.rstrip()
        s = line.strip()
        if s.upper().rstrip(":") == "ANSWER":
            in_answer, cur = True, None
            continue
        if in_answer:
            if s.startswith("- ") or s.startswith("* "):
                recs.append(["answer", s[2:].strip()])
                continue
            if not s:
                continue
            in_answer = False
        if s.startswith("@") and not s.startswith("@@"):
            m = ADDR.match(s)
            if m:
                cur = ["hop", m.group(1), int(m.group(2)), [], [], at, int(m.group(3) or 0)]
                recs.append(cur)
                continue
        if cur is not None and line.startswith(">"):
            cur[3].append(line[2:] if line.startswith("> ") else line[1:])
            continue
        if cur is not None and s.startswith("="):
            cur[4].append(s[1:].strip())
            continue
        cur = None
        if not s:
            continue
        for kind in KINDS:
            if s.upper().startswith(kind):
                m = re.search(r"@\s*(\S+?):(\d+)\s*$", s)
                recs.append([kind, s, (m.group(1), int(m.group(2))) if m else None])
                break
        else:
            recs.append(["text", s, None])
    return recs


def node_key(name):
    return [int(p) if p.isdigit() else p for p in name.split(".")]


class GitUnknown(Exception):
    """A git command that failed or timed out: the question it answers stays unanswered."""


def git_out(files, args):
    """(returncode, stdout, stderr) of `git -C ROOT args`, each decoded; GitUnknown when git cannot be run or does not
    answer in GIT_TIMEOUT seconds — and, once it has failed that way, for every later call of the process."""
    if files.git_dead:
        raise GitUnknown(files.git_dead)
    try:
        done = subprocess.run(["git", "-C", files.root, *args], capture_output=True, timeout=GIT_TIMEOUT)
    except subprocess.TimeoutExpired:
        files.git_dead = f"git {args[0]} did not answer in {GIT_TIMEOUT}s"
        raise GitUnknown(files.git_dead)
    except (OSError, subprocess.SubprocessError) as e:
        files.git_dead = f"git {args[0]} could not run: {type(e).__name__}: {e}"
        raise GitUnknown(files.git_dead)
    return done.returncode, done.stdout.decode("utf-8", "replace"), done.stderr.decode("utf-8", "replace")


def in_git(files):
    """Whether ROOT is a git work tree; GitUnknown when git cannot say."""
    if files.git_root is None:
        code, out, err = git_out(files, ["rev-parse", "--is-inside-work-tree"])
        if code != 0 and "not a git repository" not in err:
            raise GitUnknown(f"git rev-parse failed: {err.strip()[:120]}")
        files.git_root = code == 0 and out.strip() == "true"
    return files.git_root


def run_start(files, run):
    """The commit the run started from: RUN/BASE, else the commit HEAD pointed at when RUN/ROOT was written, from the
    reflog. GitUnknown when neither names a commit of this repository."""
    if run not in files.starts:
        base_path = os.path.join(run, "BASE")
        try:
            with open(base_path) as f:
                base = f.read().strip()
        except FileNotFoundError:
            base = ""
        except OSError as e:
            raise GitUnknown(f"cannot read {base_path}: {e}")
        if base:
            code, out, _ = git_out(files, ["rev-parse", "--verify", "-q", base + "^{commit}"])
            if code != 0 or not out.strip():
                raise GitUnknown(f"BASE {base[:12]} is no commit of {files.root}")
        else:
            try:
                stamp = os.path.getmtime(os.path.join(run, "ROOT"))
            except OSError as e:
                raise GitUnknown(f"cannot date the run: {e}")
            when = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(stamp))
            code, out, _ = git_out(files, ["rev-parse", "--verify", "-q", f"HEAD@{{{when}}}"])
            if code != 0 or not out.strip():
                raise GitUnknown(f"no BASE file and the reflog of HEAD names no commit at {when}")
            code, stamps, err = git_out(files, ["log", "-g", "--format=%ct", "HEAD"])
            first = stamps.split()[-1] if code == 0 and stamps.split() else ""
            if not first.isdigit() or int(first) > stamp:
                raise GitUnknown(f"no BASE file and the reflog of HEAD begins after the run did ({when})")
        files.starts[run] = out.strip()
    return files.starts[run]


def text_at(files, sha, rel):
    """The lines of `rel` in commit sha, None when that commit has no such path; GitUnknown when git cannot say."""
    if (sha, rel) not in files.old_text:
        code, out, err = git_out(files, ["show", f"{sha}:./{rel}"])
        if code == 0:
            files.old_text[(sha, rel)] = out.split("\n")
        elif "does not exist in" in err or "exists on disk, but not in" in err:
            files.old_text[(sha, rel)] = None
        else:
            raise GitUnknown(f"git show {sha[:8]}:{rel} failed: {err.strip()[:120]}")
    return files.old_text[(sha, rel)]


def sidecar_entries(files, run, node, hops):
    """The entries of RUN/{node}.write.json, one per hop in order; None when the node has none (an older run), or
    when it cannot be used — that is noted as unknown, never taken for absence."""
    if (run, node) not in files.sidecars:
        path, got = os.path.join(run, node + ".write.json"), None
        try:
            with open(path, encoding="utf-8") as f:
                got = json.load(f)
            if not isinstance(got, list) or not all(isinstance(e, dict) for e in got):
                raise ValueError("not a list of entries")
            if len(got) != hops:
                raise ValueError(f"{len(got)} entries for {hops} hops")
        except (OSError, ValueError) as e:
            if isinstance(e, FileNotFoundError):
                try:
                    modern = any(entry.endswith(".write.json") for entry in os.listdir(run))
                except OSError as lookup:
                    why = f"{node}.write.json cannot be used: missing sidecar; cannot inspect {run}: {lookup}"
                else:
                    why = f"{node}.write.json cannot be used: missing sidecar in a run with write metadata" if modern else ""
            else:
                why = f"{node}.write.json cannot be used: {e}"
            if why:
                files.unknown[why] = files.unknown.get(why, 0) + 1
            got = None
        files.sidecars[(run, node)] = got
    return files.sidecars[(run, node)]


def drift_of(files, run, side, hi, path, ln, quotes, placed):
    """A hop that does not verify now but held when walked: {shown, line, end, sha} or None. It held when its sidecar
    entry says verified or moved and the file's sha256 differs or the file is gone; with no sidecar, when the commit the
    run started from holds its first quote line near the cited line in the given or the resolved path. A git question
    that cannot be answered is counted under files.unknown and the hop stays rejected."""
    if files.pin(path):
        return None
    if side is not None:
        entry = side[hi]
        if entry.get("status") in ("verified", "moved") and entry.get("path") and entry.get("sha256"):
            if file_sha(files.key(entry["path"])) != entry["sha256"]:
                line = entry.get("line", ln)
                return dict(shown=entry["path"], line=line, end=entry.get("end", line), sha=None)
        return None
    if not quotes or placed["reason"].startswith("a directory"):
        return None
    try:
        if not in_git(files):
            return None
        sha = run_start(files, run)
        paths = [files.key(path)] + ([placed["key"]] if placed["key"] else [])
        for rel in dict.fromkeys(os.path.relpath(p, files.root) for p in paths):
            if rel.startswith(".."):
                continue
            old = text_at(files, sha, rel)
            i, _ = find_first(old, quotes[0], ln - 1, NEAR) if old else (None, None)
            if i is not None:
                return dict(shown=rel, line=i + 1, end=i + 1, sha=sha[:8])
    except GitUnknown as e:
        files.unknown[str(e)] = files.unknown.get(str(e), 0) + 1
    return None


def hop_incident(files, run, side, hi, path, ln, quotes, real, how, drift):
    """How a verified hop that the check-time resolver placed stands: "followed" when its given path held the quote when
    walked and the file was moved since, "placed" when the given path never held it, "" when the resolver did not place
    it or the sidecar counted it at write. With a sidecar the write decides: an entry stored as verified or moved at a
    path that was no placement was real, so a re-address now is a followed move. With no sidecar the commit the run
    started from decides: it holds the hop's first quote line near the cited line at the given path (followed), or it
    does not, or has no such path (placed). A hop with no quote, a root that is no git work tree, a view that does not
    look at git (`drift` off) and a git question that cannot be answered each leave it placed; the last is counted under
    files.unknown, never taken for a clean walk."""
    if not placed_path(files.root, path, real, how):
        return ""
    if side is not None:
        entry = side[hi]
        wrote = placed_path(files.root, strip_line(entry.get("given", "")), entry.get("path", ""), entry.get("how"))
        return "followed" if entry.get("status") in ("verified", "moved") and not wrote else ""
    if not quotes or not drift:
        return "placed"
    try:
        if not in_git(files):
            return "placed"
        rel = os.path.relpath(files.key(path), files.root)
        if rel.startswith(".."):
            return "placed"
        old = text_at(files, run_start(files, run), rel)
        i, _ = find_first(old, quotes[0], ln - 1, NEAR) if old else (None, None)
        return "placed" if i is None else "followed"
    except GitUnknown as e:
        files.unknown[str(e)] = files.unknown.get(str(e), 0) + 1
        return "placed"


def verify(run, files, names, counts, drift=True):
    """Per node: rendered hop lines (compact and full), answers, structure lines, and `order`, every hop in the order
    written (a broken one as its status, path, line and reason; a verified one None, its step the walk's next); plus
    every verified range."""
    nodes, ranges = {}, []
    present = set(names)
    for name in names:
        n = {"head": f"THREAD {name}: (no THREAD line written)", "hops": [], "full": [], "answers": [],
             "struct": [], "ok": 0, "bad": 0, "drift": 0, "seen": 0, "ended": False, "split": False, "walk": [], "order": [], "unsettled": [],
             "placed": [], "miscopied": [], "followed": [], "refused": False, "badnow": 0, "rej": 0}
        nodes[name] = n
        try:
            recs = parse(open(os.path.join(run, name + ".hops"), encoding="utf-8", errors="replace").read())
        except OSError as e:
            n["struct"].append(f"UNREADABLE — {e}")
            continue
        nhops = sum(1 for r in recs if r[0] == "hop")
        side = sidecar_entries(files, run, name, nhops) if nhops else None
        if side is not None:
            for e in side:
                given = strip_line(e.get("given", ""))
                if placed_path(files.root, given, e.get("path", ""), e.get("how")):
                    n["miscopied" if miscopy(e) else "placed"].append((given, e.get("path", "")))
            counts["placed"] += len(n["placed"]) + len(n["miscopied"])
        if os.path.exists(os.path.join(run, name + ".refused")):
            n["refused"] = True
            counts["refused"] += 1
        for r in recs:
            kind = r[0]
            if kind == "THREAD":
                n["head"] = r[1]
            elif kind == "answer":
                n["answers"].append(r[1])
            elif kind == "hop":
                counts["hops"] += 1
                _, path, ln, quotes, notes = r[:5]
                hi, n["seen"] = n["seen"], n["seen"] + 1
                placed = place_hop(files, path, ln, quotes)
                gone = drift_of(files, run, side, hi, path, ln, quotes, placed) \
                    if drift and placed["status"] == "rejected" else None
                if gone:
                    counts["drifted"] += 1; n["drift"] += 1
                    files.drifts.append((files.key(gone["shown"]), gone["line"], gone["end"]))
                    at = f" (at {gone['sha']})" if gone["sha"] else ""
                    msg = f"≈ DRIFTED {gone['shown']}:{gone['line']} — held when walked{at}; the file changed since: re-read before pasting"
                    n["hops"].append(msg); n["full"].append(msg)
                    n["order"].append({"st": "drifted", "path": gone["shown"], "a": gone["line"], "flag": msg.split(" — ", 1)[1]})
                    continue
                n["badnow"] += bool(bad_hop(placed))
                if placed["status"] in ("rejected", "badaddr"):
                    if placed["status"] == "badaddr":
                        counts["badaddr"] += 1
                        msg = f"✗ BAD ADDRESS {path}:{ln} — {placed['reason']}"
                    else:
                        counts["rejected"] += 1; n["rej"] += 1
                        why = placed["reason"]
                        msg = f"✗ REJECTED {path}:{ln} — " + (f"{why}: {quotes[0].strip()[:90]}" if quotes and why.startswith("quote") else why)
                    n["bad"] += 1
                    n["hops"].append(msg); n["full"].append(msg)
                    n["order"].append({"st": placed["status"], "path": path, "a": ln, "flag": msg.split(" — ", 1)[1]})
                    continue
                fkey, lines, got, tag, how = placed["key"], placed["lines"], placed["got"], placed["tag"], placed["how"]
                if placed["broke"] is not None:
                    counts["partial"] += 1
                counts["verified"] += 1; n["ok"] += 1
                real = os.path.relpath(fkey, files.root)
                kind = hop_incident(files, run, side, hi, path, ln, quotes, real, how, drift)
                if kind == "placed" and side is None:
                    n["miscopied" if quotes and got[0] + 1 == ln else "placed"].append((path, real))
                    counts["placed"] += 1
                followed = kind == "followed"
                if followed:
                    n["followed"].append((path, real))
                    counts["followed"] += 1
                a, b = got[0] + 1, got[-1] + 1
                ranges.append((fkey, a, b))
                n.setdefault("held", []).append((fkey, a, b))
                # the reader's own address for this hop: the stored one when the check moved it, the given one when the
                # write did; a cite the reader made at it moves with the hop (check_answer)
                given = {ln} if a != ln and quotes else set()
                gm = re.search(r":(\d+)(?:-\d+)?$", side[hi].get("given", "")) if side is not None else None
                if gm and int(gm.group(1)) != a:
                    given.add(int(gm.group(1)))
                files.readdressed += [(fkey, g, g + b - a, a) for g in given]
                shown = files.shown(fkey, path)
                if shown != path:
                    files.alias.setdefault(path, set()).add(fkey)
                flag = []
                if shown != path or followed:
                    flag.append(f"path resolved from {path}" + (f": {FOLLOWED_HOW}" if followed else f": {how}" if how else ""))
                if a != ln and quotes:
                    counts["moved"] += 1
                    flag.append(f"re-addressed from :{ln}")
                if tag != "exact":
                    flag.append(tag)
                wrote_placed = side is not None and placed_path(files.root, strip_line(side[hi].get("given", "")),
                                                                side[hi].get("path", ""), side[hi].get("how"))
                status = ("followed" if followed else "placed" if kind == "placed" or wrote_placed
                          else "moved" if a != ln and quotes else "verified")
                n["walk"].append({"t": "hop", "key": fkey, "line": a, "note": notes[0] if notes else "",
                                  "b": b, "shown": shown, "status": status, "flag": "; ".join(flag)})
                n["order"].append(None)
                flag = f"   ({'; '.join(flag)})" if flag else ""
                addr = f"{shown}:{a}" if a == b else f"{shown}:{a}-{b}"
                text = lines[a - 1].strip()
                short = text if len(text) <= 110 else text[:107] + "..."
                more = f"  (+{b - a} lines)" if b > a else ""
                n["hops"].append(f"{addr}  {short}{more}{flag}")
                if a == b:
                    n["full"].append(f"{addr}  {text}{flag}")
                else:
                    n["full"].append(f"{addr}{flag}")
                    n["full"] += [f"  {i + 1:>5}| {lines[i]}" for i in range(a - 1, b)]
                window = "\n".join(lines[max(0, a - 1 - NOTE_SPAN):min(len(lines), b + NOTE_SPAN)])
                for note in notes:
                    extra = window
                    for xm in XREF.finditer(note):
                        if glued(note, xm):
                            continue
                        ref, refln = xm.groups()
                        _, rl = files.find(ref)
                        if rl:
                            k = int(refln)
                            extra += "\n" + "\n".join(rl[max(0, k - 15):k + 15])
                    unseen = sorted({i for span in BACKTICK.findall(PLACEHOLDER.sub(" ", note))
                                     for i in IDENT.findall(span) if i.lower() not in KEYWORDS and i not in extra})
                    if unseen:
                        counts["unseen"] += 1
                    mark = f"   (? not near: {', '.join(unseen)})" if unseen else ""
                    n["hops"].append(f"  = {note}{mark}")
                    n["full"].append(f"  = {note}{mark}")
            else:
                text, addr = r[1], r[2]
                mark, cid = "", None
                if kind == "SPLIT":
                    n["split"] = True
                    m = re.match(r"SPLIT\s+([0-9][0-9.]*)", text, re.I)
                    cid = m.group(1).rstrip(".") if m else None
                    if cid and cid not in present:
                        if os.path.exists(os.path.join(run, cid + ".started")):
                            counts["missing"] += 1
                            mark = "   (✗ MISSING: this child started and wrote no file)"
                        else:
                            mark = "   (not spawned: no child ran under this ID)"
                if kind == "END":
                    n["ended"] = True
                if kind in ("LEAD", "NOT READ"):
                    n["unsettled"].append(text)
                if addr:
                    akey, lines = files.find(addr[0], prefer={w["key"] for w in n["walk"]})
                    if lines is None or not 1 <= addr[1] <= len(lines):
                        counts["badaddr"] += 1
                        mark += f"   (✗ BAD ADDRESS {addr[0]}:{addr[1]})"
                    elif kind == "SPLIT" and cid:
                        n["walk"].append({"t": "split", "key": akey, "line": addr[1], "child": cid})
                    elif kind == "END":
                        m = re.match(r"END\s+(\w+):?\s*(.*?)\s*@", text, re.I)
                        if m:
                            n["walk"].append({"t": "end", "key": akey, "line": addr[1], "kind": m.group(1).lower(),
                                              "note": m.group(2)})
                sym = {"SPLIT": "→", "END": "■", "LEAD": "·", "NOT READ": "·"}.get(kind, "")
                line = f"{sym} {text}{mark}".strip()
                n["struct"].append(line); n["hops"].append(line); n["full"].append(line)
        if not n["ended"] and not n["split"]:
            counts["noend"] += 1
            n["struct"].append("(no END line: where the thread ends is not stated)")
    return nodes, ranges


def span_parts(span):
    """A backticked span's literal pieces: `…`, `...`, `<placeholder>` and `{placeholder}` are gaps."""
    s = loose(span).strip("…. ")
    return [p.strip() for p in GAPS.split(s) if len(p.strip()) >= 4] or ([s] if len(s) >= 6 else [])


def span_line(lines, parts, near, reach=None):
    """The 0-based line nearest `near` holding every part (or, failing that, the first part), else None; with `reach`,
    only within that many lines of `near`."""
    lo, hi = (0, len(lines)) if reach is None else (max(0, near - reach), min(len(lines), near + reach + 1))
    for want in (parts, parts[:1]):
        hits = [i for i in range(lo, hi) if all(p in loose(lines[i]) for p in want)]
        if hits:
            return min(hits, key=lambda i: abs(i - near))
    return None


def check_answer(text, files, ranges):
    """One answer line, re-addressed: (text, flags, moves). A cite is backed by a verified hop or by a
    backticked span found at it; a span found elsewhere in a cited file moves an unbacked cite there, and a cite still
    unbacked within reach of the address its reader gave a hop the kit re-addressed moves with that hop."""
    found = list(CITE.finditer(text))
    if not found:
        return text, ["no [path:N] cite"], []
    cites = []
    for m in found:
        path, a, b = m.group(1), int(m.group(2)), int(m.group(3) or m.group(2))
        key, lines = files.find(path, prefer={k for k, _, _ in ranges})
        c = dict(m=m, path=path, a=a, b=b, key=key, lines=lines, new=None, ok=False, bad=None)
        if key:
            c["path"] = files.shown(key, path)
        if lines is None:
            c["bad"] = f"✗ no such file {path}"
        elif not 1 <= a <= len(lines):
            c["bad"] = f"✗ BAD ADDRESS {path}:{a}"
        else:
            c["ok"] = any(k == key and lo - CITE_SLACK <= a and b <= hi + CITE_SLACK for k, lo, hi in ranges)
        # a hop that held when walked and no longer verifies: the cite is stale, not unbacked
        c["drift"] = not c["ok"] and any(k == (key or files.key(path)) and lo - CITE_SLACK <= a and b <= hi + CITE_SLACK
                                         for k, lo, hi in files.drifts)
        cites.append(c)
    live = [c for c in cites if not c["bad"]]
    stale = "≈ {}:{} — a hop held it when walked; the file changed since: re-read before pasting"
    flags, moves, extra = [stale.format(c["path"], c["a"]) if c["drift"] else c["bad"] for c in cites if c["bad"]], [], []
    for span in BACKTICK.findall(text):
        parts = span_parts(span)
        if not parts or CITE.fullmatch(f"[{span}]"):
            continue
        at = [c for c in live if all(p in " ".join(loose(l) for l in c["lines"][max(0, c["a"] - 1 - CITE_SLACK):c["b"] + CITE_SLACK]) for p in parts)]
        if at:
            for c in at:
                c["ok"] = True
            continue
        best = None
        for c in live:
            i = span_line(c["lines"], parts, c["a"] - 1, files.reach(c["key"]))
            if i is not None and (best is None or abs(i + 1 - c["a"]) < abs(best[1] + 1 - best[0]["a"])):
                best = (c, i)
        if best is None:
            flags.append(f"? `{span[:50]}` not in the cited file")
            continue
        c, i = best
        if not c["ok"] and c["new"] is None:
            c["new"], c["ok"] = i + 1, True
            moves.append(f"↻ {c['path']}:{c['a']}→{i + 1}")
        else:
            extra.append(f"[{c['path']}:{i + 1}]")
            moves.append(f"↻ +{c['path']}:{i + 1}")
    for c in live:
        if c["ok"]:
            continue
        shift = next((at - lo for k, lo, hi, at in files.readdressed
                      if k == c["key"] and lo - CITE_SLACK <= c["a"] and c["b"] <= hi + CITE_SLACK), None)
        if shift is not None and 1 <= c["a"] + shift and c["b"] + shift <= len(c["lines"]):
            c["new"], c["ok"] = c["a"] + shift, True
            moves.append(f"↻ {c['path']}:{c['a']}→{c['new']}")
    for c in live:
        if not c["ok"]:
            flags.append(stale.format(c["path"], c["a"]) if c["drift"] else
                         f"unbacked {c['path']}:{c['a']} — no verified hop or quoted text there")
    out = text
    for c in reversed(cites):
        if c["new"] is not None or c["path"] != c["m"].group(1):
            line, span = c["new"] or c["a"], c["b"] - c["a"]
            addr = f"{c['path']}:{line}" + (f"-{line + span}" if span else "")
            out = out[:c["m"].start()] + f"[{addr}]" + out[c["m"].end():]
    if extra:
        out += " " + " ".join(extra)
    return out, flags, moves


def load(run, drift=True):
    """A run read and every hop verified: (run, root, names, files, nodes, ranges, counts). `drift` looks for the
    rejected hops that held when walked; a view that lists only verified hops does without."""
    run = os.path.abspath(run)
    try:
        root = open(os.path.join(run, "ROOT")).read().strip()
    except OSError as e:
        fail(f"{run} is not a tracetree run (no ROOT file: {e})")
    try:
        names = sorted((f[:-5] for f in os.listdir(run) if f.endswith(".hops")), key=node_key)
    except OSError as e:
        fail(f"cannot list {run}: {e}")
    if not names:
        fail(f"{run} holds no .hops file: no sub-tracer wrote one")
    files = Files(root)
    counts = counters(names)
    nodes, ranges = verify(run, files, names, counts, drift)
    return run, root, names, files, nodes, ranges, counts


def pinned_commit(files, path):
    """`{commit, 12 hex}:{path}` for a path pinned to a revision that names a commit; the path as given otherwise."""
    pin = files.pin(path)
    return f"{pin[0][:PIN_ABBREV]}:{pin[1]}" if pin and pin[0] else path


def placed_path(root, path, real, how):
    """Whether the kit placed a hop: the resolver re-addressed it (`how`), or the path it named is no tail of the file
    it landed in (an invented directory a later hop reaches through the first). A short path that ends the real path
    (`server.go`, `..` collapsed) is plain tail resolution, no incident. `real` is the file's path from ROOT."""
    if how in PLACED_HOW:
        return True
    if PINNED.match(real):
        return False
    norm = os.path.normpath(path)
    if os.path.isabs(norm):
        return norm != os.path.normpath(os.path.join(root, real))
    return not (real == norm or real.endswith("/" + norm))


def strip_line(given):
    """A sidecar's `path:line` given as the path alone."""
    return re.sub(r":\d+(?:-\d+)?$", "", given)


def miscopy(entry):
    """Whether a placed sidecar entry only miscopied its path: stored verified, its quote at the very line it cited."""
    m = re.search(r":(\d+)(?:-\d+)?$", entry.get("given", ""))
    return entry.get("status") == "verified" and m is not None and int(m.group(1)) == entry.get("line")


def count(k, word):
    """`1 hop`, `2 hops`."""
    return f"{k} {word}" if k == 1 else f"{k} {word}s"


def pair_list(pairs):
    """`given → path` for each distinct pair in order, the first PAIRS_SHOWN, then `+j more` for the rest."""
    seen = list(dict.fromkeys(pairs))
    shown = [f"{g} → {p}" for g, p in seen[:PAIRS_SHOWN]]
    return ", ".join(shown + ([f"+{len(seen) - PAIRS_SHOWN} more"] if len(seen) > PAIRS_SHOWN else []))


def glued(text, m):
    """Whether a path:line match is the tail of a revision-pinned name the kit did not store as its commit
    (`HEAD~1:a.go:3`, a 40-hex sha): never the working-tree file."""
    return m.start() > 0 and (text[m.start() - 1] in ":~^" or text[m.start() - 1].isalnum())


def rested_on(ans, files, nodes, own):
    """The nodes that broke the hop contract an answer line rests on: its own node, and for a cite no hop of its own
    node or of a sound node backs, every node whose verified hops do."""
    broken = {name for name, n in nodes.items() if broke_node(n)}
    got = {own} & broken
    for m in CITE.finditer(ans):
        a, b = int(m.group(2)), int(m.group(3) or m.group(2))
        key, _ = files.find(m.group(1))
        backers = {name for name, n in nodes.items() if any(k == key and lo - CITE_SLACK <= a and b <= hi + CITE_SLACK
                                                            for k, lo, hi in n.get("held", ()))}
        if own not in backers and backers <= broken:
            got |= backers
    return sorted(got, key=node_key)


def broke_node(n):
    """Whether a node broke the hop contract: it named a path that is not the file, stored hops that still do not hold
    after a refusal, or has REJECTED hops. A miscopied path alone breaks none."""
    return bool(n["placed"] or (n["refused"] and n["badnow"] > 0) or n["rej"])


def size_notes(files):
    """One `·` note per file the kit searched only near an address (past SEARCH_MAX_BYTES) and one per file it did not
    read (past READ_MAX_BYTES), each named once with its size."""
    return ([f"· {os.path.relpath(k, files.root)} is {megabytes(size)} — {big_limit()}"
             for k, size in sorted(files.big.items())]
            + [f"· {os.path.relpath(k, files.root)} is {megabytes(size)} — over the {READ_MAX_BYTES >> 20} MB read limit: "
               f"the kit reads none of it" for k, size in sorted(files.unread.items())])


def big_limit():
    return (f"past {SEARCH_MAX_BYTES >> 20} MB the kit checks a hop or cite only within {NEAR} lines of its address; "
            f"a quote further off is REJECTED, not searched for")


def incidents(loaded):
    """What broke the hop contract, from what the kit already has: {lines, notes, broke, drifted}. `lines` is the
    WARNINGS block (header first) and empty when nothing happened; `broke` counts the nodes with placed, refused or
    rejected hops. A placed hop whose quote sits at the very line it cited only miscopied its path: it breaks no
    contract and prints a `·` line in `notes`, after the block or after `WARNINGS: none`.
    The run's drifted hops print their own line and count no node as broken; followed hops (a file moved since the walk)
    print a run line only beside other lines and count no node as broken; flagged answers stay in COUNTS only, so a
    clean walk never warns. A
    sidecar that could not be read is a could-not-look line, including beside other incidents."""
    run, root, names, files, nodes, ranges, counts = loaded
    broke, details, notes = 0, [], []
    for name in names:
        n = nodes[name]
        refused = n["refused"] and n["badnow"] > 0
        broke += broke_node(n)
        if n["placed"]:
            details.append(f"⚠ node {name}: {count(len(n['placed']), 'hop')} named a path that is not the file — the kit "
                           f"placed them ({pair_list(n['placed'])})")
        if refused:
            details.append(f"⚠ node {name}: write refused once, then stored with {count(n['badnow'], 'hop')} that still "
                           f"do not hold")
        if n["rej"]:
            details.append(f"⚠ node {name}: {count(n['rej'], 'hop')} REJECTED")
        if n["miscopied"]:
            notes.append(f"· node {name}: the kit corrected a miscopied path on {count(len(n['miscopied']), 'hop')} — "
                         f"each quote sits at its cited line ({pair_list(n['miscopied'])})")
    notes += size_notes(files)
    if counts["drifted"]:
        details.append(f"⚠ run: {count(counts['drifted'], 'hop')} DRIFTED — the code changed since the walk: re-read "
                       f"before pasting")
    if details and counts["followed"]:
        moved = [pair for name in names for pair in nodes[name]["followed"]]
        details.append(f"⚠ run: {count(counts['followed'], 'hop')} followed files moved since the walk ({pair_list(moved)})")
    if details:
        if files.unknown:
            details.append(f"⚠ run: the kit could not look: {'; '.join(files.unknown)}")
        head = (f"⚠ WARNINGS — {broke} of {len(names)} nodes broke the hop contract; their answers rest on placed, "
                f"refused or rejected hops: drill before trusting them") if broke else "⚠ WARNINGS — no node broke the hop contract"
        return dict(lines=[head, *details], notes=notes, broke=broke, drifted=counts["drifted"])
    if files.unknown:
        return dict(lines=[f"⚠ WARNINGS — none found, but the kit could not look: {'; '.join(files.unknown)}"],
                    notes=notes, broke=0, drifted=0)
    return dict(lines=[], notes=notes, broke=0, drifted=0)


def warnings_block(inc):
    return (inc["lines"] or ["WARNINGS: none"]) + inc["notes"]


def map_flags(inc):
    """The MAP-line suffix: the nodes that broke the hop contract and the drifted hops."""
    return ((f" · ⚠ {count(inc['broke'], 'node')} broke the hop contract" if inc["broke"] else "")
            + (f" · ⚠ {inc['drifted']} drifted" if inc["drifted"] else ""))


def with_flags(text, suffix):
    """text with suffix at the end of its first line."""
    first, sep, rest = text.partition("\n")
    return first + suffix + sep + rest


def check_view(view, only, loaded):
    """The check view as data: ([{name, lines, hot}], COUNTS line). A node outside `only` still counts in COUNTS
    but has no block; `hot` holds a block's flagged answer lines, MISSING lines and NOT READ lines."""
    run, root, names, files, nodes, ranges, counts = loaded
    only = (only,) if isinstance(only, str) else tuple(only or ())
    blocks = []
    for name in names:
        shown = not only or any(name == o or name.startswith(o + ".") for o in only)
        if not shown and view != "answers":
            continue
        out, hot = [], []
        n, pad = nodes[name], "  " * name.count(".")
        out.append(f"{pad}[{name}] {n['head']}")
        if view == "answers":
            for ans in n["answers"]:
                counts["answers"] += 1
                ans, flags, moves = check_answer(ans, files, ranges)
                counts["flagged"] += bool(flags)
                counts["recited"] += bool(moves)
                notes = flags + moves
                out.append(f"{pad}  - {ans}" + (f"   ({'; '.join(notes)})" if notes else "")
                           + "".join(f" ⚠ node {b} broke the hop contract" for b in rested_on(ans, files, nodes, name)))
                if flags:
                    hot.append(out[-1])
            if not n["answers"]:
                out.append(f"{pad}  (no ANSWER block)")
            for st in n["struct"]:
                out.append(f"{pad}  {st}")
                if "✗ MISSING" in st or st.startswith("· NOT READ"):
                    hot.append(out[-1])
            out.append(f"{pad}  hops: {n['ok']} verified, {n['bad']} rejected" + (f", {n['drift']} drifted" if n["drift"] else ""))
        else:
            out += [f"{pad}  {st}" for st in n["hops" if view == "hops" else "full"]]
        if shown:
            blocks.append({"name": name, "lines": out, "hot": hot})
    unknown = [f"DRIFT UNKNOWN — {'; '.join(files.unknown)}; the rejected hops it could not look into stay REJECTED"] if files.unknown else []
    return blocks, "\n".join([*unknown, "COUNTS: " + " · ".join(f"{k}={v}" for k, v in counts.items())])


def cmd_check(run, view, only):
    loaded = load(run)
    inc = incidents(loaded)
    blocks, counts_line = check_view(view, only, loaded)
    print("\n".join([*warnings_block(inc), *[l for b in blocks for l in b["lines"]]]))
    print(counts_line)


READ_MAX = 30
BLOCK = re.compile(r"(.+?):(\d+)(?:-(\d+))?")


def cmd_read(run, blocks):
    """Print each FILE:A-B (FILE relative to the run's ROOT) numbered by the file's own lines. A block is refused on
    its own line when it is over READ_MAX lines, when one verified hop's quoted range holds it whole, or when the
    file or range is missing. One call per round: the second since the last `wait` fails whole."""
    run = os.path.abspath(run)
    try:
        root = open(os.path.join(run, "ROOT")).read().strip()
    except OSError as e:
        fail(f"{run} is not a tracetree run (no ROOT file: {e})")
    counter = os.path.join(run, ".reads")
    if os.path.exists(counter):
        fail("one read per round: put every block in one call")
    try:
        with open(counter, "w") as f:
            f.write("1\n")
        names = sorted((f[:-5] for f in os.listdir(run) if f.endswith(".hops") and NODE_ID.fullmatch(f[:-5])),
                       key=node_key)
    except OSError as e:
        fail(f"cannot count this read in {run}: {e}")
    files = Files(root)
    nodes, _ = verify(run, files, names, counters(names), drift=False)
    out = []
    for spec in blocks:
        got = read_block(files, root, spec, READ_MAX)
        if isinstance(got, str):
            out.append(got)
            continue
        label, key, a, b, lines = got
        holder = next((x for x in names for w in nodes[x]["walk"]
                       if w["t"] == "hop" and w["key"] == key and w["line"] <= a and b <= w["b"]), None)
        if holder:
            out.append(f"REFUSED {label} — node {holder} holds it: tt check {run} --full {holder}")
            continue
        out.append(f"== {label}")
        out += [f"{i:>6}\t{lines[i - 1]}" for i in range(a, b + 1)]
    print("\n".join(out))


def read_block(files, root, spec, most=None):
    """(label, key, A, B, lines) of one FILE:A-B block, or its `REFUSED` line: not FILE:A-B, over `most` lines, no
    readable file, or a range outside the file."""
    m = BLOCK.fullmatch(spec.strip("`"))
    if not m:
        return f"REFUSED {spec} — not FILE:A-B"
    path, a = m.group(1), int(m.group(2))
    b = int(m.group(3) or a)
    label = f"{path}:{a}-{b}"
    if most and b - a + 1 > most:
        return f"REFUSED {label} — over {most} lines"
    lines = files.lines(path)
    if lines is None:
        why = files.why_unread(path)
        return f"REFUSED {label} — " + (f"no such file under {root}" if why == "no such file" else why)
    count = len(lines) - (1 if lines and lines[-1] == "" else 0)
    if a < 1 or b < a or b > count:
        return f"REFUSED {label} — lines {a}-{b} are not in the file, which has {count} lines"
    return label, files.key(path), a, b, lines


def cmd_show(run, blocks):
    """The caller's read through the run: each FILE:A-B as the file holds it now, under `== FILE:A-B · held N/M`,
    every line a verified hop holds marked ` [{IDs}]` (hops re-addressed after edits), then each holding node's NOT
    READ and LEAD lines. Any size, any number of calls; a block it cannot read is refused on its own line."""
    run, root, names, files, nodes, _, _ = load(run)
    out = []
    for spec in blocks:
        got = read_block(files, root, spec)
        if isinstance(got, str):
            out.append(got)
            continue
        label, key, a, b, lines = got
        held = {}
        for name in names:
            for w in nodes[name]["walk"]:
                if w["t"] == "hop" and w["key"] == key and w["line"] <= b and a <= w["b"]:
                    for i in range(max(a, w["line"]), min(b, w["b"]) + 1):
                        held.setdefault(i, {})[name] = True
        out.append(f"== {label} · held {len(held)}/{b - a + 1}")
        out += [f"{i:>6}" + (f" [{','.join(held[i])}]" if i in held else "") + f"\t{lines[i - 1]}"
                for i in range(a, b + 1)]
        holders = sorted({name for ids in held.values() for name in ids}, key=node_key)
        out += [f"   open: {text} — run: node {name}" for name in holders for text in nodes[name]["unsettled"]]
    print("\n".join(out))


ELIDED = "quote every line whole, no elision"
FILE_HOP = "a hop cites a file line — name the file the listing points at"


def file_sha(path):
    """The sha256 of the file's bytes, None when it cannot be read."""
    try:
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    except OSError:
        return None


def bad_hop(placed):
    """Why a placed hop does not hold, None when it does: a rejected hop, an address past the file's end, a quote whose
    line stops matching at a `...`/`…`."""
    if placed["status"] == "rejected":
        if placed["elided"]:
            return ELIDED
        return FILE_HOP if placed["reason"].startswith("a directory") else placed["reason"]
    if placed["status"] == "badaddr":
        return placed["reason"]
    if placed["broke"] is not None and placed["elided"]:
        return ELIDED
    return None


def hop_hint(files, path, ln, placed):
    """What the writer needs to fix a bad hop, on one line: the file's own lines at the address when the path names a
    file, else the files with that basename, else that there are none."""
    key, lines = placed["key"], placed["lines"]
    if key and lines is not None:
        rel = os.path.relpath(key, files.root)
        if not 1 <= ln <= len(lines):
            return f"{rel} has {len(lines)} lines"
        shown = [f"{i}| {lines[i - 1].strip()[:110]}" for i in range(ln, min(len(lines), ln + 2) + 1)]
        return f"{rel} holds: " + " · ".join(shown)
    base = os.path.basename(os.path.normpath(path))
    near = [os.path.relpath(p, files.root) for p in files.named(base)]
    if near:
        return f"files named {base}: " + ", ".join(near[:5]) + (f" (+{len(near) - 5} more)" if len(near) > 5 else "")
    return f"no file named {base} under ROOT"


def cmd_write(run, name):
    """Store stdin as {RUN}/{ID}.hops, once: a second write of one ID is refused, so a node's file is final
    the moment it exists. A file missing THREAD, an ANSWER line, a hop or an END/SPLIT is refused unstored. Every
    hop is then placed against the files as they are (`place_hop`): the first write with a hop that does not hold
    stores nothing, names each such hop with a hint and leaves {ID}.refused; the next write stores the file. A stored
    file has each placed hop at its real path:line and a sidecar {ID}.write.json, one entry per hop in order."""
    if not NODE_ID.fullmatch(name):
        fail(f"ID {name!r} is not dotted numbers like 3 or 3.1")
    run = os.path.abspath(run)
    path = os.path.join(run, name + ".hops")
    try:
        root = open(os.path.join(run, "ROOT")).read().strip()
    except OSError as e:
        fail(f"{run} is not a tracetree run (no ROOT file: {e})")
    if os.path.exists(path):
        fail(f"{name}.hops is already written — a node writes once; reply with your one return line now")
    text = sys.stdin.read()
    recs = parse(text)
    kinds = [r[0] for r in recs]
    missing = [what for what, ok in (("a THREAD line", "THREAD" in kinds), ("an ANSWER line", "answer" in kinds),
                                     ("an `@ path:LINE` hop", "hop" in kinds),
                                     ("an END or SPLIT line", "END" in kinds or "SPLIT" in kinds)) if not ok]
    if missing:
        fail(f"not stored — the file lacks {', '.join(missing)}; fix it and run the same write again")
    files = Files(root)
    hop_recs = [r for r in recs if r[0] == "hop"]
    placed = [place_hop(files, r[1], r[2], r[3]) for r in hop_recs]
    bad = [(r, pl, why) for r, pl in zip(hop_recs, placed) for why in [bad_hop(pl)] if why]
    marker = os.path.join(run, name + ".refused")
    if bad and not os.path.exists(marker):
        try:
            open(marker, "w").close()
        except OSError as e:
            fail(f"cannot record the refusal of {name} in {run}: {e}")
        sys.stderr.write(f"TRACETREE REFUSED — {len(bad)} of {len(hop_recs)} hops do not hold; fix each, then write "
                         f"again (a second write stores the file as it is)\n")
        for r, pl, why in bad:
            sys.stderr.write(f"@ {r[1]}:{r[2]} — {why}; {hop_hint(files, r[1], r[2], pl)}\n")
        sys.exit(2)
    out, entries, readdressed = text.split("\n"), [], 0
    for r, pl in zip(hop_recs, placed):
        given = f"{r[1]}:{r[2]}"
        if pl["status"] in ("verified", "moved"):
            a, b = pl["got"][0] + 1, pl["got"][-1] + 1
            shown = files.shown(pl["key"], r[1])
            how = pl["how"] or ("exact" if shown == r[1] else "tail")
            entry = dict(given=given, path=os.path.relpath(pl["key"], root), line=a, end=b, how=how,
                         status=pl["status"], sha256=file_sha(pl["key"]))
            if shown != r[1] or a != r[2]:
                readdressed += 1
                span = f"-{r[6] + a - r[2]}" if r[6] else ""
                out[r[5]] = re.sub(r"(@\s*)\S+?:\d+(?:-\d+)?(\s*)$", lambda m: f"{m.group(1)}{shown}:{a}{span}{m.group(2)}",
                                   out[r[5]])
        else:
            entry = dict(given=given, path=r[1], line=r[2], end=r[2], how=None, status="rejected",
                         sha256=file_sha(pl["key"]) if pl["key"] else None)
        entries.append(entry)
    # a revision named by a ref (`HEAD~1`, a branch) moves: every pinned address, cite and range outside the quotes and
    # notes is stored as its commit
    out = [line if line.startswith(">") or line.lstrip().startswith("=")
           else PINREF.sub(lambda m: pinned_commit(files, m.group(1)), line) for line in out]
    # Claim the sidecar exclusively, finish it, then publish hops. A killed writer can leave metadata without a node,
    # never a node missing its metadata; a loser cannot replace the winner's entries.
    sidecar = os.path.join(run, name + ".write.json")
    try:
        claim = open(sidecar, "x", encoding="utf-8")
    except FileExistsError:
        fail(f"{name}.hops is already written or its metadata write is incomplete — a node writes once; inspect {sidecar}")
    except OSError as e:
        fail(f"cannot write {sidecar}: {e}")
    owned = [sidecar]
    try:
        with claim:
            json.dump(entries, claim, indent=1, ensure_ascii=False)
            claim.write("\n")
        with open(path, "x", encoding="utf-8") as f:
            owned.append(path)
            f.write("\n".join(out))
    except OSError as e:
        # Release only this writer's claims; a pre-existing hops file is never removed.
        for leftover in owned:
            try:
                os.remove(leftover)
            except FileNotFoundError:
                pass
            except OSError as e2:
                sys.stderr.write(f"TRACETREE WARNING — cannot remove {leftover}: {e2}\n")
        fail(f"cannot write {path} and its sidecar: {e}")
    notes = (f", {readdressed} re-addressed" if readdressed else "") + (f", {len(bad)} not holding stored as they are" if bad else "")
    print(f"stored {name}: {kinds.count('hop')} hops, {kinds.count('answer')} answer lines{notes} — {path}")
    moved = [(r[1], e["path"]) for r, e in zip(hop_recs, entries)
             if e["status"] != "rejected" and placed_path(root, r[1], e["path"], e["how"])]
    if moved:
        sys.stderr.write(f"⚠ WARNING — {len(moved)} of your hops named a path that is not the file; the kit placed them: "
                         f"{pair_list(moved)}. Copy paths as your read printed them.\n")
    if bad:
        sys.stderr.write(f"⚠ WARNING — stored with {count(len(bad), 'hop')} that do not hold; your tracer sees them REJECTED "
                         f"and is warned\n")


def question_numbers(question):
    """Every question number a card's QUESTION names, in the order it names them, each once: the numbers of
    `Q1 — why`, `Q1 and Q2 (also Q6)`. A QUESTION that does not open with a Q{n} names none."""
    if not re.match(r"\s*[Qq]\d+\b", question):
        return []
    got = []
    for m in re.finditer(r"\b[Qq](\d+)\b", question):
        if int(m.group(1)) not in got:
            got.append(int(m.group(1)))
    return got


def question_line(run, question):
    """One `QUESTION Q{n}: {text}` line per question number a card's QUESTION names, the text from {RUN}/questions;
    none for a QUESTION that names no number. A number the file lacks and a file that cannot be read are each named,
    never left out."""
    nums = question_numbers(question)
    if not nums:
        return None
    path = os.path.join(run, "questions")
    try:
        with open(path, encoding="utf-8") as f:
            got = parse_questions(f.read())
    except FileNotFoundError:
        return "\n".join(f"QUESTION Q{n}: (not in {path})" for n in nums)
    except OSError as e:
        return "\n".join(f"QUESTION Q{n}: (cannot read {path}: {e})" for n in nums)
    return "\n".join(f"QUESTION Q{n}: {got[n]}" if n in got else f"QUESTION Q{n}: (not in {path})" for n in nums)


def cmd_start(run, name, at, question=""):
    """Register node ID as running ({ID}.started, holding `FROM<tab>QUESTION` for the map page) and print its FROM line
    with the 40 lines after it, numbered; above them each question the card's QUESTION opens by naming (Q{n})."""
    if not NODE_ID.fullmatch(name):
        fail(f"ID {name!r} is not dotted numbers like 3 or 3.1")
    run = os.path.abspath(run)
    try:
        root = open(os.path.join(run, "ROOT")).read().strip()
    except OSError as e:
        fail(f"{run} is not a tracetree run (no ROOT file: {e})")
    try:
        with open(os.path.join(run, name + ".started"), "a", encoding="utf-8") as f:
            f.write(f"{at}\t{question}\n")
    except OSError as e:
        fail(f"cannot register {name} in {run}: {e}")
    m = re.fullmatch(r"(.+?):(\d+)(?:-\d+)?", at.strip("`"))
    if not m:
        fail(f"FROM {at!r} is not path:LINE")
    files = Files(root)
    key, lines = files.find(m.group(1))
    if lines is None:
        why = files.why_unread(m.group(1))
        fail(f"FROM file {m.group(1)} is not under {root} (or names several files)" if why == "no such file"
             else f"FROM file {m.group(1)}: {why}")
    ln = int(m.group(2))
    asked = question_line(run, question)
    if asked:
        print(asked)
    print(f"started {name} — {os.path.relpath(key, root)}")
    if files.reach(key):
        print(f"{os.path.relpath(key, root)} is {megabytes(files.big[key])} — {big_limit()}")
    for i in range(max(1, ln - 3), min(len(lines), ln + 40) + 1):
        print(f"{i:>6}\t{lines[i - 1]}")


def cmd_wait(run, ids, timeout, grace, start):
    """Block until every named node and every node that registered with `start` has written its file, and no
    file changed for `grace` seconds; then print the answers view. A named node that has neither started nor
    written after `start` seconds never ran — the harness drops a spawn past its depth or concurrency cap
    without a word — so it is reported, never waited for."""
    run, t0 = os.path.abspath(run), time.time()
    try:
        os.remove(os.path.join(run, ".reads"))
    except FileNotFoundError:
        pass
    except OSError as e:
        fail(f"cannot reset the read count of {run}: {e}")
    while True:
        try:
            entries = os.listdir(run)
        except OSError as e:
            fail(f"cannot list {run}: {e}")
        have = {f[:-5]: os.path.getmtime(os.path.join(run, f)) for f in entries if f.endswith(".hops")}
        started = {f[:-8] for f in entries if f.endswith(".started")}
        elapsed = time.time() - t0
        took = int(elapsed)
        unstarted = sorted(set(ids) - started - set(have), key=node_key) if elapsed >= start else []
        want = (set(ids) - set(unstarted)) | started
        never = f" — never started: {', '.join(unstarted)}" if unstarted else ""
        stamps = list(have.values()) + [os.path.getmtime(os.path.join(run, f)) for f in entries if f.endswith(".started")]
        missing = sorted(want - set(have), key=node_key)
        quiet = time.time() - max(stamps, default=t0)
        if not want and not have:
            head = f"WAIT ended after {took}s: no node started{never}"
            break
        if not missing and have and quiet >= grace:
            head = f"WAIT done after {took}s: {len(have)} files{never}"
            break
        if elapsed >= timeout:
            head = f"WAIT TIMEOUT after {took}s — started but never wrote: {', '.join(missing) or 'none'}{never}"
            break
        time.sleep(min(2.0, max(0.2, grace / 5)))
    full = head
    if have:
        loaded = load(run)
        inc = incidents(loaded)
        warn = warnings_block(inc)
        blocks, counts_line = check_view("answers", ids, loaded)
        opened = open_block(loaded, [b["name"] for b in blocks])
        map_line = write_map(loaded, inc)
        full = "\n".join([head, *warn, *[l for b in blocks for l in b["lines"]], counts_line, map_line, *opened])
    path = os.path.join(run, "answers.txt")
    try:
        with open(path, "w", encoding="utf-8") as f:
            f.write(full + "\n")
    except OSError as e:
        print(f"ANSWERS FAILED — {path} not written: {type(e).__name__}: {e}")
        print(full)
        return
    print(full if len(full) < CEILING else ceiling_view(run, head, warn, blocks, counts_line, map_line, opened))


def ceiling_view(run, head, warn, blocks, counts_line, map_line, opened):
    """The wait view cut to CEILING characters: the WARNINGS block whole, COUNTS, every flagged, MISSING and NOT READ line, OPEN, MAP, then each
    node's lines trimmed to its share, `+{k} lines: {RUN}/answers.txt` after a trimmed node, and the FULL line."""
    path = os.path.join(run, "answers.txt")
    last = f"FULL {path}"

    def capped(lines, room, keep):
        """lines cut to room characters after the first `keep` lines, the cut named."""
        out, used = list(lines[:keep]), sum(len(l) + 1 for l in lines[:keep])
        for i, l in enumerate(lines[keep:], keep):
            note = f"+{len(lines) - i} more lines: {path}"
            if used + len(l) + 1 + len(note) + 1 > room:
                return out + [note]
            out.append(l)
            used += len(l) + 1
        return out

    hot = capped([l for b in blocks for l in b["hot"]], CEILING * 35 // 100, 0)
    opened = capped(opened, CEILING * 25 // 100, 1)
    top = [head, *warn, counts_line, *hot, *opened, map_line]
    room = CEILING - 1 - sum(len(l) + 1 for l in top) - len(last)
    sizes = {b["name"]: sum(len(l) + 1 for l in b["lines"]) for b in blocks}
    share, left = {}, len(blocks)
    for name in sorted(sizes, key=lambda x: sizes[x]):
        share[name] = min(sizes[name], max(room, 0) // left)
        room -= share[name]
        left -= 1
    body = []
    for b in blocks:
        if share[b["name"]] >= sizes[b["name"]]:
            body += b["lines"]
            continue
        kept, used = b["lines"][:1], len(b["lines"][0]) + 1
        for l in b["lines"][1:]:
            note = f"+{len(b['lines']) - len(kept)} lines: {path}"
            if used + len(l) + 1 + len(note) + 1 > share[b["name"]]:
                break
            kept.append(l)
            used += len(l) + 1
        body += [*kept, f"+{len(b['lines']) - len(kept)} lines: {path}"]
    text = "\n".join([*top, *body, last])
    if len(text) >= CEILING:
        # the heads alone outgrew the ceiling: cut the middle, never the COUNTS, MAP or FULL lines
        text = text[:CEILING - 1 - len(last) - 1].rsplit("\n", 1)[0] + "\n" + last
    return text


def held_marks(text, files, nodes, held_keys):
    """The ` (held: node {ID} verified {path}:{lines})` marks of a line naming line ranges that verified hops of the
    run hold, one per node and file, and the start offsets of its `path:A-B` ranges no hop holds."""
    marks, unheld = {}, set()
    for m in SPANS.finditer(text):
        if glued(text, m):
            continue
        ref = m.group(1)
        if m.group(2):
            want = [(int(m.group(2)), int(m.group(3)))]
        else:
            want = [(int(a), int(b or a)) for a, b in re.findall(r"(\d+)(?:-(\d+))?", m.group(4))]
        key, _ = files.find(ref, prefer=held_keys)
        hit = False
        for name, n in nodes.items():
            for w in n["walk"]:
                if w["t"] == "hop" and w["key"] == key and any(w["line"] <= hi and lo <= w["b"] for lo, hi in want):
                    shown = os.path.relpath(key, files.root)
                    marks.setdefault((name, shown), set()).add((w["line"], w["b"]))
                    hit = True
        if m.group(2) and not hit:
            unheld.add(m.start())
    text_marks = [f" (held: node {name} verified {shown}:"
                  + ",".join(str(a) if a == b else f"{a}-{b}" for a, b in sorted(spans)) + ")"
                  for (name, shown), spans in sorted(marks.items(), key=lambda x: (node_key(x[0][0]), x[0][1]))]
    return "".join(text_marks), unheld


def open_block(loaded, listed):
    """OPEN, then every NOT READ and LEAD line of the listed nodes, each ending ` — run: node {ID}`; when a
    path:line in it is held by no verified hop of the whole run, ` (grep only — read it)`; when a line range it names
    (`path:A-B`, `path lines A-B`) holds verified hops of any node, ` (held: node {ID} verified {path}:{lines})` — a
    marker, the line stays. `OPEN: none` when empty."""
    _, _, _, files, nodes, ranges, _ = loaded
    held_keys = {k for k, _, _ in ranges}
    out = []
    for name in listed:
        for text in nodes[name]["unsettled"]:
            held, unheld = held_marks(text, files, nodes, held_keys)
            ranged = {m.start() for m in SPANS.finditer(text) if m.group(2)}
            grep_only = bool(unheld)
            for m in XREF.finditer(text):
                if m.start() in ranged or glued(text, m):
                    continue
                key, _ = files.find(m.group(1))
                if not key or not any(k == key and lo <= int(m.group(2)) <= hi for k, lo, hi in ranges):
                    grep_only = True
            out.append(f"{text} — run: node {name}" + (" (grep only — read it)" if grep_only else "") + held)
    return ["OPEN", *out] if out else ["OPEN: none"]


def write_map(loaded, inc):
    """RUN/map.txt, the text map, after every wait; returns the MAP line. A map that cannot be built is named,
    never silently absent."""
    run, root, names, files, nodes = loaded[:5]
    path = os.path.join(run, "map.txt")
    try:
        g = tracemap.build(run, root, names, nodes, files)
        with open(path, "w", encoding="utf-8") as f:
            f.write(with_flags(tracemap.text(g), map_flags(inc)))
    except (OSError, ValueError, KeyError, re.error) as e:
        return f"MAP FAILED — {path} not written: {type(e).__name__}: {e}"
    return f"MAP {path} — {tracemap.summary(g)}{map_flags(inc)}"


PAGE_TEXT_MAX = 4 * 1024 * 1024
PAGE_CONTEXT = 8
BROKEN = re.compile(r"(≈ DRIFTED|✗ REJECTED|✗ BAD ADDRESS) (\S+?):(\d+) — ")
BROKEN_STATUS = {"≈ DRIFTED": "drifted", "✗ REJECTED": "rejected", "✗ BAD ADDRESS": "badaddr"}


def run_file(run, name, read=False):
    """A run file's mtime, or its text with read set; None when it cannot be read — a missing time stays missing."""
    path = os.path.join(run, name)
    try:
        if not read:
            return os.path.getmtime(path)
        with open(path, encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return None


def page_data(loaded, data, text=True):
    """What the map page draws beside the map: the questions, BASE, the run's clock, COUNTS with the answers checked,
    each node's checked answers (each with the nodes that broke the hop contract it rests on, as the answers view
    marks them), structure lines, its hops in order as `steps` (a broken one a hole with no component) and broken hops, the nodes that started and never wrote, and with
    `text` the text of every file a shown component sits in — whole, the files with the most hops first, until
    PAGE_TEXT_MAX bytes; past it only the lines within PAGE_CONTEXT of a hop. A node's `questions` are every Q number
    its QUESTION names, `question` the first. Times are mtimes: `new` wrote ROOT, `start` {ID}.started, `write`
    {ID}.hops, `wait` answers.txt; one that cannot be read is null."""
    run, root, names, files, nodes, ranges, counts = loaded
    counts = dict(counts)
    out = {}
    for name in names:
        n = nodes[name]
        rec = (run_file(run, name + ".started", read=True) or "").split("\n")[0].split("\t")
        nums = question_numbers(rec[1]) if len(rec) > 1 else []
        answers = []
        for ans in n["answers"]:
            text, flags, moves = check_answer(ans, files, ranges)
            counts["answers"] += 1
            counts["flagged"] += bool(flags)
            counts["recited"] += bool(moves)
            answers.append({"text": text, "flags": flags, "moves": moves, "broke": rested_on(text, files, nodes, name)})
        broken = [{"status": BROKEN_STATUS[m.group(1)], "path": m.group(2), "line": int(m.group(3)), "text": line}
                  for line in n["hops"] for m in [BROKEN.match(line)] if m]
        walk = list(data["threads"].get(name, {}).get("walk", []))
        steps = []
        for hole in n["order"]:
            if hole:
                steps.append({"path": hole["path"], "c": None, "a": hole["a"], "b": hole["a"], "st": hole["st"], "ln": None,
                              "note": "", "flag": hole["flag"]})
            elif walk:
                w = walk.pop(0)
                steps.append({"path": w["c"].split("::")[0], "c": w["c"], "a": w["line"], "b": w["b"], "st": w["status"],
                              "ln": w["link"], "note": w["note"], "flag": w["flag"]})
        out[name] = {"thread": data["threads"].get(name, {}).get("thread", n["head"]),
                     "question": nums[0] if nums else None, "questions": nums, "from": rec[0] or None,
                     "started": run_file(run, name + ".started"), "written": run_file(run, name + ".hops"),
                     "answers": answers, "struct": n["struct"], "verified": n["ok"], "broken": broken, "steps": steps,
                     "placed": [list(p) for p in n["placed"]],
                     "miscopied": [list(p) for p in n["miscopied"]], "followed": [list(p) for p in n["followed"]],
                     "refused": n["refused"]}
    try:
        entries = os.listdir(run)
    except OSError as e:
        fail(f"cannot list {run}: {e}")
    missing = {f[:-8]: run_file(run, f) for f in entries
               if f.endswith(".started") and NODE_ID.fullmatch(f[:-8]) and f[:-8] not in out}
    hops = {}
    for c in data["components"]:
        hops.setdefault(c["file"], set()).update(c["hops"])
    for th in data["threads"].values():
        for s in th["walk"]:
            hops.setdefault(s["c"].split("::")[0], set()).update(range(s["line"], s["b"] + 1))
    shown, spent = [], 0
    for rel in sorted(hops, key=lambda r: (-len(hops[r]), r)) if text else ():
        lines = files.lines(rel)
        if lines is None:
            continue
        lines = lines[:-1] if lines and lines[-1] == "" else lines
        size = sum(len(l) + 1 for l in lines)
        entry = {"path": rel, "count": len(lines), "lines": None, "sparse": None}
        if spent + size <= PAGE_TEXT_MAX:
            entry["lines"], spent = lines, spent + size
        else:
            near = {i for h in hops[rel] for i in range(h - PAGE_CONTEXT, h + PAGE_CONTEXT + 1) if 1 <= i <= len(lines)}
            entry["sparse"] = {str(i): lines[i - 1] for i in sorted(near)}
        shown.append(entry)
    base = (run_file(run, "BASE", read=True) or "").strip() or None
    return {"title": os.path.basename(root.rstrip("/")) or root, "base": base,
            "times": {"new": run_file(run, "ROOT"), "done": run_file(run, "answers.txt")},
            "questions": {str(k): v for k, v in parse_questions(run_file(run, "questions", read=True) or "").items()},
            "counts": counts, "nodes": out, "missing": missing, "files": shown}


def cmd_map(run, opts):
    loaded = load(run)
    run, root, names, files, nodes = loaded[:5]
    inc = incidents(loaded)
    g = tracemap.build(run, root, names, nodes, files)
    show_all = opts["--all"]
    try:
        if opts["--html"]:
            import loom
            import tracemap_html
            out = os.path.abspath(opts["--html"])
            data = dict(tracemap.data(g, True, None), warnings=inc["lines"], notes=inc["notes"])
            named = opts["--from"] or opts["--to"]
            if opts["--from"] and opts["--to"]:
                raise tracemap.MapError("--html centres the hourglass on one component: give --from or --to, not both")
            hits = tracemap.resolve(g, named) if named else [None]
            if len(hits) > 1:
                raise tracemap.MapError(f"{named} matches {len(hits)} components ({', '.join(hits)}) — name one by its id")
            focal = hits[0]
            tracemap_html.write(loom.model(os.path.basename(run), root, data, page_data(loaded, data), inc, focal, show_all), out)
            print(f"HTML {out} — open it in a browser")
        elif opts["--json"]:
            import json
            print(json.dumps(dict(tracemap.data(g, show_all, tracemap.focus(g, opts["--from"], opts["--to"], show_all)),
                                  warnings=inc["lines"], notes=inc["notes"]), indent=1, ensure_ascii=False))
        elif opts["--from"] or opts["--to"]:
            parts = [tracemap.tree(g, opts[k], k[2:], show_all) for k in ("--from", "--to") if opts[k]]
            sys.stdout.write("\n".join(parts))
        else:
            sys.stdout.write(with_flags(tracemap.text(g, show_all), map_flags(inc)))
    except (tracemap.MapError, ValueError) as e:
        fail(str(e))
    except OSError as e:
        fail(f"cannot write {opts['--html']}: {e}")


RUN_ID = re.compile(r"\d{8}-\d{6}-[0-9a-f]{4}")
BOOK_COLUMNS = (("RUN", "id"), ("REPO", "repo"), ("Q", "q"), ("TIME", "dur"), ("NODES", "nodes"), ("HOPS", "hops"),
                ("HELD", "verified"), ("MOVED", "moved"), ("WORN", "drifted"), ("HOLES", "rejected"),
                ("CLAIMS", "answers"), ("FLAGGED", "flagged"))


def swatch(run):
    """One run's swatch: its id, repository, questions, running time and every count the Loom's swatch weaves from.
    Raises Unreadable with the kit's own reason when the run cannot be loaded; a symlinked run, or a run holding
    anything but files and directories (a FIFO would block the read), is refused before any read."""
    if os.path.islink(run):
        raise Unreadable("a symlink, not a run directory")
    try:
        odd = [e.name for e in os.scandir(run) if not e.is_file(follow_symlinks=False) and not e.is_dir(follow_symlinks=False)]
    except OSError as e:
        raise Unreadable(f"{type(e).__name__}: {e}")
    if odd:
        raise Unreadable(f"{min(odd)} is not a regular file")
    err = io.StringIO()
    with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(err):
        try:
            loaded = load(run)
            inc = incidents(loaded)
            g = tracemap.build(loaded[0], loaded[1], loaded[2], loaded[4], loaded[3])
            data = tracemap.data(g, True, None)
            pd = page_data(loaded, data, text=False)
        except SystemExit:
            why = [l for l in err.getvalue().splitlines() if l.strip()]
            raise Unreadable(re.sub(r"^TRACETREE FAILED — ", "", why[-1]) if why else "the kit could not load it")
        except Exception as e:  # any run the kit cannot load is a listed reason, never the end of the book
            raise Unreadable(f"{type(e).__name__}: {e}")
    root, c, t0 = loaded[1], pd["counts"], pd["times"]["new"] or 0
    done = pd["times"]["done"]
    return {"id": os.path.basename(run), "repo": os.path.basename(root.rstrip("/")) or root, "live": os.path.isdir(root),
            "q": len(pd["questions"]), "dur": round(done - t0, 1) if done else None, "broke": inc["broke"],
            "comps": len(data["components"]), "edges": len(data["edges"]),
            **{k: c.get(k, 0) for k in ("nodes", "hops", "verified", "moved", "drifted", "rejected", "placed", "followed",
                                        "refused", "partial", "answers", "flagged", "recited", "badaddr", "missing")}}


class Unreadable(Exception):
    """A run the kit cannot load: the message is its reason, listed beside the book, never dropped."""


def book_text(root, rows, failed):
    """The book as a plain table, newest first, the runs the kit could not load among them with their reason."""
    if not rows and not failed:
        return f"book: no runs under {root}\n"

    def cell(row, key):
        if key == "dur":
            return "-" if row["dur"] is None else f"{row['dur']:g}s"
        return str(row[key])

    table = [[h for h, _ in BOOK_COLUMNS]] + [[cell(r, k) for _, k in BOOK_COLUMNS] for r in rows]
    width = [max(len(row[i]) for row in table) for i in range(len(BOOK_COLUMNS))]
    lines = ["  ".join(c.ljust(w) for c, w in zip(row, width)).rstrip() for row in table]
    entries = list(zip((r["id"] for r in rows), lines[1:])) + [(f["id"], f"{f['id']}  UNREADABLE — {f['why']}") for f in failed]
    out = [f"BOOK {root} — {len(entries)} runs, newest first", lines[0]]
    out += [text for _, text in sorted(entries, key=lambda e: e[0], reverse=True)]
    return "\n".join(out) + "\n"


def cmd_book(opts):
    """Every run under this project's runs root, newest first, as one swatch row each; an unloadable run is listed with
    its reason. With --html OUT the swatch-book page is written instead of the table."""
    root = os.path.join("/tmp", project_name(os.getcwd()), "tracer")
    try:
        ids = sorted((d for d in os.listdir(root) if RUN_ID.fullmatch(d) and os.path.isdir(os.path.join(root, d))),
                     reverse=True)
    except FileNotFoundError:
        ids = []
    except OSError as e:
        fail(f"cannot list {root}: {e}")
    rows, failed = [], []
    for rid in ids:
        try:
            rows.append(swatch(os.path.join(root, rid)))
        except Unreadable as e:
            failed.append({"id": rid, "why": str(e)})
    if opts["--html"]:
        import tracemap_html
        out = os.path.abspath(opts["--html"])
        try:
            tracemap_html.write_book({"root": root, "book": rows, "failed": failed}, out)
        except OSError as e:
            fail(f"cannot write {opts['--html']}: {e}")
        print(f"HTML {out} — open it in a browser")
    else:
        sys.stdout.write(book_text(root, rows, failed))


def timed_args(args, defaults):
    """`wait`'s arguments: ({--timeout, --grace, --start} seconds, each defaulted), the rest in order."""
    opts, rest, i = dict(defaults), [], 0
    while i < len(args):
        if args[i] in opts and i + 1 < len(args):
            try:
                opts[args[i]] = float(args[i + 1])
            except ValueError:
                fail(f"{args[i]} needs a number of seconds, got {args[i + 1]!r}")
            i += 2
        else:
            rest.append(args[i])
            i += 1
    return opts, rest


RECALL_TOKEN = re.compile(r"[A-Za-z_][\w]*(?:\.[A-Za-z_][\w]*)*")
RECALL_PATH = re.compile(r"[\w.~-]*/[\w./-]*\w|[\w-]+\.(?:go|py|sh|mjs|js|ts|tsx|md|json|jsonc|toml|ya?ml|txt|rs|java|c|h|sql)\b")
FILE_EXT = {"go", "py", "sh", "mjs", "js", "ts", "tsx", "md", "json", "jsonc", "toml", "yaml", "yml", "txt", "rs", "java",
            "c", "h", "sql"}
RECALL_SHOWN = 30
DELTA_ROWS = 5
RECALL_CAP = 12 * 1024
SHELL_VAR = re.compile(r"\$[A-Za-z_]\w*")
CLOSED_WORLD = re.compile(r"(?i)\b(?:nothing|no|none|only|every|never|all callers|"
                          r"(?:\d+|one|two|three|four|five|six|seven|eight|nine|ten) callers?)\b")
REV_PIN = re.compile(r"^[0-9a-f]{7,40}:")


class Recall:
    """`recall`: one pass over the earlier runs of ROOT's repository, its git lookups cached for the pass."""

    def __init__(self, root):
        self.root = root
        self.files = Files(root)
        self.commons, self.placed, self.witnessed, self.shown_base, self.shas = {}, {}, {}, {}, {}

    def common(self, path):
        """The absolute git common dir of the repository holding path, asked from its nearest existing ancestor so a
        removed worktree answers through the directory it lived in; None outside git. GitUnknown when git cannot say."""
        if path not in self.commons:
            at = path
            while not os.path.isdir(at) and os.path.dirname(at) != at:
                at = os.path.dirname(at)
            try:
                done = subprocess.run(["git", "-C", at, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                                      capture_output=True, text=True, timeout=GIT_TIMEOUT)
            except (OSError, subprocess.SubprocessError) as e:
                raise GitUnknown(f"git rev-parse could not run in {at}: {type(e).__name__}: {e}")
            if done.returncode != 0 and "not a git repository" not in done.stderr:
                raise GitUnknown(f"git rev-parse failed in {at}: {done.stderr.strip()[:120]}")
            self.commons[path] = os.path.realpath(done.stdout.strip()) if done.returncode == 0 else None
        return self.commons[path]

    def sha(self, path):
        """The sha256 of ROOT's file at path now, None when it is gone or cannot be read."""
        if path not in self.shas:
            self.shas[path] = file_sha(os.path.join(self.root, path))
        return self.shas[path]

    def place(self, path, line, quotes):
        """(status, new path, new line, reason) of a prior span placed against ROOT now: FRESH at its line, MOVED
        elsewhere, STALE with the reason the placement gives, FAILED when git could not say."""
        k = (path, line, tuple(quotes))
        if k not in self.placed:
            try:
                got = place_hop(self.files, path, line, quotes)
            except GitUnknown as e:
                self.placed[k] = ("FAILED", path, line, f"git could not say: {e}")
                return self.placed[k]
            if got["status"] in ("rejected", "badaddr"):
                self.placed[k] = ("STALE", path, line, f"{path}:{line} {got['reason']}")
            elif got["broke"] is not None:
                self.placed[k] = ("STALE", path, line, f"{path}:{line} changed after its first {got['broke']} line(s)")
            else:
                now = self.files.shown(got["key"], path)
                same_place = got["got"][0] + 1 == line and got["key"] == self.files.key(path)
                self.placed[k] = ("FRESH" if same_place else "MOVED", now, got["got"][0] + 1, "")
        return self.placed[k]

    def base_lines(self, base, path):
        """The lines of path at commit base, or the reason git cannot show them."""
        k = (base, path)
        if k not in self.shown_base:
            try:
                code, out, err = git_out(self.files, ["show", f"{base}:./{path}"])
                self.shown_base[k] = out.split("\n") if code == 0 else f"git show {base[:PIN_ABBREV]}:{path} failed: {err.strip()[:120]}"
            except GitUnknown as e:
                self.shown_base[k] = f"git could not say: {e}"
        return self.shown_base[k]

    def changes(self, base):
        """[(sign, path, line, text)] of every line added (+) or removed (-) in ROOT's working tree since commit base,
        each untracked file's lines added; a str naming the failure when git cannot answer."""
        if base not in self.witnessed:
            self.witnessed[base] = self.diff(base)
        return self.witnessed[base]

    def diff(self, base):
        try:
            code, out, err = git_out(self.files, ["-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff",
                                                  "--no-renames", "-U0", base, "--"])
            if code != 0:
                return f"git diff {base[:PIN_ABBREV]} failed: {err.strip()[:120]}"
            code, untracked, err = git_out(self.files, ["ls-files", "-o", "--exclude-standard", "-z"])
            if code != 0:
                return f"git ls-files of the untracked files failed: {err.strip()[:120]}"
        except GitUnknown as e:
            return f"git could not say: {e}"
        rows, old_path, new_path, hunk, old, new = [], None, None, False, 0, 0
        for raw in out.split("\n"):
            if raw.startswith("diff --git "):
                hunk = False
            elif not hunk and raw.startswith("--- "):
                old_path = raw[6:] if raw.startswith("--- a/") else None
            elif not hunk and raw.startswith("+++ "):
                new_path = raw[6:] if raw.startswith("+++ b/") else None
            elif raw.startswith("@@ "):
                m = re.match(r"@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@", raw)
                hunk, old, new = True, int(m.group(1)), int(m.group(2))
            elif hunk and raw.startswith("+") and new_path:
                rows.append(("+", new_path, new, raw[1:].strip()))
                new += 1
            elif hunk and raw.startswith("-") and old_path:
                rows.append(("-", old_path, old, raw[1:].strip()))
                old += 1
        for path in filter(None, untracked.split("\0")):
            try:
                with open(os.path.join(self.root, path), "rb") as f:
                    data = f.read()
            except OSError as e:
                return f"cannot read untracked {path}: {e}"
            if b"\0" not in data:
                rows += [("+", path, i, t.strip()) for i, t in enumerate(data.decode("utf-8", "replace").split("\n"), 1)]
        return rows

    def delta(self, sym, base):
        """(added, gone) occurrences [(path, line, text)] of sym as a whole word since commit base, as a multiset of
        (path, text): a line removed and added back unchanged is no occurrence change. A str when git cannot say."""
        rows = self.changes(base)
        if isinstance(rows, str):
            return rows
        word, seen = re.compile(r"(?<!\w)" + re.escape(sym) + r"(?!\w)"), {}
        for sign, path, n, text in rows:
            if sym in text and word.search(text):
                seen.setdefault((path, text), {"+": [], "-": []})[sign].append(n)
        added, gone = [], []
        for (path, text), ns in seen.items():
            k = len(ns["+"]) - len(ns["-"])
            added += [(path, n, text) for n in ns["+"][len(ns["+"]) - k:]] if k > 0 else []
            gone += [(path, n, text) for n in ns["-"][len(ns["-"]) + k:]] if k < 0 else []
        return sorted(added), sorted(gone)


def recall_anchors(question):
    """(anchors, identifiers) of a question: backticked text, path-like tokens and identifiers (CamelCase,
    snake_case, dotted, a file name excepted), backticked or not; an identifier also witnesses the lines it matches."""
    anchors, idents = set(), set()
    for span in BACKTICK.findall(question):
        s = span.strip()
        if len(s) >= 3 and s.lower() not in KEYWORDS:
            anchors.add(s)
    for m in RECALL_PATH.finditer(question):
        if len(m.group(0)) >= 4 and not m.group(0).startswith("/"):
            anchors.add(m.group(0))
    for m in RECALL_TOKEN.finditer(question):
        t = m.group(0)
        if len(t) < 4 or t.rsplit(".", 1)[-1] in FILE_EXT:
            continue
        if "." in t or re.search(r"[A-Za-z0-9]_[A-Za-z0-9]", t) or re.search(r"[a-z0-9][A-Z]", t):
            anchors.add(t)
            idents.add(t)
    return anchors, idents


def line_witnesses(text):
    """(named, cited): the identifiers and path tokens a recalled line names in its prose — identifiers (CamelCase,
    snake_case, dotted), `$VAR`s, a backticked single token, paths — and its cites' paths besides; each one's
    occurrence set witnesses the line."""
    prose = CITE.sub(" ", text)
    named = {m.group(0) for m in SHELL_VAR.finditer(prose)} | recall_anchors(prose)[1]
    for span in BACKTICK.findall(prose):
        s = span.strip()
        if len(s) >= 2 and s.lower() not in KEYWORDS and re.fullmatch(r"[$\w.-]+", s) and re.search(r"[A-Za-z_]", s):
            named.add(s)
    for m in RECALL_PATH.finditer(prose):
        if len(m.group(0)) >= 4 and not m.group(0).startswith(("/", "~")):
            named.add(m.group(0))
    cited = {REV_PIN.sub("", m.group(1)) for m in CITE.finditer(text)}
    return named, {p for p in cited if not os.path.isabs(p)} - named


def holds(text, anchor, ident):
    return re.search(r"(?<![\w.])" + re.escape(anchor) + r"(?![\w])", text) if ident else anchor in text


def run_started(run):
    """When a run started: the time its ROOT file was written, the measure `run_start` dates a BASE-less run by."""
    return os.path.getmtime(os.path.join(run, "ROOT"))


def recall_run(rc, root, mine, run):
    """One earlier run read for recall: (its ROOT, BASE or "", [(node, answer text, hops, sidecar entries or None)],
    [why a node's sidecar cannot be used]); None when its ROOT shares neither ROOT nor ROOT's git common dir (mine).
    Unreadable names what could not be read."""
    try:
        with open(os.path.join(run, "ROOT"), encoding="utf-8") as f:
            theirs = f.read().strip()
    except OSError as e:
        raise Unreadable(f"cannot read ROOT: {e}")
    try:
        if theirs != root and (mine is None or rc.common(theirs) != mine):
            return None
    except GitUnknown as e:
        raise Unreadable(f"git could not place its ROOT {theirs}: {e}")
    try:
        with open(os.path.join(run, "BASE")) as f:
            base = f.read().strip()
    except FileNotFoundError:
        base = ""
    except OSError as e:
        raise Unreadable(f"cannot read BASE: {e}")
    lines, unusable = [], []
    try:
        nodes = sorted((f[:-5] for f in os.listdir(run) if f.endswith(".hops") and NODE_ID.fullmatch(f[:-5])),
                       key=node_key)
    except OSError as e:
        raise Unreadable(f"cannot list the run: {e}")
    for x in nodes:
        path = os.path.join(run, x + ".hops")
        try:
            with open(path, encoding="utf-8") as f:
                recs = parse(f.read())
        except (OSError, UnicodeDecodeError) as e:
            raise Unreadable(f"cannot read {path}: {e}")
        hopset = [r for r in recs if r[0] == "hop"]
        entries, why = recall_sidecar(run, x, len(hopset))
        if why:
            unusable.append(why)
        lines += [(x, r[1], hopset, entries) for r in recs if r[0] == "answer"]
    return theirs, base, lines, unusable


def recall_sidecar(run, node, hops):
    """(entries, why) of RUN/{node}.write.json: its entries, one per hop in order, or None — with why empty when the
    node has none (an older run, or a hops file stored without `write`), else naming why it cannot be used."""
    path = os.path.join(run, node + ".write.json")
    try:
        with open(path, encoding="utf-8") as f:
            got = json.load(f)
        if not isinstance(got, list) or not all(isinstance(e, dict) for e in got):
            raise ValueError("not a list of entries")
        if len(got) != hops:
            raise ValueError(f"{len(got)} entries for {hops} hops")
    except FileNotFoundError:
        return None, ""
    except (OSError, ValueError) as e:
        return None, f"cannot use {path}: {type(e).__name__}: {e} — node {node}'s lines are checked by git"
    return got, ""


def recall_runs(rc, root, exclude, before=None, recalls=()):
    """(runs, unreadable): the runs named by recalls first, in the order given, then every other earlier run under
    /tmp/{project}/tracer/ whose ROOT shares ROOT's git common dir (the same ROOT outside git), with `before` only those
    started strictly before that time, newest first; never a run twice, never exclude. Each run is (run, its ROOT, BASE
    or "", [(node, answer text, hops, sidecar entries)]); a run that cannot be read, a recalls run that is no run or is
    on another repository root, and a sidecar that cannot be used are (run, reason), never dropped."""
    parent = os.path.join("/tmp", project_name(root), "tracer")
    mine = rc.common(root)
    exclude = os.path.abspath(exclude) if exclude else None
    first, unreadable, seen = [], [], {exclude}
    for given in recalls:
        run = os.path.abspath(given)
        if run in seen:
            continue
        seen.add(run)
        if not os.path.isdir(run) or not os.path.lexists(os.path.join(run, "ROOT")):
            unreadable.append((run, "no run: no directory holding a ROOT file"))
            continue
        try:
            got = recall_run(rc, root, mine, run)
        except Unreadable as e:
            unreadable.append((run, str(e)))
            continue
        if got is None:
            unreadable.append((run, f"a run on another repository root, not {root}"))
            continue
        first.append((run, *got[:3]))
        unreadable += [(run, why) for why in got[3]]
    try:
        names = sorted((n for n in os.listdir(parent) if RUN_ID.fullmatch(n)), reverse=True)
    except FileNotFoundError:
        names = []
    except OSError as e:
        return first, unreadable + [(parent, f"cannot list the runs: {e}")]
    runs = []
    for name in names:
        run = os.path.join(parent, name)
        if run in seen or not os.path.isdir(run):
            continue
        try:
            started = run_started(run)
        except OSError as e:
            unreadable.append((run, f"cannot read ROOT: {e}"))
            continue
        if before is not None and started >= before:
            continue
        try:
            got = recall_run(rc, root, mine, run)
        except Unreadable as e:
            unreadable.append((run, str(e)))
            continue
        if got is None:
            continue
        runs.append((started, run, *got[:3]))
        unreadable += [(run, why) for why in got[3]]
    return first + [r[1:] for r in sorted(runs, key=lambda r: r[0], reverse=True)], unreadable


def walked_hashes(rc, theirs, hopset, entries):
    """{path: changed} of every working-tree file a node's hops walked, from its sidecar: changed when the file's
    sha256 at ROOT now differs from the one recorded at its write, or the file is gone; a hop pinned to a revision is
    left out. None when the node has no sidecar or a hop's entry records no sha256."""
    if entries is None:
        return None
    got = {}
    for h, e in zip(hopset, entries):
        path, sha = e.get("path"), e.get("sha256")
        if REV_PIN.match(h[1]) or (isinstance(path, str) and REV_PIN.match(path)):
            continue
        if not isinstance(path, str) or not path or not isinstance(sha, str):
            return None
        if os.path.isabs(path):
            if not path.startswith(theirs + os.sep):
                return None
            path = os.path.relpath(path, theirs)
        path = os.path.normpath(path)
        got[path] = got.get(path, False) or rc.sha(path) != sha
    return got


def hash_fresh(walked, text, theirs):
    """Whether a line is FRESH by hash alone: every file its node walked (walked, from `walked_hashes`) hashes as it did
    at the write and each path the line cites unpinned is one of them."""
    if not walked or any(walked.values()):
        return False
    for m in CITE.finditer(text):
        if REV_PIN.match(m.group(1)):
            continue
        p = m.group(1)
        p = os.path.normpath(os.path.relpath(p, theirs) if os.path.isabs(p) and p.startswith(theirs + os.sep) else p)
        if p not in walked:
            return False
    return True


def cite_paths(text, theirs):
    """The distinct paths of a line's [path:N] cites in order, each relative to its run's ROOT, a revision pin dropped."""
    out = []
    for m in CITE.finditer(text):
        p = REV_PIN.sub("", m.group(1))
        p = os.path.relpath(p, theirs) if os.path.isabs(p) and p.startswith(theirs + os.sep) else p
        if p not in out:
            out.append(p)
    return out


def recall_line(rc, run, theirs, base, text, hopset, idents, walked=None):
    """One prior answer line checked against ROOT now: (kind FRESH | DELTA | STALE | GAP, text with re-addressed
    cites, notes, failed, scope). FRESH first by hash, a closed-world claim excepted: every file its node walked
    (walked, from `walked_hashes`) and every file it cites hash as at the write — no git asked, the cites unchanged;
    else, by git and placement:
    STALE: a cite, or any hop its node walked, no longer holds; failed when git could not
    say. DELTA: a cited span moved, or a name it carries changed its occurrence set since BASE; notes the head, then the
    `-`/`+` rows: a moved span at its old and new line, each changed line once however many names it carries; scope
    the changed names, backticked, and a moved span's old path. GAP: a closed-world claim naming nothing to witness."""
    def rel(p):
        return os.path.relpath(p, theirs) if os.path.isabs(p) and p.startswith(theirs + os.sep) else p

    found = list(CITE.finditer(text))
    if not found:
        return "STALE", text, ["no [path:N] cite to re-check"], False, []
    # a closed-world claim rests on files its node never walked (a new caller elsewhere), so only git witnesses it
    if not CLOSED_WORLD.search(BACKTICK.sub(" ", CITE.sub(" ", text))) and hash_fresh(walked, text, theirs):
        return "FRESH", text, [], False, []
    out, last, stale, failed, moves = [], 0, [], False, []
    for m in found:
        path, a, b = rel(m.group(1)), int(m.group(2)), int(m.group(3) or m.group(2))
        hop = None
        for h in hopset:
            hp = rel(h[1])
            end = h[6] or h[2] + max(len(h[3]), 1) - 1
            same_file = os.path.normpath(hp) == os.path.normpath(path) or hp.endswith("/" + path) or path.endswith("/" + hp)
            if same_file and h[3] and h[2] - CITE_SLACK <= a and b <= end + CITE_SLACK:
                hop = (hp, h[2], h[3])
                break
        if hop is None:
            if not base:
                stale.append(f"{path}:{a}: no hop holds it and the run has no BASE")
                continue
            old = rc.base_lines(base, path)
            if isinstance(old, str):
                stale.append(f"{path}:{a}: BASE of run {os.path.basename(run)} unusable — {old}")
                failed = True
                continue
            if not 1 <= a <= b <= len(old):
                stale.append(f"{path}:{a}: past the end of {path} at BASE")
                continue
            hop = (path, a, old[a - 1:b])
        kind, new_path, new_line, reason = rc.place(*hop)
        if kind in ("STALE", "FAILED"):
            stale.append(reason)
            failed |= kind == "FAILED"
            continue
        shift = new_line - hop[1]
        span = f"{new_path}:{a + shift}" + (f"-{b + shift}" if m.group(3) else "")
        if kind == "MOVED":
            at = a - hop[1]
            was, said = (a, hop[2][at]) if 0 <= at < len(hop[2]) else (hop[1], hop[2][0])
            moves += [("-", path, was, said.strip()), ("+", new_path, was + shift, said.strip())]
        out.append(text[last:m.start()] + f"[{span}]")
        last = m.end()
    if stale:
        return "STALE", text, stale, failed, []
    # the line rests on every hop its node walked, not only the ones it cites; a hop that did not hold at BASE either
    # (or the run has none) changed nothing since
    for h in hopset:
        if not h[3] or REV_PIN.match(h[1]):
            continue
        kind, _, _, reason = rc.place(rel(h[1]), h[2], h[3])
        if kind == "FAILED":
            return "STALE", text, [f"its node's hop {reason}"], True, []
        if kind == "STALE":
            then = rc.place(f"{base[:PIN_ABBREV]}:{rel(h[1])}", h[2], h[3])[0] if base else None
            if then != "STALE":
                return "STALE", text, [f"its node's hop {reason}"], then == "FAILED", []
    text = "".join(out) + text[last:]
    named, cited = line_witnesses(text)
    named |= {i for i in idents if holds(text, i, True)}
    if CLOSED_WORLD.search(BACKTICK.sub(" ", CITE.sub(" ", text))) and not named:
        return "GAP", text, ["a closed-world claim naming no identifier or path to witness"], False, []
    changed, rows, placed = [], [], set()
    for sign, p, n, t in moves:
        placed.add((sign, p, n))
        rows.append(f"{sign} {p}:{n} {t[:160]}")
    for sym in sorted(named | cited):
        if not base:
            return "STALE", text, [f"no BASE to witness `{sym}` against"], False, []
        got = rc.delta(sym, base)
        if isinstance(got, str):
            return "STALE", text, [f"witness `{sym}` unusable for run {os.path.basename(run)} — {got}"], True, []
        added, gone = got
        if added or gone:
            changed.append(f"`{sym}`")
            for sign, occ in (("+", added), ("-", gone)):
                for p, n, t in occ:
                    if (sign, p, n) not in placed:
                        placed.add((sign, p, n))
                        rows.append(f"{sign} {p}:{n} {t[:160]}")
    if not (moves or changed):
        return "FRESH", text, [], False, []
    head = (["moved"] if moves else []) + ([f"changed since {base[:PIN_ABBREV]}: {', '.join(changed)}"] if changed else [])
    gone = [p for sign, p, _, _ in moves if sign == "-" and p not in cite_paths(text, theirs)]
    return "DELTA", text, head + rows, False, changed + list(dict.fromkeys(gone))


TRACED = ("FRESH", "DELTA", "STALE", "GAP")


def recall_unit(kind, lines, q=None, **more):
    """One block of recall output: kind None (a head, a GAP question, UNREADABLE, the cover), TRACE (the question's
    work line, written after the cut), REWALK (one node's line), FOLD (the question's fold line, empty until the cap
    folds) or a checked line's kind; hits (anchor hits; a REWALK line's negated place among its question's), carries
    (the anchors it hits), scope (what tracing it covers), marker (its first cite path), rows (a DELTA line's
    occurrence rows), pinned (a STALE row naming a git failure: never cut)."""
    return dict(dict(kind=kind, lines=lines, q=q, hits=0, carries=set(), scope=[], marker=None, rows=0, pinned=False),
                **more)


def recall_capped(units, anchors):
    """The output lines of units within RECALL_CAP bytes. Kinds None, TRACE, FOLD and GAP and pinned STALE rows always
    stay.
    Past the cap, fewest anchor hits first and the later unit first on a tie within each step: STALE lines collapse,
    then each DELTA line's occurrence rows shrink to `  +{rows} rows`, then DELTA lines collapse, then REWALK lines
    fold, each question's last first, then FRESH lines go, the question with the smallest share of FRESH lines first:
    answers outlast the pointers to re-walk, and a question its FRESH lines nearly settle keeps them longest.
    A collapsed line becomes `CHANGED Q{q} · {its first cite path}`, one per (question, path), never cut, so the cap
    never hides a changed fact. Folded REWALK lines and cut FRESH lines become the question's one fold line,
    `REWALK Q{q} · {w} nodes and {f} fresh lines cut by the cap · thread the rest of the TRACE Q{q} scope` (either
    part alone when the other is 0). Each question with checked lines (anchors: {q: its anchors}) gets its TRACE line
    from what stays: `none` when every line is a kept FRESH one and every anchor is hit by a line, else
    `{k} rows · {scope}` over its DELTA, STALE, CHANGED and GAP lines and FRESH lines cut, plus each anchor no line
    hits. A `RECALL CUT` line names what went, before the last unit."""
    def size(lines):
        return sum(len(x.encode()) + 1 for x in lines)

    shown, keep, markers = [list(u["lines"]) for u in units], [True] * len(units), {}
    cut = {"STALE": 0, "trim": 0, "DELTA": 0, "REWALK": 0, "FRESH": 0}
    of = {q: [i for i, u in enumerate(units) if u["q"] == q and u["kind"] in TRACED] for q in anchors}
    fold = {u["q"]: [i, 0, 0] for i, u in enumerate(units) if u["kind"] == "FOLD"}

    def folded(q, part):
        """Count one more folded REWALK line (part 1) or cut FRESH line (part 2) on q's fold line."""
        at = fold[q]
        at[part] += 1
        what = [f"{at[1]} nodes"] * (at[1] > 0) + [f"{at[2]} fresh lines"] * (at[2] > 0)
        shown[at[0]] = [f"REWALK Q{q} · {' and '.join(what)} cut by the cap · thread the rest of the TRACE Q{q} scope"]

    def trace(q):
        rows = of[q]
        todo = [i for i in rows if units[i]["kind"] != "FRESH" or not keep[i]]
        bare = anchors[q] - set().union(*(units[i]["carries"] for i in rows))
        scope = dict.fromkeys([s for i in todo for s in units[i]["scope"]] + [f"`{a}`" for a in sorted(bare)])
        if not todo and not scope:
            return f"TRACE Q{q} · none"
        return f"TRACE Q{q} · {len(todo)} rows · {', '.join(scope)}"

    def cut_line():
        s, d, w, f = cut["STALE"], cut["DELTA"], cut["REWALK"], cut["FRESH"]
        trim = f" · {cut['trim']} delta lines cut to their head" if cut["trim"] else ""
        return (f"RECALL CUT {s + d + w + f} lines past the {RECALL_CAP // 1024} KB cap · {s} stale, {d} delta, "
                f"{w} rewalk, {f} fresh · {s + d} collapsed to {len(markers)} CHANGED{trim}")

    def fits():
        kept = sum(size([trace(u["q"])]) if u["kind"] == "TRACE" else size(shown[i])
                   for i, u in enumerate(units) if keep[i])
        return kept + (size([cut_line()]) if any(cut.values()) else 0) <= RECALL_CAP

    share = {q: sum(units[i]["kind"] == "FRESH" for i in rows) / len(rows) for q, rows in of.items() if rows}
    for step, kind in (("STALE", "STALE"), ("trim", "DELTA"), ("DELTA", "DELTA"), ("REWALK", "REWALK"),
                       ("FRESH", "FRESH")):
        for i in sorted((i for i, u in enumerate(units) if u["kind"] == kind and not u["pinned"]),
                        key=lambda i: (share[units[i]["q"]] if step == "FRESH" else 0, units[i]["hits"], -i)):
            if fits():
                break
            if step == "trim":
                if len(shown[i]) < 2:
                    continue
                shown[i] = shown[i][:1] + [f"  +{units[i]['rows']} rows"]
            elif step in ("REWALK", "FRESH"):
                keep[i] = False
                folded(units[i]["q"], 1 if step == "REWALK" else 2)
            else:
                at = (units[i]["q"], units[i]["marker"])
                if at in markers:
                    keep[i] = False
                else:
                    markers[at], shown[i] = i, [f"CHANGED Q{at[0]} · {at[1]}"]
            cut[step] += 1
    out = [x for i, u in enumerate(units) if keep[i]
           for x in ([trace(u["q"])] if u["kind"] == "TRACE" else shown[i])]
    if any(cut.values()):
        out.insert(len(out) - 1, cut_line())
    return out


def node_from(run, node):
    """The FROM node was started on: the first field of the first line of RUN/{node}.started (`FROM<tab>QUESTION`);
    `unknown ({why})` when it cannot be read."""
    path = os.path.join(run, node + ".started")
    try:
        with open(path, encoding="utf-8") as f:
            first = f.readline()
    except (OSError, UnicodeDecodeError) as e:
        return f"unknown (cannot read {path}: {type(e).__name__}: {e})"
    return first.rstrip("\n").split("\t", 1)[0].strip() or f"unknown ({path} names no FROM)"


def rewalk_why(kind, notes, failed, scope):
    """What a line not FRESH needs its node re-walked for, in the words a REWALK line gives when no walked file
    changed."""
    if kind == "GAP":
        return ["closed-world claim"]
    if kind == "STALE":
        return ["git could not check" if failed else "no BASE to check against" if "no BASE" in notes[0] else
                "stale cite"]
    names = [s for s in scope if s.startswith("`")]
    return ["moved span"] * ("moved" in notes) + [f"changed occurrences of {', '.join(names)}"] * bool(names)


def rewalk_line(n, run, node, changed, hashed, why):
    """`REWALK Q{n} · {run id} node {node} · FROM {its FROM}`, then `· changed: {paths}` naming the walked files whose
    sha256 changed since its write, else `· files unchanged` (or `· files not hashed`, the node without a usable
    sidecar) `; re-walk for: {why}`."""
    head = f"REWALK Q{n} · {os.path.basename(run)} node {node} · FROM {node_from(run, node)}"
    if changed:
        return f"{head} · changed: {', '.join(changed)}"
    return f"{head} · files {'unchanged' if hashed else 'not hashed'}; re-walk for: {', '.join(why)}"


def recall(root, asked, exclude=None, before=None, recalls=()):
    """The recall of ROOT's questions over every earlier run of ROOT (the recalls runs first) as output lines: per
    question a RECALL head, its TRACE line, a `rewalk_line` per earlier node with a line not FRESH, most anchor hits and
    newest run first, its fold line once the cap folds, then each matched prior answer line FRESH (cites re-addressed),
    DELTA (a moved span or changed occurrences, with its rows) or STALE (with its reason), or GAP, as `recall_capped`
    keeps them; a GAP question's TRACE line is `whole question`; first a line per run or sidecar that could not be
    read, last the RECALL COVER line."""
    rc, out = Recall(root), []
    try:
        runs, unreadable = recall_runs(rc, root, exclude, before, recalls)
    except GitUnknown as e:
        out.append(f"RECALL GIT FAILED — {e}: candidates are runs on this exact ROOT")
        rc.commons[root] = None
        runs, unreadable = recall_runs(rc, root, exclude, before, recalls)
    if rc.commons.get(root) is None and not out:
        out.append(f"RECALL: {root} is in no git repository — candidates are runs on this exact ROOT")
    out.append(f"RECALL {root} · {len(runs)} earlier runs · {len(unreadable)} unreadable")
    out += [f"UNREADABLE {run}: {why}" for run, why in unreadable]
    units, checked, walks = [recall_unit(None, out)], {}, {}
    served = stale = delta = gaps = 0
    for n in sorted(asked):
        anchors, idents = recall_anchors(asked[n])
        if not anchors:
            units.append(recall_unit(None, [f"GAP Q{n} · the question names no anchor", f"TRACE Q{n} · whole question"]))
            continue
        seen, matched = set(), []
        for order, (run, theirs, base, lines) in enumerate(runs):
            for x, text, hopset, entries in lines:
                hit = [a for a in anchors if holds(text, a, a in idents)]
                if hit and text not in seen:
                    seen.add(text)
                    matched.append((-len(hit), order, run, theirs, base, x, text, hopset, entries, set(hit)))
        if not matched:
            units.append(recall_unit(None, [f"GAP Q{n} · no earlier answer line names {', '.join(sorted(anchors))}",
                                            f"TRACE Q{n} · whole question"]))
            continue
        matched.sort(key=lambda r: r[:2])
        shown = matched[:RECALL_SHOWN]
        rows, fresh, ids, rewalk = [], 0, [], {}
        for hits, _, run, theirs, base, x, text, hopset, entries, hit in shown:
            if (run, x) not in walks:
                walks[(run, x)] = walked_hashes(rc, theirs, hopset, entries)
            walked = walks[(run, x)]
            kind, new, notes, failed, scope = recall_line(rc, run, theirs, base, text, hopset, idents, walked)
            rid = os.path.basename(run)
            if rid not in ids:
                ids.append(rid)
            fresh += kind == "FRESH"
            stale += kind == "STALE"
            delta += kind == "DELTA"
            gaps += kind == "GAP"
            paths = cite_paths(new, theirs)
            if kind != "FRESH":
                changed = [p for p, moved in (walked or {}).items() if moved]
                why = rewalk.setdefault((run, x), (changed, walked is not None, {}))[2]
                why.update(dict.fromkeys(rewalk_why(kind, notes, failed, scope)))
            more = dict(hits=-hits, carries=hit, marker=paths[0] if paths else "uncited", pinned=failed,
                        scope=paths + scope if paths or scope else [f"`{a}`" for a in sorted(hit)])
            if kind == "STALE":
                rows.append(recall_unit(kind, [f"STALE Q{n} · {notes[0]}"], n, **more))
            elif kind == "GAP":
                rows.append(recall_unit(kind, [f"GAP Q{n} · {notes[0]}: {new[:160]}"], n, **more))
            elif kind == "FRESH":
                rows.append(recall_unit(kind, [f"FRESH {rid} node {x}: {new}"], n, **more))
            else:
                occ = [s for s in notes if s[:2] in ("+ ", "- ")]
                head = [s for s in notes if s[:2] not in ("+ ", "- ")]
                tail = [f"  +{len(occ) - DELTA_ROWS} more"] if len(occ) > DELTA_ROWS else []
                rows.append(recall_unit(kind, [f"DELTA {rid} node {x}: {new} · {'; '.join(head)}"]
                                        + ["  " + s for s in occ[:DELTA_ROWS]] + tail, n, rows=len(occ), **more))
        cut = f" · {len(matched) - len(shown)} more matched lines not checked" if len(matched) > len(shown) else ""
        units.append(recall_unit(None, [f"RECALL Q{n} · {fresh}/{len(shown)} lines fresh · runs {', '.join(ids)}{cut}"]))
        units.append(recall_unit("TRACE", [], n))
        units += [recall_unit("REWALK", [rewalk_line(n, run, x, *node)], n, hits=-k)
                  for k, ((run, x), node) in enumerate(rewalk.items())]
        units.append(recall_unit("FOLD", [], n))
        units += rows
        checked[n] = anchors
        served += fresh > 0
    gap = f" · {gaps} unwitnessed" if gaps else ""
    units.append(recall_unit(None, [f"RECALL COVER {served}/{len(asked)} · {stale} stale · {delta} delta{gap}"]))
    return recall_capped(units, checked)


def cmd_recall(root, before_run=None, recalls=()):
    root = os.path.abspath(root)
    if not os.path.isdir(root):
        fail(f"repo root {root} is not a directory")
    before = None
    if before_run is not None:
        before_run = os.path.abspath(before_run)
        try:
            before = run_started(before_run)
        except OSError as e:
            fail(f"--before {before_run} is no run whose start can be read: {e}")
    asked = parse_questions(sys.stdin.read())
    if not asked:
        fail("recall read no numbered question from stdin: start each with `1.`, `1)` or `Q1`")
    print("\n".join(recall(root, asked, exclude=before_run, before=before, recalls=recalls)))


def main():
    a = sys.argv[1:]
    if a and a[0] == "recall":
        usage = "usage: tracetree.py recall ROOT [--before RUN] [--recall RUN]... < numbered questions"
        before, recalls, roots, i = None, [], [], 1
        while i < len(a):
            if a[i] == "--before" and i + 1 < len(a) and before is None:
                before, i = a[i + 1], i + 2
            elif a[i] == "--recall" and i + 1 < len(a):
                recalls.append(a[i + 1])
                i += 2
            elif a[i].startswith("--"):
                fail(f"recall does not take {a[i]!r} — {usage}")
            else:
                roots.append(a[i])
                i += 1
        if len(roots) != 1:
            fail(usage)
        return cmd_recall(roots[0], before, recalls)
    if not a or a[0] not in ("new", "start", "check", "wait", "write", "map", "read", "show", "book") or (len(a) < 2 and a[0] != "book"):
        fail("usage: tracetree.py new ROOT [--seek REGEX] [--questions [--recall RUN]...] | recall ROOT [--before RUN] [--recall RUN]... < questions | start RUN ID PATH:LINE [QUESTION] | write RUN ID < hops (a first write with a hop that does not hold is refused once; the same write again stores it) | wait RUN ID... [--timeout S] [--grace S] [--start S] | check RUN [--hops [ID] | --full [ID]] | map RUN [--from NAME | --to NAME] [--all] [--json | --html OUT] | read RUN FILE:A-B [FILE:A-B ...] | show RUN FILE:A-B [FILE:A-B ...] | book [--html OUT]")
    if a[0] == "book":
        if not (len(a) == 1 or (len(a) == 3 and a[1] == "--html" and not a[2].startswith("--"))):
            fail("book takes only --html OUT — usage: tracetree.py book [--html OUT]")
        return cmd_book({"--html": a[2] if len(a) == 3 else None})
    if a[0] == "new":
        usage = "usage: tracetree.py new ROOT [--seek REGEX] [--questions [--recall RUN]...]"
        seek, questions, recalls, roots, i = None, False, [], [], 1
        while i < len(a):
            if a[i] == "--seek" and i + 1 < len(a):
                seek, i = a[i + 1], i + 2
            elif a[i] == "--questions":
                questions, i = True, i + 1
            elif a[i] == "--recall" and i + 1 < len(a):
                recalls.append(a[i + 1])
                i += 2
            elif a[i].startswith("--"):
                fail(f"new does not take {a[i]!r} — {usage[7:]}")
            else:
                roots.append(a[i])
                i += 1
        if len(roots) != 1:
            fail(usage)
        if recalls and not questions:
            fail(f"--recall recalls for the questions, and needs --questions — {usage[7:]}")
        return cmd_new(roots[0], seek, questions, recalls)
    if a[0] == "start":
        if len(a) < 4:
            fail("usage: tracetree.py start RUN ID PATH:LINE [QUESTION]")
        return cmd_start(a[1], a[2], a[3], " ".join(a[4:]))
    if a[0] == "write":
        if len(a) != 3:
            fail("usage: tracetree.py write RUN ID < hops")
        return cmd_write(a[1], a[2])
    if a[0] == "read":
        if len(a) < 3:
            fail("usage: tracetree.py read RUN FILE:A-B [FILE:A-B ...]")
        return cmd_read(a[1], a[2:])
    if a[0] == "show":
        if len(a) < 3:
            fail("usage: tracetree.py show RUN FILE:A-B [FILE:A-B ...]")
        return cmd_show(a[1], a[2:])
    if a[0] == "wait":
        opts, rest = timed_args(a[2:], {"--timeout": 180.0, "--grace": 15.0, "--start": 120.0})
        ids = [arg.rstrip(".,") for arg in rest]
        if not ids:
            fail("wait needs the IDs of the sub-tracers spawned")
        bad = [i for i in ids if not NODE_ID.fullmatch(i)]
        if bad:
            fail(f"wait takes node IDs like 3 or 3.1, got {', '.join(bad)}")
        return cmd_wait(a[1], ids, opts["--timeout"], opts["--grace"], opts["--start"])
    if a[0] == "map":
        opts, i = {"--from": None, "--to": None, "--html": None, "--all": False, "--json": False}, 2
        while i < len(a):
            if a[i] in ("--all", "--json"):
                opts[a[i]] = True
                i += 1
            elif a[i] in ("--from", "--to", "--html") and i + 1 < len(a):
                opts[a[i]] = a[i + 1]
                i += 2
            else:
                fail(f"map does not take {a[i]!r} — map RUN [--from NAME | --to NAME] [--all] [--json | --html OUT]")
        if opts["--json"] and opts["--html"]:
            fail("map takes --json or --html, not both")
        return cmd_map(a[1], opts)
    view, only = "answers", None
    for flag in ("--hops", "--full"):
        if flag in a:
            view = flag[2:]
            i = a.index(flag)
            if i + 1 < len(a) and not a[i + 1].startswith("--"):
                only = a[i + 1]
    cmd_check(a[1], view, only)


if __name__ == "__main__":
    main()
