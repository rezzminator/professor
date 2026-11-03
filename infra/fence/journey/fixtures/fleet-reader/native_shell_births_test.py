#!/usr/bin/env python3
"""Picker-birth oracles distinguish the real caller from its inherited pane."""
import copy
from contextlib import contextmanager
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

import native_shell_births as births
import native_shells as shells
from runtime import read_json


class NativeShellBirthTests(unittest.TestCase):
    def test_auxiliary_fresh_scope_precedes_validation_and_retires_before_move_cleanup(self):
        subject = {"backend": "herdr", "pane": "source"}
        aux = {"backend": "tmux", "socket": "/owned/aux", "pane": "aux:0.0"}
        events, active = [], []
        @contextmanager
        def authorize(target):
            self.assertEqual(aux, target)
            active.append(target)
            events.append("authorize-aux")
            try:
                yield target
            finally:
                active.pop()
                events.append("retire-aux")
        @contextmanager
        def moved(*args):
            events.append("move")
            try:
                yield {"backend": "herdr", "pane": "moved"}
            finally:
                events.append("restore-move")
        def owned(target):
            self.assertEqual([aux], active)
            self.assertEqual(aux, target)
            events.append("validate-aux")
            raise RuntimeError("named auxiliary validation failed")
        mover = SimpleNamespace(destination="beacon", move=None, current=lambda: {"pane_id": "restored"})
        with patch.dict(births.os.environ, {"HERDR_SOCKET_PATH": "/owned/herdr"}), \
                patch("native_forecasts.PaneMove", return_value=mover), patch.object(births.faults, "moved_pane", side_effect=moved), \
                patch.object(births.faults, "fresh_target", side_effect=authorize), \
                patch.object(births, "terminal", return_value={"actual": "original"}), patch.object(births, "type_command"), \
                patch.object(births, "wait", return_value=True), patch.object(births, "read_json", return_value=aux), \
                patch.object(births, "checked_owner", side_effect=owned), patch.object(births, "request") as close, \
                self.assertRaisesRegex(RuntimeError, "^named auxiliary validation failed$"):
            births.stale_shell(subject, {}, "owner.json", {"socket_name": "reader-aux", "session": "aux"}, "/owned/proof", {})
        self.assertEqual(["move", "authorize-aux", "validate-aux", "retire-aux", "restore-move"], events)
        self.assertEqual([], active)
        self.assertEqual({"backend": "herdr", "pane": "restored"}, subject)
        close.assert_called_once_with("/owned/herdr", "pane.close", {"pane_id": "restored"})

    def test_picker_birth_uses_public_staging_capture_and_actual_end_before_expectation(self):
        target = {"backend": "herdr", "pane": "caller"}
        resources = {"birth_branch": "linux+herdr-direct+project+claude", "birth_origin": "reader-c83",
                     "project": "atlas", "workspace": "actual-atlas", "fixture": "/owned/seed.json"}
        child = {"session_id": "native-session", "engine": "claude", "terminal": {"backend": "herdr", "pane": "caller"}}
        original = {"pane": {"terminal_id": "physical"}, "identity": {"tab": "hand-tab"}}
        display = {"pane": {"terminal_id": "physical"}, "identity": {"tab": "hand-tab"}, "tab": {"tab": {"label": "Reader hand label"}}}
        departing = {"pid": 20, "start": "native-birth"}
        evidence = {"checks": []}
        before_end = {"actual": "before end"}
        def reader(*args):
            evidence["during"] = {"actual": "during"}
        def verify(observed, checks, prefix):
            for row in checks:
                self.assertEqual(row["expected"], row["actual"], row["field"])
        with patch.object(births.chat, "stage_role", return_value={"scenario": "/owned/scenario.json"}) as staged, \
                patch.object(births.chat, "before", return_value={"target": target, "seats": ["prior"]}) as before, \
                patch.object(births.chat, "pick_new", return_value={"account": "1"}) as picker, \
                patch.object(births.chat, "capture", return_value=child) as capture, \
                patch.object(births, "wait", side_effect=lambda label, predicate, bound: predicate()), \
                patch.object(births, "terminal", return_value=display), patch.object(shells, "require", side_effect=verify), \
                patch.object(shells, "reader", side_effect=reader), patch.object(births, "engine_birth", return_value=departing), \
                patch.object(births, "process_snapshot", return_value={20: {"start": "native-birth"}}), \
                patch.object(births, "bundle", return_value=before_end), \
                patch.object(births, "action", return_value={"exit_code": 0}) as ended, \
                patch.object(births.faults, "birth_exited", return_value=True) as exited, \
                patch.object(births, "expect_ended") as retained:
            births.picker_birth("user-tab-exec", target, original, resources, {}, "/owned/owner.json", "/owned/proof", evidence)
        staged.assert_called_once_with(target, resources["birth_branch"], "reader-c83", "/owned/seed.json", "atlas", "actual-atlas")
        before.assert_called_once_with(target)
        picker.assert_called_once_with(target, "atlas", "claude", "1", "1h")
        capture.assert_called_once_with(target, resources["birth_branch"], "reader-c83", {"target": target, "seats": ["prior"]}, "atlas", "actual-atlas")
        ended.assert_called_once_with({}, ["pfm", "chat", "end", "native-session"], "/owned/proof.end-child")
        exited.assert_called_once_with(departing)
        retained.assert_called_once_with(resources["birth_branch"], "reader-c83", before_end)
        self.assertEqual(child, evidence["child"])

    def test_inherited_tmux_shell_requires_old_environment_new_terminal_location_and_no_foreground(self):
        original = {"identity": {"pane": "birth-pane"}, "pane": {"terminal_id": "physical-terminal"}}
        moved = {"identity": {"pane": "moved-pane"}, "pane": {"terminal_id": "physical-terminal"},
                 "processes": {"foreground_processes": [{"pid": 10, "name": "zsh"}]}}
        target = {"socket": "/owned/tmux", "shell_pid": 20}
        environment = {"HERDR_PANE_ID": "birth-pane", "TMUX": "/owned/tmux,30,0"}
        cases = [(None, None, []), ("HERDR_PANE_ID", "moved-pane", ["tmux-shell-original-herdr-environment", "tmux-shell-stale-pane"]),
                 ("HERDR_PANE_ID", "unrelated", ["tmux-shell-original-herdr-environment"]),
                 ("TMUX", "/owned/other,30,0", ["tmux-shell-native-socket"])]
        for key, value, broken in cases:
            with self.subTest(key=key, value=value):
                actual = births.inherited_checks(original, moved, target, environment if key is None else {**environment, key: value})
                self.assertEqual(broken, [row["field"] for row in actual if row["expected"] != row["actual"]])
        replaced = copy.deepcopy(moved)
        replaced["pane"]["terminal_id"] = "replacement-terminal"
        replaced["processes"]["foreground_processes"] = [{"pid": 20, "name": "zsh"}]
        self.assertEqual(["tmux-shell-moved-physical-terminal", "tmux-shell-not-foreground"],
                         [row["field"] for row in births.inherited_checks(original, replaced, target, environment) if row["expected"] != row["actual"]])

    def test_picker_birth_location_and_hand_label_have_independent_literal_oracles(self):
        original = {"pane": {"terminal_id": "physical"}, "identity": {"tab": "hand-tab"}}
        child = {"engine": "claude", "terminal": {"backend": "herdr"}}
        display = {"pane": {"terminal_id": "physical"}, "identity": {"tab": "hand-tab"},
                   "tab": {"tab": {"label": "Reader hand label"}}}
        cases = [("user-tab-exec", display, []),
                 ("stale-tmux-shell", {**display, "pane": {"terminal_id": "new-child-terminal"}}, []),
                 ("stale-tmux-shell", display, ["picker-born-in-caller-terminal"]),
                 ("user-tab-exec", {**display, "pane": {"terminal_id": "new-child-terminal"}}, ["picker-born-in-caller-terminal"]),
                 ("user-tab-exec", {**display, "tab": {"tab": {"label": "atlas"}}}, ["picker-keeps-hand-label"]),
                 ("user-tab-exec", {**display, "identity": {"tab": "other-tab"}}, ["picker-keeps-hand-labelled-tab"])]
        for name, observed, broken in cases:
            with self.subTest(name=name, observed=observed):
                checks = births.birth_checks(name, original, child, observed)
                self.assertEqual(broken, [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_actual_parent_prelude_uses_the_existing_native_constructor_once(self):
        with tempfile.TemporaryDirectory() as folder:
            target = {"backend": "tmux", "socket": folder + "/actual", "pane": "actual-session:0.0"}
            output = Path(folder) / "target.json"
            with patch.object(births.faults, "owned_path") as owned, patch.object(births, "tmux_server", return_value=target) as native:
                births.client_prelude("reader-stale", "reader-stale", str(output))
            native.assert_called_once_with("reader-stale", "reader-stale")
            owned.assert_called_once_with(output.parent)
            self.assertEqual(target, read_json(output))

    def test_birth_descriptors_name_only_the_required_public_setup_resources(self):
        common = ["subject", "birth_branch", "birth_origin", "project", "workspace", "fixture"]
        for name, extra in (("stale-tmux-shell", ["socket_name", "session", "placeholder", "destination_workspace"]),
                            ("user-tab-exec", ["id", "old_chat"])):
            with self.subTest(name=name):
                self.assertEqual({"plan": name, "entry": "shell-birth", "engine": "claude", "resources": common + extra,
                                  "argv": ["pfm", "ls", "--all", "--tsv"]}, births.describe(name))
        with self.assertRaisesRegex(ValueError, "^NATIVE-SHELL-BIRTH-PLAN: other$"):
            births.describe("other")


if __name__ == "__main__":
    unittest.main()
