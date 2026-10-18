"""The timeline of an OpenCode session: one row per assistant message (one model call), priced from the token
record the message carries. `pfm tokens --timeline` could not read a store session (UNREADABLE); this is native.

Each message's tokens: uncached `input`, `output`, `reasoning` (outside output), `cache.read`, `cache.write`. A
Claude row bills the write at the 5-minute rate (OpenCode records no TTL); a Codex row bills every input token
with the read as its cached part. OpenCode's own recorded `cost` is never used: under a subscription it is $0."""
import bisect
import math

from ..engines import opencode as engine
from ..model import truthy
from . import claude as C, prices as P
from .fmt import NOT_PRICED, clip, count, fixed, head as head_line, run_rows, utf16_len

BIG = 20 * 1024
GATEWAY_PROVIDERS = ("anthropic", "openai")


def _count(tokens, *path):
    v = tokens
    for k in path:
        v = v.get(k) if isinstance(v, dict) else None
    return count(v)


def _price(book, provider, model, t, scan):
    """USD of one message, None when the table cannot price it (counted in the scan)."""
    inp, out, read, write, reasoning = t
    key = f"{provider}/{model}" if provider in GATEWAY_PROVIDERS else model
    row = book.resolve(key) if book.loaded else None
    parts = None
    if row is not None and row.get("engine") == P.CLAUDE:
        if write:
            scan.tier_unknown += 1
        parts = P.claude_parts(row, (inp, out + reasoning, read, write, 0))
    elif row is not None and row.get("engine") == P.CODEX:
        total_in = inp + read + write
        parts = P.codex_parts(row, total_in, read, out + reasoning, total_in)
    if parts is None:
        name = "/".join(x for x in (provider, model) if x) or "(none)"
        scan.unpriced += 1
        scan.unpriced_models[name] = scan.unpriced_models.get(name, 0) + 1
        return None
    return sum(parts)


def run(path, p, book):
    """(body lines or None, scan, extra gaps) of the session at the locator `path`."""
    loc = engine.split_locator(path)
    scan = C.Scan()
    if loc is None:
        raise C.Unreadable(f"not an opencode locator: {path}")
    try:
        sess, msgs, parts = engine.session_rows(*loc)
    except engine.StoreError as e:
        raise C.Unreadable(str(e))
    by_msg = {}
    for part in parts:
        by_msg.setdefault(part[1], []).append(part)
    stamps, calls = [], []
    t0, t1, big = math.inf, -math.inf, 0
    agent = ""
    for mid, created, data in msgs:
        m = engine._json(data)
        if m is None:
            scan.bad += 1
            continue
        for ms in (created,):
            if isinstance(ms, (int, float)) and ms > 0:
                t0, t1 = min(t0, ms), max(t1, ms)
        tools = []
        for pid, _, _, pdata in by_msg.get(mid, []):
            d = engine._json(pdata)
            if d is None:
                scan.bad += 1
                continue
            if d.get("type") != "tool":
                continue
            state = d.get("state") if isinstance(d.get("state"), dict) else {}
            name = d["tool"] if isinstance(d.get("tool"), str) and d["tool"] else "?"
            status = state.get("status")
            slot = dict(name=name, target=clip(engine.target(name, state.get("input"))) if "input" in state else "None",
                        chars=0, has=False, err=False, dur=0.0)
            if status in ("completed", "error"):
                text = state.get("output") if status == "completed" else state.get("error")
                slot.update(chars=utf16_len(text) if isinstance(text, str) else 0, has=True, err=status == "error")
                tm = state.get("time") if isinstance(state.get("time"), dict) else {}
                if isinstance(tm.get("start"), (int, float)) and isinstance(tm.get("end"), (int, float)):
                    slot["dur"] = max(0.0, float(tm["end"] - tm["start"]))
                    stamps.append(tm["end"])
                    t1 = max(t1, tm["end"])
                if slot["chars"] > BIG:
                    big += 1
                if slot["err"]:
                    scan.tool_errors += 1
            tools.append(slot)
        if m.get("role") != "assistant":
            continue
        agent = agent or (m["agent"] if isinstance(m.get("agent"), str) else "")
        tokens = m.get("tokens")
        if not isinstance(tokens, dict):
            scan.bad += 1
            continue
        try:
            t = (_count(tokens, "input"), _count(tokens, "output"), _count(tokens, "cache", "read"),
                 _count(tokens, "cache", "write"), _count(tokens, "reasoning"))
        except ValueError:
            scan.bad += 1
            continue
        if not any(t):
            scan.synthetic += 1
            continue
        tm = m.get("time") if isinstance(m.get("time"), dict) else {}
        end = tm.get("completed") if isinstance(tm.get("completed"), (int, float)) else tm.get("created")
        end = end if isinstance(end, (int, float)) and end > 0 else created
        start = tm.get("created") if isinstance(tm.get("created"), (int, float)) and tm["created"] > 0 else end
        if not isinstance(end, (int, float)) or end <= 0:
            scan.no_stamp += 1
            continue
        t0, t1 = min(t0, end), max(t1, end)
        calls.append((float(end), float(start), t, m.get("providerID") or "", m.get("modelID") or "",
                      m.get("variant") if truthy(m.get("variant")) else "-", tools))
    models, efforts, rows = {}, {}, []
    tok_out, ctx_peak, usd_run, unpriced_run = 0, 0.0, 0.0, False
    stamps.sort()
    for n, (end, start, t, provider, model, effort, tools) in enumerate(calls, 1):
        inp, out, read, write, reasoning = t
        ctx = float(inp + read + write)
        usd = _price(book, provider, model, t, scan)
        key = "/".join(x for x in (provider, model) if x) or "(none)"
        models.setdefault(key, 0.0)
        efforts.setdefault(effort, 0.0)
        tok_out += out + reasoning
        ctx_peak = max(ctx_peak, ctx)
        if usd is None:
            unpriced_run = True
        else:
            usd_run += usd
            models[key] += usd
            efforts[effort] += usd
        # the last tool result before the message began is when the model was handed the turn
        i = bisect.bisect_left(stamps, start)
        gap = None if i == 0 else end - stamps[i - 1]
        rows.append((n, end, gap, ctx, out + reasoning, usd, tools))
    if not rows:
        return None, scan, []
    who = ("subagent" if not agent else agent) if sess["parent_id"] else "main"
    usd_text = NOT_PRICED if unpriced_run else "$" + fixed(usd_run, 4)
    head = head_line(who, models, efforts, len(rows), t0, t1, ctx_peak, tok_out, scan, big, usd_text, path)
    return [head] + run_rows(rows), scan, []
