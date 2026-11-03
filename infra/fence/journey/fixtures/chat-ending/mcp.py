#!/usr/bin/env python3
"""Read actual mock MCP receipts, preserving the server's structured result."""
import json
from pathlib import Path
from runpy import run_path
import sys


result = run_path(str(Path(__file__).resolve().parents[1] / "fleet-reader/projection.py"))["mcp_result"]


def record(path):
    lines = Path(path).read_text().splitlines()
    if not lines:
        raise ValueError(f"ENDING-MCP-EMPTY: {path}")
    return result(json.loads(lines[-1]))


if __name__ == "__main__":
    try:
        path, field, *arguments = sys.argv[1:]
        if field == "input-count":
            print(Path(path).read_text().count(arguments[0]))
            sys.exit(0)
        value = record(path)
        selected = {key: value[key] for key in ("code", "status", "typed")} if field == "delivery" else value[field]
        print(json.dumps(selected, sort_keys=True, separators=(",", ":"), ensure_ascii=False))
    except (OSError, KeyError, ValueError, TypeError) as error:
        print(f"ENDING-MCP: {error}", file=sys.stderr)
        sys.exit(2)
