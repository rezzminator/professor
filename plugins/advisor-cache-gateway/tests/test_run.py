"""Tests for run.sh: it links Claude Code's settings to the gateway only while the gateway answers."""
import http.client
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(os.path.dirname(HERE), "run.sh")
JQ = shutil.which("jq") or "/usr/bin/jq"


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def wait_for(check, timeout=10.0):
    end = time.time() + timeout
    while time.time() < end:
        if check():
            return True
        time.sleep(0.1)
    return False


class RunTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.settings = os.path.join(self.dir, "settings.json")
        self.port = free_port()
        self.url = "http://127.0.0.1:%d" % self.port
        self.proc = None

    def tearDown(self):
        if self.proc:
            # run.sh leads its own process group: signal the gateways too, or a killed run.sh orphans them.
            try:
                os.killpg(self.proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            self.proc.wait()
            self.proc.stderr.close()
        shutil.rmtree(self.dir)

    def write_settings(self, env):
        with open(self.settings, "w") as f:
            json.dump({"model": "opus", "env": env}, f)

    def read_settings(self):
        with open(self.settings) as f:
            return json.load(f)

    def start(self, python=sys.executable, upstream=None, settle="0"):
        self.upstream = upstream or "http://127.0.0.1:%d" % free_port()
        env = dict(os.environ,
                   ADVISOR_GATEWAY_PYTHON=python,
                   ADVISOR_GATEWAY_JQ=JQ,
                   ADVISOR_GATEWAY_RETRY_SECONDS="0",
                   ADVISOR_GATEWAY_SETTLE_SECONDS=settle,
                   CLAUDE_SETTINGS=self.settings,
                   ADVISOR_CACHE_GATEWAY_PORT=str(self.port),
                   ADVISOR_CACHE_GATEWAY_LOG=os.path.join(self.dir, "gateway.log"),
                   ADVISOR_CACHE_CAPTURE_DIR="off",
                   ADVISOR_CACHE_UPSTREAM=self.upstream)
        self.proc = subprocess.Popen(["/bin/sh", RUN, "serve"], env=env,
                                     stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, start_new_session=True)

    def healthy(self):
        try:
            with urllib.request.urlopen(self.url + "/__gateway/health", timeout=1) as r:
                return r.status == 200
        except OSError:
            return False

    def linked(self):
        return self.read_settings()["env"].get("ANTHROPIC_BASE_URL") == self.url

    def released(self):
        # Released = pointed at the upstream itself. A running Claude Code session follows a changed
        # URL live but keeps a removed one, so failing open must rewrite the URL, never delete it.
        return self.read_settings()["env"].get("ANTHROPIC_BASE_URL") == self.upstream

    def test_links_once_healthy_and_releases_to_the_api_when_stopped(self):
        self.write_settings({"KEEP": "1"})
        self.start()
        self.assertTrue(wait_for(self.linked), "never linked")
        self.assertTrue(self.healthy())
        self.proc.send_signal(signal.SIGTERM)
        self.assertEqual(self.proc.wait(timeout=10), 0)
        settings = self.read_settings()
        self.assertEqual(settings["env"], {"KEEP": "1", "ANTHROPIC_BASE_URL": self.upstream})
        self.assertEqual(settings["model"], "opus")
        self.assertFalse(self.healthy())

    def test_the_settings_file_keeps_its_mode(self):
        self.write_settings({})
        os.chmod(self.settings, 0o600)
        self.start()
        self.assertTrue(wait_for(self.linked), "never linked")
        self.assertEqual(os.stat(self.settings).st_mode & 0o777, 0o600)
        self.proc.send_signal(signal.SIGTERM)
        self.assertEqual(self.proc.wait(timeout=10), 0)
        self.assertTrue(self.released())
        self.assertEqual(os.stat(self.settings).st_mode & 0o777, 0o600)

    def test_links_over_its_own_release(self):
        upstream = "http://127.0.0.1:%d" % free_port()
        self.write_settings({"ANTHROPIC_BASE_URL": upstream})
        self.start(upstream=upstream)
        self.assertTrue(wait_for(self.linked), "a released URL was not taken back")

    def test_missing_python_fails_open(self):
        self.write_settings({"ANTHROPIC_BASE_URL": self.url, "KEEP": "1"})
        self.start(python=os.path.join(self.dir, "no-such-python"))
        self.assertEqual(self.proc.wait(timeout=10), 1)
        self.assertEqual(self.read_settings()["env"], {"KEEP": "1", "ANTHROPIC_BASE_URL": self.upstream})
        self.assertIn(b"missing or older", self.proc.stderr.read())

    def test_gateway_that_dies_fails_open(self):
        self.write_settings({})
        self.start()
        self.assertTrue(wait_for(self.linked), "never linked")
        child = subprocess.run(["pgrep", "-P", str(self.proc.pid)], capture_output=True, text=True).stdout.split()
        self.assertEqual(len(child), 1, child)
        os.kill(int(child[0]), signal.SIGKILL)
        self.assertEqual(self.proc.wait(timeout=10), 1)
        self.assertTrue(self.released())

    def test_port_taken_fails_open(self):
        blocker = socket.socket()
        blocker.bind(("127.0.0.1", self.port))
        blocker.listen(1)
        try:
            self.write_settings({"ANTHROPIC_BASE_URL": self.url})
            self.start()
            self.assertEqual(self.proc.wait(timeout=20), 1)
            self.assertTrue(self.released())
        finally:
            blocker.close()

    def test_other_url_is_left_alone(self):
        other = "http://127.0.0.1:1/other-proxy"
        self.write_settings({"ANTHROPIC_BASE_URL": other})
        self.start()
        self.assertTrue(wait_for(self.healthy), "gateway never answered")
        time.sleep(0.5)
        self.assertEqual(self.read_settings()["env"]["ANTHROPIC_BASE_URL"], other)
        self.proc.send_signal(signal.SIGTERM)
        self.assertEqual(self.proc.wait(timeout=10), 0)
        self.assertEqual(self.read_settings()["env"]["ANTHROPIC_BASE_URL"], other)


    def serving_pid(self):
        try:
            with urllib.request.urlopen(self.url + "/__gateway/health", timeout=1) as r:
                return int(r.read().split(b"pid=")[1].split()[0])
        except (OSError, IndexError, ValueError):
            return None

    def slow_upstream(self, delay=0.25):
        sys.path.insert(0, HERE)
        from test_gateway import SlowUpstream
        from http.server import ThreadingHTTPServer
        handler = type("Upstream", (SlowUpstream,), {"DELAY": delay})
        up = ThreadingHTTPServer(("127.0.0.1", free_port()), handler)
        threading.Thread(target=up.serve_forever, daemon=True).start()
        self.addCleanup(up.server_close)
        self.addCleanup(up.shutdown)
        return "http://127.0.0.1:%d" % up.server_address[1]

    def stream(self, out):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=20)
        conn.request("POST", "/v1/messages", body=b'{"model":"m","messages":[]}')
        out.append(conn.getresponse().read())
        conn.close()

    def test_hup_restarts_without_cutting_a_stream(self):
        self.write_settings({})
        self.start(upstream=self.slow_upstream())
        self.assertTrue(wait_for(self.linked), "never linked")
        first = self.serving_pid()
        got = []
        t = threading.Thread(target=self.stream, args=(got,))
        t.start()
        time.sleep(0.3)
        self.proc.send_signal(signal.SIGHUP)
        self.assertTrue(wait_for(lambda: self.serving_pid() not in (None, first)), "no new gateway took over")
        t.join(20)
        self.assertIn(b"message_stop", got[0])
        self.assertTrue(self.linked())
        self.assertIsNone(self.proc.poll(), "run.sh exited on HUP")

        def drained():
            with open(os.path.join(self.dir, "gateway.log")) as f:
                return "stop: drained, exiting" in f.read()
        # The restart is complete once the old gateway has drained; stop run.sh only then.
        self.assertTrue(wait_for(drained), "the old gateway never drained")
        self.proc.send_signal(signal.SIGTERM)
        self.assertEqual(self.proc.wait(timeout=15), 0)
        self.assertIn(b"restarted: gateway", self.proc.stderr.read())

    @unittest.skipUnless(sys.platform.startswith("linux"), "systemd socket activation is the Linux route")
    def test_socket_activated_hup_under_load_refuses_no_connection(self):
        # Linux spreads new connections over every SO_REUSEPORT listener and resets the ones queued at a
        # listener that closes, so there every gateway generation shares the listener systemd holds.
        self.write_settings({})
        self.upstream = self.slow_upstream(delay=0)
        listener = socket.socket()
        listener.bind(("127.0.0.1", self.port))
        listener.listen(128)
        self.addCleanup(listener.close)
        fd = listener.fileno()
        env = dict(os.environ, ADVISOR_GATEWAY_PYTHON=sys.executable, ADVISOR_GATEWAY_JQ=JQ,
                   ADVISOR_GATEWAY_RETRY_SECONDS="0", ADVISOR_GATEWAY_SETTLE_SECONDS="0",
                   CLAUDE_SETTINGS=self.settings, ADVISOR_CACHE_GATEWAY_PORT=str(self.port),
                   ADVISOR_CACHE_GATEWAY_LOG=os.path.join(self.dir, "gateway.log"),
                   ADVISOR_CACHE_CAPTURE_DIR="off", ADVISOR_CACHE_UPSTREAM=self.upstream,
                   LISTEN_FDS="1", LISTEN_FDNAMES="advisor-cache-gateway.socket")
        # What systemd does: the listener as fd 3, LISTEN_PID naming the main process (exec keeps the pid).
        self.proc = subprocess.Popen(["/bin/sh", "-c", 'exec 3<&%d; LISTEN_PID=$$ exec /bin/sh "$0" serve' % fd, RUN],
                                     env=env, pass_fds=(fd,), stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, start_new_session=True)
        self.assertTrue(wait_for(self.linked), "never linked")
        stop = threading.Event()
        ok, bad = [0], []

        def client():
            while not stop.is_set():
                conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
                try:
                    conn.request("POST", "/v1/messages", body=b'{"model":"m","messages":[]}')
                    resp = conn.getresponse()
                    resp.read()
                    ok[0] += resp.status == 200
                    if resp.status != 200:
                        bad.append(resp.status)
                except Exception as e:
                    bad.append(type(e).__name__)
                finally:
                    conn.close()

        threads = [threading.Thread(target=client) for _ in range(8)]
        for t in threads:
            t.start()
        try:
            for _ in range(10):
                before = self.serving_pid()
                self.proc.send_signal(signal.SIGHUP)
                self.assertTrue(wait_for(lambda: self.serving_pid() not in (None, before)), "no new gateway took over")
                time.sleep(0.2)
        finally:
            stop.set()
            for t in threads:
                t.join(20)
        self.assertEqual(bad, [])
        self.assertGreater(ok[0], 100)
        self.proc.send_signal(signal.SIGTERM)
        self.assertEqual(self.proc.wait(timeout=15), 0)

    def test_stop_releases_first_serves_while_sessions_follow_then_drains(self):
        self.write_settings({})
        self.start(upstream=self.slow_upstream(), settle="2")
        self.assertTrue(wait_for(self.linked), "never linked")
        got = []
        t = threading.Thread(target=self.stream, args=(got,))
        t.start()
        time.sleep(0.3)
        self.proc.send_signal(signal.SIGTERM)
        self.assertTrue(wait_for(self.released, timeout=1), "not released to the upstream at once")
        self.assertTrue(self.healthy(), "stopped answering before running sessions could follow the release")
        self.assertEqual(self.proc.wait(timeout=15), 0)
        t.join(20)
        self.assertIn(b"message_stop", got[0])


if __name__ == "__main__":
    unittest.main()

