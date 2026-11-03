#!/usr/bin/env python3
"""Native actor completion follows actual child and scoped restoration."""
from contextlib import contextmanager
from pathlib import Path
import tempfile
import sys
import copy
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import native_actor as native
from runtime import command, read_json, write_json


class NativeActorTests(unittest.TestCase):
    def test_generated_environment_comes_from_the_native_unset_selector_receipt(self):
        good = {"argv": ["/usr/lib/systemd/user-environment-generators/30-systemd-environment-d-generator"],
                "unset": ["CLAUDE_CONFIG_DIR", "PFM_CLAUDE_CONFIG_DIR_DEFAULT"], "status": 0, "stderr": "",
                "stdout": "PATH=/bin\nCLAUDE_CONFIG_DIR='/owned/account one'\nPFM_CLAUDE_CONFIG_DIR_DEFAULT='/owned/account one'\n"}
        expected = {"CLAUDE_CONFIG_DIR": "/owned/account one", "PFM_CLAUDE_CONFIG_DIR_DEFAULT": "/owned/account one"}
        self.assertEqual(expected, native.generated_environment(good))
        changes = [("status", 1, "RECEIPT"), ("status", False, "RECEIPT"), ("unset", [], "RECEIPT"),
                   ("stderr", "failed input read", "RECEIPT"), ("argv", ["/owned/fake"], "RECEIPT"),
                   ("stdout", "CLAUDE_CONFIG_DIR=/owned/one\n", "DEFAULT"),
                   ("stdout", "CLAUDE_CONFIG_DIR=/owned/one\nPFM_CLAUDE_CONFIG_DIR_DEFAULT=/owned/two\n", "DEFAULT"),
                   ("stdout", good["stdout"] + "CLAUDE_CONFIG_DIR=/owned/replaced\n", "ASSIGNMENT")]
        for field, value, error in changes:
            with self.subTest(field=field, value=value), self.assertRaisesRegex(ValueError, "^NATIVE-ENVIRONMENT-D-" + error + ":"):
                native.generated_environment({**good, field: value})
        with tempfile.TemporaryDirectory() as folder:
            receipt = Path(folder) / "environment.json"
            write_json(receipt, good)
            with patch.object(native.faults, "owned_path"):
                self.assertEqual(expected, native.declared_environment({"environment_receipt": str(receipt), "generated_environment": expected}))
                with self.assertRaisesRegex(ValueError, "^NATIVE-ENVIRONMENT-D-CHANGED:"):
                    native.declared_environment({"environment_receipt": str(receipt), "generated_environment": {}})

    def test_login_absence_is_specific_to_the_actual_native_id(self):
        for rows, actual in (([{"id": "carried"}], []), ([{"id": "carried"}, {"id": "native"}], ["native"])):
            with self.subTest(rows=rows):
                self.assertEqual({"field": "login-default-is-not-agent", "expected": [], "actual": actual}, native.login_absence(rows, "native"))

    def test_before_prompt_row_is_the_actual_native_account_and_identity(self):
        expected = [{"name": "(no prompt)", "account": "2", "kind": "agent"}]
        cases = [([{ "id": "native", **expected[0]}, {"id": "peer", "name": "other", "account": "1", "kind": "agent"}], expected),
                 ([{"id": "peer", **expected[0]}], []),
                 ([{"id": "native", **expected[0], "account": "1"}], [{"name": "(no prompt)", "account": "1", "kind": "agent"}])]
        for rows, actual in cases:
            with self.subTest(rows=rows):
                self.assertEqual({"field": "before-first-prompt-account", "expected": expected, "actual": actual}, native.before_prompt_row(rows, "native"))

    def test_generated_pair_is_proven_in_both_native_processes(self):
        expected = {"CLAUDE_CONFIG_DIR": "/owned/one", "PFM_CLAUDE_CONFIG_DIR_DEFAULT": "/owned/one"}
        for child, parents, broken in ((expected, [expected], []), ({}, [expected], ["native-generated-child-environment"]),
                                       (expected, [{}], ["native-generated-parent-environment"]),
                                       (expected, [], ["native-generated-parent-environment"])):
            with self.subTest(broken=broken, parents=parents):
                checks = native.generated_checks(expected, child, parents)
                self.assertEqual(broken, [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_native_binary_refusal_preserves_the_old_codex_error(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "not-executable"
            path.write_text("fixture input is no executable")
            setup = {key: folder for key in ("scenario", "records", "cwd")}
            setup.update(native_binary=str(path))
            setup_file = Path(folder) / "setup.json"
            write_json(setup_file, setup)
            for engine, label in (("codex", "CODEX"), ("claude", "ACTOR")):
                with self.subTest(engine=engine), patch.object(native.faults, "owned_path"), \
                        self.assertRaisesRegex(ValueError, "^NATIVE-" + label + "-BINARY: executable required$"):
                    native.wrapper(setup_file, engine)

    def test_native_preflight_retains_each_independent_refusal(self):
        good = {"setup": {"session_id": "native-session"}, "birth": {"pid": 20, "parent_pid": 10},
                "observed": {"births": [{"pid": 20, "environment": {"PFM_RUN": "owned"}}]},
                "child": {"parent": 10}, "recorded_argv": "claude\n--session-id\nnative-session\n", "seats": [], "launches": []}
        cases = [(None, None, []), ("recorded_argv", "codex\n", ["native-actor-argv"]),
                 ("observed", {"births": [{"pid": 99, "environment": {}}]}, ["native-actor-owned-child"]),
                 ("child", {"parent": 99}, ["native-actor-parent"]), ("seats", [{"seat_id": "existing"}], ["native-actor-no-seat"]),
                 ("launches", [{"session_id": "existing"}], ["native-actor-no-launch"]),
                 ("observed", {"births": [{"pid": 20, "environment": {"TMUX_PANE": "%1"}}]}, ["native-actor-multiplexer"])]
        for field, value, expected in cases:
            with self.subTest(field=field, value=value):
                actual = copy.deepcopy(good)
                if field:
                    actual[field] = value
                self.assertEqual(expected, [row["field"] for row in native.preflight(**actual) if row["expected"] != row["actual"]])

    def test_held_native_read_never_aliases_a_ready_or_existing_transcript(self):
        resources = {"ready_hold": "/owned/gate", "config_dir": "/owned/account2"}
        cases = [({"ready_hold": "/owned/gate"}, {"CLAUDE_CONFIG_DIR": "/owned/account2"}, [], False, []),
                 ({"ready_hold": "/owned/other"}, {"CLAUDE_CONFIG_DIR": "/owned/account2"}, [], False, ["native-ready-gate"]),
                 ({"ready_hold": "/owned/gate"}, {"CLAUDE_CONFIG_DIR": "/owned/account1"}, [], False, ["native-account-config-dir"]),
                 ({"ready_hold": "/owned/gate"}, {"CLAUDE_CONFIG_DIR": "/owned/account2"}, ["/owned/native.jsonl"], True,
                  ["native-transcript-before-first-prompt", "native-ready-gate-absent"])]
        for scenario, environment, transcripts, exists, expected in cases:
            with self.subTest(expected=expected):
                rows = native.held_checks(resources, environment, scenario, transcripts, exists)
                self.assertEqual(expected, [row["field"] for row in rows if row["expected"] != row["actual"]])

    def test_native_arguments_are_declared_inputs_not_a_carried_identity(self):
        cases = [("codex", {"session_id": "unused"}, []),
                 ("claude", {"session_id": "native-session"}, ["--session-id", "native-session"]),
                 ("claude", {"session_id": "native-session", "hook_settings": "/owned/hooks.json"},
                  ["--session-id", "native-session", "--settings", "/owned/hooks.json"])]
        for engine, setup, expected in cases:
            with self.subTest(engine=engine, setup=setup):
                self.assertEqual(expected, native.arguments(setup, engine))

    def test_native_subject_requires_independent_pty_and_owned_births(self):
        for backend in ("pty", "herdr", "tmux"):
            target = {"backend": backend, "pane": "native"}
            with self.subTest(backend=backend), patch.object(native, "checked_owner", return_value={"identity": "actual"}) as owned:
                if backend == "pty":
                    self.assertEqual({"identity": "actual"}, native.subject_owner(target))
                    owned.assert_called_once_with(target)
                else:
                    with self.assertRaisesRegex(ValueError, "^NATIVE-ACTOR-TRANSPORT:"):
                        native.subject_owner(target)
                    owned.assert_not_called()

    def test_registered_usage_hook_retains_real_status_and_config_diagnostic(self):
        settings = native.usage_settings("/owned/broken.json", "/owned/hook")
        self.assertEqual({"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command":
            'pfm --config /owned/broken.json usage-hook >/owned/hook.out 2>/owned/hook.err; result=$?; printf "%s\\n" "$result" >/owned/hook.status; exit "$result"'}]}]}}, settings)
        with tempfile.TemporaryDirectory() as folder:
            prefix = str(Path(folder) / "hook")
            wanted = "pfm: config: parse config /owned/broken.json at byte 1: unexpected EOF; running on defaults until it is fixed (pfm doctor)"
            for status, diagnostic in (("0\n", wanted), ("1\n", wanted), ("0\n", "different failure")):
                with self.subTest(status=status, diagnostic=diagnostic):
                    Path(prefix + ".status").write_text(status)
                    Path(prefix + ".err").write_text(diagnostic + "\n")
                    self.assertEqual([{"field": "native-usage-hook-exit", "expected": "0\n", "actual": status},
                                      {"field": "native-usage-hook-config-warning", "expected": wanted, "actual": diagnostic}],
                                     native.usage_checks({"hook_prefix": prefix, "broken_config": "/owned/broken.json"}))

    def test_actual_native_wrapper_clis_dispatch_setup_refusal(self):
        cases = [("native_denied.py", "denial-wrapper", "NATIVE-DENIED-ERROR: 'scenario'\n"),
                 ("native_actor.py", "native-wrapper", "NATIVE-ACTOR-ERROR: 'scenario'\n")]
        for script, verb, error in cases:
            with self.subTest(script=script), tempfile.TemporaryDirectory() as folder:
                setup = Path(folder) / "setup.json"
                write_json(setup, {})
                capture = {}
                output = command([sys.executable, str(Path(__file__).with_name(script)), verb, str(setup)], allowed_codes=(0, 2), capture=capture)
                self.assertEqual({"stdout": "", "stderr": error, "status": 2},
                                 {key: capture[key] for key in ("stdout", "stderr", "status")})
                self.assertEqual("", output)

    def test_wrapper_always_restores_fault_before_completed_receipt(self):
        for engine, status in (("codex", 0), ("codex", 7), ("codex", None), ("claude", 0)):
            with self.subTest(engine=engine, status=status), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                setup = {key: str(root) for key in ("scenario", "records", "cwd", "codex_home")}
                alias = root / "claude"
                alias.symlink_to("/usr/bin/true")
                setup.update(native_binary=str(alias), prefix=str(root / "native"), session_id="native-session", config_dir=str(root))
                setup_file = root / "setup.json"
                write_json(setup_file, setup)
                events = []
                @contextmanager
                def fault(prefix):
                    events.append("fault-entered")
                    try:
                        yield {}
                    finally:
                        events.append({"event": "fault-restored", "completed_receipt": Path(setup["prefix"] + ".wrapper-exit.json").exists()})
                @contextmanager
                def child(*args, **options):
                    expected = ["/usr/bin/true"] if engine == "codex" else [str(alias), "--session-id", "native-session"]
                    self.assertEqual(expected, args[0])
                    events.append("native-child-entered")
                    def wait():
                        if status is None:
                            raise RuntimeError("native child interrupted")
                        return status
                    yield SimpleNamespace(wait=wait)
                with patch.object(native.faults, "owned_path"), patch.object(native, "start", side_effect=child):
                    if status is None:
                        with self.assertRaisesRegex(RuntimeError, "^native child interrupted$"):
                            native.wrapper(setup_file, engine, fault)
                    else:
                        native.wrapper(setup_file, engine, fault)
                self.assertEqual(["fault-entered", "native-child-entered", {"event": "fault-restored", "completed_receipt": False}], events)
                self.assertEqual({"exit_code": 2 if status is None else status}, read_json(setup["prefix"] + ".wrapper-exit.json"))


if __name__ == "__main__":
    unittest.main()
