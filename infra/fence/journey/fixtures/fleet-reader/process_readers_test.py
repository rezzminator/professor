#!/usr/bin/env python3
"""Literal projections reject early idle, unsafe preview and unrelated errors."""
import copy
from contextlib import contextmanager
from pathlib import Path
import runpy
from types import SimpleNamespace
import unittest
from unittest import mock

reader = runpy.run_path(str(Path(__file__).with_name("process_readers.py")))


class ProcessReaderTests(unittest.TestCase):
    def test_reap_ages_only_after_child_setup_and_restores_the_actual_auxiliary(self):
        order = []
        rows = {"apply": False, "killed": 0, "failed": 0, "unknown": [],
                "reap": [{"socket": "cc-reader-dead", "state": "dead", "reason": "stale socket file"}],
                "spared": [{"socket": "cc-busy", "state": "active", "reason": "transcript written within 1m0s"},
                           {"socket": "cc-reader-foreign", "state": "hosts", "reason": "hosts non-chat processes: fixture"}]}
        @contextmanager
        def background(chat, prefix, evidence):
            order.append("child-setup")
            evidence["child_birth"] = {"pid": 33, "start": "child"}
            yield
            order.append("child-finished")
        @contextmanager
        def dead(socket, restore):
            order.append("dead-enter")
            try:
                yield {"pid": 11, "start": "old"}
            finally:
                restore()
                order.append("dead-exit")
        @contextmanager
        def age(values, seconds, *, kind):
            self.assertEqual((["/synthetic/cc-reader-dead"], 3601, "socket"), (values, seconds, kind))
            order.append("age-enter")
            yield
            order.append("age-exit")
        def entry(owner, arguments, prefix):
            self.assertEqual(["pfm", "reap", "--json"], arguments)
            order.append("preview-first" if prefix.endswith("first") else "preview-second")
            return {"exit_code": 0, "output": reader["json"].dumps(rows)}
        def born(socket):
            if socket.endswith("dead"):
                order.append("restored-birth")
                return {"pid": 12, "start": "new"}
            order.append("foreign-birth")
            return {"pid": 22, "start": "foreign"}
        def restore(alias, session):
            self.assertEqual(("cc-reader-dead", "reader-dead"), (alias, session))
            order.append("restore-factory")
        resources = {"dead_socket": "/synthetic/cc-reader-dead", "foreign_socket": "/synthetic/cc-reader-foreign"}
        evidence = {"checks": [], "receipts": []}
        replacements = {"background": background, "action": entry, "tmux_server": restore,
                        "process_snapshot": lambda: {33: {"start": "child"}}, "now_ms": lambda: 3_601_001}
        with mock.patch.dict(reader["reap"].__globals__, replacements), \
             mock.patch.multiple(reader["faults"], owned_path=lambda value: Path(value), dead_tmux=dead, aged_files=age, tmux_birth=born), \
             mock.patch.object(Path, "is_socket", return_value=True), \
             mock.patch.object(Path, "stat", return_value=SimpleNamespace(st_mtime_ns=0)), \
             mock.patch.dict(reader["os"].environ, {"PFM_TMUX_DIR": "/synthetic"}):
            reader["reap"]({"terminal": {"socket": "/synthetic/cc-busy"}}, {}, resources, "/synthetic/check", evidence)
        self.assertEqual(["foreign-birth", "child-setup", "dead-enter", "age-enter", "preview-first", "preview-second",
                          "age-exit", "restore-factory", "dead-exit", "restored-birth", "foreign-birth", "child-finished"], order)
        self.assertEqual([], [row["field"] for row in evidence["checks"] if row["expected"] != row["actual"]])

    def test_auxiliary_factory_uses_run_names_and_preserves_supplied_targets(self):
        created = []
        def create(alias, session):
            created.append((alias, session))
            return {"backend": "tmux", "socket": "/synthetic/" + alias, "pane": session + ":0.0"}
        with mock.patch.dict(reader["resources_for"].__globals__, {"tmux_server": create}), \
             mock.patch.dict(reader["os"].environ, {"PFM_RUN": "synthetic-run"}):
            value = reader["resources_for"]("reap", {"fixture": "kept"})
            self.assertEqual("kept", value["fixture"])
            self.assertEqual([("cc-reader-dead-f04bdee2befe", "reader-dead"),
                              ("cc-reader-foreign-f04bdee2befe", "reader-foreign")], created)
            created.clear()
            supplied = {"dead_socket": "/synthetic/dead", "foreign_socket": "/synthetic/foreign"}
            self.assertEqual(supplied, reader["resources_for"]("reap", supplied))
            self.assertEqual([], created)

    def test_watch_keeps_identity_and_only_one_idle_after_working(self):
        self.assertEqual([{"event": "SEEN", "state": "working"}, {"event": "IDLE", "state": "idle"}],
                         reader["watch_events"]("SEEN scout working\nIDLE scout idle_seconds=12\n", "scout"))
        for value in ("SEEN peer working\n", "ERROR scout unavailable\n", "IDLE scout idle_seconds=unknown\n"):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "^PROCESS-WATCH-"):
                reader["watch_events"](value, "scout")
        after_stop = {"state": "working", "active_child_ids": ["child"], "main_stopped_at": 9}
        self.assertEqual(after_stop, reader["child_after_main_stop"](after_stop, "child"))
        for value in ({"state": "working", "active_child_ids": ["child"]},
                      {"state": "working", "active_child_ids": ["child"], "main_stopped_at": 0},
                      {"state": "idle", "active_child_ids": ["child"], "main_stopped_at": 9},
                      {"state": "working", "active_child_ids": ["peer"], "main_stopped_at": 9}):
            with self.subTest(activity=value):
                self.assertEqual(None, reader["child_after_main_stop"](value, "child"))
        completion = {"pid": 17, "command": "owned shell", "stdout": "reader-child-finished", "stderr": "", "exit_code": 0}
        checks = reader["child_completion_checks"]([completion], 17, "owned shell")
        self.assertEqual([], [row["field"] for row in checks if row["expected"] != row["actual"]])
        for changed in ({**completion, "exit_code": 124}, {**completion, "pid": 18}):
            checks = reader["child_completion_checks"]([changed], 17, "owned shell")
            self.assertEqual(["native-child-completion"], [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_preview_keeps_busy_and_foreign_owner_and_repeats_classification(self):
        rows = {"apply": False, "killed": 0, "failed": 0, "unknown": [],
                "reap": [{"socket": "cc-dead", "state": "dead", "reason": "stale socket file"}],
                "spared": [{"socket": "cc-busy", "state": "active", "reason": "transcript written within 1m0s"},
                           {"socket": "cc-foreign", "state": "hosts", "reason": "hosts non-chat processes: fixture"}]}
        checks = reader["retained_checks"](rows, copy.deepcopy(rows), "cc-dead", "cc-busy", "cc-foreign")
        self.assertEqual([], [row["field"] for row in checks if row["expected"] != row["actual"]])
        changed = copy.deepcopy(rows)
        changed["spared"][0]["state"] = "orphan"
        checks = reader["retained_checks"](rows, changed, "cc-dead", "cc-busy", "cc-foreign")
        self.assertEqual(["repeat-preview-classifications"], [row["field"] for row in checks if row["expected"] != row["actual"]])
        changed = copy.deepcopy(rows)
        changed["reap"].append({"socket": "cc-foreign", "state": "orphan"})
        checks = reader["retained_checks"](changed, changed, "cc-dead", "cc-busy", "cc-foreign")
        self.assertEqual(["only-dead-owned-socket-removable"], [row["field"] for row in checks if row["expected"] != row["actual"]])
        for index, field in ((0, "busy-child-retained"), (1, "foreign-owner-retained")):
            changed = copy.deepcopy(rows)
            changed["spared"][index]["state"] = "orphan"
            checks = reader["retained_checks"](changed, changed, "cc-dead", "cc-busy", "cc-foreign")
            self.assertEqual([field], [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_theme_requires_exact_stopped_server_error_and_bounded_cleanup(self):
        paths = ["account-1", "account-2", "account-3"]
        values = {"account-1": "light", "account-2": "light", "account-3": "light"}
        checks = reader["theme_source_checks"](paths, values)
        self.assertEqual([], [row["field"] for row in checks if row["expected"] != row["actual"]])
        for changed in ({**values, "account-2": "dark"}, {"account-1": "light", "account-2": "light"}):
            checks = reader["theme_source_checks"](paths, changed)
            self.assertEqual(["account-settings-source-theme"], [row["field"] for row in checks if row["expected"] != row["actual"]])
        for before, after, expected in (
            ("bg=#373737,fg=#ffffff", "bg=#f0f0f0,fg=#000000", []),
            ("bg=#373737,fg=#ffffff", "bg=#373737,fg=#ffffff", ["healthy-status-style", "healthy-status-style-changed"]),
            ("bg=#f0f0f0,fg=#000000", "bg=#f0f0f0,fg=#000000", ["healthy-status-style-changed"]),
        ):
            with self.subTest(before=before, after=after):
                checks = reader["theme_style_checks"](before, after)
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])
        value = {"use": {"value": "light", "refused": []}, "report": {"enabled": True, "source": {"value": "light"},
                 "surfaces": [{"surface": "tmux", "verdict": "ERROR", "detail":
                 "themesync: list tmux servers: read tmux server pid on stopped: tmux server did not answer: signal: killed\n"
                 "1 of 3 servers failed: tmux stopped: read tmux option stopped status-style: tmux server did not answer: signal: killed"}]}}
        for elapsed, failures in ((14999, []), (15000, []), (15001, ["theme-probe-bounded"])):
            with self.subTest(elapsed=elapsed):
                checks = reader["theme_checks"](value, "/synthetic/stopped", elapsed, [])
                self.assertEqual(failures, [row["field"] for row in checks if row["expected"] != row["actual"]])
        changed = copy.deepcopy(value)
        changed["report"]["surfaces"][0]["detail"] = value["report"]["surfaces"][0]["detail"].replace("stopped", "peer")
        checks = reader["theme_checks"](changed, "/synthetic/stopped", 10, [])
        self.assertEqual(["stopped-theme-failure"], [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_probe_clients_excludes_exact_server_and_preserves_blocked_children(self):
        snapshot = {10: {"pid": 10, "start": "server", "argv": "tmux -S /synthetic/stopped new-session"},
                    11: {"pid": 11, "start": "client", "argv": "tmux -S /synthetic/stopped show -gv status-style"},
                    12: {"pid": 12, "start": "peer", "argv": "tmux -S /synthetic/peer show -gv status-style"},
                    13: {"pid": 13, "start": "unrelated", "argv": "sh -c 'unrelated unterminated command"}}
        self.assertEqual([{"pid": 11, "start": "client", "argv": ["tmux", "-S", "/synthetic/stopped", "show", "-gv", "status-style"]}],
                         reader["probe_clients"](snapshot, "/synthetic/stopped", 10))


if __name__ == "__main__":
    unittest.main()
