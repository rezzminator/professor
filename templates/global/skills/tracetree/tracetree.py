#!/usr/bin/env python3
"""tracetree — the run directory and the quote check behind tracer and sub-tracer.

  tracetree.py new ROOT [--seek REGEX] [--questions]
                                        create a run directory under /tmp/{project}/tracer/, print its path;
                                        --questions reads numbered questions (`1.`, `1)`, `Q1`) from stdin to EOF into
                                        RUN/questions (stdin is read only with it); --seek lists, after the path, every
                                        verified hop of an earlier run on this ROOT (modified within 6 hours, running
                                        ones included) whose path or current line matches REGEX, then one PRIOR: line;
                                        in a git work tree RUN/BASE holds the commit ROOT is at (no file otherwise)
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
                                        grep only) (--timeout S, default 540); a named node not started after --start S
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
SEEK_HOURS = 6
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
    """Earlier runs on this root, modified within SEEK_HOURS, running ones included: every verified hop whose path or
    current line matches rx, newest run first. What it could not read is counted in the summary, never dropped."""
    parent, now = os.path.dirname(run), time.time()
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
            if now - stamp > SEEK_HOURS * 3600:
                continue
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


def cmd_new(root, seek=None, questions=False):
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


def cmd_seek(run, seek):
    """The PRIOR lines of `new --seek` for a run that already exists — the run a caller opened and handed the tracer."""
    run = os.path.abspath(run)
    try:
        with open(os.path.join(run, "ROOT"), encoding="utf-8") as f:
            root = f.read().strip()
    except OSError as e:
        fail(f"{run} is not a tracetree run (no ROOT file: {e})")
    try:
        rx = re.compile(seek)
    except re.error as e:
        fail(f"seek {seek!r} is not a regular expression: {e}")
    print(run)
    seek_prior(run, root, rx)


# `Repo root` / `Repository root` may carry an alias before the path (`Repo root / TREE = /a`); a bare `Root` takes the
# path only directly after it, so `Root cause: /a/b` names no root
INTAKE_ROOT = re.compile(r"(?im)^[ \t]*(?:repo(?:sitory)?[ \t]+root\b[^\n]*?|root[ \t]*[:=]?[ \t]*[`'\"(]?)"
                         r"((?:~/|/)[^\s`'\"()\[\],;]+)")
INTAKE_RUN = re.compile(r"(?m)^[ \t]*RUN:[ \t]*\S")
TT = "python3 $HOME/.claude/skills/tracetree/tracetree.py"


def intake_log(msg):
    """A hook has no caller to tell when its payload is unreadable: the failure goes to intake.log in the tracer
    scratch directory of the project the hook runs in, /tmp/{project}/tracer/, and to stderr when even that cannot be
    written."""
    folder = os.path.join("/tmp", project_name(os.getcwd()), "tracer")
    try:
        os.makedirs(folder, exist_ok=True)
        with open(os.path.join(folder, "intake.log"), "a", encoding="utf-8") as f:
            f.write(time.strftime("%Y-%m-%dT%H:%M:%S ") + msg + "\n")
    except OSError as e:
        sys.stderr.write(f"tracetree intake: {msg} (log not written to {folder}: {e})\n")


def cmd_intake():
    """PreToolUse hook on the Agent tool. A `tracer` spawn whose brief holds numbered questions and no `RUN:` line gets
    its run opened here — ROOT from the brief's `Repo root:` line, else the hook's cwd — the brief rewritten to carry
    `RUN: {run}` after that line, and the caller told how to pull each answer. Every other call passes untouched; a
    run that cannot be opened passes the spawn on unchanged and tells the caller why."""
    raw = sys.stdin.read()
    try:
        event = json.loads(raw)
        if not isinstance(event, dict) or not isinstance(event.get("tool_input") or {}, dict):
            raise ValueError("not a hook payload object")
    except ValueError as e:
        intake_log(f"unreadable hook payload ({e}): {raw[:200]!r}")
        return
    tool, given = event.get("tool_name"), event.get("tool_input") or {}
    if tool not in ("Agent", "Task") or given.get("subagent_type") != "tracer":
        return
    brief = given.get("prompt")
    if not isinstance(brief, str) or INTAKE_RUN.search(brief):
        return
    asked = parse_questions(brief)
    if not asked:
        return
    m = INTAKE_ROOT.search(brief)
    named = m.group(1).rstrip(".:") if m else ""
    root = os.path.abspath(os.path.expanduser(named or str(event.get("cwd") or "")))

    def say(text, rewritten=None):
        out = {"hookEventName": "PreToolUse", "additionalContext": text}
        if rewritten is not None:
            out.update(permissionDecision="allow", updatedInput=rewritten)
        print(json.dumps({"hookSpecificOutput": out}))

    if not os.path.isdir(root):
        say(f"tracer intake opened no run: repo root {root} is not a directory — the brief goes to tracer unchanged, "
            "and tracer opens its own run; its final names it.")
        return
    stored = "\n".join(f"{n}. {asked[n]}" for n in sorted(asked)) + "\n"
    try:
        run = open_run(root, stored)
    except OSError as e:
        say(f"tracer intake opened no run: {e} — the brief goes to tracer unchanged, and tracer opens its own run; "
            "its final names it.")
        return
    lines = brief.split("\n")
    lines.insert(brief[:m.end()].count("\n") + 1 if m else 0, f"RUN: {run}")
    rewritten = dict(given, prompt="\n".join(lines))
    nums = ", ".join(map(str, sorted(asked)))
    say(f"tracer RUN {run} holds your questions {nums}. Its final, returned when the run ends, is your answer: it "
        "drills the readers' threads, fills their gaps and names what they leave unsafe. To start work before then, "
        f"`{TT} answer {run} {{n…}}` blocks until those questions' threads are walked, most within three minutes, "
        "and prints the readers' raw answers, every cite re-read from disk — facts to build on, never a ruling: a "
        "verdict, a SAFE or a closing claim waits for the final, so end your turn once only the final is left. Until "
        "then work on what no question covers — the testing manual, fixtures, a doc; a file a pending question "
        "covers, read now, is paid for twice. /tracer:use has the rest.", rewritten)


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


ANSWER_TRUST = ("every [path:N] below was re-read from disk just now: an unflagged line is evidence — cite it, never "
                "re-read it; a flagged line is not")


def started_questions(run, entries):
    """{node: set of question numbers its card named} from each `.started` file; a child whose card names none takes
    its dotted parent's. A `.started` file that cannot be read serves no question and is named in the second value."""
    got, bad = {}, []
    for f in entries:
        if not f.endswith(".started") or not NODE_ID.fullmatch(f[:-8]):
            continue
        try:
            with open(os.path.join(run, f), encoding="utf-8") as fh:
                text = fh.read()
        except OSError as e:
            bad.append(f"{f}: {e}")
            continue
        got[f[:-8]] = {n for line in text.splitlines() for n in question_numbers(line.partition("\t")[2])}
    for name in sorted(got, key=node_key):
        if not got[name] and "." in name:
            got[name] = set(got.get(name.rsplit(".", 1)[0], ()))
    return got, bad


def cmd_answer(run, wanted, timeout, grace, start):
    """Block until every node serving the named questions — each node whose card named one, and every child it split
    off — has written its file and none of them changed for `grace` seconds; then print those questions' answers, each
    cite checked against the file as it is now. The caller pulls the answer it needs when it needs it while the rest
    of the run walks. No number named: every question in RUN/questions. A question no node took within `start` seconds
    of the first node starting is named, never waited for; a timeout prints what is in and names what still walks."""
    run, t0 = os.path.abspath(run), time.time()
    if not os.path.isfile(os.path.join(run, "ROOT")):
        fail(f"{run} is not a tracetree run (no ROOT file) — `new ROOT --questions` prints one")
    asked, qnote = {}, None
    try:
        with open(os.path.join(run, "questions"), encoding="utf-8") as f:
            asked = parse_questions(f.read())
    except FileNotFoundError:
        qnote = f"QUESTIONS: none stored — {run}/questions is absent"
    except OSError as e:
        qnote = f"QUESTIONS UNREADABLE — {run}/questions: {e}"
    wanted = sorted(set(wanted)) if wanted else sorted(asked)
    if not wanted:
        fail(f"answer needs question numbers: {qnote or run + '/questions holds none'}")
    while True:
        try:
            entries = os.listdir(run)
        except OSError as e:
            fail(f"cannot list {run}: {e}")
        have = {f[:-5]: os.path.getmtime(os.path.join(run, f)) for f in entries if f.endswith(".hops")}
        took, bad = started_questions(run, entries)
        first = min((os.path.getmtime(os.path.join(run, n + ".started")) for n in took), default=None)
        serving = {}
        for q in wanted:
            heads = {n for n, nums in took.items() if q in nums}
            serving[q] = sorted(heads | {n for n in set(have) | set(took) if any(n.startswith(h + ".") for h in heads)},
                                key=node_key)
        walking = sorted({n for q in wanted for n in serving[q]} - set(have), key=node_key)
        late = first is not None and time.time() - first >= start
        untaken = [q for q in wanted if not serving[q] and late]
        pending = [q for q in wanted if not serving[q] and not late]
        stamps = [have[n] for q in wanted for n in serving[q] if n in have]
        quiet = time.time() - max(stamps, default=t0)
        elapsed = time.time() - t0
        if not walking and not pending and quiet >= grace:
            head = f"ANSWER after {int(elapsed)}s"
            break
        if elapsed >= timeout:
            head = f"ANSWER TIMEOUT after {int(elapsed)}s"
            break
        time.sleep(min(1.0, max(0.2, grace / 5)))
    done = [q for q in wanted if serving[q] and not set(serving[q]) - set(have)]
    names = sorted({n for q in done for n in serving[q]}, key=node_key)
    top = [f"{head} · RUN {run} · Q{', Q'.join(map(str, done))} · {ANSWER_TRUST}" if done else
           f"{head} · RUN {run} · no question answered yet"]
    if qnote:
        top.append(qnote)
    top += [f"STARTED UNREADABLE — {b}" for b in bad]
    for q in wanted:
        if q in done:
            continue
        if serving[q]:
            top.append(f"STILL WALKING Q{q}: nodes {', '.join(n for n in serving[q] if n not in have)} started, no "
                       f"file yet — `answer {run} {q}` again")
        elif q in untaken:
            top.append(f"NO THREAD Q{q}: no node took it {int(start)}s after the first started — the tracer's final "
                       f"answers it")
        else:
            top.append(f"NOT STARTED Q{q}: no node has started yet — `answer {run} {q}` again")
    body, opened = [], []
    if names:
        loaded = load(run)
        inc = incidents(loaded)
        mine = [l for l in inc["lines"][1:] if l.startswith("⚠ run:")
                or any(l.startswith(f"⚠ node {n}:") for n in names)]
        if mine:
            top += [inc["lines"][0], *mine]
        blocks, _ = check_view("answers", names, loaded)
        by_name, shown = {b["name"]: b for b in blocks}, set()
        for q in done:
            body.append(f"Q{q} · nodes {', '.join(serving[q])} · "
                        f"{(asked.get(q) or '(not in questions)').splitlines()[0][:300]}")
            for n in serving[q]:
                if n in by_name and n not in shown:
                    body += by_name[n]["lines"]
                    shown.add(n)
        opened = open_block(loaded, names)
    full = "\n".join([*top, *body, *opened])
    if len(full) < CEILING:
        print(full)
        return
    path = os.path.join(run, "answer-Q" + "-".join(map(str, wanted)) + ".txt")
    try:
        with open(path, "w", encoding="utf-8") as f:
            f.write(full + "\n")
    except OSError as e:
        print(f"ANSWER FILE FAILED — {path} not written: {type(e).__name__}: {e}")
        print(full)
        return
    room = CEILING - 200 - sum(len(l) + 1 for l in [*top, *opened[:40]])
    kept, used = [], 0
    for l in body:
        if used + len(l) + 1 > room:
            break
        kept.append(l)
        used += len(l) + 1
    print("\n".join([*top, *kept, f"+{len(body) - len(kept)} answer lines: {path} — ask fewer questions per call",
                     *opened[:40], *([f"+{len(opened) - 40} OPEN lines: {path}"] if len(opened) > 40 else [])]))


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
    """`wait`'s and `answer`'s arguments: ({--timeout, --grace, --start} seconds, each defaulted), the rest in order."""
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


def main():
    a = sys.argv[1:]
    if a == ["intake"]:
        return cmd_intake()
    if a and a[0] == "segment":
        import segment
        sys.exit(segment.main(a[1:], fail))
    if not a or a[0] not in ("new", "seek", "start", "check", "wait", "answer", "write", "map", "read", "show", "book") or (len(a) < 2 and a[0] != "book"):
        fail("usage: tracetree.py new ROOT [--seek REGEX] [--questions] | seek RUN REGEX | intake < PreToolUse hook payload | start RUN ID PATH:LINE [QUESTION] | write RUN ID < hops (a first write with a hop that does not hold is refused once; the same write again stores it) | wait RUN ID... [--timeout S] [--grace S] [--start S] | answer RUN [Q...] [--timeout S] [--grace S] [--start S] | check RUN [--hops [ID] | --full [ID]] | map RUN [--from NAME | --to NAME] [--all] [--json | --html OUT] | read RUN FILE:A-B [FILE:A-B ...] | show RUN FILE:A-B [FILE:A-B ...] | book [--html OUT] | segment ROOT [--rev SHA] --seed SEED...|--seeds FILE [--variant contract|package|cochange|tracer] [--floor LINES] [--read-max SHARE] [--max K] [--json]")
    if a[0] == "seek":
        if len(a) != 3:
            fail("usage: tracetree.py seek RUN REGEX")
        return cmd_seek(a[1], a[2])
    if a[0] == "answer":
        opts, rest = timed_args(a[2:], {"--timeout": 300.0, "--grace": 5.0, "--start": 90.0})
        qs = []
        for arg in rest:
            m = re.fullmatch(r"[Qq]?(\d+)[.,]?", arg)
            if not m:
                fail(f"answer takes question numbers like 3 or Q3, got {arg!r}")
            qs.append(int(m.group(1)))
        return cmd_answer(a[1], qs, opts["--timeout"], opts["--grace"], opts["--start"])
    if a[0] == "book":
        if not (len(a) == 1 or (len(a) == 3 and a[1] == "--html" and not a[2].startswith("--"))):
            fail("book takes only --html OUT — usage: tracetree.py book [--html OUT]")
        return cmd_book({"--html": a[2] if len(a) == 3 else None})
    if a[0] == "new":
        seek, questions, roots, i = None, False, [], 1
        while i < len(a):
            if a[i] == "--seek" and i + 1 < len(a):
                seek, i = a[i + 1], i + 2
            elif a[i] == "--questions":
                questions, i = True, i + 1
            elif a[i].startswith("--"):
                fail(f"new does not take {a[i]!r} — new ROOT [--seek REGEX] [--questions]")
            else:
                roots.append(a[i])
                i += 1
        if len(roots) != 1:
            fail("usage: tracetree.py new ROOT [--seek REGEX] [--questions]")
        return cmd_new(roots[0], seek, questions)
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
        opts, rest = timed_args(a[2:], {"--timeout": 540.0, "--grace": 15.0, "--start": 120.0})
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
