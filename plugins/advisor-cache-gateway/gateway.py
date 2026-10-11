"""advisor-cache-gateway: a local passthrough to the Anthropic API for Claude Code.

Claude Code sends the advisor server tool without `caching`, so the advisor
re-reads the whole transcript at full price on every call. This gateway adds
`caching: {"type": "ephemeral", "ttl": TTL}` (and, when set, `max_tokens`) to
the advisor tool of each POST /v1/messages and passes everything else through
byte for byte, streaming included. Credentials pass through and are never logged.

Claude Code reaches it through `ANTHROPIC_BASE_URL=http://127.0.0.1:PORT` in
~/.claude/settings.json `env`, which run.sh sets only while this gateway answers.
Python 3.9+ standard library only.

Usage:
  gateway.py            serve
  gateway.py stats      summarize advisor cache use from the log

Env:
  ADVISOR_CACHE_GATEWAY_PORT   listen port on 127.0.0.1 (18787)
  ADVISOR_CACHE_TTL            main sessions: 1h | 5m | off (5m)
  ADVISOR_CACHE_TTL_SUBAGENT   sub-agents, the requests carrying x-claude-code-agent-id: 1h | 5m | off (5m)
                               A conversation keeps one TTL: a 1h request does not read a 5m entry (measured).
  ADVISOR_MAX_TOKENS           cap on the advisor's output per call, >= 1024 (unset: no cap)
  ADVISOR_CACHE_UPSTREAM       upstream origin (https://api.anthropic.com)
  ADVISOR_CACHE_GATEWAY_LOG    log file (~/Library/Logs/advisor-cache-gateway.log)
  ADVISOR_CACHE_CAPTURE_DIR    full literal capture of every request and response (~/.professor/tmp/gw-logs; off disables)
  ADVISOR_CACHE_CAPTURE_KEEP_DAYS  capture day folders older than this many days are deleted (7)
  ADVISOR_CACHE_UPSTREAM_IDLE  an upstream connection is reused only this many seconds after its last request began (20)
  ADVISOR_CACHE_DRAIN_SECONDS  on SIGTERM: stop accepting, let in-flight requests finish for up to this long (600)
  ADVISOR_GATEWAY_LISTEN_FD    serve this inherited listening socket instead of binding the port (run.sh sets it
                               under systemd socket activation, so every gateway generation shares one queue)
  ADVISOR_CACHE_DEBUG          set to log each advisor request's thinking config and message shape
"""
import http.client
import json
import os
import re
import signal
import socket
import ssl
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

PORT = int(os.environ.get("ADVISOR_CACHE_GATEWAY_PORT", "18787"))
TTL = os.environ.get("ADVISOR_CACHE_TTL", "5m")
TTL_SUBAGENT = os.environ.get("ADVISOR_CACHE_TTL_SUBAGENT", "5m")
TTLS = ("1h", "5m", "off")


def ttl_for(headers):
    """(kind, ttl) for a request: sub-agents carry x-claude-code-agent-id, main sessions do not.

    An unknown TTL value falls back to the default of its kind.
    """
    if headers.get("x-claude-code-agent-id"):
        return "sub", TTL_SUBAGENT if TTL_SUBAGENT in TTLS else "5m"
    return "main", TTL if TTL in TTLS else "5m"
MAX_TOKENS = os.environ.get("ADVISOR_MAX_TOKENS", "").strip()
UPSTREAM = urlsplit(os.environ.get("ADVISOR_CACHE_UPSTREAM", "https://api.anthropic.com"))
LOG_PATH = os.path.expanduser(os.environ.get("ADVISOR_CACHE_GATEWAY_LOG", "~/Library/Logs/advisor-cache-gateway.log"))
DEBUG = bool(os.environ.get("ADVISOR_CACHE_DEBUG"))
LOG_MAX = 5 * 1024 * 1024
UPSTREAM_IDLE = float(os.environ.get("ADVISOR_CACHE_UPSTREAM_IDLE", "20"))
DRAIN_SECONDS = float(os.environ.get("ADVISOR_CACHE_DRAIN_SECONDS", "600"))
_inflight = [0]
_inflight_lock = threading.Lock()
_draining = threading.Event()
_idle = set()  # client connections between requests, closed at once when draining starts
_fresh = {}  # accepted connections whose first request has not arrived yet: connection -> accept time
FRESH_GRACE = 2.0  # a draining gateway waits this long for a fresh connection's first request, then serves it
HOP = {"host", "content-length", "connection", "accept-encoding", "transfer-encoding",
       "keep-alive", "proxy-connection", "proxy-authorization", "te", "trailer", "upgrade"}
SSL_CTX = ssl.create_default_context()
_log_lock = threading.Lock()
_local = threading.local()


def log(line):
    with _log_lock:
        try:
            if os.path.exists(LOG_PATH) and os.path.getsize(LOG_PATH) > LOG_MAX:
                os.replace(LOG_PATH, LOG_PATH + ".1")
            with open(LOG_PATH, "a") as f:
                f.write(time.strftime("%Y-%m-%d %H:%M:%S ") + line + "\n")
        except OSError:
            pass


def advisor_tools(j):
    return [t for t in j.get("tools") or [] if isinstance(t, dict) and str(t.get("type", "")).startswith("advisor_")]


def rewrite(body, ttl=None, max_tokens=None):
    """Return (body, note, meta) with the advisor tool given `caching` (and `max_tokens`).

    A field the caller already set is kept. A body that is not a JSON object, or
    carries no advisor tool, comes back byte for byte.
    """
    ttl = TTL if ttl is None else ttl
    max_tokens = MAX_TOKENS if max_tokens is None else max_tokens
    if not body:
        return body, "", ""
    try:
        j = json.loads(body)
    except ValueError:
        return body, "", ""
    if not isinstance(j, dict):
        return body, "", ""
    msgs = j.get("messages") or []
    note = " model=%s" % j.get("model")
    meta = " req(max_tokens=%s msgs=%d tools=%d stream=%s)" % (
        j.get("max_tokens"), len(msgs) if isinstance(msgs, list) else -1,
        len(j.get("tools") or []), j.get("stream"))
    tools = advisor_tools(j)
    if DEBUG and tools:
        log("    debug context_management=%s thinking=%s" % (
            json.dumps(j.get("context_management")), json.dumps(j.get("thinking"))))
    changed = []
    kept = []
    for t in tools:
        if "caching" in t:
            c = t["caching"]
            # The caller's own caching: logged with its TTL (the API's default is 5m) so stats prices it.
            kept.append("advisor-caching-kept=%s" % ((c.get("ttl") if isinstance(c, dict) else None) or "5m"))
        elif ttl != "off":
            t["caching"] = {"type": "ephemeral", "ttl": ttl}
            changed.append("advisor-caching=" + ttl)
        if max_tokens and "max_tokens" not in t:
            t["max_tokens"] = int(max_tokens)
            changed.append("advisor-max_tokens=" + max_tokens)
    if kept:
        note += " " + " ".join(kept)
    elif not tools and j.get("tools"):
        # Marks a request whose tools carry no advisor, so an advisor row logged for it is explained.
        note += " advisor-tool=none"
    if changed:
        return json.dumps(j, separators=(",", ":")).encode(), note + " " + " ".join(changed), meta
    return body, note, meta


SECRET_HEADERS = {"authorization", "x-api-key", "proxy-authorization", "cookie"}

CAPTURE_DIR = os.environ.get("ADVISOR_CACHE_CAPTURE_DIR", "~/.professor/tmp/gw-logs").strip()
CAPTURE_DIR = "" if CAPTURE_DIR.lower() in ("", "off", "0") else os.path.expanduser(CAPTURE_DIR)
CAPTURE_KEEP_DAYS = int(os.environ.get("ADVISOR_CACHE_CAPTURE_KEEP_DAYS", "7"))
_capture_lock = threading.Lock()
_capture_seq = [0]
_capture_pruned = [None]  # the day the capture folder was last pruned
DAY_FOLDER = re.compile(r"^\d{4}-\d{2}-\d{2}$")


def prune_captures(root, keep_days, today):
    """Delete the day folders ({YYYY-MM-DD}) in root dated before today minus keep_days; nothing else."""
    import datetime
    import shutil
    cutoff = today - datetime.timedelta(days=keep_days)
    try:
        names = os.listdir(root)
    except FileNotFoundError:
        return
    except OSError as e:
        log("    capture prune error %s: %r" % (root, e))
        return
    for name in sorted(names):
        path = os.path.join(root, name)
        if not DAY_FOLDER.match(name) or not os.path.isdir(path) or os.path.islink(path):
            continue
        try:
            day = datetime.date.fromisoformat(name)
        except ValueError:
            continue
        if day < cutoff:
            try:
                shutil.rmtree(path)
                log("capture pruned %s (older than %d days)" % (path, keep_days))
            except OSError as e:
                log("    capture prune error %s: %r" % (path, e))


def prune_captures_once_a_day():
    """On the first call of each day, prune the capture folder on a background thread."""
    import datetime
    if not CAPTURE_DIR:
        return
    today = datetime.date.today()
    with _capture_lock:
        if _capture_pruned[0] == today:
            return
        _capture_pruned[0] = today
    threading.Thread(target=prune_captures, args=(CAPTURE_DIR, CAPTURE_KEEP_DAYS, today),
                     name="capture-prune", daemon=True).start()


class Capture:
    """One folder per exchange, named so it sorts by arrival and says what it was:
    {date}/{HHMMSS.mmm}-{seq}-{kind}-{METHOD}-{path}/ holding, in exchange order,
    1-request.json (method, path, headers; secrets redacted), 2-request.body (as the client sent it),
    3-request.sent.body (only when the gateway rewrote it), 4-response.json (status, headers),
    5-response.body (the literal bytes streamed back, SSE included).
    Capture never breaks a request: every write failure is logged once and capture stops for it."""

    def __init__(self, method, path, headers, kind):
        self.dir = None
        self.body = None
        if not CAPTURE_DIR:
            return
        prune_captures_once_a_day()
        try:
            with _capture_lock:
                _capture_seq[0] += 1
                seq = _capture_seq[0]
            now = time.time()
            slug = re.sub(r"[^A-Za-z0-9]+", "_", path.split("?", 1)[0]).strip("_")[:60] or "root"
            name = "%s.%03d-%06d-%s-%s-%s" % (time.strftime("%H%M%S", time.localtime(now)),
                                              int(now * 1000) % 1000, seq, kind, method, slug)
            self.dir = os.path.join(CAPTURE_DIR, time.strftime("%Y-%m-%d", time.localtime(now)), name)
            os.makedirs(self.dir, exist_ok=True)
            hdrs = [[k, "<redacted>" if k.lower() in SECRET_HEADERS else v] for k, v in headers.items()]
            self.write("1-request.json", json.dumps({"time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(now)),
                       "seq": seq, "kind": kind, "method": method, "path": path, "headers": hdrs}, indent=2).encode())
            self.body = None
        except Exception as e:
            self.fail(e)

    def fail(self, e):
        log("    capture error %s: %r" % (self.dir, e))
        self.dir = None

    def write(self, name, data):
        if self.dir is None:
            return
        try:
            with open(os.path.join(self.dir, name), "wb") as f:
                f.write(data)
        except Exception as e:
            self.fail(e)

    def response(self, status, reason, headers):
        self.write("4-response.json", json.dumps({"status": status, "reason": reason,
                   "headers": [[k, v] for k, v in headers]}, indent=2).encode())
        if self.dir is not None:
            try:
                self.body = open(os.path.join(self.dir, "5-response.body"), "wb")
            except Exception as e:
                self.fail(e)

    def chunk(self, data):
        if self.body is not None:
            try:
                self.body.write(data)
            except Exception as e:
                self.close()
                self.fail(e)

    def close(self):
        if self.body is not None:
            try:
                self.body.close()
            except Exception:
                pass
            self.body = None


def debug_headers(headers):
    """The non-secret request headers the debug log records: x-*, anthropic-* and user-agent."""
    return {k: v for k, v in headers.items()
            if k.lower() not in SECRET_HEADERS
            and (k.lower().startswith(("x-", "anthropic-")) or k.lower() == "user-agent")}


def upstream():
    """This thread's upstream connection, reused only within UPSTREAM_IDLE seconds of its last request's start.

    A client keeps its connection to the gateway open for minutes between requests (a sub-agent waiting on
    its tools); the matching upstream connection may have been dropped on the way without a reset by then,
    and a request written into it hangs until the timeout (seen: three 900-second 502s on devbox).
    """
    conn = getattr(_local, "conn", None)
    if conn is not None and time.time() - getattr(_local, "used", 0) > UPSTREAM_IDLE:
        drop_upstream()
        conn = None
    _local.used = time.time()
    if conn is None:
        port = UPSTREAM.port
        if UPSTREAM.scheme == "https":
            conn = http.client.HTTPSConnection(UPSTREAM.hostname, port or 443, timeout=900, context=SSL_CTX)
        else:
            conn = http.client.HTTPConnection(UPSTREAM.hostname, port or 80, timeout=900)
        _local.conn = conn
    return conn


def drop_upstream():
    conn = getattr(_local, "conn", None)
    if conn is not None:
        try:
            conn.close()
        except Exception:
            pass
    _local.conn = None


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def setup(self):
        super().setup()
        with _inflight_lock:
            _fresh[self.connection] = time.time()

    def finish(self):
        # One thread serves one client connection; its upstream connection ends with it.
        with _inflight_lock:
            _idle.discard(self.connection)
            _fresh.pop(self.connection, None)
        try:
            super().finish()
        finally:
            drop_upstream()
    server_version = "advisor-cache-gateway"

    def log_message(self, *args):
        pass

    def read_body(self):
        if "chunked" in (self.headers.get("transfer-encoding") or "").lower():
            parts = []
            while True:
                size = int(self.rfile.readline().split(b";")[0].strip() or b"0", 16)
                if size == 0:
                    while self.rfile.readline() not in (b"\r\n", b"\n", b""):
                        pass  # trailers
                    break
                parts.append(self.rfile.read(size))
                self.rfile.readline()
            return b"".join(parts)
        n = int(self.headers.get("content-length") or 0)
        return self.rfile.read(n) if n else b""

    def fail(self, status, message):
        data = json.dumps({"type": "error", "error": {"type": "api_error",
                           "message": "advisor-cache-gateway: " + message}}).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def handle_any(self):
        with _inflight_lock:
            _fresh.pop(self.connection, None)
        if self.path == "/__gateway/health":
            data = ("ok pid=%d ttl=%s subagent=%s max_tokens=%s\n" % (
                os.getpid(), TTL, TTL_SUBAGENT, MAX_TOKENS or "-")).encode()
            self.send_response(200)
            self.send_header("content-type", "text/plain")
            self.send_header("content-length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            with _inflight_lock:
                _idle.add(self.connection)
            if _draining.is_set():
                self.close_connection = True
            return
        with _inflight_lock:
            _inflight[0] += 1
            _idle.discard(self.connection)
        try:
            self.proxy()
        finally:
            with _inflight_lock:
                _inflight[0] -= 1
                _idle.add(self.connection)
            if _draining.is_set():
                self.close_connection = True  # a draining gateway serves no further request on this connection

    def proxy(self):
        body = self.read_body()
        note = meta = ""
        kind, ttl = ttl_for(self.headers)
        cap = Capture(self.command, self.path, self.headers, kind)
        cap.write("2-request.body", body)
        try:
            self.exchange(body, note, meta, kind, ttl, cap)
        finally:
            cap.close()

    def exchange(self, body, note, meta, kind, ttl, cap):
        sent = body
        is_messages = self.command == "POST" and self.path.split("?", 1)[0] == "/v1/messages"
        if is_messages:
            if DEBUG:
                hdr = debug_headers(self.headers)
                try:
                    md = json.loads(body).get("metadata")
                except Exception:
                    md = None
                log("    debug headers=%s metadata=%s" % (json.dumps(hdr), json.dumps(md)[:300]))
            body, note, meta = rewrite(body, ttl=ttl)
            note += " conv=" + kind
            if body is not sent:
                cap.write("3-request.sent.body", body)
        headers = {k: v for k, v in self.headers.items() if k.lower() not in HOP}
        headers["accept-encoding"] = "identity"
        if body or self.command in ("POST", "PUT", "PATCH"):
            headers["content-length"] = str(len(body))

        resp = None
        for attempt in (1, 2):
            try:
                conn = upstream()
                conn.request(self.command, self.path, body=body or None, headers=headers)
                resp = conn.getresponse()
                break
            except (http.client.RemoteDisconnected, ConnectionResetError, BrokenPipeError,
                    http.client.CannotSendRequest, http.client.ResponseNotReady) as e:
                drop_upstream()  # a pooled connection the server closed: one fresh try
                if attempt == 2:
                    log("%s %s -> 502 upstream: %r" % (self.command, self.path, e))
                    cap.write("4-response.json", json.dumps({"status": 502, "gateway_error": repr(e)}).encode())
                    return self.fail(502, "upstream connection failed: %r" % e)
            except (OSError, socket.timeout) as e:
                drop_upstream()
                log("%s %s -> 502 upstream: %r" % (self.command, self.path, e))
                cap.write("4-response.json", json.dumps({"status": 502, "gateway_error": repr(e)}).encode())
                return self.fail(502, "upstream unreachable: %r" % e)

        cap.response(resp.status, resp.reason, resp.getheaders())
        no_body = self.command == "HEAD" or resp.status in (204, 304) or 100 <= resp.status < 200
        self.send_response(resp.status)
        for k, v in resp.getheaders():
            if k.lower() not in HOP:
                self.send_header(k, v)
        if no_body:
            self.send_header("content-length", "0")
            self.end_headers()
            resp.read()
            self.after(resp, note, meta, is_messages, b"", [])
            return
        self.send_header("transfer-encoding", "chunked")
        self.end_headers()
        tail = b""
        usage_lines = []
        error_head = b""
        try:
            while True:
                chunk = resp.read1(65536)
                if not chunk:
                    break
                cap.chunk(chunk)
                if resp.status >= 400 and len(error_head) < 600:
                    error_head += chunk[:600]
                if is_messages:
                    tail += chunk
                    *lines, tail = tail.split(b"\n")
                    for ln in lines:
                        if b"advisor_message" in ln and ln.startswith(b"data:"):
                            usage_lines.append(ln[5:].strip())
                    if len(tail) > 1 << 20:
                        tail = b""
                self.wfile.write(b"%x\r\n%s\r\n" % (len(chunk), chunk))
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            drop_upstream()  # the client left mid-stream; this upstream connection is spent
            log("%s %s client disconnected mid-stream%s" % (self.command, self.path, note))
            self.close_connection = True
            return
        except Exception as e:
            drop_upstream()
            log("%s %s stream error %r%s" % (self.command, self.path, e, note))
            self.close_connection = True
            return
        if is_messages and tail.startswith(b"{") and b"advisor_message" in tail:
            usage_lines.append(tail)  # a non-streaming JSON response
        self.after(resp, note, meta, is_messages, error_head, usage_lines)

    def after(self, resp, note, meta, is_messages, error_head, usage_lines):
        if not is_messages and resp.status < 400:
            return
        line = "%s %s -> %d%s" % (self.command, self.path, resp.status, note)
        if resp.status >= 400:
            line += meta
            rl = " ".join("%s=%s" % (k.lower().replace("anthropic-ratelimit-", ""), v) for k, v in resp.getheaders()
                          if k.lower().startswith("anthropic-ratelimit-") or k.lower() == "retry-after")
            if rl:
                line += " ratelimit(" + rl + ")"
            line += " body=" + error_head[:300].decode("utf-8", "replace").replace("\n", " ")
        log(line)
        for raw in usage_lines[-1:]:  # the last usage event carries every iteration
            try:
                usage = json.loads(raw).get("usage") or {}
            except ValueError:
                continue
            for it in [i for i in usage.get("iterations") or [] if i.get("type") == "advisor_message"]:
                ttl = re.search(r"advisor-caching(?:-kept)?=(\w+)", note)
                kind = re.search(r"conv=(\w+)", note)
                log("    advisor %s conv=%s ttl=%s in=%s cache_read=%s cache_write=%s out=%s" % (
                    it.get("model"), kind.group(1) if kind else "-", ttl.group(1) if ttl else "-",
                    it.get("input_tokens"), it.get("cache_read_input_tokens"),
                    it.get("cache_creation_input_tokens"), it.get("output_tokens")))

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = do_OPTIONS = handle_any


class Server(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def server_bind(self):
        # A restarted gateway binds the port while its predecessor still drains; SO_REUSEPORT lets both listen.
        # macOS hands new connections to the newest listener. Linux spreads them over all and resets those
        # queued at a listener that closes, so there run.sh passes a shared listener instead (LISTEN_FD).
        self.socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEPORT, 1)
        super().server_bind()

    def handle_error(self, request, client_address):
        # Claude Code drops idle keep-alive connections; that is not an error worth a traceback.
        err = sys.exc_info()[1]
        if isinstance(err, (ConnectionResetError, BrokenPipeError, socket.timeout)):
            return
        log("    client error %s: %r" % (client_address[1], err))


def make_server():
    fd = os.environ.get("ADVISOR_GATEWAY_LISTEN_FD")
    if not fd:
        return Server(("127.0.0.1", PORT), Handler)
    sock = socket.socket(fileno=int(fd))
    server = Server(sock.getsockname()[:2], Handler, bind_and_activate=False)
    server.socket.close()
    server.socket = sock
    # Generations share one queue: a gateway woken for a connection another one took must not block in
    # accept(), or its stop would wait for the next connection.
    sock.setblocking(False)
    return server


ADVISOR_ROW = re.compile(r"^(\S+ \S+)     advisor (\S+) (?:conv=(\S+) )?(?:ttl=(\S+) )?in=(\d+) cache_read=(\d+) cache_write=(\d+) out=(\d+)")
WRITE_RATE = {"1h": 2.0, "5m": 1.25}
# Cache read price as a share of the model's base input price (platform.claude.com pricing, 2026-10-08).
READ_RATE = (("claude-fable-5-1", 0.025), ("claude-mythos-5-1", 0.025),
             ("claude-opus-5-5", 0.05), ("claude-sonnet-5-5", 0.05))


def read_rate(model):
    for prefix, rate in READ_RATE:
        if model.startswith(prefix):
            return rate
    return 0.1


def stats(paths):
    """Sum advisor rows, overall and per conversation kind (main, sub, - for rows logged before kinds).

    Input is priced in units of the advisor model's base input price, with and without the cache: a
    write at its TTL's rate (1h 2x, 5m 1.25x; a row without a TTL counts as 1h), a read at the model's
    rate (read_rate). A hit is a call that read anything from the cache.
    """
    groups = {}
    first = last = None
    for path in paths:
        try:
            with open(path) as f:
                lines = f.read().splitlines()
        except OSError:
            continue
        for ln in lines:
            m = ADVISOR_ROW.match(ln)
            if not m:
                continue
            first = first or m.group(1)
            last = m.group(1)
            fresh, read, write, out = (int(m.group(i)) for i in (5, 6, 7, 8))
            model = m.group(2)
            for key in ("all", m.group(3) or "-"):
                g = groups.setdefault(key, {"calls": 0, "hits": 0, "fresh": 0, "cache_read": 0, "cache_write": 0,
                                            "output": 0, "write_cost": 0.0, "read_cost": 0.0})
                g["calls"] += 1
                g["hits"] += read > 0
                g["fresh"] += fresh
                g["cache_read"] += read
                g["cache_write"] += write
                g["output"] += out
                g["write_cost"] += WRITE_RATE.get(m.group(4) or "1h", 2.0) * write
                g["read_cost"] += read_rate(model) * read
    for g in groups.values():
        uncached = g["fresh"] + g["cache_read"] + g["cache_write"]
        cached = g["fresh"] + g.pop("read_cost") + g.pop("write_cost")
        g["input_units_uncached"] = uncached
        g["input_units_cached"] = round(cached)
        g["saved_pct"] = round(100 * (1 - cached / uncached), 1) if uncached else 0.0
    result = dict(groups.get("all") or {"calls": 0})
    result.update(first=first, last=last, by_kind={k: v for k, v in groups.items() if k != "all"})
    return result


def main(argv):
    if argv[1:2] == ["stats"]:
        s = stats([LOG_PATH + ".1", LOG_PATH])
        if not s["calls"]:
            print("no advisor calls logged yet")
            return 0
        print("advisor calls: %d, %d read from the cache (%s .. %s)" % (s["calls"], s["hits"], s["first"], s["last"]))
        print("input cost in base-input-token units (write 2x at 1h, 1.25x at 5m; read 0.05x on Opus/Sonnet 5.5): %d with cache vs %d without, %.1f%% saved" % (
            s["input_units_cached"], s["input_units_uncached"], s["saved_pct"]))
        for kind, g in sorted(s["by_kind"].items()):
            print("  %-4s %4d calls, %4d hits: %d vs %d, %.1f%% saved" % (
                kind, g["calls"], g["hits"], g["input_units_cached"], g["input_units_uncached"], g["saved_pct"]))
        return 0
    if argv[1:]:
        print(__doc__)
        return 2
    log("start port=%d listen_fd=%s ttl=%s subagent=%s max_tokens=%s upstream=%s python=%s" % (
        PORT, os.environ.get("ADVISOR_GATEWAY_LISTEN_FD") or "-", TTL, TTL_SUBAGENT, MAX_TOKENS or "-", UPSTREAM.geturl(),
        sys.version.split()[0]))
    server = make_server()
    prune_captures_once_a_day()

    def on_term(signum, frame):
        if _draining.is_set():
            return
        _draining.set()
        # shutdown() waits for serve_forever, which runs on this thread: stop it from another one.
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, on_term)
    signal.signal(signal.SIGINT, on_term)
    server.serve_forever()
    server.server_close()  # no new connections from here on; the successor takes them
    drain(DRAIN_SECONDS)
    return 0


def drain(limit):
    """Wait until every in-flight request has finished, or `limit` seconds pass; log the outcome.

    An idle keep-alive connection is closed at once: its client reconnects to the successor. A connection
    accepted just before the stop is not idle, its first request may be on the way: it gets FRESH_GRACE
    seconds to arrive and is then served like any other; one still silent after that is closed.
    """
    deadline = time.time() + limit

    def close(conns):
        for conn in conns:
            try:
                conn.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    def pending():
        # In-flight requests, plus fresh connections still inside their grace; closes the ones past it.
        now = time.time()
        with _inflight_lock:
            stale = [c for c, t in _fresh.items() if now - t >= FRESH_GRACE]
            for c in stale:
                del _fresh[c]
            waiting = len(_fresh)
            busy = _inflight[0]
        close(stale)
        return busy, waiting

    with _inflight_lock:
        idle = list(_idle)
    close(idle)
    busy, waiting = pending()
    log("stop: draining %d in-flight request(s) and %d new connection(s), up to %ds" % (busy, waiting, limit))
    while (busy or waiting) and time.time() < deadline:
        time.sleep(0.05)
        busy, waiting = pending()
    if busy:
        log("stop: %d request(s) still in flight after %ds; exiting anyway" % (busy, limit))
    else:
        log("stop: drained, exiting")
    return busy


if __name__ == "__main__":
    sys.exit(main(sys.argv))
