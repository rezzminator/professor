#!/usr/bin/env python3
"""Drive the outside-terminal launcher while one owned server is stopped."""
from pathlib import Path
import sys

from bootstrap import wait
from faults import stopped_tmux
from observation import report
import runtime
from terminal import type_command


def main(target_file, socket, prefix, output):
    target = runtime.read_json(target_file)
    if target["backend"] != "pty":
        raise ValueError("BIRTH-STOPPED-CALLER: actual outside-multiplexer PTY required")
    clients = ["tmux", "-S", socket, "list-clients", "-F", "#{client_pid}\t#{client_tty}"]
    previous = report(clients)
    status = Path(prefix + ".status")
    start = runtime.now_ms()
    with stopped_tmux(socket) as birth:
        type_command(target, ["claude"], prefix)
        wait("outside launcher refusal while tmux server stopped", status.is_file, 10)
        result = {"server": birth, "elapsed_ms": runtime.now_ms() - start,
                  "status": status.read_text().strip(), "output": Path(prefix + ".out").read_text().strip()}
    result.update(clients_before=previous, clients_after=report(clients))
    runtime.write_json(output, result)


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError) as error:
        print(f"BIRTH-STOPPED-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
