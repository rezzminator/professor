#!/usr/bin/env python3
"""Native receipt bytes survive cleanup without following unrelated paths."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import artifacts
from runtime import read_json, write_text


class RetainedArtifacts(unittest.TestCase):
    def test_native_receipts_and_carry_are_copied_with_explicit_absence(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "out"
            receipt = root / "branches/owned/records/input.jsonl"
            write_text(receipt, '{"input":"/stats"}\n', mode=0o400)
            write_text(root / "carry.json", '{"session_id":"actual-native"}\n')
            for _ in range(2):
                with patch.object(artifacts, "now_ms", return_value=123):
                    artifacts.retain(root, output)
                self.assertEqual(b'{"input":"/stats"}\n', (output / "root/branches/owned/records/input.jsonl").read_bytes())
                self.assertEqual(0o400, (output / "root/branches/owned/records/input.jsonl").stat().st_mode & 0o777)
                self.assertEqual({"session_id": "actual-native"}, read_json(output / "root/carry.json"))
                self.assertEqual({"source_root": str(root), "artifact_root": "root", "copied": ["branches", "carry.json"],
                                  "absent": ["mock-records", "previous-install"], "captured_ms": 123}, read_json(output / "root-artifacts.json"))
            for name, link in ((".credentials.json", False), ("external", True)):
                path = receipt.parent / name
                if link:
                    path.symlink_to(root / "carry.json")
                else:
                    write_text(path, "forbidden fixture\n")
                try:
                    with self.assertRaisesRegex(ValueError, "ARTIFACT-SOURCE-REFUSED"):
                        artifacts.retain(root, output)
                finally:
                    path.unlink()


if __name__ == "__main__":
    unittest.main()
