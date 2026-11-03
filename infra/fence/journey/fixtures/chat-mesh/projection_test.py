#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import tempfile
import unittest


def module(path):
    spec = importlib.util.spec_from_file_location(path.parent.name + "_projection", path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


reader = module(Path(__file__).parent.parent / "fleet-reader" / "projection.py")
native = module(Path(__file__).with_name("live_input.py"))
from runtime import command, write_json


class ProjectionTests(unittest.TestCase):
    def test_changed_actors_use_stable_keys_and_refuse_a_missing_key(self):
        with tempfile.TemporaryDirectory() as directory:
            first, second = Path(directory) / "first.json", Path(directory) / "second.json"
            write_json(first, {"key": "first", "name": ""})
            write_json(second, {"key": "second", "name": ""})
            script = '''source "$1"
mesh_bundle() { printf '%s\\n' "$@"; }
mesh_bundle_actors identity before "$2" "replaced:$3"
'''
            args = ["bash", "-c", script, "_", str(Path(__file__).with_name("entry.sh")), str(first), str(second)]
            received = command(args)
            self.assertEqual("identity\nbefore\nkey:first\nreplaced:key:second\n", received)
            write_json(second, {"name": ""})
            proof = {}
            received = command(args, allowed_codes=(2,), capture=proof)
            self.assertEqual("", received)
            self.assertEqual(f"MESH-ACTOR-KEY: expected a non-empty stable key: {second}\n", proof["stderr"])

    def test_native_delivery_retains_prior_records_and_appends_one_user(self):
        before = {"truncated": False, "entries": [{"role": "assistant", "text": "old reply"}]}
        after = {"truncated": False, "entries": before["entries"] + [
            {"role": "user", "text": "signed pane input"}, {"role": "assistant", "text": "pong"}]}
        self.assertEqual([], [row for row in native.checks(before, after, "signed pane input")
                              if row["expected"] != row["actual"]])
        after["entries"].append({"role": "user", "text": "signed pane input"})
        self.assertEqual([{"field": "public-new-user-records", "expected": ["signed pane input"],
                           "actual": ["signed pane input", "signed pane input"]}],
                         [row for row in native.checks(before, after, "signed pane input")
                          if row["expected"] != row["actual"]])

    def test_delivery_keeps_refusal_distinct_from_success(self):
        for received, expected in (
            ({"code": 0, "status": "delivered", "typed": True, "proof": "screen"}, {"code": 0, "status": "delivered", "typed": True}),
            ({"code": 6, "status": "undelivered"}, {"code": 6, "status": "undelivered", "typed": False}),
        ):
            with self.subTest(received=received):
                self.assertEqual(expected, reader.delivery(received))

    def test_attribution_reads_submitted_inputs_once(self):
        entries = [{"role": "user", "text": "ping-atlas — caller"},
                   {"role": "assistant", "text": "ping-atlas"},
                   {"role": "user", "text": "unrelated"}]
        self.assertEqual(["ping-atlas — caller"], reader.prompts(entries, "ping-atlas"))
        entries.append({"role": "user", "text": "ping-atlas — caller"})
        self.assertEqual(["ping-atlas — caller", "ping-atlas — caller"], reader.prompts(entries, "ping-atlas"))

    def test_mcp_decodes_native_result_and_names_errors(self):
        for receipt in ({"structuredContent": {"state": "idle"}},
                        {"content": [{"type": "text", "text": '{"state":"idle"}'}]}):
            self.assertEqual({"state": "idle"}, reader.mcp_result(receipt))
        with self.assertRaisesRegex(ValueError, '^READER-MCP-ERROR: \\[\\{"text": "dead", "type": "text"\\}\\]$'):
            reader.mcp_result({"isError": True, "content": [{"text": "dead", "type": "text"}]})

    def test_fleet_matches_carried_identity_not_a_neighbour(self):
        chat = {"key": "carried", "session_id": "atlas-session", "seat_id": "atlas-seat", "engine": "claude",
                "project": "atlas", "name": "scout", "account": 3, "terminal": {"backend": "herdr", "pane": "p1"}}
        rows = [{"id": "other", "engine": "cc", "project": "atlas", "name": "scout", "account": 3, "backend": "herdr", "pane": "p1"}]
        result = reader.fleet_checks({"rows": rows}, {"chats": [chat]})
        self.assertEqual([], result[0]["actual"])
        rows[0]["id"] = "atlas-session"
        result = reader.fleet_checks({"rows": rows}, {"chats": [chat]})
        self.assertEqual([{"engine": "cc", "project": "atlas", "name": "scout", "account": 3, "backend": "herdr", "pane": "p1"}], result[0]["actual"])

    def test_bundle_reports_exact_wrong_identity_and_account(self):
        chat = {"key": "carried", "session_id": "atlas-session", "engine": "claude", "name": "scout",
                "account": 3, "project": "atlas", "terminal": {"backend": "tmux", "socket": "/synthetic/tmux", "pane": "%1"}}
        current = {"name": "scout", "status": {"session_id": "wrong-session", "engine": "cc"},
                   "terminal": {"backend": "tmux", "identity": {"pane": "%1", "socket": "/synthetic/tmux"}, "workspace": "n/a:tmux"},
                   "picker": [{"name": "scout", "account": 1, "project": "atlas"}]}
        with self.assertRaisesRegex(ValueError, '^READER-IDENTITY: carried: expected one row, got 0$'):
            reader.selected({"chats": [current]}, chat)
        current["status"]["session_id"] = "atlas-session"
        checks = reader.bundle_checks({"errors": [], "chats": [current]}, {"chats": [chat]})
        failures = [row for row in checks if row["expected"] != row["actual"]]
        self.assertEqual([
            {"field": "carried:account", "expected": [3], "actual": [1]},
        ], failures)
        with self.assertRaisesRegex(ValueError, '^READER-IDENTITY: carried: expected one row, got 2$'):
            reader.selected({"chats": [current, current]}, chat)

    def test_sender_name_in_body_cannot_replace_delivery_signature(self):
        caller = {"name": "atlas", "session_id": "abcdefgh-session"}
        callee = {"name": "beacon", "key": "recipient", "session_id": "beacon-session"}
        record = {"name": "beacon", "status": {"session_id": "beacon-session", "state": "idle"},
                  "stored": {"transcript": {"entries": [{"role": "user", "text": "ping from atlas"},
                                                         {"role": "assistant", "text": "pong"}]}}}
        result = reader.call_checks({"chats": [record]}, caller, callee, "ping from atlas")
        self.assertEqual({"field": "sender", "expected": ["sid abcdefgh · to reply: call atlas <message>"],
                          "actual": ["ping from atlas"]}, result[-1])
        record["stored"]["transcript"]["entries"][0]["text"] += " — sid abcdefgh · to reply: call atlas <message>"
        result = reader.call_checks({"chats": [record]}, caller, callee, "ping from atlas")
        self.assertEqual([], [row for row in result if row["expected"] != row["actual"]])


if __name__ == "__main__":
    unittest.main()
