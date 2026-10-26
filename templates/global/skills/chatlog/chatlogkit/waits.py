"""The `waits` verb: per sub-agent run of the matched types, how long it and its parent sat idle while a child
was out, and what the prompt-cache rewrites after those waits cost. Every number is read from the records'
timestamps and usage blocks and priced by the kit's price book; nothing is estimated."""
import json
import math
import os
import re
import statistics
import sys
import time

from . import cli, cost, locate
from .cost import prices as P
from .cost.claude import Unreadable, Usage, call_key, read
from .cost.fmt import epoch_ms
from .engines import CLAUDE, Failure, load as load_jsonl
from .model import parse_stamp, truthy
from .rows import die, opt

USAGE = ("usage: chatlog.py waits --calls TARGET | [--type-re RE] [--child-re RE] [--since 14d] [--root DIR]... [--host NAME] "
         "[--gap S] [--out F]")
DEFAULT_TYPES = r"^(reviewer|reviewer-pro)$"
DEFAULT_CHILD = r"^(sub-)?tracer.*$"
COLS = ("transcript_path host parent_type reviewer_wall_s tracer_runs tracer_wall_s reviewer_longest_idle_s "
        "reviewer_idle_total_s parent_idle_s cache_rewrites_after_wait rewrite_tokens rewrite_usd usd_reviewer "
        "usd_tracer_tree usd_parent_during_wait findings_count parent_idle_total_s parent_rewrites "
        "parent_rewrite_tokens parent_rewrite_usd waits_over_gap_no_spike w5m_tokens w1h_tokens parent_w5m_tokens parent_w1h_tokens children_any "
        "concurrent_siblings reviewer_dormant_s rewrites_child_wait rewrite_usd_child_wait agent_type start_utc parent_path review_path").split()
REVIEW_PATH = re.compile(r"(/[^\s`'\"]+\.md)")
FINDING = re.compile(r"(?m)^\W*(F\d+)\b")
IDLE_MIN = 30  # seconds with no request in flight that count as waiting; a grep or a read returns well inside it
ASYNC = ("async agent launched", "agent is running in the background", "launched in the background")


class Run:
    """One transcript read once: its model calls (deduplicated by call key, priced), its Agent spawns and
    their results, its first and last stamp, its final text."""

    def __init__(self, path, book):
        self.path, self.calls, self.uses, self.results = path, [], {}, {}
        self.t0 = self.t1 = None
        self.final, self.unpriced, self.bad = "", 0, 0
        by_key, sent = {}, None
        for n, r in read(path):
            if r is None:
                self.bad += 1
                continue
            ts = parse_stamp(r.get("timestamp")) if isinstance(r.get("timestamp"), str) else None
            ts = epoch_ms(ts) / 1000.0 if ts is not None else None
            if ts is not None:
                self.t0 = ts if self.t0 is None else min(self.t0, ts)
                self.t1 = ts if self.t1 is None else max(self.t1, ts)
            msg = r.get("message") if isinstance(r.get("message"), dict) else None
            content = msg.get("content") if msg else None
            fork = r["forkedFrom"].get("sessionId") if isinstance(r.get("forkedFrom"), dict) else None
            if fork and fork != r.get("sessionId"):
                continue  # a record copied from another session is that session's call
            if r.get("type") == "assistant" and msg is not None and ts is not None:
                for b in content if isinstance(content, list) else []:
                    if not isinstance(b, dict):
                        continue
                    if b.get("type") == "tool_use" and isinstance(b.get("id"), str):
                        inp = b.get("input") if isinstance(b.get("input"), dict) else {}
                        self.uses[b["id"]] = dict(name=b.get("name"), ts=ts, type=inp.get("subagent_type"))
                    elif b.get("type") == "text" and isinstance(b.get("text"), str) and b["text"].strip():
                        self.final = b["text"].strip()
                if not truthy(msg.get("usage")) or msg.get("model") == "<synthetic>":
                    continue
                try:
                    blk = Usage(msg["usage"])
                except ValueError:
                    self.bad += 1
                    continue
                if not any(blk.billed()[0]):
                    continue
                key = call_key(r, msg, path, n)
                # the request went out at the record that preceded its first response record
                by_key[key] = (ts, blk, msg.get("model") or "", by_key[key][3] if key in by_key else sent or ts)
            if r.get("type") != "assistant" and ts is not None:
                sent = ts
            if r.get("type") == "user" and isinstance(content, list) and ts is not None:
                for b in content:
                    if isinstance(b, dict) and b.get("type") == "tool_result" and isinstance(b.get("tool_use_id"), str):
                        c = b.get("content")
                        text = c if isinstance(c, str) else " ".join(
                            x.get("text", "") for x in c if isinstance(x, dict)) if isinstance(c, list) else ""
                        self.results.setdefault(b["tool_use_id"], (ts, text[:400].lower()))
        for key, (ts, blk, model, sent_ts) in by_key.items():
            row = book.resolve(model) if book.loaded else None
            usd = excess = 0.0
            priced = row is not None
            for rq in blk.requests() if priced else []:
                got = P.response_parts(row, rq)
                hit = P.response_parts(row, (rq[0], rq[1], rq[2] + rq[3] + rq[4], 0, 0))
                if got is None or hit is None:
                    priced = False
                    break
                usd += P.total(got)
                excess += P.total(got) - P.total(hit)
            billed, _ = blk.billed()
            if not priced:
                self.unpriced += 1
            self.calls.append(dict(ts=ts, sent=sent_ts, usd=usd if priced else 0.0, excess=excess if priced else 0.0,
                                   ctx=billed[0] + billed[2] + billed[3] + billed[4], write=billed[3] + billed[4], cr=billed[2], inp=billed[0], model=model,
                                   w5=billed[3], w1=billed[4], key=key))
        self.calls.sort(key=lambda c: c["ts"])
        prev = sent = None
        pctx = 0
        for c in self.calls:
            # lost: the tokens of the previous request's context this one did not read from the cache, capped at
            # what it wrote; their price over a cache read is the rewrite's cost (the tokens new since then are
            # written whatever the gap)
            c["prev_ctx"] = pctx
            c["lost"] = min(c["write"], max(0, pctx - c["cr"])) if prev is not None else 0
            c["lost_usd"] = c["excess"] * c["lost"] / c["write"] if c["write"] else 0.0
            pctx = c["ctx"]
            # gap: seconds the cache sat unread, from this request's send back to the previous request's send;
            # gap_from: the previous response, where an idle stretch (no model output) begins
            c["gap"] = c["sent"] - sent if sent is not None else 0.0
            c["gap_from"] = prev
            prev, sent = c["ts"], c["sent"]

    def usd(self):
        return sum(c["usd"] for c in self.calls)


def spiked(c):
    """A call that re-wrote at least half of the previous request's context (and 5,000 tokens or more): the
    prefix it had sent before was no longer in the cache."""
    return c["gap_from"] is not None and c["lost"] >= max(5000, 0.5 * c["prev_ctx"])


def overlap(a0, a1, spans):
    """Seconds of [a0, a1] covered by the union of spans."""
    pts, total, cur0, cur1 = sorted((max(s0, a0), min(s1, a1)) for s0, s1 in spans if min(s1, a1) > max(s0, a0)), 0.0, None, None
    for s0, s1 in pts:
        if cur1 is None or s0 > cur1:
            if cur1 is not None:
                total += cur1 - cur0
            cur0, cur1 = s0, s1
        else:
            cur1 = max(cur1, s1)
    return total + (cur1 - cur0 if cur1 is not None else 0.0)


class Session:
    """A Claude session directory's transcripts: the main one and every sub-agent, each read on first ask."""

    def __init__(self, main, book, fails):
        self.main, self.book, self.fails, self.runs = main, book, fails, {}
        self.sub = main[:-len(".jsonl")] + os.sep + "subagents"
        self.metas = {}
        try:
            names = os.listdir(self.sub)
        except OSError:
            names = []
        for f in names:
            if f.endswith(".meta.json"):
                meta, why = locate.read_meta(os.path.join(self.sub, f))
                if meta is None:
                    fails.append((os.path.join(self.sub, f), why))
                    continue
                self.metas[os.path.join(self.sub, f[:-len(".meta.json")] + ".jsonl")] = meta
        self.by_tuid = {m.get("toolUseId"): p for p, m in self.metas.items() if isinstance(m.get("toolUseId"), str)}

    def run(self, path):
        if path not in self.runs:
            if not os.path.isfile(path):
                self.fails.append((path, "no transcript beside its .meta.json"))
                self.runs[path] = None
                return None
            try:
                self.runs[path] = Run(path, self.book)
            except Unreadable as e:
                self.fails.append((path, str(e)))
                self.runs[path] = None
        return self.runs[path]

    def holder(self, tuid):
        """The transcript whose Agent call carries tuid, found by its bytes and then parsed."""
        needle = tuid.encode()
        for p in [self.main] + sorted(self.metas):
            try:
                with open(p, "rb") as f:
                    if needle not in f.read():
                        continue
            except OSError:
                continue
            r = self.run(p)
            if r is not None and tuid in r.uses:
                return p
        return None

    def children(self, run):
        """[(child path, meta, spawn ts, return ts)] of the sub-agents this run spawned."""
        out = []
        for tuid, use in run.uses.items():
            p = self.by_tuid.get(tuid)
            if p is None:
                continue
            child = self.run(p)
            res = run.results.get(tuid)
            ret = res[0] if res else None
            if child is not None and child.t1 is not None:
                ret = child.t1 if ret is None or any(a in res[1] for a in ASYNC) else max(ret, child.t1)
            out.append((p, self.metas[p], use["ts"], ret if ret is not None else use["ts"]))
        return out

    def tree_usd(self, path, seen=None):
        seen = seen if seen is not None else set()
        if path in seen:
            return 0.0
        seen.add(path)
        r = self.run(path)
        if r is None:
            return 0.0
        return r.usd() + sum(self.tree_usd(p, seen) for p, _, _, _ in self.children(r))


def candidates(roots, type_re, fails):
    """[(sub-agent path, meta, main session path)] of every matched agent type under the roots."""
    out = []
    for root in roots:
        root = locate.real(root)
        try:
            projs = sorted(os.listdir(root))
        except OSError as e:
            fails.append((root, str(e)))
            continue
        for pd in projs:
            for dp, _, fs in os.walk(os.path.join(root, pd)):
                if not dp.endswith(os.sep + "subagents"):
                    continue
                for f in fs:
                    if not f.endswith(".meta.json"):
                        continue
                    meta, why = locate.read_meta(os.path.join(dp, f))
                    if meta is None:
                        fails.append((os.path.join(dp, f), why))
                    elif isinstance(meta.get("agentType"), str) and type_re.search(meta["agentType"]):
                        out.append((os.path.join(dp, f[:-len(".meta.json")] + ".jsonl"), meta,
                                    os.path.dirname(dp) + ".jsonl"))
    return out


def findings(final):
    """(count, review path) from the file the run's final line names; count '-' when it is not on disk."""
    m = REVIEW_PATH.findall(final or "")
    if not m:
        return "-", "-"
    p = m[-1]
    try:
        with open(p, errors="replace") as f:
            return str(len(set(FINDING.findall(f.read())))), p
    except OSError:
        return "-", p


def measure(path, meta, sess, host, gap_s, child_re):
    r = sess.run(path)
    if r is None or r.t0 is None:
        return None
    kids = sess.children(r)
    out_spans = [(s, e) for _, _, s, e in kids]
    tracers = [(p, s, e) for p, m, s, e in kids if child_re.search(str(m.get("agentType") or ""))]
    longest = total = dormant = 0.0
    for c in r.calls:
        if c["gap_from"] is None:
            continue
        # idle: from the previous response to this request's send no request was in flight
        a, b = c["gap_from"], max(c["sent"], c["gap_from"])
        idle = overlap(a, b, out_spans) if b - a > IDLE_MIN else 0.0
        c["child_out"] = overlap(a, b, out_spans) > 0
        total += idle
        longest = max(longest, idle)
        if b - a - overlap(a, b, out_spans) > gap_s:
            dormant += b - a - overlap(a, b, out_spans)  # a stretch with no child out: the run waited on a message
    rewrites = [c for c in r.calls if c["gap"] > gap_s and spiked(c)]
    child_rewrites = [c for c in rewrites if c.get("child_out")]
    held = [c for c in r.calls if c["gap"] > gap_s and not spiked(c)]
    tuid = meta.get("toolUseId") if isinstance(meta.get("toolUseId"), str) else ""
    ppath = sess.holder(tuid) if tuid else None
    parent = sess.run(ppath) if ppath else None
    ptype = "main" if ppath == sess.main else str(sess.metas.get(ppath, {}).get("agentType") or "-") if ppath \
        else "NOT FOUND"
    pidle = ptotal = pusd = 0.0
    prew, sibs = [], 0
    w0, w1 = r.t0, r.t1
    if parent is not None:
        w0 = parent.uses[tuid]["ts"]
        for c in parent.calls:
            if c["gap_from"] is not None:
                a, b = max(c["gap_from"], w0), min(max(c["sent"], c["gap_from"]), w1)
                if b - a > IDLE_MIN:
                    pidle = max(pidle, b - a)
                    ptotal += b - a
                if c["gap"] > gap_s and spiked(c) and c["gap_from"] < w1 and c["ts"] > w0:
                    prew.append(c)
            if w0 < c["ts"] <= w1:
                pusd += c["usd"]
        sibs = sum(1 for p, _, s, e in sess.children(parent) if p != path and s < w1 and e > w0)
    fc, rp = findings(r.final)
    seen = {path}
    tracer_usd = sum(sess.tree_usd(p, seen) for p, _, _ in tracers)
    stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(r.t0))
    return dict(
        transcript_path=path, host=host, parent_type=ptype, reviewer_wall_s=round(r.t1 - r.t0),
        tracer_runs=len(tracers), tracer_wall_s=round(sum(e - s for _, s, e in tracers)),
        reviewer_longest_idle_s=round(longest), reviewer_idle_total_s=round(total), parent_idle_s=round(pidle),
        cache_rewrites_after_wait=len(rewrites), rewrite_tokens=sum(c["lost"] for c in rewrites),
        rewrite_usd=f"{sum(c['lost_usd'] for c in rewrites):.4f}", usd_reviewer=f"{r.usd():.4f}",
        usd_tracer_tree=f"{tracer_usd:.4f}", usd_parent_during_wait=f"{pusd:.4f}", findings_count=fc,
        parent_idle_total_s=round(ptotal), parent_rewrites=len(prew), parent_rewrite_tokens=sum(c["lost"] for c in prew),
        parent_rewrite_usd=f"{sum(c['lost_usd'] for c in prew):.4f}", waits_over_gap_no_spike=len(held),
        w5m_tokens=sum(c["w5"] for c in r.calls), w1h_tokens=sum(c["w1"] for c in r.calls),
        parent_w5m_tokens=sum(c["w5"] for c in parent.calls) if parent else 0,
        parent_w1h_tokens=sum(c["w1"] for c in parent.calls) if parent else 0, children_any=len(kids),
        concurrent_siblings=sibs, reviewer_dormant_s=round(dormant), rewrites_child_wait=len(child_rewrites),
        rewrite_usd_child_wait=f"{sum(c['lost_usd'] for c in child_rewrites):.4f}", agent_type=meta.get("agentType"), start_utc=stamp, parent_path=ppath or "-",
        review_path=rp, _t0=r.t0, _unpriced=r.unpriced + (parent.unpriced if parent else 0),
        _prew_keys=[(ppath, c["key"], c["lost_usd"], c["lost"]) for c in prew], _pkeys=[
            (ppath, c["key"], c["usd"]) for c in (parent.calls if parent else []) if w0 < c["ts"] <= w1],
        _side=([(c["gap"] > gap_s, spiked(c), c["w1"] > 0) for c in r.calls[1:]],
               [(c["gap"] > gap_s, spiked(c), c["w1"] > 0) for c in (parent.calls[1:] if parent else [])
                if c["gap_from"] is not None and c["gap_from"] < w1 and c["ts"] > w0]),
        _base=[(c["gap"] > gap_s, spiked(c)) for c in r.calls[1:]] + [
            (c["gap"] > gap_s, spiked(c)) for c in (parent.calls[1:] if parent else [])])


def calls_table(target):
    """Every model call of one transcript: response and send stamps, the cache gap, and its usage split."""
    try:
        path = locate.resolve(target)
        engine = load_jsonl(path).engine
        if engine != CLAUDE:
            raise Unreadable(f"a {engine} transcript; waits reads Claude transcripts only")
        r = Run(path, cost.prices.load(cli.KIT_DIR))
    except (locate.NotFound, Failure, Unreadable) as e:
        die(f"FAILED {target} — {e}")
    print("n\tresponse_utc\tsent_utc\tgap_s\tctx\tinput\tcache_read\tw5m\tw1h\tprev_ctx\tlost\tspiked\tusd\tlost_usd\tmodel")
    for i, c in enumerate(r.calls, 1):
        print(f"{i}\t{time.strftime('%H:%M:%S', time.gmtime(c['ts']))}\t{time.strftime('%H:%M:%S', time.gmtime(c['sent']))}"
              f"\t{c['gap']:.0f}\t{c['ctx']}\t{c['inp']}\t{c['cr']}\t{c['w5']}\t{c['w1']}\t{c['prev_ctx']}\t{c['lost']}\t{'Y' if spiked(c) else '-'}"
              f"\t{c['usd']:.4f}\t{c['lost_usd']:.4f}\t{c['model']}")
    print(f"# {len(r.calls)} calls · {r.unpriced} unpriced · {r.bad} malformed records · {r.path}")


def stats(vals):
    vals = sorted(vals)
    if not vals:
        return "-", "-"
    p90 = vals[min(len(vals) - 1, math.ceil(0.9 * len(vals)) - 1)]
    return statistics.median(vals), p90


def waits(args):
    began = time.time()
    one = opt(args, "--calls")
    if one is not None:
        if args:
            die(USAGE, 2)
        calls_table(one)
        return
    type_re = re.compile(opt(args, "--type-re", DEFAULT_TYPES))
    child_re = re.compile(opt(args, "--child-re", DEFAULT_CHILD))
    since = opt(args, "--since", "14d")
    host = opt(args, "--host", "mac")
    gap_s = float(opt(args, "--gap", "300"))
    out = opt(args, "--out")
    roots = []
    while "--root" in args:
        roots.append(opt(args, "--root"))
    if args:
        die(USAGE, 2)
    roots = roots or locate.claude_roots()
    cutoff = locate.since_cutoff(since)
    book = cost.prices.load(cli.KIT_DIR)
    fails, rows, sessions = [], [], {}
    for path, meta, main in candidates(roots, type_re, fails):
        sess = sessions.setdefault(main, Session(main, book, fails))
        try:
            row = measure(path, meta, sess, host, gap_s, child_re)
        except Exception as e:  # one transcript's fault must not end the sweep: named, exit 1
            fails.append((path, f"internal error {type(e).__name__}: {e}"))
            continue
        if row is None:
            if all(s != path for s, _ in fails):
                fails.append((path, "no stamped record"))
        elif row["_t0"] >= cutoff:
            rows.append(row)
    rows.sort(key=lambda r: r["_t0"])
    lines = ["\t".join(COLS)] + ["\t".join(str(r[c]) for c in COLS) for r in rows]
    # the summary: medians and p90, totals with each parent call counted once however many runs it waited on
    summ = [f"RUNS {len(rows)} · types {type_re.pattern} · children {child_re.pattern} · gap {gap_s:.0f}s · since "
            f"{since} · roots {' '.join(roots)}"]
    for c in COLS[3:30]:
        try:
            vals = [float(r[c]) for r in rows if r[c] != "-"]
        except ValueError:
            continue
        med, p90 = stats(vals)
        summ.append(f"STAT {c} · n {len(vals)} · median {med} · p90 {p90} · sum {round(sum(vals), 4)}")
    prew = {(p, k): (x, w) for r in rows for p, k, x, w in r["_prew_keys"]}
    pdur = {(p, k): u for r in rows for p, k, u in r["_pkeys"]}
    rv_x = sum(float(r["rewrite_usd"]) for r in rows)
    summ.append(f"LOST reviewer rewrite excess ${rv_x:.4f} · parent rewrite excess (each parent call once) "
                f"${sum(x for x, _ in prew.values()):.4f} over {len(prew)} calls, "
                f"{sum(w for _, w in prew.values())} tokens · parent usd during waits (each call once) "
                f"${sum(pdur.values()):.4f}")
    wall = sum(r["reviewer_wall_s"] for r in rows)
    summ.append(f"IDLE reviewer idle while a child was out {sum(r['reviewer_idle_total_s'] for r in rows)} s of "
                f"{wall} s wall")
    base = [b for r in rows for b in r["_base"]]
    long_ = [s for g, s in base if g]
    short = [s for g, s in base if not g]
    summ.append(f"BASELINE calls after a gap > {gap_s:.0f}s: {len(long_)}, spiked {sum(long_)} · after a shorter gap: "
                f"{len(short)}, spiked {sum(short)} (reviewer and parent calls, a parent shared by runs counted per run)")
    w5, w1 = sum(r["w5m_tokens"] for r in rows), sum(r["w1h_tokens"] for r in rows)
    summ.append(f"TTL reviewer cache writes ephemeral_5m {w5} · ephemeral_1h {w1} · runs writing any 1h "
                f"{sum(1 for r in rows if r['w1h_tokens'])} · parents (each once) ephemeral_5m "
                f"{sum(dict((r['parent_path'], r['parent_w5m_tokens']) for r in rows).values())} · ephemeral_1h "
                f"{sum(dict((r['parent_path'], r['parent_w1h_tokens']) for r in rows).values())}")
    for side, k in (("reviewer", 0), ("parent", 1)):
        b = [x for r in rows for x in r["_side"][k]]
        summ.append(f"SIDE {side} calls after a gap > {gap_s:.0f}s: {sum(1 for g, _, _ in b if g)}, spiked "
                    f"{sum(1 for g, s, _ in b if g and s)} (of those writing 1h {sum(1 for g, s, h in b if g and s and h)}),"
                    f" held {sum(1 for g, s, _ in b if g and not s)} (of those writing 1h "
                    f"{sum(1 for g, s, h in b if g and not s and h)}) · after a shorter gap spiked "
                    f"{sum(1 for g, s, _ in b if not g and s)} of {sum(1 for g, _, _ in b if not g)}")
    unp = sum(r["_unpriced"] for r in rows)
    if unp:
        summ.append(f"UNPRICED {unp} calls (reviewer or parent) carry no rate: their dollars count as $0")
    if out:
        try:
            with open(out, "w") as f:
                f.write("\n".join(lines) + "\n")
            summ.append(f"OUT {out}")
        except OSError as e:
            fails.append((out, f"cannot write: {e}"))
    fails = list(dict.fromkeys(fails))  # a meta both the sweep and its session read fails once
    gaps = [f"GAPS {p} — {r.bad} malformed records skipped: its numbers count without them"
            for sess in sessions.values() for p, r in sorted(sess.runs.items()) if r is not None and r.bad]
    print("\n".join((lines if not out else []) + summ + gaps + [f"FAILED {s} — {w}" for s, w in fails]
                    + [f"# waits {len(rows)} runs · {len(fails)} FAILED · {time.time() - began:.1f}s"]))
    if fails:
        sys.exit(1)
