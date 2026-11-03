"""Indicator retirement validates its observer identity before native signalling."""
import os
import sys
import unittest
from unittest.mock import patch

import faults_test
import faults
from observation import process_snapshot
from runtime import start


class IndicatorFault(unittest.TestCase):
    setUp = faults_test.FaultTests.setUp

    def test_retirement_requires_matching_daemon_and_restores_after_error(self):
        socket = self.root / "herdr.sock"
        socket.touch()
        child = start([sys.executable, "-c", "import time; time.sleep(30)"])
        self.addCleanup(child.wait)
        self.addCleanup(lambda: child.terminate() if child.poll() is None else None)
        birth = {key: process_snapshot()[child.pid][key] for key in ("pid", "start")}
        restored = []
        restore = lambda: restored.append("restored")
        with patch.dict(os.environ, {"HERDR_SOCKET_PATH": str(socket)}):
            other = self.root / "other.sock"
            other.touch()
            for target, callback, error in ((other, restore, "SOCKET"), (socket, None, "RESTORE")):
                with self.subTest(error=error), self.assertRaisesRegex(ValueError, "^FAULT-INDICATOR-" + error + ":"):
                    with faults.retired_indicator(birth, target, callback):
                        self.fail("invalid indicator scope accepted")
                self.assertEqual(None, child.poll())
                self.assertEqual([], restored)
            for observed in ("n/a:indicator-daemon-absent", {**birth, "start": "other"}, {**birth, "pid": child.pid + 1}):
                with self.subTest(observed=observed), patch("faults_indicator.indicator_daemon", return_value=observed):
                    with self.assertRaisesRegex(ValueError, "^FAULT-INDICATOR-BIRTH:"):
                        with faults.retired_indicator(birth, socket, restore):
                            self.fail("wrong daemon accepted")
                    self.assertEqual(None, child.poll())
                    self.assertEqual([], restored)
            with patch("faults_indicator.indicator_daemon", return_value=birth) as observer:
                with self.assertRaisesRegex(RuntimeError, "^owner failed$"):
                    with faults.retired_indicator(birth, socket, restore, pane="w1:p2") as actual:
                        self.assertEqual(birth, actual)
                        self.assertEqual(-15, child.wait(timeout=2))
                        raise RuntimeError("owner failed")
                self.assertEqual(1, observer.call_count)
                selected = observer.call_args.kwargs
                self.assertEqual("w1:p2", selected["pane"])
                snapshot = selected.get("snapshot", {})
                self.assertEqual([child.pid], list(snapshot))
                self.assertEqual(birth, {key: snapshot[child.pid][key] for key in ("pid", "start")})
            self.assertEqual(["restored"], restored)


if __name__ == "__main__":
    unittest.main()
