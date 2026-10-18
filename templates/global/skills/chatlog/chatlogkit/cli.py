"""The verbs and their dispatch. `prep` is here (it composes engines, digest, labels and cost); the
reader's verbs over a prepared directory are in rows.py, `runs` in locate.py."""
import json
import os
import tempfile
import time
import traceback

from . import census, cost, locate, rows
from .digest import EVENT_LINE, Request, build, clean
from .engines import Failure, load as load_jsonl, opencode
from .labels import MECH, mechanical
from .rows import die, opt

KIT_DIR = os.environ.get("CHATLOG_KIT_DIR") or os.path.join(tempfile.gettempdir(), "chatlog-kit")
USAGE = """chatlog kit: the mechanics of reading transcripts, so a reader only reads and a count is code.

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
  census --type T [--since 30d] [--project SUBSTR] | --session S | PATH...  [--tree] [--out F]
                          one TSV row per transcript (calls, errors, denials, compactions,
                          spawns, USD, final line), a TOTAL row, FAILED per unreadable one,
                          GAPS per transcript whose dollars carry data gaps;
                          --tree the spawn tree with calls and USD rolled up per branch

TARGET is anything the locator resolves: a path, a session id or prefix, an agent id, a chat name,
a Codex agent path, an OpenCode id, title or {store}#{session}. Rows are tab-separated:
id (D{n} digest line, M{n} timeline line), text, label, ref."""


def parse(target, path):
    """The Parsed transcript a located target names."""
    split = opencode.split_locator(path)
    return opencode.load(*split) if split else load_jsonl(path)


def prep(args):
    lines = opt(args, "--lines", "")
    dirp = opt(args, "--dir")
    window = int(opt(args, "--window", "40000"))
    rmax = int(opt(args, "--reader-max", "250000"))
    if len(args) != 1:
        die("usage: chatlog.py prep TARGET [--lines A-B] [--dir DIR] [--window BYTES] [--reader-max BYTES]", 2)
    target = args[0]
    try:
        path = locate.resolve(target)
        p = parse(target, path)
    except (locate.NotFound, Failure) as e:
        die(f"FAILED {target} — {str(e)[:600]}")
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
            same = isinstance(prev, dict) and prev.get("path") == path and prev.get("lines") == (lines or "all")
        except (OSError, ValueError):
            same = False
        if not same:
            dirp = tempfile.mkdtemp(prefix="reader.", dir=dirp)
    try:
        raw, spans = build(p, Request(lines), os.path.expanduser("~"))
    except ValueError as e:
        die(f"FAILED {target} — {e}")
    dig = [clean(x) for x in raw]
    with open(os.path.join(dirp, "digest.txt"), "w") as f:
        f.write("\n".join(dig) + "\n")

    tl, tl_state = [], "skipped (a slice past line 1)"
    if not lines or lines.startswith("1-"):
        tl, tl_state = cost.timeline(path, p, KIT_DIR)
    with open(os.path.join(dirp, "timeline.txt"), "w") as f:
        f.write("\n".join(tl) + ("\n" if tl else ""))

    mech, tools, writes = mechanical(dig, spans)
    mech = list(mech) + cost.rows(tl)
    with open(os.path.join(dirp, "mech.tsv"), "w") as f:
        for r in mech:
            f.write("\t".join(r) + "\t-\n")
    open(os.path.join(dirp, "notes.tsv"), "a").close()

    wins = windows(dig, window)
    split = split_plan(dig, rmax)
    kit = {"target": target, "path": path, "lines": lines or "all", "cwd": p.truthy_meta("cwd"), "windows": wins,
           "timeline": tl_state, "tools": tools, "writes": writes, "made": time.time(),
           "engine": p.engine, "na": p.na}
    with open(os.path.join(dirp, "kit.json"), "w") as f:
        json.dump(kit, f)

    counts = {}
    for r in mech:
        counts[r[1]] = counts.get(r[1], 0) + 1
    nbytes = sum(len(x.encode()) + 1 for x in dig)
    print(f"DIR {dirp}")
    print("\n".join(h[:240] for h in dig[:4]))
    print(f"DIGEST {nbytes} B · {len(dig)} lines · windows {len(wins)}: " + " ".join(f"{a}-{b}" for a, b in wins))
    print("TIMELINE " + tl_state)
    print("TOOLS " + (" · ".join(f"{k} {v}" for k, v in sorted(tools.items(), key=lambda x: -x[1])) or "none"))
    print("MECH " + (" · ".join(f"{k} {counts[k]}" for k in MECH if k in counts) or "none"))
    if split and not lines:
        print("SPLIT " + target + f" · {nbytes} B · Lines: " + " ".join(split))


def windows(dig, window):
    """Windows over the digest, cut at event lines: (first, last) digest line numbers."""
    wins, start, size = [], 1, 0
    for i, ln in enumerate(dig, 1):
        if size >= window and EVENT_LINE.match(ln):
            wins.append((start, i - 1))
            start, size = i, 0
        size += len(ln.encode()) + 1
    if start <= len(dig):
        wins.append((start, len(dig)))
    return wins


def split_plan(dig, rmax):
    """The transcript line ranges that each fit a reader, [] when the digest fits one."""
    split, s0, size = [], None, 0
    for ln in dig:
        m = EVENT_LINE.match(ln)
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
    return split


def main(argv):
    verbs = {"prep": prep, "read": rows.read, "find": rows.find, "note": rows.note, "report": rows.report,
             "verify": rows.verify, "prompts": rows.prompts, "writes": rows.writes, "runs": locate.runs, "census": census.census}
    if len(argv) < 2 or argv[1] not in verbs:
        die(USAGE, 2)
    try:
        verbs[argv[1]](argv[2:])
    except Exception as e:
        traceback.print_exc()
        die(f"FAILED {argv[1]} — internal error {type(e).__name__}: {e}")
