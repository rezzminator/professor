#!/usr/bin/env python3
"""Pin actual hook selection and the early native rename evidence contract."""
import copy
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
import early


class EarlyNativeRename(unittest.TestCase):
    def test_account_settings_preserve_actual_registration_command_and_other_handlers(self):
        command = "/fixture/bin/pfm internal herdr-state --engine claude start"
        actual = {"type": "command", "command": command, "timeout": 30}
        other = {"type": "command", "command": "printf unchanged"}
        rendered = {"hooks": {"SessionStart": [{"hooks": [other, actual]}]}}
        for hooks, expected in (
                ({"SessionStart": [{"matcher": "resume", "hooks": [copy.deepcopy(actual), other]}]},
                 {"SessionStart": [{"matcher": "resume", "hooks": [{**actual, "async": True}, other]}]}),
                ({"Stop": [{"hooks": [other]}]},
                 {"Stop": [{"hooks": [other]}], "SessionStart": [{"hooks": [{**actual, "async": True}]}]})):
            original = {"env": {"KEEP": "yes"}, "hooks": hooks}
            with self.subTest(hooks=hooks):
                updated, selected = early.registration_settings(json.dumps(original),
                    ["claude", "--settings", json.dumps(rendered)])
                self.assertEqual(command, selected)
                self.assertEqual({"env": {"KEEP": "yes"}, "hooks": expected}, json.loads(updated))
        with self.assertRaisesRegex(ValueError, "^BIRTH-EARLY-LAUNCH-SETTINGS: actual rendered settings required$"):
            early.registration_settings("{}", ["claude", "--resume", "actual-session"])
        rendered["hooks"]["SessionStart"][0]["hooks"] = [other]
        with self.assertRaisesRegex(ValueError, "^BIRTH-EARLY-REGISTRATION: exactly one actual native registration command required$"):
            early.registration_settings("{}", ["claude", "--settings", json.dumps(rendered)])

    def test_registration_receipts_require_completed_success_with_same_native_input(self):
        payload = {"hook_event_name": "SessionStart", "source": "resume", "session_id": "native-session"}
        receipts = [{"phase": "entered", "exit_code": -1, "error": "", "input": payload},
                    {"phase": "completed", "exit_code": 0, "error": "", "input": payload}]
        self.assertEqual(True, early.registration_completed(receipts))
        self.assertEqual(False, early.registration_completed(receipts[:1]))
        self.assertEqual(False, early.registration_completed(receipts + receipts[:1]))
        for index, key, wrong in ((0, "phase", "completed"), (0, "exit_code", 0),
                                  (1, "phase", "cancelled"), (1, "exit_code", 1),
                                  (1, "error", "hook failed"), (1, "input", {"session_id": "foreign"})):
            changed = copy.deepcopy(receipts)
            changed[index][key] = wrong
            with self.subTest(index=index, key=key, wrong=wrong):
                self.assertEqual(False, early.registration_completed(changed))

    def test_projection_rejects_wrong_native_identity_timing_and_label(self):
        evidence = {
            "initial": {"session_id": "native-session"},
            "resumed": {"session_id": "native-session", "name": "atlas-early-native"},
            "initial_birth": {"pid": 10, "start": 100},
            "initial_native": {"pid": 10, "start": 100},
            "resumed_birth": {"pid": 11, "start": 200, "terminal": "actual-terminal",
                              "argv": ["claude", "--resume", "native-session", "--settings", "{}"]},
            "after_registration": {"birth": {"pid": 11, "start": 200, "terminal": "actual-terminal"}},
            "resumed_native": {"pid": 11, "start": 200, "parent_pid": 9},
            "resumed_process": {"parent": 9}, "resumed_terminal": {"identity": "actual-terminal"},
            "hook_payload": {"session_id": "native-session", "hook_event_name": "SessionStart", "source": "resume"},
            "prior_native_name": "atlas-early-prior", "renamed_native_name": "atlas-early-native",
            "held_before_rename": True, "held_after_rename": True,
            "registration_completed": True, "settings_restored": True,
            "carried_origins_preserved": True}
        expected = {"name": "atlas-early-native", "prior_native_name": "atlas-early-prior",
                    "same_native_session": True, "new_native_birth": True,
                    "native_birth_bound": True, "actual_resume_argv": True,
                    "no_name_argument": True, "registration_held_during_rename": True,
                    "native_rename_recorded": True, "registration_completed": True,
                    "settings_restored": True, "carried_origins_preserved": True}
        self.assertEqual(expected, early.projection(evidence))
        changes = [("resumed", "name", "atlas-early-prior", "name", "atlas-early-prior"),
                   ("hook_payload", "session_id", "foreign", "same_native_session", False),
                   ("hook_payload", "source", "startup", "same_native_session", False),
                   ("hook_payload", "hook_event_name", "SessionEnd", "same_native_session", False),
                   ("resumed_native", "start", 0, "native_birth_bound", False),
                   ("resumed_native", "parent_pid", 99, "native_birth_bound", False),
                   ("resumed_birth", "terminal", "foreign", "native_birth_bound", False),
                   ("after_registration", "birth", {"pid": 12, "start": 201, "terminal": "actual-terminal"}, "native_birth_bound", False),
                   ("resumed_birth", "argv", ["claude", "--resume", "native-session"], "actual_resume_argv", False),
                   ("resumed_birth", "argv", ["claude", "--resume", "wrong"], "actual_resume_argv", False),
                   ("resumed_birth", "argv", ["claude", "--resume", "native-session", "--fork-session"], "actual_resume_argv", False),
                   ("resumed_birth", "argv", ["claude", "--name", "old"], "no_name_argument", False),
                   ("resumed_birth", "argv", ["claude", "--name=old"], "no_name_argument", False),
                   (None, "held_before_rename", False, "registration_held_during_rename", False),
                   (None, "held_after_rename", False, "registration_held_during_rename", False),
                   (None, "renamed_native_name", "old", "native_rename_recorded", False),
                   (None, "registration_completed", False, "registration_completed", False),
                   (None, "settings_restored", False, "settings_restored", False),
                   (None, "carried_origins_preserved", False, "carried_origins_preserved", False)]
        for section, key, wrong, field, wanted in changes:
            changed = copy.deepcopy(evidence)
            (changed if section is None else changed[section])[key] = wrong
            with self.subTest(section=section, key=key, wrong=wrong):
                self.assertEqual(wanted, early.projection(changed)[field])
        same = copy.deepcopy(evidence)
        same["initial_birth"] = {"pid": 11, "start": 200}
        self.assertEqual(False, early.projection(same)["new_native_birth"])


if __name__ == "__main__":
    unittest.main()
