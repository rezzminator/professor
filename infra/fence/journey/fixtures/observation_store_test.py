"""Read-only recipe, lineage and schedule projections over actual SQLite."""
import json
import os
import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import journey_path
import observation
from runtime import write_json


class ObservationStore(unittest.TestCase):
    def database(self, root):
        path = root / "pfm.db"
        with sqlite3.connect(path) as db:
            db.executescript('''
CREATE TABLE seat(seat_id TEXT PRIMARY KEY, session_id TEXT, engine TEXT, backend TEXT,
 tmux_socket TEXT, tmux_pane TEXT, herdr_socket TEXT, herdr_pane TEXT, herdr_workspace TEXT,
 name TEXT, state TEXT, reload_pending INTEGER, lost_note TEXT, updated_at INTEGER);
CREATE TABLE chat(uuid TEXT PRIMARY KEY,cwd TEXT,label TEXT,prompts INTEGER);
CREATE TABLE launch(session_id TEXT PRIMARY KEY,engine TEXT,account INTEGER,cache1h INTEGER,launched_at INTEGER,updated_at INTEGER);
CREATE TABLE hidden(uuid TEXT PRIMARY KEY,hidden_at INTEGER,at_payload INTEGER,revision TEXT NOT NULL DEFAULT '');
CREATE TABLE children(kind TEXT,key TEXT,val TEXT,created_at INTEGER);
CREATE TABLE meta(key TEXT PRIMARY KEY,val TEXT,updated_at INTEGER);
CREATE TABLE schedules(id INTEGER PRIMARY KEY,session_id TEXT,prompt TEXT);
INSERT INTO seat VALUES('carried-seat','carried-session','cc','herdr','','','fixture.sock','w1:p1','w1','carried-chat','live',1,'',7);
INSERT INTO seat VALUES('foreign-seat','foreign-session','cc','herdr','','','fixture.sock','w2:p1','w2','foreign-chat','live',0,'',8);
INSERT INTO chat VALUES('carried-session','/fixture/atlas','carried-chat',1);
INSERT INTO chat VALUES('foreign-session','/fixture/cobalt','foreign-chat',2);
INSERT INTO launch VALUES('carried-session','cc',1,1,3,4);
INSERT INTO launch VALUES('foreign-session','cc',3,1,3,4);
INSERT INTO children VALUES('new','carried-session','child-session',5);
INSERT INTO children VALUES('new','foreign-session','foreign-child',6);
INSERT INTO meta VALUES('branch-seat:carried-socket','parent-session',9);
INSERT INTO meta VALUES('branch-seat:foreign-socket','foreign-parent',9);
INSERT INTO schedules VALUES(1,'carried-session','fixture-prompt');
INSERT INTO schedules VALUES(2,'foreign-session','foreign-prompt');
''')
        write_json(root / "seats/carried-seat.json", {"binary": "/fixture/claude", "args": ["--resume", "carried-session"],
                                                     "env": ["CLAUDE_CONFIG_DIR=/fixture/account"], "cwd": "/fixture/atlas", "cost_ledger": False})
        return path

    def test_store_reads_recipe_lineage_and_owned_schedule(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            database = self.database(root)
            chat = {"name": "carried-chat", "session_id": "carried-session", "seat_id": "carried-seat",
                    "socket": "carried-socket", "schedule_ids": [1], "terminal": {"backend": "herdr", "pane": "w1:p1"}}
            transcript = {"name": "carried-chat", "engine": "cc", "path": "/fixture/carried-session.jsonl", "entries": [], "truncated": False}
            with patch.dict(os.environ, {"PFM_STATE_DB": str(database)}), patch.object(observation, "command", return_value=json.dumps(transcript)):
                actual = observation.stored(chat)
            self.assertEqual([{"id": 1, "session_id": "carried-session", "prompt": "fixture-prompt"}], actual["schedules"])
            self.assertEqual({"binary": "/fixture/claude", "args": ["--resume", "carried-session"],
                              "env": ["CLAUDE_CONFIG_DIR=/fixture/account"], "cwd": "/fixture/atlas", "cost_ledger": False}, actual["recipe"])
            self.assertEqual([{"kind": "new", "key": "carried-session", "val": "child-session", "created_at": 5}], actual["children"])
            self.assertEqual([{"key": "branch-seat:carried-socket", "val": "parent-session", "updated_at": 9}], actual["lineage"])
            self.assertEqual(transcript, actual["transcript"])
            with sqlite3.connect(database) as db:
                db.executemany("INSERT INTO hidden VALUES(?,?,?,?)", [
                    ("carried-session", 12, None, "carried-revision"),
                    ("prior-session", 11, 4, "prior-revision"),
                    ("foreign-session", 10, None, "foreign-revision")])
            with patch.dict(os.environ, {"PFM_STATE_DB": str(database)}), patch.object(observation, "command", return_value=json.dumps(transcript)):
                actual = observation.stored({**chat, "lineage_ids": ["prior-session"]})
            self.assertEqual([{"uuid": "carried-session", "hidden_at": 12, "at_payload": None, "revision": "carried-revision"},
                              {"uuid": "prior-session", "hidden_at": 11, "at_payload": 4, "revision": "prior-revision"}], actual["hidden"])
            with sqlite3.connect(database) as db:
                self.assertEqual([(1, "carried-session", "fixture-prompt"), (2, "foreign-session", "foreign-prompt")], db.execute("SELECT * FROM schedules ORDER BY id").fetchall())
            (root / "seats/carried-seat.json").unlink()
            with patch.dict(os.environ, {"PFM_STATE_DB": str(database)}), patch.object(observation, "command", return_value=json.dumps(transcript)):
                with self.assertRaises(FileNotFoundError):
                    observation.stored(chat)
                with sqlite3.connect(database) as db:
                    db.execute("UPDATE seat SET backend='tmux',tmux_socket='carried-socket',tmux_pane='%0' WHERE seat_id='carried-seat'")
                actual = observation.stored({**chat, "terminal": {"backend": "tmux", "socket": "carried-socket", "pane": "%0"}})
            self.assertEqual("n/a:tmux-launch-record", actual["recipe"])
            self.assertEqual([("carried-seat", "carried-session", "tmux")],
                             [(seat["seat_id"], seat["session_id"], seat["backend"]) for seat in actual["seat"]])
            self.assertEqual(transcript, actual["transcript"])

    def test_foreign_schedule_cannot_be_claimed_by_carried_chat(self):
        with tempfile.TemporaryDirectory() as scratch:
            database = self.database(Path(scratch))
            chat = {"name": "carried-chat", "session_id": "carried-session", "seat_id": "carried-seat", "schedule_ids": [2]}
            with patch.dict(os.environ, {"PFM_STATE_DB": str(database)}), patch.object(observation, "command", return_value="{}"):
                with self.assertRaisesRegex(ValueError, "OBSERVE-SCHEDULE-OWNERSHIP: 2"):
                    observation.stored(chat)

    def test_unset_codex_lineage_does_not_read_foreign_rollouts(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            database = self.database(root)
            cache = root / "cache.db"
            with sqlite3.connect(cache) as db:
                db.executescript("CREATE TABLE rollouts(id TEXT,session_id TEXT,lineage_root TEXT); INSERT INTO rollouts VALUES('carried-session','',''),('foreign-session','','');")
            chat = {"name": "carried-chat", "session_id": "carried-session", "seat_id": "carried-seat"}
            with patch.dict(os.environ, {"PFM_STATE_DB": str(database), "PFM_CACHE_DB": str(cache)}), patch.object(observation, "command", return_value='{"engine":"cx"}'):
                actual = observation.stored(chat, "cx")
            self.assertEqual([{"id": "carried-session", "session_id": "", "lineage_root": ""}], actual["engine_lineage"])


if __name__ == "__main__":
    unittest.main()
