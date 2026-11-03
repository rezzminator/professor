#!/usr/bin/env python3
"""Continuation observations use the real cached transcript index."""
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import chat_carry
import continuation
import observation
import observation_process


class ContinuationIndex(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory(prefix="continuation-index-")
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.cache = self.root / "pfm-cache.db"
        self.state = self.root / "pfm.db"
        with sqlite3.connect(self.cache) as database:
            database.execute("CREATE TABLE transcripts(uuid TEXT PRIMARY KEY, path TEXT NOT NULL, continued_in TEXT NOT NULL)")
        with sqlite3.connect(self.state) as database:
            database.execute("CREATE TABLE marker(value TEXT NOT NULL)")
            database.execute("INSERT INTO marker VALUES ('state')")
        self.environment = patch.dict(os.environ, {
            "JOURNEY_ROOT": str(self.root),
            "HOME": str(self.root / "home"),
            "PFM_CACHE_DB": str(self.cache),
            "PFM_STATE_DB": str(self.state),
        })
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def rows(self, *values):
        with sqlite3.connect(self.cache) as database:
            database.executemany("INSERT INTO transcripts VALUES (?, ?, ?)", values)

    def test_indexed_returns_actual_chain_tail_and_path(self):
        paths = [self.root / name for name in ("launch.jsonl", "middle.jsonl", "tail.jsonl")]
        for path in paths:
            path.write_text(path.name)
        self.rows(("launch", str(paths[0]), "middle"),
                  ("middle", str(paths[1]), "tail"),
                  ("tail", str(paths[2]), ""))

        self.assertEqual({
            "launch_id": "launch", "tail_id": "tail", "path": str(paths[2]),
            "chain": [
                {"uuid": "launch", "path": str(paths[0]), "continued_in": "middle"},
                {"uuid": "middle", "path": str(paths[1]), "continued_in": "tail"},
                {"uuid": "tail", "path": str(paths[2]), "continued_in": ""},
            ],
        }, continuation.indexed({"session_id": "launch"}))

    def test_cycle_stops_at_last_new_row_and_missing_successor_stays_on_predecessor(self):
        cycle_a, cycle_b, orphan = (self.root / name for name in ("a.jsonl", "b.jsonl", "orphan.jsonl"))
        for path in (cycle_a, cycle_b, orphan):
            path.write_text(path.name)
        self.rows(("a", str(cycle_a), "b"), ("b", str(cycle_b), "a"),
                  ("orphan", str(orphan), "not-indexed"))

        self.assertEqual("b", continuation.indexed({"session_id": "a"})["tail_id"])
        self.assertEqual(["a", "b"], [row["uuid"] for row in continuation.indexed({"session_id": "a"})["chain"]])
        missing = continuation.indexed({"session_id": "orphan"})
        self.assertEqual(("orphan", str(orphan)), (missing["tail_id"], missing["path"]))
        self.assertEqual(["orphan"], [row["uuid"] for row in missing["chain"]])

    def test_self_continuation_is_not_superseded(self):
        path = self.root / "self.jsonl"
        path.write_text("self")
        self.rows(("self", str(path), "self"))
        actual = continuation.indexed({"session_id": "self"})
        self.assertEqual(("self", str(path)), (actual["tail_id"], actual["path"]))
        self.assertEqual(["self"], [row["uuid"] for row in actual["chain"]])

    def test_missing_launch_is_its_own_empty_path_without_filesystem_search(self):
        actual = continuation.indexed({"session_id": "not-indexed"})
        self.assertEqual({"launch_id": "not-indexed", "tail_id": "not-indexed", "path": "", "chain": []}, actual)

    def test_empty_indexed_tail_path_and_foreign_path_are_distinct_errors(self):
        self.rows(("empty", "", ""))
        with self.assertRaisesRegex(ValueError, "^CONTINUATION-PATH-EMPTY:"):
            continuation.indexed({"session_id": "empty"})
        foreign = Path(tempfile.mkdtemp(prefix="continuation-foreign-")) / "tail.jsonl"
        try:
            with sqlite3.connect(self.cache) as database:
                database.execute("INSERT INTO transcripts VALUES (?, ?, ?)", ("foreign", str(foreign), ""))
            with self.assertRaisesRegex(ValueError, "^CONTINUATION-PATH:"):
                continuation.indexed({"session_id": "foreign"})
        finally:
            foreign.parent.rmdir()

    def test_stored_defaults_to_state_and_cache_is_explicit(self):
        self.assertEqual([{"value": "state"}], chat_carry.stored("SELECT value FROM marker"))
        with sqlite3.connect(self.cache) as database:
            database.execute("CREATE TABLE marker(value TEXT NOT NULL)")
            database.execute("INSERT INTO marker VALUES ('cache')")
        self.assertEqual([{"value": "cache"}], chat_carry.stored("SELECT value FROM marker", cache=True))

    def test_native_reads_exact_live_record_and_distinguishes_absent_from_unreadable(self):
        config = self.root / "home/.cc/3"
        sessions = config / "sessions"
        sessions.mkdir(parents=True)
        target = {"backend": "tmux", "socket": str(self.root / "tmux.sock"), "pane": "%1"}
        display = {"identity": {"pane": "%1"}, "births": []}
        birth = {"pid": 42, "start": "fixture-start", "environment": {
            "PFM_RUN": "synthetic", "CLAUDE_CONFIG_DIR": str(config)}, "argv": ["claude"]}
        with patch.dict(os.environ, {"JOURNEY_ROOT": str(self.root), "PFM_RUN": "synthetic"}), \
                patch.object(observation, "terminal", return_value=display), \
                patch.object(observation, "terminal_births", return_value=[birth]), \
                patch.object(observation_process, "engine_births", return_value=[birth]), \
                patch.object(observation_process, "environment", return_value=birth["environment"]), \
                patch.object(observation, "process_snapshot", return_value={42: {"pid": 42, "start": "fixture-start"}}):
            chat = {"terminal": target, "session_id": "scenario-id"}
            self.assertEqual("n/a:native-session-absent", continuation.native(chat))
            record = {"pid": 42, "sessionId": "actual-session", "cwd": str(self.root / "project"),
                      "kind": "interactive", "scenario": "scenario-id"}
            (sessions / "42.json").write_text(json.dumps(record))
            self.assertEqual({"path": str(sessions / "42.json"), "pid": 42,
                              "sessionId": "actual-session", "cwd": str(self.root / "project")},
                             continuation.native(chat))
            with patch.object(observation, "process_snapshot", return_value={42: {"pid": 42, "start": "recycled"}}), \
                    self.assertRaisesRegex(ValueError, r"^CONTINUATION-NATIVE-BIRTH:"):
                continuation.native(chat)
            (sessions / "42.json").write_text("{")
            with self.assertRaisesRegex(ValueError, r"^CONTINUATION-NATIVE-READ:"):
                continuation.native(chat)


if __name__ == "__main__":
    unittest.main()
