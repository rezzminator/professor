"""The timeline of a Claude Code transcript: one row per model call, priced from the usage block the
transcript records."""
import itertools
import json
import math
import os
import re
import time

from ..model import parse_stamp, truthy
from . import prices as P
from .fmt import (NOT_PRICED, advisor_text, call_line, cash, clip, count, epoch_ms, fixed, head as head_line, hms,
                  tools_text, utf16_len)

BIG = 20 * 1024
ADVISOR = "advisor_message"
COALESCE_TARGET = ("file_path", "command", "pattern", "path", "subagent_type", "skill", "url", "query")


class Unreadable(Exception):
    pass


class Shared:
    """What owners() learns from the projects tree, kept across every transcript one process prices (a
    census): each domain's file list and the facts read from each file's bytes. A prep holds one for its one
    transcript; a census holds one for all of them, so no domain is walked twice and no unchanged file is read
    again for an id it has answered. A file's bytes are never kept: a read answers the asked ids by substring and
    leaves them answered; with `eager_index` (a census across sessions, whose transcripts bring different ids)
    the first read also leaves the file's UUID windows (see uuid_index), which answer every later session id
    without a read; without it (one session's transcripts, which share their ids) the windows are built on the
    file's second read, the first that a new id forces. The "msg_..." strings (see MSG_RUNS) are kept with the
    windows, or from the first read the message-id gate needs, whichever comes first.

    A fact lives only while its file does not change: every read stamps the file (mtime_ns, size), and stat(),
    which owners() calls for each file per transcript, re-stats a file whose last stat is older than `fresh_s`
    seconds and drops the facts of one changed or unstatable since; so no fact is more than `fresh_s` behind its
    file when a transcript is priced, within the time one prep's own read of a large domain spans. A failed read
    is never kept: each transcript retries it and reports its own error. The domain's file list is the census's
    snapshot: a file created after its walk is not seen. `walked` and `reads` count the domain walks and the
    other-transcript reads performed, `read_bytes` the bytes those reads returned and `index_bytes` the size of
    the indexes they left."""

    FRESH_S = 5.0

    def __init__(self, eager_index=True, fresh_s=FRESH_S):
        self.eager_index, self.fresh_s = eager_index, fresh_s
        self.walks = {}     # domain -> (.jsonl paths, the read errors that walk produced)
        self.listings = {}  # domain -> ([paths, one copy per file], those paths as a set)
        self.stats = {}     # path -> (monotonic time of the stat, mtime or None, the OSError text or None)
        self.stamps = {}    # path -> (mtime_ns, size) of the bytes the facts below were read from
        self.uuids = {}     # path -> the UUID windows of the bytes, one per line (see uuid_index)
        self.answers = {}   # path -> {id: whether it is a substring of the bytes}, for ids the windows do not answer
        self.msgs = {}      # path -> frozenset of the "msg_..." strings the bytes quote (see MSG_RUNS)
        self.tokens = {}    # path -> frozenset of the quoted tokens in the bytes, built when the msg gate cannot tell
        self.holders = {}   # path -> [(call key, session, named, fork)] of every assistant record with usage
        self.walked = self.reads = self.read_bytes = self.index_bytes = 0

    def walk(self, domain):
        """The .jsonl files under domain and the read errors the walk produced, walked on first ask."""
        if domain not in self.walks:
            found, scan = [], Scan()
            _walk(domain, found, scan)
            self.walks[domain] = (found, scan.read_errors)
            self.walked += 1
        return self.walks[domain]

    def forget(self, path):
        """Drop every fact read from path's bytes."""
        for facts in (self.stamps, self.uuids, self.answers, self.msgs, self.tokens, self.holders):
            facts.pop(path, None)

    def stat(self, path):
        """(mtime, None) of path, or (None, the OSError text), at most fresh_s old; a file whose (mtime, size)
        differs from the stamp of its facts, or that cannot be statted, loses them."""
        now = time.monotonic()
        hit = self.stats.get(path)
        if hit is not None and now - hit[0] < self.fresh_s:
            return hit[1], hit[2]
        try:
            st = os.stat(path)
        except OSError as e:
            self.forget(path)
            self.stats[path] = (now, None, str(e))
            return None, str(e)
        if self.stamps.get(path, (st.st_mtime_ns, st.st_size)) != (st.st_mtime_ns, st.st_size):
            self.forget(path)
        self.stats[path] = (now, st.st_mtime, None)
        return st.st_mtime, None

    def listing(self, domain):
        """The files of domain that owners() considers, in path order, and their paths as a set: one copy of
        every file that shares its last three path parts with another, built on first ask."""
        if domain not in self.listings:
            seen, files = set(), []
            for f in self.walk(domain)[0]:
                tail = "/".join(f.split(os.sep)[-3:])
                if tail not in seen:
                    seen.add(tail)
                    files.append(f)
            self.listings[domain] = (files, set(files))
        return self.listings[domain]

    def told(self, f, i):
        """Whether id i is a substring of the bytes of f as the kept facts tell it, None when they cannot."""
        index = self.uuids.get(f)
        if index is not None and UUID.fullmatch(i):
            return i in index
        return self.answers.get(f, {}).get(i)

    def holds_id(self, f, ids, load):
        """Whether any of ids is a substring of the bytes of f, which load() returns (None when it cannot read
        them, the answer then None too): told from the kept facts where they tell it, else from the bytes, read
        once for all the ids pending."""
        if any(self.told(f, i) for i in ids):
            return True
        pending = [i for i in ids if self.told(f, i) is None]
        if not pending:
            return False
        data = load()
        if data is None:
            return None
        known = self.answers.setdefault(f, {})  # after the load, which drops the facts of a changed file
        for i in pending:
            known[i] = i in data
        return any(known[i] for i in pending)

    def calls(self, f, ids, origin_named, needles, scan):
        """The (call key, session, named, fork) of every assistant record with usage in another transcript f
        that passes the ids and token gates; None when a gate skips it or its bytes cannot be read (the
        error goes to scan). The bytes are read at most once per call, only when a fact is not kept, so a file
        that does not change is read again only for an id no kept fact answers."""
        data = None

        def load():
            nonlocal data
            if data is None:
                try:
                    with open(f, "rb") as fh:
                        st = os.fstat(fh.fileno())
                        data = fh.read()
                except OSError as e:
                    self.forget(f)
                    scan.read_errors.append(f"{f}: {e}")
                    return None
                self.reads += 1
                self.read_bytes += len(data)
                stamp = (st.st_mtime_ns, st.st_size)
                again = self.stamps.get(f) == stamp
                if not again:
                    self.forget(f)  # first read, or the bytes changed since the facts were read: rebuild from these
                    self.stamps[f] = stamp
                if f not in self.uuids and (self.eager_index or again):
                    self.uuids[f] = uuid_index(data)
                    self.index_bytes += len(self.uuids[f])
                    msg_index()
            return data

        def msg_index():
            if f not in self.msgs:
                self.msgs[f] = frozenset(MSG_RUNS.findall(data))
                self.index_bytes += sum(map(len, self.msgs[f]))

        if ids and not origin_named and not self.holds_id(f, ids, load):
            return None     # no id in the bytes, or the bytes could not be read (the error is on scan)
        if needles.exact:
            if needles.by_msg:
                if f not in self.msgs:
                    if load() is None:
                        return None
                    msg_index()
                if needles.ids.isdisjoint(self.msgs[f]):
                    return None     # a quoted token is a quoted msg_ string: the index holds every one
            if f not in self.tokens:
                if load() is None:
                    return None
                self.tokens[f] = frozenset(TOKEN.findall(data))
            if not needles.ids.intersection(self.tokens[f]):
                return None
        if f not in self.holders:
            if load() is None:
                return None
            self.holders[f] = parse_calls(f, data)
        return self.holders[f]


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
UUID_TAILS = re.compile(rb"-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}")
UUID = re.compile(rb"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}")
# every "msg_..." string a file quotes, found at memory speed; the closing quote is looked at, never consumed,
# so no match can hide another, and each token TOKEN reads of that shape is one of them
MSG_RUNS = re.compile(rb'"(msg_[0-9A-Za-z_.:-]*)(?=")')
MSG_ID = re.compile(rb"msg_[0-9A-Za-z_.:-]*")


class Needles:
    """The ids of the call keys one transcript looks for in the other transcripts' bytes. `exact`: each is a
    quoted token as TOKEN reads them, so a file holds one when its token set meets them; `by_msg`: each is also
    a "msg_..." string, so a file whose index holds none of them holds none as a token."""

    def __init__(self, ids):
        self.ids = ids
        self.exact = all(TOKEN.fullmatch(b'"' + v + b'"') for v in ids)
        self.by_msg = self.exact and all(MSG_ID.fullmatch(v) for v in ids)


def uuid_index(data):
    """The 36 bytes that end at each UUID tail of data (fewer at the file start), one per line, so that a UUID-shaped
    id is a substring of data exactly when it is a substring of this index: an id lies in one line (it holds no
    newline), and a line is a window of data, so an id inside one occurs in data; an id that occurs in data ends
    at a UUID tail whose window is the id itself. Scanning for the last four groups, which open with a literal
    `-`, runs at memory speed (~1.3 GB/s, against ~50 MB/s for an index of every run of id characters), and the
    eight bytes before each hit are read back. Two tails cannot overlap (their dashes sit 5 apart and a tail is
    28 long, so a tail shifted by 5, 10 or 15 meets a hex digit where it needs a dash), so the scan misses no
    occurrence."""
    return b"\n".join({data[max(m.start() - 8, 0):m.end()] for m in UUID_TAILS.finditer(data)})


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


def parse_calls(f, data):
    """(call key, session, named, fork) of every assistant record with valid usage in the bytes of transcript
    f, in line order, skipping the at: keys (no other transcript can hold one)."""
    out = []
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
        if key.startswith("at:"):
            continue
        session = key_string(r["sessionId"]) if truthy(r.get("sessionId")) else os.path.basename(f)[:-len(".jsonl")]
        fork = r["forkedFrom"].get("sessionId") if isinstance(r.get("forkedFrom"), dict) else None
        named = key_string(fork) if truthy(fork) else key_string(r["session_id"]) if truthy(r.get("session_id")) else ""
        out.append((key, session, named, truthy(r.get("forkedFrom"))))
    return out


def owners(path, keys, scan, shared=None):
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
    shared = shared if shared is not None else Shared()
    scan.read_errors.extend(shared.walk(domain)[1])
    files, listed = shared.listing(domain)
    needles = Needles({k.split(":", 1)[1].encode() for k in want})
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
    floor = born - 60 if born is not None else None
    shaped = [i for i in ids if UUID.fullmatch(i)]
    # With no fork origin and only UUID-shaped ids a file whose index is built is told from the ids alone.
    by_index = bool(ids) and not origins and len(shaped) == len(ids)
    holders = {}
    for f in itertools.chain(files, () if me in listed else (me,)):
        if f == me:
            # the transcript's own bytes are read per call, never served from the shared facts
            try:
                with open(f, "rb"):
                    pass
            except OSError as e:
                scan.read_errors.append(f"{f}: {e}")
                continue
            calls = parse_calls(f, mine)
        else:
            m, failure = shared.stat(f)
            if floor is not None and not any(f"{os.sep}{o}.jsonl" in f or f"{os.sep}{o}{os.sep}" in f for o in origins):
                if failure is not None:
                    scan.read_errors.append(f"{f}: {failure}")
                    continue
                if m < floor:
                    continue
            if by_index:
                index = shared.uuids.get(f)
                if index is not None and not any(i in index for i in shaped):
                    continue
            origin_named = any(o.encode() in f.encode() for o in origins)
            calls = shared.calls(f, ids, origin_named, needles, scan)
            if calls is None:
                continue
        for key, session, named, fork in calls:
            if key not in want:
                continue
            h = holders.setdefault(key, {}).setdefault(f, dict(session=session, named=set(), fork=False))
            if named and named != session:
                h["named"].add(named)
            if fork:
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


def run(path, book, shared=None):
    """(header text, row lines, scan) of the transcript at path; header None when it holds no call. `shared`
    is the Shared walk a census passes for all its transcripts; None reads the tree for this one alone."""
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
    owned = owners(path, [k for k in usage], scan, shared)
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
