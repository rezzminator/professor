#!/usr/bin/env python3
"""Runner verdicts pin mapping membership and the paper budget boundary."""
from contextlib import redirect_stdout
import io
from pathlib import Path
import tempfile
import unittest

import journey_path
from results import judge
from runtime import read_json, write_json


class RunVerdict(unittest.TestCase):
    def test_mapping_and_budget(self):
        with tempfile.TemporaryDirectory() as scratch, redirect_stdout(io.StringIO()):
            root = Path(scratch)
            (root / "design.md").write_text("budget-headroom-pct: 20\ntarget-sequence-ms: 2700000\n")
            (root / "beats.md").write_text("terminal-open/open-picker@linux+tmux · cost_ms:12000 · picker.open-typed\n")
            (root / "map.tsv").write_text("landscape_id\trow\treason\npicker.open-typed\tterminal-open/open-picker@linux+tmux\tlive-only\n")
            for wall, identity, expected in ((14399, "picker.open-typed", 0), (14400, "picker.open-typed", 0),
                                              (14401, "picker.open-typed", 1), (1, "unmapped", 1)):
                with self.subTest(wall=wall, identity=identity):
                    (root / "ledger.tsv").write_text("row\tverdict\tassertions-by-id\twall_ms\n"
                        f"terminal-open/open-picker@linux+tmux\tPASS\t{identity}\t1\n")
                    write_json(root / "lanes.json", {"terminal-open": {"wall_ms": wall}})
                    self.assertEqual(expected, judge(root, root, wall))
                    self.assertEqual(14400, read_json(root / "timing.json")["lanes"]["terminal-open"]["limit_ms"])
            (root / "beats.md").write_text("terminal-open/open-picker@linux+tmux · cost_ms:1000 · picker.open-typed\n")
            write_json(root / "lanes.json", {"terminal-open": {"wall_ms": 1201}})
            self.assertEqual(1, judge(root, root, 1201))
            self.assertEqual(1200, read_json(root / "timing.json")["lanes"]["terminal-open"]["limit_ms"])
            (root / "ledger.tsv").write_text("row\tverdict\tassertions-by-id\twall_ms\n"
                "terminal-open/unmapped@linux+tmux\tPASS\tpicker.open-typed\t1\n")
            self.assertEqual(1, judge(root, root, 1201))

    def test_final_lifecycle_rows_use_the_final_dispatch_budget(self):
        with tempfile.TemporaryDirectory() as scratch, redirect_stdout(io.StringIO()):
            root = Path(scratch)
            (root / "design.md").write_text("budget-headroom-pct: 20\ntarget-sequence-ms: 2700000\n")
            (root / "beats.md").write_text("machine-lifecycle/install · cost_ms:108000 · install\n"
                "machine-lifecycle/uninstall · cost_ms:29000 · uninstall\n")
            (root / "map.tsv").write_text("id\trow\ninstall\tmachine-lifecycle/install\nuninstall\tmachine-lifecycle/uninstall\n")
            (root / "ledger.tsv").write_text("row\tverdict\tassertions-by-id\twall_ms\n"
                "machine-lifecycle/install\tPASS\tinstall\t1\nmachine-lifecycle/uninstall\tPASS\tuninstall\t1\n")
            totals = {"machine-lifecycle": {"wall_ms": 129600, "row_start": 0, "row_end": 1},
                      "machine-lifecycle-final": {"wall_ms": 34800, "row_start": 1, "row_end": 2}}
            write_json(root / "lanes.json", totals)
            self.assertEqual(0, judge(root, root, 164400))
            self.assertEqual(34800, read_json(root / "timing.json")["lanes"]["machine-lifecycle-final"]["limit_ms"])
            self.assertEqual(29000, read_json(root / "timing.json")["lanes"]["machine-lifecycle-final"]["original_ms"])
            totals["chat-mesh"] = {"wall_ms": 17, "row_start": 2, "row_end": 2, "exit": 1}
            write_json(root / "lanes.json", totals)
            self.assertEqual(1, judge(root, root, 164417))
            self.assertEqual({"NOT-RUN": 1}, read_json(root / "timing.json")["lanes"]["chat-mesh"]["verdicts"])
            totals["chat-mesh"]["exit"] = 0
            write_json(root / "lanes.json", totals)
            with self.assertRaisesRegex(ValueError, "LANE-ROW-SPAN"):
                judge(root, root, 164417)
            del totals["chat-mesh"]
            for first, last in ((0, 2), (1, 3), (2, 1)):
                totals["machine-lifecycle-final"].update(row_start=first, row_end=last)
                write_json(root / "lanes.json", totals)
                with self.subTest(first=first, last=last), self.assertRaisesRegex(ValueError, "LANE-ROW-SPAN"):
                    judge(root, root, 164400)


if __name__ == "__main__":
    unittest.main()
