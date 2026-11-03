#!/usr/bin/env python3
"""Pin native topology evidence decisions and the public input hook contract."""
import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
import topology
from runtime import read_json, write_json
from unittest.mock import patch
import copy
import os
import tempfile


class NativeTopology(unittest.TestCase):
    def test_prepare_preserves_shared_config_and_rejects_reuse(self):
        rows = read_json(Path(__file__).with_name("topology-rows.json"))
        with tempfile.TemporaryDirectory(prefix="birth-topology-") as temporary:
            root = Path(temporary)
            home = root / "home"
            home.mkdir()
            config = home / "config.json"
            original = {"claude": {"binary": "/usr/local/bin/claude"}, "chat": {"backend": "auto"}}
            write_json(config, original)
            with patch.dict(os.environ, {"JOURNEY_ROOT": temporary, "HOME": str(home), "PFM_CONFIG": str(config)}):
                for row in rows:
                    with self.subTest(kind=row["kind"]):
                        directory = home / row["kind"]
                        directory.mkdir()
                        result = topology.prepare(row["factors"], str(directory))
                        self.assertEqual(str(directory / "topology-input.json"), result)
                        self.assertEqual({"factors": row["factors"]}, read_json(directory / "topology-state.json"))
                        with self.assertRaisesRegex(ValueError, "^BIRTH-TOPOLOGY-REUSED: staging directory already prepared$"):
                            topology.prepare(row["factors"], str(directory))
                        if row["kind"] == "wrapper":
                            self.assertEqual({"claude": {"binary": str(directory / "topology-claude")}, "chat": {"backend": "tmux"}},
                                             read_json(directory / "topology-config.json"))
                            self.assertEqual('#!/usr/bin/env bash\n/usr/local/bin/claude "$@" <&0 &\nwait "$!"\n',
                                             (directory / "topology-claude").read_text())
                self.assertEqual(original, read_json(config))
                with self.assertRaisesRegex(ValueError, "^BIRTH-TOPOLOGY-FACTORS: unsupported$"):
                    topology.expected("unsupported")

    def test_projection_refuses_each_wrong_native_observation(self):
        attach = {"caller": {"engine": "codex"}, "caller_birth": {"pid": 10, "environment": {"HERDR_PANE_ID": "w1:p2"}},
                  "shell": {"pid": 11, "parent": 10}, "child": {"engine": "claude", "terminal": {"backend": "tmux"}},
                  "child_birth": {"pid": 20, "start": "birth-one", "terminal": {"socket": "child", "pane": "%0"}, "environment": {}},
                  "child_after": {"pid": 20, "start": "birth-one", "terminal": {"socket": "child", "pane": "%0"}},
                  "child_native": {"pid": 20, "parent_pid": 19}, "child_process": {"parent": 19},
                  "session_pane": "HERDR_PANE_ID=w1:p2", "clients": "1\n", "spawn_receipt": {"pid": 11, "exit_code": 0}, "attach_receipt": {"exit_code": 0}}
        wrapper = {"child": {"engine": "claude", "terminal": {"backend": "tmux"}, "session_id": "native-session"},
                   "resumed": {"session_id": "native-session"},
                   "initial_birth": {"pid": 20, "start": "birth-one", "terminal": "same-terminal"},
                   "resumed_birth": {"pid": 21, "start": "birth-two", "terminal": "same-terminal"},
                   "initial_native": {"pid": 20}, "resumed_native": {"pid": 21, "parent_pid": 22}, "wrapper": {"pid": 22},
                   "pane_command": "bash", "engine_tty": "pts/7", "wrapper_tty": "pts/7", "open_ms": 4999,
                   "spawn_status": "0", "open_status": "0"}
        rows = read_json(Path(__file__).with_name("topology-rows.json"))
        changes = {
            "attach": [("caller", "engine", "claude"), ("shell", "parent", 99), ("child", "engine", "codex"),
                       ("spawn_receipt", "pid", 99), ("child_native", "parent_pid", 99),
                       ("child", "terminal", {"backend": "herdr"}), ("child_birth", "environment", {"HERDR_PANE_ID": "w1:p2"}),
                       ("child_birth", "environment", {"NO_COLOR": "1"}), (None, "session_pane", "HERDR_PANE_ID=w9:p9"),
                       (None, "clients", "0\n"), ("child_after", "start", "recycled-birth"),
                       ("spawn_receipt", "exit_code", 2), ("attach_receipt", "exit_code", 1)],
            "wrapper": [("child", "engine", "codex"), ("child", "terminal", {"backend": "herdr"}), (None, "pane_command", "claude"),
                        ("resumed_native", "parent_pid", 99), (None, "wrapper_tty", "pts/8"),
                        ("resumed_birth", "terminal", "foreign-terminal"), ("resumed", "session_id", "foreign-session"),
                        (None, "open_ms", 5000), (None, "open_ms", 5001), (None, "open_ms", -1),
                        ("resumed_native", "pid", 99), (None, "spawn_status", "2"), (None, "open_status", "1")]}
        with tempfile.TemporaryDirectory(prefix="birth-topology-evidence-") as temporary:
            directory = Path(temporary) / "evidence"
            directory.mkdir()
            with patch.dict(os.environ, {"JOURNEY_ROOT": temporary, "HOME": temporary}):
                for row in rows:
                    value = copy.deepcopy(wrapper if row["kind"] == "wrapper" else attach)
                    value["factors"] = row["factors"]
                    write_json(directory / "topology-evidence.json", value)
                    try:
                        actual = topology.assert_topology(row["factors"], str(directory))
                    except ValueError as error:
                        if str(error).startswith("BIRTH-TOPOLOGY-PROJECTION: expected="):
                            self.fail(str(error))
                        raise
                    self.assertEqual(row["expected"], actual)
                    if row["kind"] == "wrapper":
                        for elapsed in (0, 1):
                            value["open_ms"] = elapsed
                            self.assertEqual(True, topology.wrapper_projection(value)["public_open_before_quiet_fallback"])
                    for section, key, wrong in changes[row["kind"]]:
                        changed = copy.deepcopy(value)
                        (changed if section is None else changed[section])[key] = wrong
                        write_json(directory / "topology-evidence.json", changed)
                        with self.subTest(kind=row["kind"], section=section, key=key, wrong=wrong), \
                             self.assertRaisesRegex(ValueError, "^BIRTH-TOPOLOGY-PROJECTION: expected="):
                            topology.assert_topology(row["factors"], str(directory))

    def test_native_shell_contract_keeps_actual_command_and_background_execution(self):
        self.assertEqual([{"type": "tool_call", "tool": "exec_command", "input": {"cmd": "pfm chat new --engine claude", "run_in_background": True}},
                          {"type": "turn", "reply": "spawn-started", "busy_ms": 0}],
                         topology.tool_steps("pfm chat new --engine claude", "spawn-started"))


if __name__ == "__main__":
    unittest.main()
