#!/usr/bin/env python3
"""Real unregistered Codex caller under a receipt-proven unreadable ancestor."""
from pathlib import Path
import sys

from forecast import check, checked_owner
from bootstrap import wait
import faults
from observation import bundle, expected_environment_denial, process_snapshot, terminal
from runtime import read_json, write_json
from native_actor import composer, wrapper as actor_wrapper
from terminal import keys, text, type_command


def wrapper(setup_file):
    actor_wrapper(setup_file, "codex", faults.nondumpable)

def run(owner_file, resources, prefix, arguments, evidence, execute, require):
    owner = read_json(owner_file)
    if owner["backend"] != "pty":
        raise ValueError("NATIVE-CODEX-OUTSIDE: multiplexer-free owned PTY required")
    for key in ("scenario", "records", "cwd", "codex_home"):
        faults.owned_path(resources[key])
    if Path(resources["codex_home"], "hooks.json").exists():
        raise ValueError("NATIVE-CODEX-REGISTERED: fresh hook-free CODEX_HOME required")
    setup = {**resources, "prefix": str(prefix)}
    setup_file = str(prefix) + ".setup.json"
    write_json(setup_file, setup)
    type_command(owner, [sys.executable, str(Path(__file__).resolve()), "denial-wrapper", setup_file])
    receipt_path = str(prefix) + ".environ.json"
    records = Path(resources["records"])
    try:
        wait("native Codex denial receipt", lambda: Path(receipt_path).exists(), 15)
        with expected_environment_denial(receipt_path) as denial:
            wait("actual native Codex birth", lambda: (records / "birth.json").exists(), 15)
            birth = read_json(records / "birth.json")
            snapshot = process_snapshot()
            engine = snapshot[birth["pid"]]
            observed = checked_owner(owner, expected_denial=denial)
            composer(owner, "codex", "actual native Codex composer")
            require(prefix, evidence, [check("outside-native-engine", "codex\n", (records / "argv").read_text()),
                    check("native-codex-parent", denial["pid"], engine["parent"]),
                    check("native-codex-argv", str(Path(resources["native_binary"]).resolve()), engine["argv"]),
                    check("native-codex-multiplexer", [{} for row in observed["births"] if row["pid"] != denial["pid"]],
                        [{key: row["environment"][key] for key in ("TMUX", "TMUX_PANE", "HERDR_SOCKET_PATH", "HERDR_PANE_ID") if key in row["environment"]}
                         for row in observed["births"] if row["pid"] != denial["pid"]])])
            outside = {"engine": "codex", "records": resources["records"], "terminal": owner}
            outside_file = str(prefix) + ".outside.json"
            write_json(outside_file, outside)
            write_json(outside_file + ".target", owner)
            evidence["before_denied"] = bundle(owner_file)
            actual = execute(outside_file, arguments, str(prefix) + ".command", resources["id"], registered=False)
            composer(owner, "codex", "native Codex idle after actual failed tool")
            require(prefix, evidence, [check("native-denied-exit", 1, actual["exit_code"]),
                    check("native-denied-diagnostic", f"look up the caller: read environment of pid {denial['pid']}: open /proc/{denial['pid']}/environ: permission denied", actual["output"].strip())])
            evidence.update(receipt=actual, denial=denial, native_birth=birth, during=bundle(owner_file))
    finally:
        birth_file = records / "birth.json"
        if birth_file.exists() and read_json(birth_file)["pid"] in process_snapshot():
            text(owner, "/exit")
            keys(owner, ["enter"])
        wait("native Codex wrapper restored dumpability and exited", lambda: Path(str(prefix) + ".wrapper-exit.json").exists(), 15)
    require(prefix, evidence, [check("native-codex-wrapper-exit", {"exit_code": 0}, read_json(str(prefix) + ".wrapper-exit.json"))])


if __name__ == "__main__":
    try:
        if len(sys.argv) != 3 or sys.argv[1] != "denial-wrapper":
            raise ValueError("usage: native_denied.py denial-wrapper SETUP_JSON")
        wrapper(sys.argv[2])
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(f"NATIVE-DENIED-ERROR: {error}", file=sys.stderr)
        sys.exit(2)

