#!/usr/bin/env python3
"""Journey transport to real Herdr; JSON envelopes are never replaced by a fake."""
import json
import os
import socket
import sys


class Refused(ValueError):
    def __init__(self, method, response):
        error = response.get("error")
        self.code = error.get("code") if isinstance(error, dict) else None
        super().__init__(f"{method}: refused: {response}")


def request(path, method, params):
    request_bytes = json.dumps({"id": "journey", "method": method, "params": params}).encode() + b"\n"
    limit = 8 * 1024 * 1024
    with socket.socket(socket.AF_UNIX) as transport:
        transport.settimeout(10)
        transport.connect(path)
        with transport.makefile("rwb") as frames:
            frames.write(request_bytes)
            frames.flush()
            reply = frames.readline(limit + 1)
    if len(reply) > limit:
        raise ValueError(f"{method}: response exceeds 8MiB")
    if not reply.endswith(b"\n"):
        raise ValueError(f"{method}: response ended before newline: {reply!r}")
    response = json.loads(reply)
    if response.get("id") != "journey":
        raise ValueError(f"{method}: mismatched response id: {response.get('id')!r}")
    if response.get("error") is not None or response.get("ok") is False:
        raise Refused(method, response)
    if "result" not in response:
        raise ValueError(f"{method}: unrecognized response envelope")
    return response


def create_tab(workspace_id, cwd):
    """NEED fixture setup only; action beats enter through the owner's pane."""
    return request(os.environ["HERDR_SOCKET_PATH"], "tab.create",
                   {"workspace_id": workspace_id, "cwd": str(cwd), "focus": False})["result"]


def main():
    if len(sys.argv) == 5 and sys.argv[1] == "request":
        print(json.dumps(request(sys.argv[2], sys.argv[3], json.loads(sys.argv[4])), sort_keys=True))
        return 0
    raise ValueError("usage: herdr.py request SOCKET METHOD JSON")


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError) as error:
        print(f"journey-herdr: {error}", file=sys.stderr)
        sys.exit(2)
