"""OpenCode sessions: rows of `opencode.db` (SQLite, opened read-only). A RECORD is one message row or
one part row, numbered in store order (the session's messages by creation, each followed by its parts by
id); that number is the L a line carries. A session is named by its locator `{db path}#{session id}`."""
import os
import urllib.parse
import sqlite3
import json
from datetime import datetime, timezone

from ..model import (CALL, COMPACTED_NOTE, FINAL, HEADER, NOTE, PROMPT, REPLY, SKIPPED, Event,
                     Parsed, dumps, pystr, truthy)

STORE_FILE = "opencode.db"
SESSION_PREFIX = "ses_"
NA = {"NOTICE": "OpenCode has no task-notification record",
      "HOOK": "the kit renders no OpenCode hook record"}
FILE_TOOLS = ("write", "edit", "multiedit")


class StoreError(Exception):
    """A store that cannot be opened or read, or a session it does not hold."""


def split_locator(target):
    """(db path, session id) of `{.../opencode.db}#{id}`, or None."""
    at = target.rfind("#")
    if at <= 0 or at == len(target) - 1 or os.path.basename(target[:at]) != STORE_FILE:
        return None
    return target[:at], target[at + 1:]


def locator(db, sid):
    return f"{db}#{sid}"


def connect(db):
    if not os.path.isfile(db):
        raise StoreError(f"no opencode store at {db}")
    try:
        con = sqlite3.connect("file:" + urllib.parse.quote(db) + "?mode=ro", uri=True, timeout=5)
        con.execute("PRAGMA query_only=ON")
        return con
    except sqlite3.Error as e:
        raise StoreError(f"open opencode store read-only {db}: {e}")


def ms_stamp(ms):
    if not isinstance(ms, (int, float)) or ms <= 0:
        return ""
    return datetime.fromtimestamp(ms / 1000, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.") + f"{int(ms) % 1000:03d}Z"


def sessions(db):
    """Every session of a store as dicts (id, parent_id, title, directory, created, updated), sorted by id."""
    con = connect(db)
    try:
        rows = con.execute("SELECT id, COALESCE(parent_id, ''), title, directory, time_created, time_updated "
                           "FROM session ORDER BY id").fetchall()
    except sqlite3.Error as e:
        raise StoreError(f"query opencode store {db}: {e}")
    finally:
        con.close()
    return [dict(id=r[0], parent_id=r[1], title=r[2] or "", directory=r[3] or "", created=r[4] or 0,
                 updated=r[5] or 0) for r in rows]


def session_rows(db, sid):
    """(session, messages, parts): messages by (created, id), parts by id; a part is (id, message_id,
    created, data)."""
    con = connect(db)
    try:
        s = con.execute("SELECT id, COALESCE(parent_id, ''), title, directory, time_created, time_updated "
                        "FROM session WHERE id = ?", (sid,)).fetchone()
        if s is None:
            raise StoreError(f"no such opencode session {sid} in {db}")
        msgs = con.execute("SELECT id, time_created, data FROM message WHERE session_id = ?", (sid,)).fetchall()
        parts = con.execute("SELECT id, message_id, time_created, data FROM part WHERE session_id = ?",
                            (sid,)).fetchall()
    except sqlite3.Error as e:
        raise StoreError(f"query opencode store {db}: {e}")
    finally:
        con.close()
    msgs.sort(key=lambda r: (r[1] or 0, r[0]))
    parts.sort(key=lambda r: r[0])
    sess = dict(id=s[0], parent_id=s[1], title=s[2] or "", directory=s[3] or "", created=s[4] or 0,
                updated=s[5] or 0)
    return sess, msgs, parts


def _json(data):
    try:
        v = json.loads(data)
    except (TypeError, ValueError):
        return None
    return v if isinstance(v, dict) else None


def load(db, sid):
    loc = locator(db, sid)
    try:
        sess, msgs, parts = session_rows(db, sid)
    except StoreError as e:
        raise _failure(f"cannot read {loc}: {e}")
    p = Parsed(loc, "opencode")
    p.title, p.messages, p.parts = sess["title"], len(msgs), len(parts)
    p.meta["cwd"], p.meta["session"] = sess["directory"], sess["id"]
    child = bool(sess["parent_id"])
    if child:
        p.meta["agent"], p.meta["session"] = sess["id"], sess["parent_id"]
    by_msg = {}
    for part in parts:
        by_msg.setdefault(part[1], []).append(part)
    n = 0
    for row in msgs:
        n += 1
        msg = _message(p, n, row, child)
        mine = by_msg.pop(row[0], [])
        final = ""
        if msg is not None and msg.get("role") == "assistant" and msg.get("finish") == "stop":
            final = _last_text(mine)
        for part in mine:
            n += 1
            summary = msg is not None and msg.get("role") == "assistant" and msg.get("summary") is True
            role = msg["role"] if msg is not None and isinstance(msg.get("role"), str) else ""
            _part(p, n, part, role, part[0] == final, summary)
    for part in parts:
        if part[1] in by_msg:
            n += 1
            p.stamp(ms_stamp(part[2]))
            p.mark(n, SKIPPED, "part (no message)")
    _pair_spawns(p.events)
    p.records = n
    if n == 0:
        raise _failure(f"empty transcript: {loc}")
    p.na.update(NA)
    return p


def _pair_spawns(events):
    """One spawn, one SPAWN: a user message's subtask part and the task call it starts are the same spawn,
    so the SUBTASK note naming a later task call's target gives the mark to the call."""
    paired = set()
    for i, ev in enumerate(events):
        if ev.kind != NOTE or "SPAWN" not in ev.mech or not ev.text.startswith("SUBTASK "):
            continue
        for j in range(i + 1, len(events)):
            later = events[j]
            if j not in paired and later.kind == CALL and later.name == "task" and "SPAWN" in later.mech \
                    and "SUBTASK " + (later.text or "") == ev.text:
                paired.add(j)
                ev.mech.discard("SPAWN")
                break


def _failure(message):
    from . import Failure
    return Failure(message)


def _last_text(parts):
    last = ""
    for pid, _, _, data in parts:
        d = _json(data)
        if d and d.get("type") == "text" and not d.get("ignored") and isinstance(d.get("text"), str) \
                and d["text"].strip():
            last = pid
    return last


def _message(p, n, row, child):
    ts = ms_stamp(row[1])
    p.stamp(ts)
    msg = _json(row[2])
    if msg is None:
        p.mark(n, SKIPPED, "message (unparseable)")
        return None
    if child and "agent_type" not in p.meta and truthy(msg.get("agent")):
        p.meta["agent_type"] = pystr(msg["agent"])
    role = msg.get("role")
    role = role if isinstance(role, str) else ""
    if role == "assistant":
        if "model" not in p.meta and truthy(msg.get("modelID")):
            p.meta["model"] = "/".join(x for x in (pystr(msg.get("providerID") or ""), pystr(msg["modelID"])) if x)
            if truthy(msg.get("variant")):
                p.meta["effort"] = pystr(msg["variant"])
        text = error_text(msg.get("error"))
        if text:
            p.add(Event(n, ts, NOTE, text="ENGINE ERROR — " + text), "message.assistant (error)")
            return msg
        p.mark(n, HEADER, "message.assistant")
    elif role == "user":
        p.mark(n, HEADER, "message.user")
    else:
        p.mark(n, SKIPPED, "message." + (role or "?"))
    return msg


def error_text(err):
    """An error object as "{name}: {data.message}", "" for none."""
    if not isinstance(err, dict):
        return ""
    name = pystr(err["name"]) if "name" in err else ""
    text = ""
    if isinstance(err.get("data"), dict) and "message" in err["data"]:
        text = pystr(err["data"]["message"])
    if text == "" and "message" in err:
        text = pystr(err["message"])
    out = ": ".join(x for x in (name, text) if x)
    return out or dumps(err)


def _part(p, n, row, role, final, summary):
    ts = ms_stamp(row[2])
    p.stamp(ts)
    part = _json(row[3])
    if part is None:
        p.mark(n, SKIPPED, "part (unparseable)")
        return
    kind = part.get("type") if isinstance(part.get("type"), str) else ""
    label = "part." + (kind or "?")
    if not role:
        p.mark(n, SKIPPED, label + " (of an unreadable message)")
        return

    def note(text, mech=None):
        ev = Event(n, ts, NOTE, text=text)
        if mech:
            ev.mech.add(mech)
        p.add(ev, label)

    if kind == "text":
        _text(p, n, ts, label, part, role, final, summary)
    elif kind == "file":
        if role != "user":
            p.mark(n, SKIPPED, label)
            return
        name = part.get("filename") or part.get("mime") or "?"
        p.add(Event(n, ts, PROMPT, text="[file " + pystr(name) + "]"), label)
    elif kind == "tool":
        if not _state_reads(part.get("state")):
            p.mark(n, SKIPPED, "part (unparseable)")
            return
        p.add(_call(n, ts, part), label)
    elif kind == "compaction":
        note(COMPACTED_NOTE, "COMPACTION")
    elif kind == "agent":
        note("AGENT @" + pystr(part.get("name") or ""))
    elif kind == "subtask":
        note("SUBTASK " + pystr(part.get("agent") or "") + ": " + pystr(part.get("description") or ""), "SPAWN")
    elif kind == "retry":
        attempt = pystr(part["attempt"]) if isinstance(part.get("attempt"), int) else "?"
        note("RETRY " + attempt + " — " + error_text(part.get("error")))
    else:
        p.mark(n, SKIPPED, label)


def _text(p, n, ts, label, part, role, final, summary):
    """A text part; an assistant text of a compaction's summary message (`summary: true`) carries COMPACT-SUMMARY."""
    text = part["text"].strip() if isinstance(part.get("text"), str) else ""
    if part.get("ignored"):
        p.mark(n, SKIPPED, label + " (ignored)")
    elif text == "":
        p.mark(n, SKIPPED, label + " (empty)")
    elif role == "user" and part.get("synthetic"):
        p.add(Event(n, ts, NOTE, text=text), label + " (synthetic)")
    elif role == "user":
        p.add(Event(n, ts, PROMPT, text=text), label)
    elif role == "assistant":
        ev = Event(n, ts, FINAL if final else REPLY, text=text)
        if summary:
            ev.mech.add("COMPACT-SUMMARY")
        p.add(ev, label)
    else:
        p.mark(n, SKIPPED, label)


def _state_reads(state):
    """Whether a tool part's state decodes as pfm's openCodeState (status string, output and error strings,
    time.start and time.end numbers, each optional); a mismatch makes the whole part unparseable to pfm."""
    if state is None:
        return True
    if not isinstance(state, dict):
        return False
    for k in ("status", "output", "error"):
        if state.get(k) is not None and not isinstance(state[k], str):
            return False
    t = state.get("time")
    if t is None:
        return True
    if not isinstance(t, dict):
        return False
    return all(t.get(k) is None or (isinstance(t[k], (int, float)) and not isinstance(t[k], bool))
               for k in ("start", "end"))


def _call(n, ts, part):
    name = part["tool"] if isinstance(part.get("tool"), str) and part["tool"] else "?"
    ev = Event(n, ts, CALL, name=name, cmd=name == "bash")
    if name == "task":
        ev.mech.add("SPAWN")
    state = part.get("state")
    if not isinstance(state, dict):
        ev.text = "None"
        return ev
    inp = state.get("input")
    ev.text, ev.raw = target(name, inp), dumps(inp)
    if name in FILE_TOOLS and isinstance(inp, dict) and truthy(inp.get("filePath")):
        ev.writes = [pystr(inp["filePath"])]
    elif name in ("apply_patch", "patch") and isinstance(inp, dict) and isinstance(inp.get("patchText"), str):
        from .codex import PATCH_FILE
        ev.writes = [m.group(2) for m in PATCH_FILE.finditer(inp["patchText"])]
    status = state.get("status")
    if status == "completed":
        out = state.get("output")
        ev.result = out if isinstance(out, str) else ""
    elif status == "error":
        err = state.get("error")
        ev.result, ev.err = (err if isinstance(err, str) else ""), True
    t = state.get("time") if isinstance(state.get("time"), dict) else {}
    if ev.result is not None and isinstance(t.get("start"), (int, float)) and isinstance(t.get("end"), (int, float)):
        ev.dur = (t["end"] - t["start"]) / 1000
    return ev


def target(name, inp):
    """What an OpenCode tool call acts on, as one line names it."""
    if not isinstance(inp, dict):
        return "None" if inp is None else pystr(inp)
    g = inp.get
    if name == "bash":
        return pystr(g("command", ""))
    if name in ("read", "write", "edit", "multiedit"):
        path = pystr(g("filePath", ""))
        if name == "read" and (truthy(g("offset")) or truthy(g("limit"))):
            return f"{path} @{pystr(g('offset', 1))}+{pystr(g('limit', ''))}"
        if name == "edit":
            old, new = pystr(g("oldString", "")), pystr(g("newString", ""))
            return f"{path} (-{old.count(chr(10)) + 1} +{new.count(chr(10)) + 1} lines)"
        return path
    if name in ("grep", "glob", "list"):
        return " ".join(pystr(g(k)) for k in ("pattern", "path", "include") if truthy(g(k)))
    if name == "task":
        return f"{pystr(g('subagent_type', ''))}: {pystr(g('description', ''))}"
    if name in ("apply_patch", "patch") and g("patchText") is not None:
        from .codex import target as codex_target
        return codex_target("apply_patch", g("patchText"))
    for k in ("url", "query", "name"):
        if truthy(g(k)):
            return pystr(g(k))
    return dumps(inp)
