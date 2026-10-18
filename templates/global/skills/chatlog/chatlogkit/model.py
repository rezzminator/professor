"""The one event model, and the text helpers every engine and the digest share.

An Event is one rendered thing at its transcript line L: a prompt, a reply, a call with its result,
a note or the final reply. A Parsed is one transcript read into events plus the disposition of
every record: rendered, header (folded into the digest header) or skipped (counted by label).
The line number L is pfm's: the 1-based line of a Claude or Codex file (blank lines counted), the
record number of an OpenCode session in store order.
"""
import json
import re
from datetime import datetime, timedelta, timezone

PROMPT, REPLY, CALL, NOTE, FINAL = "prompt", "reply", "call", "note", "final"
RENDERED, HEADER, SKIPPED = "rendered", "header", "skipped"
COMPACTED_NOTE = "COMPACTED — the context was compacted here"
NO_TIMESTAMPS = "no timestamps"
ABSENT = object()  # a JSON member that is not there, apart from one that is null


class Event:
    """kind: prompt|reply|call|note|final. For a call: name, text (its target as one line names it,
    uncapped), raw (its input as text), result (None = no result), err, cmd (a shell command that
    may write), dur (seconds), hidden (folded into a wrapper, never rendered or counted).
    sub: a tag after PROMPT/NOTE or a status suffix of a call (EXIT 1). writes: the file paths the
    call wrote when the engine knows them (None = unknown; the labels derive them from a shell
    command). mech: extra mechanical labels the engine knows ({"SPAWN"}, {"COMPACTION"}, ...)."""
    __slots__ = ("line", "ts", "kind", "name", "text", "sub", "raw", "result", "err", "cmd", "hidden",
                 "dur", "children", "mech", "writes")

    def __init__(self, line, ts, kind, name="", text="", sub="", raw="", cmd=False):
        self.line, self.ts, self.kind, self.name, self.text = line, ts, kind, name, text
        self.sub, self.raw, self.cmd = sub, raw, cmd
        self.result, self.err, self.hidden, self.dur, self.children = None, False, False, None, 0
        self.mech, self.writes = set(), None

    def set_result(self, text, failed, ts):
        """A call's result; its duration, once, from the call's and the result's stamps."""
        self.result, self.err = text, bool(failed)
        a, b = parse_stamp(self.ts), parse_stamp(ts)
        if a and b and self.dur is None:
            self.dur = (b - a).total_seconds()


class Parsed:
    """One transcript read into events. `disp[line] = (state, label)` for every record, `order` the
    lines in first-marked order; `meta` the header facts (session, agent, cwd, model, effort,
    agent_type, description, meta_error, cli); `nulls` the meta keys whose value was JSON null;
    `na` maps a mechanical label the engine has no concept of to the reason (header row
    `LABEL n/a (reason)`); `unparsed` the lines holding no JSON object."""

    def __init__(self, path, engine):
        self.path, self.engine = path, engine
        self.bytes, self.records, self.messages, self.parts = 0, 0, 0, 0
        self.events, self.order, self.disp = [], [], {}
        self.meta, self.nulls, self.title = {}, set(), ""
        self.t0 = self.t1 = None
        self.pending, self.calls, self.wrappers = {}, {}, []
        self.na, self.unparsed = {}, []

    def mark(self, line, state, label):
        if line not in self.disp:
            self.order.append(line)
        self.disp[line] = (state, label)

    def add(self, ev, label):
        self.events.append(ev)
        self.mark(ev.line, RENDERED, label)

    def set_meta(self, key, value):
        self.meta.setdefault(key, value)

    def set_meta_value(self, key, value):
        """Set key, unless set, to str(value); a JSON null prints "None" and counts as absent."""
        if key in self.meta:
            return
        self.meta[key] = "" if value is ABSENT else pystr(value)
        if value is None:
            self.nulls.add(key)

    def truthy_meta(self, key):
        return "" if key in self.nulls else self.meta.get(key, "")

    def stamp(self, ts):
        at = parse_stamp(ts)
        if at is None:
            return
        if self.t0 is None or at < self.t0:
            self.t0 = at
        if self.t1 is None or at > self.t1:
            self.t1 = at


# ------------------------------------------------------------------ text helpers (Python's own)

_ISO = re.compile(r"^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?"
                  r"(?:[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|[+-]\d{2}:?\d{2})?)?$")


def parse_stamp(ts):
    """A record's timestamp string as an aware UTC datetime, as pfm's ParseTimestamp reads it (a stamp
    without an offset is UTC; a day past the month's end rolls over; 24:00 is the next midnight); None
    when absent, not a string or unreadable."""
    if not isinstance(ts, str) or not ts:
        return None
    m = _ISO.match(ts)
    if not m:
        return None
    y, mo, d, h, mi, s, frac, off = m.groups()
    mo, d, h, mi, s = int(mo or 1), int(d or 1), int(h or 0), int(mi or 0), int(s or 0)
    us = int(((frac or "") + "00000")[:6] or 0)
    if not (1 <= mo <= 12 and 1 <= d <= 31 and mi <= 59 and s <= 59 and h <= 24) or (h == 24 and mi + s + us):
        return None
    try:
        at = datetime(int(y), mo, 1, tzinfo=timezone.utc) + timedelta(days=d - 1, hours=h, minutes=mi,
                                                                      seconds=s, microseconds=us)
    except (ValueError, OverflowError):
        return None
    if off and off != "Z":
        digits = off[1:].replace(":", "")
        sec = (int(digits[:2]) * 60 + int(digits[2:])) * 60
        try:
            at -= timedelta(seconds=sec if off[0] == "+" else -sec)
        except OverflowError:
            return None
    return at


def truthy(v):
    """Python's truth of a decoded JSON value; an absent member is false."""
    return v is not ABSENT and bool(v)


def get(obj, key):
    """obj[key] when obj is a dict holding it, else ABSENT."""
    return obj.get(key, ABSENT) if isinstance(obj, dict) else ABSENT


def pystr(v):
    """Python's str() of a decoded JSON value, as the digest prints it."""
    return v if isinstance(v, str) else str(v)


def dumps(v):
    """json.dumps(value, ensure_ascii=False), the digest's rendering of a structured input."""
    return json.dumps(v, ensure_ascii=False)


def one_line(s):
    return re.sub(r"\s+", " ", s).strip()


def cap_text(s, limit):
    """Text cut to limit characters with the cut counted; limit <= 0 keeps it whole."""
    if limit <= 0 or len(s) <= limit:
        return s
    return s[:limit] + f"…[+{len(s) - limit}]"


def size_text(n):
    if n < 1000:
        return f"{n}B"
    if n < 1_000_000:
        return f"{n / 1000:.1f}K"
    return f"{n / 1_000_000:.1f}M"


def clock_text(ts):
    at = parse_stamp(ts)
    return at.strftime("%H:%M:%S") if at else "--:--:--"


def member(obj, key):
    """obj[key] when obj is a dict, else None."""
    return obj.get(key) if isinstance(obj, dict) else None


def flat_content(content):
    """A message or result content as text: a string whole, a list/dict by block (text; [image];
    [tool name]; anything else as its JSON), blocks joined by newlines."""
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, dict):
        content = [content]
    if not isinstance(content, list):
        return pystr(content)
    parts = []
    for b in content:
        if isinstance(b, str):
            parts.append(b)
        elif isinstance(b, dict):
            if isinstance(b.get("text"), str):
                parts.append(b["text"])
            elif b.get("type") in ("image", "input_image"):
                parts.append("[image]")
            elif b.get("type") == "tool_reference":
                parts.append("[tool " + pystr(b.get("tool_name") or "") + "]")
            else:
                parts.append(dumps(b))
    return "\n".join(parts)
