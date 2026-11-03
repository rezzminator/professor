#!/usr/bin/env python3
"""Run reader entries inside evidenced, reversible forecast conditions."""
from contextlib import contextmanager
import csv
import errno
import io
import json
import os
from pathlib import Path
import sys

HERE = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(HERE))
import faults
from observation import bundle, terminal
from runtime import command, now_ms, read_json, write_json
from terminal import type_command


def factors(value):
    # Forecast cells have repository-defined assignments; csv owns their framing.
    result = {}
    for item in next(csv.reader(io.StringIO(value), strict=True)):
        name, separator, content = item.partition("=")
        if not separator or not name or not content or name in result:
            raise ValueError(f"FORECAST-FACTORS: {value!r}")
        result[name] = content
    return result


def operation(values):
    if values in ({"engine": "codex", "multiplexer": "herdr"},
                  {"engine": "opencode", "multiplexer": "herdr"},
                  {"multiplexer": "herdr", "door": "list"}):
        return "herdr-reader"
    if values == {"tmux_server": "sigstopped", "probe_site": "gather_probe_without_deadline", "client_pipe": "held_by_stopped_server"}:
        return "stopped-probe"
    if values == {"process_state": "stopped", "tmux_server": "job_control_stopped", "caller_context": "outside_pane"}:
        return "outside-stopped"
    if values == {"tmux_server": "sigstopped", "caller": "claude_launch_outside_pane"}:
        return "outside-stopped"
    raise ValueError("FORECAST-UNSUPPORTED: no evidenced setup for " + json.dumps(values, sort_keys=True))


def check(name, expected, actual):
    return {"field": name, "expected": expected, "actual": actual}


def checked_owner(owner, expected_denial=None):
    if owner.get("backend") != "pty":
        return faults.owned_target(owner)
    faults.owned_path(owner["socket"])
    observed = terminal(owner)
    if not observed["births"]:
        raise ValueError("FORECAST-OWNER-FOREIGN: plain PTY process is not owned by this run")
    for birth in observed["births"]:
        environment = birth["environment"]
        if environment.get("PFM_RUN") == os.environ["PFM_RUN"]:
            continue
        if (expected_denial is not None and environment == expected_denial
                and environment.get("state") == "denied" and environment.get("errno") == errno.EACCES
                and environment.get("pid") == birth["pid"] and environment.get("start") == birth["start"]):
            faults.owned_path(environment["receipt"])
            continue
        raise ValueError("FORECAST-OWNER-FOREIGN: plain PTY process is not owned by this run")
    return observed


def responsive_owner(owner, socket):
    if owner.get("backend") == "tmux" and Path(owner["socket"]).resolve() == Path(socket).resolve():
        raise ValueError("FORECAST-OWNER-STOPPED: action would run in the stopped server")
    carry = read_json(os.environ["JOURNEY_CARRY"])
    foreign = carry["foreign"]["terminal"]
    if foreign.get("backend") == "tmux" and Path(foreign["socket"]).resolve() == Path(socket).resolve():
        raise ValueError("FORECAST-FOREIGN-STOPPED: sentinel server cannot be a scratch fault")
    return checked_owner(owner)


def outside_checks(owner, observed):
    if owner.get("backend") != "pty":
        raise ValueError("FORECAST-OUTSIDE-PANE: a kernel PTY outside Herdr and tmux is required")
    environment = [{key: birth["environment"][key] for key in ("TMUX", "TMUX_PANE", "HERDR_SOCKET_PATH", "HERDR_PANE_ID")
                    if key in birth["environment"]} for birth in observed["births"]]
    if not environment:
        raise ValueError("FORECAST-OUTSIDE-PROCESS: no actual PTY process birth was observed")
    return [check("outside-pane-environment", [{} for _ in environment], environment),
            check("owner-pty", owner["pane"], observed["identity"]["pane"])]


def stopped_checks(birth):
    state = command(["ps", "-o", "stat=", "-p", str(birth["pid"])]).strip()
    return [check("tmux-server-state", "stopped", "stopped" if state.startswith("T") else state),
            check("tmux-server-birth", birth, {"pid": birth["pid"], "start": faults.checked_birth(birth)["start"]})]


def targeted_entry(arguments, socket):
    aliases = {socket, Path(socket).name}
    if arguments[:3] in (["pfm", "chat", "status"], ["pfm", "chat", "capture"]) and aliases.intersection(arguments[3:]):
        return
    if arguments[:2] == ["pfm", "ls"]:
        carry = read_json(os.environ["JOURNEY_CARRY"])
        listed = [row["terminal"].get("socket") for row in carry["chats"] if row["terminal"]["backend"] == "tmux"]
        if socket in listed and Path(socket).parent.resolve() == Path(os.environ["PFM_TMUX_DIR"]).resolve():
            return
    raise ValueError("FORECAST-PROBE-TARGET: public entry must address the exact stopped server")


def bounded_check(elapsed, bound):
    return {**check("probe-wall", "bounded", "bounded" if elapsed <= bound else "overrun"),
            "elapsed_ms": elapsed, "maximum_ms": bound}


@contextmanager
def scope(values, chat, owner, resources):
    selected = operation(values)
    evidence = {"checks": [], "resources": {}}
    if selected == "herdr-reader":
        observed = faults.owned_target(chat["terminal"])
        checked_owner(owner)
        evidence["checks"].append(check("backend", "herdr", observed["backend"]))
        if "engine" in values:
            status = json.loads(command(["pfm", "chat", "status", chat["session_id"], "--json"]))
            evidence["checks"].append(check("engine", {"codex": "cx", "opencode": "ox"}[values["engine"]], status["engine"]))
        evidence["resources"]["recipient"] = observed["identity"]
        yield evidence
        return
    socket = resources.get("tmux_socket")
    if not isinstance(socket, str):
        raise ValueError("FORECAST-TMUX-RESOURCE: resources.tmux_socket is required")
    observed_owner = responsive_owner(owner, socket)
    if selected == "outside-stopped":
        evidence["checks"].extend(outside_checks(owner, observed_owner))
    with faults.stopped_tmux(socket) as birth:
        evidence["resources"]["tmux_socket"] = socket
        evidence["resources"]["tmux_birth"] = birth
        evidence["checks"].extend(stopped_checks(birth))
        yield evidence


def action(owner, arguments, prefix, bound=30, *, expected_denial=None):
    if not arguments:
        raise ValueError("FORECAST-ACTION: an owner-entry argv is required")
    receipt = str(prefix)
    for suffix in (".status", ".out"):
        path = Path(receipt + suffix)
        if path.exists() or path.is_symlink():
            raise ValueError(f"FORECAST-RECEIPT-EXISTS: {path}")
    Path(receipt).parent.resolve(strict=True).relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True))
    checked_owner(owner, expected_denial=expected_denial)
    type_command(owner, list(arguments), receipt=receipt)
    command(["bash", "-c", 'source "$1/lib.sh"; poll forecast-owner-receipt "$2" test -s "$3"',
             "forecast-wait", str(HERE), str(bound), receipt + ".status"], timeout=bound + 5)
    return {"argv": list(arguments), "exit_code": int(Path(receipt + ".status").read_text().strip()),
            "output": Path(receipt + ".out").read_text(), "status_file": receipt + ".status", "output_file": receipt + ".out"}


def run(value, chat_file, owner_file, prefix, resources_file, arguments):
    values = factors(value)
    chat, owner, resources = read_json(chat_file), read_json(owner_file), read_json(resources_file)
    prefix = Path(prefix)
    root = Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True)
    prefix.parent.resolve(strict=True).relative_to(root)
    if prefix.is_symlink():
        raise ValueError("FORECAST-ARTIFACT-SYMLINK")
    target = str(prefix) + ".target.json"
    result = {"factors": values, "operation": operation(values)}
    if result["operation"] == "herdr-reader" and arguments[:2] != ["pfm", "ls"]:
        raise ValueError("FORECAST-LIST-ENTRY: public pfm ls argv required")
    if values.get("caller") == "claude_launch_outside_pane" and arguments[:3] != ["pfm", "chat", "new"]:
        raise ValueError("FORECAST-LAUNCH-ENTRY: public pfm chat new argv required")
    if result["operation"] == "stopped-probe":
        targeted_entry(arguments, resources.get("tmux_socket", ""))
        if type(resources.get("max_wall_ms")) is not int or resources["max_wall_ms"] <= 0:
            raise ValueError("FORECAST-PROBE-BOUND: positive resources.max_wall_ms is required")
    faults.owned_target(chat["terminal"])
    write_json(target, chat["terminal"])
    with scope(values, chat, owner, resources) as evidence:
        result["evidence"] = evidence
        started = now_ms()
        result["receipt"] = action(owner, arguments, str(prefix) + ".command")
        result["receipt"]["elapsed_ms"] = now_ms() - started
        if result["operation"] == "stopped-probe":
            evidence["checks"].append(bounded_check(result["receipt"]["elapsed_ms"], resources["max_wall_ms"]))
    if "tmux_birth" in evidence["resources"]:
        actual = int(command(["tmux", "-S", resources["tmux_socket"], "display-message", "-p", "#{pid}"]).strip())
        evidence["checks"].append(check("restored-server-pid", evidence["resources"]["tmux_birth"]["pid"], actual))
    result["observation"] = bundle(target)
    result["observation_phase"] = "after-restore"
    result["restored"] = terminal(owner)["identity"]
    output = str(prefix) + ".forecast.json"
    write_json(output, result)
    print(output)


def main(*arguments):
    if len(arguments) < 8 or arguments[0] != "run" or arguments[6] != "--":
        raise ValueError("FORECAST-USAGE: run FACTORS CHAT OWNER PREFIX RESOURCES -- COMMAND...")
    run(*arguments[1:6], list(arguments[7:]))


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError, KeyError, csv.Error) as error:
        print(f"FORECAST-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
