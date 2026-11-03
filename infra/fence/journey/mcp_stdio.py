#!/usr/bin/env python3
"""Keep MCP stdin open through the selected response, then bound process teardown."""
import json
import math
import os
from pathlib import Path
import selectors
import subprocess
import sys
import time

from runtime import start


def exchange(wanted, bound, frames, arguments, grace=2):
    if not math.isfinite(bound) or not math.isfinite(grace) or bound < 0 or grace < 0 or not arguments:
        raise ValueError("MCP-STDIO-USAGE: bound/grace must be nonnegative and a command is required")
    deadline = time.monotonic() + bound
    process = start(arguments, stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    result, why, end = 1, "exited before answering", "eof"
    try:
        try:
            process.stdin.write(frames.encode() + b"\n")
            process.stdin.flush()
        except BrokenPipeError:
            pass
        with selectors.DefaultSelector() as ready:
            ready.register(process.stdout, selectors.EVENT_READ)
            pending = bytearray()
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    result, why = 2, f"no answer in {bound:g}s"
                    break
                if not ready.select(remaining):
                    continue
                chunk = os.read(process.stdout.fileno(), 65536)
                if not chunk:
                    break
                sys.stdout.buffer.write(chunk)
                sys.stdout.buffer.flush()
                pending.extend(chunk)
                if len(pending) > 8 * 1024 * 1024:
                    raise ValueError("MCP-STDIO-REPLY: frame exceeds 8MiB")
                while b"\n" in pending:
                    line, _, rest = pending.partition(b"\n")
                    pending = bytearray(rest)
                    response = json.loads(line)
                    if not isinstance(response, dict):
                        raise ValueError("MCP-STDIO-REPLY: expected a JSON object")
                    if type(response.get("id")) is type(wanted) and response.get("id") == wanted:
                        result, why = 0, ""
                        break
                if result == 0:
                    break
    except (ValueError, OSError) as error:
        result, why = 3, str(error)
    finally:
        try:
            process.stdin.close()
        except BrokenPipeError:
            pass
        if result in (2, 3) and process.poll() is None:
            process.terminate()
            end = "term"
        try:
            process.wait(timeout=grace)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            end = "kill"
        process.stdout.close()
    return result, why, end


if __name__ == "__main__":
    status, why, end = 3, "could not start", ""
    try:
        wanted, bound, frames, metadata, *arguments = sys.argv[1:]
        status, why, end = exchange(json.loads(wanted), float(bound), frames, arguments,
                                    float(os.environ.get("MCP_STDIO_GRACE_SECS", "2")))
    except (ValueError, OSError) as error:
        why = str(error)
    finally:
        if len(sys.argv) >= 5:
            Path(sys.argv[4]).write_text(why.replace("\n", " ") + "\n" + end + "\n")
    sys.exit(status)
