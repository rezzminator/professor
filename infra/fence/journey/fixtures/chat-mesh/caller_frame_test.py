#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("caller_frame", Path(__file__).with_name("caller_frame.py"))
frames = importlib.util.module_from_spec(spec)
spec.loader.exec_module(frames)


class CallerFrames(unittest.TestCase):
    def test_first_complete_frame_preserves_full_caller_bytes(self):
        self.assertEqual(b"caller\n", frames.caller_frame(b"caller\n"))
        self.assertEqual(b"caller\n", frames.caller_frame(b"caller\nchild\n"))

    def test_incomplete_suffix_or_noncanonical_frame_is_refused(self):
        for actual, expected in ((b"", "MESH-INPUT: incomplete newline framing"),
                                 (b"caller\nchild", "MESH-INPUT: incomplete newline framing"),
                                 (b"caller\r\n", "MESH-INPUT: empty or noncanonical caller frame")):
            with self.subTest(actual=actual):
                try:
                    frames.caller_frame(actual)
                except ValueError as error:
                    self.assertEqual(expected, str(error))
                else:
                    self.fail(f"invalid frame accepted: {actual!r}")


if __name__ == "__main__":
    unittest.main()
