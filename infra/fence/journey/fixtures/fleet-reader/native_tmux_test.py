#!/usr/bin/env python3
"""Empty-server readers retain the repair message and every healthy row."""
from contextlib import contextmanager
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import native_tmux
from runtime import write_json


class NativeTmuxTests(unittest.TestCase):
    def test_unset_is_a_native_error_then_a_captured_prior_and_a_restored_error(self):
        phases = {"before": {"status": 1, "stdout": "", "stderr": "invalid option: @host_accent\n"},
                  "prior_before": {"status": 1, "stdout": "", "stderr": "invalid option: @pfm-theme-prior-@host_accent\n"},
                  "prior": {"status": 0, "stdout": "u:\n", "stderr": ""},
                  "after": {"status": 1, "stdout": "", "stderr": "invalid option: @host_accent\n"}}
        self.assertEqual([], [value["field"] for value in native_tmux.unset_checks(phases) if value["expected"] != value["actual"]])
        for phase, field in (("before", "native-host-accent-unset"), ("prior_before", "native-host-accent-prior-absent"),
                             ("prior", "native-host-accent-captured-unset"), ("after", "native-host-accent-restored-unset")):
            with self.subTest(phase=phase):
                actual = {**phases, phase: {"status": 0, "stdout": "incorrectly-guessed-value\n", "stderr": ""}}
                checks = native_tmux.unset_checks(actual)
                self.assertEqual([field], [value["field"] for value in checks if value["expected"] != value["actual"]])

    def test_theme_off_and_source_file_restoration_survive_an_owner_failure(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            owner = root / "owner.json"
            write_json(owner, {"backend": "pty", "socket": folder + "/owner", "pane": "actual"})
            events = []
            @contextmanager
            def preserved(path, **options):
                self.assertEqual({"retain_inode": True}, options)
                events.append("preserve:" + path)
                try:
                    yield
                finally:
                    events.append("restore:" + path)
            def owner_action(target, arguments, prefix):
                events.append(arguments)
                if arguments == ["pfm", "theme", "on", "--json"]:
                    raise RuntimeError("native owner failed")
                return {"exit_code": 0, "output": '{"enabled":false}'}
            values = {"tmux_socket": folder + "/cc-reader-unset", "account_settings": ["one", "two"]}
            with patch.object(native_tmux, "checked_owner"), patch.object(native_tmux.faults, "tmux_birth", return_value={"pid": 51, "start": "actual"}), \
                    patch.object(native_tmux.faults, "preserved_file", side_effect=preserved), patch.object(native_tmux, "option", return_value={}), \
                    patch.object(native_tmux, "global_options", return_value={values["tmux_socket"]: "status-style old\n"}), \
                    patch.object(native_tmux, "carry", return_value={"chats": []}), \
                    patch.object(native_tmux, "action", side_effect=owner_action):
                with self.assertRaisesRegex(RuntimeError, "^native owner failed$"):
                    native_tmux.run_unset(owner, values, root / "reader")
            self.assertEqual(["preserve:one", "preserve:two", ["pfm", "theme", "status", "--json"],
                              ["pfm", "theme", "on", "--json"], ["pfm", "theme", "off", "--json"],
                              "restore:two", "restore:one"], events)

    def test_generation_is_two_owned_bumps_and_every_peer_option_is_restored(self):
        before = {"generation": 10}
        options = {"cc-peer": "@operator-colour custom\nstatus-style original\n", "cc-subject": "status-style original\n"}
        required = ["cc-peer", "cc-subject"]
        cases = ((11, 12, options, options, []), (12, 12, options, options, ["theme-on-generation"]),
                 (11, 13, options, options, ["theme-off-generation"]),
                 (11, 12, {}, {}, ["native-theme-server-presence"]),
                 (11, 12, options, {**options, "cc-peer": "@operator-colour overwritten\nstatus-style themed\n"}, ["all-native-global-options-restored"]))
        for on, off, initial, after, expected in cases:
            with self.subTest(on=on, off=off, expected=expected):
                checks = native_tmux.restoration_checks(before, {"generation": on}, {"generation": off}, initial, after, required)
                self.assertEqual(expected, [value["field"] for value in checks if value["expected"] != value["actual"]])

    def test_empty_warning_is_exact_and_never_drops_other_diagnostics_or_rows(self):
        header = "kind\tid\tproject\tcwd\tname\taccount\tkilled\tsocket\n"
        row = "cc\tactual-session\tatlas\tfixture-cwd\tactual-name\t2\tfalse\tcc-owned\n"
        warning = "pfm: tmux probe warning: cc-reader-empty: abandoned empty tmux server — it runs with no session and no pane yet holds this pfm chat socket; reap it: pfm chat ls --reap\n"
        carried = {"chats": [{"key": "actor", "session_id": "actual-session", "name": "actual-name", "account": 2, "project": "atlas"}]}
        for output, failures in ((warning + header + row, []), ("different diagnostic\n" + header + row, ["empty-server-explicit-warning"]),
                                 (warning + "unexpected warning\n" + header + row, ["empty-server-explicit-warning"]),
                                 (warning + header, ["actor:text-row"])):
            with self.subTest(output=output):
                checks = native_tmux.receipt_checks(output, carried, "/synthetic/cc-reader-empty")
                self.assertEqual(failures, [value["field"] for value in checks if value["expected"] != value["actual"]])

    def test_owner_entry_and_observation_run_inside_the_reversible_empty_scope(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            owner_file, prefix = root / "owner.json", root / "read"
            write_json(owner_file, {"backend": "pty", "socket": folder + "/owner", "pane": "actual-pty"})
            inputs = {"tmux_socket": folder + "/cc-reader-empty", "session": "reader-empty"}
            birth = {"pid": 51, "start": "actual-server-birth"}
            events = []
            @contextmanager
            def emptied(socket, session, restore):
                self.assertEqual((inputs["tmux_socket"], "reader-empty"), (socket, session))
                events.append("empty")
                try:
                    yield {"birth": birth, "socket": socket, "list_sessions": "", "exit_empty": "off"}
                finally:
                    restore()
                    events.append("restored")
            def owner_action(owner, arguments, receipt):
                self.assertEqual(["empty"], events)
                self.assertEqual(["pfm", "ls", "--all", "--tsv"], arguments)
                self.assertEqual(str(prefix) + ".command", receipt)
                events.append("entry")
                return {"exit_code": 0, "output": "actual-public-table"}
            def observed(target):
                self.assertEqual(["empty", "entry"], events)
                self.assertEqual(owner_file, target)
                events.append("observation")
                return {"actual": "during-empty"}
            with patch.object(native_tmux, "checked_owner"), patch.object(native_tmux.faults, "empty_tmux", side_effect=emptied), \
                    patch.object(native_tmux.faults, "tmux_birth", return_value=birth), patch.object(native_tmux, "action", side_effect=owner_action), \
                    patch.object(native_tmux, "bundle", side_effect=observed), patch.object(native_tmux, "receipt_checks", return_value=[]), \
                    patch.object(native_tmux, "carry", return_value={"chats": []}), patch.object(native_tmux, "now_ms", side_effect=[100, 101]), \
                    patch.object(native_tmux, "tmux_server", side_effect=lambda name, session: events.append((name, session))):
                result = native_tmux.run(owner_file, inputs, prefix)
            self.assertEqual(["empty", "entry", "observation", ("cc-reader-empty", "reader-empty"), "restored"], events)
            self.assertEqual([], [value["field"] for value in result["checks"] if value["expected"] != value["actual"]])
            self.assertEqual({"actual": "during-empty"}, result["during"])


if __name__ == "__main__":
    unittest.main()
