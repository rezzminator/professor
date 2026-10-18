"""The price table: `pfm model-cost --json --all`, the one table, cached beside the kit's runs. A model the table
cannot price is ignorance (`n/a`, named in the data gaps), never $0."""
import json
import os
import re
import subprocess
import tempfile
import time

CACHE_SECONDS = 86400
CLAUDE, CODEX = "claude", "codex"
_BEDROCK = re.compile(r"^(?:[a-z0-9-]+\.)?anthropic\.")
_BEDROCK_VERSION = re.compile(r"-v\d+(?::\d+)?$")
_SNAPSHOT = re.compile(r"-(?:\d{8}|\d{4}-\d{2}-\d{2})$")
RATE_KEYS = ("in", "out", "hit", "w5m", "w1h", "cached")


class Book:
    """A loaded table (or none, with the reason) and the path it was saved at."""

    def __init__(self, doc, path, failure=""):
        self.doc, self.path, self.failure = doc or {}, path, failure
        self.rows = {}
        for row in self.doc.get("rows") or []:
            if isinstance(row, dict) and isinstance(row.get("key"), str):
                self.rows.setdefault(row["key"], row)

    @property
    def loaded(self):
        return bool(self.rows)

    def resolve(self, model_id):
        """The row that prices a model id: by its normalized id, else without a trailing snapshot date."""
        ident = normalize(model_id)
        if ident is None:
            return None
        if ident in self.rows:
            return self.rows[ident]
        undated = _SNAPSHOT.sub("", ident)
        return self.rows.get(undated) if undated != ident else None

    def line(self):
        providers = ", ".join(s.get("provider", "") for s in self.doc.get("sources") or [] if isinstance(s, dict))
        saved = f"saved table {self.path}" if self.path else "current API list prices"
        return (f"pricing: {saved}; Standard service, published prompt/context tiers (Codex aggregates use the "
                f"run peak tier); table fetched {self.doc.get('fetched_at', '')} from {providers}; not historical billing")


def normalize(model_id):
    if not isinstance(model_id, str):
        return None
    ident = model_id.strip().lower()
    if ident.startswith("anthropic/"):
        ident = ident[len("anthropic/"):]
        if not ident.startswith("claude-"):
            return None
    elif ident.startswith("openai/"):
        ident = ident[len("openai/"):]
        if ident.startswith("claude-"):
            return None
    ident = _BEDROCK.sub("", ident)
    at = ident.find("[")
    if at > 0 and ident.endswith("]"):
        ident = ident[:at]
    at = ident.find("@")
    if at > 0:
        ident = ident[:at]
    if ident.startswith("claude-"):
        ident = _BEDROCK_VERSION.sub("", ident).replace(".", "-")
    return ident or None


def rates_at(row, context):
    """The row's rates at a call of `context` tokens: the upper tier when the context passes its threshold."""
    long = row.get("long")
    if isinstance(long, dict) and context > long.get("above", float("inf")):
        return {k: long.get(k) for k in RATE_KEYS}
    return {k: row.get(k) for k in RATE_KEYS}


def claude_parts(row, u):
    """USD by column (input, output, write5m, write1h, cache read) of one request `u` = (in, out, cr, w5, w1)
    on a Claude row; None when a nonzero count has no published rate."""
    if row.get("engine") != CLAUDE:
        return None
    inp, out, cr, w5, w1 = u
    rates = rates_at(row, inp + cr + w5 + w1)
    parts = []
    for count, key in ((inp, "in"), (out, "out"), (w5, "w5m"), (w1, "w1h"), (cr, "hit")):
        if count == 0:
            parts.append(0.0)
        elif rates[key] is None:
            return None
        else:
            parts.append(float(count) * rates[key] / 1e6)
    return tuple(parts)


def codex_parts(row, inp, cached, out, context):
    """USD by column (uncached input, cache read, output) of Codex usage on a Codex row; None when unpriceable."""
    if row.get("engine") != CODEX:
        return None
    rates = rates_at(row, context)
    uncached = max(inp - cached, 0)
    per = []
    for count, key in ((uncached, "in"), (cached, "cached"), (out, "out")):
        if rates[key] is not None:
            per.append(rates[key] / 1e6)
        elif count > 0:
            return None
        else:
            per.append(0.0)
    return (float(uncached) * per[0], float(cached) * per[1], float(out) * per[2])


def response_parts(row, u):
    """One request of a Claude Code transcript priced on whichever row its model resolves to, as the tuple
    claude_parts gives; a gateway-served Codex row prices the input and cache read as Codex does."""
    if row.get("engine") != CODEX:
        return claude_parts(row, u)
    inp, out, cr, w5, w1 = u
    context = inp + cr + w5 + w1
    got = codex_parts(row, inp + cr, cr, out, context)
    if got is None:
        return None
    unc, cached, output = got
    if not w5 + w1:
        return (unc, output, 0.0, 0.0, cached)
    rate = rates_at(row, context)["in"]
    if rate is None:
        return None
    per = rate / 1e6
    return (unc, output, float(w5) * per, float(w1) * per, cached)


def total(parts):
    return parts[0] + parts[1] + parts[2] + parts[3] + parts[4]


def load(kit_dir):
    """The cached table, refreshed from `pfm model-cost --json --all` when older than a day. A failure to get
    any table is a Book with no rows and the reason."""
    path = os.path.join(kit_dir, "prices.json")
    failure = ""
    try:
        fresh = time.time() - os.path.getmtime(path) < CACHE_SECONDS
    except OSError:
        fresh = False
    if not fresh:
        try:
            os.makedirs(kit_dir, exist_ok=True)
            r = subprocess.run(["pfm", "model-cost", "--json", "--all"], capture_output=True, text=True, timeout=120)
            if r.returncode == 0:
                json.loads(r.stdout)
                fd, tmp = tempfile.mkstemp(dir=kit_dir, prefix=".prices.")
                try:
                    with os.fdopen(fd, "w") as f:
                        f.write(r.stdout)
                    os.replace(tmp, path)
                except OSError:
                    if os.path.exists(tmp):
                        os.unlink(tmp)
                    raise
            else:
                failure = f"pfm model-cost rc {r.returncode}: {(r.stderr or r.stdout).strip()[:200]}"
        except (OSError, ValueError, subprocess.SubprocessError) as e:
            failure = f"pfm model-cost: {e}"
    try:
        with open(path) as f:
            doc = json.load(f)
        return Book(doc if isinstance(doc, dict) else {}, path, failure)
    except (OSError, ValueError) as e:
        return Book({}, path, failure or f"read {path}: {e}")
