#!/usr/bin/env python3
"""Branch carry is discovered from owned live seats, never fixture identities."""
import json, os, sqlite3, tempfile, unittest
from pathlib import Path
from unittest.mock import patch

import journey_path
HERE = journey_path.JOURNEY
import chat
import chat_carry
import observation
from runtime import command, read_json, write_json

BRANCH = "linux+herdr-shield+project+claude"
ORIGIN = "new-typed"


class CarriedChat(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.database = self.root / "pfm.db"
        self.carry = self.root / "carry.json"
        write_json(self.carry, {"chats": [], "workspaces": {"atlas": {"id": "w1"}},
                               "owner": {"backend": "tmux", "socket": str(self.root / "tmux.sock"), "pane": "owner:0.0"}})
        self.environment = patch.dict(os.environ, {"JOURNEY_ROOT": str(self.root), "JOURNEY_CARRY": str(self.carry),
            "PFM_STATE_DB": str(self.database), "HERDR_SOCKET_PATH": str(self.root / "herdr.sock"),
            "MOCK_ENGINE_SCENARIO": str(self.root / "scenario.json"), "PFM_BEAT_FILE": str(self.root / "beat.json"),
            "HOME": str(self.root / "seat"), "SHELL": "/bin/zsh", "PFM_TMUX_DIR": str(self.root)})
        self.environment.start()
        self.addCleanup(self.environment.stop)
        write_json(self.root / "scenario.json", {"pane": {"busy": "fixture-busy"}, "action_file": "old",
                                                "record_dir": str(self.root / "records"), "steps": [{"type": "turn", "reply": "baseline"}]})
        with sqlite3.connect(self.database) as connection:
            connection.executescript('''
CREATE TABLE seat(seat_id TEXT PRIMARY KEY,session_id TEXT,engine TEXT,backend TEXT,
 tmux_socket TEXT,tmux_pane TEXT,herdr_socket TEXT,herdr_pane TEXT,herdr_workspace TEXT,
 name TEXT,state TEXT);
CREATE TABLE launch(session_id TEXT PRIMARY KEY,account INTEGER,cache1h INTEGER);
CREATE TABLE children(kind TEXT,key TEXT,val TEXT);
CREATE TABLE meta(key TEXT,val TEXT);
''')
        self.target = {"backend": "herdr", "pane": "w1:p1"}
        self.birth_scenarios = {}
        observation = patch.object(chat_carry, "observe_terminal", side_effect=lambda target: {
            "identity": target, "births": [{"pid": 42, "start": "fixture-birth", "chain": [{"argv": "claude"}],
                "environment": {"MOCK_ENGINE_SCENARIO": self.birth_scenarios.get(target["pane"], str(self.root / "scenario.json"))}}]})
        observation.start()
        self.addCleanup(observation.stop)

    def capture(self, target, branch, origin, previous, *scope):
        path = chat.metadata_path(branch, origin)
        if not path.exists():
            write_json(path, {"scenario": str(self.root / "scenario.json"), "project": "atlas", "workspace": "w1"})
        return chat.capture(target, branch, origin, previous, *scope)

    def seat(self, identity, socket=None, workspace="w1", engine="cc", backend="herdr", state="live"):
        with sqlite3.connect(self.database) as connection:
            connection.execute("INSERT INTO seat VALUES(?,?,?,?,?,?,?,?,?,?,?)", (
                identity, identity + "-session", engine, backend, socket or str(self.root / "tmux.sock"),
                "%" + identity, socket or os.environ["HERDR_SOCKET_PATH"], workspace + ":p" + identity,
                workspace, identity + "-name", state))
            connection.execute("INSERT INTO launch VALUES(?,?,?)", (identity + "-session", 2, 1))

    def test_capture_is_scoped_and_preserves_every_origin(self):
        self.seat("old")
        previous = chat.before(self.target)
        self.seat("foreign", socket="foreign.sock")
        self.seat("wrong-workspace", workspace="w2")
        self.seat("wrong-engine", engine="cx")
        self.assertIsNone(self.capture(self.target, BRANCH, ORIGIN, previous))
        diagnostic_path = chat.metadata_path(BRANCH, ORIGIN).parent / ORIGIN / "capture.json"
        diagnostic = read_json(diagnostic_path)
        self.assertEqual(["old", "wrong-workspace", "wrong-engine"], [row["seat_id"] for row in diagnostic["scoped_seats"]])
        self.assertEqual([], diagnostic["candidates"])
        self.assertEqual([], diagnostic["predicates"])
        self.seat("new")
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET herdr_pane='' WHERE seat_id='new'")
        self.assertIsNone(self.capture(self.target, BRANCH, ORIGIN, previous))
        pending = read_json(chat.metadata_path(BRANCH, ORIGIN).parent / ORIGIN / "capture-pending.json")
        self.assertEqual([("new", "")], [(row["seat_id"], row["herdr_pane"]) for row in pending["pending_terminal_binding"]])
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET herdr_pane='w1:pnew' WHERE seat_id='new'")
        expected = {"key": "chat-birth:" + BRANCH + ":new-typed", "branch": BRANCH, "origin": ORIGIN,
            "engine": "claude", "place": "project", "terminal_kind": "herdr-shield", "name": "new-name",
            "session_id": "new-session", "seat_id": "new", "terminal": {"backend": "herdr", "pane": "w1:pnew"},
            "project": "atlas", "workspace": "w1", "scenario": str(self.root / "scenario.json"), "account": 2, "cache1h": 1}
        self.assertEqual(expected, self.capture(self.target, BRANCH, ORIGIN, previous))
        diagnostic = read_json(diagnostic_path)
        self.assertEqual(["new"], diagnostic["candidates"])
        self.assertEqual([("new", True, True)], [(row["seat_id"], row["terminal_bound"], row["scenario_matches"]) for row in diagnostic["predicates"]])
        self.assertEqual(expected, chat.select(BRANCH, ORIGIN))
        previous = chat.before(self.target)
        self.seat("cli")
        cli = self.capture(self.target, BRANCH, "new-from-cli", previous)
        self.assertEqual("chat-birth:" + BRANCH + ":new-from-cli", cli["key"])
        self.assertEqual(["new", "cli"], [value["seat_id"] for value in read_json(self.carry)["chats"]])
        self.assertIsNone(chat.select(BRANCH, "new-from-picker"))
        with self.assertRaisesRegex(ValueError, "CHAT-CAPTURE-TARGET"):
            self.capture({"backend": "herdr", "pane": "w2:p1"}, BRANCH, "new-from-picker", previous)

    def test_ambiguous_capture_and_refresh_lineage(self):
        previous = chat.before(self.target)
        self.seat("one")
        self.seat("two")
        with self.assertRaisesRegex(ValueError, "CHAT-CAPTURE-AMBIGUOUS"):
            self.capture(self.target, BRANCH, ORIGIN, previous)
        self.assertEqual([], read_json(self.carry)["chats"])
        with sqlite3.connect(self.database) as connection:
            connection.execute("DELETE FROM seat WHERE seat_id='two'")
        current = self.capture(self.target, BRANCH, ORIGIN, previous)
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET session_id='continued',name='renamed',herdr_pane='w1:p9' WHERE seat_id='one'")
            connection.execute("INSERT INTO launch VALUES('continued',3,0)")
        updated = chat.refresh(BRANCH, ORIGIN)
        self.assertEqual({**current, "session_id": "continued", "name": "renamed", "account": 3,
            "cache1h": 0, "terminal": {"backend": "herdr", "pane": "w1:p9"}, "lineage_ids": ["one-session"]}, updated)
        self.assertEqual(updated, chat.refresh(BRANCH, ORIGIN))
        with patch.object(chat, "type_command") as owner_input:
            self.assertEqual(updated, chat.reopen(self.target, BRANCH, ORIGIN))
            owner_input.assert_called_once_with(self.target, ["pfm", "chat", "open", "continued"])
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET seat_id='restored',herdr_pane='w1:prestored' WHERE seat_id='one'")
        restored = chat.refresh(BRANCH, ORIGIN)
        self.assertEqual({**updated, "seat_id": "restored", "terminal": {"backend": "herdr", "pane": "w1:prestored"},
                          "prior_seat_ids": ["one"]}, restored)
        with self.assertRaisesRegex(ValueError, "CHAT-REOPEN-ABSENT"):
            chat.reopen(self.target, BRANCH, "new-from-picker")
        declared = read_json(self.carry)
        declared["chats"][0]["expect"] = {"lifecycle": "archived", "terminal": "absent", "visibility": "archived"}
        write_json(self.carry, declared)
        with patch.object(chat, "type_command"), self.assertRaisesRegex(ValueError, "CHAT-REOPEN-LIFECYCLE"):
            chat.reopen(self.target, BRANCH, ORIGIN)
        self.assertEqual(1, chat.main("need", BRANCH, ORIGIN))
        declared["chats"].append(updated)
        write_json(self.carry, declared)
        with self.assertRaisesRegex(ValueError, "CHAT-CARRY-AMBIGUOUS"):
            chat.select(BRANCH, ORIGIN)

    def test_temporary_expectation_restores_after_refresh_even_on_failure(self):
        previous = chat.before(self.target)
        self.seat("one")
        current = self.capture(self.target, BRANCH, ORIGIN, previous)
        original = {"lifecycle": "live", "terminal": "live", "visibility": "visible"}
        declared = read_json(self.carry)
        declared["chats"][0]["expect"] = original
        write_json(self.carry, declared)
        transient = {"lifecycle": "transient", "terminal": "live", "visibility": "visible"}
        before = {"chats": [{"status": {"session_id": "one-session"}, "terminal": {"identity": self.target},
            "process_births": [{"pid": 43, "start": "scope-birth", "chain": [{"argv": "claude"}], "environment": {}}]}]}
        with self.assertRaisesRegex(RuntimeError, "scope-abort"):
            with chat_carry.temporary_expectation(BRANCH, ORIGIN, transient, before):
                self.assertEqual(transient, chat.select(BRANCH, ORIGIN)["expect"])
                self.assertEqual([{"pid": 43, "start": "scope-birth"}], chat.select(BRANCH, ORIGIN)["expected_prior_births"])
                with sqlite3.connect(self.database) as connection:
                    connection.execute("UPDATE seat SET session_id='continued',name='renamed' WHERE seat_id='one'")
                    connection.execute("INSERT INTO launch VALUES('continued',3,0)")
                refreshed = chat.refresh(BRANCH, ORIGIN)
                self.assertEqual(transient, refreshed["expect"])
                self.assertEqual("continued", refreshed["session_id"])
                with sqlite3.connect(self.database) as connection:
                    connection.execute("UPDATE seat SET seat_id='renewed',herdr_pane='w1:prestored' WHERE seat_id='one'")
                refreshed = chat.refresh(BRANCH, ORIGIN)
                self.assertEqual(transient, refreshed["expect"])
                self.assertEqual("renewed", refreshed["seat_id"])
                raise RuntimeError("scope-abort")
        restored = chat.select(BRANCH, ORIGIN)
        self.assertEqual(original, restored["expect"])
        self.assertEqual("continued", restored["session_id"])
        self.assertEqual("renewed", restored["seat_id"])
        self.assertEqual(["one-session"], restored["lineage_ids"])
        self.assertEqual(["one"], restored["prior_seat_ids"])

    def test_ended_annotation_requires_observed_death_and_preserves_the_role(self):
        previous = chat.before(self.target)
        self.seat("auxiliary")
        current = self.capture(self.target, BRANCH, ORIGIN, previous)
        before = {"chats": [{"status": {"session_id": "auxiliary-session"},
            "terminal": {"identity": self.target}, "process_births": [{"pid": 42, "start": "prior",
            "chain": [{"argv": "claude"}], "environment": {}}],
            "stored": {"transcript": {"path": str(self.root / "auxiliary.jsonl")}}}]}
        declared = read_json(self.carry)
        declared["foreign"] = {**current, "key": "foreign-key", "session_id": "foreign-session"}
        write_json(self.carry, declared)
        foreign_before = {"chats": [], "foreign_full": {**before["chats"][0], "status": {"session_id": "foreign-session"}}}
        for lifecycle, visibility, terminal in (("live", "killed", "live"), ("ended", "visible", "shell"), ("archived", "archived", "shell")):
            updated = chat_carry.expect(declared["foreign"], lifecycle, foreign_before, visibility)
            self.assertEqual({"lifecycle": lifecycle, "terminal": terminal, "visibility": visibility}, updated["expect"])
            self.assertEqual([{"pid": 42, "start": "prior"}], updated["expected_prior_births"])
            self.assertEqual(current, chat.select(BRANCH, ORIGIN))
        foreign_before["foreign_full"]["process_births"] = []
        retained = read_json(self.carry)
        with self.assertRaisesRegex(ValueError, "CHAT-ENGINE-BIRTH-MISSING"):
            chat_carry.expect(declared["foreign"], "ended", foreign_before)
        self.assertEqual(retained, read_json(self.carry))
        shell = {"births": [{"chain": [{"argv": "zsh -l"}]}]}
        for state, processes, error in (("idle", {}, "OBSERVE-END-STATE"),
                ("dead", {42: {"start": "prior"}}, "OBSERVE-ENGINE-STILL-LIVE"), ("dead", {}, None)):
            with self.subTest(state=state, processes=processes), \
                    patch.object(observation, "report", return_value=json.dumps({"state": state})), \
                    patch.object(observation, "stored", return_value={"seat": [{"state": "ended"}]}), \
                    patch.object(observation, "process_snapshot", return_value=processes), \
                    patch.object(chat_carry, "observe_terminal", return_value=shell):
                if error:
                    with self.assertRaisesRegex(ValueError, error):
                        chat.expect_ended(BRANCH, ORIGIN, before)
                    self.assertEqual(current, chat.select(BRANCH, ORIGIN))
                else:
                    updated = chat.expect_ended(BRANCH, ORIGIN, before)
                    self.assertEqual({**current, "expect": {"lifecycle": "ended", "terminal": "shell", "visibility": "visible"},
                        "expected_prior_births": [{"pid": 42, "start": "prior"}],
                        "transcript_path": str(self.root / "auxiliary.jsonl")}, updated)

    def test_continuation_replaces_role_without_branch_parent(self):
        previous = chat.before(self.target)
        self.seat("old")
        current = self.capture(self.target, BRANCH, ORIGIN, previous)
        declared = read_json(self.carry)
        declared["chats"][0].update({"prior_seat_ids": ["ancestor-seat"], "lineage_ids": ["ancestor-session"]})
        write_json(self.carry, declared)
        staged_origin = "continued"
        staged = self.root / "continued-scenario.json"
        write_json(staged, {"record_dir": str(self.root / "continued-records")})
        write_json(chat.metadata_path(BRANCH, staged_origin), {
            "scenario": str(staged), "project": "atlas", "workspace": "w1",
        })
        previous = chat.before(self.target)
        with self.assertRaisesRegex(ValueError, "CHAT-CONTINUATION-LIVE"):
            chat.adopt_continuation(self.target, BRANCH, ORIGIN, staged_origin, previous)
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET state='ended' WHERE seat_id='old'")
        self.seat("continued", workspace="w1")
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET session_id='old-session' WHERE seat_id='continued'")
            connection.execute("UPDATE launch SET account=3,cache1h=0 WHERE session_id='old-session'")
        self.birth_scenarios["w1:pcontinued"] = str(staged)
        updated = chat.adopt_continuation(self.target, BRANCH, ORIGIN, staged_origin, previous)
        self.assertEqual(current["key"], updated["key"])
        self.assertEqual((ORIGIN, "continued", "old-session"),
                         (updated["origin"], updated["seat_id"], updated["session_id"]))
        self.assertEqual({"backend": "herdr", "pane": "w1:pcontinued"}, updated["terminal"])
        self.assertEqual(["ancestor-seat", "old"], updated["prior_seat_ids"])
        self.assertEqual(["ancestor-session"], updated["lineage_ids"])
        self.assertNotIn("parent", updated)
        self.assertNotIn("parent_key", updated)
        self.assertEqual([updated], read_json(self.carry)["chats"])
        self.assertIsNone(chat.select(BRANCH, staged_origin))

    def test_wizard_receipt_records_preselected_chips_before_requested_selection(self):
        screens = ["◆ pfm ◖ Claude ◗", "◆ pfm ◖ 🥈 2 ◗", "◆ pfm ◖ 🥇 1 ◗",
                   "◆ pfm ◖ 1h ◗", "◆ pfm ◖ 5m ◗", "❯"]
        receipt = self.root / "wizard.json"
        with patch.object(chat, "screen", side_effect=screens), patch.object(chat, "keys"):
            actual = chat.wizard(self.target, "claude", "1", "5m", receipt)
        expected = {"account": 2, "cache": "1h"}
        self.assertEqual(expected, actual)
        self.assertEqual(expected, read_json(receipt))

    def test_scenario_and_live_steps_enter_through_owner_input(self):
        fixture = self.root / "turn.json"
        write_json(fixture, {"steps": [{"type": "turn", "reply": "fixture-reply"}]})
        with patch.object(chat, "type_command") as owner_input, patch.object(chat.uuid, "uuid4", return_value="799035e2-4902-459e-9317-3f5f20b28e42"):
            actual = chat.scenario(self.target, BRANCH, ORIGIN, fixture)
            owner_input.assert_called_once_with(self.target, ["export", "MOCK_ENGINE_SCENARIO=" + actual["scenario"],
                "PFM_TEST_FRESH_SOCKET=cc-799035e2-4902-459e-9317-3f5f20b28e42"])
        value = read_json(actual["scenario"])
        self.assertEqual({"pane": {"busy": "fixture-busy"}, "action_file": str(self.root / "beat.json"),
            "record_dir": actual["records"], "session_id": "799035e2-4902-459e-9317-3f5f20b28e42",
            "steps": [{"type": "turn", "reply": "fixture-reply"}]}, value)
        with self.assertRaisesRegex(ValueError, "CHAT-SCENARIO-EXISTS"):
            chat.scenario(self.target, BRANCH, ORIGIN, fixture)
        steps = self.root / "steps.json"
        write_json(steps, [{"type": "mcp", "server": "chat-professor", "tool": "ls", "input": {}},
                           {"type": "turn", "reply": "fixture-reply"}])
        with patch.object(chat, "text") as owner_text, patch.object(chat, "keys") as owner_keys:
            chat.inline(self.target, steps)
            owner_text.assert_called_once_with(self.target,
                'journey-script mock-engine: [{"input":{},"server":"chat-professor","tool":"ls","type":"mcp"},{"reply":"fixture-reply","type":"turn"}]')
            owner_keys.assert_called_once_with(self.target, ["enter"])
        self.assertEqual(value, read_json(actual["scenario"]))
        for origin in ("storm-3", "forecast-retired-seat", "spawn-child-from-mcp"):
            self.assertIsNone(chat.select(BRANCH, origin))
        for branch in ("linux+tmux+project+../../escape", "linux+tmux+home+claude", "linux+tmux+project"):
            with self.subTest(branch=branch), self.assertRaisesRegex(ValueError, "CHAT-BRANCH"):
                chat.select(branch, ORIGIN)

    def test_new_tmux_socket_is_bound_to_the_public_launch_input(self):
        branch = "linux+tmux+project+claude"
        target = read_json(self.carry)["owner"]
        previous = chat.before(target)
        fixed = "799035e2-4902-459e-9317-3f5f20b28e42"
        with patch.object(chat, "type_command"), patch.object(chat.uuid, "uuid4", return_value=fixed):
            staged = chat.scenario(target, branch, "storm-3", self.root / "scenario.json")
        self.seat("foreign", socket=str(self.root / "another-chat"), backend="tmux")
        self.assertIsNone(self.capture(target, branch, "storm-3", previous))
        self.seat("child", socket="cc-" + fixed, backend="tmux")
        self.birth_scenarios["%child"] = staged["scenario"]
        value = self.capture(target, branch, "storm-3", previous)
        self.assertEqual("child-session", value.get("session_id") if isinstance(value, dict) else value)
        self.assertEqual({"backend": "tmux", "socket": str(self.root / ("cc-" + fixed)), "pane": "%child"}, value["terminal"])
        self.assertEqual(staged["launch_socket"], value["terminal"]["socket"])
        declared = read_json(self.carry)
        declared["chats"] = []
        write_json(self.carry, declared)
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET tmux_pane='' WHERE seat_id='child'")
        self.birth_scenarios["%42"] = staged["scenario"]
        with patch.object(observation, "report", return_value="%42\n") as native:
            discovered = self.capture(target, branch, "storm-3", previous)
        self.assertEqual({**value, "terminal": {**value["terminal"], "pane": "%42"}}, discovered)
        native.assert_called_once_with(["tmux", "-S", staged["launch_socket"], "list-panes", "-a", "-F", "#{pane_id}"])
        row = next(row for row in chat.seats() if row["seat_id"] == "child")
        self.assertEqual("", row["tmux_pane"])
        for panes in ("", "%one\n%two\n"):
            with self.subTest(panes=panes), patch.object(observation, "report", return_value=panes):
                with self.assertRaisesRegex(ValueError, "CHAT-TMUX-PANE-AMBIGUOUS"):
                    chat.seat_terminal(row)

    def test_plain_pty_launch_binds_the_staged_native_tmux_socket(self):
        target = {"backend": "pty", "pane": "outside-terminal"}
        for engine, code, native_id in (("claude", "cc", "00000000-0000-4000-8000-000000000001"),
                                        ("codex", "cx", "00000000-0000-4000-8000-000000000002"),
                                        ("opencode", "ox", "00000000-0000-4000-8000-000000000003")):
            with self.subTest(engine=engine):
                branch = "linux+tmux+project+" + engine
                previous = chat.before(target)
                with patch.object(chat, "type_command"), patch.object(chat.uuid, "uuid4", return_value=native_id):
                    metadata = chat.scenario(target, branch, "outside-cli", self.root / "scenario.json")
                write_json(chat.metadata_path(branch, "outside-cli"), {**metadata, "launch_argv": ["pfm", "chat", "new"]})
                self.seat(engine, engine=code, backend="tmux", socket=metadata["launch_socket"])
                with patch.object(chat_carry, "observed_scenario", return_value={"scenario": metadata["scenario"]}):
                    if engine != "claude":
                        self.assertIsNone(chat.capture(target, branch, "outside-cli", previous))
                        with sqlite3.connect(self.database) as connection:
                            connection.execute("UPDATE seat SET session_id=? WHERE seat_id=?", (native_id, engine))
                    actual = chat.capture(target, branch, "outside-cli", previous)
                expected_id = "claude-session" if engine == "claude" else native_id
                self.assertEqual((expected_id, metadata["launch_socket"]), (actual["session_id"], actual["terminal"]["socket"]))

    def test_codex_tmux_capture_uses_native_identity_without_a_seat(self):
        branch = "linux+tmux+project+codex"
        previous = chat.before(self.target)
        with patch.object(chat, "type_command"):
            metadata = chat.scenario(self.target, branch, "seatless", self.root / "scenario.json")
        socket = metadata["launch_socket"]
        Path(socket).touch()
        target = {"backend": "tmux", "socket": socket, "pane": "%42"}
        birth = {"pid": 73, "start": "native-73", "chain": [{"argv": "codex"}],
                 "environment": {"MOCK_ENGINE_SCENARIO": metadata["scenario"]}}
        display = {"identity": {"socket": socket, "pane": "%42"}, "births": [birth]}
        row = {"id": "native-codex", "kind": "live-codex", "socket": socket,
               "project": "atlas", "name": "actual-name", "account": "2"}
        status = {"session_id": "native-codex", "state": "idle", "engine": "cx", "socket": socket}
        def report(argv, **_):
            return "%42\n" if argv[0] == "tmux" else json.dumps(status)
        with patch.object(observation, "fleet_rows", return_value=[{**row, "socket": str(self.root / "foreign")}] ) as fleet, \
             patch.object(observation, "report", side_effect=report), \
             patch.object(chat_carry, "observe_terminal", return_value=display), \
             patch.object(observation, "read_actual_codex_session", return_value={"path": "actual-rollout", "id": "native-codex"}):
            self.assertIsNone(chat.capture(self.target, branch, "seatless", previous))
            fleet.return_value = [row]
            actual = chat.capture(self.target, branch, "seatless", previous)
            self.assertIsNotNone(actual)
            self.assertEqual(("native-codex", "actual-name", 2, target),
                             (actual["session_id"], actual["name"], actual["account"], actual["terminal"]))
            self.assertNotIn("seat_id", actual)
            self.assertEqual([], chat.seats())
            self.assertEqual({"status": status, "terminal": target, "births": [{"pid": 73, "start": "native-73",
                "argv": ["codex"], "environment": {"MOCK_ENGINE_SCENARIO": metadata["scenario"]},
                "terminal": {"socket": socket, "pane": "%42"}}]}, actual["tmux_launch"])
            self.assertEqual(True, observation.reopenable(actual))
            broken = {**actual, "tmux_launch": {**actual["tmux_launch"], "status": {**status, "session_id": "foreign"}}}
            with self.assertRaisesRegex(ValueError, "OBSERVE-REOPEN-CHECKPOINT-MISMATCH"):
                observation.reopenable(broken)
            self.assertEqual(actual, chat.select(branch, "seatless"))
            self.assertEqual(actual, chat.refresh(branch, "seatless"))
            with self.assertRaisesRegex(ValueError, "CHAT-CARRY-EXISTS"):
                chat.capture(self.target, branch, "seatless", previous)

    def test_wizard_and_missing_typed_prelude_keep_the_real_origin(self):
        with patch.object(chat, "screen", side_effect=["› ✦ New chat → beacon", "› ✦ New chat → atlas"]), \
             patch.object(chat, "type_command") as entry, patch.object(chat, "keys") as navigation, \
             patch.object(chat, "wizard", return_value={"account": 1, "cache": "1h"}) as wizard:
            self.assertEqual({"account": 1, "cache": "1h"}, chat.pick_new(self.target, "atlas", "codex", "2", "5m"))
            entry.assert_called_once_with(self.target, ["pfm"])
            navigation.assert_called_once_with(self.target, ["down"])
            wizard.assert_called_once_with(self.target, "codex", "2", "5m")
        for place, suffix in (("project", "atlas"), ("subfolder", "atlas/docs/specs"), ("worktree", "atlas/.worktrees/flight-export")):
            self.assertEqual(str(self.root / "seat/work" / suffix), chat.branch_cwd("linux+herdr-shield+" + place + "+claude"))
        screens = ["◆ pfm ◖ Claude ◗", "◆ pfm ◖ 🥇 1 ◗", "◆ pfm ◖ 🥈 2 ◗", "◆ pfm ◖ 🥉 3 ◗",
                   "◆ pfm ◖ 1h ◗", "◆ pfm ◖ 5m ◗", "❯"]
        screens = [shown + "\n◖ ⏎ start ◗" if shown.startswith("◆ pfm") else shown for shown in screens]
        with patch.object(chat, "screen", side_effect=screens), patch.object(chat, "keys") as owner_keys:
            chat.wizard(self.target, "claude", "3", "5m")
            self.assertEqual([["enter"], ["enter"], ["right"], ["right"], ["enter"], ["right"], ["enter"]],
                             [call.args[1] for call in owner_keys.call_args_list])
        observed = {"origin": "new-from-cli", "session_id": "actual-session"}
        with patch.object(chat, "fresh_shell", return_value=self.target), patch.object(chat, "scenario", return_value={"scenario_session_id": "00000000-0000-4000-8000-000000000002"}) as staging, \
             patch.object(chat, "before", return_value={"target": self.target, "seats": []}), \
             patch.object(chat, "capture", return_value=observed), patch.object(chat, "type_command") as owner_input:
            self.assertEqual(observed, chat.ensure(BRANCH, ORIGIN))
            self.assertEqual(ORIGIN, staging.call_args.args[2])
            owner_input.assert_called_once_with(self.target, ["pfm", "chat", "new", "--engine", "cc", "--backend", "herdr",
                "--account", "1", "--cwd", str(self.root / "seat/work/atlas"), "--name", "need-00000000-0000-4000-8000-000000000002"])

    def test_picker_credits_only_the_rendered_selected_session(self):
        previous = chat.before(self.target)
        self.seat("one")
        current = self.capture(self.target, BRANCH, ORIGIN, previous)
        screens = ["◆ pfm fixture\nfind › 2/2 match\n│  session foreign-session │", "◆ pfm fixture\nfind › 2/2 match\n│  session foreign-session │", "◆ pfm fixture\nfind › 2/2 match\n│  session one-session │"]
        with patch.object(chat, "screen", side_effect=screens), patch.object(chat, "fleet_rows", return_value=[{"id": "foreign-session"}, {"id": "one-session"}]), \
             patch.object(chat, "text") as owner_text, patch.object(chat, "keys") as owner_keys:
            self.assertEqual(current, chat.pick(self.target, BRANCH, ORIGIN, "ctrl+o"))
            owner_text.assert_called_once_with(self.target, "one-name")
            self.assertEqual([["ctrl+u"], ["down"], ["ctrl+o"]], [call.args[1] for call in owner_keys.call_args_list])
        state = {"query": "stale-query"}
        def input_text(_, value):
            state["query"] += value
        def input_keys(_, values):
            if values == ["ctrl+u"]:
                state["query"] = ""
        def picker_screen(_):
            identity = "one-session" if state["query"] == "one-name" else "foreign-session"
            return "◆ pfm fixture\nfind › 2/2 match\n│  session " + identity + " │"
        with patch.object(chat, "screen", side_effect=picker_screen), patch.object(chat, "keys", side_effect=input_keys), \
             patch.object(chat, "text", side_effect=input_text), patch.object(chat, "fleet_rows", return_value=[{"id": "foreign-session"}, {"id": "one-session"}]):
            self.assertEqual(current, chat.pick(self.target, BRANCH, ORIGIN))
            self.assertEqual(current, chat.pick(self.target, BRANCH, ORIGIN))
            self.assertEqual("one-name", state["query"])
        with patch.object(chat, "screen", side_effect=["journey$", "journey$", "◆ pfm fixture", "◆ pfm fixture\nfind › 2/2 match\n│  session one-session │"]), \
             patch.object(chat, "type_command") as owner_command, patch.object(chat, "keys"), \
             patch.object(chat, "text"), patch.object(chat, "fleet_rows", return_value=[{"id": "foreign-session"}, {"id": "one-session"}]):
            self.assertEqual(current, chat.pick(self.target, BRANCH, ORIGIN))
            owner_command.assert_called_once_with(self.target, ["pfm"])

    def test_killed_picker_uses_the_public_all_view(self):
        previous = chat.before(self.target)
        self.seat("killed")
        current = self.capture(self.target, BRANCH, ORIGIN, previous)
        declared = read_json(self.carry)
        declared["chats"][0]["expect"] = {"visibility": "killed"}
        write_json(self.carry, declared)
        screens = ["◆ pfm default", "journey$", "journey$", "◆ pfm all", "◆ pfm all\nfind › 2/2 match\n│  session killed-session │"]
        with patch.object(chat, "screen", side_effect=screens), patch.object(chat, "keys") as owner_keys, \
             patch.object(chat, "text"), patch.object(chat, "fleet_rows", return_value=[{"id": "killed-session"}]), \
             patch.object(chat, "type_command") as owner_command:
            self.assertEqual({**current, "expect": {"visibility": "killed"}}, chat.pick(self.target, BRANCH, ORIGIN))
            owner_command.assert_called_once_with(self.target, ["pfm", "ls", "--all"])
            self.assertEqual([["escape"], ["ctrl+u"]], [call.args[1] for call in owner_keys.call_args_list])

    def test_owned_tmux_shell_and_need_status(self):
        declared = read_json(self.carry)
        with patch.object(chat, "command", return_value="%7\n") as spawn, patch.object(chat, "screen", return_value="journey$\n"):
            self.assertEqual({"backend": "tmux", "socket": declared["owner"]["socket"], "pane": "%7"},
                             chat.fresh_shell("linux+tmux+worktree+claude"))
            spawn.assert_called_once_with(["tmux", "-S", declared["owner"]["socket"], "new-window", "-d", "-P", "-F", "#{pane_id}",
                "-t", "owner", "-c", str(self.root / "seat/work/atlas/.worktrees/flight-export"), "/bin/zsh", "-l"])
        previous = chat.before(self.target)
        self.seat("one")
        self.capture(self.target, BRANCH, ORIGIN, previous)
        for state, expected in (("idle", 0), ("working", 0), ("blocked", 0), ("ended", 1)):
            with self.subTest(state=state), patch.object(chat, "report", return_value=json.dumps({"state": state})):
                self.assertEqual(expected, chat.main("need", BRANCH, ORIGIN))

    def test_mcp_child_uses_the_actual_parent_edge_and_child_engine(self):
        branch = "linux+tmux+project+codex"
        parent = {"branch": branch, "key": "chat-birth:" + branch + ":new-typed", "session_id": "caller-session"}
        self.seat("child", backend="tmux")
        with sqlite3.connect(self.database) as connection:
            connection.execute("INSERT INTO children VALUES('new','caller-session',?)", (str(self.root / "tmux.sock"),))
        response = {"structuredContent": {"status": "ok", "code": 0}}
        records = self.root / "child-records"
        scenario_path = self.root / "child-scenario.json"
        write_json(scenario_path, {"record_dir": str(records)})
        display = {"identity": {"socket": "owned", "pane": "%child"}, "births": [{"pid": 42, "start": "fixture-birth",
            "chain": [{"argv": "claude --model sonnet"}], "environment": {"MOCK_ENGINE_SCENARIO": str(scenario_path)}}]}
        with patch.object(chat_carry, "observe_terminal", return_value=display):
            actual = chat.adopt_child(parent, "child-session", response)
        self.assertEqual({"key": "chat-mesh:" + branch + ":scout", "role": "scout", "branch": branch, "origin": "spawn-child-from-mcp",
            "engine": "claude", "place": "project", "terminal_kind": "tmux", "name": "child-name", "session_id": "child-session",
            "seat_id": "child", "terminal": {"backend": "tmux", "socket": str(self.root / "tmux.sock"), "pane": "%child"},
            "project": "atlas", "workspace": "w1", "account": 2, "cache1h": 1, "parent_key": parent["key"], "parent": "caller-session",
            "scenario": str(scenario_path), "records": str(records)}, actual)
        self.seat("ready", backend="tmux")
        with patch.object(chat_carry, "observe_terminal", return_value=display):
            readiness = chat.adopt_child(parent, "ready-session", response, "readiness")
        self.assertEqual(("chat-mesh:" + branch + ":readiness", "spawn-child-from-mcp", "caller-session"),
                         (readiness["key"], readiness["origin"], readiness["parent"]))
        receipt = str(self.root / "branch")
        Path(receipt + ".status").write_text("0\n")
        Path(receipt + ".out").write_text("Branched caller… as \"fork\" into detached socket tmux.sock — waiting in pfm ls; open later.\n")
        self.seat("fork", backend="tmux")
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET state='ended' WHERE seat_id='child'")
            connection.execute("INSERT INTO meta VALUES('branch-seat:tmux.sock','caller-session')")
        with patch.object(chat_carry, "observe_terminal", return_value=display):
            fork = chat.adopt_branch({**parent, "project": "cobalt"}, "branch-from-cli", "fork-session", receipt)
        self.assertEqual(("branch-from-cli", "fork-session", "caller-session", "cobalt"),
                         (fork["origin"], fork["session_id"], fork["parent"], fork["project"]))
        with self.assertRaisesRegex(ValueError, "CHAT-BRANCH-PARENT"):
            chat.adopt_branch({**parent, "session_id": "foreign", "project": "cobalt"}, "bad-branch", "fork-session", receipt)
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET state='live' WHERE seat_id='child'")
        with self.assertRaisesRegex(ValueError, "CHAT-CHILD-REFUSED"):
            chat.adopt_child(parent, "child-session", {"structuredContent": {"status": "error", "code": 3}})
        with patch.object(chat_carry, "observe_terminal", return_value=display), self.assertRaisesRegex(ValueError, "CHAT-CHILD-PARENT"):
            chat.adopt_child({**parent, "session_id": "foreign-parent"}, "child-session", response)
        with self.assertRaisesRegex(ValueError, "CHAT-CHILD-SELF"):
            chat.adopt_child({**parent, "session_id": "child-session"}, "child-session", response)
        with self.assertRaisesRegex(ValueError, "CHAT-CHILD-SEAT"):
            chat.adopt_child(parent, "invented-session", response)
        write_json(self.root / "different-scenario.json", {"record_dir": str(self.root / "other-records")})
        display["births"].append({"pid": 43, "start": "another-birth", "chain": [{"argv": "claude --model opus"}],
            "environment": {"MOCK_ENGINE_SCENARIO": str(self.root / "different-scenario.json")}})
        with patch.object(chat_carry, "observe_terminal", return_value=display), self.assertRaisesRegex(ValueError, "CHAT-SCENARIO-AMBIGUOUS"):
            chat.observed_scenario(self.target, "claude")

    def test_cli_child_uses_owner_receipt_tsv_and_actual_parent_edge(self):
        branch = "linux+tmux+project+codex"
        parent = {"branch": branch, "key": "chat-birth:" + branch + ":new-typed", "session_id": "caller-session",
                  "terminal": {"backend": "tmux", "socket": str(self.root / "tmux.sock"), "pane": "%caller"}}
        self.seat("cli-child", backend="tmux")
        with sqlite3.connect(self.database) as connection:
            connection.execute("INSERT INTO children VALUES('new','caller-session',?)", (str(self.root / "tmux.sock"),))
        receipt = self.root / "cli-child"
        Path(str(receipt) + ".status").write_text("0\n")
        Path(str(receipt) + ".out").write_text(
            "claude\tcli-child-name\ttmux.sock\tnamed\tlisted\n"
            "attach: tmux -L tmux.sock attach -t cli-child-session\n")
        scenario_path = self.root / "cli-child-scenario.json"
        write_json(scenario_path, {"record_dir": str(self.root / "cli-child-records")})
        display = {"identity": {"socket": "owned", "pane": "%cli-child"}, "births": [{
            "pid": 42, "start": "fixture-birth", "chain": [{"argv": "claude --model sonnet"}],
            "environment": {"MOCK_ENGINE_SCENARIO": str(scenario_path)}}]}
        with self.assertRaisesRegex(ValueError, "requested session is not in public output"):
            chat.adopt_cli_child(parent, "invented-session", str(receipt), "cli")
        with patch.object(chat_carry, "observe_terminal", return_value=display):
            actual = chat.adopt_cli_child(parent, "cli-child-session", str(receipt), "cli")
        self.assertEqual(("spawn-child-from-cli", "cli", "cli-child-session", "caller-session"),
                         (actual["origin"], actual["role"], actual["session_id"], actual["parent"]))
        self.assertEqual({"backend": "tmux", "socket": str(self.root / "tmux.sock"), "pane": "%cli-child"}, actual["terminal"])

    def test_concurrent_cobalt_launches_bind_their_own_native_process(self):
        declared = read_json(self.carry)
        declared["workspaces"]["cobalt"] = {"id": "w8"}
        write_json(self.carry, declared)
        staged, previous = [], chat.before(self.target)
        fixture = self.root / "scenario.json"
        for number in range(8):
            origin = "storm-" + str(number)
            with patch.object(chat, "type_command"):
                metadata = chat.scenario(self.target, BRANCH, origin, fixture, "cobalt", "cobalt")
            staged.append(metadata)
            identity = "storm" + str(number)
            self.seat(identity, workspace="w8")
            self.birth_scenarios["w8:p" + identity] = metadata["scenario"]
        self.seat("other-branch", workspace="w8")
        try:
            actual = [chat.capture(self.target, BRANCH, "storm-" + str(number), previous, "cobalt", "cobalt") for number in range(8)]
        except ValueError as error:
            self.fail("independent native launches must capture without ambiguity: " + str(error))
        self.assertEqual([("storm" + str(n), "storm" + str(n) + "-session", "cobalt", "w8") for n in range(8)],
                         [(row["seat_id"], row["session_id"], row["project"], row["workspace"]) for row in actual])
        self.assertEqual([metadata["scenario_session_id"] for metadata in staged], [row["scenario_session_id"] for row in actual])
        staged_value = read_json(staged[0]["scenario"])
        write_json(staged[0]["scenario"], {**staged_value, "session_id": "tampered"})
        with self.assertRaisesRegex(ValueError, "CHAT-CAPTURE-SCENARIO"):
            chat.capture(self.target, BRANCH, "storm-0", previous, "cobalt", "cobalt")
        write_json(staged[0]["scenario"], staged_value)
        with self.assertRaisesRegex(ValueError, "CHAT-CAPTURE-SCOPE"):
            chat.capture(self.target, BRANCH, "storm-0", previous, "atlas", "cobalt")

    def test_reconciled_workspace_label_binds_the_actual_destination(self):
        self.seat("moved", workspace="w9")
        write_json(chat.metadata_path(BRANCH, "mixed"), {"scenario": str(self.root / "scenario.json"),
            "project": "cobalt-mixed", "workspace": "label:cobalt-mixed"})
        display = {"identity": {"pane": "w9:pmoved"}, "workspace": {"workspace": {"label": "cobalt-mixed"}},
            "births": [{"pid": 42, "start": "fixture", "chain": [{"argv": "claude"}],
                        "environment": {"MOCK_ENGINE_SCENARIO": str(self.root / "scenario.json")}}]}
        previous = {"target": self.target, "seats": []}
        with patch.object(chat_carry, "observe_terminal", return_value=display):
            display["workspace"]["workspace"]["label"] = "foreign"
            self.assertIsNone(chat.capture(self.target, BRANCH, "mixed", previous))
            display["workspace"]["workspace"]["label"] = "cobalt-mixed"
            actual = chat.capture(self.target, BRANCH, "mixed", previous)
            self.assertEqual(("cobalt-mixed", "w9"), (actual["project"], actual["workspace"]))

    def test_distinct_cli_roles_and_reload_annotations(self):
        previous = chat.before(self.target)
        for role in ("caller", "recipient", "dead-scratch"):
            self.seat(role)
            with patch.object(chat, "type_command"):
                staged = chat.stage_role(self.target, BRANCH, role, self.root / "scenario.json")
            self.birth_scenarios["w1:p" + role] = staged["scenario"]
            captured = self.capture(self.target, BRANCH, role, previous)
            self.assertEqual(("new-from-cli", role, role + "-session"),
                             (captured["origin"], captured["role"], (chat.select(BRANCH, role) or {}).get("session_id")))
            previous = chat.before(self.target)
        self.assertEqual(["caller", "recipient", "dead-scratch"], [row["seat_id"] for row in read_json(self.carry)["chats"]])
        launches = iter((("created", "worker"), ("unnamed", "need-00000000-0000-4000-8000-000000000001"),
                         ("explicit-empty", "")))
        def launch(target, arguments):
            if arguments[:3] == ["pfm", "chat", "new"]:
                identity, expected_name = next(launches)
                self.assertEqual(["--name", expected_name], arguments[-2:])
                self.seat(identity)
                with sqlite3.connect(self.database) as connection:
                    connection.execute("UPDATE seat SET name=? WHERE seat_id=?", (expected_name, identity))
                self.birth_scenarios["w1:p" + identity] = read_json(chat.metadata_path(BRANCH, identity))["scenario"]
        with patch.object(chat, "fresh_shell", return_value=self.target), patch.object(chat, "type_command", side_effect=launch) as input_command:
            created = chat.ensure_role(BRANCH, "created", "worker", self.root / "scenario.json")
            self.assertEqual(("new-from-cli", "created", "created-session", "chat-role:" + BRANCH + ":created"),
                             (created["origin"], created["role"], created["session_id"], created["key"]))
            self.assertEqual(["--name", "worker"], input_command.call_args.args[1][-2:])
            with patch.object(chat.uuid, "uuid4", return_value="00000000-0000-4000-8000-000000000001"):
                chat.ensure_role(BRANCH, "unnamed", None, self.root / "scenario.json")
            self.assertEqual("need-00000000-0000-4000-8000-000000000001", input_command.call_args.args[1][-1])
            empty = chat.ensure_role(BRANCH, "explicit-empty", "", self.root / "scenario.json")
            self.assertEqual(("", "explicit-empty-session", "chat-role:" + BRANCH + ":explicit-empty"),
                             (empty["name"], empty["session_id"], empty["key"]))
        declared = read_json(self.carry)
        unkeyed = {"session_id": "root-session", "name": "root actor"}
        declared["chats"].append(unkeyed)
        write_json(self.carry, declared)
        annotated = chat.annotate(BRANCH, "caller", {"reload_records": True})
        self.assertEqual(unkeyed, read_json(self.carry)["chats"][-1])
        with sqlite3.connect(self.database) as connection:
            connection.execute("UPDATE seat SET seat_id='renewed',session_id='continuation' WHERE seat_id='caller'")
        updated = chat.refresh(BRANCH, "caller")
        self.assertEqual({**annotated, "seat_id": "renewed", "session_id": "continuation", "account": None,
                          "cache1h": None, "prior_seat_ids": ["caller"], "lineage_ids": ["caller-session"]}, updated)
        for values in ({"reload_records": "yes"}, {"session_id": "invented"}):
            with self.assertRaisesRegex(ValueError, "CHAT-ANNOTATE"):
                chat.annotate(BRANCH, "caller", values)



if __name__ == "__main__":
    unittest.main()
