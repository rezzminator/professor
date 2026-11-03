#!/usr/bin/env python3
"""Public launcher receipts prove the input fault without creating fake chats."""
from contextlib import contextmanager
import copy
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import native_launch as launch
from runtime import write_json


class NativeLaunchTests(unittest.TestCase):
    def failures(self, rows):
        return [row["field"] for row in rows if row["expected"] != row["actual"]]

    def test_version_receipt_requires_native_argv_no_birth_and_unchanged_resources(self):
        inputs = {"native_version": "2.1.270 (Claude Code)"}
        receipt = {"exit_code": 0, "output": "2.1.270 (Claude Code)\n", "elapsed_ms": 10000}
        inventory = {"panes": [{"pane_id": "actual"}], "sockets": ["actual-owned-socket"]}
        cases = [({}, "claude\n--version\n", False, inventory, []), ({"exit_code": 1}, "claude\n--version\n", False, inventory, ["launcher-version-status"]),
                 ({"output": "unrelated version\n"}, "claude\n--version\n", False, inventory, ["launcher-native-version"]),
                 ({}, "claude\n", False, inventory, ["launcher-native-version-argv"]), ({}, "claude\n--version\n", True, inventory, ["launcher-no-chat-birth"]),
                 ({}, "claude\n--version\n", False, {**inventory, "panes": []}, ["launcher-owned-pane-and-socket-inventory"]),
                 ({"elapsed_ms": 9999}, "claude\n--version\n", False, inventory, []),
                 ({"elapsed_ms": 10001}, "claude\n--version\n", False, inventory, ["probe-wall"])]
        for override, argv, birth, after, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(expected, self.failures(launch.version_checks(inputs, {**receipt, **override}, argv, birth, inventory, after)))

    def test_version_full_observation_occurs_inside_stopped_server_scope(self):
        with tempfile.TemporaryDirectory() as folder:
            root, events = Path(folder), []
            owner = {"backend": "pty", "pane": "actual-owner"}
            write_json(root / "owner", owner)
            write_json(root / "scenario", {"version": "2.1.270 (Claude Code)", "record_dir": folder})
            (root / "argv").write_text("claude\n--version\n")
            inputs = {"native_version": "2.1.270 (Claude Code)", "records": folder, "scenario": str(root / "scenario"), "tmux_socket": "owned-socket"}
            @contextmanager
            def stopped(socket):
                self.assertEqual("owned-socket", socket)
                events.append("stop")
                try:
                    yield {"pid": 41, "start": "native"}
                finally:
                    events.append("continue")
            def observed(*args):
                events.append("full-bundle")
                return {"chats": []}
            def execute(target, argv, prefix):
                self.assertEqual((owner, ["claude", "--version"], str(root / "beat") + ".command"), (target, argv, prefix))
                events.append("native-version")
                return {"exit_code": 0, "output": "2.1.270 (Claude Code)\n"}
            checks = []
            with patch.object(launch, "responsive_owner"), patch.object(launch, "outside_checks", return_value=[]), \
                    patch.object(launch, "stopped_checks", return_value=[]), patch.object(launch.faults, "owned_path", side_effect=Path), \
                    patch.object(launch.faults, "stopped_tmux", side_effect=stopped), patch.object(launch.faults, "tmux_birth", return_value={"pid": 41, "start": "native"}), \
                    patch.object(launch, "bundle", side_effect=observed), patch.object(launch, "inventory", return_value={"panes": [], "sockets": []}), \
                    patch.object(launch, "action", side_effect=execute), patch.object(launch, "carry", return_value={"chats": []}), \
                    patch.object(launch, "bundle_checks", return_value=[]):
                result = launch.run_version(root / "owner", inputs, root / "beat", lambda prefix, evidence, rows: checks.extend(rows))
            self.assertEqual(["full-bundle", "stop", "native-version", "full-bundle", "continue", "full-bundle"], events)
            self.assertEqual([], self.failures(checks))
            self.assertEqual({"pid": 41, "start": "native"}, result["stopped_birth"])

    def test_cost_log_reader_retains_only_new_messages_and_refuses_changed_inode_or_truncation(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "cost-sink.log"
            self.assertIsNone(launch.log_cursor(path))
            path.write_text("old cost-launch: earlier native launch\n")
            cursor = launch.log_cursor(path)
            with path.open("a") as stream:
                stream.write("native-time cost-launch: actual owned message\nother-writer: unrelated\n")
            self.assertEqual(["actual owned message"], launch.cost_messages(path, cursor))
            path.write_text("")
            with self.assertRaisesRegex(ValueError, "^NATIVE-COST-LOG-CHANGED:"):
                launch.cost_messages(path, cursor)
            path.rename(path.with_suffix(".old"))
            path.write_text("replacement log with enough bytes to clear the old offset\n")
            with self.assertRaisesRegex(ValueError, "^NATIVE-COST-LOG-CHANGED:"):
                launch.cost_messages(path, cursor)

    def test_telemetry_launch_requires_real_errno_native_identity_user_exporter_and_cause(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            binary, foreign = root / "claude", root / "other"
            binary.write_text("actual fixture binary")
            foreign.write_text("other fixture binary")
            inputs = {"settings": folder + "/account/settings.json", "native_binary": str(binary), "scenario": folder + "/scenario", "user_otlp": "http://127.0.0.1:4318"}
            env = {"PFM_COST_LEDGER": "preempted", "CLAUDE_CONFIG_DIR": folder + "/account", "MOCK_ENGINE_SCENARIO": folder + "/scenario", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318"}
            messages = [f"settings.json unreadable, so the user's telemetry settings cannot be ruled out: read {inputs['settings']}: read {inputs['settings']}: is a directory",
                        "pre-empted, no cost ledger for pid 41: the launch environment already sets OTEL_EXPORTER_OTLP_ENDPOINT"]
            cases = [(None, None, [], 21, False, False, []),
                     ("env", {"PFM_COST_LEDGER": "on"}, [], 21, False, False, ["launch-cost-preempted"]),
                     ("env", {"PFM_COST_LEDGER_PID": "41"}, [], 21, False, False, ["launch-no-owned-cost-sink"]),
                     ("env", {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:9999"}, [], 21, False, False, ["launch-user-otlp-retained"]),
                     ("env", {"CLAUDE_CONFIG_DIR": "other"}, [], 21, False, False, ["launch-native-account"]),
                     ("env", {"MOCK_ENGINE_SCENARIO": "other"}, [], 21, False, False, ["launch-native-scenario"]),
                     ("process", {"pid": 99}, [], 21, False, False, ["launch-native-pid"]),
                     ("process", {"argv": str(foreign)}, [], 21, False, False, ["launch-native-executable"]),
                     (None, None, [], 0, False, False, ["launch-settings-native-errno"]),
                     (None, None, [], 21, True, False, ["launch-ready-gate-closed"]),
                     (None, None, [], 21, False, True, ["launch-before-native-hooks"]),
                     (None, None, [messages[-1]], 21, False, False, ["launch-native-diagnostic"])]
            for field, override, log, number, gate, startup, expected in cases:
                actual_env, process = copy.deepcopy(env), {"pid": 41, "argv": str(binary)}
                if field:
                    (actual_env if field == "env" else process).update(override)
                with self.subTest(field=field, expected=expected):
                    self.assertEqual(expected, self.failures(launch.launch_checks(inputs, {"pid": 41}, process, actual_env, number, log or messages, gate, startup)))

    def test_public_launch_argv_and_root_capture_never_invent_a_parent_edge(self):
        inputs = {"branch": "linux+herdr-shield+project+claude", "role": "reader-telemetry", "launch_name": "telemetry", "cwd": "/fixture/atlas"}
        self.assertEqual(["pfm", "chat", "new", "--engine", "cc", "--backend", "herdr", "--account", "1", "--cwd", "/fixture/atlas", "--name", "telemetry"], launch.launch_arguments(inputs))
        chat = {"origin": "new-from-cli", "role": "reader-telemetry", "branch": inputs["branch"], "name": "telemetry", "account": 1, "session_id": "actual"}
        current = {"status": {"session_id": "actual", "state": "idle"}}
        cases = [({}, current, []), ({"origin": "spawn-child-from-cli"}, current, ["launch-root-identity"]),
                 ({"parent_key": "invented-parent"}, current, ["launch-root-parent-edge"]),
                 ({}, {"status": {"session_id": "foreign", "state": "idle"}}, ["launch-root-public-session"]),
                 ({}, {"status": {"session_id": "actual", "state": "dead"}}, ["launch-root-public-state"])]
        for override, observed, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(expected, self.failures(launch.captured_checks(inputs, {**chat, **override}, observed)))

    def test_native_sink_filter_binds_the_actual_claude_pid_and_kernel_birth(self):
        process = {"pid": 51, "start": "actual-sink-birth", "argv": "/owned/pfm internal cost-sink --pid 41 --start 123"}
        for argv, expected in ((process["argv"], [process]),
                               ("/owned/pfm internal cost-sink --pid 42 --start 123", []),
                               ("/owned/pfm internal cost-sink --pid 41 --start 124", []),
                               ("/owned/pfm internal statusline --pid 41 --start 123", [])):
            with self.subTest(argv=argv):
                self.assertEqual(expected, launch.cost_processes({"pid": 41, "start": 123}, {51: {**process, "argv": argv}}))

    def test_restored_version_keeps_the_original_birth_and_all_panes_and_sockets(self):
        birth, inventory = {"pid": 41, "start": "actual"}, {"panes": ["actual"], "sockets": ["owned"]}
        for actual, after, expected in ((birth, inventory, []),
                                        ({**birth, "start": "reused"}, inventory, ["launcher-restored-server-birth"]),
                                        (birth, {**inventory, "sockets": []}, ["launcher-final-inventory"])):
            with self.subTest(expected=expected):
                self.assertEqual(expected, self.failures(launch.version_restored_checks(birth, actual, inventory, after)))


if __name__ == "__main__":
    unittest.main()
