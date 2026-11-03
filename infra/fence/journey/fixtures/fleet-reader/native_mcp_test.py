#!/usr/bin/env python3
"""Fresh MCP provenance uses the completed current-beat action, never presence."""
import os
from pathlib import Path
import tempfile
import unittest
import uuid
from unittest.mock import patch

import native_mcp
from runtime import read_json, write_json


class NativeMCPTests(unittest.TestCase):
    def test_one_current_creation_selects_the_actual_daemon_birth(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            resources, child_file, result_file, beat_file = [root / name for name in ("resources", "child", "result", "beat")]
            context = {"run": "owned-run", "beat": "owned-beat"}
            child = {"name": "child", "session_id": "native-child"}
            write_json(resources, {"id": "chat.ls-cli"})
            write_json(beat_file, context)
            write_json(child_file, child)
            write_json(result_file, {"status": "ok", "code": 0})
            with patch.dict(os.environ, {"PFM_BEAT_FILE": str(beat_file), "JOURNEY_ACTIVITY": str(root / "activity")}), \
                    patch.object(native_mcp, "cursor", return_value={"inode": 41}), \
                    patch.object(native_mcp.uuid, "uuid4", return_value=uuid.UUID("12345678-1234-4234-8234-123456789abc")):
                native_mcp.stage(resources, root / "new")
            staged = read_json(resources)
            self.assertEqual(context, staged["expected_context"])
            self.assertEqual({"engine": "claude", "account": 3, "name": "reader-mcp-123456781234"}, staged["mcp_input"])
            action = {**context, "action": "chat.new", "door": "mcp", "pid": 42}
            tool = {**context, "msg": "mcp.call", "tool": "new", "pid": 42}
            for events, expected in (([action, tool], True), ([{**action, "beat": "other"}, tool], False),
                                     ([action, action, tool], False), ([{**action, "pid": "42"}, tool], False)):
                with self.subTest(events=events), patch.dict(os.environ, {"PFM_BEAT_FILE": str(beat_file), "JOURNEY_ACTIVITY": str(root / "activity")}), \
                        patch.object(native_mcp, "records", return_value=events), \
                        patch.object(native_mcp, "bundle", return_value={"chats": [{"status": {"session_id": "native-child"}}]}), \
                        patch.object(native_mcp, "process_tree", return_value=[{"pid": 42, "start": "actual-daemon-birth"}]) as birth:
                    if expected:
                        native_mcp.complete(resources, child_file, result_file, root / "owner", root / "new")
                        actual = read_json(resources)
                        self.assertEqual({"pid": 42, "start": "actual-daemon-birth"}, actual["producer"])
                        self.assertEqual([tool], actual["tool_records"])
                        self.assertEqual(child, actual["child"])
                        birth.assert_called_once_with([42])
                    else:
                        with self.assertRaisesRegex(ValueError, "^NATIVE-MCP-PRODUCER:"):
                            native_mcp.complete(resources, child_file, result_file, root / "owner", root / "new")
                        birth.assert_not_called()
            write_json(beat_file, {**context, "beat": "later-beat"})
            with patch.dict(os.environ, {"PFM_BEAT_FILE": str(beat_file)}), \
                    self.assertRaisesRegex(ValueError, "^NATIVE-MCP-CONTEXT:"):
                native_mcp.complete(resources, child_file, result_file, root / "owner", root / "new")
