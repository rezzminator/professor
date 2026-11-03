#!/usr/bin/env python3
"""Reload checks reject wrong lineage, a second live seat, and stale badges."""
import copy
from pathlib import Path
import sys
import unittest

sys.path.append(str(Path(__file__).resolve().parents[2]))
from projection import (attention as projection, attention_invariants, account_caches, birth_presence, checks,
                        metadata_checks, metadata_visible, settled_metadata)


class ReloadProjection(unittest.TestCase):
    def test_account_cache_aliases_and_symlinks_remain_visible(self):
        one = {"state": "regular-file", "regular": True, "symlink": False,
               "identity": {"device": 1, "inode": 101}}
        for two, expected in [
                ({**one, "identity": {"device": 1, "inode": 202}},
                 {"one": ["regular-file", True, False], "two": ["regular-file", True, False], "identity": "distinct"}),
                (one, {"one": ["regular-file", True, False], "two": ["regular-file", True, False], "identity": "shared"}),
                ({**one, "state": "symlink", "regular": False, "symlink": True},
                 {"one": ["regular-file", True, False], "two": ["symlink", False, True], "identity": "shared"})]:
            with self.subTest(two=two):
                self.assertEqual(expected, account_caches(one, two))

    def setUp(self):
        self.carry = {"name": "atlas", "engine": "claude", "session_id": "session-atlas", "account": 1, "cache1h": 1}
        self.chat = {
            "name": "atlas", "terminal": {"identity": {"pane": "atlas-pane"}, "births": []},
            "process_births": [{"pid": 101, "start": "fixture-birth-before", "chain": [{"argv": "claude --resume session-atlas"}],
                                "environment": {"CLAUDE_CONFIG_DIR": "/fixture/.cc/1"}}],
            "status": {"state": "idle", "cwd": "/fixture/atlas", "model": "fixture-model",
                       "engine": "cc", "session_id": "session-atlas"},
            "rows": [{"id": "session-atlas", "name": "atlas", "project": "atlas", "account": "1"}],
            "picker": [{"engine": "cc", "project": "atlas", "backend": "herdr",
                        "address": {"pane": "atlas-pane"}, "account": 1, "badge": {"state": "idle"}}],
            "stored": {"launch": [{"session_id": "session-atlas", "engine": "cc", "account": 1, "cache1h": 1}],
                       "seat": [{"session_id": "session-atlas", "state": "live", "reload_pending": 0}],
                       "hidden": []}}
        self.before = {"chats": [self.chat], "foreign": {"session_id": "session-cobalt"}}

    def test_metadata_setup_learns_model_and_preserves_the_carried_identity(self):
        for name, model, expected in [("atlas", "fixture-model", True), ("", "fixture-model", False),
                                      ("atlas", "", False), ("atlas", "n/a:unreported-model", False)]:
            self.assertEqual(expected, settled_metadata({"model": model}, {"name": name}))
        for state, name, frame, expected in [("idle", "settled-atlas", "⏺ reload metadata ready\n", True),
                                            ("working", "settled-atlas", "⏺ reload metadata ready\n", False),
                                            ("idle", "wrong", "⏺ reload metadata ready\n", False),
                                            ("idle", "settled-atlas", "⏺ wrong reply\n", False)]:
            self.assertEqual(expected, metadata_visible({"state": state}, {"name": name}, frame, "settled-atlas"))
        after = copy.deepcopy(self.before)
        after["chats"][0]["status"]["model"] = "newly-reported-model"
        after["chats"][0]["rows"][0]["name"] = "settled-atlas"
        rows = metadata_checks(self.before, after, "session-atlas", "claude", "settled-atlas")
        self.assertEqual([], [row for row in rows if row["expected"] != row["actual"]])
        after["chats"][0]["process_births"][0]["start"] = "wrong-birth"
        self.assertEqual({"field": "birth", "expected": {"pid": 101, "start": "fixture-birth-before"},
                          "actual": {"pid": 101, "start": "wrong-birth"}},
                         next(row for row in metadata_checks(self.before, after, "session-atlas", "claude", "settled-atlas")
                              if row["field"] == "birth"))

    def test_wrong_surfaces_keep_their_actual_value(self):
        after = copy.deepcopy(self.before)
        chat = after["chats"][0]
        chat["terminal"]["identity"]["pane"] = "foreign-pane"
        chat["process_births"][0].update(pid=202, start="fixture-birth-after")
        chat["picker"][0]["badge"]["state"] = "blocked"
        chat["stored"]["seat"].append({"session_id": "retired-session", "state": "live", "reload_pending": 1})
        result = {row["field"]: row for row in checks(self.before, after, self.carry, self.carry, "resume")}
        self.assertEqual({"field": "terminal", "expected": {"pane": "atlas-pane"},
                          "actual": {"pane": "foreign-pane"}}, result["terminal"])
        self.assertEqual({"field": "picker-badge", "expected": "idle", "actual": "blocked"}, result["picker-badge"])
        self.assertEqual({"field": "engine-terminal", "expected": {"pane": "atlas-pane"},
                          "actual": {"pane": "foreign-pane"}}, result["engine-terminal"])
        self.assertEqual([{"session_id": "session-atlas", "reload_pending": 0},
                          {"session_id": "retired-session", "reload_pending": 1}], result["live-seat"]["actual"])
        after["foreign"]["session_id"] = "wrong-foreign"
        chat["rows"][0]["account"] = "2"
        unchanged = {row["field"]: row for row in attention_invariants(self.before, after, "session-atlas", "claude")}
        self.assertEqual({"field": "birth", "expected": {"pid": 101, "start": "fixture-birth-before"},
                          "actual": {"pid": 202, "start": "fixture-birth-after"}}, unchanged["birth"])
        self.assertEqual({"field": "account", "expected": "1", "actual": "2"}, unchanged["account"])
        self.assertEqual({"field": "terminal", "expected": {"pane": "atlas-pane"},
                          "actual": {"pane": "foreign-pane"}}, unchanged["terminal"])
        self.assertEqual({"field": "foreign", "expected": {"session_id": "session-cobalt"},
                          "actual": {"session_id": "wrong-foreign"}}, unchanged["foreign"])
        for bundle, expected in ((self.before, "preserved"), (after, "replaced")):
            restored = {row["field"]: row for row in attention_invariants(self.before, bundle, "session-atlas", "claude", "restore")}
            self.assertEqual({"field": "birth", "expected": "replaced", "actual": expected}, restored["birth"])
        with self.assertRaisesRegex(ValueError, "ATTENTION-TRANSITION"):
            attention_invariants(self.before, after, "session-atlas", "claude", "wrong-transition")

    def test_fresh_hidden_pins_exact_predecessor(self):
        after = copy.deepcopy(self.before)
        chat = after["chats"][0]
        chat["status"]["session_id"] = "session-fresh"
        chat["rows"][0]["id"] = "session-fresh"
        for table in ("launch", "seat"):
            chat["stored"][table][0]["session_id"] = "session-fresh"
        current = {**self.carry, "session_id": "session-fresh", "lineage_ids": ["session-atlas"]}
        for hidden, expected in [([], False), ([{"uuid": "foreign-session"}], False), ([{"uuid": "session-atlas"}], True)]:
            chat["stored"]["hidden"] = hidden
            rows = {row["field"]: row for row in checks(self.before, after, self.carry, current, "fresh-hidden")}
            self.assertEqual({"field": "predecessor-hidden", "expected": True, "actual": expected}, rows["predecessor-hidden"])
            self.assertEqual({"field": "predecessor", "expected": "session-atlas", "actual": "session-atlas"}, rows["predecessor"])
        for tail in ("session-fresh", "wrong-tail"):
            indexed = {"launch_id": "session-atlas", "tail_id": tail}
            rows = {row["field"]: row for row in checks(self.before, after, self.carry, current, "continuation", indexed)}
            self.assertEqual({"field": "continuation-tail", "expected": tail, "actual": "session-fresh"}, rows["continuation-tail"])
            self.assertEqual({"field": "continuation-launch", "expected": "session-atlas", "actual": "session-atlas"}, rows["continuation-launch"])
            self.assertEqual({"field": "lineage-mode", "expected": "continued", "actual": "continued"}, rows["lineage-mode"])
        with self.assertRaisesRegex(ValueError, "RELOAD-CONTINUATION"):
            checks(self.before, after, self.carry, current, "continuation")

    def test_birth_reuse_and_account_change_require_both_identity_and_environment(self):
        for pid, start, home, transition in [
                (202, "fixture-birth-after", "/fixture/.cc/2", "replaced"),
                (101, "fixture-birth-before", "/fixture/.cc/1", "preserved"),
                (101, "fixture-birth-after", "/fixture/.cc/2", "replaced")]:
            after = copy.deepcopy(self.before)
            after["chats"][0]["process_births"][0].update(pid=pid, start=start, environment={"CLAUDE_CONFIG_DIR": home})
            rows = {row["field"]: row for row in checks(self.before, after, self.carry, self.carry, "account")}
            with self.subTest(pid=pid, start=start, home=home):
                self.assertEqual("present" if transition == "preserved" else "ended",
                                 birth_presence({"pid": 101, "start": "fixture-birth-before"}, {pid: {"start": start}}))
                self.assertEqual({"field": "engine-birth", "expected": "replaced", "actual": transition}, rows["engine-birth"])
                self.assertEqual({"field": "engine-home", "expected": "/fixture/.cc/2", "actual": home}, rows["engine-home"])



class AttentionProjection(unittest.TestCase):
    def test_disagreeing_surfaces_are_not_replaced_by_reported_state(self):
        chat = {"name": "atlas", "status": {"state": "idle", "session_id": "session-atlas"}, "rows": [{"id": "session-atlas"}],
                "picker": [{"state": "working", "badge": {"state": "blocked"}}],
                "terminal": {"backend": "herdr", "tab": {"tab": {"label": "[W] atlas"}},
                             "pane": {"agent_status": "working", "tokens": {"pfm_blocked": "●"}}}}
        expected = {"reported_state": "idle", "picker_state": "working", "badge_state": "blocked",
                    "herdr_state": "working", "tab_tag": "[W]", "indicator": "pfm_blocked"}
        self.assertEqual(expected, projection({"chats": [chat]}, "session-atlas"))
        other = copy.deepcopy(chat)
        other["status"]["session_id"] = "other-session"
        other["picker"][0]["badge"]["state"] = "error"
        self.assertEqual(expected, projection({"chats": [other, chat]}, "session-atlas"))
        display = chat["terminal"]
        chat["terminal"] = {"backend": "tmux"}
        self.assertEqual({**expected, "herdr_state": "n/a:tmux", "tab_tag": "n/a:tmux",
                          "indicator": "n/a:tmux"}, projection({"chats": [chat]}, "session-atlas"))
        self.assertEqual(expected, projection({"chats": [chat]}, "session-atlas", display))
        with self.assertRaises(KeyError):
            projection({"chats": [chat]}, "session-atlas", {})

    def test_missing_and_ambiguous_chat_or_picker_are_errors(self):
        base = {"name": "atlas", "status": {"state": "idle", "session_id": "session-atlas"}, "rows": [{"id": "session-atlas"}],
                "picker": [{"state": "idle", "badge": {"state": "idle"}}],
                "terminal": {"backend": "herdr", "tab": {"tab": {"label": "atlas"}},
                             "pane": {"agent_status": "idle"}}}
        self.assertEqual({"reported_state": "idle", "picker_state": "idle", "badge_state": "idle",
                          "herdr_state": "idle", "tab_tag": "", "indicator": "none"},
                         projection({"chats": [base]}, "session-atlas"))
        for chats, message in [([], "ATTENTION-CHAT"), ([base, base], "ATTENTION-CHAT")]:
            with self.subTest(chats=chats), self.assertRaisesRegex(ValueError, message):
                projection({"chats": chats}, "session-atlas")
        for rows in ([], base["picker"] * 2):
            bad = copy.deepcopy(base)
            bad["picker"] = rows
            with self.subTest(rows=rows), self.assertRaisesRegex(ValueError, "ATTENTION-PICKER"):
                projection({"chats": [bad]}, "session-atlas")



if __name__ == "__main__":
    unittest.main()
