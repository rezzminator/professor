#!/usr/bin/env python3
"""Public branch setup and carried identities; every action uses an owner entry."""
import json
import os
from pathlib import Path
import re
import sqlite3
import sys
import uuid

from bootstrap import rearm_picker, wait
from herdr import create_tab
from observation import bundle, fleet_rows, report, screen
from runtime import command, read_json, write_json
from terminal import keys, text, type_command

from chat_carry import (ENGINES, branch_parts, carry, expects_live, select, seats, owned,
                        socket_path, seat_terminal, before, metadata_path, capture, remember,
                        adopt_child, adopt_continuation, observed_scenario, refresh, workspace_id,
                        annotate, adopt_branch, adopt_cli_child, expect_ended)


def branch_cwd(branch, project="atlas"):
    _, _, place, _ = branch_parts(branch)
    home = Path(os.environ["HOME"])
    base = home / "work" / project
    return str({"project": base, "subfolder": base / "docs/specs",
                "worktree": base / ".worktrees/flight-export"}[place])


def fresh_shell(branch, workspace="atlas", cwd=None):
    _, terminal_kind, _, _ = branch_parts(branch)
    folder = str(cwd or branch_cwd(branch))
    declared = carry()
    if terminal_kind == "tmux":
        socket = declared["owner"]["socket"]
        pane = command(["tmux", "-S", socket, "new-window", "-d", "-P", "-F", "#{pane_id}",
                        "-t", "owner", "-c", folder, os.environ["SHELL"], "-l"]).strip()
        target = {"backend": "tmux", "socket": socket, "pane": pane}
    else:
        result = create_tab(workspace_id(workspace=workspace), folder)
        target = {"backend": "herdr", "pane": result["root_pane"]["pane_id"]}
    wait("branch shell or picker", lambda: "New chat" in screen(target) or shell_ready(target), 20)
    if "New chat" in screen(target):
        keys(target, ["escape"])
    wait("branch parent shell", lambda: shell_ready(target), 20)
    return target


def shell_ready(target):
    lines = [line for line in screen(target).splitlines() if line.strip()]
    return bool(lines and lines[-1].rstrip().endswith(("%", "$", "#")))


def scenario(target, branch, origin, fixture, project="atlas", workspace=None):
    path = metadata_path(branch, origin)
    if path.exists():
        raise ValueError(f"CHAT-SCENARIO-EXISTS: {path}; live chats use inline steps")
    directory = path.parent / origin
    scenario_path, records = directory / "scenario.json", directory / "records"
    session_id = str(uuid.uuid4())
    value = {**read_json(os.environ["MOCK_ENGINE_SCENARIO"]), **read_json(fixture),
             "session_id": session_id, "record_dir": str(records), "action_file": os.environ["PFM_BEAT_FILE"]}
    write_json(scenario_path, value)
    socket = ENGINES[branch_parts(branch)[3]] + "-" + session_id
    metadata = {"scenario": str(scenario_path), "records": str(records), "scenario_session_id": session_id,
                "launch_socket": socket_path(socket), "project": project, "workspace": workspace_id(project, workspace)}
    write_json(path, metadata)
    type_command(target, ["export", "MOCK_ENGINE_SCENARIO=" + str(scenario_path), "PFM_TEST_FRESH_SOCKET=" + socket])
    return metadata


def inline(target, steps, prompt="journey-script"):
    value = read_json(steps)
    if not isinstance(value, (dict, list)) or "\n" in prompt:
        raise ValueError("CHAT-INLINE: one prompt and a JSON step object or array required")
    text(target, prompt + " mock-engine: " + json.dumps(value, separators=(",", ":")))
    keys(target, ["enter"])


def wizard(target, engine, account, cache, receipt=None):
    if engine not in ENGINES or not account.isdecimal() or cache not in {"1h", "5m"}:
        raise ValueError("CHAT-WIZARD: engine, numeric account and 1h|5m required")
    engine_labels = {"claude": "Claude", "codex": "Codex", "opencode": "OpenCode"}
    observed = {}

    def note(selected):
        if selected in {"1h", "5m"}:
            observed.setdefault("cache", selected)
        elif selected not in engine_labels.values():
            match = re.search(r"(\d+)$", selected or "")
            if match:
                observed.setdefault("account", int(match.group(1)))
        if receipt is not None:
            write_json(receipt, observed)

    def state(previous=None):
        shown = screen(target)
        selected = [chip for chip in re.findall(r"◖ (.+?) ◗", shown) if not chip.startswith("⏎ ")]
        if len(selected) > 1:
            raise ValueError(f"CHAT-WIZARD-AMBIGUOUS: {selected!r}")
        started = "◆ pfm " not in shown and any(glyph in shown for glyph in ("❯", "›", "FIXTURE-OPENCODE-PROMPT"))
        value = selected[0] if selected else "started" if started else None
        return value if value != previous else None
    keys(target, ["enter"])
    selected = wait("wizard choice", state, 10)
    note(selected)
    for _ in range(3):
        if selected == "started":
            return observed
        expected = engine_labels[engine] if selected in engine_labels.values() else cache if selected in {"1h", "5m"} else account
        note(selected)
        for _ in range(8):
            if selected == expected or (expected == account and selected.split()[-1] == account):
                break
            previous = selected
            keys(target, ["right"])
            selected = wait("wizard next chip", lambda: state(previous), 10)
            note(selected)
        else:
            raise ValueError(f"CHAT-WIZARD-CHOICE: expected={expected!r} actual={selected!r}")
        previous = selected
        keys(target, ["enter"])
        selected = wait("wizard next step", lambda: state(previous), 10)
        note(selected)
    if selected != "started":
        raise ValueError(f"CHAT-WIZARD-UNFINISHED: {selected!r}")
    return observed


def pick_new(target, project, engine, account, cache):
    def selected():
        if re.search(r"›\s+✦ New chat.*" + re.escape(project), screen(target)):
            return True
        keys(target, ["down"])
        return False
    type_command(target, ["pfm"])
    wait(project + "-new-chat-row", selected, 10)
    return wizard(target, engine, account, cache)


def pick(target, branch, origin, key=None):
    current = select(branch, origin)
    if current is None:
        raise ValueError(f"CHAT-PICK-ABSENT: {branch}:{origin}")
    include_hidden = current.get("expect", {}).get("visibility") in {"killed", "hidden"}
    if include_hidden and "◆ pfm " in screen(target):
        keys(target, ["escape"])
        wait("killed picker parent shell", lambda: shell_ready(target), 10)
    if include_hidden or "◆ pfm " not in screen(target):
        if not shell_ready(target):
            raise ValueError("CHAT-PICK-NOT-SHELL: picker absent and parent shell not visible")
        type_command(target, ["pfm", "ls", "--all"] if include_hidden else ["pfm"])
        wait("typed picker open", lambda: "◆ pfm " in screen(target), 10)
    keys(target, ["ctrl+u"])
    text(target, current["name"])
    from observation_picker import select_session
    select_session(target, current, [row["id"] for row in fleet_rows()], screen, keys)
    if key:
        keys(target, [key])
    return current


def ensure(branch, origin, fixture=None):
    current = select(branch, origin)
    if current:
        if not expects_live(current):
            raise ValueError(f"CHAT-ENSURE-LIFECYCLE: {current['key']}")
        status = json.loads(report(["pfm", "chat", "status", current["session_id"], "--json"], allowed_codes=(0, 3)))
        if status["state"] in {"idle", "working", "blocked"}:
            return current
        target = fresh_shell(branch, current.get("workspace", current["project"]),
                             branch_cwd(branch, current["project"]))
        reopen(target, branch, origin)
        return wait("reopened carried chat", lambda: refresh(branch, origin), 30)
    return ensure_role(branch, origin, None, fixture)


def stage_role(target, branch, role, fixture, project="atlas", workspace=None):
    origin = "new-from-cli"
    metadata = scenario(target, branch, role, fixture, project, workspace)
    if role != origin:
        metadata.update({"origin": origin, "role": role, "key": "chat-role:" + branch + ":" + role})
        write_json(metadata_path(branch, role), metadata)
    return metadata


def ensure_role(branch, role, name, fixture=None, project="atlas", workspace=None):
    branch_parts(branch, role)
    if select(branch, role):
        return ensure(branch, role, fixture)
    folder = branch_cwd(branch, project)
    target = fresh_shell(branch, workspace or project, folder)
    fixture = fixture or Path(__file__).parent / "scenarios/default.json"
    metadata = stage_role(target, branch, role, fixture, project, workspace)
    previous = before(target)
    backend = "tmux" if branch_parts(branch)[1] == "tmux" else "herdr"
    arguments = ["pfm", "chat", "new", "--engine", ENGINES[branch_parts(branch)[3]],
                 "--backend", backend, "--account", "1", "--cwd", folder]
    arguments += ["--name", "need-" + metadata["scenario_session_id"] if name is None else name]
    write_json(metadata_path(branch, role), {**metadata, "launch_target": target, "launch_argv": arguments})
    type_command(target, arguments)
    return wait("public CLI prelude chat", lambda: capture(target, branch, role, previous, project, workspace), 30)


def reopen(target, branch, origin):
    current = select(branch, origin)
    if current is None:
        raise ValueError(f"CHAT-REOPEN-ABSENT: {branch}:{origin}")
    if not expects_live(current):
        raise ValueError(f"CHAT-REOPEN-LIFECYCLE: {current['key']}: {current['expect']['lifecycle']}")
    type_command(target, ["pfm", "chat", "open", current["session_id"]])
    return current


def main(operation, *args):
    if operation in {"select", "refresh", "need"}:
        value = refresh(*args[:2]) if operation == "refresh" else select(*args[:2])
        if value is None:
            return 1
        if operation == "need":
            if not expects_live(value):
                return 1
            status = json.loads(report(["pfm", "chat", "status", value["session_id"], "--json"], allowed_codes=(0, 3)))
            return 0 if status["state"] in {"idle", "working", "blocked"} else 1
    elif operation == "ensure":
        value = ensure(*args)
    elif operation == "ensure-role":
        value = ensure_role(*args)
    elif operation == "annotate":
        value = annotate(args[0], args[1], read_json(args[2]))
    elif operation == "adopt-branch":
        value = adopt_branch(read_json(args[0]), args[1], args[2], args[3])
    elif operation == "adopt-child":
        value = adopt_child(read_json(args[0]), args[1], read_json(args[2]), *args[3:])
    elif operation == "adopt-cli-child":
        value = adopt_cli_child(read_json(args[0]), args[1], args[2], args[3])
    elif operation == "expect-ended":
        value = expect_ended(args[0], args[1], read_json(args[2]))
    elif operation == "adopt-continuation":
        value = adopt_continuation(read_json(args[0]), args[1], args[2], args[3], read_json(args[4]), *args[5:])
        if value is None:
            return 1
    elif operation == "before":
        write_json(args[1], before(read_json(args[0])))
        return 0
    elif operation == "capture":
        value = capture(read_json(args[0]), args[1], args[2], read_json(args[3]), *args[4:])
        if value is None:
            return 1
    elif operation == "shell-ready":
        return 0 if shell_ready(read_json(args[0])) else 1
    elif operation == "shell":
        value = fresh_shell(args[0], *args[2:])
        write_json(args[1], value)
    elif operation == "stage-role":
        value = stage_role(read_json(args[0]), *args[1:])
    elif operation == "scenario":
        value = scenario(read_json(args[0]), *args[1:])
    elif operation == "inline":
        inline(read_json(args[0]), *args[1:])
        return 0
    elif operation == "pick-new":
        value = pick_new(read_json(args[0]), *args[1:])
    elif operation == "wizard":
        value = wizard(read_json(args[0]), *args[1:])
    elif operation == "picker-ready":
        value = rearm_picker(read_json(args[0]))
    elif operation == "reload-ready":
        from observation import reload_ready, terminal
        current = terminal(read_json(args[2]))
        if len(args) == 4:
            write_json(args[3], current)
        return 0 if reload_ready(read_json(args[0]), current, read_json(args[1])) else 1
    elif operation == "pick":
        value = pick(read_json(args[0]), *args[1:])
    elif operation == "reopen":
        value = reopen(read_json(args[0]), *args[1:])
    elif operation == "snapshot":
        value = bundle(args[0])
        write_json(args[1], value)
        if value["errors"]:
            raise ValueError(f"CHAT-SNAPSHOT: {value['errors']!r}")
    else:
        raise ValueError("usage: chat.py OPERATION ...; public signatures are in lib.sh")
    print(json.dumps(value, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(*sys.argv[1:]))
    except (OSError, ValueError, RuntimeError, KeyError, IndexError, sqlite3.Error) as error:
        print(f"CHAT-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
