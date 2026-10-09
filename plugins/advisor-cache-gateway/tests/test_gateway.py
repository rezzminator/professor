"""Tests for gateway.py: the rewrite rules, and the server end to end against a fake upstream."""
import http.client
import importlib.util
import json
import os
import signal
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
GATEWAY = os.path.join(os.path.dirname(HERE), "gateway.py")


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def load_gateway(env):
    # never capture into the real home
    env = dict({"ADVISOR_CACHE_CAPTURE_DIR": "off"}, **env)
    old = {k: os.environ.get(k) for k in env}
    os.environ.update(env)
    try:
        spec = importlib.util.spec_from_file_location("gateway_under_test_%d" % id(env), GATEWAY)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        for k, v in old.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v


ADVISOR = {"type": "advisor_20260301", "name": "advisor", "model": "claude-opus-5-5"}


class RewriteTest(unittest.TestCase):
    def setUp(self):
        self.g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull})

    def body(self, **extra):
        return json.dumps({"model": "m", "messages": [], "tools": [dict(ADVISOR, **extra), {"name": "Bash"}]}).encode()

    def test_adds_caching_to_the_advisor_tool_only(self):
        out, note, _ = self.g.rewrite(self.body(), ttl="1h", max_tokens="")
        tools = json.loads(out)["tools"]
        self.assertEqual(tools[0]["caching"], {"type": "ephemeral", "ttl": "1h"})
        self.assertNotIn("caching", tools[1])
        self.assertIn("advisor-caching=1h", note)

    def test_keeps_caching_the_caller_set(self):
        out, note, _ = self.g.rewrite(self.body(caching={"type": "ephemeral", "ttl": "5m"}), ttl="1h", max_tokens="")
        self.assertEqual(json.loads(out)["tools"][0]["caching"]["ttl"], "5m")
        self.assertIn("advisor-caching-kept=5m", note)
        self.assertNotIn("advisor-caching=", note)

    def test_marks_a_request_whose_tools_carry_no_advisor(self):
        body = json.dumps({"model": "m", "messages": [], "tools": [{"name": "Read", "input_schema": {}}]}).encode()
        out, note, _ = self.g.rewrite(body, ttl="5m", max_tokens="")
        self.assertIs(out, body)
        self.assertIn("advisor-tool=none", note)

    def test_off_leaves_the_body_byte_for_byte(self):
        body = self.body()
        out, _, _ = self.g.rewrite(body, ttl="off", max_tokens="")
        self.assertIs(out, body)

    def test_adds_max_tokens_when_configured(self):
        out, note, _ = self.g.rewrite(self.body(), ttl="off", max_tokens="2048")
        self.assertEqual(json.loads(out)["tools"][0]["max_tokens"], 2048)
        self.assertIn("advisor-max_tokens=2048", note)

    def test_keeps_max_tokens_the_caller_set(self):
        out, _, _ = self.g.rewrite(self.body(max_tokens=4096), ttl="off", max_tokens="2048")
        self.assertEqual(json.loads(out)["tools"][0]["max_tokens"], 4096)

    def test_without_an_advisor_tool_the_body_is_untouched(self):
        body = json.dumps({"model": "m", "messages": [], "tools": [{"name": "Bash"}]}).encode()
        self.assertIs(self.g.rewrite(body, ttl="1h", max_tokens="2048")[0], body)

    def test_non_json_and_non_object_bodies_pass_through(self):
        for body in (b"not json", b"[1,2]", b""):
            self.assertIs(self.g.rewrite(body, ttl="1h", max_tokens="")[0], body)


class Upstream(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    seen = []

    def log_message(self, *a):
        pass

    def do_POST(self):
        n = int(self.headers.get("content-length") or 0)
        body = self.rfile.read(n)
        Upstream.seen.append({"path": self.path, "headers": dict(self.headers), "body": body})
        if self.path.startswith("/v1/messages"):
            events = [
                b'event: message_start\ndata: {"type":"message_start"}\n\n',
                b'event: message_delta\ndata: {"type":"message_delta","usage":{"iterations":['
                b'{"type":"message","input_tokens":5},'
                b'{"type":"advisor_message","model":"claude-opus-5-5","input_tokens":154,'
                b'"cache_read_input_tokens":9000,"cache_creation_input_tokens":100,"output_tokens":300}]}}\n\n',
                b'event: message_stop\ndata: {"type":"message_stop"}\n\n',
            ]
            self.send_response(200)
            self.send_header("content-type", "text/event-stream")
            self.send_header("request-id", "req_test")
            self.send_header("transfer-encoding", "chunked")
            self.end_headers()
            for e in events:
                self.wfile.write(b"%x\r\n%s\r\n" % (len(e), e))
                self.wfile.flush()
                time.sleep(0.05)
            self.wfile.write(b"0\r\n\r\n")
            return
        data = b'{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}'
        self.send_response(429)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST


class ServerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.up_port = free_port()
        cls.upstream = ThreadingHTTPServer(("127.0.0.1", cls.up_port), Upstream)
        threading.Thread(target=cls.upstream.serve_forever, daemon=True).start()
        cls.tmp = tempfile.mkdtemp()
        cls.log = os.path.join(cls.tmp, "gw.log")
        cls.port = free_port()
        cls.g = load_gateway({
            "ADVISOR_CACHE_GATEWAY_PORT": str(cls.port), "ADVISOR_CACHE_TTL": "1h",
            "ADVISOR_CACHE_UPSTREAM": "http://127.0.0.1:%d" % cls.up_port,
            "ADVISOR_CACHE_GATEWAY_LOG": cls.log,
        })
        cls.server = cls.g.Server(("127.0.0.1", cls.port), cls.g.Handler)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.upstream.shutdown()
        cls.upstream.server_close()

    def setUp(self):
        Upstream.seen.clear()

    def request(self, method, path, body=None, headers=None, chunked=False):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        if chunked:
            conn.putrequest(method, path)
            for k, v in (headers or {}).items():
                conn.putheader(k, v)
            conn.putheader("transfer-encoding", "chunked")
            conn.endheaders()
            half = len(body) // 2
            for part in (body[:half], body[half:]):
                conn.send(b"%x\r\n%s\r\n" % (len(part), part))
            conn.send(b"0\r\n\r\n")
        else:
            conn.request(method, path, body=body, headers=headers or {})
        resp = conn.getresponse()
        data = resp.read()
        conn.close()
        return resp, data

    def messages_body(self):
        return json.dumps({"model": "claude-haiku-5-5", "stream": True, "messages": [{"role": "user", "content": "hi"}],
                           "tools": [ADVISOR]}).encode()

    def test_streams_the_response_and_rewrites_the_advisor(self):
        resp, data = self.request("POST", "/v1/messages?beta=true", self.messages_body(),
                                  {"authorization": "Bearer secret-token", "anthropic-beta": "advisor-tool-2026-03-01"})
        self.assertEqual(resp.status, 200)
        self.assertIn(b"message_stop", data)
        self.assertEqual(resp.getheader("request-id"), "req_test")
        sent = Upstream.seen[-1]
        self.assertEqual(sent["path"], "/v1/messages?beta=true")
        self.assertEqual(sent["headers"].get("authorization") or sent["headers"].get("Authorization"), "Bearer secret-token")
        self.assertEqual(json.loads(sent["body"])["tools"][0]["caching"], {"type": "ephemeral", "ttl": "1h"})
        time.sleep(0.1)
        with open(self.log) as f:
            logged = f.read()
        self.assertIn("cache_read=9000 cache_write=100", logged)
        self.assertNotIn("secret-token", logged)

    def test_chunked_request_bodies_are_read_whole(self):
        resp, _ = self.request("POST", "/v1/messages", self.messages_body(), {"content-type": "application/json"}, chunked=True)
        self.assertEqual(resp.status, 200)
        self.assertIn("caching", json.loads(Upstream.seen[-1]["body"])["tools"][0])

    def test_upstream_errors_pass_through_with_their_status(self):
        resp, data = self.request("GET", "/v1/models")
        self.assertEqual(resp.status, 429)
        self.assertIn(b"rate_limit_error", data)

    def test_health(self):
        resp, data = self.request("GET", "/__gateway/health")
        self.assertEqual(resp.status, 200)
        self.assertEqual(data, b"ok pid=%d ttl=1h subagent=5m max_tokens=-\n" % os.getpid())

    def test_stats_from_the_log(self):
        self.request("POST", "/v1/messages", self.messages_body())
        time.sleep(0.1)
        s = self.g.stats([self.log])
        self.assertGreaterEqual(s["calls"], 1)
        self.assertGreater(s["saved_pct"], 0)

    def test_sub_agent_requests_get_the_sub_agent_ttl(self):
        self.request("POST", "/v1/messages", self.messages_body(), {"x-claude-code-agent-id": "a1"})
        self.assertEqual(json.loads(Upstream.seen[-1]["body"])["tools"][0]["caching"]["ttl"], "5m")


class CapturePruneTest(unittest.TestCase):
    """Day folders older than ADVISOR_CACHE_CAPTURE_KEEP_DAYS are deleted; nothing else in the folder is."""

    def make(self, root, names):
        for n in names:
            os.makedirs(os.path.join(root, n, "120000.000-000001-main-POST-v1_messages"))
            with open(os.path.join(root, n, "120000.000-000001-main-POST-v1_messages", "2-request.body"), "w") as f:
                f.write("{}")

    def test_deletes_only_day_folders_past_the_limit(self):
        import datetime
        root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, root)
        self.make(root, ["2026-09-30", "2026-10-01", "2026-10-08", "2026-13-99", "notes"])
        with open(os.path.join(root, "2026-09-01"), "w") as f:
            f.write("a file, not a day folder")
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull})
        g.prune_captures(root, 7, datetime.date(2026, 10, 8))
        self.assertEqual(sorted(os.listdir(root)), ["2026-09-01", "2026-10-01", "2026-10-08", "2026-13-99", "notes"])

    def test_the_first_capture_of_a_day_prunes_in_the_background(self):
        import datetime
        up_port, port, tmp = free_port(), free_port(), tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp)
        cap = os.path.join(tmp, "cap")
        old = (datetime.date.today() - datetime.timedelta(days=30)).isoformat()
        self.make(cap, [old])
        upstream = ThreadingHTTPServer(("127.0.0.1", up_port), Upstream)
        threading.Thread(target=upstream.serve_forever, daemon=True).start()
        g = load_gateway({"ADVISOR_CACHE_UPSTREAM": "http://127.0.0.1:%d" % up_port, "ADVISOR_CACHE_GATEWAY_PORT": str(port),
                          "ADVISOR_CACHE_GATEWAY_LOG": os.devnull, "ADVISOR_CACHE_CAPTURE_DIR": cap})
        server = g.Server(("127.0.0.1", port), g.Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
            conn.request("POST", "/v1/messages", body=b'{"model":"m","messages":[]}')
            conn.getresponse().read()
            conn.close()
            end = time.time() + 10
            while os.path.exists(os.path.join(cap, old)) and time.time() < end:
                time.sleep(0.05)
        finally:
            server.shutdown()
            server.server_close()
            upstream.shutdown()
            upstream.server_close()
        self.assertFalse(os.path.exists(os.path.join(cap, old)), "a 30-day-old day folder survived")
        self.assertIn(datetime.date.today().isoformat(), os.listdir(cap))


class CaptureTest(unittest.TestCase):
    def test_each_exchange_lands_in_one_folder_with_secrets_redacted(self):
        up_port, port, tmp = free_port(), free_port(), tempfile.mkdtemp()
        cap = os.path.join(tmp, "cap")
        upstream = ThreadingHTTPServer(("127.0.0.1", up_port), Upstream)
        threading.Thread(target=upstream.serve_forever, daemon=True).start()
        g = load_gateway({"ADVISOR_CACHE_UPSTREAM": "http://127.0.0.1:%d" % up_port, "ADVISOR_CACHE_TTL": "5m",
                          "ADVISOR_CACHE_GATEWAY_LOG": os.devnull, "ADVISOR_CACHE_CAPTURE_DIR": cap})
        server = g.Server(("127.0.0.1", port), g.Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            body = json.dumps({"model": "m", "stream": True, "messages": [], "tools": [ADVISOR]}).encode()
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
            conn.request("POST", "/v1/messages?beta=true", body=body, headers={"authorization": "Bearer secret-token"})
            streamed = conn.getresponse().read()
            conn.request("GET", "/v1/models")
            conn.getresponse().read()
            conn.close()
            time.sleep(0.1)
            (day,) = os.listdir(cap)
            first, second = sorted(os.listdir(os.path.join(cap, day)))
            self.assertRegex(first, r"^\d{6}\.\d{3}-000001-main-POST-v1_messages$")
            self.assertRegex(second, r"^\d{6}\.\d{3}-000002-main-GET-v1_models$")
            d = os.path.join(cap, day, first)
            self.assertEqual(sorted(os.listdir(d)), ["1-request.json", "2-request.body", "3-request.sent.body",
                                                     "4-response.json", "5-response.body"])
            with open(os.path.join(d, "1-request.json")) as f:
                req = json.load(f)
            self.assertIn(["authorization", "<redacted>"], req["headers"])
            with open(os.path.join(d, "2-request.body"), "rb") as f:
                self.assertEqual(f.read(), body)
            with open(os.path.join(d, "3-request.sent.body"), "rb") as f:
                self.assertEqual(json.loads(f.read())["tools"][0]["caching"]["ttl"], "5m")
            with open(os.path.join(d, "4-response.json")) as f:
                self.assertEqual(json.load(f)["status"], 200)
            with open(os.path.join(d, "5-response.body"), "rb") as f:
                self.assertEqual(f.read(), streamed)
            for root, _, files in os.walk(cap):
                for name in files:
                    with open(os.path.join(root, name), "rb") as f:
                        self.assertNotIn(b"secret-token", f.read())
            self.assertNotIn("3-request.sent.body", os.listdir(os.path.join(cap, day, second)))
        finally:
            server.shutdown()
            server.server_close()
            upstream.shutdown()
            upstream.server_close()


class StatsTest(unittest.TestCase):
    def test_prices_writes_by_their_ttl_and_reads_by_the_model(self):
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull})
        path = os.path.join(tempfile.mkdtemp(), "gw.log")
        with open(path, "w") as f:
            f.write("2026-10-08 03:00:00     advisor claude-opus-5-5 conv=sub ttl=5m in=0 cache_read=0 cache_write=1000 out=1\n")
            f.write("2026-10-08 03:00:01     advisor claude-opus-5-5 conv=main ttl=1h in=0 cache_read=0 cache_write=1000 out=1\n")
            f.write("2026-10-08 03:00:02     advisor claude-opus-5-5 in=0 cache_read=1000 cache_write=0 out=1\n")
        s = g.stats([path])
        self.assertEqual(s["calls"], 3)
        self.assertEqual(s["input_units_uncached"], 3000)
        self.assertEqual(s["input_units_cached"], 1250 + 2000 + 50)
        self.assertEqual(s["hits"], 1)
        self.assertEqual(s["by_kind"]["sub"]["input_units_cached"], 1250)
        self.assertEqual(s["by_kind"]["main"]["calls"], 1)

    def test_read_rate_per_model(self):
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull})
        self.assertEqual(g.read_rate("claude-opus-5-5"), 0.05)
        self.assertEqual(g.read_rate("claude-sonnet-5-5"), 0.05)
        self.assertEqual(g.read_rate("claude-fable-5-1"), 0.025)
        self.assertEqual(g.read_rate("claude-haiku-5-5"), 0.1)


class DebugHeadersTest(unittest.TestCase):
    def test_secrets_never_reach_the_debug_log(self):
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull})
        kept = g.debug_headers({"Authorization": "Bearer x", "x-api-key": "k", "Cookie": "c",
                                "x-claude-code-agent-id": "a1", "anthropic-beta": "b", "user-agent": "u"})
        self.assertEqual(set(k.lower() for k in kept), {"x-claude-code-agent-id", "anthropic-beta", "user-agent"})


class PolicyTest(unittest.TestCase):
    def test_kind_from_the_agent_header(self):
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull, "ADVISOR_CACHE_TTL": "bogus",
                          "ADVISOR_CACHE_TTL_SUBAGENT": "off"})
        self.assertEqual(g.ttl_for({"x-claude-code-agent-id": "a"}), ("sub", "off"))
        self.assertEqual(g.ttl_for({}), ("main", "5m"))


class UpstreamDownTest(unittest.TestCase):
    def test_answers_502_when_upstream_is_unreachable(self):
        port = free_port()
        g = load_gateway({"ADVISOR_CACHE_UPSTREAM": "http://127.0.0.1:%d" % free_port(),
                          "ADVISOR_CACHE_GATEWAY_LOG": os.devnull})
        server = g.Server(("127.0.0.1", port), g.Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
            conn.request("POST", "/v1/messages", body=b"{}")
            resp = conn.getresponse()
            body = json.loads(resp.read())
            self.assertEqual(resp.status, 502)
            self.assertEqual(body["type"], "error")
            conn.close()
        finally:
            server.shutdown()
            server.server_close()


class ClientResetTest(unittest.TestCase):
    def test_a_client_reset_writes_no_traceback(self):
        import contextlib
        import io
        import struct
        port = free_port()
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_LOG": os.devnull})
        server = g.Server(("127.0.0.1", port), g.Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        err = io.StringIO()
        try:
            with contextlib.redirect_stderr(err):
                s = socket.create_connection(("127.0.0.1", port))
                s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
                time.sleep(0.2)
                s.close()
                time.sleep(0.3)
        finally:
            server.shutdown()
            server.server_close()
        self.assertEqual(err.getvalue(), "")


class SlowUpstream(BaseHTTPRequestHandler):
    """Streams a messages response over about 1.5 seconds (DELAY between its 7 events)."""
    protocol_version = "HTTP/1.1"
    DELAY = 0.25

    def log_message(self, *a):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get("content-length") or 0))
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("transfer-encoding", "chunked")
        self.end_headers()
        try:
            for i in range(6):
                e = b"event: ping\ndata: {\"i\":%d}\n\n" % i
                self.wfile.write(b"%x\r\n%s\r\n" % (len(e), e))
                self.wfile.flush()
                time.sleep(self.DELAY)
            e = b'event: message_stop\ndata: {"type":"message_stop"}\n\n'
            self.wfile.write(b"%x\r\n%s\r\n0\r\n\r\n" % (len(e), e))
        except (BrokenPipeError, ConnectionResetError):
            pass  # the drain-limit test cuts its client on purpose


class DrainTest(unittest.TestCase):
    """Real gateway processes: SIGTERM must let in-flight streams finish while a successor takes the port."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.port = free_port()
        self.upstream = ThreadingHTTPServer(("127.0.0.1", free_port()), SlowUpstream)
        threading.Thread(target=self.upstream.serve_forever, daemon=True).start()
        self.procs = []

    def tearDown(self):
        for p in self.procs:
            if p.poll() is None:
                p.kill()
            p.wait()
        self.upstream.shutdown()
        self.upstream.server_close()

    def gateway(self, drain="10"):
        env = dict(os.environ, ADVISOR_CACHE_GATEWAY_PORT=str(self.port), ADVISOR_CACHE_CAPTURE_DIR="off",
                   ADVISOR_CACHE_UPSTREAM="http://127.0.0.1:%d" % self.upstream.server_address[1],
                   ADVISOR_CACHE_GATEWAY_LOG=os.path.join(self.tmp, "gw.log"), ADVISOR_CACHE_DRAIN_SECONDS=drain)
        p = subprocess.Popen([sys.executable, "-I", GATEWAY], env=env)
        self.procs.append(p)
        return p

    def health_pid(self):
        try:
            conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=2)
            conn.request("GET", "/__gateway/health")
            data = conn.getresponse().read()
            conn.close()
            return int(data.split(b"pid=")[1].split()[0])
        except (OSError, IndexError, ValueError):
            return None

    def wait_for_pid(self, pid):
        end = time.time() + 10
        while time.time() < end:
            if self.health_pid() == pid:
                return True
            time.sleep(0.05)
        return False

    def stream(self, out):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=20)
        try:
            conn.request("POST", "/v1/messages", body=b'{"model":"m","messages":[]}')
            out.append(conn.getresponse().read())
        finally:
            conn.close()  # the drain-limit test cuts this stream on purpose

    def test_a_stream_survives_a_restart_and_the_successor_takes_new_requests(self):
        old = self.gateway()
        self.assertTrue(self.wait_for_pid(old.pid))
        got = []
        t = threading.Thread(target=self.stream, args=(got,))
        t.start()
        time.sleep(0.4)  # the stream is now mid-flight on the old gateway
        new = self.gateway()
        time.sleep(0.5)  # the successor binds beside it
        old.send_signal(signal.SIGTERM)
        self.assertTrue(self.wait_for_pid(new.pid), "the successor never answered")
        t.join(20)
        self.assertEqual(len(got), 1)
        self.assertIn(b"message_stop", got[0])
        self.assertEqual(old.wait(timeout=10), 0)
        after = []
        self.stream(after)
        self.assertIn(b"message_stop", after[0])
        with open(os.path.join(self.tmp, "gw.log")) as f:
            self.assertIn("stop: drained, exiting", f.read())

    def test_drain_gives_up_at_its_limit(self):
        old = self.gateway(drain="0.3")
        self.assertTrue(self.wait_for_pid(old.pid))
        t = threading.Thread(target=lambda: self.assertRaises(Exception, self.stream, []))
        t.start()
        time.sleep(0.3)
        start = time.time()
        old.send_signal(signal.SIGTERM)
        self.assertEqual(old.wait(timeout=10), 0)
        self.assertLess(time.time() - start, 1.2)
        t.join(20)
        with open(os.path.join(self.tmp, "gw.log")) as f:
            self.assertIn("still in flight", f.read())


CANARY = "PROMPT-CANARY-5d1e9b"


class PortRecorder(BaseHTTPRequestHandler):
    """Answers on a kept-alive connection and records which client port each request came from."""
    protocol_version = "HTTP/1.1"
    ports = []

    def log_message(self, *a):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get("content-length") or 0))
        PortRecorder.ports.append(self.client_address[1])
        data = b'{"type":"message","content":[],"usage":{"input_tokens":1,"output_tokens":1}}'
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


class UpstreamIdleTest(unittest.TestCase):
    """An upstream connection idle past ADVISOR_CACHE_UPSTREAM_IDLE is replaced, never written into."""

    def test_an_idle_upstream_connection_is_replaced_and_a_busy_one_reused(self):
        upstream = ThreadingHTTPServer(("127.0.0.1", free_port()), PortRecorder)
        threading.Thread(target=upstream.serve_forever, daemon=True).start()
        port = free_port()
        g = load_gateway({"ADVISOR_CACHE_GATEWAY_PORT": str(port), "ADVISOR_CACHE_GATEWAY_LOG": os.devnull,
                          "ADVISOR_CACHE_UPSTREAM": "http://127.0.0.1:%d" % upstream.server_address[1],
                          "ADVISOR_CACHE_UPSTREAM_IDLE": "0.5"})
        server = g.Server(("127.0.0.1", port), g.Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        PortRecorder.ports.clear()
        conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)  # one kept-alive client connection
        try:
            for pause in (0, 0.1, 1.0):
                time.sleep(pause)
                conn.request("POST", "/v1/messages", body=b'{"model":"m","messages":[]}')
                conn.getresponse().read()
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            upstream.shutdown()
            upstream.server_close()
        first, quick, after_idle = PortRecorder.ports
        self.assertEqual(quick, first, "a connection in use was not reused")
        self.assertNotEqual(after_idle, first, "a connection idle past the limit was written into again")


if __name__ == "__main__":
    unittest.main()

