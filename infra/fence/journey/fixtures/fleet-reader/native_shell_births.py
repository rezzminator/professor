#!/usr/bin/env python3
"""Picker births from an inherited tmux shell and a user-labelled Herdr tab."""
import os
from pathlib import Path
import sys

from forecast import action, check, checked_owner
from bootstrap import tmux_server, wait
import chat
from chat_carry import carry, expect_ended
import faults
from herdr import request
from observation import bundle, process_snapshot, terminal
from observation_process import environment
from projection import bundle_checks, engine_birth
from runtime import read_json, write_json
from terminal import type_command

PLANS = {
    "pane_fronted=false,pane_env=inherited_stale,backend=tmux_shell": ("stale-tmux-shell", "shell-birth"),
    "tab_label=user_hand_set,backend=herdr_with_tmux,chat_launch=in_place_exec": ("user-tab-exec", "shell-birth"),
}


def describe(name):
    if name not in {value[0] for value in PLANS.values()}:
        raise ValueError(f"NATIVE-SHELL-BIRTH-PLAN: {name}")
    resources = ["subject", "birth_branch", "birth_origin", "project", "workspace", "fixture"]
    resources += ["socket_name", "session", "placeholder", "destination_workspace"] if name == "stale-tmux-shell" else ["id", "old_chat"]
    return {"plan": name, "entry": "shell-birth", "engine": "claude", "resources": resources, "argv": ["pfm", "ls", "--all", "--tsv"]}


def client_prelude(socket_name, session, output):
    faults.owned_path(Path(output).parent)
    write_json(output, tmux_server(socket_name, session))


def inherited_checks(original, moved, target, parent):
    return [check("tmux-shell-original-herdr-environment", original["identity"]["pane"], parent.get("HERDR_PANE_ID")),
            check("tmux-shell-moved-physical-terminal", original["pane"]["terminal_id"], moved["pane"]["terminal_id"]),
            check("tmux-shell-stale-pane", "different", "same" if parent.get("HERDR_PANE_ID") == moved["identity"]["pane"] else "different"),
            check("tmux-shell-native-socket", target["socket"], parent.get("TMUX", "").split(",")[0]),
            check("tmux-shell-not-foreground", [], [row["pid"] for row in moved["processes"]["foreground_processes"] if row["pid"] == target["shell_pid"]])]


def birth_checks(name, original, child, observed):
    expected_same = name == "user-tab-exec"
    actual_same = observed["pane"]["terminal_id"] == original["pane"]["terminal_id"]
    checks = [check("picker-born-native-backend", "herdr", child["terminal"]["backend"]),
              check("picker-born-engine", "claude", child["engine"]),
              check("picker-born-in-caller-terminal", expected_same, actual_same)]
    if expected_same:
        checks += [check("picker-keeps-hand-label", "Reader hand label", observed["tab"]["tab"]["label"]),
                   check("picker-keeps-hand-labelled-tab", original["identity"]["tab"], observed["identity"]["tab"])]
    return checks


def picker_birth(name, target, original, resources, owner, owner_file, prefix, evidence):
    import native_shells as shells
    staged = chat.stage_role(target, resources["birth_branch"], resources["birth_origin"], resources["fixture"], resources["project"], resources["workspace"])
    evidence["scenario"] = staged
    previous = chat.before(target)
    child = None
    try:
        evidence["picker"] = chat.pick_new(target, resources["project"], "claude", "1", "1h")
        child = wait("actual picker-born native identity", lambda: chat.capture(target, resources["birth_branch"], resources["birth_origin"], previous, resources["project"], resources["workspace"]), 30)
        evidence["child"] = child
        display = terminal(child["terminal"])
        shells.require(evidence, birth_checks(name, original, child, display), prefix)
        shells.reader(owner, owner_file, prefix, evidence)
        native = engine_birth(evidence["during"], child)
        shells.require(evidence, [check("picker-born-native-kernel-birth", "live", "live" if process_snapshot().get(native["pid"], {}).get("start") == native["start"] else "gone")], prefix)
    finally:
        if child is not None:
            before_end = bundle(owner_file)
            departing = engine_birth(before_end, child)
            receipt = action(owner, ["pfm", "chat", "end", child["session_id"]], str(prefix) + ".end-child")
            evidence.setdefault("receipts", []).append({**receipt, "prefix": str(prefix) + ".end-child", "door": "typed"})
            shells.require(evidence, [check("picker-child-public-end", 0, receipt["exit_code"])], prefix)
            wait("publicly ended native child kernel birth", lambda: faults.birth_exited(departing), 20)
            expect_ended(resources["birth_branch"], resources["birth_origin"], before_end)


def stale_shell(subject, owner, owner_file, resources, prefix, evidence):
    import native_shells as shells
    from native_forecasts import PaneMove
    mover = PaneMove(None, resources, subject=subject)
    original = terminal(subject)
    try:
        with faults.moved_pane(subject, mover.destination, mover.move) as moved:
            subject.update(moved)
            output = str(prefix) + ".tmux-target.json"
            type_command(subject, [sys.executable, str(Path(__file__).resolve()), "client-prelude", resources["socket_name"], resources["session"], output])
            wait("tmux server born through actual moved Herdr parent", lambda: Path(output).exists(), 15)
            target = read_json(output)
            with faults.fresh_target(target):
                checked_owner(target)
                snapshot = terminal(target)
                pid = int(snapshot["pane"]["pane_pid"])
                proof = {**target, "shell_pid": pid}
                shells.require(evidence, inherited_checks(original, terminal(subject), proof, environment(pid)), prefix)
                evidence["tmux_parent"] = {"target": target, "birth": process_snapshot()[pid], "environment": environment(pid)}
                picker_birth("stale-tmux-shell", target, original, resources, owner, owner_file, prefix, evidence)
    finally:
        subject.update({"backend": "herdr", "pane": mover.current()["pane_id"]})
        request(os.environ["HERDR_SOCKET_PATH"], "pane.close", {"pane_id": subject["pane"]})


def labelled_shell(subject, owner, owner_file, resources, prefix, evidence):
    import native_shells as shells
    from native_forecasts import engine_action, owned_chat
    old = read_json(resources["old_chat"])
    owned_chat(old)
    if old["engine"] != "claude" or old["terminal"]["backend"] != "tmux":
        raise ValueError("NATIVE-LABEL-PRIOR: actual carried Claude tmux actor required")
    target = old["terminal"]
    session = shells.client_session(target)
    evidence["changed"] = ["key:" + old["key"]]
    with shells.attached_client(subject, target, session, evidence):
        evidence["prior_label_action"] = engine_action(resources["old_chat"], ["pfm", "whoami", "--json"], str(prefix) + ".prior-label", resources["id"])
        rendered = shells.displayed(subject, "pfm actually rendered the prior chat label", lambda actual: actual["tab"]["tab"]["label"] == old["name"])
        shells.require(evidence, [check("prior-pfm-rendered-label", old["name"], rendered["tab"]["tab"]["label"])], prefix)
    original = terminal(subject)
    request(os.environ["HERDR_SOCKET_PATH"], "tab.rename", {"tab_id": original["identity"]["tab"], "label": "Reader hand label"})
    renamed = terminal(subject)
    shells.require(evidence, [check("actual-user-hand-set-label", "Reader hand label", renamed["tab"]["tab"]["label"])], prefix)
    picker_birth("user-tab-exec", subject, original, resources, owner, owner_file, prefix, evidence)


def run(name, chat_file, owner_file, resources, prefix):
    import native_shells as shells
    describe(name)
    owner, subject = read_json(owner_file), resources["subject"]
    checked_owner(owner)
    shells.scratch(subject)
    evidence = {"plan": name, "checks": [], "inputs": [], "before": bundle(owner_file)}
    try:
        (stale_shell if name == "stale-tmux-shell" else labelled_shell)(subject, owner, owner_file, resources, prefix, evidence)
    finally:
        evidence["after"] = bundle(owner_file)
        shells.require(evidence, bundle_checks(evidence["after"], carry(), evidence["before"], changed=evidence.get("changed", ())), prefix)
    return evidence


if __name__ == "__main__":
    if len(sys.argv) != 5 or sys.argv[1] != "client-prelude":
        raise ValueError("usage: native_shell_births.py client-prelude SOCKET_NAME SESSION OUT_JSON")
    client_prelude(*sys.argv[2:])
