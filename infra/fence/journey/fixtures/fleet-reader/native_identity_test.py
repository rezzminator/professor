#!/usr/bin/env python3
"""The blank name never replaces the native identity or durable seat address."""
import copy
import os
import unittest
from unittest.mock import patch

import native_identity


class NativeIdentityTests(unittest.TestCase):
    def test_unnamed_native_caller_pins_seat_address_transcript_and_actual_birth(self):
        chat = {"key": "unnamed", "name": "", "seat_id": "seat-a", "session_id": "native-a", "engine": "claude",
                "terminal": {"backend": "herdr", "pane": "pane-a"}}
        row = {"name": "", "status": {"session_id": "native-a"}, "stored": {"seat": [{"seat_id": "seat-a", "name": ""}],
                "recipe": {"binary": "/fixture/claude"}}, "terminal": {"identity": {"pane": "pane-a"}},
               "process_births": [{"pid": 31, "start": "native-birth", "environment": {}, "chain": [{"argv": "/fixture/claude"}]}]}
        before = {"chats": [row]}
        cli = {"id": "native-a", "backend": "herdr", "pane": "pane-a", "engine": "cc"}
        answer = {"status": "ok", "session": "seat:seat-a", "id": "native-a", "engine": "cc", "pane": "pane-a", "socket_path": "/fixture/herdr.sock"}
        for field, value, expected in ((None, None, []), ("session", "seat-a", ["unnamed-mcp-native-identity"]),
                ("id", "seat-a", ["unnamed-mcp-native-identity"]), ("pane", "other-pane", ["unnamed-mcp-native-identity"]),
                ("socket_path", "/fixture/foreign.sock", ["unnamed-mcp-native-identity"]),
                ("status", "not_found", ["unnamed-mcp-native-identity"]),
                ("engine", "codex", ["unnamed-mcp-native-identity"]),
                ("seat", "other-seat", ["unnamed-durable-seat-name", "unnamed-mcp-native-identity"]),
                ("name", "named", ["unnamed-native-caller"]), ("stored-name", "named", ["unnamed-durable-seat-name"]),
                ("birth", "replacement-birth", ["unnamed-same-native-engine-birth"])):
            with self.subTest(field=field), patch.dict(os.environ, {"HERDR_SOCKET_PATH": "/fixture/herdr.sock"}):
                actor, after, result = copy.deepcopy(chat), copy.deepcopy(before), {**answer}
                if field == "name":
                    actor["name"] = value
                elif field == "seat":
                    actor["seat_id"] = value
                elif field == "stored-name":
                    after["chats"][0]["stored"]["seat"][0]["name"] = value
                elif field == "birth":
                    after["chats"][0]["process_births"][0]["start"] = value
                elif field:
                    result[field] = value
                checks = native_identity.unnamed_checks(actor, before, after, cli, result)
                self.assertEqual(expected, [row["field"] for row in checks if row["expected"] != row["actual"]])
