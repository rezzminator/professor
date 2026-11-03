#!/usr/bin/env python3
"""Carried expectations permit missing dead terminals, never missing evidence."""
import json
import sys

from chat_carry import expect
from observation import report
from runtime import read_json, write_json


def state(chat, wanted, output):
    actual = json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3)))
    write_json(output, actual)
    if actual["state"] == "not-found":
        raise ValueError(f"ENDING-STATUS-MISSING: {chat['name']} {chat['session_id']}")
    return 0 if actual["state"] in ({"dead", "lost"} if wanted == "lost" else {wanted}) else 1


if __name__ == "__main__":
    try:
        operation, path, value, *args = sys.argv[1:]
        chat = read_json(path)
        if operation == "expect":
            expect(chat, value, read_json(args[0]) if args and args[0] != "-" else None, args[1] if len(args) > 1 else "visible")
        elif operation == "state":
            sys.exit(state(chat, value, args[0]))
        else:
            raise ValueError(f"ENDING-LIFECYCLE-OPERATION: {operation}")
    except (OSError, ValueError, KeyError, StopIteration, TypeError) as error:
        print(f"ENDING-LIFECYCLE: {error}", file=sys.stderr)
        sys.exit(2)
