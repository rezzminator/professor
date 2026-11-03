#!/usr/bin/env python3
"""Mock staging preserves the caller's input and always releases a started child."""
import os
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from runtime import command, read_json, write_json


class ReadinessInputs(unittest.TestCase):
    def test_held_receipt_retains_every_native_birth_and_session_presence(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            source, caller, prefix = root / "scenario.json", root / "caller.json", root / "birth"
            write_json(source, {"steps": []})
            write_json(caller, {"scenario": str(source)})
            records = Path(str(prefix) + ".child-records")
            records.mkdir()
            first = {"pid": 177, "start": "first", "parent_pid": 2, "pgid": 177, "cwd": "/synthetic/atlas"}
            second = {"pid": 178, "start": "second", "parent_pid": 2, "pgid": 178, "cwd": "/synthetic/beacon"}
            # The dependency's receipt is newline JSON, rather than a JSON array.
            (records / "birth.json").write_text(json.dumps(first) + "\n" + json.dumps(second) + "\n")
            write_json(root / ".cc/3/sessions/178.json", {"sessionId": "native-second"})
            command([sys.executable, str(Path(__file__).with_name("readiness.py")), "held", str(caller), str(prefix)],
                    env={**os.environ, "JOURNEY_ROOT": scratch, "HOME": scratch})
            self.assertEqual([{**first, "native_session_present": False}, {**second, "native_session_present": True}],
                             read_json(str(prefix) + ".held-births.json"))

    def test_scoped_child_gate_preserves_parent_inputs(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            source, caller, prefix = root / "scenario.json", root / "caller.json", root / "birth"
            original = {"steps": [{"type": "turn", "reply": "pong"}], "record_dir": str(root / "parent-records")}
            write_json(source, original)
            write_json(caller, {"scenario": str(source)})
            environ = {**os.environ, "JOURNEY_ROOT": scratch}
            script = str(Path(__file__).with_name("readiness.py"))
            for operation in ("stage", "release", "restore"):
                command([sys.executable, script, operation, str(caller), str(prefix)], env=environ)
                if operation == "stage":
                    self.assertEqual({**original, "ready_hold": str(prefix) + ".ready", "record_dir": str(prefix) + ".child-records"}, read_json(source))
                elif operation == "release":
                    self.assertEqual({"release": True}, read_json(str(prefix) + ".ready"))
            self.assertEqual(original, read_json(source))
            try:
                command([sys.executable, script, "stage", str(caller), str(prefix)], env=environ)
            except RuntimeError as error:
                self.assertEqual("ValueError: MESH-READINESS: release gate already exists", str(error).splitlines()[-1])
            else:
                self.fail("existing release gate was accepted")


if __name__ == "__main__":
    unittest.main()
