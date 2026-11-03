"""Native move evidence is scoped without rewriting retained identities."""
import copy
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import chat_carry
import observation
import observation_hostless as hostless
from herdr import Refused
from runtime import command, write_json


class HostlessObservation(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.receipt_path = Path(self.scratch.name) / "move.json"
        self.chat = {"key": "reader-scratch", "session_id": "carried-session", "seat_id": "carried-seat",
                     "engine": "claude", "terminal": {"backend": "herdr", "pane": "w1:p1"}}
        self.target = {"backend": "herdr", "pane": "w2:p2"}
        self.supervisor = {"pid": 100, "start": "native-supervisor"}
        self.engine = {"pid": 101, "start": "native-engine", "chain": [
            {"pid": 101, "start": "native-engine", "argv": "claude"},
            {"pid": 100, "start": "native-supervisor", "state": "T", "argv": "pfm"}], "environment": {}}
        self.source = {"backend": "herdr", "pane": {"terminal_id": "physical-terminal"},
                       "identity": {"socket": "owned.sock", "pane": "w1:p1"}, "births": [self.engine]}
        self.prekill = {**self.source, "identity": {"socket": "owned.sock", "pane": "w2:p2"}}
        self.display = {**self.prekill, "births": [self.engine], "text": "real engine remains"}
        self.before = {"chats": [{"status": {"session_id": "carried-session"}, "terminal": self.source}]}
        self.receipt = {"run": "fixture-run", "source": self.chat["terminal"], "target": self.target,
                        "supervisor": self.supervisor, "before": self.source, "prekill": self.prekill}
        self.storage = {"seat": [{"seat_id": "carried-seat", "state": "lost", "herdr_socket": "owned.sock", "herdr_pane": "w1:p1",
                                 "seat_host_pid": 100, "herdr_terminal_id": "physical-terminal"}]}
        self.processes = {101: {"pid": 101, "start": "native-engine", "state": "S", "argv": "claude"}}
        environment = patch.dict(os.environ, {"PFM_RUN": "fixture-run", "HERDR_SOCKET_PATH": "owned.sock", "JOURNEY_ROOT": self.scratch.name})
        environment.start()
        self.addCleanup(environment.stop)
        os.environ.pop("JOURNEY_HOSTLESS", None)

    def missing(self, socket, method, params):
        self.assertEqual(("owned.sock", "pane.get", {"pane_id": "w1:p1"}), (socket, method, params))
        raise Refused(method, {"error": {"code": "pane_not_found"}})

    def scope(self):
        write_json(self.receipt_path, self.receipt)
        return chat_carry.temporary_hostless("fixture-branch", "reader-scratch", self.before, self.receipt_path)

    def test_full_projection_keeps_native_hostless_evidence_and_strict_badge_equality(self):
        self.chat["name"] = "reader-scratch"
        storage = {**self.storage, "hidden": []}
        status = {"engine": "cc", "state": "dead"}
        row = {"id": "carried-session", "kind": "dead", "project": "atlas", "name": "reader-scratch",
               "account": "1", "killed": "false"}
        with patch.object(chat_carry, "select", return_value=self.chat), \
             patch.object(observation, "process_snapshot", return_value=self.processes), \
             patch.object(observation, "terminal", return_value=self.display), \
             patch.object(observation, "stored", return_value=storage), \
             patch.object(observation, "report", return_value=json.dumps(status)), \
             patch.object(observation.herdr, "request", side_effect=self.missing), self.scope():
            with self.assertRaises(ValueError) as raised:
                observation.chat_projection(self.chat, [row], {"state": "idle", "line": "actual rendered idle"})
            self.assertIsInstance(raised.exception, observation.lifecycle.ProjectionMismatch)
            actual = raised.exception.projection
            self.assertEqual(status, actual["status"])
            self.assertEqual(self.display, actual["terminal"])
            self.assertEqual(storage, actual["stored"])
            self.assertEqual("alive", actual["lifecycle"]["hostless"]["engines"][0]["state"])
            self.assertEqual({"backend": "herdr", "pane": "w1:p1"}, self.chat["terminal"])

    def test_hostless_scope_reports_actual_engine_and_preserves_carry_in_child(self):
        original = copy.deepcopy(self.chat)
        with patch.object(chat_carry, "select", return_value=self.chat), patch.object(observation, "process_snapshot", return_value=self.processes), \
             patch.object(observation, "terminal", return_value=self.display) as terminal:
            with self.assertRaisesRegex(RuntimeError, "reader-abort"):
                with self.scope() as scope:
                    self.assertEqual(original, self.chat)
                    self.assertEqual({"terminal_id": "physical-terminal"}, scope["receipt"]["prekill"]["pane"])
                    child = command([sys.executable, "-c", "import json,os; print(json.loads(os.environ['JOURNEY_HOSTLESS'])['engines'])"])
                    self.assertEqual("[{'pid': 101, 'start': 'native-engine'}]\n", child)
                    self.assertEqual(None, hostless.hostless_scope({**self.chat, "key": "foreign"}))
                    with self.assertRaisesRegex(ValueError, "OBSERVE-HOSTLESS-IDENTITY"):
                        hostless.hostless_scope({**self.chat, "session_id": "replacement"})
                    for processes, state, current in ((self.processes, "alive", self.processes[101]),
                                                      ({}, "dead", "n/a:process-absent"),
                                                      ({101: {"start": "reused", "state": "S"}}, "dead", {"start": "reused", "state": "S"}),
                                                      ({101: {"start": "native-engine", "state": "Z"}}, "dead", {"start": "native-engine", "state": "Z"})):
                        with self.subTest(state=state, current=current):
                            display, proof = hostless.hostless(self.chat, {"state": "dead"}, self.storage, processes, terminal, self.missing)
                            self.assertEqual(self.display, display)
                            self.assertEqual([{"prior": {"pid": 101, "start": "native-engine"}, "state": state, "current": current}], proof["engines"])
                            self.assertEqual({"prior": {"pid": 100, "start": "native-supervisor"}, "state": "dead"}, proof["supervisor"])
                            self.assertEqual("pane_not_found", proof["stored_pane"])
                    with self.assertRaisesRegex(ValueError, "CHAT-HOSTLESS-SCOPE"):
                        with self.scope():
                            self.fail("nested scope entered")
                    raise RuntimeError("reader-abort")
            self.receipt["prekill"] = {**self.prekill, "births": [], "shield": {"births": [self.engine]}}
            try:
                with self.scope() as scope:
                    self.assertEqual([{"pid": 101, "start": "native-engine"}], scope["engines"])
                    self.assertEqual([{"pid": 101, "start": "native-engine", "chain": [
                        {"pid": 101, "start": "native-engine"}, {"pid": 100, "start": "native-supervisor", "state": "T"}]}],
                        scope["receipt"]["prekill"]["births"])
            except ValueError as error:
                self.fail("actual shield engine birth refused: " + str(error))
        self.assertEqual(None, hostless.hostless_scope(self.chat))
        self.assertEqual(original, self.chat)

    def test_hostless_refuses_receipt_drift_live_supervisor_and_unproven_missing_pane(self):
        mutations = [
            ("OBSERVE-HOSTLESS-RECEIPT", lambda receipt: receipt.update(run="foreign-run")),
            ("OBSERVE-HOSTLESS-RECEIPT", lambda receipt: receipt.update(source=self.target)),
            ("OBSERVE-HOSTLESS-MOVE", lambda receipt: receipt["prekill"]["identity"].update(pane="invented")),
            ("OBSERVE-HOSTLESS-PHYSICAL", lambda receipt: receipt["prekill"]["pane"].update(terminal_id="foreign-terminal")),
            ("OBSERVE-HOSTLESS-SUPERVISOR", lambda receipt: receipt["supervisor"].update(start="reused")),
            ("CHAT-HOSTLESS-ENGINE", lambda receipt: receipt["prekill"]["births"][0].update(start="replacement-engine")),
        ]
        original = copy.deepcopy(self.receipt)
        with patch.object(chat_carry, "select", return_value=self.chat), patch.object(observation, "process_snapshot", return_value=self.processes), \
             patch.object(observation, "terminal", return_value=self.display):
            for error, mutate in mutations:
                self.receipt = copy.deepcopy(original)
                mutate(self.receipt)
                with self.subTest(error=error), self.assertRaisesRegex(ValueError, error):
                    with self.scope():
                        self.fail("invalid scope entered")
                self.assertEqual(None, hostless.hostless_scope(self.chat))
            self.receipt = original
            with self.scope():
                for supervisor, error in (({"start": "native-supervisor", "state": "S"}, "OBSERVE-HOSTLESS-SUPERVISOR"),):
                    with self.assertRaisesRegex(ValueError, error):
                        hostless.hostless(self.chat, {}, self.storage, {100: supervisor}, observation.terminal, self.missing)
                with self.assertRaisesRegex(ValueError, "OBSERVE-HOSTLESS-PANE"):
                    hostless.hostless(self.chat, {}, self.storage, self.processes, observation.terminal, lambda *args: {})
                with self.assertRaisesRegex(Refused, "socket_error"):
                    hostless.hostless(self.chat, {}, self.storage, self.processes, observation.terminal,
                                      lambda *args: (_ for _ in ()).throw(Refused("pane.get", {"error": {"code": "socket_error"}})))
                with self.assertRaisesRegex(ValueError, "OBSERVE-HOSTLESS-SEAT"):
                    hostless.hostless(self.chat, {}, {"seat": []}, self.processes, observation.terminal, self.missing)
                with self.assertRaisesRegex(ValueError, "OBSERVE-HOSTLESS-SEAT"):
                    hostless.hostless(self.chat, {}, {"seat": [{**self.storage["seat"][0], "seat_host_pid": 101}]}, self.processes, observation.terminal, self.missing)
                with patch.object(observation, "terminal", return_value={**self.display, "pane": {"terminal_id": "replaced"}}):
                    with self.assertRaisesRegex(ValueError, "OBSERVE-HOSTLESS-PHYSICAL"):
                        hostless.hostless(self.chat, {}, self.storage, self.processes, observation.terminal, self.missing)
            for payload in ({}, {"run": "fixture-run"}):
                write_json(self.receipt_path, payload)
                with self.assertRaises(KeyError):
                    with chat_carry.temporary_hostless("fixture-branch", "reader-scratch", self.before, self.receipt_path):
                        self.fail("malformed receipt entered")
            self.receipt_path.unlink()
            with self.assertRaises(FileNotFoundError):
                with chat_carry.temporary_hostless("fixture-branch", "reader-scratch", self.before, self.receipt_path):
                    self.fail("missing receipt entered")

    def test_hostless_guards_fail_in_private_fence_copy(self):
        self.assertEqual("1", os.environ.get("PFM_DEV_FENCE"))
        source = Path(__file__).resolve().parents[1]
        mutations = [
            ('observation.terminal_births(receipt["prekill"])', 'receipt["prekill"]["births"]', "test_hostless_scope_reports_actual_engine_and_preserves_carry_in_child"),
            ('return "alive" if birth_presence', 'return "dead" if birth_presence', "test_hostless_scope_reports_actual_engine_and_preserves_carry_in_child"),
            ('if _birth_state(supervisor, processes) != "dead":', 'if False:', "test_hostless_refuses_receipt_drift_live_supervisor_and_unproven_missing_pane"),
            ('if _physical(before) != _physical(prekill) or _physical(display) != _physical(prekill):', 'if False:', "test_hostless_refuses_receipt_drift_live_supervisor_and_unproven_missing_pane"),
            ('if error.code != "pane_not_found":', 'if False:', "test_hostless_refuses_receipt_drift_live_supervisor_and_unproven_missing_pane"),
            ('os.environ.pop("JOURNEY_HOSTLESS", None)', 'pass', "test_hostless_scope_reports_actual_engine_and_preserves_carry_in_child"),
        ]
        with tempfile.TemporaryDirectory(prefix="journey-hostless-mut-") as scratch:
            tree = Path(scratch) / "journey"
            shutil.copytree(source, tree, ignore=shutil.ignore_patterns("__pycache__"))
            path = tree / "observation_hostless.py"
            original = path.read_text()
            for old, new, test in mutations:
                with self.subTest(statement=old):
                    self.assertEqual(1, original.count(old), "MUTATION-SOURCE-DRIFT: " + old)
                    path.write_text(original.replace(old, new))
                    try:
                        try:
                            result = command([sys.executable, str(tree / "fixtures" / "observation_hostless_test.py"), "HostlessObservation." + test],
                                             timeout=20, env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
                        except RuntimeError as error:
                            result = str(error)
                        self.assertIn("AssertionError", result, result)
                        self.assertIn("FAILED (failures=", result, result)
                        print("WATCHED-FAIL observation_hostless.py/" + old + " PASS")
                    finally:
                        path.write_text(original)


if __name__ == "__main__":
    unittest.main()
