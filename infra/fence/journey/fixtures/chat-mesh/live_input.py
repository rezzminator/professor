#!/usr/bin/env python3
"""Compare the complete public transcript delta after a native recipient input."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from runtime import read_json, write_json


def checks(before, after, signed):
    prior = before["entries"]
    delta = after["entries"][len(prior):]
    return [
        {"field": "public-before-complete", "expected": False, "actual": before["truncated"]},
        {"field": "public-after-complete", "expected": False, "actual": after["truncated"]},
        {"field": "public-prior-retained", "expected": prior, "actual": after["entries"][:len(prior)]},
        {"field": "public-new-user-records", "expected": [signed],
         "actual": [entry["text"] for entry in delta if entry["role"] == "user"]},
    ]


if __name__ == "__main__":
    write_json(sys.argv[4], checks(read_json(sys.argv[1]), read_json(sys.argv[2]), sys.argv[3]))
