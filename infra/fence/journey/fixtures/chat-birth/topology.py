#!/usr/bin/env python3
"""Native shell-tool spawning and actual tmux attach environment evidence."""
import json
import os
from pathlib import Path
import shlex
import sys

from boot import native_records
from bootstrap import wait
from chat import before, capture, inline, refresh, scenario
from faults import owned_path, signalled_process
from observation import process_snapshot, report, terminal, terminal_births
from observation_process import KEYS, engine_births
from runtime import now_ms, read_json, write_json, write_text
from terminal import keys, type_command

PANE_KEYS = {"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH",
             "HERDR_WORKSPACE_ID", "HERDR_TAB_ID"}
CODEX_KEYS = {"CODEX_TOOL_CALL_ID", "NO_COLOR", "PAGER"}


def supported(factors):
    rows = [row for row in read_json(Path(__file__).with_name("topology-rows.json"))
            if row["factors"] == factors]
    if len(rows) != 1:
        raise ValueError("BIRTH-TOPOLOGY-FACTORS: " + factors)
    row = rows[0]
    return row


def expected(factors):
    return supported(factors)["expected"]


def staging_path(directory):
    path = owned_path(directory)
    if not path.is_dir():
        raise ValueError("BIRTH-TOPOLOGY-STAGING: existing run-owned directory required")
    return path


def prepare(factors, directory):
    row = supported(factors)
    directory = staging_path(directory)
    path = directory / "topology-input.json"
    if path.exists():
        raise ValueError("BIRTH-TOPOLOGY-REUSED: staging directory already prepared")
    write_json(path, read_json(Path(__file__).with_name("scenario.json")))
    write_json(directory / "topology-state.json", {"factors": factors})
    if row["kind"] == "wrapper":
        wrapper = directory / "topology-claude"
        write_text(wrapper, '#!/usr/bin/env bash\n/usr/local/bin/claude "$@" <&0 &\nwait "$!"\n', mode=0o700)
        config = read_json(owned_path(os.environ["PFM_CONFIG"]))
        config.setdefault("claude", {})["binary"] = str(wrapper)
        config.setdefault("chat", {})["backend"] = "tmux"
        write_json(directory / "topology-config.json", config)
    return str(path)


def before_entry(factors, directory, owner_file):
    row = supported(factors)
    directory = staging_path(directory)
    owner = read_json(owner_file)
    normal = read_json(directory / "scenario.json")
    branch = os.environ["JOURNEY_BRANCH"].split("+")
    engine = "codex" if row["kind"] == "attach" else "claude"
    if branch[1:] != ["herdr-shield", "project", engine] or owner["backend"] != "herdr":
        raise ValueError("BIRTH-TOPOLOGY-CALLER: canonical Herdr shield project " + engine + " birth required")
    child_branch = branch[0] + "+tmux+project+claude"
    origin = "topology-child-" + normal["scenario_session_id"]
    child = scenario(owner, child_branch, origin, str(directory / "topology-input.json"),
                     normal["project"], normal["workspace"])
    type_command(owner, ["export", "MOCK_ENGINE_SCENARIO=" + normal["scenario"],
                         "PFM_TEST_FRESH_SOCKET=" + Path(normal["launch_socket"]).name])
    write_json(directory / "topology-state.json", {"factors": factors, "owner": owner,
               "child": child, "branch": child_branch, "origin": origin})
    return {"child_branch": child_branch, "child_origin": origin}


def observed_engine(current):
    display = terminal(current["terminal"])
    births = engine_births({"terminal": display, "process_births": terminal_births(display)}, current["engine"])
    if len(births) != 1:
        raise ValueError("BIRTH-TOPOLOGY-ENGINE: exactly one classified native engine required")
    return births[0], display


def tool_steps(command, reply):
    return [{"type": "tool_call", "tool": "exec_command",
             "input": {"cmd": command, "run_in_background": True}},
            {"type": "turn", "reply": reply, "busy_ms": 0}]


def run_tool(current, directory, name, command):
    path = directory / (name + "-steps.json")
    write_json(path, tool_steps(command, name + "-started"))
    inline(current["terminal"], str(path), name)


def shell_record(records, command):
    path = Path(records) / "shell.jsonl"
    def recorded():
        values = [value for value in native_records(path) if value["command"] == command] if path.is_file() else []
        if len(values) > 1:
            raise ValueError("BIRTH-TOPOLOGY-SHELL-AMBIGUOUS: " + str(path))
        return values[0] if values else None
    return wait("native topology shell receipt " + str(path), recorded, 15)


def projection(evidence):
    child = evidence["child_birth"]
    return {"caller_engine": evidence["caller"]["engine"],
            "native_shell_parent_is_caller": evidence["shell"]["parent"] == evidence["caller_birth"]["pid"],
            "native_shell_receipt_bound": evidence["spawn_receipt"]["pid"] == evidence["shell"]["pid"],
            "native_child_record_bound": evidence["child_native"]["pid"] == child["pid"] and
                evidence["child_native"]["parent_pid"] == evidence["child_process"]["parent"],
            "child_engine": evidence["child"]["engine"], "child_backend": evidence["child"]["terminal"]["backend"],
            "child_pane_identity_stripped": not (PANE_KEYS & child["environment"].keys()),
            "child_codex_injections_stripped": not (CODEX_KEYS & child["environment"].keys()),
            "attach_recopied_caller_pane": evidence["session_pane"] == "HERDR_PANE_ID=" + evidence["caller_birth"]["environment"]["HERDR_PANE_ID"],
            "attach_control_client": evidence["clients"] == "1\n",
            "same_child_birth_after_attach": (child["pid"], child["start"], child["terminal"]) ==
                (evidence["child_after"]["pid"], evidence["child_after"]["start"], evidence["child_after"]["terminal"]),
            "native_spawn_exit": evidence["spawn_receipt"]["exit_code"],
            "native_attach_exit": evidence["attach_receipt"]["exit_code"]}


def native_birth(current, birth):
    path = Path(current["records"]) / "birth.json"
    rows = [value for value in native_records(path) if value["pid"] == birth["pid"]]
    if len(rows) != 1:
        raise ValueError("BIRTH-TOPOLOGY-NATIVE-BIRTH: one matching actual birth required: " + str(path))
    return rows[0]


def wrapper_projection(evidence):
    initial, resumed = evidence["initial_birth"], evidence["resumed_birth"]
    return {"child_engine": evidence["child"]["engine"], "child_backend": evidence["child"]["terminal"]["backend"],
            "pane_command": evidence["pane_command"],
            "engine_is_wrapper_child": evidence["resumed_native"]["parent_pid"] == evidence["wrapper"]["pid"],
            "same_tty": evidence["engine_tty"] == evidence["wrapper_tty"] and evidence["engine_tty"] not in {"", "?", "??"},
            "new_birth_in_same_terminal": (initial["pid"], initial["start"]) != (resumed["pid"], resumed["start"]) and initial["terminal"] == resumed["terminal"],
            "same_native_session": evidence["child"]["session_id"] == evidence["resumed"]["session_id"],
            "public_open_before_quiet_fallback": 0 <= evidence["open_ms"] < 5000,
            "native_birth_records_bound": evidence["initial_native"]["pid"] == initial["pid"] and evidence["resumed_native"]["pid"] == resumed["pid"],
            "native_spawn_exit": evidence["spawn_status"], "native_open_exit": evidence["open_status"]}


def wrapper_after_entry(factors, directory, current, state):
    owner = state["owner"]
    child_meta = state["child"]
    socket = child_meta["launch_socket"]
    environment = ["env", "PFM_CONFIG=" + str(directory / "topology-config.json"),
                   "MOCK_ENGINE_SCENARIO=" + child_meta["scenario"],
                   "PFM_TEST_FRESH_SOCKET=" + Path(socket).name]
    prefix = str(directory / "topology-new")
    previous = before(owner)
    type_command(owner, [*environment, "pfm", "chat", "new", "--engine", "claude", "--backend", "tmux", "--name", "atlas-topology-wrapper"], prefix)
    wait("wrapper native CLI receipt", lambda: Path(prefix + ".status").is_file(), 30)
    child = wait("wrapper native child capture", lambda: capture(owner, state["branch"], state["origin"], previous), 20)
    initial, display = observed_engine(child)
    evidence = {"factors": factors, "caller": current, "child": child, "initial_birth": initial,
                "initial_native": native_birth(child, initial), "initial_terminal": display,
                "spawn_status": Path(prefix + ".status").read_text().strip(), "started_at_ms": now_ms()}
    reopen = str(directory / "topology-open")
    def restore():
        started = now_ms()
        type_command(owner, [*environment, "pfm", "chat", "open", child["session_id"]], reopen)
        def attached():
            # A client can attach only after watchLaunch returns; five seconds is its quiet fallback.
            if not Path(socket).exists():
                return None
            value = report(["tmux", "-S", socket, "list-clients", "-F", "#{client_pid}"])
            return value.strip() or None
        try:
            evidence["attached_client"] = wait("public compound resume attached after launch watch", attached, 20)
            evidence["open_ms"] = now_ms() - started
            resumed = wait("wrapper resumed native carry", lambda: refresh(child["branch"], child["origin"]), 20)
            evidence["resumed"] = resumed
            evidence["resumed_birth"], evidence["resumed_terminal"] = observed_engine(resumed)
            native = native_birth(resumed, evidence["resumed_birth"])
            evidence["resumed_native"] = native
            evidence["wrapper"] = process_snapshot()[native["parent_pid"]]
            evidence["pane_command"] = report(["tmux", "-S", socket, "display-message", "-p", "-t", resumed["terminal"]["pane"], "#{pane_current_command}"]).strip()
            evidence["engine_tty"] = report(["ps", "-o", "tty=", "-p", str(native["pid"])]).strip()
            evidence["wrapper_tty"] = report(["ps", "-o", "tty=", "-p", str(native["parent_pid"])]).strip()
        finally:
            keys(owner, ["ctrl+b", "d"])
        wait("public compound resume receipt after detach", lambda: Path(reopen + ".status").is_file(), 15)
        evidence["open_status"] = Path(reopen + ".status").read_text().strip()
    try:
        with signalled_process(child["terminal"], initial, "TERM", restore=restore):
            wait("actual initial wrapper child exited", lambda: initial["pid"] not in process_snapshot(), 10)
            wait("actual initial wrapper server exited", lambda: not Path(socket).exists(), 10)
    finally:
        evidence["completed_at_ms"] = now_ms()
        write_json(directory / "topology-evidence.json", evidence)
    evidence["projection"] = wrapper_projection(evidence)
    write_json(directory / "topology-evidence.json", evidence)
    return evidence["projection"]


def after_entry(factors, directory, chat_file):
    row = supported(factors)
    directory = staging_path(directory)
    state = read_json(directory / "topology-state.json")
    if state["factors"] != factors:
        raise ValueError("BIRTH-TOPOLOGY-STATE: staged factors changed")
    current = read_json(chat_file)
    metadata = read_json(directory / "scenario.json")
    if current["scenario"] != metadata["scenario"] or current["records"] != metadata["records"]:
        raise ValueError("BIRTH-TOPOLOGY-CAPTURE: actual staged native caller required")
    if row["kind"] == "wrapper":
        return wrapper_after_entry(factors, directory, current, state)
    if not (PANE_KEYS | CODEX_KEYS) <= KEYS:
        raise ValueError("BIRTH-TOPOLOGY-ENV-UNAVAILABLE: shared environment door omits " + ",".join(sorted((PANE_KEYS | CODEX_KEYS) - KEYS)))
    if current["engine"] != "codex":
        raise ValueError("BIRTH-TOPOLOGY-CAPTURE: actual staged Codex caller required")
    caller_birth, caller_display = observed_engine(current)
    if not caller_birth["environment"].get("HERDR_PANE_ID"):
        raise ValueError("BIRTH-TOPOLOGY-PANE: caller must have actual Herdr pane identity")
    previous = before(current["terminal"])
    spawn_release, attach_release = directory / "topology-spawn-release.json", directory / "topology-attach-release.json"
    pid_path, status_path = directory / "topology-shell.pid", directory / "topology-cli.status"
    child_meta = state["child"]
    arguments = ["env", "MOCK_ENGINE_SCENARIO=" + child_meta["scenario"],
                 "PFM_TEST_FRESH_SOCKET=" + Path(child_meta["launch_socket"]).name,
                 "pfm", "chat", "new", "--engine", "claude", "--backend", "tmux", "--name", "atlas-topology-child"]
    spawn_command = ("export CODEX_TOOL_CALL_ID=journey-topology NO_COLOR=1 PAGER=cat; "
                     "printf '%s\\n' \"$BASHPID\" > " + shlex.quote(str(pid_path)) + "; " + shlex.join(arguments) +
                     " > " + shlex.quote(str(directory / "topology-cli.out")) + " 2>&1; status=$?; "
                     "printf '%s\\n' \"$status\" > " + shlex.quote(str(status_path)) +
                     "; while [ ! -e " + shlex.quote(str(spawn_release)) + " ]; do sleep .1; done; exit \"$status\"")
    session = Path(child_meta["launch_socket"]).name
    attach_command = ("{ while [ ! -e " + shlex.quote(str(attach_release)) +
                      " ]; do sleep .1; done; printf 'detach-client\\n'; } | " +
                      shlex.join(["tmux", "-C", "-S", child_meta["launch_socket"], "attach-session", "-t", session]))
    evidence = {"factors": factors, "caller": current, "caller_birth": caller_birth,
                "caller_terminal": caller_display, "started_at_ms": now_ms()}
    try:
        run_tool(current, directory, "topology-spawn", spawn_command)
        wait("native tmux spawn returned", lambda: status_path.is_file(), 30)
        if status_path.read_text().strip() != "0":
            raise ValueError("BIRTH-TOPOLOGY-SPAWN: " + (directory / "topology-cli.out").read_text())
        shell_pid = int(pid_path.read_text())
        evidence["shell"] = process_snapshot()[shell_pid]
        child = wait("native topology child capture", lambda: capture(current["terminal"], state["branch"], state["origin"], previous), 20)
        evidence["child"] = child
        evidence["child_birth"], evidence["child_terminal"] = observed_engine(child)
        evidence["child_native"] = native_birth(child, evidence["child_birth"])
        evidence["child_process"] = process_snapshot()[evidence["child_birth"]["pid"]]
        socket = child["terminal"]["socket"]
        if socket != child_meta["launch_socket"]:
            raise ValueError("BIRTH-TOPOLOGY-SOCKET: captured child is not the staged native launch")
        run_tool(current, directory, "topology-attach", attach_command)
        def attached():
            clients = report(["tmux", "-S", socket, "list-clients", "-F", "#{client_control_mode}"])
            return clients if clients == "1\n" else None
        evidence["clients"] = wait("real tmux control client attached", attached, 15)
        evidence["session_pane"] = report(["tmux", "-S", socket, "show-environment", "-t", session, "HERDR_PANE_ID"]).strip()
        evidence["child_after"], evidence["attached_terminal"] = observed_engine(child)
    finally:
        write_json(attach_release, {"release": True})
        write_json(spawn_release, {"release": True})
        evidence["completed_at_ms"] = now_ms()
        write_json(directory / "topology-evidence.json", evidence)
    evidence["spawn_receipt"] = shell_record(current["records"], spawn_command)
    evidence["attach_receipt"] = shell_record(current["records"], attach_command)
    evidence["projection"] = projection(evidence)
    write_json(directory / "topology-evidence.json", evidence)
    return evidence["projection"]


def assert_topology(factors, directory):
    row = supported(factors)
    evidence = read_json(staging_path(directory) / "topology-evidence.json")
    if evidence["factors"] != factors:
        raise ValueError("BIRTH-TOPOLOGY-EVIDENCE: factors changed")
    actual = (wrapper_projection if row["kind"] == "wrapper" else projection)(evidence)
    if actual != expected(factors):
        raise ValueError(f"BIRTH-TOPOLOGY-PROJECTION: expected={expected(factors)!r} actual={actual!r}")
    return actual


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        value = {"prepare": prepare, "before-entry": before_entry, "after-entry": after_entry,
                 "assert": assert_topology, "expected": expected, "supported": supported}[operation](*arguments)
        print(value if isinstance(value, str) else json.dumps(value, sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print("BIRTH-TOPOLOGY-ERROR: " + str(error), file=sys.stderr)
        sys.exit(2)
