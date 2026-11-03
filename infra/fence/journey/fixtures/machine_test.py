#!/usr/bin/env python3
"""Lifecycle observations distinguish advisory text and the running binary."""
import os
import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import machine


def fake_systemd_manager(arguments, manager_output):
    if "--property=MainPID" in arguments:
        return str(os.getpid()) + "\n"
    if "--property=InvocationID,ExecMainStartTimestampMonotonic,NRestarts" in arguments:
        return manager_output
    return "ActiveState=active\n"


class InstalledMachine(unittest.TestCase):
    def test_idle_receipt_waits_for_the_actual_picker_badge(self):
        with tempfile.TemporaryDirectory() as scratch:
            receipt = Path(scratch) / "state.json"
            declared = {"chats": [{"session_id": "owned", "name": "carried-chat"}], "observer_picker": {"pane": "observer"}}
            for status, badge, wanted, expected in (("working", "working", "idle", False),
                    ("idle", "working", "idle", False), ("idle", "idle", "idle", True),
                    ("working", "idle", "working", True)):
                with self.subTest(status=status, badge=badge, wanted=wanted), \
                     patch.dict(os.environ, {"JOURNEY_CARRY": "fixture-carry"}), \
                     patch.object(machine, "read_json", return_value=declared), \
                     patch.object(machine, "report", return_value=json.dumps({"state": status})), \
                     patch.object(machine, "fleet_rows", return_value=[{"id": "owned"}]), \
                     patch.object(machine.observation_picker, "badges", return_value={"owned": {"state": badge, "line": "exact rendered row"}}) as badges:
                    self.assertEqual(expected, machine.chat_state("owned", wanted, receipt))
                    self.assertEqual(status == wanted == "idle", bool(badges.call_count))
                    if expected:
                        self.assertEqual({"state": status, **({"picker": {"state": "idle", "line": "exact rendered row"}} if wanted == "idle" else {})}, json.loads(receipt.read_text()))

    def test_install_waits_for_running_jobs_and_refuses_unreadable_state(self):
        with patch.dict(os.environ, {"PFM_UNIT_PREFIX": "pfm-owned"}):
            for platform, output, expected in (("linux", "inactive", True), ("linux", "failed", True),
                    ("linux", "activating", False), ("linux", "active", False),
                    ("darwin", "state = waiting", True), ("darwin", "state = running", False)):
                with self.subTest(platform=platform, output=output), patch.object(machine.sys, "platform", platform), \
                        patch.object(machine, "command", return_value=output):
                    self.assertEqual(expected, machine.install_idle())
            with patch.object(machine.sys, "platform", "linux"), patch.object(machine, "command", return_value=""), \
                    self.assertRaisesRegex(ValueError, "INSTALL-GATE-UNREADABLE"):
                machine.install_idle()

    def test_doctor_does_not_treat_default_information_as_a_warning(self):
        with tempfile.TemporaryDirectory() as scratch:
            output = Path(scratch) / "doctor.txt"
            output.write_text("doctor: config: missing key chat.backend (default auto) — informational: unset, its default applies\n"
                              "doctor: dep uv path=(none) MISSING optional — install: install uv\n"
                              "doctor: hook claude settings.json SessionStart identity ok\n"
                              "doctor: hook codex[1] hooks.json SessionStart identity MISSING\n"
                              'doctor: herdr-shell: ok user-owned default_shell="/fixture/herdr-shield" in config.toml\n'
                              'doctor: mcp daemon=running pid=42 since=fixture endpoint=http://127.0.0.1:23456\n'
                              "doctor: warnings=1\n")
            self.assertEqual({"errors": ["doctor: hook codex[1] hooks.json SessionStart identity MISSING", "doctor: warnings=1"],
                              "families": ["claude", "codex", "herdr", "mcp"], "hook_accounts": ["claude"],
                              "herdr_shell": ["/fixture/herdr-shield"], "mcp_endpoint": ["http://127.0.0.1:23456"]}, machine.doctor(output))

    def test_known_product_gap_cannot_hide_another_failed_assertion(self):
        with tempfile.TemporaryDirectory() as scratch:
            path = Path(scratch) / "state.json"
            for errors, assertions, expected in [
                ([], [{"source": "gap", "pass": False}, {"source": "install", "pass": True}], True),
                ([], [{"source": "gap", "pass": False}, {"source": "install", "pass": False}], False),
                (["observation failed"], [{"source": "gap", "pass": False}], False),
                ([], [{"source": "gap", "pass": True}], False),
            ]:
                with self.subTest(errors=errors, assertions=assertions):
                    path.write_text(json.dumps({"errors": errors, "assertions": assertions}))
                    self.assertEqual(expected, machine.gap_only(path, "gap"))
            for sources, expected in ((('gap',), False), (('gap', 'color'), True), (('other', 'color'), False)):
                path.write_text(json.dumps({"errors": [], "assertions": [
                    {"source": "gap", "pass": False}, {"source": "color", "pass": False}]}))
                self.assertEqual(expected, machine.gap_only(path, *sources))

    def test_systemd_projection_checks_binary_and_manager_receipt(self):
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"HOME": scratch, "PFM_UNIT_PREFIX": "pfm-fixture"}):
            root = Path(scratch)
            (root / ".local/share/pfm/install").mkdir(parents=True)
            binary = root / ".local/bin/pfm"
            binary.parent.mkdir(parents=True)
            shutil.copyfile(sys.executable, binary)
            manager_output = "InvocationID=fixture-invocation\nExecMainStartTimestampMonotonic=42\nNRestarts=0\n"

            def manager(arguments):
                return fake_systemd_manager(arguments, manager_output)

            with patch.object(machine, "command", side_effect=manager):
                self.assertEqual({"files": {}, "units": {"pfm-fixture-mcp.service": "ActiveState=active\n",
                    "pfm-fixture-schedule.timer": "ActiveState=active\n", "pfm-fixture-name-sync.path": "ActiveState=active\n"},
                    "mcp": {"pid": os.getpid(), "start": "42", "receipt": "fixture-invocation", "restarts": "0"}}, machine.installed())
                binary.write_bytes(b"other release")
                with self.assertRaisesRegex(ValueError, "UNIT-BINARY-MISMATCH: pfm-fixture-mcp.service"):
                    machine.installed()
                manager_output = "ExecMainStartTimestampMonotonic=42\nNRestarts=0\n"
                with self.assertRaisesRegex(ValueError, "MCP-MANAGER-RECEIPT-ABSENT"):
                    machine.installed()

    def test_launchd_projection_uses_the_manager_run_receipt(self):
        with tempfile.TemporaryDirectory() as scratch, patch.dict(os.environ, {"HOME": scratch, "PFM_UNIT_PREFIX": "pfm-fixture"}), \
                patch.object(machine.sys, "platform", "darwin"):
            (Path(scratch) / ".local/share/pfm/install").mkdir(parents=True)

            def manager(arguments):
                label = arguments[-1]
                pid = "41" if label.endswith(".mcp") else "42"
                return f"state = running\npid = {pid}\nruns = 3\n"

            with patch.object(machine, "command", side_effect=manager):
                self.assertEqual({"files": {}, "units": {
                    f"gui/{os.getuid()}/com.professor.pfm-fixture.mcp": "state = running\npid = 41\nruns = 3\n",
                    f"gui/{os.getuid()}/com.professor.pfm-fixture.schedule": "state = running\npid = 42\nruns = 3\n",
                    f"gui/{os.getuid()}/com.professor.pfm-fixture.name-sync": "state = running\npid = 42\nruns = 3\n"},
                    "mcp": {"pid": 41, "start": "3", "receipt": "3"}}, machine.installed())


if __name__ == "__main__":
    unittest.main()
