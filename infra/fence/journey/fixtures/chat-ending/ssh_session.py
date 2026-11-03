#!/usr/bin/env python3
"""Resume through the root's second login, with only its own SSH identity."""
import os
from pathlib import Path
import shlex
import sys

from observation import process_snapshot
from runtime import read_json, write_json
from terminal import type_command


def arguments(root, config, session, proof=None):
    if config["host"] != "127.0.0.1" or type(config["port"]) is not int or not 1024 <= config["port"] <= 65535:
        raise ValueError("ENDING-SSH-ENDPOINT: expected the run's loopback sshd")
    for key in ("identity", "known_hosts"):
        path = Path(config[key])
        path.resolve().relative_to(root.resolve())
        if not path.is_file():
            raise ValueError(f"ENDING-SSH-FILE: {key}: {path}")
    remote = ". " + shlex.quote(str(root / "env.sh")) + "; "
    if proof:
        Path(proof).resolve().relative_to(root.resolve())
        remote += shlex.join(["env", "PYTHONPATH=" + str(Path(__file__).resolve().parents[2]), "python3", str(Path(__file__).resolve()), "probe", proof]) + " && "
    remote += "exec env -u HERDR_PANE_ID -u TMUX -u TMUX_PANE " + shlex.join(["pfm", "chat", "open", session])
    return ["ssh", "-tt", "-p", str(config["port"]), "-i", config["identity"], "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + config["known_hosts"], config["user"] + "@" + config["host"], remote]


def ancestor_access():
    if sys.platform != "linux" or not os.environ.get("SSH_CONNECTION") or not os.environ.get("SSH_TTY"):
        raise ValueError("ENDING-SSH-PRIVSEP: a real Linux SSH PTY is required")
    processes, pid, seen, ancestors = process_snapshot(), os.getpid(), set(), []
    while pid > 1 and pid not in seen:
        seen.add(pid)
        process = processes[pid]
        pid = process["parent"]
        if "sshd" not in process["argv"] or Path("/proc", str(process["pid"])).stat().st_uid == os.getuid():
            continue
        denied = False
        try:
            descriptor = os.open(str(Path("/proc", str(process["pid"]), "environ")), os.O_RDONLY)
        except PermissionError:
            denied = True
        else:
            os.close(descriptor)
        ancestors.append({"pid": process["pid"], "start": process["start"], "foreign_uid": True, "environ_denied": denied})
    if not ancestors:
        raise ValueError("ENDING-SSH-PRIVSEP: no observed foreign-uid sshd ancestor")
    return {"ssh_pty": True, "ancestors": ancestors, "foreign_environ_denied": any(row["environ_denied"] for row in ancestors)}


if __name__ == "__main__":
    try:
        if sys.argv[1] == "probe":
            write_json(sys.argv[2], ancestor_access())
        else:
            target, receipt, session, *proof = sys.argv[1:]
            root = Path(os.environ["JOURNEY_ROOT"])
            type_command(read_json(target), arguments(root, read_json(root / "ssh.json"), session, proof[0] if proof else None), receipt)
    except (OSError, ValueError, KeyError) as error:
        print(f"ENDING-SSH: {error}", file=sys.stderr)
        sys.exit(2)
