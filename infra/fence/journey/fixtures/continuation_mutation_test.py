#!/usr/bin/env python3
"""Private source mutations must break the continuation observer tests."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import journey_path


class ContinuationMutations(unittest.TestCase):
    def test_missing_successor_guard_is_watched(self):
        source = journey_path.JOURNEY / "continuation.py"
        old = "successor not in transcripts"
        with tempfile.TemporaryDirectory(prefix="continuation-mutation-") as scratch:
            tree = Path(scratch) / "journey"
            shutil.copytree(journey_path.JOURNEY, tree, ignore=shutil.ignore_patterns("__pycache__"))
            path = tree / "continuation.py"
            original = path.read_text()
            self.assertEqual(1, original.count(old), source)
            path.write_text(original.replace(old, "False"))
            result = subprocess.run(
                [sys.executable, str(tree / "fixtures/continuation_test.py"),
                 "ContinuationIndex.test_cycle_stops_at_last_new_row_and_missing_successor_stays_on_predecessor"],
                capture_output=True, text=True,
                env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
            )
            self.assertNotEqual(0, result.returncode)
            self.assertIn("FAILED (errors=", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
