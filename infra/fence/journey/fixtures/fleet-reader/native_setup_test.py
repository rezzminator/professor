#!/usr/bin/env python3
"""Native move preludes keep an actual sibling in the original tab."""
import json
import os
from pathlib import Path
import tempfile
import sys
import unittest
from unittest.mock import patch

import native_setup as setup
from runtime import read_json, write_json


class NativeSetupTests(unittest.TestCase):
    def test_outside_launcher_uses_declared_native_version_and_public_role_staging(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            files = [root / name for name in ("plan", "chat", "owner", "resources")]
            write_json(files[0], {"plan": "outside-launcher-version", "entry": "launcher", "resources": ["tmux_socket", "native_version", "scenario", "records"]})
            write_json(files[1], {"branch": "linux+herdr-shield+project+claude"})
            owner = {"backend": "pty", "socket": str(root / "owner"), "pane": "actual-owner"}
            write_json(files[2], owner)
            with patch.dict(os.environ, {"JOURNEY_ROOT": folder}), patch.object(setup, "shell_ready", return_value=True), \
                    patch.object(setup, "stage_role", return_value={"scenario": "actual-scenario", "records": "actual-records"}) as stage, \
                    patch.object(setup, "tmux_server", return_value={"socket": "actual-owned-socket"}) as server:
                setup.prepare(*map(str, files), "launcher.version")
            self.assertEqual({"id": "launcher.version", "tmux_socket": "actual-owned-socket", "native_version": "2.1.270 (Claude Code)",
                              "scenario": "actual-scenario", "records": "actual-records"}, read_json(files[3]))
            stage.assert_called_once_with(owner, "linux+herdr-shield+project+claude", "reader-native-launcher-version",
                                          str(setup.HERE / "fixtures/fleet-reader/native-version-seed.json"))
            server.assert_called_once_with("reader-native-launcher-stopped", "reader-launcher")

    def test_native_alias_and_before_prompt_inputs_preserve_the_real_launch_contract(self):
        for name, entry, expected_alias in (("outside-before-prompt", "list", True), ("outside-denied-codex", "denied", False)):
            with self.subTest(plan=name), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                alias = root / "claude"
                alias.symlink_to(sys.executable)
                files = [root / file for file in ("plan.json", "chat.json", "owner.json", "resources.json")]
                write_json(files[0], {"plan": name, "entry": entry, "resources": ["id", "scenario", "records", "native_binary", "cwd"]})
                write_json(files[1], {"name": "actual", "key": "actor", "branch": "linux+herdr-shield+project+claude"})
                owner = {"backend": "herdr", "pane": "owner"}
                subject = {"backend": "pty", "pane": "actual-pty", "socket": str(root / "socket")}
                write_json(files[2], owner)
                staged = {"scenario": str(root / "scenario.json"), "records": str(root / "records"), "scenario_session_id": "actual-native-uuid"}
                observed = {"chats": [{"name": "actual", "stored": {"recipe": {"binary": str(alias)}}}]}
                with patch.dict(os.environ, {"JOURNEY_ROOT": folder, "HOME": folder}), patch.object(setup, "shell_ready", return_value=True), \
                        patch.object(setup, "scenario", return_value=staged) as stage, patch.object(setup, "bundle", return_value=observed), \
                        patch.object(setup, "command", return_value=json.dumps(subject)) as create:
                    setup.prepare(*map(str, files), "chat.ls-cli")
                values = read_json(files[3])
                self.assertEqual(str(alias) if expected_alias else str(Path(sys.executable).resolve()), values["native_binary"])
                self.assertEqual(staged["scenario"], values["scenario"])
                self.assertEqual("Owned native input recording directory.\n", (root / "records/.reader-input").read_text())
                self.assertEqual(subject if expected_alias else owner, stage.call_args.args[0])
                if name == "outside-before-prompt":
                    self.assertEqual([sys.executable, str(setup.HERE / "terminal.py"), "create"], create.call_args.args[0])
                    self.assertEqual({"ready_hold": str(root / "reader-native-outside-before-prompt.ready"), "ready_timeout_ms": 120000},
                                     {key: read_json(root / "reader-native-outside-before-prompt.seed.json")[key] for key in ("ready_hold", "ready_timeout_ms")})
                    self.assertEqual("actual-native-uuid", values["session_id"])
                    self.assertEqual(str(root / ".cc/2"), values["config_dir"])
                else:
                    create.assert_not_called()
                    self.assertEqual("", (root / "reader-native-codex-home/config.toml").read_text())

    def test_cost_launch_stages_a_real_root_role_and_the_unopened_native_gate(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            files = [root / name for name in ("plan", "chat", "owner", "resources")]
            branch = "linux+herdr-shield+project+claude"
            write_json(files[0], {"plan": "launch-unreadable-settings", "entry": "launch-reader",
                                  "resources": ["branch", "role", "launch_name", "cwd", "scenario", "records", "ready_hold", "settings", "user_otlp", "native_binary"]})
            write_json(files[1], {"branch": branch})
            owner = {"backend": "pty", "socket": str(root / "owner"), "pane": "actual-owner"}
            write_json(files[2], owner)
            with patch.dict(os.environ, {"JOURNEY_ROOT": folder, "HOME": folder, "PFM_RUN": "owned-12345678"}), \
                    patch.object(setup, "shell_ready", return_value=True), \
                    patch.object(setup, "stage_role", return_value={"scenario": "actual-scenario", "records": "actual-records"}) as stage, \
                    patch.object(setup, "recipe_binary", return_value="actual-native-binary"):
                setup.prepare(*map(str, files), "chat.new-cli")
            self.assertEqual({"id": "chat.new-cli", "branch": branch, "role": "reader-native-cost-launch", "launch_name": "reader-cost-12345678",
                              "cwd": str(root / "work/atlas"), "scenario": "actual-scenario", "records": "actual-records",
                              "ready_hold": str(root / "reader-native-cost-launch.ready"), "settings": str(root / ".cc/1/settings.json"),
                              "user_otlp": "http://127.0.0.1:9", "native_binary": "actual-native-binary"}, read_json(files[3]))
            stage.assert_called_once_with(owner, branch, "reader-native-cost-launch", str(root / "reader-native-cost-launch.seed.json"))
            seed = read_json(root / "reader-native-cost-launch.seed.json")
            self.assertEqual({"ready_hold": str(root / "reader-native-cost-launch.ready"), "ready_timeout_ms": 120000},
                             {key: seed[key] for key in ("ready_hold", "ready_timeout_ms")})
            self.assertEqual(False, (root / "reader-native-cost-launch.ready").exists())

    def test_move_retains_only_an_observed_independent_original_tab_sibling(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            files = [root / name for name in ("plan.json", "chat.json", "owner.json", "resources.json")]
            write_json(files[0], {"plan": "moved", "entry": "identity", "resources": ["id", "placeholder", "destination_workspace"]})
            write_json(files[1], {"terminal": {"backend": "herdr", "pane": "actual-chat"}})
            write_json(files[2], {"backend": "herdr", "pane": "owner"})
            actual = {"identity": {"tab": "actual-original-tab", "workspace": "actual-atlas"}}
            good = {"pane_id": "actual-placeholder", "tab_id": "actual-original-tab"}
            expected_move = {"id": "chat.whoami-cli", "placeholder": "actual-placeholder", "destination_workspace": "actual-beacon"}
            for name, siblings, expected in (("moved", [good], expected_move),
                    ("stale-seat-moved", [good], {**expected_move, "restore_owner": {"backend": "herdr", "pane": "owner"}}),
                    ("stale-seat-missing-pane", [good], {**expected_move, "restore_owner": {"backend": "herdr", "pane": "owner"}}),
                    ("moved", [], None), ("moved", [good, {**good, "pane_id": "another"}], None)):
                write_json(files[0], {"plan": name, "entry": "identity", "resources": ["id", "placeholder", "destination_workspace"]})
                with self.subTest(plan=name, siblings=siblings), patch.dict(os.environ, {"JOURNEY_ROOT": folder, "HERDR_SOCKET_PATH": folder + "/herdr", "HOME": folder}), \
                        patch.object(setup, "terminal", return_value=actual), \
                        patch.object(setup, "carry", return_value={"workspaces": {"beacon": {"id": "actual-beacon"}}}), \
                        patch.object(setup, "request", side_effect=[{"result": {"created": True}}, {"result": {"panes": siblings}}]) as native:
                    if expected is None:
                        with self.assertRaisesRegex(ValueError, "^NATIVE-PRELUDE-PLACEHOLDER:"):
                            setup.prepare(*map(str, files), "chat.whoami-cli")
                    else:
                        setup.prepare(*map(str, files), "chat.whoami-cli")
                        self.assertEqual(expected, read_json(files[3]))
                    self.assertEqual("pane.split", native.call_args_list[0].args[1])
                    self.assertEqual({"target_pane_id": "actual-chat", "direction": "right", "focus": False,
                                      "cwd": str(root / "work/atlas")}, native.call_args_list[0].args[2])

    def test_missing_native_resource_is_a_named_failure(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            files = [root / name for name in ("plan.json", "chat.json", "owner.json", "resources.json")]
            write_json(files[0], {"plan": "indicators-idle", "entry": "list", "resources": ["unavailable-native-input"]})
            write_json(files[1], {})
            write_json(files[2], {})
            with patch.dict(os.environ, {"JOURNEY_ROOT": folder}), self.assertRaisesRegex(ValueError, "^NATIVE-PRELUDE-MISSING: \\['unavailable-native-input'\\]$"):
                setup.prepare(*map(str, files), "chat.ls-cli")


if __name__ == "__main__":
    unittest.main()
