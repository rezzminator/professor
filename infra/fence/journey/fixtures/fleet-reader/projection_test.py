#!/usr/bin/env python3
from pathlib import Path
import runpy
import copy
import unittest

projection = runpy.run_path(str(Path(__file__).with_name("projection.py")))


class TextFleetTests(unittest.TestCase):
    def test_unnamed_chats_bind_by_session_and_key_scopes_only_the_changed_actor(self):
        chats = [{"key": "actor-a", "name": "", "session_id": "session-a", "engine": "claude", "account": 1,
                  "project": "atlas", "terminal": {"backend": "herdr", "pane": "pane-a"}},
                 {"key": "actor-b", "name": "", "session_id": "session-b", "engine": "claude", "account": 2,
                  "project": "beacon", "terminal": {"backend": "herdr", "pane": "pane-b"}}]
        rows = []
        for chat in chats:
            rows.append({"name": "", "status": {"engine": "cc", "session_id": chat["session_id"]},
                         "terminal": {"backend": "herdr", "identity": {"pane": chat["terminal"]["pane"]}, "text": "original"},
                         "picker": [{key: chat[key] for key in ("name", "account", "project")}],
                         "stored": {key: [] for key in ("seat", "launch", "recipe", "hidden", "lineage")}})
        before = {"errors": [], "foreign": {"session": "sentinel"}, "chats": rows}
        actual = copy.deepcopy(before)
        actual["chats"][0]["terminal"]["text"] = "expected actor update"
        try:
            selected = projection["selected"](actual, chats[0])
            checks = projection["bundle_checks"](actual, {"chats": chats}, before, ["key:actor-a"])
        except ValueError as error:
            self.fail(f"expected session-a selected despite duplicate empty names; actual={error}")
        self.assertEqual("session-a", selected["status"]["session_id"])
        self.assertEqual({"actor-a": {"replaced"}, "actor-b": {"moved"}},
                         projection["changed_actors"](["replaced:key:actor-a", "moved:key:actor-b"], chats))
        self.assertEqual([], [value["field"] for value in checks if value["expected"] != value["actual"]])
        actual["chats"][1]["terminal"]["text"] = "unwanted peer update"
        checks = projection["bundle_checks"](actual, {"chats": chats}, before, ["key:actor-a"])
        self.assertEqual(["actor-b:untouched"], [value["field"] for value in checks if value["expected"] != value["actual"]])
        with self.assertRaisesRegex(ValueError, "^READER-CHANGED-IDENTITY:"):
            projection["bundle_checks"](actual, {"chats": chats}, before, [""])
        with self.assertRaisesRegex(ValueError, "^READER-IDENTITY:"):
            projection["selected"](actual, {**chats[0], "session_id": "unrelated-session"})

    def test_sender_footer_uses_sid_for_unnamed_and_quotes_whitespace_names(self):
        for name, address in (("", "a1b2c3d4"), ("plain", "plain"), ("pair name", '"pair name"'), (" \t ", "a1b2c3d4")):
            with self.subTest(name=name):
                caller = {"session_id": "a1b2c3d4-2222-4444-8888-000000000000", "name": name}
                callee = {"key": "recipient", "name": "recipient", "session_id": "recipient-session"}
                footer = "sid a1b2c3d4 · to reply: call " + address + " <message>"
                row = {"name": "recipient", "status": {"state": "idle", "session_id": "recipient-session"},
                       "stored": {"transcript": {"entries": [{"role": "user", "text": "nonce — " + footer},
                                                               {"role": "assistant", "text": "pong"}]}}}
                checks = projection["call_checks"]({"chats": [row]}, caller, callee, "nonce")
                self.assertEqual([], [value["field"] for value in checks if value["expected"] != value["actual"]])

    def test_declared_move_rebinds_only_the_owned_seat_locator(self):
        chat = {"key": "actor", "name": "mover", "seat_id": "seat", "session_id": "session", "engine": "claude",
                "account": 3, "project": "atlas", "workspace": "atlas-workspace", "terminal": {"backend": "herdr", "pane": "new-pane"}}
        seat = {"seat_id": "seat", "herdr_pane": "old-pane", "herdr_workspace": "atlas-workspace", "updated_at": 10,
                "seat_host_pid": 31, "herdr_terminal_id": "same-physical-terminal", "state": "live"}
        foreign_seat = {"seat_id": "other-seat", "updated_at": 9, "state": "lost"}
        old = {"name": "mover", "status": {"engine": "cc", "session_id": "session"},
               "terminal": {"backend": "herdr", "identity": {"socket": "owned", "workspace": "atlas-workspace", "tab": "original-tab", "pane": "old-pane"}},
               "picker": [{"name": "mover", "account": 3, "project": "atlas"}],
               "stored": {"seat": [seat, foreign_seat], "launch": [], "recipe": {"binary": "actual-native"}, "hidden": [], "lineage": []}}
        before = {"errors": [], "foreign": {"session": "sentinel"}, "chats": [old]}
        good = copy.deepcopy(before)
        current = good["chats"][0]
        current["terminal"]["identity"]["pane"] = "new-pane"
        current["stored"]["seat"][0].update(herdr_pane="new-pane", updated_at=12)
        mutations = ((None, None, []), ("herdr_pane", "wrong-pane", ["actor:seat"]),
                     ("seat_host_pid", 99, ["actor:seat"]), ("herdr_terminal_id", "replacement-terminal", ["actor:seat"]),
                     ("herdr_workspace", "foreign-workspace", ["actor:seat"]))
        for field, value, expected in mutations:
            with self.subTest(field=field):
                actual = copy.deepcopy(good)
                if field:
                    actual["chats"][0]["stored"]["seat"][0][field] = value
                checks = projection["bundle_checks"](actual, {"chats": [chat]}, before, ["moved:mover"])
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])
        for field, expected in (("tab", ["actor:terminal-identity"]), ("recipe", ["actor:recipe"]), ("other-seat", ["actor:seat"])):
            with self.subTest(field=field):
                actual = copy.deepcopy(good)
                current = actual["chats"][0]
                if field == "tab":
                    current["terminal"]["identity"]["tab"] = "different-tab"
                elif field == "recipe":
                    current["stored"]["recipe"]["binary"] = "different-native"
                else:
                    current["stored"]["seat"][1]["updated_at"] = 99
                checks = projection["bundle_checks"](actual, {"chats": [chat]}, before, ["moved:mover"])
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_supervisor_birth_uses_persisted_host_and_actual_target_ancestry(self):
        chat = {"key": "scratch", "name": "moved", "seat_id": "owned-seat"}
        host = {"pid": 31, "start": "actual-host", "argv": "/synthetic/pfm internal seat-host owned-seat"}
        engine = {"pid": 32, "start": "actual-engine", "argv": "/synthetic/mock-engine claude"}
        row = {"name": "moved", "stored": {"seat": [{"seat_id": "owned-seat", "seat_host_pid": 31}]},
               "process_births": [{"pid": 32, "start": "actual-engine", "chain": [engine, host]}]}
        self.assertEqual({"pid": 31, "start": "actual-host"}, projection["supervisor_birth"]({"chats": [row]}, chat))
        for pid in (0, 32, 999):
            with self.subTest(pid=pid), self.assertRaisesRegex(ValueError, "^READER-SEAT-HOST"):
                row["stored"]["seat"][0]["seat_host_pid"] = pid
                projection["supervisor_birth"]({"chats": [row]}, chat)

    def test_deleted_plain_folder_keeps_chat_project_and_state(self):
        chat = {"name": "gone-scratch", "project": "atlas-scratch-run", "session_id": "owned"}
        row = {"name": "gone-scratch", "status": {"session_id": "owned", "state": "idle"},
               "picker": [{"name": "gone-scratch", "project": "atlas-scratch-run", "state": "idle"}]}
        checks = projection["deleted_cwd_checks"]({"chats": [row]}, chat)
        self.assertEqual([], [check for check in checks if check["expected"] != check["actual"]])
        row["picker"][0]["project"] = "atlas"
        checks = projection["deleted_cwd_checks"]({"chats": [row]}, chat)
        self.assertEqual(["deleted-cwd-picker"], [check["field"] for check in checks if check["expected"] != check["actual"]])

    def test_herdr_schema_keeps_all_advertised_methods_and_rejects_duplicates(self):
        methods = [{"properties": {"method": {"const": value}}} for value in ("pane.get", "tab.create")]
        schema = {"schemas": {"request": {"oneOf": methods}}}
        self.assertEqual(["pane.get", "tab.create"], projection["herdr_methods"](schema))
        methods.append(methods[0])
        with self.assertRaisesRegex(ValueError, "^READER-HERDR-SCHEMA:"):
            projection["herdr_methods"](schema)

    def test_stopped_server_warning_stays_named_beside_valid_rows(self):
        table = "kind\tid\tproject\tcwd\tname\taccount\tkilled\tsocket\ncc\towned\tatlas\t/synthetic/atlas\tscout\t3\tfalse\tcc-owned\n"
        carry = {"chats": [{"key": "child", "session_id": "owned", "project": "atlas", "name": "scout", "account": 3}]}
        text = "pfm ls: read /synthetic/cc-owned: tmux server did not answer\n" + table
        checks = projection["stopped_fleet_checks"](text, carry, "/synthetic/cc-owned")
        self.assertEqual([], [row for row in checks if row["expected"] != row["actual"]])
        checks = projection["stopped_fleet_checks"]("pfm ls: different failure\n" + table, carry, "/synthetic/cc-owned")
        self.assertEqual(["stopped-server-warning", "unrelated-warning"], [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_lost_row_requires_the_dead_carried_seat_and_empty_cwd(self):
        chat = {"key": "scratch", "name": "reader-lost-run", "session_id": "session", "seat_id": "seat"}
        observed = {"name": "reader-lost-run", "status": {"state": "dead", "session_id": "session"},
                    "stored": {"seat": [{"seat_id": "seat", "state": "lost"}]}, "rows": [{"id": "seat", "cwd": ""}]}
        checks = projection["lost_checks"]("↻ reader-lost-run  🥇  0p\n", {"chats": [observed]}, chat)
        self.assertEqual([], [row for row in checks if row["expected"] != row["actual"]])
        observed["rows"][0]["cwd"] = "/synthetic/stale"
        checks = projection["lost_checks"]("● reader-lost-run  🥇  0p\n", {"chats": [observed]}, chat)
        self.assertEqual(["lost-public-row", "lost-row-cwd"], [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_engine_birth_rejects_a_shell_or_multiple_engines(self):
        chat = {"name": "reader-lost-run", "key": "scratch", "engine": "claude"}
        shell = {"pid": 11, "start": "synthetic-shell", "chain": [{"argv": "/bin/zsh -l"}], "environment": {}}
        engine = {"pid": 12, "start": "synthetic-engine", "chain": [{"argv": "/synthetic/mock-engine claude"}], "environment": {}}
        row = {"name": "reader-lost-run", "process_births": [shell, engine], "terminal": {"identity": {"pane": "synthetic-pane"}}}
        self.assertEqual({"pid": 12, "start": "synthetic-engine"}, projection["engine_birth"]({"chats": [row]}, chat))
        for births in ([shell], [engine, {**engine, "pid": 13}]):
            with self.subTest(births=births), self.assertRaisesRegex(ValueError, "^READER-ENGINE-BIRTH:"):
                row["process_births"] = births
                projection["engine_birth"]({"chats": [row]}, chat)

    def test_declared_terminal_replacement_still_checks_new_carried_pane(self):
        chat = {"key": "actor", "name": "scout", "session_id": "session", "engine": "claude", "account": 3,
                "project": "atlas", "terminal": {"backend": "tmux", "socket": "/synthetic/tmux", "pane": "%2"}}
        current = {"name": "scout", "status": {"engine": "cc", "session_id": "session"},
                   "terminal": {"backend": "tmux", "identity": {"pane": "%2", "socket": "/synthetic/tmux"}, "workspace": "n/a:tmux"},
                   "picker": [{"name": "scout", "account": 3, "project": "atlas"}],
                   "stored": {key: [] for key in ("seat", "launch", "recipe", "hidden", "lineage")}}
        after = {"errors": [], "foreign": {"session": "foreign"}, "chats": [current]}
        before = copy.deepcopy(after)
        before["chats"][0]["terminal"]["identity"]["pane"] = "%1"
        for changed, pane, expected_fields in (("scout", "%2", ["actor:terminal-identity"]),
                                                ("replaced:scout", "%2", []),
                                                ("replaced:scout", "%3", ["actor:pane"])):
            with self.subTest(changed=changed, pane=pane):
                current["terminal"]["identity"]["pane"] = pane
                checks = projection["bundle_checks"](after, {"chats": [chat]}, before, [changed])
                self.assertEqual(expected_fields, [row["field"] for row in checks if row["expected"] != row["actual"]])

    def test_text_roster_matches_exact_carried_fields(self):
        text = "kind\tid\tproject\tcwd\tname\taccount\tkilled\tsocket\ncc\towned\tatlas\t/synthetic/atlas\tscout\t3\tfalse\tcc-owned\n"
        carry = {"chats": [{"key": "child", "session_id": "owned", "seat_id": "seat-owned",
                            "project": "atlas", "name": "scout", "account": 3}]}
        self.assertEqual([{"field": "child:text-row", "expected": [{"name": "scout", "account": "3", "project": "atlas"}],
                           "actual": [{"name": "scout", "account": "3", "project": "atlas"}]}],
                         projection["text_fleet_checks"](text, carry))
        self.assertEqual([], projection["text_fleet_checks"](text.replace("\towned\t", "\tforeign\t"), carry)[0]["actual"])

    def test_unreadable_schema_is_distinct_from_empty_fleet(self):
        with self.assertRaisesRegex(ValueError, "^READER-TEXT-SCHEMA: "):
            projection["text_fleet_checks"]("name\nscout\n", {"chats": []})
        self.assertEqual([], projection["text_fleet_checks"]("kind\tid\tproject\tcwd\tname\taccount\tkilled\tsocket\n", {"chats": []}))

    def test_mcp_text_drift_keeps_exact_directory(self):
        text = "kind\tid\tproject\tcwd\tname\taccount\tkilled\tsocket\ncc\towned\tatlas\t/synthetic/atlas\tscout\t3\tfalse\tcc-owned\n"
        carry = {"chats": [{"key": "child", "session_id": "owned", "name": "scout"}]}
        result = {"rows": [{"id": "owned", "project": "atlas", "dir": "/synthetic/atlas", "name": "scout", "account": 3}]}
        checks = projection["fleet_drift_checks"](result, text, carry)
        self.assertEqual([], [row for row in checks if row["expected"] != row["actual"]])
        result["rows"][0]["dir"] = "/synthetic/beacon"
        checks = projection["fleet_drift_checks"](result, text, carry)
        self.assertEqual([{ "name": "scout", "account": "3", "project": "atlas", "cwd": "/synthetic/beacon"}], checks[0]["actual"])


if __name__ == "__main__":
    unittest.main()
