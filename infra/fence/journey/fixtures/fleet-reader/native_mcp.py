#!/usr/bin/env python3
"""Assemble fresh native MCP producer receipts through the shared readers."""
import os
import sys
import uuid

from forecast import HERE
from activity import cursor, records
from observation import bundle, process_tree
from projection import selected
from runtime import read_json, write_json


def stage(resources_file, prefix):
    values = read_json(resources_file)
    name = "reader-mcp-" + uuid.uuid4().hex[:12]
    values.update(expected_context=read_json(os.environ["PFM_BEAT_FILE"]),
                  expected_child={"engine": "claude", "account": 3, "name": name},
                  mcp_input={"engine": "claude", "account": 3, "name": name})
    write_json(str(prefix) + ".cursor.json", cursor(os.environ["JOURNEY_ACTIVITY"]))
    write_json(resources_file, values)


def complete(resources_file, child_file, result_file, owner_file, prefix):
    values, child = read_json(resources_file), read_json(child_file)
    context = values["expected_context"]
    if read_json(os.environ["PFM_BEAT_FILE"]) != context:
        raise ValueError("NATIVE-MCP-CONTEXT: beat changed during native creation")
    actual = records(os.environ["JOURNEY_ACTIVITY"], read_json(str(prefix) + ".cursor.json"))
    scoped = [row for row in actual if all(row.get(key) == context[key] for key in ("run", "beat"))]
    actions = [row for row in scoped if row.get("action") == "chat.new" and row.get("door") == "mcp"]
    if len(actions) != 1 or type(actions[0].get("pid")) is not int:
        raise ValueError(f"NATIVE-MCP-PRODUCER: expected one actual attributed creation pid, actual={actions!r}")
    pid = actions[0]["pid"]
    tools = [row for row in scoped if row.get("msg") == "mcp.call" and row.get("tool") == "new" and row.get("pid") == pid]
    values.update(child=child, child_observed=selected(bundle(owner_file), child),
                  producer=process_tree([pid])[0], action_records=actions, tool_records=tools,
                  creation_result=read_json(result_file))
    write_json(resources_file, values)


if __name__ == "__main__":
    operation, *arguments = sys.argv[1:]
    if operation == "stage":
        stage(*arguments)
    elif operation == "complete":
        complete(*arguments)
    else:
        raise ValueError("usage: native_mcp.py stage|complete ...")
