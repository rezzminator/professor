#!/usr/bin/env python3
"""Pin cancellation evidence: a leaked child must stay red after recovery."""
from unittest.mock import patch
import copy
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
import cancel
from bootstrap import wait
from runtime import command, read_json
from terminal import type_command
from observation import terminal


class CancellationEvidence(unittest.TestCase):
    def test_owner_ctrl_c_keeps_actual_shell_status_and_recovers(self):
        with tempfile.TemporaryDirectory(prefix="birth-cancel-owner-") as temporary:
            root = Path(temporary)
            target = {"backend": "tmux", "socket": str(root / "tmux"), "pane": "%0"}
            with patch.dict(os.environ, {"JOURNEY_ROOT": temporary, "HOME": temporary,
                                         "PFM_RUN": "synthetic", "PFM_BEAT": "cancellation-self-test"}):
                command(["tmux", "-S", target["socket"], "-f", "/dev/null", "new-session", "-d",
                         "-s", "owner", "zsh", "-f"])
                try:
                    wait("synthetic owner shell", lambda: cancel.shell_ready(target), 5)
                    type_command(target, ["cat"], str(root / "cancelled"))
                    wait("actual foreground cat", lambda: any(
                        row["chain"][0]["argv"] == "cat" for row in terminal(target)["births"]), 5)
                    self.assertEqual({"status": "130", "output": ""}, cancel.cancel_owner(target, root / "cancelled"))
                    self.assertEqual({"status": "0", "output": "recovered\n"},
                                     cancel.public(target, ["printf", "recovered\\n"], root / "recovery"))
                finally:
                    command(["tmux", "-S", target["socket"], "kill-server"])

    def test_projection_keeps_precancellation_birth_and_every_cleanup_refusal(self):
        absent = {"child": None, "launcher": None, "panes": [], "seats": [],
                  "chats": [], "launches": [], "activity": "n/a:reporter-activity",
                  "native_session": None, "startup_events": []}
        evidence = {"held": {**absent, "child": {"pid": 20}}, "after_cancel": absent,
                    "gate_open_at_cancel": False, "cancel_receipt": {"status": "130"},
                    "recovery": {"session_id": "actual-recovery"}}
        expected = {"actual_child_born": True, "cancelled_before_session_start": True,
                    "owner_exit_status": "130", "launcher_gone": True, "child_gone": True,
                    "pane_gone": True, "seat_gone": True, "chat_gone": True, "launch_gone": True,
                    "native_session_gone": True, "activity": "n/a:reporter-activity",
                    "recovery_captured": True}
        self.assertEqual(expected, cancel.projection(evidence))
        for section, key, wrong, field in (
                ("held", "child", None, "actual_child_born"),
                ("held", "native_session", {"pid": 20}, "cancelled_before_session_start"),
                ("held", "startup_events", [{"event": "SessionStart-completed"}], "cancelled_before_session_start"),
                ("after_cancel", "child", {"pid": 20}, "child_gone"),
                ("after_cancel", "launcher", {"pid": 10}, "launcher_gone"),
                ("after_cancel", "panes", ["%1"], "pane_gone"),
                ("after_cancel", "seats", [{"seat_id": "native-seat"}], "seat_gone"),
                ("after_cancel", "chats", [{"uuid": "cancelled-session"}], "chat_gone"),
                ("after_cancel", "launches", [{"session_id": "cancelled-session"}], "launch_gone"),
                ("after_cancel", "native_session", {"pid": 20}, "native_session_gone")):
            changed = copy.deepcopy(evidence)
            changed[section][key] = wrong
            with self.subTest(field=field, key=key):
                self.assertEqual({**expected, field: False}, cancel.projection(changed))
        changed = {**evidence, "gate_open_at_cancel": True, "recovery": None}
        self.assertEqual({**expected, "cancelled_before_session_start": False, "recovery_captured": False},
                         cancel.projection(changed))

    def test_prepare_stages_only_owned_inputs_and_refuses_reuse(self):
        with tempfile.TemporaryDirectory(prefix="birth-cancel-") as temporary:
            directory = Path(temporary) / "cancelled"
            directory.mkdir()
            with patch.dict(os.environ, {"JOURNEY_ROOT": temporary, "HOME": temporary, "PFM_RUN": "synthetic"}):
                path = cancel.prepare(cancel.FACTORS, str(directory))
                value = read_json(path)
                self.assertEqual(str(directory / "cancel-input.json"), path)
                self.assertEqual(str(directory / "cancel-ready.json"), value.pop("ready_hold"))
                self.assertEqual(30000, value.pop("ready_timeout_ms"))
                self.assertEqual(read_json(Path(__file__).with_name("scenario.json")), value)
                with self.assertRaisesRegex(ValueError, "^BIRTH-CANCEL-REUSED: staging directory already prepared$"):
                    cancel.prepare(cancel.FACTORS, str(directory))
                with self.assertRaisesRegex(ValueError, "^BIRTH-CANCEL-FACTORS: unsupported$"):
                    cancel.prepare("unsupported", str(directory))
                with self.assertRaises(ValueError):
                    cancel.prepare(cancel.FACTORS, temporary)


if __name__ == "__main__":
    unittest.main()
