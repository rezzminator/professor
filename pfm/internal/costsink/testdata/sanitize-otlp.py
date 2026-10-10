#!/usr/bin/env python3
"""The only writer of probe-session.jsonl, the OTLP replay fixture of internal/costsink.

usage: python3 -I sanitize-otlp.py RAW_LOGS RAW_TRACES OUT

RAW_LOGS and RAW_TRACES are receiver captures, one OTLP/HTTP JSON POST per line:
`{"t": <epoch seconds>, "ct": "..."}<TAB><body>`. OUT receives one line per POST in
arrival order: `{"t": <seconds since the first POST>, "path": "/v1/logs"|"/v1/traces",
"body": <sanitized body>}`.

Every identifying value becomes an invented placeholder, every attribute and span field
outside an allow-list is dropped, only the first record of each other event or span name is
kept (enough to prove the sink ignores them), and the run refuses (writing nothing) when any original
identifying value, a machine path or a foreign address is still in the output.
"""
import json
import re
import sys

PII = {
    "user.id": "0000000000000000000000000000000000000000000000000000000000000001",
    "user.email": "user@example.invalid",
    "user.account_id": "user_fixture0000000000000001",
    "user.account_uuid": "00000000-0000-4000-8000-00000000a001",
    "organization.id": "00000000-0000-4000-8000-00000000b001",
}
RESOURCE_KEYS = {"host.arch", "os.type", "os.version", "service.name", "service.version"}
COMMON = {"session.id", "terminal.type", "event.name", "event.timestamp", "event.sequence", "prompt.id"}
API_REQUEST_KEYS = COMMON | {
    "model", "input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens",
    "cost_usd", "cost_usd_micros", "duration_ms", "ttft_ms", "request_id", "speed",
    "query_source", "effort", "agent.name",
}
LLM_SPAN_KEYS = {
    "session.id", "terminal.type", "span.type", "model", "gen_ai.system", "gen_ai.request.model",
    "llm_request.context", "speed", "query_source_safe", "duration_ms", "input_tokens",
    "output_tokens", "cache_read_tokens", "cache_creation_tokens", "success", "attempt",
    "request_id", "gen_ai.response.id", "ttft_ms", "first_content_ms", "effort", "stop_reason",
    "gen_ai.response.finish_reasons", "agent_id", "parent_agent_id",
}
OTHER_SPAN_KEYS = {"session.id", "terminal.type", "span.type"}
BUILTIN_AGENT_NAMES = {"general-purpose", "Explore", "Plan"}
SCOPE_NAMES = {"com.anthropic.claude_code.events", "com.anthropic.claude_code.tracing"}


class Mapper:
    """Assigns each original id its invented stand-in, in order of first appearance."""

    def __init__(self):
        self.seen = {}
        self.originals = set()

    def get(self, kind, original, fmt):
        key = (kind, original)
        if key not in self.seen:
            n = 1 + sum(1 for k in self.seen if k[0] == kind)
            self.seen[key] = fmt(n)
            self.originals.add(original)
        return self.seen[key]


M = Mapper()
NOISE_SEEN = set()
FORMATS = {
    "session": lambda n: "00000000-0000-4000-8000-0000000000%02x" % (0xa0 + n),
    "prompt": lambda n: "00000000-0000-4000-8000-00000000c%03x" % n,
    "request": lambda n: "req_011CFixture%08d" % n,
    "agent": lambda n: "a%016x" % n,
    "trace": lambda n: "%032x" % n,
    "span": lambda n: "%016x" % n,
}
KIND_OF_KEY = {
    "session.id": "session", "prompt.id": "prompt", "request_id": "request",
    "gen_ai.response.id": "request", "agent_id": "agent", "parent_agent_id": "agent",
}


def attr_value(v):
    for k in ("stringValue", "intValue", "doubleValue", "boolValue"):
        if k in v:
            return v[k]
    return None


def clean_attrs(attrs, allowed):
    out = []
    for item in attrs:
        key, value = item["key"], item["value"]
        if key in PII:
            out.append({"key": key, "value": {"stringValue": PII[key]}})
            continue
        if key not in allowed:
            continue
        if key in KIND_OF_KEY:
            kind = KIND_OF_KEY[key]
            original = attr_value(value)
            value = {"stringValue": M.get(kind, original, FORMATS[kind])}
        elif key == "agent.name":
            name = attr_value(value)
            if name not in BUILTIN_AGENT_NAMES:
                name = "custom-agent"
            value = {"stringValue": name}
        out.append({"key": key, "value": value})
    return out


def id_of(kind, original):
    if not original:
        return original
    return M.get(kind, original, FORMATS[kind])


def clean_resource(resource):
    return {
        "attributes": [a for a in resource.get("attributes", []) if a["key"] in RESOURCE_KEYS],
        "droppedAttributesCount": 0,
    }


def clean_log_body(body):
    out = {"resourceLogs": []}
    for rl in body.get("resourceLogs", []):
        scopes = []
        for sl in rl.get("scopeLogs", []):
            records = []
            for r in sl.get("logRecords", []):
                attrs = {a["key"]: attr_value(a["value"]) for a in r.get("attributes", [])}
                name = attrs.get("event.name")
                if name != "api_request":
                    if ("log", name) in NOISE_SEEN:
                        continue
                    NOISE_SEEN.add(("log", name))
                allowed = API_REQUEST_KEYS if name == "api_request" else COMMON
                rec = {k: v for k, v in r.items() if k not in ("attributes", "traceId", "spanId")}
                rec["attributes"] = clean_attrs(r.get("attributes", []), allowed)
                if r.get("traceId"):
                    rec["traceId"] = id_of("trace", r["traceId"])
                if r.get("spanId"):
                    rec["spanId"] = id_of("span", r["spanId"])
                records.append(rec)
            scope = sl.get("scope", {})
            if records:
                scopes.append({"scope": {"name": scope.get("name") if scope.get("name") in SCOPE_NAMES else "scope",
                                          "version": scope.get("version", "")},
                               "logRecords": records})
        if scopes:
            out["resourceLogs"].append({"resource": clean_resource(rl.get("resource", {})), "scopeLogs": scopes})
    return out


def clean_trace_body(body):
    out = {"resourceSpans": []}
    for rs in body.get("resourceSpans", []):
        scopes = []
        for ss in rs.get("scopeSpans", []):
            spans = []
            for s in ss.get("spans", []):
                if s.get("name") != "claude_code.llm_request":
                    if ("span", s.get("name")) in NOISE_SEEN:
                        continue
                    NOISE_SEEN.add(("span", s.get("name")))
                allowed = LLM_SPAN_KEYS if s.get("name") == "claude_code.llm_request" else OTHER_SPAN_KEYS
                span = {k: v for k, v in s.items()
                        if k not in ("attributes", "traceId", "spanId", "parentSpanId", "events", "links")}
                span["traceId"] = id_of("trace", s.get("traceId"))
                span["spanId"] = id_of("span", s.get("spanId"))
                if s.get("parentSpanId"):
                    span["parentSpanId"] = id_of("span", s["parentSpanId"])
                span["attributes"] = clean_attrs(s.get("attributes", []), allowed)
                span["events"] = []
                span["links"] = []
                spans.append(span)
            scope = ss.get("scope", {})
            if spans:
                scopes.append({"scope": {"name": scope.get("name") if scope.get("name") in SCOPE_NAMES else "scope",
                                          "version": scope.get("version", "")},
                               "spans": spans})
        if scopes:
            out["resourceSpans"].append({"resource": clean_resource(rs.get("resource", {})), "scopeSpans": scopes})
    return out


def read_posts(path, cleaner, url):
    posts, raw_values = [], set()
    with open(path, encoding="utf-8") as fh:
        for n, line in enumerate(fh, 1):
            line = line.rstrip("\n")
            if not line:
                continue
            meta, _, body = line.partition("\t")
            if not body:
                sys.exit("%s:%d: no tab-separated body" % (path, n))
            doc = json.loads(body)
            collect_pii(doc, raw_values)
            cleaned = cleaner(doc)
            if any(cleaned.values()):
                posts.append((json.loads(meta)["t"], url, cleaned))
    return posts, raw_values


def collect_pii(node, found):
    """Gathers every original value an output must not contain."""
    if isinstance(node, dict):
        if "key" in node and "value" in node and node["key"] in PII:
            v = attr_value(node["value"])
            if v:
                found.add(str(v))
        for v in node.values():
            collect_pii(v, found)
    elif isinstance(node, list):
        for v in node:
            collect_pii(v, found)


def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    logs_path, traces_path, out_path = sys.argv[1:]
    logs, pii_l = read_posts(logs_path, clean_log_body, "/v1/logs")
    traces, pii_t = read_posts(traces_path, clean_trace_body, "/v1/traces")
    posts = sorted(logs + traces, key=lambda p: p[0])
    base = posts[0][0]
    lines = [json.dumps({"t": round(t - base, 1), "path": path, "body": body}, separators=(",", ":"), sort_keys=True)
             for t, path, body in posts]
    text = "\n".join(lines) + "\n"
    leaks = [v for v in (pii_l | pii_t | M.originals) if v and v in text]
    if leaks:
        sys.exit("refusing: %d original identifying values remain in the output" % len(leaks))
    for pattern in (r"/Users/", r"/home/", r"/private/", r"@(?!example\.invalid)[A-Za-z0-9-]+\.[a-z]+"):
        m = re.search(pattern, text)
        if m:
            sys.exit("refusing: %r found in the output at offset %d" % (pattern, m.start()))
    with open(out_path, "w", encoding="utf-8") as fh:
        fh.write(text)
    print("wrote %d posts to %s" % (len(lines), out_path))


if __name__ == "__main__":
    main()
