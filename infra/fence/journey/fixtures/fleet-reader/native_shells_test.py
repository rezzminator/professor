#!/usr/bin/env python3
"""Shell transitions retain independent kernel births, titles, and input."""
from pathlib import Path
from contextlib import contextmanager
import tempfile
import unittest
from unittest.mock import patch

import native_shells as shells


class NativeShellTests(unittest.TestCase):
    def test_fresh_authorization_covers_validation_dispatch_and_error_cleanup(self):
        subject = {"backend": "herdr", "pane": "fresh"}
        owner = {"backend": "tmux", "socket": "/owned/owner", "pane": "%0"}
        declared = {"owner": owner, "foreign": {"terminal": {"backend": "herdr", "pane": "foreign"}}, "chats": []}
        for name in ("nested-shell-release", "moved-replaced-client", "user-tab-exec", "stale-tmux-shell"):
            for failed in (False, True):
                events, active = [], []
                @contextmanager
                def authorized(target):
                    self.assertEqual(subject, target)
                    events.append("authorize")
                    active.append(target)
                    try:
                        yield target
                    finally:
                        active.pop()
                        events.append("retire")
                def owned(target):
                    if target == subject:
                        self.assertEqual([subject], active)
                        events.append("validate")
                    return {}
                def transition(*args):
                    self.assertEqual([subject], active)
                    events.append("dispatch")
                    if failed:
                        raise RuntimeError("named native transition failed")
                    return {"result": "birth"}
                def close(*args):
                    self.assertEqual([subject], active)
                    events.append("close")
                birth = name in {value[0] for value in shells.native_shell_births.PLANS.values()}
                with self.subTest(name=name, failed=failed), patch.dict(shells.os.environ, {"HERDR_SOCKET_PATH": "/owned/herdr"}), \
                        patch.object(shells.faults, "fresh_target", side_effect=authorized), patch.object(shells.faults, "owned_path"), \
                        patch.object(shells, "read_json", return_value=owner), patch.object(shells, "checked_owner", side_effect=owned), \
                        patch.object(shells, "carry", return_value=declared), patch.object(shells, "shell_ready", return_value=True), \
                        patch.object(shells, "bundle", return_value={}), patch.object(shells, "bundle_checks", return_value=[]), \
                        patch.object(shells, "require"), patch.object(shells, "nested_transition", side_effect=transition), \
                        patch.object(shells, "replaced_client", side_effect=transition), \
                        patch.object(shells.native_shell_births, "run", side_effect=transition), patch.object(shells, "request", side_effect=close):
                    if failed:
                        with self.assertRaisesRegex(RuntimeError, "^named native transition failed$"):
                            shells.run(name, "unused-chat", "owner.json", {"subject": subject}, "/owned/proof")
                    else:
                        result = shells.run(name, "unused-chat", "owner.json", {"subject": subject}, "/owned/proof")
                        self.assertEqual({"result": "birth"} if birth else name, result if birth else result["plan"])
                self.assertEqual(["authorize", "dispatch", "retire"] if birth else
                                 ["authorize", "validate", "dispatch", "close", "retire"], events)
                self.assertEqual([], active)

    def test_replacement_oracle_keeps_the_exact_native_session_title_and_terminal(self):
        chat = {"session_id": "actual-replacement", "name": "Replacement chat"}
        pane = {"agent_session": {"source": "pfm", "agent": "claude", "kind": "id", "value": "actual-replacement"},
                "title": "Replacement chat", "terminal_id": "actual-terminal"}
        cases = [(None, None, []), ("title", "Reader stale client", ["replacement-client-title"]),
                 ("terminal_id", "other-terminal", ["replacement-client-physical-terminal"]),
                 ("agent_session", {"source": "pfm", "agent": "claude", "kind": "id", "value": "old-session"}, ["replacement-client-native-session"])]
        for field, value, expected in cases:
            with self.subTest(field=field):
                observed = {"pane": pane if field is None else {**pane, field: value}, "agent_status": {"pane": "idle"}}
                checks = shells.replacement_checks(chat, observed, "actual-terminal")
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])
        for status, broken in (({"session_id": "old-session", "name": "Reader stale client"}, []),
                               ({"session_id": "actual-replacement", "name": "Reader stale client"}, ["stale-name-native-session"]),
                               ({"session_id": "old-session", "name": "Replacement chat"}, ["stale-name-native-name"])):
            with self.subTest(status=status):
                checks = shells.renamed_checks({"session_id": "old-session"}, status, "Reader stale client", "stale-name")
                self.assertEqual(broken, [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_client_scope_resumes_only_the_same_suspended_birth_before_detach(self):
        subject = {"backend": "herdr", "pane": "actual"}
        target = {"backend": "tmux", "socket": "/owned/socket", "pane": "%0"}
        with patch.object(shells, "command", return_value="cc-reader\n") as observed:
            self.assertEqual("cc-reader", shells.client_session(target))
        observed.assert_called_once_with(["tmux", "-S", "/owned/socket", "display-message", "-p", "-t", "%0", "#{session_name}"])
        born = {"pid": 20, "start": "attached", "state": "S"}
        cases = [({**born, "state": "T"}, ["fg"], [["ctrl+b", "d"]]),
                 (born, [], [["ctrl+b", "d"]]), ({**born, "start": "reused"}, [], []), (None, [], [])]
        for current, expected_input, expected_keys in cases:
            with self.subTest(current=current), patch.object(shells, "clients", return_value=[]), \
                    patch.object(shells, "type_command") as typed, \
                    patch.object(shells, "wait", side_effect=lambda label, predicate, bound: ["20"] if label == "one real owner-attached tmux client" else True), \
                    patch.object(shells, "process_snapshot", side_effect=[{20: born}, {20: current} if current else {}]), \
                    patch.object(shells, "literal_input") as resumed, patch.object(shells, "keys") as keyed:
                evidence = {"inputs": []}
                with shells.attached_client(subject, target, "cc-reader", evidence) as actual:
                    self.assertEqual(born, actual)
                self.assertEqual(expected_input, [call.args[1] for call in resumed.call_args_list])
                self.assertEqual(expected_keys, [call.args[1] for call in keyed.call_args_list])
                typed.assert_called_once_with(subject, ["tmux", "-S", "/owned/socket", "attach-session", "-t", "cc-reader"])
                self.assertEqual([born], evidence["clients"])

    def test_shell_title_oracle_checks_command_exit_and_product_format(self):
        cases = [(": exit 0 · 0s", []), (": exit 0 · 12s", []),
                 ("other exit 0 · 0s", ["done-title"]), (": exit 1 · 0s", ["done-title"]),
                 (None, ["done-title", "done-duration"]),
                 (": exit 0 · -1s", ["done-title", "done-duration"])]
        for title, broken in cases:
            with self.subTest(title=title):
                actual = shells.title_checks("done", {"pane": {"title": title}}, ":")
                self.assertEqual(broken, [row["field"] for row in actual if row["expected"] != row["actual"]])
        self.assertEqual({"command": "zsh -i", "exit": 0, "duration": 7},
                         shells.title_fields({"pane": {"title": "zsh -i exit 0 · 7s"}}))

    def test_nested_exit_checks_same_birth_and_reused_pid_separately(self):
        parent, child = {"pid": 10, "start": "parent"}, {"pid": 20, "start": "child"}
        cases = [(child, parent, {}, []),
                 (child, parent, {20: {"start": "child"}}, ["nested-zsh-exited"]),
                 (child, parent, {20: {"start": "new-child"}}, []),
                 (parent, parent, {}, ["nested-zsh-new-birth"]),
                 (child, {"pid": 10, "start": "new-parent"}, {}, ["parent-zsh-foreground-restored"])]
        for nested, returned, processes, broken in cases:
            with self.subTest(nested=nested, returned=returned, processes=processes):
                checks = shells.nested_checks(parent, nested, returned, processes)
                self.assertEqual(broken, [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_kernel_shell_selection_requires_actual_foreground_and_parent(self):
        display = {"processes": {"foreground_processes": [{"pid": 20, "name": "zsh"}]}}
        rows = {20: {"pid": 20, "start": "child", "argv": "zsh -i", "parent": 10, "stat": "S+", "state": "S"},
                99: {"pid": 99, "start": "unrelated", "argv": "zsh -i", "parent": 10, "stat": "S", "state": "S"}}
        with patch.object(shells, "process_snapshot", return_value=rows):
            self.assertEqual({"pid": 20, "start": "child"}, shells.shell_birth(display, {"pid": 10}))
            self.assertIs(False, shells.shell_birth(display, {"pid": 99}))
            self.assertIs(False, shells.shell_birth({"processes": {"foreground_processes": []}}))
            self.assertIs(False, shells.shell_birth({"processes": {"foreground_processes": [{"pid": 20, "name": "zsh"}, {"pid": 99, "name": "zsh"}]}}))
            self.assertIs(False, shells.shell_birth({"processes": {"foreground_processes": [{"pid": 20, "name": "tmux"}]}}))

    def test_scratch_refuses_every_carried_terminal_and_other_transports(self):
        protected = [{"backend": "herdr", "pane": name} for name in ("foreign", "owner", "chat")]
        declared = {"foreign": {"terminal": protected[0]}, "owner": protected[1], "chats": [{"terminal": protected[2]}]}
        with patch.object(shells, "carry", return_value=declared), \
                patch.object(shells, "checked_owner", return_value={"actual": "observed"}) as checked, \
                patch.object(shells, "shell_ready", return_value=True):
            for subject in [*protected, *[{**row, "annotation": "unrelated extra field"} for row in protected], {"backend": "tmux", "pane": "scratch"}]:
                with self.subTest(subject=subject), self.assertRaisesRegex(ValueError, "^NATIVE-SHELL-SUBJECT:"):
                    shells.scratch(subject)
            checked.assert_not_called()
            self.assertEqual({"actual": "observed"}, shells.scratch({"backend": "herdr", "pane": "scratch"}))
        with patch.object(shells, "carry", return_value=declared), patch.object(shells, "checked_owner"), \
                patch.object(shells, "shell_ready", return_value=False), self.assertRaisesRegex(ValueError, "^NATIVE-SHELL-PROMPT:"):
            shells.scratch({"backend": "herdr", "pane": "scratch"})

    def test_input_receipt_retains_actual_literal_keys_and_target(self):
        target, evidence = {"backend": "herdr", "pane": "scratch"}, {"inputs": []}
        with patch.object(shells, "text") as text, patch.object(shells, "keys") as keys:
            shells.literal_input(target, "zsh -i", evidence)
        text.assert_called_once_with(target, "zsh -i")
        keys.assert_called_once_with(target, ["enter"])
        self.assertEqual({"inputs": [{"target": target, "text": "zsh -i", "keys": ["enter"]}]}, evidence)

    def test_poll_returns_the_same_observed_display_without_a_second_read(self):
        actual = {"pane": {"title": "zsh -i exit 0 · 1s"}}
        with patch.object(shells, "terminal", return_value=actual) as observe, \
                patch.object(shells, "wait", side_effect=lambda label, predicate, bound: predicate()):
            self.assertEqual(actual, shells.displayed({"backend": "herdr", "pane": "scratch"}, "named condition", lambda row: row == actual))
        observe.assert_called_once()

    def test_failed_assertion_keeps_exact_expected_actual_and_artifact(self):
        with tempfile.TemporaryDirectory() as folder:
            prefix = str(Path(folder) / "proof")
            evidence = {"checks": []}
            with self.assertRaisesRegex(ValueError, "NATIVE-PRECONDITION:.*expected.*zero.*actual.*one.*artifact="):
                shells.require(evidence, [{"field": "broken", "expected": "zero", "actual": "one"}], prefix)
            self.assertEqual([{ "field": "broken", "expected": "zero", "actual": "one"}], evidence["checks"])
            self.assertEqual(evidence, shells.read_json(prefix + ".evidence.json"))


if __name__ == "__main__":
    unittest.main()
