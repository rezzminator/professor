#!/usr/bin/env python3
"""Owner entries and native evidence for the remaining birth escapes."""
import copy
import json
import os
from pathlib import Path
import shlex
import sys

from bootstrap import wait
from chat import capture, inline, refresh
from faults import owned_path, owned_target, released_agent, temporary_text
from herdr import Refused, request
import observation
from observation_process import engine_births
import runtime
import terminal
from boot import native_records
from edge import owner_action


def scope(directory, engine):
    if (os.environ.get("PFM_DEV_FENCE") != "1" or not os.environ.get("PFM_RUN") or
            os.environ["JOURNEY_BRANCH"] != "linux+herdr-shield+project+" + engine):
        raise ValueError("BIRTH-ESCAPE-SCOPE: fenced Linux Herdr project branch required")
    owned_path(os.environ["HERDR_SOCKET_PATH"])
    directory = owned_path(directory)
    if not directory.is_dir():
        raise ValueError("BIRTH-ESCAPE-STAGING: existing owned directory required")
    return directory


def native_birth(current):
    display = observation.terminal(current["terminal"])
    births = engine_births({"terminal": display, "process_births": observation.terminal_births(display)}, current["engine"])
    if len(births) != 1:
        raise ValueError("BIRTH-ESCAPE-NATIVE-BIRTH: exactly one actual engine required")
    return births[0]


def topology():
    socket = os.environ["HERDR_SOCKET_PATH"]
    workspaces = request(socket, "workspace.list", {})["result"]["workspaces"]
    panes = request(socket, "pane.list", {})["result"]["panes"]
    tabs = [tab for workspace in workspaces for tab in
            request(socket, "tab.list", {"workspace_id": workspace["workspace_id"]})["result"]["tabs"]]
    engines = []
    for pane in panes:
        display = observation.terminal({"backend": "herdr", "pane": pane["pane_id"]})
        observed = {"terminal": display, "process_births": observation.terminal_births(display)}
        for engine in ("claude", "codex", "opencode"):
            engines.extend(engine_births(observed, engine))
    return {"workspaces": sorted((row["workspace_id"], row["label"]) for row in workspaces),
            "tabs": sorted((row["tab_id"], row["workspace_id"], row["label"]) for row in tabs),
            "panes": sorted((row["pane_id"], row["tab_id"], row["terminal_id"]) for row in panes),
            "engines": sorted({(row["pid"], row["start"]) for row in engines})}


def alias_projection(evidence):
    return {"exit_status": evidence["receipt"]["status"], "stdout": evidence["stdout"],
            "stderr": evidence["stderr"], "topology_preserved": evidence["before"] == evidence["after"],
            "catalogue_restored": evidence["catalogue_before"] == evidence["catalogue_after"]}


def unknown_alias(directory, target_file, name):
    directory = scope(directory, "codex")
    target = runtime.read_json(owned_path(target_file))
    if target["backend"] != "herdr" or not name.startswith("atlas-"):
        raise ValueError("BIRTH-ESCAPE-ALIAS-INPUT: Herdr caller and owned atlas name required")
    catalogue = owned_path(Path(os.environ["HOME"]) / ".codex/models_cache.json", preserve=True)
    evidence = {"catalogue_before": catalogue.read_text() if catalogue.exists() else None,
                "before": topology()}
    arguments = ["pfm", "chat", "new", "--engine", "codex", "--model", "bogus", "--name", name]
    stdout, stderr = directory / "alias.stdout", directory / "alias.stderr"
    if stdout.exists() or stderr.exists():
        raise ValueError("BIRTH-ESCAPE-ALIAS-REUSED: refusal artifacts already exist")
    line = shlex.join(arguments) + " >" + shlex.quote(str(stdout)) + " 2>" + shlex.quote(str(stderr))
    evidence["expected"] = {"exit_status": "2", "stdout": "", "stderr":
        'pfm chat new: codex model "bogus" is not offered by Codex (catalogue ' + str(catalogue) +
        '); offered: gpt-6-astra\n', "topology_preserved": True, "catalogue_restored": True}
    try:
        with temporary_text(catalogue, '{"models":[{"slug":"gpt-6-astra"}]}\n'):
            evidence["receipt"] = owner_action(target, directory, "alias-owner", ["sh", "-c", line])
            evidence.update(stdout=stdout.read_text(), stderr=stderr.read_text(), after=topology())
    finally:
        evidence["catalogue_after"] = catalogue.read_text() if catalogue.exists() else None
        runtime.write_json(directory / "alias-evidence.json", evidence)
    evidence["projection"] = alias_projection(evidence)
    runtime.write_json(directory / "alias-evidence.json", evidence)
    return evidence["projection"]


def permission_config(config):
    changed = copy.deepcopy(config)
    if any("permissionMode" in row.get("claude", {}) for row in config["accounts"]):
        raise ValueError("BIRTH-ESCAPE-PERMISSION-OVERRIDE: account permission source would mask global auto")
    changed.setdefault("claude", {})["permissionMode"] = "auto"
    return changed


def permission_projection(evidence):
    arguments = evidence["birth"]["argv"]
    modes = [arguments[index:index + 2] for index, word in enumerate(arguments) if word == "--permission-mode"]
    original = json.loads(evidence["original"])
    unchanged = copy.deepcopy(evidence["configured"])
    if "permissionMode" in original.get("claude", {}):
        unchanged["claude"]["permissionMode"] = original["claude"]["permissionMode"]
    else:
        unchanged["claude"].pop("permissionMode", None)
    return {"permission_arguments": modes, "config_restored": evidence["original"] == evidence["restored"],
            "configured_mode": evidence["configured"]["claude"]["permissionMode"],
            "other_sources_preserved": original == unchanged}


def permission_birth(directory, target_file, origin, name):
    directory = scope(directory, "claude")
    target = runtime.read_json(owned_path(target_file))
    if target["backend"] != "herdr" or origin != "new-from-cli":
        raise ValueError("BIRTH-ESCAPE-PERMISSION-ENTRY: canonical CLI Herdr origin required")
    config = owned_path(os.environ["PFM_CONFIG"])
    original = config.read_text()
    faulted = permission_config(json.loads(original))
    evidence = {"original": original, "faulted": faulted,
                "expected": {"permission_arguments": [["--permission-mode", "auto"]], "configured_mode": "auto",
                             "config_restored": True, "other_sources_preserved": True}}
    previous = runtime.read_json(directory / "before.json")
    try:
        with temporary_text(config, json.dumps(faulted) + "\n"):
            evidence["receipt"] = owner_action(target, directory, "permission-owner",
                                               ["pfm", "chat", "new", "--engine", "claude", "--name", name])
            current = wait("CLI Claude auto permission birth", lambda:
                           capture(target, os.environ["JOURNEY_BRANCH"], origin, previous), 25)
            evidence.update(chat=current, birth=native_birth(current), configured=runtime.read_json(config))
    finally:
        evidence["restored"] = config.read_text()
        runtime.write_json(directory / "permission-evidence.json", evidence)
    evidence["projection"] = permission_projection(evidence)
    runtime.write_json(directory / "permission-evidence.json", evidence)
    return evidence["projection"]


def current_chat(chat_file, engine):
    current = runtime.read_json(owned_path(chat_file))
    if current["engine"] != engine or current not in runtime.read_json(os.environ["JOURNEY_CARRY"])["chats"]:
        raise ValueError("BIRTH-ESCAPE-CAPTURE: actual carried " + engine + " required")
    owned_target(current["terminal"])
    return current


def title_projection(evidence):
    return {"native_titles": [row["thread_name"] for row in evidence["native_titles"]],
            "name": evidence["after"]["name"], "tab": evidence["terminal"]["tab"]["tab"]["label"],
            "birth_preserved": evidence["birth_before"] == evidence["birth_after"],
            "session_preserved": evidence["before"]["session_id"] == evidence["after"]["session_id"]}


def codex_title(directory, chat_file, name):
    directory = scope(directory, "codex")
    current = current_chat(chat_file, "codex")
    if current["origin"] != "new-from-cli":
        raise ValueError("BIRTH-ESCAPE-TITLE-ENTRY: canonical CLI origin required")
    path = owned_path(Path(os.environ["HOME"]) / ".codex/session_index.jsonl", preserve=True)
    records = owned_path(current["records"])
    prompt_file = records / "prompts"
    offset = prompt_file.stat().st_size if prompt_file.exists() else 0
    evidence = {"before": current, "birth_before": native_birth(current),
                "expected": {"native_titles": ["atlas-thread-title"], "name": name,
                             "tab": name,
                             "birth_preserved": True, "session_preserved": True}}
    terminal.text(current["terminal"], "/rename atlas-thread-title")
    terminal.keys(current["terminal"], ["enter"])
    def arrived():
        values = [row for row in native_records(path) if row.get("id") == current["session_id"]] if path.exists() else []
        return values[-1:] if values and values[-1]["thread_name"] == "atlas-thread-title" else None
    try:
        evidence["native_titles"] = wait("actual Codex session_index title arrival", arrived, 5)
        evidence["fleet_report"] = observation.report(["pfm", "ls", "--all", "--tsv"])
        evidence["after"] = refresh(current["branch"], current.get("role", current["origin"]))
        evidence.update(terminal=observation.terminal(current["terminal"]), birth_after=native_birth(current),
                        engine_input=prompt_file.read_bytes()[offset:].decode())
        runtime.write_json(directory / "codex-title-evidence.json", evidence)
        if evidence["engine_input"] != "/rename atlas-thread-title\n":
            raise ValueError("BIRTH-ESCAPE-TITLE-INPUT: native input receipt differs")
    finally:
        runtime.write_json(directory / "codex-title-evidence.json", evidence)
    evidence["projection"] = title_projection(evidence)
    runtime.write_json(directory / "codex-title-evidence.json", evidence)
    return evidence["projection"]


def agentless_rename(directory, chat_file, owner_file):
    directory = scope(directory, "claude")
    current = current_chat(chat_file, "claude")
    owner = runtime.read_json(owned_path(owner_file))
    owned_target(owner)
    fixture = directory / "rename-restore.json"
    runtime.write_json(fixture, [{"type": "turn", "reply": "rename-authority-restored", "busy_ms": 0}])
    evidence = {"before": current, "birth_before": native_birth(current),
                "rename_count": "n/a:public-backend-request-log-unavailable"}
    def restore():
        inline(current["terminal"], str(fixture), "rename-authority-restored")
        wait("actual Claude agent restored after public release", lambda:
             restored_agent(current["terminal"]), 15)
    try:
        with released_agent(current, restore):
            try:
                request(os.environ["HERDR_SOCKET_PATH"], "agent.get", {"target": current["terminal"]["pane"]})
            except Refused as error:
                evidence["agent_get_refusal"] = error.code
                evidence["agent_get_error"] = str(error)
            else:
                raise ValueError("BIRTH-ESCAPE-AGENT-PRESENT: public release did not remove the actual agent")
            if evidence["agent_get_refusal"] != "agent_not_found":
                raise ValueError("BIRTH-ESCAPE-AGENT-REFUSAL: " + str(evidence["agent_get_refusal"]))
            evidence["receipt"] = owner_action(owner, directory, "agentless-owner",
                                                ["pfm", "chat", "name", current["session_id"], "atlas-1"])
            evidence["after"] = refresh(current["branch"], current.get("role", current["origin"]))
            evidence.update(terminal=observation.terminal(current["terminal"]), birth_after=native_birth(current))
    finally:
        runtime.write_json(directory / "agentless-evidence.json", evidence)
    return agentless_projection(evidence)


def restored_agent(target):
    if "⏺ rename-authority-restored" not in [line.strip() for line in observation.terminal(target)["text"].splitlines()]:
        return None
    try:
        return request(os.environ["HERDR_SOCKET_PATH"], "agent.get", {"target": target["pane"]})
    except Refused as error:
        if error.code == "agent_not_found":
            return None
        raise


def agentless_projection(evidence):
    return {"agent_get_refusal": evidence["agent_get_refusal"], "name": evidence["after"]["name"],
            "exit_status": evidence["receipt"]["status"], "rename_count": evidence["rename_count"],
            "birth_preserved": evidence["birth_before"] == evidence["birth_after"]}


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        result = {"unknown-alias": unknown_alias, "permission-birth": permission_birth,
                  "codex-title": codex_title, "agentless-rename": agentless_rename}[operation](*arguments)
        print(json.dumps(result, sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print("BIRTH-ESCAPE-ERROR: " + str(error), file=sys.stderr)
        sys.exit(2)
