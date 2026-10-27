"""Mechanical labels, computed from events (never by pattern over rendered text). Each engine sets
the facts only it knows on the event (Event.mech, Event.writes, Event.err, Event.dur); the label
rules are one function here, the same on every engine. A row is (line id, label, ref) with the
line id `D{n}` a line of the reader's digest."""
import re

from .digest import UNFINISHED
from .model import CALL, FINAL, NOTE, PROMPT, one_line
from .writes import event_writes

MECH = ("BRIEF PROMPT FINAL SAY-CLAIM NOTE NOTICE COMPACTION COMPACT-SUMMARY COMPACT-CALL HANDOFF "
        "HOOK SPAWN ERROR ERROR-OUTPUT DENIAL AFTER-ERROR NO-RESULT UNFINISHED WAIT WRITE REPEAT "
        "TOTALS MODEL-WAIT COSTLIEST PROMPT-SNAPSHOT INHERITED").split()
DENY = re.compile(r"denied|rejected|doesn't want to proceed|not allowed|\bblocked\b(?!\s*:)|refused|permission", re.I)
SPAWN_TOOLS = ("Agent", "Task", "spawn_agent", "task")
WAIT_SECONDS = 60
NOTE_MECH = ("COMPACTION", "COMPACT-SUMMARY", "NOTICE", "HOOK", "SPAWN", "PROMPT-SNAPSHOT")


def note_label(ev):
    for label in NOTE_MECH:
        if label in ev.mech:
            return label
    return "NOTE"


def mechanical(dig, spans):
    """(rows, tools, writes): the rows in digest order, the call counts per tool, and the writes
    ({D, L, tool, paths}). `dig` is the reader's digest lines, `spans` from digest.build."""
    rows, tools, writes = [], {}, []
    seen_calls, after_err, briefed = {}, None, False
    tail = []  # (error line, [its body lines]) waiting to be flushed

    def flush():
        if not tail:
            return
        at, body = tail.pop()
        if not body:
            return
        picks = [body[0]] + body[-2:] if len(body) > 3 else body
        for t in dict.fromkeys(picks):
            rows.append((f"D{t}", "ERROR-OUTPUT", f"D{at}"))
        if any(DENY.search(dig[t - 1]) for t in body):
            rows.append((f"D{at}", "DENIAL", "-"))

    for ev, first, count in spans:
        flush()
        d = f"D{first}"
        if ev.kind != NOTE and "COMPACT-SUMMARY" in ev.mech:
            rows.append((d, "COMPACT-SUMMARY", "-"))
        elif ev.kind == PROMPT and "INHERITED" in ev.mech:
            rows.append((d, "INHERITED", "-"))  # the parent's prompt a forked child carries, not its brief
        elif ev.kind == PROMPT:
            rows.append((d, "PROMPT" if briefed else "BRIEF", "-"))
            briefed = True
        elif ev.kind == FINAL:
            rows.append((d, "FINAL", "-"))
        elif ev.kind == NOTE:
            rows.append((d, note_label(ev), "-"))
        elif ev.kind == CALL:
            tools[ev.name] = tools.get(ev.name, 0) + 1
            if after_err:
                rows.append((d, "AFTER-ERROR", f"D{after_err}"))
                after_err = None
            if ev.result is None:
                rows.append((d, "NO-RESULT", "-"))
            if ev.err:
                rows.append((d, "ERROR", "-"))
                after_err = first
                tail.append((first, list(range(first + 1, first + count))))
            if ev.dur is not None and round(ev.dur) >= WAIT_SECONDS:
                rows.append((d, "WAIT", "-"))
            low = ev.name.lower()
            if "compact" in low and "handoff" not in low:
                rows.append((d, "COMPACT-CALL", "-"))
            if "handoff" in low:
                rows.append((d, "HANDOFF", "-"))
            if ev.name in SPAWN_TOOLS or "SPAWN" in ev.mech:
                rows.append((d, "SPAWN", "-"))
            # an engine-known write (Edit, Write) whose call failed wrote nothing
            paths = [] if ev.writes is not None and ev.err else event_writes(ev)
            if paths:
                rows.append((d, "WRITE", "-"))
                writes.append({"D": first, "L": ev.line, "tool": ev.name, "paths": paths})
            key = ev.name + " " + one_line(ev.text)
            if key in seen_calls:
                rows.append((d, "REPEAT", f"D{seen_calls[key]}"))
            else:
                seen_calls[key] = first
    flush()
    for i, ln in enumerate(dig, 1):
        if ln == UNFINISHED:
            rows.append((f"D{i}", "UNFINISHED", "-"))
    return rows, tools, writes
