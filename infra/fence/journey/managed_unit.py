#!/usr/bin/env python3
"""Native run-owned launchd observation and a reversible, initially-unloaded job."""
import os
import math
from pathlib import Path
import plistlib
import re
import sys

from isolation import journey_os
from runtime import command, read_json, write_json, write_text


def identity(name):
    from faults import owned_path
    prefix = os.environ["PFM_UNIT_PREFIX"]
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,62}", prefix) or prefix == "pfm" or name != "theme":
        raise ValueError(f"MANAGED-UNIT-OWNERSHIP: {prefix!r}/{name!r}")
    if journey_os() != "macos":
        raise ValueError("MANAGED-UNIT-PLATFORM: native launchd requires macOS")
    label = f"com.professor.{prefix}.{name}"
    path = owned_path(Path(os.environ["HOME"]) / "Library/LaunchAgents" / (label + ".plist"), preserve=True)
    if path.is_symlink():
        raise ValueError(f"MANAGED-UNIT-SYMLINK: {path}")
    return label, f"gui/{os.getuid()}/{label}", path


def manager(target):
    receipt = {}
    command(["launchctl", "print", target], allowed_codes=(0, 113), capture=receipt)
    if receipt["status"] == 113:
        if "Could not find service" not in receipt["stderr"]:
            raise ValueError(f"MANAGED-UNIT-UNREADABLE: {receipt!r}")
        return {"state": "absent", "pid": None, "last_exit_code": None, "receipt": receipt}
    # Python has no launchctl print decoder; read only its exact, single scalar lines.
    def scalar(key):
        found = re.findall(r"(?m)^\s*" + re.escape(key) + r" = ([^\n{}]+)$", receipt["stdout"])
        if len(found) > 1:
            raise ValueError(f"MANAGED-UNIT-AMBIGUOUS: {target}: {key}")
        return found[0].strip() if found else None
    state = scalar("state")
    if not state or receipt["stderr"]:
        raise ValueError(f"MANAGED-UNIT-UNREADABLE: {receipt!r}")
    pid, status = scalar("pid"), scalar("last exit code")
    if status == "(never exited)":
        status = None
    if (pid is not None and not re.fullmatch(r"[1-9][0-9]*", pid)) or (status is not None and not re.fullmatch(r"-?[0-9]+", status)):
        raise ValueError(f"MANAGED-UNIT-NUMBER: {receipt!r}")
    if (state == "running") != (pid is not None):
        raise ValueError(f"MANAGED-UNIT-PID-STATE: {receipt!r}")
    return {"state": state, "pid": int(pid) if pid else None,
            "last_exit_code": int(status) if status is not None else None, "receipt": receipt}


def observe(name):
    """Read disk definition separately from actual manager state and kernel birth."""
    from observation import process_snapshot
    label, target, path = identity(name)
    definition = None
    try:
        content = plistlib.loads(path.read_bytes())
    except FileNotFoundError:
        pass
    else:
        if not isinstance(content, dict) or content.get("Label") != label:
            raise ValueError(f"MANAGED-UNIT-DEFINITION: {path}")
        definition = {"path": str(path), **{key: content.get(key) for key in
                      ("ProgramArguments", "WatchPaths", "EnvironmentVariables")}}
    actual = manager(target)
    birth = None
    if actual["pid"] is not None:
        birth = process_snapshot().get(actual["pid"])
        if birth is None:
            raise ValueError(f"MANAGED-UNIT-KERNEL-ABSENT: {target}: pid={actual['pid']}")
        fresh = manager(target)
        current = process_snapshot().get(actual["pid"])
        if fresh["pid"] != actual["pid"] or current is None or current["start"] != birth["start"]:
            raise ValueError(f"MANAGED-UNIT-BIRTH-CHANGED: {target}")
    return {"platform": "darwin", "label": label, "target": target, **actual,
            "start": birth["start"] if birth else None, "argv": birth["argv"] if birth else None,
            "definition": definition}


def run(name, argv, prefix, *, before=None, after=None, bound=30):
    """Run public argv under launchd; requires an unloaded job and unused artifact prefix.

    before(job) may change the disk plist while the held, loaded job stays authoritative.
    A caller preserving an existing loaded job scopes faults.unit_state(label, False).
    """
    from bootstrap import wait
    from faults import interruptible, owned_path, preserved_file
    label, target, path = identity(name)
    if not isinstance(argv, (list, tuple)) or not argv or any(not isinstance(word, str) or not word or "\0" in word for word in argv):
        raise ValueError("MANAGED-UNIT-ARGV: nonempty native argument strings required")
    if not isinstance(bound, (int, float)) or not math.isfinite(bound) or bound <= 0:
        raise ValueError("MANAGED-UNIT-BOUND: positive finite seconds required")
    prefix = owned_path(prefix, preserve=True)
    artifacts = {key: Path(str(prefix) + "." + key) for key in ("request.json", "ready.json", "release", "result.json", "out", "err", "status", "job.json", "launcher.out", "launcher.err")}
    if any(item.exists() or item.is_symlink() for item in artifacts.values()):
        raise ValueError(f"MANAGED-UNIT-ARTIFACT-EXISTS: {prefix}")
    original = observe(name)
    if original["state"] != "absent":
        raise ValueError(f"MANAGED-UNIT-LOADED: scope faults.unit_state({label!r}, False) first")
    environment = {key: value for key, value in os.environ.items() if key.startswith(("PFM_", "JOURNEY_", "HERDR_", "XDG_")) or key in
                   {"HOME", "PATH", "USER", "LOGNAME", "SHELL", "LANG", "TERM", "TMPDIR", "MOCK_ENGINE_SCENARIO", "TMUX_TMPDIR"}}
    definition = {"Label": label, "ProgramArguments": [sys.executable, str(Path(__file__).resolve()), "invoke", str(prefix)],
                  "EnvironmentVariables": environment, "RunAtLoad": True,
                  "StandardOutPath": str(artifacts["launcher.out"]), "StandardErrorPath": str(artifacts["launcher.err"])}
    write_json(artifacts["request.json"], {"argv": list(argv), "label": label, "bound": bound})
    with interruptible(), preserved_file(path):
        write_text(path, plistlib.dumps(definition).decode(), 0o600)
        try:
            command(["launchctl", "bootstrap", target.rsplit("/", 1)[0], str(path)])
            ready = wait("managed unit native ready", lambda: read_json(artifacts["ready.json"]) if artifacts["ready.json"].is_file() else False, bound)
            job = observe(name)
            if job["pid"] != ready["birth"]["pid"] or job["start"] != ready["birth"]["start"] or ready["xpc_service_name"] != label or ready["birth"]["chain"][0]["parent"] != 1:
                raise ValueError(f"MANAGED-UNIT-ANCESTRY: job={job!r} ready={ready!r}")
            write_json(artifacts["job.json"], {"before": original, "held": job, "native": ready})
            if before is not None:
                before(job)
            write_text(artifacts["release"], "release\n")
            result = wait("managed unit command completion", lambda: read_json(artifacts["result.json"]) if artifacts["result.json"].is_file() else False, bound)
            if "error" in result:
                raise ValueError(f"MANAGED-UNIT-COMMAND: {result!r}")
            receipt = {**result, "job": job, "native": ready, "artifacts": {key: str(value) for key, value in artifacts.items()}}
            if after is not None:
                after(job, receipt)
            return receipt
        finally:
            if manager(target)["state"] != "absent":
                command(["launchctl", "bootout", target])
                wait("managed unit unload", lambda: manager(target)["state"] == "absent", bound)


def invoke(prefix):
    """Launchd's real supervisor; retain the actual public child and its native ancestry."""
    from bootstrap import wait
    from observation import process_tree
    request = read_json(str(prefix) + ".request.json")
    result = {}
    birth = None
    def prove_child(child):
        nonlocal birth
        birth = process_tree([child.pid])[0]
        if birth["chain"][0]["parent"] != os.getpid() or birth["environment"].get("XPC_SERVICE_NAME") != request["label"]:
            raise ValueError(f"MANAGED-UNIT-CHILD-ANCESTRY: {birth!r}")
    try:
        birth = process_tree([os.getpid()])[0]
        write_json(str(prefix) + ".ready.json", {"birth": birth, "xpc_service_name": os.environ.get("XPC_SERVICE_NAME")})
        wait("managed unit release", lambda: Path(str(prefix) + ".release").is_file(), request["bound"])
        command(request["argv"], allowed_codes=range(-255, 256), capture=result, started=prove_child, timeout=request["bound"],
                env={**os.environ, "PFM_DOOR": "cli"})
        result["birth"] = birth
        write_text(str(prefix) + ".out", result["stdout"])
        write_text(str(prefix) + ".err", result["stderr"])
        write_text(str(prefix) + ".status", str(result["status"]) + "\n")
    except Exception as error:
        result = {"error": str(error)}
    finally:
        write_json(str(prefix) + ".result.json", result)


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "invoke":
        invoke(sys.argv[2])
    else:
        raise SystemExit("usage: managed_unit.py invoke PREFIX")
