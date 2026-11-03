#!/usr/bin/env python3
"""Quiet means observed native work/counters, followed by an untouched update."""
from contextlib import contextmanager, ExitStack
import errno
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import native_presence as presence
from runtime import read_json, write_json


class NativePresenceTests(unittest.TestCase):
    def scan(self, **changes):
        return {"run": "owned", "pid": 51, "op": "tmux.exec", "subcmd": "list-panes", "exit": 0, **changes}

    def shown(self, marker="↻"):
        return "◆ pfm \n› " + marker + " presence\n  session actual-session\n"

    def inputs(self):
        return {"subject": {"backend": "tmux", "socket": "/owned/cc-presence", "pane": "presence:0.0"},
                "picker": {"backend": "pty", "socket": "/owned/picker", "pane": "actual-picker"},
                "session_id": "actual-session", "name": "presence", "cwd": "/fixture/atlas", "socket_name": "cc-presence", "session": "presence"}

    def roster(self, kind="resume-claude"):
        return "kind\tid\tname\taccount\tcwd\tproject\tkilled\tsocket\n" + kind + "\tactual-session\tpresence\t2\t/fixture/atlas\tatlas\tfalse\t\n"

    def test_scans_credit_only_actual_picker_run_pid_operation_and_completed_status(self):
        good = self.scan()
        rows = [good, self.scan(pid=99), self.scan(run="foreign"), self.scan(op="runner.run"),
                self.scan(subcmd="capture-pane"), self.scan(exit=1)]
        self.assertEqual([good], presence.scans(rows, 51, "owned"))
        self.assertEqual([good, self.scan(exit=1)], presence.scans(rows, 51, "owned", successful=False))
        for invalid in (None, "0", False):
            with self.subTest(exit=invalid), self.assertRaisesRegex(ValueError, "^NATIVE-PRESENCE-SCAN-SHAPE:"):
                presence.scans([self.scan(exit=invalid)], 51, "owned")

    def test_picker_birth_requires_the_actual_action_credited_bare_public_entry(self):
        def born(pid, argv):
            return {"pid": pid, "start": "native", "chain": [{"argv": argv}]}
        cases = [([born(51, "/owned/pfm"), born(52, "/owned/pfm ls --all"), born(53, "/owned/herdr server")], {"pid": 51, "start": "native"}),
                 ([born(52, "/owned/pfm ls --all")], False),
                 ([born(51, "/owned/pfm"), born(54, "/other/pfm")], False)]
        for births, expected in cases:
            with self.subTest(expected=expected), patch.object(presence, "terminal"), \
                    patch.object(presence, "terminal_births", return_value=births), patch.object(presence.faults, "checked_birth") as checked:
                self.assertEqual(expected, presence.picker_birth(self.inputs()["picker"]))
                self.assertEqual(1 if expected else 0, checked.call_count)

    def test_quiet_witness_binds_birth_native_state_view_time_and_two_percent_cpu_boundary(self):
        birth = {"pid": 51, "start": "native"}
        current = {**birth, "state": "S"}
        cases = [(current, .239, 12000, "", []), (current, .24, 12000, "", []),
                 (current, .241, 12000, "", ["quiet-picker-own-cpu-fraction"]),
                 (current, .01, 11999, "", ["quiet-picker-observed-interval"]),
                 (current, .01, 12001, "", []),
                 ({**current, "start": "reused"}, .01, 12000, "", ["quiet-picker-native-birth"]),
                 ({**current, "state": "T"}, .01, 12000, "", ["quiet-picker-native-state"]),
                 ({**current, "state": "Z"}, .01, 12000, "", ["quiet-picker-native-state"])]
        cases += [(current, .01, 12000, marker, ["quiet-picker-view-failures"]) for marker in ("⟳ refreshing", "fleet stale", "⚠ row facts")]
        for process, cpu, elapsed, screen, expected in cases:
            with self.subTest(expected=expected, cpu=cpu):
                checks = presence.quiet_checks(birth, process, 0, cpu, elapsed, screen)
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])
        for metrics in ({"cpu_seconds": "n/a:process-permission"}, {"cpu_seconds": True}, {"cpu_seconds": -1}, {"cpu_seconds": float("nan")}):
            with self.subTest(metrics=metrics), self.assertRaisesRegex(ValueError, "^NATIVE-PRESENCE-CPU-UNAVAILABLE:"):
                presence.own_cpu(metrics)
        self.assertEqual(.5, presence.own_cpu({"cpu_seconds": .5}))
        for previous, current_cpu, elapsed in ((1, 0, 12000), (0, 0, 0)):
            with self.subTest(elapsed=elapsed), self.assertRaisesRegex(ValueError, "^NATIVE-PRESENCE-COUNTERS:"):
                presence.quiet_checks(birth, current, previous, current_cpu, elapsed, "")

    def test_row_and_selected_dossier_use_the_actual_native_identity_and_resume_marker(self):
        inputs = self.inputs()
        for kind, resumed, expected in (("live-claude", False, []), ("agent", False, []),
                                        ("resume-claude", True, []), ("live-claude", True, ["raw-native-row-kind"]),
                                        ("resume-claude", False, ["raw-native-row-kind"])):
            with self.subTest(kind=kind, resumed=resumed):
                rows = presence.text_rows(self.roster(kind))
                checks = presence.row_checks(rows, inputs, resumed=resumed)
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])
        rows = presence.text_rows(self.roster())
        for field in ("name", "account", "cwd", "project"):
            with self.subTest(field=field):
                checks = presence.row_checks([{**rows[0], field: "foreign"}], inputs, resumed=True)
                self.assertEqual(["raw-native-row-identity"], [row["field"] for row in checks if row["expected"] != row["actual"]])
        self.assertEqual({"session_id": "actual-session", "marker": "↻"}, presence.visible_identity(self.shown(), "actual-session"))
        self.assertEqual({"session_id": "actual-session", "marker": "●"}, presence.visible_identity(self.shown("●"), "actual-session"))
        expected = {"screen": self.shown(), "scans": [self.scan(exit=1)], "identity": {"session_id": "actual-session", "marker": "↻"}}
        self.assertEqual(expected, presence.automatic_update(self.shown(), "actual-session", [self.scan(exit=1)]))
        for shown, frames in ((self.shown(), []), (self.shown("●"), [self.scan()]), (self.shown() + "⟳ refreshing", [self.scan()])):
            with self.subTest(shown=shown, frames=frames):
                self.assertIs(False, presence.automatic_update(shown, "actual-session", frames))

    def test_quiet_sampling_selects_once_then_uses_only_public_reads(self):
        with tempfile.TemporaryDirectory() as folder:
            inputs, events = self.inputs(), []
            first, second = [self.scan()], [self.scan(), self.scan()]
            def selected(target, actor, identities, read, send):
                send(target, ["down"])
                events.append("initial-selection")
                return self.shown("●")
            with patch.dict(presence.os.environ, {"PFM_RUN": "owned", "JOURNEY_ACTIVITY": "native-root-log"}), \
                    patch.object(presence, "type_command", side_effect=lambda target, argv: events.append(argv)), \
                    patch.object(presence, "picker_birth", return_value={"pid": 51, "start": "native"}), \
                    patch.object(presence, "select_session", side_effect=selected), patch.object(presence, "keys") as keys, \
                    patch.object(presence, "report", return_value=self.roster("live-claude")), patch.object(presence, "screen", return_value=self.shown("●")), \
                    patch.object(presence.activity, "records", side_effect=[first, second, second]), \
                    patch.object(presence, "process_metrics", return_value={"cpu_seconds": .5}), \
                    patch.object(presence, "now_ms", side_effect=[1000, 13000]), \
                    patch.object(presence.faults, "checked_birth", return_value={"pid": 51, "start": "native", "state": "S"}):
                evidence = {"checks": []}
                result = presence.quiet_picker(inputs, Path(folder) / "beat", evidence, {})
            keys.assert_called_once_with(inputs["picker"], ["down"])
            self.assertEqual([["export", "PFM_LOG_LEVEL=debug"], ["pfm"], "initial-selection"], events)
            self.assertEqual({"pid": 51, "start": "native"}, result)
            self.assertEqual(12000, evidence["quiet"]["elapsed_ms"])
            self.assertEqual(second, evidence["quiet"]["records"])

    def test_dead_server_scope_contains_automatic_update_and_full_bundle_then_always_restores(self):
        for failed, bad_restore in ((False, False), (True, False), (False, "birth"), (False, "socket")):
            with self.subTest(failed=failed, bad_restore=bad_restore), tempfile.TemporaryDirectory() as folder:
                root, events, inputs = Path(folder), [], self.inputs()
                owner = {"backend": "pty", "socket": "/owned/owner", "pane": "owner"}
                write_json(root / "owner", owner)
                @contextmanager
                def fresh(target):
                    self.assertEqual(inputs["subject"], target)
                    events.append("fresh-enter")
                    try:
                        yield
                    finally:
                        events.append("fresh-exit")
                @contextmanager
                def dead(socket, restore):
                    events.append("dead-enter")
                    try:
                        yield {"pid": 61, "start": "server"}
                    finally:
                        restore()
                        events.append("dead-exit")
                def raw(*args):
                    events.append("raw-native")
                    return {"pid": 41, "start": "raw"}
                def quiet(values, prefix, evidence, start):
                    events.append("quiet-picker")
                    evidence["quiet"] = {"records": [self.scan(), self.scan()]}
                    return {"pid": 51, "start": "native"}
                def execute(*args):
                    events.append("public-list")
                    if failed:
                        raise RuntimeError("owner-read-failed")
                    return {"exit_code": 0, "output": self.roster()}
                def close(argv):
                    self.assertEqual([presence.sys.executable, str(presence.HERE / "terminal.py"), "close", str(root / "beat.picker.json")], argv)
                    self.assertEqual(inputs["picker"], read_json(argv[-1]))
                    events.append("close-picker")
                with ExitStack() as scopes:
                    for scope in (patch.dict(presence.os.environ, {"PFM_RUN": "owned", "JOURNEY_ACTIVITY": "native-root-log"}),
                            patch.object(presence, "checked_owner"), patch.object(presence, "carry", return_value={"chats": []}),
                            patch.object(presence.faults, "owned_path", side_effect=Path), patch.object(presence.faults, "fresh_target", side_effect=fresh),
                            patch.object(presence.faults, "dead_tmux", side_effect=dead), patch.object(presence.faults, "birth_exited", return_value=True),
                            patch.object(presence.faults, "tmux_birth", return_value={"pid": 61, "start": "server"} if bad_restore == "birth" else {"pid": 71, "start": "restored"}),
                            patch.object(presence.faults, "refusing_socket", return_value={"socket": inputs["subject"]["socket"], "errno": errno.ECONNREFUSED}),
                            patch.object(presence, "launch_raw", side_effect=raw), patch.object(presence, "quiet_picker", side_effect=quiet),
                            patch.object(presence.activity, "cursor", return_value={}), patch.object(presence.activity, "records", return_value=[self.scan(), self.scan(), self.scan()]),
                            patch.object(presence, "screen", return_value=self.shown()), patch.object(presence, "action", side_effect=execute),
                            patch.object(presence, "bundle", side_effect=lambda *args: events.append("full-bundle") or {}),
                            patch.object(presence, "bundle_checks", return_value=[]), patch.object(presence, "command", side_effect=close),
                            patch.object(presence, "tmux_server", side_effect=lambda *args: events.append("restore-server") or {**inputs["subject"], **({"socket": "wrong-socket"} if bad_restore == "socket" else {})})):
                        scopes.enter_context(scope)
                    invoke = lambda: presence.run("quiet-picker-dead-server", "unused-chat", root / "owner", inputs, root / "beat")
                    if failed:
                        with self.assertRaisesRegex(RuntimeError, "^owner-read-failed$"):
                            invoke()
                    elif bad_restore:
                        field = "presence-restored-server-birth" if bad_restore == "birth" else "presence-restored-auxiliary-socket"
                        with self.assertRaisesRegex(ValueError, "^NATIVE-PRECONDITION:.*" + field):
                            invoke()
                    else:
                        result = invoke()
                        self.assertEqual({"session_id": "actual-session", "marker": "↻"}, result["automatic_picker"]["identity"])
                during = [] if failed else ["full-bundle"]
                expected = ["full-bundle", "fresh-enter", "raw-native", "quiet-picker", "dead-enter", "public-list", *during,
                            "restore-server", "dead-exit"]
                expected.append("fresh-exit")
                self.assertEqual(expected + ["close-picker", "full-bundle"], events)


if __name__ == "__main__":
    unittest.main()
