#!/usr/bin/env python3
"""Actual process births, environment boundaries and kernel accounting."""
import errno, json, os, select, signal, subprocess, sys, tempfile, unittest
from pathlib import Path
from unittest.mock import patch

import journey_path
import observation
import observation_process
from runtime import command, start


class Observation(unittest.TestCase):
    def test_reload_readiness_requires_one_actual_replacement_birth(self):
        def birth(pid, start):
            return {"pid": pid, "start": start, "chain": [{"argv": "claude"}], "environment": {}}
        old = birth(42, "old")
        before = {"status": {"session_id": "owned"}, "terminal": {"identity": {}}, "process_births": [old]}
        previous, carry = {"chats": [before]}, {"session_id": "owned", "engine": "claude"}
        for births, expected in (([], False), ([old], False), ([birth(43, "new")], True),
                                 ([birth(42, "new")], True), ([old, birth(43, "new")], False)):
            with self.subTest(births=births):
                self.assertEqual(expected, observation.reload_ready(previous, {"identity": {}, "births": births}, carry))
        for births in ([], [old, birth(43, "new")]):
            with self.subTest(prior=births), self.assertRaisesRegex(ValueError, "RELOAD-PRIOR-ENGINE"):
                observation.reload_ready({"chats": [{**before, "process_births": births}]}, {"identity": {}, "births": [old]}, carry)
        with self.assertRaisesRegex(ValueError, "ATTENTION-CHAT"):
            observation.reload_ready({"chats": []}, {}, carry)
        with self.assertRaises(KeyError):
            observation.reload_ready(previous, {"identity": {}, "births": [{"pid": 1}]}, carry)

    def test_terminal_recaptures_only_confirmed_process_exits_and_retains_evidence(self):
        old = {"pid": 42, "parent": 0, "start": "old-birth", "argv": "short-lived-hook"}
        current = {"pid": 43, "parent": 0, "start": "new-birth", "argv": "claude"}
        denied = PermissionError(errno.EACCES, "Permission denied", "/proc/42/environ")
        for error, following in ((FileNotFoundError("/proc/42/environ"), {}), (denied, {}),
                                 (denied, {42: {**old, "start": "replacement-birth"}})):
            with self.subTest(error=type(error).__name__, following=following):
                reads = []
                def display(target):
                    reads.append(target)
                    return {"births": observation.process_tree([42 if len(reads) == 1 else 43])}
                def environment(pid):
                    if pid == 42:
                        raise error
                    return {"PFM_RUN": "synthetic"}
                with patch.object(observation, "_terminal", side_effect=display), \
                     patch.object(observation, "process_snapshot", side_effect=[{42: old}, following, {43: current}]), \
                     patch.object(observation, "environment", side_effect=environment):
                    actual = observation.terminal({"pane": "owned"})
                self.assertEqual({"births": [{"pid": 43, "start": "new-birth", "chain": [current],
                    "environment": {"PFM_RUN": "synthetic"}}], "process_read_exits": [old],
                    "process_read_failures": [{"process": old, "following": following.get(42),
                    "error": {"type": type(error).__name__, "errno": error.errno, "message": str(error)}}]}, actual)
                self.assertEqual([{"pane": "owned"}, {"pane": "owned"}], reads)
        for error in (FileNotFoundError("still-present"), denied, PermissionError(errno.EPERM, "not EACCES")):
            with self.subTest(error=type(error).__name__), patch.object(observation, "process_snapshot", return_value={42: old}), \
                 patch.object(observation, "environment", side_effect=error):
                with self.assertRaises(type(error)):
                    observation.process_tree([42])
        if sys.platform != "linux":
            return
        if os.geteuid() == 0:
            command(["setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "--bounding-set=-all",
                     sys.executable, __file__, "Observation.test_terminal_recaptures_only_confirmed_process_exits_and_retains_evidence"])
            return
        child = start([sys.executable, "-u", "-c",
                       "import ctypes,sys; assert ctypes.CDLL(None).prctl(4,0)==0; print('ready'); sys.stdin.read()"],
                      stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            self.assertEqual([child.stdout], select.select([child.stdout], [], [], 3)[0])
            self.assertEqual("ready\n", child.stdout.readline())
            birth = observation.process_snapshot()[child.pid]
            with self.assertRaises(PermissionError) as live:
                observation.process_tree([child.pid])
            self.assertEqual(13, live.exception.errno)
            native_environment = observation.environment
            def exits_after_native_denial(pid):
                try:
                    return native_environment(pid)
                except PermissionError:
                    child.stdin.close()
                    child.wait(timeout=3)
                    raise
            with patch.object(observation, "environment", side_effect=exits_after_native_denial):
                with self.assertRaises(observation.ProcessExited) as exited:
                    observation.process_tree([child.pid])
            self.assertEqual((child.pid, birth["start"], 13, None),
                             (exited.exception.process["pid"], exited.exception.process["start"],
                              exited.exception.__cause__.errno, exited.exception.evidence["following"]))
        finally:
            if not child.stdin.closed:
                child.stdin.close()
            if child.poll() is None:
                child.kill()
            child.wait(timeout=3)
            child.stdout.close()

    def test_engine_births_bind_versioned_image_to_observed_recipe(self):
        target = {"pane": "w1:p1"}
        for image, expected in (("/fixture/versions/2.1.250", [42]), ("/foreign/2.1.250", []), ("/fixture/versions/2.1.999", [])):
            with self.subTest(image=image):
                observed = {"stored": {"recipe": {"binary": "/fixture/versions/2.1.250"}},
                            "terminal": {"identity": target}, "process_births": [{"pid": 42, "start": "fixture-start",
                            "chain": [{"argv": image + " --resume fixture-session"}], "environment": {}}]}
                result = observation_process.engine_births(observed, "claude")
                self.assertEqual(expected, [row["pid"] for row in result])
                if expected:
                    self.assertEqual({"pid": 42, "start": "fixture-start", "argv": [image, "--resume", "fixture-session"],
                                      "environment": {}, "terminal": target}, result[0])

    def test_process_environment_keeps_configuration_and_redacts_credentials(self):
        expected = {"CLAUDE_CONFIG_DIR": "/fixture/account", "PFM_RUN": "synthetic", "MOCK_ENGINE_SCENARIO": "/fixture/scenario.json", "PFM_TEST_FRESH_SOCKET": "engine-fixture",
                    "CODEX_TOOL_CALL_ID": "fixture-tool", "NO_COLOR": "1", "PAGER": "cat", "GIT_PAGER": "cat", "GH_PAGER": "cat",
                    "CODEX_CI": "1", "CODEX_SESSION_ID": "fixture-session", "CODEX_VERSION": "fixture-version",
                    "CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS": "1", "HERDR_ENV": "fixture", "HERDR_WORKSPACE_ID": "w1",
                    "HERDR_TAB_ID": "w1:t1", "HERDR_BIN_PATH": "/fixture/herdr",
                    "PFM_CLAUDE_CONFIG_DIR_DEFAULT": "/fixture/default-account", "XPC_SERVICE_NAME": "com.professor.fixture.theme",
                    "PFM_COST_LEDGER": "/fixture/cost-ledger", "PFM_COST_LEDGER_PID": "42",
                    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "CLAUDE_CODE_ENABLE_TELEMETRY": "1"}
        child = subprocess.Popen([sys.executable, "-c", "import time;time.sleep(10)"], env={"PATH": "/usr/bin:/bin", **expected, "ANTHROPIC_API_KEY": "synthetic-secret"})
        try:
            actual = observation.process_tree([child.pid])
            self.assertEqual(expected, actual[0]["environment"])
            self.assertEqual(child.pid, actual[0]["chain"][0]["pid"])
        finally:
            child.terminate()
            child.wait(timeout=5)

    def test_expected_environment_denial_is_native_and_visible_to_worker_threads(self):
        if sys.platform == "linux" and os.geteuid() == 0 and not os.environ.get("JOURNEY_PERMISSION_TEST_CHILD"):
            command(["setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "--bounding-set=-all",
                     sys.executable, __file__, "Observation.test_expected_environment_denial_is_native_and_visible_to_worker_threads"],
                    env={**os.environ, "JOURNEY_PERMISSION_TEST_CHILD": "1"})
            return
        if sys.platform != "linux":
            return
        import faults
        from concurrent.futures import ThreadPoolExecutor
        with tempfile.TemporaryDirectory(prefix="observation-denial-") as scratch:
            root = Path(scratch)
            (root / "home").mkdir()
            (root / "artifacts").mkdir()
            with patch.dict(os.environ, {"JOURNEY_ROOT": str(root), "HOME": str(root / "home"), "PFM_RUN": "synthetic"}):
                prefix = root / "artifacts" / "permission"
                with faults.nondumpable(prefix):
                    receipt = prefix.with_name(prefix.name + ".environ.json")
                    with observation.expected_environment_denial(receipt) as expected:
                        self.assertEqual({"state": "denied", "pid": os.getpid(),
                                          "start": expected["start"], "errno": 13,
                                          "receipt": str(receipt)}, expected)
                        read_fd, write_fd = os.pipe()
                        child = os.fork()
                        if child == 0:
                            os.close(read_fd)
                            try:
                                with ThreadPoolExecutor(max_workers=1) as pool:
                                    value = pool.submit(observation.environment, os.getppid()).result()
                                payload = {"value": value}
                            except Exception as error:
                                payload = {"error": type(error).__name__ + ": " + str(error)}
                            with os.fdopen(write_fd, "w") as stream:
                                stream.write(json.dumps(payload))
                            os._exit(0)
                        os.close(write_fd)
                        raw = os.read(read_fd, 65536)
                        os.close(read_fd)
                        _, status = os.waitpid(child, 0)
                        self.assertEqual(0, os.waitstatus_to_exitcode(status), raw.decode())
                        self.assertEqual({"value": expected}, json.loads(raw))
                probe = 'import os; open("/proc/"+str(os.getppid())+"/environ","rb").close(); print("readable")'
                self.assertEqual("readable\n", command([sys.executable, "-c", probe]))

    def test_process_snapshot_preserves_birth_and_process_metrics_are_actual(self):
        code = "import os,sys; os.chdir('/'); pid=os.fork(); (os._exit(0) if pid==0 else None); print(pid,flush=True); sys.stdin.read(); os.waitpid(pid,0)"
        child = subprocess.Popen([sys.executable, "-c", code], env={"PATH": "/usr/bin:/bin"},
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            zombie = int(child.stdout.readline())
            from bootstrap import wait
            row = wait("owned child zombie", lambda: (value if (value := observation.process_snapshot().get(zombie, {})).get("state") == "Z" else None))
            self.assertEqual("Z", row["state"])
            self.assertEqual(child.pid, row["parent"])
            actual = observation.process_snapshot()[child.pid]
            self.assertEqual(child.pid, actual["pid"])
            self.assertIsInstance(actual["start"], str)
            self.assertEqual("/", observation.process_cwd(child.pid))
            try:
                os.kill(child.pid, signal.SIGSTOP)
                stopped = wait("owned process kernel stopped", lambda: (value if (value := observation.process_state(child.pid))["state"] == "T" else None))
                self.assertEqual({"pid": child.pid, "start": actual["start"], "state": "T"}, stopped)
            finally:
                os.kill(child.pid, signal.SIGCONT)
            resumed = wait("owned process kernel sleeping", lambda: (value if (value := observation.process_state(child.pid))["state"] == "S" else None))
            self.assertEqual({"pid": child.pid, "start": actual["start"], "state": "S"}, resumed)
            counters = observation_process.process_metrics(child.pid)
            self.assertGreaterEqual(counters["cpu_seconds"], 0)
            self.assertTrue(isinstance(counters["write_bytes"], int) or counters["write_bytes"].startswith("n/a:"))
            with patch.object(observation_process, "_kernel_start", side_effect=["old", "replacement"]), \
                 self.assertRaisesRegex(ValueError, "OBSERVE-PROCESS-STATE-BIRTH"):
                observation.process_state(child.pid)
        finally:
            child.stdin.close()
            child.wait(timeout=5)
            child.stdout.close()

    def test_darwin_process_environment_reads_the_kernel_abi(self):
        import ctypes
        from types import SimpleNamespace
        payload = bytes(ctypes.c_int(2)) + b"/fixture/codex\0\0/fixture/codex\0--resume\0CODEX_HOME=/fixture/codex-home\0TOKEN=synthetic-secret\0"
        def sysctl(names, count, buffer, size, new, newsize):
            self.assertEqual([1, 49, 123], list(names))
            self.assertEqual(3, count)
            ctypes.cast(size, ctypes.POINTER(ctypes.c_size_t)).contents.value = len(payload)
            if buffer is not None:
                ctypes.memmove(buffer, payload, len(payload))
            return 0
        with patch.object(observation_process.sys, "platform", "darwin"), patch.object(observation_process.ctypes, "CDLL", return_value=SimpleNamespace(sysctl=sysctl)):
            self.assertEqual({"CODEX_HOME": "/fixture/codex-home"}, observation_process.environment(123))

    def test_darwin_process_metrics_read_cumulative_kernel_counters(self):
        import ctypes
        import errno
        import struct
        from unittest.mock import Mock
        # Apple's rusage_info_v2: UUID, then eighteen uint64_t counters.
        for user, system, written, native_errno, status, numer, denom, expected in (
                (54_000_000, 12_000_000, 4_294_971_392, 0, 0, 125, 3,
                 {"cpu_seconds": 2.75, "write_bytes": 4_294_971_392}),
                (2_250_000_000, 500_000_000, 4096, 0, 0, 1, 1,
                 {"cpu_seconds": 2.75, "write_bytes": 4096}),
                (0, 0, 0, 0, 0, 125, 3, {"cpu_seconds": 0.0, "write_bytes": 0}),
                (0, 0, 0, errno.ESRCH, 0, 125, 3,
                 {"cpu_seconds": "n/a:process-absent", "write_bytes": "n/a:process-absent"}),
                (0, 0, 0, errno.EPERM, 0, 125, 3,
                 {"cpu_seconds": "n/a:process-permission", "write_bytes": "n/a:process-permission"}),
                (0, 0, 0, errno.EACCES, 0, 125, 3,
                 {"cpu_seconds": "n/a:process-permission", "write_bytes": "n/a:process-permission"}),
                (0, 0, 0, errno.EIO, 0, 125, 3, None),
                (0, 0, 0, 0, 5, 125, 3, "OBSERVE-PROCESS-TIMEBASE: status=5 numer=125 denom=3"),
                (0, 0, 0, 0, 0, 0, 3, "OBSERVE-PROCESS-TIMEBASE: status=0 numer=0 denom=3"),
                (0, 0, 0, 0, 0, 125, 0, "OBSERVE-PROCESS-TIMEBASE: status=0 numer=125 denom=0")):
            with self.subTest(native_errno=native_errno, written=written, timebase=(status, numer, denom)):
                payload = struct.pack("=16s18Q", b"fixture-uuid-123", user, system,
                                      3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, written)
                def read(pid, flavor, buffer):
                    self.assertEqual((123, 2), (pid, flavor))
                    self.assertEqual(160, ctypes.sizeof(buffer._obj))
                    ctypes.memmove(buffer, payload, len(payload))
                    ctypes.set_errno(native_errno)
                    return -1 if native_errno else 0
                native = Mock()
                native.proc_pid_rusage.side_effect = read
                def clock(buffer):
                    self.assertEqual(8, ctypes.sizeof(buffer._obj))
                    ctypes.memmove(buffer, struct.pack("=II", numer, denom), 8)
                    return status
                native.mach_timebase_info.side_effect = clock
                with patch.object(observation_process.sys, "platform", "darwin"), patch.object(
                        observation_process.ctypes, "CDLL", return_value=native):
                    if expected is None:
                        with self.assertRaises(OSError) as failure:
                            observation_process.process_metrics(123)
                        self.assertEqual((errno.EIO, "read process resource usage", 123),
                                         (failure.exception.errno, failure.exception.strerror, failure.exception.filename))
                    elif isinstance(expected, str):
                        with self.assertRaises(ValueError) as failure:
                            observation_process.process_metrics(123)
                        self.assertEqual(expected, str(failure.exception))
                    else:
                        self.assertEqual(expected, observation_process.process_metrics(123))

    def test_darwin_process_cwd_reads_native_fields_and_checks_birth(self):
        for output, status, stderr, starts, expected in (
                ("p123\0\nfcwd\0n/\0\n", 0, "", ["birth", "birth"], "/"),
                ("p123\0\nfcwd\0n/fixture/atlas worktree\0\n", 0, "", ["birth", "birth"], "/fixture/atlas worktree"),
                ("", 1, "permission denied", ["birth"], "OBSERVE-PROCESS-CWD-READ: pid=123: exit=1 stderr='permission denied'"),
                ("", 1, "", ["birth"], "OBSERVE-PROCESS-CWD-READ: pid=123: exit=1 stderr=''"),
                ("p123\0\nfcwd\0n/\0\n", 0, "permission denied", ["birth"],
                 "OBSERVE-PROCESS-CWD-READ: pid=123: exit=0 stderr='permission denied'"),
                ("p123\0\nfcwd\0n/\0\n", 0, "", ["birth", "replacement"],
                 "OBSERVE-PROCESS-CWD-BIRTH: pid=123: expected=birth actual=replacement"),
                ("p999\0\nfcwd\0n/\0\n", 0, "", ["birth", "birth"],
                 "OBSERVE-PROCESS-CWD-SHAPE: pid=123: 'p999\\x00\\nfcwd\\x00n/\\x00\\n'"),
                ("p123\0\nfroot\0n/\0\n", 0, "", ["birth", "birth"],
                 "OBSERVE-PROCESS-CWD-SHAPE: pid=123: 'p123\\x00\\nfroot\\x00n/\\x00\\n'"),
                ("p123\0\nfcwd\0nrelative\0\n", 0, "", ["birth", "birth"],
                 "OBSERVE-PROCESS-CWD-SHAPE: pid=123: 'p123\\x00\\nfcwd\\x00nrelative\\x00\\n'")):
            with self.subTest(output=output, status=status, starts=starts):
                def read(arguments, *, allowed_codes, capture):
                    self.assertEqual(["/usr/sbin/lsof", "-a", "-p", "123", "-d", "cwd", "-Fpnf0"], arguments)
                    self.assertEqual((0, 1), allowed_codes)
                    capture.update(status=status, stderr=stderr)
                    return output
                with patch.object(observation_process.sys, "platform", "darwin"), patch.object(
                        observation_process, "_kernel_start", side_effect=starts) as birth, patch.object(
                        observation_process, "command", side_effect=read):
                    if expected.startswith("/"):
                        self.assertEqual(expected, observation.process_cwd(123))
                        self.assertEqual([((123,), {}), ((123,), {})], birth.call_args_list)
                    else:
                        with self.assertRaises((RuntimeError, ValueError)) as failure:
                            observation.process_cwd(123)
                        self.assertEqual(expected, str(failure.exception))

    def test_process_cwd_preserves_native_absence_and_permission_failures(self):
        for failure in (FileNotFoundError(2, "process absent"), PermissionError(13, "process refused")):
            with self.subTest(failure=failure), patch.object(observation_process.sys, "platform", "linux"), patch.object(
                    observation_process, "_kernel_start", return_value="birth"), patch.object(
                    observation_process.os, "readlink", side_effect=failure):
                with self.assertRaises(type(failure)) as caught:
                    observation.process_cwd(123)
                self.assertEqual((failure.errno, failure.strerror), (caught.exception.errno, caught.exception.strerror))


if __name__ == "__main__":
    unittest.main()
