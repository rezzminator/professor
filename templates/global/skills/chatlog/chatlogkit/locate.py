"""Finding transcripts: a target (path, id or prefix, chat name, Codex agent path, OpenCode id, title or
locator) resolved to one transcript, and the `runs` verb (a type's sub-agent runs, a session's agents)."""
import json
import os
import re
import time

from .engines import opencode
from .model import parse_stamp
from .rows import die, opt

NAME_TAIL = 256 * 1024
ROLLOUT_PREFIX = "rollout-"
AGENT_PATH = re.compile(r"^/root(?:/[0-9A-Za-z_-]+)+$")
ID_TARGET = re.compile(r"^[0-9A-Za-z_-]{6,}$")
TAIL_STAMP = re.compile(r'"timestamp"\s*:\s*"([^"]+)"')


class NotFound(Exception):
    """A target that names no transcript, or several."""


def _env_roots(name):
    """The roots a pfm root variable names: a path list, split as pfm splits it (filepath.SplitList)."""
    return [r for r in os.environ.get(name, "").split(os.pathsep) if r]


def claude_roots():
    """PFM_CLAUDE_ROOTS, else pfm's ~/.claude/projects plus CLAUDE_CONFIG_DIR's projects when that is set
    (pfm reads only the first; an account Claude Code keeps elsewhere is searched too)."""
    env = _env_roots("PFM_CLAUDE_ROOTS")
    if env:
        return env
    roots = [os.path.expanduser("~/.claude/projects")]
    if os.environ.get("CLAUDE_CONFIG_DIR"):
        roots.append(os.path.join(os.environ["CLAUDE_CONFIG_DIR"], "projects"))
    out, seen = [], set()
    for r in roots:
        if os.path.realpath(r) not in seen:
            seen.add(os.path.realpath(r))
            out.append(r)
    return out


def codex_homes():
    return _env_roots("PFM_CODEX_ROOT") or [os.path.expanduser("~/.codex")]


def opencode_roots():
    return _env_roots("PFM_OPENCODE_ROOT") or [os.path.expanduser("~/.local/share/opencode")]


def _dirs(path):
    """Subdirectories of path by name, dot names skipped; a missing path holds nothing."""
    try:
        names = sorted(os.listdir(path))
    except FileNotFoundError:
        return [], []
    except OSError as e:
        return [], [f"list {path}: {e}"]
    return [os.path.join(path, n) for n in names if not n.startswith(".") and os.path.isdir(os.path.join(path, n))], []


def find_sessions(roots, prefix):
    found, errs = [], []
    for root in roots:
        projects, e = _dirs(root)
        errs += e
        for pd in projects:
            try:
                names = sorted(os.listdir(pd))
            except OSError as ex:
                errs.append(f"list Claude project {pd}: {ex}")
                continue
            for n in names:
                if n.startswith(prefix) and n.endswith(".jsonl") and not n.startswith(".") \
                        and os.path.isfile(os.path.join(pd, n)):
                    found.append(os.path.join(pd, n))
    return found, errs


def subagent_transcripts(folder, errs=None):
    """The agent-*.jsonl files under folder; a directory the walk cannot list goes to errs as (path, reason)."""
    found = []

    def failed(e):
        if errs is not None:
            errs.append((e.filename or folder, str(e)))
    for dp, dns, fns in os.walk(folder, onerror=failed):
        for f in sorted(fns):
            if f.startswith("agent-") and f.endswith(".jsonl") and not os.path.isdir(os.path.join(dp, f)):
                found.append(os.path.join(dp, f))
    return found


def find_subagents(roots, prefix):
    found, errs = [], []
    for root in roots:
        projects, e = _dirs(root)
        errs += e
        for pd in projects:
            sessions, e = _dirs(pd)
            errs += e
            for sd in sessions:
                d = os.path.join(sd, "subagents")
                if os.path.isdir(d):
                    failed = []
                    found += [p for p in subagent_transcripts(d, failed)
                              if os.path.basename(p).startswith("agent-" + prefix)]
                    errs += [f"list subagents {path}: {reason}" for path, reason in failed]
    return found, errs


def walk_rollouts(home):
    found, errs = [], []
    for sub in ("sessions", "archived_sessions"):
        root = os.path.join(home, sub)
        if not os.path.isdir(root):
            continue
        part = []
        for dp, _, fns in os.walk(os.path.realpath(root), onerror=lambda e: errs.append(str(e))):
            for f in fns:
                p = os.path.join(dp, f)
                if f.startswith(ROLLOUT_PREFIX) and f.endswith(".jsonl") and not os.path.islink(p):
                    part.append(p)
        found += sorted(part)
    return found, errs


def first_text(*vals):
    """The first non-empty string among vals, '' when none."""
    return next((v for v in vals if isinstance(v, str) and v), "")


def rollout_header(path):
    """The session_meta facts of a rollout from its first 40 lines: id, parent (the thread that spawned it),
    role, nickname and agent_path, each '' when absent, and child (a sub-agent rollout). Every fact is empty when
    the rollout has no session_meta; None when the file cannot be read."""
    head = dict(id="", parent="", role="", nickname="", agent_path="", child=False)
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            for _, line in zip(range(40), f):
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                if not isinstance(rec, dict) or rec.get("type") != "session_meta":
                    continue
                pl = rec.get("payload") if isinstance(rec.get("payload"), dict) else {}
                src = pl.get("source") if isinstance(pl.get("source"), dict) else {}
                sub = src.get("subagent") if isinstance(src.get("subagent"), dict) else {}
                spawn = sub.get("thread_spawn") if isinstance(sub.get("thread_spawn"), dict) else {}
                head.update(id=first_text(pl.get("id"), pl.get("session_id")),
                            parent=first_text(spawn.get("parent_thread_id"), pl.get("parent_thread_id")),
                            role=first_text(pl.get("agent_role"), spawn.get("agent_role")),
                            nickname=first_text(pl.get("agent_nickname"), spawn.get("agent_nickname")),
                            agent_path=first_text(rec.get("agent_path"), pl.get("agent_path"),
                                                  spawn.get("agent_path")))
                head["child"] = bool(src.get("subagent")) or bool(head["parent"])
                return head
    except OSError:
        return None
    return head


def rollout_agent_path(path):
    """The agent_path a rollout's session_meta names, '' when none, None when its header cannot be read."""
    head = rollout_header(path)
    return None if head is None else head["agent_path"]


def opencode_sessions():
    """(sessions as (locator, session dict), errors) over every OpenCode store that exists."""
    out, errs = [], []
    for root in opencode_roots():
        db = os.path.join(root, opencode.STORE_FILE)
        if not os.path.exists(db):
            continue
        try:
            for s in opencode.sessions(db):
                out.append((opencode.locator(db, s["id"]), s))
        except opencode.StoreError as e:
            errs.append(str(e))
    return out, errs


def real(p):
    return os.path.realpath(p)


def unique(paths):
    seen, out = set(), []
    for p in paths:
        r = real(p) if os.path.exists(p) else p
        if r not in seen:
            seen.add(r)
            out.append(r)
    return out


def searched():
    roots = [r for r in claude_roots() + codex_homes() if os.path.isdir(r)]
    roots += [os.path.join(r, opencode.STORE_FILE) for r in opencode_roots()
              if os.path.exists(os.path.join(r, opencode.STORE_FILE))]
    return ", ".join(roots) or "no root exists"


def pick(target, hits, unreadable=()):
    hits = unique(hits)
    if unreadable:
        raise NotFound(f"INCOMPLETE lookup for {target!r} — {len(unreadable)} discovery/read errors: "
                       f"{' '.join(unreadable)}")
    if not hits:
        raise NotFound(f"NOT FOUND {target} — searched {searched()}")
    if len(hits) > 1:
        raise NotFound(f"AMBIGUOUS {target} — {len(hits)} transcripts: {' '.join(sorted(hits))}")
    return hits[0]


def id_hits(ident):
    hits, failed = [], []
    if ident.startswith(opencode.SESSION_PREFIX):
        sess, errs = opencode_sessions()
        failed += errs
        hits += [loc for loc, s in sess if s["id"].startswith(ident)]
    found, errs = find_sessions(claude_roots(), ident)
    hits, failed = hits + found, failed + errs
    found, errs = find_subagents(claude_roots(), ident)
    hits, failed = hits + found, failed + errs
    for home in codex_homes():
        rollouts, errs = walk_rollouts(home)
        failed += errs
        hits += [r for r in rollouts if ident in os.path.basename(r)[len(ROLLOUT_PREFIX):]]
    return hits, failed


def tail_title(tail):
    """The title the last custom-title / agent-name record of a transcript's tail names."""
    for line in reversed(tail.split(b"\n")):
        if b"custom-title" not in line and b"agent-name" not in line:
            continue
        try:
            rec = json.loads(line)
        except ValueError:
            continue
        if not isinstance(rec, dict):
            continue
        if rec.get("type") == "custom-title" and isinstance(rec.get("customTitle"), str) and rec["customTitle"]:
            return rec["customTitle"]
        if rec.get("type") == "agent-name" and isinstance(rec.get("agentName"), str) and rec["agentName"]:
            return rec["agentName"]
    return ""


def read_tail(path, n):
    with open(path, "rb") as f:
        size = f.seek(0, 2)
        f.seek(max(0, size - n))
        data = f.read()
    return data if size <= n else data[data.find(b"\n") + 1:]


def by_name(target):
    unreadable, files, pending = [], [], []
    for root in claude_roots():
        if os.path.isdir(root):
            pending.append(root)
        elif os.path.exists(root):
            unreadable.append(f"{root} (not a directory)")
        elif os.path.islink(root):
            unreadable.append(f"{root} (dangling link)")
    visited = set()
    while pending:
        folder = real(pending.pop())
        if folder in visited:
            continue
        visited.add(folder)
        try:
            entries = sorted(os.listdir(folder))
        except OSError as e:
            unreadable.append(f"{folder} ({e})")
            continue
        for n in entries:
            p = os.path.join(folder, n)
            try:
                isdir = os.path.isdir(p)
            except OSError as e:
                unreadable.append(f"{p} ({e})")
                continue
            if isdir:
                pending.append(p)
            elif n.endswith(".jsonl") and not n.startswith("agent-") and not n.startswith(ROLLOUT_PREFIX):
                files.append(p)
    files = unique(files)
    hits = []
    for p in files:
        try:
            tail = read_tail(p, NAME_TAIL)
        except OSError as e:
            unreadable.append(f"{p} ({e})")
            continue
        name = tail_title(tail)
        if name and name.casefold() == target.casefold():
            hits.append((p, tail))
    sess, errs = opencode_sessions()
    unreadable += errs
    named = [(loc, s) for loc, s in sess if s["title"].casefold() == target.casefold()]
    if unreadable:
        raise NotFound(f"INCOMPLETE name lookup for {target!r} — {len(unreadable)} discovery/read errors: "
                       f"{' '.join(unreadable)}")
    total = len(hits) + len(named)
    if total == 1:
        return named[0][0] if named else hits[0][0]
    if total == 0:
        titles = f" and {len(sess)} OpenCode session titles" if sess else ""
        raise NotFound(f"NOT FOUND {target} — searched {searched()} for an id or prefix; the name lookup over "
                       f"{len(files)} session files (the last {NAME_TAIL // 1024} KB of each){titles} found no chat "
                       f"whose last title is {target!r} (a seat name resolves to its id with "
                       f"`pfm chat resolve {target}`, third column)")
    rows = []
    for p, tail in sorted(hits):
        stamps = TAIL_STAMP.findall(tail.decode("utf-8", "replace"))
        try:
            size = f"{os.path.getsize(p)} bytes"
        except OSError as e:
            size = f"size unreadable: {e}"
        rows.append(f"{p} ({size}, last record {stamps[-1] if stamps else 'without a timestamp'})")
    rows += [f"{loc} (OpenCode session)" for loc, _ in named]
    raise NotFound(f"AMBIGUOUS {target} — {len(rows)} transcripts named {target!r}: {' '.join(sorted(rows))}")


def resolve(target):
    """The transcript a target names: a file as given, an OpenCode locator as given, a Codex agent path by its
    rollout's session_meta, an id or prefix by file name or OpenCode session id, else a Claude chat name or an
    OpenCode session title. Raises NotFound with the reason."""
    if os.path.isfile(target):
        return target
    if opencode.split_locator(target):
        return target
    if AGENT_PATH.match(target):
        hits, unreadable = [], []
        for home in codex_homes():
            rollouts, failed = walk_rollouts(home)
            unreadable += failed
            for r in rollouts:
                ap = rollout_agent_path(r)
                if ap is None:
                    unreadable.append(r)
                elif ap == target:
                    hits.append(r)
        return pick(target, hits, unreadable)
    if os.path.isabs(target) or target.startswith("~") or target.startswith(".") or target.endswith(".jsonl"):
        raise NotFound(f"no such transcript file: {target}")
    ident = target[len("agent-"):] if target.startswith("agent-") else target
    hits, failed = id_hits(ident) if ID_TARGET.match(ident) else ([], [])
    if hits or failed:
        return pick(target, hits, failed)
    return by_name(target)


# --- runs ---------------------------------------------------------------------------------------------

def _span(path):
    """(first stamp, last stamp, records, tool calls) of a Claude sub-agent transcript."""
    first = last = None
    records = calls = 0
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            if not line.strip():
                continue
            records += 1
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if not isinstance(rec, dict):
                continue
            ts = parse_stamp(rec.get("timestamp")) if isinstance(rec.get("timestamp"), str) else None
            if ts is not None:
                first = ts if first is None or ts < first else first
                last = ts if last is None or ts > last else last
            if rec.get("type") == "assistant":
                msg = rec.get("message")
                content = msg.get("content") if isinstance(msg, dict) else None
                if isinstance(content, list):
                    calls += sum(1 for b in content if isinstance(b, dict) and b.get("type") == "tool_use")
    return first, last, records, calls


def _size_text(n):
    from .model import size_text
    return size_text(n)


def _agent_row(path):
    base = os.path.basename(path)
    try:
        first, last, records, calls = _span(path)
        size = os.path.getsize(path)
    except OSError as e:
        return None, f"{base} · READ ERROR {e}"
    meta = path[:-len(".jsonl")] + ".meta.json"
    if not os.path.isfile(meta):
        mtext = f"META ERROR missing {meta}"
    else:
        try:
            with open(meta) as f:
                m = json.load(f)
            kind = (m.get("agentType") if isinstance(m, dict) else "") or "type ?"
            desc = (m.get("description") if isinstance(m, dict) else "") or "no description"
            mtext = f"{kind} · {desc}"
        except (OSError, ValueError) as e:
            mtext = f"META ERROR {e}"
    aid = base[len("agent-"):-len(".jsonl")]
    fmt = "%Y-%m-%d %H:%M:%S"
    span = "no timestamps" if first is None else f"{first.strftime(fmt)}Z → {last.strftime(fmt)}Z"
    return first, f"{aid} · {mtext} · {span} · {records} records · {_size_text(size)} · {calls} calls"


def session_agents(path):
    name = os.path.basename(path)
    if name.startswith(ROLLOUT_PREFIX):
        members, errors = codex_family(path)
        head = rollout_header(path)
        lines = [f"SESSION {head['id']} · {path}", f"AGENTS {len(members) - 1}"]
        for member in members[1:]:
            child = rollout_header(member)
            lines.append(f"{child['id']} · {child['agent_path'] or child['nickname']} · "
                         f"{child['role']} · parent {child['parent']} · "
                         f"{_size_text(os.path.getsize(member))} · {member}")
        return lines + [f"UNREADABLE {place} — {reason}" for place, reason in errors]
    if os.path.basename(os.path.dirname(path)) == "subagents" or name.startswith("agent-") \
            or name.startswith(ROLLOUT_PREFIX):
        die(f"KIT FAILED — agents takes a top-level Claude session; {path} is a sub-agent or Codex transcript")
    sid = name[:-len(".jsonl")] if name.endswith(".jsonl") else name
    folder = path[:-len(".jsonl")] + "/subagents" if path.endswith(".jsonl") else path + "/subagents"
    lines = [f"SESSION {sid} · {path}"]
    if not os.path.exists(folder):
        return lines + [f"AGENTS 0 — no subagents directory at {folder}"]
    if not os.path.isdir(folder):
        die(f"KIT FAILED — subagents path is not a directory: {folder}")
    rows = [_agent_row(p) for p in unique(subagent_transcripts(folder))]
    rows.sort(key=lambda r: (r[0] is None, r[0].timestamp() if r[0] else 0, r[1]))
    return lines + [f"AGENTS {len(rows)}"] + [r[1] for r in rows]


def opencode_agents(target):
    db, sid = opencode.split_locator(target)
    kids = [s for s in opencode.sessions(db) if s["parent_id"] == sid]
    lines = [f"SESSION {sid} · {target}", f"AGENTS {len(kids)}"]
    for s in sorted(kids, key=lambda s: (s["created"], s["id"])):
        lines.append(f"{s['id']} · {s['title'] or 'no title'} · {opencode.ms_stamp(s['created'])} → "
                     f"{opencode.ms_stamp(s['updated'])} · {opencode.locator(db, s['id'])}")
    return lines


def since_cutoff(since):
    """The epoch second an `Nh` or `Nd` window reaches back to."""
    m = re.fullmatch(r"(\d+)([hd])", since)
    if not m:
        die(f"KIT FAILED — --since {since}: use Nh or Nd")
    return time.time() - int(m.group(1)) * (3600 if m.group(2) == "h" else 86400)


def read_meta(mp):
    """(meta, ''), or (None, the reason) when a sub-agent's .meta.json is no readable JSON object."""
    try:
        with open(mp) as f:
            meta = json.load(f)
    except (OSError, ValueError) as e:
        return None, str(e)
    if not isinstance(meta, dict):
        return None, "not a JSON object"
    return meta, ""


def type_runs(typ, since, proj):
    """([(mtime of the meta, transcript path, meta)] of the Claude sub-agent runs of agent type typ across
    every project, [(meta path, reason)] of the metas that could not be read). Both `runs` and `census` select
    through here."""
    cutoff = since_cutoff(since)
    found, unreadable = [], []
    for root in claude_roots():
        root = real(root)
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
                        mtime = os.path.getmtime(mp)
                    except OSError as e:
                        unreadable.append((mp, str(e)))
                        continue
                    if mtime < cutoff:
                        continue
                    meta, why = read_meta(mp)
                    if meta is None:
                        unreadable.append((mp, why))
                    elif meta.get("agentType") == typ:
                        found.append((mtime, mp[:-len(".meta.json")] + ".jsonl", meta))
    return found, unreadable


def runs(args):
    sess = opt(args, "--session")
    if sess:
        try:
            path = resolve(sess)
            lines = opencode_agents(path) if opencode.split_locator(path) else session_agents(path)
        except (NotFound, opencode.StoreError) as e:
            die(f"FAILED {sess} — {str(e)[:600]}")
        print("\n".join(lines))
        return
    typ = opt(args, "--type")
    since = opt(args, "--since", "30d")
    proj = opt(args, "--project", "")
    if not typ:
        die("usage: chatlog.py runs --type T [--since 7d] [--project SUBSTR] | --session S", 2)
    found, unreadable = type_runs(typ, since, proj)
    for mp, why in unreadable:
        print(f"UNREADABLE {mp} — {why}")
    listed = []
    for mtime, jp, meta in found:
        sz = os.path.getsize(jp) if os.path.exists(jp) else -1
        listed.append((mtime, sz, meta.get("model") or "-", meta.get("description") or "", jp))
    for t, sz, mo, de, jp in sorted(listed):
        print(f"{time.strftime('%m-%d %H:%M', time.localtime(t))}\t{sz}\t{mo}\t{de[:90]}\t{jp}")
    print(f"# runs {len(found)} of {typ} since {since}")


# --- a session and everything under it ----------------------------------------------------------------

def refuse(path, what):
    die(f"usage: chatlog.py census --session takes a top-level session; {path} is {what}", 2)


def claude_family(path):
    name = os.path.basename(path)
    if os.path.basename(os.path.dirname(path)) == "subagents" or name.startswith("agent-"):
        refuse(path, "a sub-agent transcript")
    folder = path[:-len(".jsonl")] + "/subagents" if path.endswith(".jsonl") else path + "/subagents"
    if not os.path.exists(folder):
        return [path], []
    if not os.path.isdir(folder):
        return [path], [(folder, "the subagents path is not a directory")]
    errs = []
    return [path] + unique(subagent_transcripts(folder, errs)), errs


def codex_family(path):
    head = rollout_header(path)
    if head is None:
        raise NotFound(f"cannot read the rollout {path}")
    if head["child"]:
        refuse(path, "a sub-agent rollout")
    errs, by_parent = [], {}
    for home in codex_homes():
        rollouts, walk_errs = walk_rollouts(home)
        errs += [(home, e) for e in walk_errs]
        for r in rollouts:
            h = rollout_header(r)
            if h is None:
                errs.append((r, "header unreadable — it could be a sub-agent of this session"))
            elif h["parent"]:
                by_parent.setdefault(h["parent"], []).append((r, h["id"]))
    members, taken = [path], {real(path)}
    queue = [head["id"]] if head["id"] else []
    while queue:
        for r, cid in sorted(by_parent.get(queue.pop(0), [])):
            if real(r) in taken:
                continue
            members.append(r)
            taken.add(real(r))
            if cid:
                queue.append(cid)
    return members, errs


def opencode_family(target):
    db, sid = opencode.split_locator(target)
    every = opencode.sessions(db)
    root = next((s for s in every if s["id"] == sid), None)
    if root is None:
        raise NotFound(f"NOT FOUND {sid} — no such opencode session in {db}")
    if root["parent_id"]:
        refuse(target, "a child session")
    kids = {}
    for s in every:
        kids.setdefault(s["parent_id"], []).append(s)
    members, taken, queue = [opencode.locator(db, sid)], {sid}, [sid]
    while queue:
        for s in sorted(kids.get(queue.pop(0), []), key=lambda s: (s["created"], s["id"])):
            if s["id"] not in taken:
                taken.add(s["id"])
                members.append(opencode.locator(db, s["id"]))
                queue.append(s["id"])
    return members, []


def session_members(path):
    """([the session's transcript or locator, then every sub-agent under it], [(place, reason)] the discovery
    could not read). Claude: the subagents folder of the session; Codex: the rollouts whose session_meta
    parent chain reaches the session's id; OpenCode: the descendants by parent_id. A sub-agent or child is
    refused with a usage error naming it. Raises NotFound or opencode.StoreError for a session it cannot read."""
    if opencode.split_locator(path):
        return opencode_family(path)
    if os.path.basename(path).startswith(ROLLOUT_PREFIX):
        return codex_family(path)
    return claude_family(path)
