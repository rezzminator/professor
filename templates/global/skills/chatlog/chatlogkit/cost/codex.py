"""The timeline of a Codex rollout: one row per model call, priced from the token_count events the rollout
records.

COUNTING RULE (the rule `pfm tokens --codex` keeps): a token_count carries a CUMULATIVE total_token_usage that
resets on resume and on compaction, and a duplicate event re-emits an identical cumulative. So an event whose
cumulative equals the last one counts nothing, and a call is priced from its own last_token_usage, which a reset
leaves whole; with none, from the cumulative's delta inside one segment (the cumulative itself after a reset).

PRICING differs from `pfm tokens --codex` by design: each call is priced at its turn's model, with its own input
as the long-tier context; pfm prices the whole thread at its last model and its context peak. The two totals
agree on a single-model rollout that stays inside one tier, and differ on one that switches models or crosses a
long-context threshold."""
import bisect
import math

from ..engines import codex as engine
from ..model import parse_stamp, truthy
from . import claude as C, prices as P
from .fmt import NOT_PRICED, clip, count, epoch_ms, fixed, head as head_line, run_rows, utf16_len

BIG = 20 * 1024
USAGE = ("input_tokens", "cached_input_tokens", "output_tokens", "total_tokens", "reasoning_output_tokens")


def _usage(v):
    """(input, cached, output, total, reasoning) usage; reasoning is included in output."""
    if not isinstance(v, dict):
        return None
    try:
        return tuple(count(v.get(k)) for k in USAGE)
    except ValueError:
        return None


def _stamp(r, payload):
    for v in (r.get("timestamp"), payload.get("timestamp")):
        if isinstance(v, str) and truthy(v):
            d = parse_stamp(v)
            if d is not None:
                return epoch_ms(d)
    return math.nan


def _meta(recs):
    """(role, thread source) of the rollout's own session_meta, the first one."""
    for _, r in recs:
        if r and r.get("type") == "session_meta":
            pl = r.get("payload") if isinstance(r.get("payload"), dict) else {}
            role = pl.get("agent_role")
            return (role if isinstance(role, str) and role else ""), pl.get("thread_source")
    return "", None


def run(path, p, book):
    """(body lines or None, scan, extra gaps) of the rollout at path."""
    recs = C.read(path)
    scan = C.Scan()
    if recs and all(r is None for _, r in recs):
        raise C.Unreadable(f"no line parsed as JSON ({len(recs)} malformed)")
    role, source = _meta(recs)
    who = role or ("subagent" if source == "subagent" else "main")
    model, effort = "", "-"
    calls, slots, rows, pending, stamps = {}, {}, [], [], []
    prev_total, seen, no_last, resets = None, False, 0, 0
    t0, t1, big = math.inf, -math.inf, 0
    models, efforts = {}, {}
    n_calls, tok_out, ctx_peak, usd_run, unpriced_run = 0, 0, 0.0, 0.0, False
    tok_in = tok_cached = tok_reasoning = 0
    for n, r in recs:
        if r is None:
            scan.bad += 1
            continue
        pl = r.get("payload") if isinstance(r.get("payload"), dict) else {}
        ts = _stamp(r, pl)
        if not math.isnan(ts):
            t0, t1 = min(t0, ts), max(t1, ts)
        kind = r["type"]
        pk = pl.get("type") if isinstance(pl.get("type"), str) else ""
        if kind == "turn_context":
            settings = pl.get("collaboration_mode", {}).get("settings") if isinstance(pl.get("collaboration_mode"), dict) else None
            settings = settings if isinstance(settings, dict) else {}
            m = pl.get("model") if truthy(pl.get("model")) else settings.get("model")
            e = pl.get("effort") if truthy(pl.get("effort")) else settings.get("reasoning_effort")
            model = C.key_string(m) if truthy(m) else model
            effort = C.key_string(e) if truthy(e) else effort
        elif pk in ("function_call", "custom_tool_call"):
            name = C.key_string(pl["name"]) if truthy(pl.get("name")) else "?"
            inp = pl.get("arguments" if pk == "function_call" else "input")
            slot = dict(name=name, target=clip(engine.target(name, inp)), chars=0, has=False, err=False, dur=0.0, ts=ts)
            slots[repr(pl.get("call_id"))] = slot
            pending.append(slot)
        elif pk in ("function_call_output", "custom_tool_call_output"):
            slot = slots.get(repr(pl.get("call_id")))
            text, failed = engine.tool_output(pl.get("output"))
            chars = utf16_len(text)
            if chars > BIG:
                big += 1
            if failed:
                scan.tool_errors += 1
            if not math.isnan(ts):
                last_tool_after = ts
            else:
                last_tool_after = math.nan
            if slot is not None:
                slot.update(chars=chars, has=True, err=failed,
                            dur=max(0.0, ts - slot["ts"]) if not (math.isnan(ts) or math.isnan(slot["ts"])) else 0.0)
            if not math.isnan(last_tool_after):
                stamps.append(last_tool_after)
        elif pk == "token_count":
            info = pl.get("info") if isinstance(pl.get("info"), dict) else None
            total = _usage(info.get("total_token_usage")) if info else None
            if total is None:
                continue
            if seen and total[3] == prev_total:
                continue
            reset = seen and total[3] < prev_total
            resets += reset
            last = _usage(info.get("last_token_usage"))
            if last is None or not any(last):
                no_last += 1
                base = (0,) * len(USAGE) if reset or not seen else prev_usage
                last = tuple(max(a - b, 0) for a, b in zip(total, base))
            prev_total, prev_usage, seen = total[3], total, True
            if math.isnan(ts):
                scan.no_stamp += 1
                pending = []
                continue
            rows.append(("call", ts, last, model, effort, pending))
            pending = []
    # replay: a gap runs from the last tool result before the call's first tool call (its own stamp without one)
    out_rows = []
    stamps.sort()
    for item in rows:
        _, ts, last, mdl, eff, tools = item
        inp, cached, out, _total, reasoning = last
        tok_in += inp
        tok_cached += cached
        tok_reasoning += reasoning
        n_calls += 1
        row = book.resolve(mdl) if book.loaded else None
        parts = P.codex_parts(row, inp, cached, out, inp) if row else None
        key = mdl or "(none)"
        models.setdefault(key, 0.0)
        efforts.setdefault(eff, 0.0)
        start = next((s["ts"] for s in tools if not math.isnan(s["ts"])), ts)
        i = bisect.bisect_left(stamps, start)
        gap = None if i == 0 else start - stamps[i - 1]
        tok_out += out
        ctx_peak = max(ctx_peak, float(inp))
        if parts is None:
            scan.unpriced += 1
            scan.unpriced_models[key] = scan.unpriced_models.get(key, 0) + 1
            unpriced_run, usd = True, None
        else:
            usd = sum(parts)
            usd_run += usd
            models[key] += usd
            efforts[eff] += usd
        out_rows.append((n_calls, ts, gap, float(inp), out, usd, tools))
    if not out_rows:
        return None, scan, []
    usd_text = NOT_PRICED if unpriced_run else "$" + fixed(usd_run, 4)
    head = head_line(who, models, efforts, n_calls, t0, t1, ctx_peak, tok_out, scan, big, usd_text, path)
    extra = []
    if no_last:
        extra.append(f"{no_last} token_count events without last_token_usage (priced from the cumulative's delta)")
    usage = (f"TOKENS input={tok_in} cached_input={tok_cached} uncached_input={tok_in - tok_cached} "
             f"output={tok_out} reasoning_output={tok_reasoning} "
             f"nonreasoning_output={tok_out - tok_reasoning}; reasoning is included in output")
    return [head] + run_rows(out_rows) + [usage], scan, extra
