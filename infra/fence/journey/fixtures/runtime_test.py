#!/usr/bin/env python3
"""Runtime timeout cleanup and byte-preserving log transport."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest

from journey_path import JOURNEY as HERE
from runtime import command


class JourneyRuntime(unittest.TestCase):
    def test_command_timeout_with_inherited_pipe_writers(self):
        root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="command-timeout-")))
        pidfile = root / "writer.pid"
        child_code = "import subprocess,time; from pathlib import Path; child=subprocess.Popen(['sleep','60']); Path(" + repr(str(pidfile)) + ").write_text(str(child.pid)); time.sleep(60)"
        probe = "from runtime import command; command(" + repr([sys.executable, "-c", child_code]) + ", timeout=0.2, start_new_session=True)"
        try:
            try:
                result = subprocess.run([sys.executable, "-c", probe], cwd=HERE,
                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, timeout=4)
            except subprocess.TimeoutExpired:
                self.fail("COMMAND-TIMEOUT-CLEANUP: inherited pipe kept the reader blocked beyond four seconds")
            self.assertEqual(1, result.returncode)
            self.assertIn("COMMAND-UNAVAILABLE", result.stderr)
            self.assertIn("timed out after 0.2 seconds", result.stderr)
        finally:
            if pidfile.exists():
                os.killpg(os.getpgid(int(pidfile.read_text())), signal.SIGKILL)

    def test_console_and_artifact_receive_the_complete_large_write(self):
        with tempfile.TemporaryDirectory() as scratch:
            path = Path(scratch) / "lane.log"
            for size in (8191, 8192, 8193, 18029, 131073):
                with self.subTest(size=size):
                    payload = "x" * size + "\n"
                    captured = {}
                    actual = command([sys.executable, str(HERE / "runtime.py"), "tee", str(path)], input=payload, capture=captured)
                    self.assertEqual(payload, actual)
                    self.assertEqual(payload, path.read_text())
                    self.assertEqual((payload, "", 0), (captured["stdout"], captured["stderr"], captured["status"]))
            captured = {}
            actual = command([sys.executable, "-c", "import sys; print('rows'); print('native-seat-detail', file=sys.stderr); sys.exit(3)"],
                             allowed_codes=(3,), capture=captured)
            self.assertEqual("rows\n", actual)
            self.assertEqual(("rows\n", "native-seat-detail\n", 3), (captured["stdout"], captured["stderr"], captured["status"]))

    def test_command_start_receipt_and_callback_failure_reap_the_child(self):
        for refused in (False, True):
            children = []
            def started(child):
                children.append(child)
                self.assertEqual(None, child.poll())
                if refused:
                    raise ValueError("native birth refused")
                child.stdin.write("release\n")
                child.stdin.flush()
            with self.subTest(refused=refused):
                argv = [sys.executable, "-c", "print(input())"]
                if refused:
                    with self.assertRaisesRegex(ValueError, "^native birth refused$"):
                        command(argv, started=started, input="")
                else:
                    try:
                        output = command(argv, started=started, input="")
                    except RuntimeError as error:
                        self.fail(f"expected callback release, actual={error}")
                    self.assertEqual("release\n", output)
                self.assertEqual(1, len(children))
                self.assertEqual(-signal.SIGKILL if refused else 0, children[0].poll())


if __name__ == "__main__":
    unittest.main()
