#!/usr/bin/env python3
"""Observe the public launcher exiting before its real done waiter connects."""
from pathlib import Path
import sys

from bootstrap import wait
from faults import delayed_tmux_waiter
from runtime import read_json, write_json
from terminal import type_command

from boot import after_entry


def run(factors, directory, target_file, output):
    directory = Path(directory)
    target = read_json(target_file)
    if target["backend"] != "pty":
        raise ValueError("BIRTH-WAITER-CALLER: outside-terminal PTY required")
    fault = directory / "waiter"
    fault.mkdir()
    prefix = str(directory / "early-exit")
    with delayed_tmux_waiter(target, fault) as scoped:
        type_command(target, ["claude"], prefix)
        held = after_entry(factors, str(directory), target_file)
        wait("typed launcher completed after early engine exit", lambda: Path(prefix + ".status").is_file(), 15)
        entries = sorted(Path(scoped["directory"]).glob("waiter-*/entered.json"))
        if len(entries) != 1:
            raise ValueError(f"BIRTH-WAITER-ENTRIES: expected one actual done waiter, found {entries!r}")
        entered = read_json(entries[0])
        result_path = entries[0].with_name("result.json")
        wait("done waiter connect result", result_path.is_file, 5)
        result = read_json(result_path)
        metadata = read_json(directory / "scenario.json")
        arguments = entered["argv"]
        if len(arguments) != 4 or arguments[:3] != ["-S", metadata["launch_socket"], "wait-for"] or not arguments[3].startswith("pfm-launch-done-"):
            raise ValueError(f"BIRTH-WAITER-ARGV: wrong owned done waiter: {arguments!r}")
        evidence = {"entered": entered, "result": result, "birth": held["birth"],
                    "status": Path(prefix + ".status").read_text().strip(),
                    "stdout": Path(prefix + ".out").read_text(),
                    "projection": {"caller": target["backend"], "exit_status": Path(prefix + ".status").read_text().strip(),
                                   "done_waiter": result["outcome"], "same_waiter_birth":
                                   (entered["pid"], entered["start"]) == (result["pid"], result["start"])}}
        write_json(output, evidence)
    scenario = read_json(metadata["scenario"])
    for field in ("ready_hold", "ready_timeout_ms", "startup_exit_code"):
        scenario.pop(field, None)
    write_json(metadata["scenario"], scenario)


if __name__ == "__main__":
    try:
        run(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f"BIRTH-WAITER-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
