"""The timeline of a Claude Code transcript: one row per model call, priced from the usage block the
transcript records."""
import json
import math
import os
import re

from ..model import parse_stamp, truthy
from . import prices as P
from .fmt import (NOT_PRICED, advisor_text, call_line, cash, clip, count, epoch_ms, fixed, head as head_line, hms,
                  tools_text, utf16_len)

BIG = 20 * 1024
ADVISOR = "advisor_message"
COALESCE_TARGET = ("file_path", "command", "pattern", "path", "subagent_type", "skill", "url", "query")


class Unreadable(Exception):
    pass


def key_string(v):
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, dict):
        return "[object Object]"
    if isinstance(v, list):
        return ",".join("" if x is None else key_string(x) for x in v)
    if isinstance(v, float) and v.is_integer():
        return str(int(v))
    return str(v)


def stringified_length(v):
    return utf16_len(json.dumps(v, ensure_ascii=False, separators=(",", ":")))


def result_chars(content):
    if isinstance(content, str):
        return utf16_len(content)
    if isinstance(content, list):
        total = 0
        for b in content:
            kind = b.get("type") if isinstance(b, dict) else None
            if kind == "text":
                total += utf16_len(b["text"]) if isinstance(b.get("text"), str) else 0
            elif kind == "image":
                total += 6000
            else:
                total += stringified_length(b)
        return total
    return stringified_length(content) if truthy(content) else 0


def tool_target(inp):
    if not isinstance(inp, dict):
        return ""
    target = ""
    for k in COALESCE_TARGET:
        if inp.get(k) is not None:
            target = key_string(inp[k])
            break
    else:
        for v in inp.values():
            if isinstance(v, str):
                target = v
                break
    return clip(target)


class Usage:
    """A response's usage block as the transcript records it."""

    def __init__(self, u):
        if not isinstance(u, dict):
            raise ValueError("usage is not an object")
        self.input, self.output = count(u.get("input_tokens")), count(u.get("output_tokens"))
        self.cache_read = count(u.get("cache_read_input_tokens"))
        self.cache_creation = count(u.get("cache_creation_input_tokens"))
        b = u.get("cache_creation")
        self.breakdown = (count(b.get("ephemeral_5m_input_tokens")), count(b.get("ephemeral_1h_input_tokens"))) \
            if isinstance(b, dict) else None
        self.kind = u.get("type") if isinstance(u.get("type"), str) else ""
        self.model = u.get("model") if isinstance(u.get("model"), str) else ""
        self.iterations = [Usage(i) for i in u["iterations"]] if isinstance(u.get("iterations"), list) else []
        d = u.get("output_tokens_details")
        self.thinking = count(d.get("thinking_tokens")) if isinstance(d, dict) else 0

    def is_advisor(self):
        return self.kind == ADVISOR

    def is_executor(self):
        return self.kind in ("", "message")

    def billed(self):
        """((input, output, cache read, write5m, write1h), split)."""
        f_in, f_out, f_cr, f_cc, bd = self.input, self.output, self.cache_read, self.cache_creation, self.breakdown
        if f_in + f_out + f_cr + f_cc == 0 and self.iterations:
            f_in = f_out = f_cr = f_cc = 0
            b5 = b1 = 0
            for it in self.iterations:
                if it.is_advisor():
                    continue
                f_in, f_out, f_cr, f_cc = f_in + it.input, f_out + it.output, f_cr + it.cache_read, f_cc + it.cache_creation
                if it.breakdown:
                    b5, b1 = b5 + it.breakdown[0], b1 + it.breakdown[1]
            bd = (b5, b1)
        w5, w1 = bd if bd else (0, 0)
        return (f_in, f_out, f_cr, w5 + max(f_cc - w5 - w1, 0), w1), w5 + w1 > 0

    def advisors(self):
        out = []
        for it in self.iterations:
            if it.is_advisor():
                u, split = it.billed()
                if any(u):
                    out.append((it.model, u, split))
        return out

    def unknown_iterations(self):
        return [it.kind for it in self.iterations if not it.is_executor() and not it.is_advisor()]

    def requests(self):
        billed, _ = self.billed()
        empty = self.input + self.output + self.cache_read + self.cache_creation == 0
        reqs, total = [], (0, 0, 0, 0, 0)
        for it in self.iterations:
            if it.is_advisor() or (not empty and not it.is_executor()):
                continue
            r, _ = it.billed()
            if not any(r):
                continue
            reqs.append(r)
            total = tuple(a + b for a, b in zip(total, r))
        if empty:
            return reqs
        if reqs and total == billed:
            return reqs
        return [billed]


def context(u):
    return u[0] + u[2] + u[3] + u[4]


class Scan:
    def __init__(self):
        self.bad = self.no_stamp = self.synthetic = self.unpriced = self.tier_unknown = self.identity = 0
        self.copied = self.tool_errors = 0
        self.unpriced_models, self.unknown_iter, self.read_errors = {}, {}, []


def read(path):
    """The records of a transcript, each (line number, dict) or (line number, None) for a malformed line."""
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            return [(n, _json(line)) for n, line in enumerate(f, 1) if line.strip()]
    except OSError as e:
        raise Unreadable(f"read {path}: {e}")


def _json(line):
    try:
        v = json.loads(line)
    except ValueError:
        return None
    return v if isinstance(v, dict) and isinstance(v.get("type"), str) else None


def subagent_meta(path, scan):
    base = os.path.basename(path)
    sub = base.startswith("agent-") and base.endswith(".jsonl") and f"{os.sep}subagents{os.sep}" in path
    meta = {}
    if sub:
        mp = path[:-len(".jsonl")] + ".meta.json"
        try:
            with open(mp) as f:
                m = json.load(f)
            meta = m if isinstance(m, dict) else {}
        except FileNotFoundError:
            pass
        except (OSError, ValueError) as e:
            scan.read_errors.append(f"read sub-agent meta {mp}: {e}")
    return sub, meta


def call_key(r, msg, path, n):
    """pfm's callKey: the message id, else the request id, else the record uuid, else the line."""
    mid = msg["id"] if isinstance(msg.get("id"), str) else ""
    rid = r["requestId"] if isinstance(r.get("requestId"), str) else ""
    uid = r["uuid"] if isinstance(r.get("uuid"), str) else ""
    return ("message:" + mid if mid else "request:" + rid if rid else "uuid:" + uid if uid
            else f"at:{path}:{n}")


TOKEN = re.compile(rb'"([0-9A-Za-z_.:-]+)"')
SESSION_ID = re.compile(rb'"(?:sessionId|session_id)"\s*:\s*"([0-9A-Za-z_.:-]+)"')
FORK_ORIGIN = re.compile(rb'"forkedFrom"\s*:\s*\{[^}]*?"sessionId"\s*:\s*"([0-9A-Za-z_.:-]+)"')


def _walk(d, out, scan):
    try:
        entries = sorted(os.scandir(d), key=lambda e: e.name)
    except OSError as e:
        scan.read_errors.append(f"{d}: {e}")
        return
    for e in entries:
        try:
            if e.is_dir(follow_symlinks=False):
                _walk(e.path, out, scan)
            elif e.name.endswith(".jsonl"):
                out.append(e.path)
        except OSError as err:
            scan.read_errors.append(f"{e.path}: {err}")


def owners(path, keys, scan):
    """pfm's callOrigins over the transcript's root: the owner path of every call in keys that more than
    one transcript holds — the holder another copy names as its origin (session_id or forkedFrom), else
    the first holder not marked forkedFrom, in path order; "" when every holder is a marked fork copy."""
    want = {k for k in keys if not k.startswith("at:")}
    if not want:
        return {}
    me = os.path.abspath(path)
    domain = os.path.dirname(me)
    at = me.find(f"{os.sep}subagents{os.sep}")
    if at >= 0:
        domain = os.path.dirname(me[:at])
    # The transcript's own project directory, never the whole projects root: pfm's default scans every
    # project (~3,000 directories, ~30 s) for fork copies that live beside their origin in practice.
    found, seen, files = [], set(), []
    _walk(domain, found, scan)
    for f in found:
        tail = "/".join(f.split(os.sep)[-3:])
        if tail not in seen:
            seen.add(tail)
            files.append(f)
    if me not in files:
        files.append(me)
    needles = {k.split(":", 1)[1].encode() for k in want}
    exact = all(TOKEN.fullmatch(b'"' + v + b'"') for v in needles)
    # Cheap gates before the token scan: a copy of this transcript's calls carries one of its session ids
    # (its records' sessionId, or forkedFrom naming it) and was written after it began; the origin this
    # transcript names in forkedFrom is read unconditionally.
    try:
        with open(me, "rb") as fh:
            mine = fh.read()
    except OSError as e:
        scan.read_errors.append(f"{me}: {e}")
        mine = b""
    ids = set(SESSION_ID.findall(mine))
    origins = {o.decode() for o in FORK_ORIGIN.findall(mine)}
    born = getattr(os.stat(me), "st_birthtime", None) if os.path.exists(me) else None
    holders = {}
    for f in files:
        if f != me and not any(f"{os.sep}{o}.jsonl" in f or f"{os.sep}{o}{os.sep}" in f for o in origins):
            try:
                if born is not None and os.path.getmtime(f) < born - 60:
                    continue
            except OSError as e:
                scan.read_errors.append(f"{f}: {e}")
                continue
        try:
            with open(f, "rb") as fh:
                data = mine if f == me else fh.read()
        except OSError as e:
            scan.read_errors.append(f"{f}: {e}")
            continue
        if f != me and ids and not any(i in data for i in ids) and not any(o.encode() in f.encode() for o in origins):
            continue
        if f != me and exact and not needles.intersection(TOKEN.findall(data)):
            continue
        for n, line in enumerate((x for x in data.split(b"\n") if x.strip()), 1):
            r = _json(line.decode("utf-8", "replace"))
            if r is None or r["type"] != "assistant" or not isinstance(r.get("message"), dict):
                continue
            msg = r["message"]
            if not truthy(msg.get("usage")):
                continue
            try:
                Usage(msg["usage"])
            except ValueError:
                continue
            key = call_key(r, msg, f, n)
            if key not in want:
                continue
            session = key_string(r["sessionId"]) if truthy(r.get("sessionId")) else os.path.basename(f)[:-len(".jsonl")]
            fork = r["forkedFrom"].get("sessionId") if isinstance(r.get("forkedFrom"), dict) else None
            named = key_string(fork) if truthy(fork) else key_string(r["session_id"]) if truthy(r.get("session_id")) else ""
            h = holders.setdefault(key, {}).setdefault(f, dict(session=session, named=set(), fork=False))
            if named and named != session:
                h["named"].add(named)
            if truthy(r.get("forkedFrom")):
                h["fork"] = True
    out = {}
    for key, by_file in holders.items():
        if len(by_file) < 2:
            continue
        named = set().union(*(h["named"] for h in by_file.values()))
        paths = sorted(by_file)
        owner = next((p for p in paths if not by_file[p]["fork"] and by_file[p]["session"] in named), "")
        out[key] = owner or next((p for p in paths if not by_file[p]["fork"]), "")
    return out


def run(path, book):
    """(header text, row lines, scan) of the transcript at path; header None when it holds no call."""
    recs = read(path)
    scan = Scan()
    if recs and all(r is None for _, r in recs):
        raise Unreadable(f"no line parsed as JSON ({len(recs)} malformed)")
    sub, meta = subagent_meta(path, scan)
    sid = os.path.basename(os.path.dirname(os.path.dirname(path))) if sub else os.path.basename(path)[:-len(".jsonl")]
    uses, slots, usage, seq = {}, {}, {}, []
    t0, t1, big = math.inf, -math.inf, 0
    for n, r in recs:
        if r is None:
            scan.bad += 1
            continue
        ts = parse_stamp(r["timestamp"]) if truthy(r.get("timestamp")) and isinstance(r.get("timestamp"), str) else None
        ts = epoch_ms(ts) if ts is not None else math.nan
        if not math.isnan(ts):
            t0, t1 = min(t0, ts), max(t1, ts)
        kind = r["type"]
        msg = r.get("message") if isinstance(r.get("message"), dict) else None
        if kind in ("custom-title", "agent-name", "ai-title", "cost-state"):
            continue
        try:
            blk = Usage(msg["usage"]) if msg is not None and truthy(msg.get("usage")) else None
        except ValueError:
            scan.bad += 1
            continue
        content = msg.get("content") if msg else None
        if kind == "assistant" and blk is not None:
            model = msg.get("model") if isinstance(msg.get("model"), str) else ""
            billed, _ = blk.billed()
            if model == "<synthetic>" or (not any(billed) and not blk.advisors()):
                scan.synthetic += 1
                continue
            if math.isnan(ts):
                scan.no_stamp += 1
                continue
            if not truthy(msg.get("id")) and not truthy(r.get("requestId")):
                scan.identity += 1
            key = call_key(r, msg, path, n)
            fork = r["forkedFrom"].get("sessionId") if isinstance(r.get("forkedFrom"), dict) else None
            fork = key_string(fork) if truthy(fork) else ""
            cur = key_string(r["sessionId"]) if truthy(r.get("sessionId")) else sid
            if key not in usage:
                seq.append(("call", key))
            eff = "-"
            for k in ("effort", "perTurnEffort"):
                if r.get(k) is not None:
                    eff = key_string(r[k])
                    break
            usage[key] = dict(blk=blk, model=model, nomodel=msg.get("model") is None, ts=ts, effort=eff,
                              copied=bool(fork and fork != cur), tools=usage.get(key, {}).get("tools", []))
            for b in content if isinstance(content, list) else []:
                if isinstance(b, dict) and b.get("type") == "tool_use":
                    inp = b.get("input") if truthy(b.get("input")) else {}
                    bid = key_string(b.get("id")) if b.get("id") is not None else "undefined"
                    uses[bid] = (key_string(b.get("name")) if b.get("name") is not None else "undefined", ts)
                    slot = dict(name=uses[bid][0], target=tool_target(inp), chars=0, has=False, err=False, dur=0.0)
                    slots[bid] = slot
                    usage[key]["tools"].append(slot)
        elif kind == "user" and isinstance(content, list):
            for b in content:
                if not (isinstance(b, dict) and b.get("type") == "tool_result"):
                    continue
                tid = key_string(b.get("tool_use_id")) if b.get("tool_use_id") is not None else "undefined"
                use = uses.get(tid)
                chars = result_chars(b.get("content"))
                dur = max(0.0, ts - use[1]) if use and not math.isnan(ts) else 0.0
                err = truthy(b.get("is_error"))
                seq.append(("tool", ts, err))
                if chars > BIG:
                    big += 1
                if tid in slots:
                    slots[tid].update(chars=chars, has=True, err=err, dur=dur)
    # replay
    owned = owners(path, [k for k in usage], scan)
    me = os.path.abspath(path)
    book_loaded = book.loaded
    models, efforts = {}, {}
    rows, calls, tok_out, ctx_peak, usd_run, unpriced_run = [], 0, 0, 0.0, 0.0, False
    adv = dict(calls=0, unpriced=0, usd=0.0)
    last_tool = math.nan
    for item in seq:
        if item[0] == "tool":
            if item[2]:
                scan.tool_errors += 1
            if not math.isnan(item[1]):
                last_tool = item[1]
            continue
        c = usage[item[1]]
        blk, requests = c["blk"], c["blk"].requests()
        if c["copied"] or (item[1] in owned and owned[item[1]] != me):
            scan.copied += 1
            continue
        for kind in blk.unknown_iterations():
            scan.unknown_iter[kind] = scan.unknown_iter.get(kind, 0) + 1
        mkey = "undefined" if c["nomodel"] else c["model"]
        # advisor consultations: their own model, their own context
        a_usd, a_calls, a_unpriced = 0.0, 0, 0
        for a_model, au, a_split in blk.advisors():
            tok_out += au[1]
            a_calls += 1
            row = book.resolve(a_model) if book_loaded else None
            parts = P.response_parts(row, au) if row else None
            if parts is None:
                scan.unpriced += 1
                scan.unpriced_models[a_model or "(none)"] = scan.unpriced_models.get(a_model or "(none)", 0) + 1
                a_unpriced += 1
                unpriced_run = True
                continue
            if au[3] + au[4] and not a_split:
                scan.tier_unknown += 1
            a_usd += P.total(parts)
            usd_run += P.total(parts)
        adv["calls"] += a_calls
        adv["unpriced"] += a_unpriced
        if not requests:
            models.setdefault(mkey, 0.0)
            efforts.setdefault(c["effort"], 0.0)
            rows.append(("advisor", c["ts"], None if a_unpriced else a_usd, c["tools"]))
            continue
        ctx = float(context(requests[-1]))
        out = float(blk.billed()[0][1])
        gap = None if math.isnan(last_tool) else c["ts"] - last_tool
        row = book.resolve(c["model"]) if book_loaded else None
        parts = None
        if row is not None:
            acc, ok = (0.0,) * 5, True
            for rq in requests:
                got = P.response_parts(row, rq)
                if got is None:
                    ok = False
                    break
                acc = tuple(x + y for x, y in zip(acc, got))
            parts = acc if ok else None
        calls += 1
        tok_out += int(out)
        ctx_peak = max(ctx_peak, ctx)
        models.setdefault(mkey, 0.0)
        efforts.setdefault(c["effort"], 0.0)
        if parts is None:
            name = c["model"] or "(none)"
            scan.unpriced += 1
            scan.unpriced_models[name] = scan.unpriced_models.get(name, 0) + 1
            unpriced_run = True
            usd = None
        else:
            u_billed, split = blk.billed()
            if u_billed[3] + u_billed[4] and not split and row.get("engine") != P.CODEX:
                scan.tier_unknown += 1
            usd = P.total(parts)
            usd_run += usd
            models[mkey] += usd
            efforts[c["effort"]] += usd
        rows.append(("call", calls, c["ts"], gap, ctx, out, None if usd is None else usd + a_usd, a_calls, a_unpriced,
                     a_usd, c["tools"]))
    if calls == 0 and adv["calls"] == 0:
        return None, [], scan
    who = "main"
    if sub:
        who = key_string(meta.get("agentType")) if truthy(meta.get("agentType")) else \
            "agent (type unknown: no .meta.json)"
    usd_text = NOT_PRICED if unpriced_run else "$" + fixed(usd_run, 4) + advisor_text(
        adv["calls"], adv["unpriced"], _advisor_total(rows))
    head = head_line(who, models, efforts, calls, t0, t1, ctx_peak, tok_out, scan, big, usd_text, path)
    return head, rows, scan


def _advisor_total(rows):
    total = 0.0
    for r in rows:
        total += (r[2] or 0.0) if r[0] == "advisor" else r[9]
    return total


def render(rows):
    """The row lines of a run's timeline."""
    lines = []
    for r in rows:
        text = tools_text(r[-1])
        if r[0] == "advisor":
            lines.append(f"  advisor {hms(r[1])} · {cash(r[2])} · {text}")
            continue
        _, n, ts, gap, ctx, out, usd, a_calls, a_unpriced, a_usd, _t = r
        lines.append(call_line(n, ts, gap, ctx, out, usd, text, advisor_text(a_calls, a_unpriced, a_usd)))
    return lines
