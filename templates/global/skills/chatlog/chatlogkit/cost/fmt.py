"""Number and text forms the timeline prints, those of `pfm tokens --timeline`."""
import math
import re
from datetime import datetime, timezone

NOT_PRICED = "n/a"
_MODEL_PREFIX = re.compile(r"^claude-")
_MODEL_DATE = re.compile(r"-\d{8}$")
_GPT = re.compile(r"^gpt-")


def num(v):
    """A number as JavaScript prints it: whole values without a fraction."""
    if isinstance(v, float) and v.is_integer():
        return str(int(v))
    return str(v)


def fixed(v, digits):
    return f"{v:.{digits}f}"


def jround(v):
    """Math.round: halves go toward +Infinity."""
    return math.floor(v + 0.5)


def tokens_text(v):
    if v >= 1e6:
        return fixed(v / 1e6, 1) + "M"
    if v >= 1000:
        return fixed(v / 1000, 1) + "K"
    return num(jround(v))


def seconds(ms):
    return fixed(ms / 1000, 1) + "s"


def cash(usd):
    return NOT_PRICED if usd is None else "$" + fixed(usd, 4)


def advisor_text(calls, unpriced, usd):
    if calls == 0:
        return ""
    if unpriced:
        return f" (advisor {NOT_PRICED})"
    return " (advisor $" + fixed(usd, 4) + ")"


def utf16_len(s):
    return len(s.encode("utf-16-le", "surrogatepass")) // 2


def model_stem(model):
    return _MODEL_DATE.sub("", _MODEL_PREFIX.sub("", model or "?"))


def codex_short(model):
    return _GPT.sub("", model or "?")


def hms(ms):
    return datetime.fromtimestamp(int(ms) / 1000, timezone.utc).strftime("%H:%M:%S")


def epoch_ms(dt):
    """Milliseconds since the epoch of an aware datetime, cut (not rounded) as a JS Date holds it."""
    d = dt - datetime(1970, 1, 1, tzinfo=timezone.utc)
    return float((d.days * 86400 + d.seconds) * 1000 + d.microseconds // 1000)


def clip(text):
    """A tool target as the timeline prints it: whitespace runs collapsed, cut to 100 UTF-16 units."""
    text = re.sub(r"\s+", " ", text).strip()
    return text[:99] + "…" if utf16_len(text) > 100 else text


def count(v):
    """An integer token count; absent is 0, any other form is a malformed usage block."""
    if v is None:
        return 0
    if isinstance(v, bool) or not isinstance(v, (int, float)) or (isinstance(v, float) and not v.is_integer()):
        raise ValueError(f"token count {v!r}")
    return int(v)


def tools_text(slots):
    """A call's tool slots: name, target and result size, ` | `-joined; `(no tools)` for none."""
    texts = []
    for s in slots:
        res = "(no result)"
        if s["has"]:
            res = f"{num(s['chars'])}ch" + (" ERR" if s["err"] else "") + " " + seconds(s["dur"])
        texts.append(f"{s['name']}: {s['target']} {res}")
    return " | ".join(texts) or "(no tools)"


def call_line(n, ts, gap, ctx, out, usd, tools, advisor=""):
    return (f"  #{n} {hms(ts)} after {'-' if gap is None else seconds(gap)} · ctx {tokens_text(ctx)}"
            f" · out {num(out)} · {cash(usd)}{advisor} · {tools}")


def head(who, models, efforts, calls, t0, t1, ctx_peak, tok_out, scan, big, usd_text, path):
    """The TIMELINE line of one run."""
    return (f"TIMELINE {who} · " + "+".join(model_stem(m) for m in models) + " · effort " + "/".join(efforts)
            + f" · {calls} calls · wall {num(jround((t1 - t0) / 1000))}s · peak ctx {tokens_text(ctx_peak)}"
            f" · out {tok_out} tok · tool errors {scan.tool_errors} · results >20KB {big} · {usd_text} · {path}")


def run_rows(rows):
    """The row lines of a Codex or OpenCode run: (n, ts, gap, ctx, out, usd, slots) each."""
    return [call_line(n, ts, gap, ctx, out, usd, tools_text(slots)) for n, ts, gap, ctx, out, usd, slots in rows]
