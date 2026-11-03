#!/usr/bin/env python3
"""Retain real dead-seat evidence only for the owned loss observation window."""
import sys

from forecast import command, read_json
from chat_carry import temporary_expectation


def run(chat_file, before_file, boundary, *arguments):
    if boundary != "--" or not arguments:
        raise ValueError("READER-TRANSIENT-USAGE: CHAT BEFORE -- COMMAND...")
    chat = read_json(chat_file)
    role = chat.get("role", chat["origin"])
    with temporary_expectation(chat["branch"], role,
            {"lifecycle": "transient", "terminal": "live", "visibility": "visible"},
            before=read_json(before_file)):
        print(command(list(arguments), timeout=90), end="")


if __name__ == "__main__":
    try:
        run(*sys.argv[1:])
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(f"READER-TRANSIENT-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
