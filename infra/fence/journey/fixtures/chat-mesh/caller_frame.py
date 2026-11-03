#!/usr/bin/env python3
"""Read one complete caller frame without searching for its expected contents."""
import io
from pathlib import Path
import sys


def caller_frame(data):
    if not data or not data.endswith(b"\n"):
        raise ValueError("MESH-INPUT: incomplete newline framing")
    frame = io.BytesIO(data).readline()
    if frame == b"\n" or b"\r" in frame:
        raise ValueError("MESH-INPUT: empty or noncanonical caller frame")
    return frame


if __name__ == "__main__":
    Path(sys.argv[2]).write_bytes(caller_frame(Path(sys.argv[1]).read_bytes()))
