#!/usr/bin/env python3
"""Focused evidence for the product paced-worker observer."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from journey_path import JOURNEY as HERE
import observation_paced
from runtime import write_json


class PacedObservationTests(unittest.TestCase):
    def rig(self):
        temporary = tempfile.TemporaryDirectory(prefix="observation-paced-")
        root = Path(temporary.name)
        home = root / "home"
        directory = home / "tmp" / ("cc-sl-paced-" + str(os.getuid()))
        directory.mkdir(parents=True)
        environment = {"JOURNEY_ROOT": str(root), "HOME": str(home), "PFM_HOME": str(home), "PFM_RUN": "fixture-run"}
        return temporary, root, directory, environment

    def input_file(self, directory, key="0123456789abcdef0123456789abcdef", session="fixture-session"):
        path = directory / (key + ".input")
        write_json(path, {"session_id": session, "model": {"display_name": "Opus"}, "context_window": {"used_percentage": 42}})
        return path

    def process(self, pid=41, start="worker-birth", key="0123456789abcdef0123456789abcdef"):
        return {pid: {"pid": pid, "parent": 7, "stat": "S", "state": "S", "start": start,
                      "argv": "/fixture/pfm statusline --paced " + key}}

    def test_actual_input_joins_native_worker_and_preserves_birth_proof(self):
        temporary, root, directory, environment = self.rig()
        self.addCleanup(temporary.cleanup)
        path = self.input_file(directory)
        initial = self.process()
        actual_environment = {"PFM_RUN": "fixture-run", "NO_COLOR": "1"}
        with patch.dict(os.environ, environment, clear=False), \
             patch.object(observation_paced, "process_snapshot", side_effect=[initial, initial]), \
             patch.object(observation_paced, "environment", return_value=actual_environment):
            result = observation_paced.paced_worker({"session_id": "fixture-session"})
        self.assertEqual({**initial[41], "key": path.stem, "input_path": str(path),
                          "input": {"context_window": {"used_percentage": 42}, "model": {"display_name": "Opus"},
                                    "session_id": "fixture-session"}, "environment": actual_environment}, result)

    def test_foreign_worker_is_absent_and_missing_input_is_distinct(self):
        temporary, root, directory, environment = self.rig()
        self.addCleanup(temporary.cleanup)
        self.input_file(directory)
        foreign = self.process()
        with patch.dict(os.environ, {**environment, "PFM_RUN": "fixture-run"}, clear=False), \
             patch.object(observation_paced, "process_snapshot", return_value=foreign), \
             patch.object(observation_paced, "environment", return_value={"PFM_RUN": "other-run"}):
            self.assertEqual("n/a:statusline-paced-worker-absent", observation_paced.paced_worker({"session_id": "fixture-session"}))
        for path in directory.iterdir():
            path.unlink()
        with patch.dict(os.environ, environment, clear=False), patch.object(observation_paced, "process_snapshot") as snapshot:
            self.assertEqual("n/a:statusline-paced-input-absent", observation_paced.paced_worker({"session_id": "fixture-session"}))
            snapshot.assert_not_called()
        directory.rmdir()
        with patch.dict(os.environ, environment, clear=False), patch.object(observation_paced, "process_snapshot") as snapshot:
            self.assertEqual("n/a:statusline-paced-input-absent", observation_paced.paced_worker({"session_id": "fixture-session"}))
            snapshot.assert_not_called()

    def test_ambiguous_workers_are_refused(self):
        temporary, root, directory, environment = self.rig()
        self.addCleanup(temporary.cleanup)
        self.input_file(directory)
        first, second = self.process(pid=41), self.process(pid=42, start="second-birth")
        processes = {**first, **second}
        with patch.dict(os.environ, environment, clear=False), \
             patch.object(observation_paced, "process_snapshot", return_value=processes), \
             patch.object(observation_paced, "environment", return_value={"PFM_RUN": "fixture-run"}), \
             self.assertRaisesRegex(ValueError, "^OBSERVE-PACED-WORKER-AMBIGUOUS:"):
            observation_paced.paced_worker({"session_id": "fixture-session"})

    def test_worker_birth_change_is_refused(self):
        temporary, root, directory, environment = self.rig()
        self.addCleanup(temporary.cleanup)
        self.input_file(directory)
        initial, changed = self.process(), self.process(start="reused-pid")
        with patch.dict(os.environ, environment, clear=False), \
             patch.object(observation_paced, "process_snapshot", side_effect=[initial, changed]), \
             patch.object(observation_paced, "environment", return_value={"PFM_RUN": "fixture-run"}), \
             self.assertRaisesRegex(ValueError, "^OBSERVE-PACED-WORKER-BIRTH:"):
            observation_paced.paced_worker({"session_id": "fixture-session"})

    def test_unreadable_input_is_not_reported_as_absent(self):
        temporary, root, directory, environment = self.rig()
        self.addCleanup(temporary.cleanup)
        (directory / "0123456789abcdef0123456789abcdef.input").write_text("{")
        with patch.dict(os.environ, environment, clear=False), \
             self.assertRaisesRegex(ValueError, "^OBSERVE-PACED-INPUT-READ:"):
            observation_paced.paced_worker({"session_id": "fixture-session"})


if __name__ == "__main__":
    unittest.main()
