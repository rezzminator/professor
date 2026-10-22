"""Response-level audit metrics, with explicit sources and missing-usage gaps.

Unlike the legacy timeline, Codex usage comes only from token_usage_record,
uniqued by response_id, never from cumulative token_count. Claude usage may
be joined to a read-only gateway database by the manifest's agent identity.
"""
import collections
import json
import os
import re
import shlex
import sqlite3
from datetime import datetime, timezone

from .engines import load
from .cost.claude import read
from .model import parse_stamp
from .rows import die, opt


def _price(book, model):
    key = re.sub(r"\[.*$", "", model or "")
    for row in book.get("rows", []):
        if row.get("key") == key:
            return row
    return None


def _read_paths(ev):
    """Explicit file operands only; shell expansion/dynamic paths stay unknown."""
    try:
        obj = json.loads(ev.raw)
    except (ValueError, TypeError):
        obj = {}
    if ev.name in ("Read", "functions.Read"):
        path = obj.get("file_path")
        return [path] if path else []
    if not ev.cmd and ev.name != "exec":
        return None
    cmd = obj.get("command", obj.get("cmd", "")) if isinstance(obj, dict) else ""
    if not cmd and ev.name == "exec":
        cmd = ev.text
    if isinstance(cmd, list):
        cmd = " ".join(map(str, cmd))
    paths = []
    # Deliberately conservative: report explicit cat/sed/head/tail operands,
    # not every path a search command mentions or every file its glob expands.
    for part in re.split(r"[;\n]|&&|\|\|", cmd):
        try:
            words = shlex.split(part)
        except ValueError:
            continue
        if not words or words[0] not in ("cat", "sed", "head", "tail"):
            continue
        for word in words[1:]:
            if (word.startswith(("/", "./", "../")) or re.fullmatch(r"[\w./-]+\.[\w]+", word)) and not re.search(r"[$*?{}]", word):
                paths.append(word)
    return paths or None


def measure(target, db, book):
    before = os.stat(target["path"])
    p = load(target["path"])
    recs = read(target["path"])
    after = os.stat(target["path"])
    result = dict(target)
    result.update(engine=p.engine, bytes=p.bytes, model=p.meta.get("model"),
                  effort=p.meta.get("effort"), gaps=[], meta=p.meta,
                  measured_utc=datetime.now(timezone.utc).isoformat())
    if (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns):
        result["gaps"].append("Source changed during measurement; rerun metrics for a stable per-file observation")
    calls = [e for e in p.events if e.kind == "call" and not e.hidden]
    raw_codex_calls = [(line, r.get("payload") or {}) for line, r in recs if r and r.get("type") == "response_item" and (r.get("payload") or {}).get("type") in ("function_call", "custom_tool_call")]
    result["rendered_operations"] = len(calls)
    result["tools"] = dict(collections.Counter(pl.get("name", "?") for _, pl in raw_codex_calls)) if p.engine == "codex" else dict(collections.Counter(e.name for e in calls))
    result["tool_calls"] = len(raw_codex_calls) if p.engine == "codex" else len(calls)
    result["tool_call_lines"] = [n for n, _ in raw_codex_calls] if p.engine == "codex" else [e.line for e in calls]
    result["tool_errors"] = [dict(line=e.line, tool=e.name, input=e.raw[:350],
                                  result=(e.result or "")[:700]) for e in calls if e.err]
    result["compactions"] = sum("COMPACTION" in e.mech for e in p.events)
    result["wait_calls"] = [dict(line=e.line, tool=e.name, seconds=e.dur, input=e.raw[:300])
        for e in calls if re.search(r"wait_agent|TaskOutput|write_stdin|sleep\s", e.name + " " + e.raw)]
    result["calls_over_60s"] = [dict(line=e.line, tool=e.name, seconds=e.dur)
                                for e in calls if e.dur is not None and e.dur > 60]
    result["spawn_calls"] = [dict(line=e.line, tool=e.name, input=e.raw[:1600])
                               for e in calls if "SPAWN" in e.mech]
    result["file_reads"] = [dict(line=e.line, tool=e.name, explicit_paths=_read_paths(e))
                              for e in calls if e.name in ("Read", "functions.Read", "exec") or e.cmd]
    result["file_read_method"] = "Read is exact; shell operands are conservative lower bounds, dynamic/glob reads unknown"
    result["explicit_written_paths"] = sorted({str(f) for e in calls for f in (e.writes or [])})
    result["wall_seconds"] = (p.t1-p.t0).total_seconds() if p.t0 and p.t1 else None
    result["start_utc"] = p.t0.isoformat() if p.t0 else None
    result["end_utc"] = p.t1.isoformat() if p.t1 else None
    finals = [e for e in p.events if e.kind in ("final", "reply") and not e.hidden]
    result["last_reply"] = dict(line=finals[-1].line, text=finals[-1].text[:1500]) if finals else None
    result["outcome"] = "NOT VERIFIED"
    if finals:
        text = finals[-1].text.strip()
        if re.match(r"(?:\*\*)?DONE\b", text): result["outcome"] = "DONE (self-reported)"
        elif re.match(r"(?:\*\*)?PARTIAL\b", text): result["outcome"] = "PARTIAL"
    usage, batches, seen, contexts = [], [], set(), []
    cumulative = None
    if p.engine == "codex":
        pending = 0
        for line, r in recs:
            if not r: continue
            pl = r.get("payload") or {}
            kind = r.get("type")
            if kind == "turn_context":
                contexts.append(dict(line=line, model=pl.get("model"), effort=pl.get("effort")))
            if kind == "response_item" and pl.get("type") in ("function_call", "custom_tool_call"):
                pending += 1
            if kind == "event_msg" and pl.get("type") == "token_count":
                cumulative = (pl.get("info") or {}).get("total_token_usage")
            if kind != "token_usage_record": continue
            rid = pl.get("response_id")
            if not rid:
                result["gaps"].append(f"L{line}: token_usage_record missing response_id")
                continue
            if rid in seen: continue
            seen.add(rid)
            u = pl.get("usage")
            if not isinstance(u, dict):
                result["gaps"].append(f"L{line}: response {rid} missing usage")
                continue
            usage.append(dict(line=line, response_id=rid, model=(contexts[-1].get("model") if contexts else result["model"]),
                              input_tokens=u.get("input_tokens", 0), cached_input_tokens=u.get("cached_input_tokens", 0),
                              output_tokens=u.get("output_tokens", 0), reasoning_tokens=u.get("reasoning_output_tokens", 0)))
            batches.append(pending)
            pending = 0
        result["usage_source"] = "token_usage_record deduplicated by response_id; no cumulative usage added"
        result["cumulative_crosscheck_last_segment_only"] = cumulative
        result["tool_calls_unassigned_after_last_response"] = pending
    else:
        messages = collections.OrderedDict()
        for line, r in recs:
            if r and r.get("type") == "api-request":
                params = r.get("params") or {}
                effort = (params.get("output_config") or {}).get("effort")
                if effort:
                    contexts.append(dict(line=line, model=params.get("model"), effort=effort))
                    result["effort"] = effort
            if not r or r.get("type") != "assistant": continue
            msg = r.get("message") or {}
            key = msg.get("id") or r.get("uuid") or str(line)
            # Streaming fragments can repeat an assistant message id; tool ids
            # prevent counting a re-emitted block twice.
            tools = messages.setdefault(key, set())
            for i,b in enumerate(msg.get("content") or []):
                if isinstance(b,dict) and b.get("type") == "tool_use":
                    tools.add(b.get("id") or (line,i))
        batches = [len(v) for v in messages.values()]
        result["assistant_messages"] = len(messages)
        if db is None:
            result["gaps"].append("Claude gateway database was not supplied")
        else:
            rows = [dict(r) for r in db.execute("SELECT * FROM requests WHERE agent_id=? ORDER BY started,id",(target["id"],))]
            result["gateway_rows"] = len(rows)
            result["gateway_statuses"] = dict(collections.Counter(str(r["status"]) for r in rows))
            result["gateway_attempts"] = sum(r.get("attempts") or 0 for r in rows)
            result["gateway_retry_rows"] = [dict(id=r["id"],status=r["status"],attempts=r["attempts"],error=r.get("error_head")) for r in rows if (r.get("attempts") or 0)>1 or r.get("error_head")]
            result["gateway_models"] = dict(collections.Counter(r["model"] for r in rows))
            result["gateway_local_responses"] = dict(collections.Counter(r.get("note") for r in rows if (r.get("note") or "").startswith("answered=")))
            for r in rows:
                if (r.get("note") or "").startswith("answered="):
                    continue
                if r.get("input_tokens") is None or r.get("output_tokens") is None:
                    result["gaps"].append(f"gateway row {r['id']}: missing token usage (status {r['status']})")
                    continue
                usage.append(dict(gateway_id=r["id"], request_id=r["request_id"],model=r["model"],
                                  input_tokens=r["input_tokens"],cached_input_tokens=r["cache_read_tokens"] or 0,
                                  output_tokens=r["output_tokens"],reasoning_tokens=r["reasoning_tokens"] or 0,
                                  started=r["started"], ended=r["ended"]))
            if rows:
                result["gateway_wall_seconds"] = max(r["ended"] or r["started"] for r in rows)-min(r["started"] for r in rows)
            if not rows: result["gaps"].append("No gateway rows joined by exact agent_id")
        result["usage_source"] = "gateway requests joined by exact agent_id; reasoning is a subset of output"
    result["contexts"] = contexts
    result["batch_histogram_including_zero_tool_responses"] = dict(collections.Counter(batches))
    nonzero = [n for n in batches if n]
    result["tools_per_tool_bearing_response"] = sum(nonzero)/len(nonzero) if nonzero else None
    result["parallel_tool_responses"] = sum(n>1 for n in batches)
    result["model_requests_with_usage"] = len(usage)
    if not usage:
        result["gaps"].append("No response-level usage; tokens and price NOT VERIFIED")
        result["tokens"] = result["api_equivalent_usd"] = None
    else:
        totals = {k:sum(u[k] or 0 for u in usage) for k in ("input_tokens","cached_input_tokens","output_tokens","reasoning_tokens")}
        totals["uncached_input_tokens"] = totals["input_tokens"]-totals["cached_input_tokens"]
        totals["cache_hit_fraction"] = totals["cached_input_tokens"]/totals["input_tokens"] if totals["input_tokens"] else None
        result["tokens"] = totals
        result["first_request"] = usage[0]
        result["last_request"] = usage[-1]
        components = dict(uncached_input=0.0,cached_input=0.0,output=0.0)
        missing = set()
        for u in usage:
            price = _price(book,u["model"])
            if not price:
                missing.add(u["model"])
                continue
            components["uncached_input"] += (u["input_tokens"]-u["cached_input_tokens"])*price["in"]/1e6
            components["cached_input"] += u["cached_input_tokens"]*price.get("cached",price.get("hit",0))/1e6
            components["output"] += u["output_tokens"]*price["out"]/1e6
        result["price_components_usd"] = components
        result["api_equivalent_usd"] = None if missing else sum(components.values())
        if missing: result["gaps"].append("No supplied price for model(s): "+str(sorted(missing)))
        usd=result["api_equivalent_usd"]
        result["normalized"] = dict(usd_per_request=usd/len(usage) if usd is not None else None,
            usd_per_tool_call=usd/result["tool_calls"] if usd is not None and result["tool_calls"] else None,
            input_tokens_per_request=totals["input_tokens"]/len(usage),
            output_tokens_per_request=totals["output_tokens"]/len(usage))
    result["per_request_usage"] = usage
    return result


def metrics(args):
    manifest = opt(args,"--manifest")
    gateway = opt(args,"--gateway")
    prices = opt(args,"--prices")
    out = opt(args,"--out")
    if args or not manifest or not out or not prices:
        die("usage: chatlog.py metrics --manifest FILE --prices FILE --out FILE [--gateway DB]",2)
    with open(manifest) as f: targets=json.load(f)
    with open(prices) as f: book=json.load(f)
    db=None
    if gateway:
        db=sqlite3.connect("file:"+os.path.abspath(gateway)+"?mode=ro",uri=True)
        db.row_factory=sqlite3.Row
    results=[]
    for target in targets:
        try:
            r=measure(target,db,book)
        except Exception as e:
            r=dict(target,failed=f"{type(e).__name__}: {e}")
        results.append(r)
        print(target["id"],"FAILED "+r["failed"] if "failed" in r else
              f"{r['engine']} requests={r['model_requests_with_usage']} tools={r['tool_calls']} usd={r['api_equivalent_usd']} gaps={len(r['gaps'])}")
    with open(out,"w") as f:json.dump(results,f,indent=2)
    if db: db.close()
