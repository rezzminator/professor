"""The digest: a Parsed rendered as `pfm transcript show` prints it (same header, event lines and
line numbers), plus the header rows the kit adds: RECORDS (the accounting of every record), DROPPED
(one row per dropped-record label), UNPARSED and `LABEL n/a (reason)`. The kit asks for pfm's defaults
(only prompt,reply,call,note,final; results brief; text 500; width 180) and a `--lines` span."""
import re

from .model import (CALL, FINAL, HEADER, NOTE, NO_TIMESTAMPS, PROMPT, REPLY, SKIPPED,
                    cap_text, clock_text, one_line, size_text)

DEFAULT_ONLY = "prompt,reply,call,note,final"
UNFINISHED = "UNFINISHED — the last turn has no final reply: the run was cut off or is still running"
LINE_SPAN = re.compile(r"^(\d+)(?:-(\d*))?$")
UNPARSEABLE = "unparseable line"


class Request:
    def __init__(self, lines="", results="brief", text=500, width=180, final=0, err_lines=12):
        self.lines, self.results, self.text = lines, results, text
        self.width, self.final, self.err_lines = width, final, err_lines
        self.only = DEFAULT_ONLY


def line_bounds(spec):
    m = LINE_SPAN.match(spec)
    if not m:
        raise ValueError(f"--lines {spec!r}: FROM-TO, FROM- or one line number")
    low = high = float(m.group(1))
    if "-" in spec:
        high = float(m.group(2)) if m.group(2) else float("inf")
    if low > high:
        raise ValueError(f"--lines {spec}: FROM is after TO")
    return low, high


def select(p, req):
    kinds = {k.strip() for k in req.only.split(",") if k.strip()}
    events = [e for e in p.events if e.kind in kinds and not e.hidden]
    if req.lines:
        low, high = line_bounds(req.lines)
        events = [e for e in events if low <= e.line <= high]
    return events


def last_text_lines(text, count, width):
    if count <= 0:
        return []
    lines = [ln.rstrip() for ln in text.splitlines() if ln.strip()]
    return [cap_text(ln, width) for ln in lines[max(0, len(lines) - count):]]


def render_call(ev, req):
    head = f"L{ev.line} {clock_text(ev.ts)} {ev.name} {cap_text(one_line(ev.text), req.width)}"
    if ev.result is None:
        return [head + " → NO RESULT"]
    if ev.sub and ev.err:
        status = "ERR " + ev.sub
    elif ev.sub:
        status = ev.sub
    elif ev.err:
        status = "ERR"
    else:
        status = "ok"
    head += f" → {status} {size_text(len(ev.result))}"
    if ev.dur is not None and ev.dur >= 5:
        head += f" · {ev.dur:.0f}s"
    body = []
    mode = req.results
    if mode == "full":
        body = ev.result.splitlines()
    elif mode.startswith("tail:"):
        body = last_text_lines(ev.result, int(mode[5:]), 200)
    elif ev.err and mode != "none":
        body = last_text_lines(ev.result, req.err_lines, 200)
    elif mode == "brief" and ev.cmd:
        last = last_text_lines(ev.result, 1, 140)
        if last:
            head += " ‹" + last[0] + "›"
    return [head] + ["    | " + ln for ln in body]


def render_event(ev, req):
    if ev.kind == CALL:
        return render_call(ev, req)
    if ev.kind == FINAL:
        lines = [f"L{ev.line} {clock_text(ev.ts)} FINAL"]
        return lines + ["  " + ln for ln in cap_text(ev.text, req.final).splitlines()]
    tag = {PROMPT: "PROMPT", REPLY: "SAY", NOTE: "NOTE"}[ev.kind]
    if ev.sub:
        tag += f"({ev.sub})"
    limit = min(req.text, 300) if ev.kind == NOTE and req.text > 0 else req.text
    return [f"L{ev.line} {clock_text(ev.ts)} {tag} {cap_text(one_line(ev.text), limit)}"]


class Tally:
    """Counts in the order first seen, Python's Counter."""

    def __init__(self):
        self.counts = {}

    def add(self, key):
        self.counts[key] = self.counts.get(key, 0) + 1

    def total(self):
        return sum(self.counts.values())

    def most_common(self):
        return sorted(self.counts, key=lambda k: -self.counts[k])

    def text(self):
        return " · ".join(f"{k} {self.counts[k]}" for k in self.most_common())


def accounting(p):
    """Rendered, header, dropped (skipped but unparseable lines), unparsed record counts and the
    dropped-label tally: every record is exactly one of them."""
    rendered = header = 0
    dropped = Tally()
    for line in p.order:
        state, label = p.disp[line]
        if state == "rendered":
            rendered += 1
        elif state == HEADER:
            header += 1
        elif label == UNPARSEABLE:
            continue
        else:
            dropped.add(label)
    return rendered, header, dropped, len(p.unparsed)


def header_rows(p, req, shown):
    meta = lambda key, default: p.meta.get(key, default)
    who = meta("session", "?")
    if p.meta.get("agent"):
        who = f"agent {p.meta['agent']} of session {who}"
    role = " · ".join(x for x in (p.meta.get("agent_type", ""), p.meta.get("description", "")) if x)
    model = " ".join(x for x in (p.truthy_meta("model"), p.truthy_meta("effort")) if x) or "model ?"
    first = f"TRANSCRIPT {p.engine} · {who} · {model}" + (f" · {role}" if role else "")
    if p.engine == "opencode":
        source = (f"STORE {p.path} · {p.records} records: {p.messages} messages, {p.parts} parts (L = record "
                  f"number in store order; no file lines or bytes) · cwd {meta('cwd', '?')} (shown as ./)")
    else:
        source = f"FILE {p.path} · {p.bytes} bytes · {p.records} records · cwd {meta('cwd', '?')} (shown as ./)"
    rows = [first, source]
    if p.title:
        rows.append("NAME " + p.title)
    if p.t0 is None:
        span = NO_TIMESTAMPS
    else:
        span = (f"{p.t0.strftime('%Y-%m-%d %H:%M:%S')}Z → {p.t1.strftime('%H:%M:%S')}Z · "
                f"{int((p.t1 - p.t0).total_seconds())}s wall")
    rows.append("SPAN " + span)
    per, errs, calls, unanswered = Tally(), Tally(), 0, 0
    for ev in p.events:
        if ev.kind != CALL or ev.hidden:
            continue
        calls += 1
        per.add(ev.name)
        if ev.err:
            errs.add(ev.name)
        if ev.result is None:
            unanswered += 1
    tally = [f"{n} {per.counts[n]}" + (f" ({errs.counts[n]} err)" if errs.counts.get(n) else "")
             for n in per.most_common()] or ["none"]
    rows.append(f"CALLS {calls}, {errs.total()} err, {unanswered} without result: " + " · ".join(tally))
    if p.meta.get("meta_error"):
        rows.append("META UNREADABLE " + p.meta["meta_error"])
    rendered, head, dropped, unparsed = accounting(p)
    rows.append(f"RECORDS {p.records} = {rendered} rendered + {head} header + {dropped.total()} dropped + "
                f"{unparsed} unparsed")
    rows += [f"DROPPED {k} {dropped.counts[k]}" for k in dropped.most_common()]
    if p.unparsed:
        rows.append("UNPARSED " + " ".join(f"L{n}" for n in p.unparsed[:20])
                    + (f" … +{len(p.unparsed) - 20}" if len(p.unparsed) > 20 else ""))
    rows += [f"{k} n/a ({v})" for k, v in p.na.items()]
    total = sum(1 for e in p.events if not e.hidden)
    flt = "FILTER only " + req.only + (f" · lines {req.lines}" if req.lines else "")
    rows.append(flt + f" · results {req.results} · text {req.text} · shown {shown} of {total} events")
    return rows


def skipped_line(p):
    skipped, head = Tally(), Tally()
    for line in p.order:
        state, label = p.disp[line]
        if state == SKIPPED:
            skipped.add(label)
        elif state == HEADER:
            head.add(label)
    out = f"SKIPPED {skipped.total()} records"
    if skipped.total():
        out += ": " + skipped.text()
    if head.total():
        out += f" | HEADER {head.total()}: {head.text()}"
    return out


def shorten(text, p, home):
    cwd = "" if "cwd" in p.nulls else p.meta.get("cwd", "").replace("file://", "").rstrip("/")
    if len(cwd) > 1:
        text = text.replace(cwd + "/", "./")
    if len(home) > 1:
        text = text.replace(home + "/", "~/")
    return text


def build(p, req, home):
    """(lines, spans): the digest lines and, per event shown, (event, first line number from 1,
    line count); the UNFINISHED line is the last of `lines` before SKIPPED when it applies."""
    events = select(p, req)
    lines = header_rows(p, req, len(events))
    spans = []
    for ev in events:
        out = render_event(ev, req)
        spans.append((ev, len(lines) + 1, len(out)))
        lines += out
    for ev in reversed(p.events):
        if ev.kind == FINAL:
            break
        if ev.kind in (PROMPT, REPLY, CALL):
            lines.append(UNFINISHED)
            break
    lines.append(skipped_line(p))
    for i in range(2, len(lines)):
        lines[i] = shorten(lines[i], p, home)
    return lines, spans


def show(p, req, home):
    """The digest text of a parsed transcript."""
    return "\n".join(build(p, req, home)[0]) + "\n"


# ------------------------------------------------------------------ the reader's digest

NOTIF = re.compile(r"<task-notification>.*?(?:<status>(.*?)</status>)?.*?(?:<summary>(.*?)</summary>)?", re.S)
REMINDER = re.compile(r"<system-reminder>.*?(</system-reminder>|$)", re.S)
EVENT_LINE = re.compile(r"^L(\d+) (\S+) (\S+)")


def clean(line):
    """The LLM digest line: a task notification collapsed to its status and summary, system reminders
    cut, a compact summary cut to 400 characters; the L number and the kind kept."""
    m = EVENT_LINE.match(line)
    if not m:
        return line
    if "<task-notification>" in line:
        n = NOTIF.search(line)
        st = (n.group(1) or "?") if n else "?"
        sm = ((n.group(2) or "") if n else "").strip()
        return f"L{m.group(1)} {m.group(2)} NOTE task-notification · {st} · {sm[:200]}"
    line = REMINDER.sub("", line)
    if " COMPACT SUMMARY " in line:
        return line[:400] + ("…" if len(line) > 400 else "")
    return line
