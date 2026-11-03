#!/usr/bin/env python3
"""Release actual Herdr authority and restore it through a genuine engine prompt."""
from pathlib import Path
import ast
import os
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from chat import inline
from faults import released_agent
from herdr import request
from runtime import command, read_json, write_json
from terminal import keys

here, caller_file, callee_file, prefix, identity, *arguments = sys.argv[1:]
mode = arguments[0] if arguments else "protected"
if mode not in ("protected", "delivery"):
    raise ValueError(f"unknown agentless call mode: {mode}")
callee = read_json(callee_file)


def restore():
    if mode == "protected":
        keys(callee["terminal"], ["escape"])
    inline(callee["terminal"], str(Path(__file__).with_name("turn.json")), "agent-authority-restored")


with released_agent(callee, restore):
    try:
        response = request(os.environ["HERDR_SOCKET_PATH"], "agent.get", {"target": callee["terminal"]["pane"]})
    except ValueError as error:
        _, separator, encoded = str(error).partition("agent.get: refused: ")
        if not separator:
            raise
        response = ast.literal_eval(encoded)
    write_json(prefix + ".agent-get.json", {"response": response, "code": response.get("error", {}).get("code")})
    command(["bash", str(Path(__file__).with_name("agentless-call.sh")), here,
             caller_file, callee_file, prefix, identity, mode], timeout=60)
