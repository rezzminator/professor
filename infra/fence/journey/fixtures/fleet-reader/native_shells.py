#!/usr/bin/env python3
"""Real shell/client transitions through installed zsh hooks and owner input."""
import os
import re
from contextlib import contextmanager

from forecast import action, check, checked_owner
from bootstrap import wait
from chat import shell_ready
from chat_carry import carry
import faults
from herdr import request
from observation import bundle, process_snapshot, process_state, terminal
from projection import bundle_checks, roster_receipt, text_fleet_checks
from runtime import command, read_json
from terminal import keys, text, type_command
import native_shell_births

PLANS = {
    "herdr_pane=replaced_by_new_client,pane_state=moved,client_registry=stale_tmux_client": ("moved-replaced-client", "shell-client"),
    "shell_exit=nested_zsh_ctrl_d,herdr=drops_agent_report_on_bare_shell,shell_state=bare_shell_foreground": ("nested-shell-release", "shell-transition"),
    "pane_owner=parent_shell_done,herdr_title=unsequenced_metadata,shell_nesting=nested_zsh_exit": ("nested-parent-title", "shell-transition"),
}
PLANS.update(native_shell_births.PLANS)
ARGV = ["pfm", "ls", "--all", "--tsv"]


def describe(name):
    if name in {value[0] for value in native_shell_births.PLANS.values()}:
        return native_shell_births.describe(name)
    if name not in {value[0] for value in PLANS.values()}:
        raise ValueError(f"NATIVE-SHELL-PLAN: {name}")
    resources = ["subject"]
    entry = "shell-transition"
    if name == "moved-replaced-client":
        entry = "shell-client"
        resources += ["id", "old_chat", "replacement_chat", "placeholder", "destination_workspace"]
    return {"plan": name, "entry": entry, "engine": "claude", "resources": resources, "argv": ARGV}


def scratch(subject):
    declared = carry()
    protected = [declared["foreign"]["terminal"], declared["owner"],
                 *[chat["terminal"] for chat in declared["chats"]]]
    identities = {(row.get("backend"), row.get("pane")) for row in protected}
    if subject.get("backend") != "herdr" or (subject.get("backend"), subject.get("pane")) in identities:
        raise ValueError("NATIVE-SHELL-SUBJECT: independent owned Herdr shell required")
    observed = checked_owner(subject)
    if not shell_ready(subject):
        raise ValueError("NATIVE-SHELL-PROMPT: actual bare shell prompt required")
    return observed


def title_fields(display):
    # The shell reporter's own command/exit/duration title is a product-specific format.
    found = re.fullmatch(r"(.*) exit (-?\d+) · (\d+)s", display["pane"].get("title") or "")
    return {"command": found[1], "exit": int(found[2]), "duration": int(found[3])} if found else None


def title_checks(label, display, command):
    parsed = title_fields(display)
    return [check(label + "-title", {"command": command, "exit": 0},
                  {key: parsed[key] for key in ("command", "exit")} if parsed else display["pane"].get("title")),
            check(label + "-duration", "native-nonnegative-seconds",
                  "native-nonnegative-seconds" if parsed and parsed["duration"] >= 0 else parsed)]


def shell_birth(display, parent=None):
    front = display["processes"]["foreground_processes"]
    snapshots = process_snapshot()
    matches = [snapshots[row["pid"]] for row in front if row["pid"] in snapshots
               and row["name"] == "zsh"
               and (parent is None or snapshots[row["pid"]]["parent"] == parent["pid"])]
    if len(matches) != 1:
        return False
    return {key: matches[0][key] for key in ("pid", "start")}


def nested_checks(parent, nested, returned, processes):
    alive = processes.get(nested["pid"])
    return [check("nested-zsh-new-birth", "distinct", "same" if nested == parent else "distinct"),
            check("nested-zsh-exited", "gone", "alive" if alive and alive["start"] == nested["start"] else "gone"),
            check("parent-zsh-foreground-restored", parent, returned)]


def literal_input(subject, value, evidence):
    text(subject, value)
    keys(subject, ["enter"])
    evidence["inputs"].append({"target": subject, "text": value, "keys": ["enter"]})


def displayed(subject, label, predicate):
    def observed():
        actual = terminal(subject)
        return actual if predicate(actual) else False
    return wait(label, observed, 15)


def clients(target, session):
    return command(["tmux", "-S", target["socket"], "list-clients", "-t", session, "-F", "#{client_pid}"]).splitlines()


def client_session(target):
    return command(["tmux", "-S", target["socket"], "display-message", "-p", "-t", target["pane"], "#{session_name}"]).strip()


@contextmanager
def attached_client(subject, target, session, evidence):
    previous = set(clients(target, session))
    type_command(subject, ["tmux", "-S", target["socket"], "attach-session", "-t", session])
    def newly_attached():
        new = sorted(set(clients(target, session)) - previous)
        if len(new) > 1:
            raise ValueError(f"NATIVE-SHELL-CLIENT: one new native client required, actual={new!r}")
        return new
    attached = wait("one real owner-attached tmux client", newly_attached, 15)
    birth = process_snapshot()[int(attached[0])]
    evidence.setdefault("clients", []).append(birth)
    try:
        yield birth
    finally:
        actual = process_snapshot().get(birth["pid"])
        if actual and actual["start"] == birth["start"]:
            if actual["state"] == "T":
                literal_input(subject, "fg", evidence)
                wait("suspended native client resumed", lambda: process_state(birth["pid"])["state"] != "T", 15)
            keys(subject, ["ctrl+b", "d"])
            wait("native client exited after detach",
                 lambda: process_snapshot().get(birth["pid"], {}).get("start") != birth["start"], 15)
        wait("owner shell returned after tmux detach", lambda: shell_ready(subject), 15)


def reader(owner, owner_file, prefix, evidence):
    receipt = action(owner, ARGV, str(prefix) + ".command")
    evidence["receipt"] = {**receipt, "prefix": str(prefix) + ".command", "door": "typed"}
    diagnostics, rows = roster_receipt(receipt["output"])
    require(evidence, [check("shell-reader-public-argv", ARGV, receipt["argv"]),
            check("shell-reader-exit", 0, receipt["exit_code"]),
            check("shell-reader-diagnostics", [], diagnostics), *text_fleet_checks(rows, carry())], prefix)
    evidence["during"] = bundle(owner_file)
    require(evidence, bundle_checks(evidence["during"], carry(), evidence["before"], changed=evidence.get("changed", ())), prefix)


def replacement_checks(chat, observed, terminal_id):
    pane = observed["pane"]
    return [check("replacement-client-native-session", {"source": "pfm", "agent": "claude", "kind": "id", "value": chat["session_id"]}, pane.get("agent_session")),
            check("replacement-client-title", chat["name"], pane.get("title")),
            check("replacement-client-physical-terminal", terminal_id, pane["terminal_id"]),
            check("replacement-client-state", "idle", observed["agent_status"]["pane"])]


def renamed_checks(chat, status, name, label):
    return [check(label + "-native-session", chat["session_id"], status.get("session_id")),
            check(label + "-native-name", name, status.get("name"))]


def replaced_client(subject, owner, owner_file, resources, prefix, evidence):
    from native_forecasts import PaneMove, engine_action, owned_chat, state
    old, replacement = read_json(resources["old_chat"]), read_json(resources["replacement_chat"])
    for chat in (old, replacement):
        owned_chat(chat)
        if chat["terminal"]["backend"] != "tmux" or chat["engine"] != "claude":
            raise ValueError("NATIVE-SHELL-REPLACEMENT: carried Claude tmux actors required")
    if old["session_id"] == replacement["session_id"]:
        raise ValueError("NATIVE-SHELL-REPLACEMENT: two distinct native sessions required")
    evidence["changed"] = ["key:" + chat["key"] for chat in (old, replacement)]
    mover = PaneMove(None, resources, subject=subject)
    with attached_client(subject, old["terminal"], client_session(old["terminal"]), evidence) as old_client:
        keys(subject, ["ctrl+z"])
        wait("old client suspended by actual owner key", lambda: process_state(old_client["pid"])["state"] == "T", 15)
        wait("parent prompt fronts parked old client", lambda: shell_ready(subject), 15)
        with attached_client(subject, replacement["terminal"], client_session(replacement["terminal"]), evidence):
            try:
                with faults.moved_pane(subject, mover.destination, mover.move) as moved:
                    subject.update(moved)
                    evidence["replacement_action"] = engine_action(resources["replacement_chat"], ["pfm", "whoami", "--json"], str(prefix) + ".replacement-ready", resources["id"])
                    displayed(subject, "actual replacement session owns moved physical terminal",
                              lambda actual: actual["pane"].get("agent_session", {}).get("value") == replacement["session_id"])
                    require(evidence, replacement_checks(replacement, terminal(subject), mover.terminal_id), prefix)
                    renamed = False
                    try:
                        for label, name in (("stale-name", "Reader stale client"), ("restore-name", old["name"])):
                            renamed = True
                            receipt = action(owner, ["pfm", "chat", "name", old["session_id"], name], str(prefix) + "." + label)
                            evidence.setdefault("receipts", []).append({**receipt, "prefix": str(prefix) + "." + label, "door": "typed"})
                            require(evidence, [check(label + "-exit", 0, receipt["exit_code"])] + renamed_checks(old, state(old), name, label)
                                    + replacement_checks(replacement, terminal(subject), mover.terminal_id), prefix)
                            if label == "restore-name":
                                renamed = False
                    finally:
                        if renamed:
                            action(owner, ["pfm", "chat", "name", old["session_id"], old["name"]], str(prefix) + ".cleanup-name")
                    evidence["subject_during"] = terminal(subject)
                    reader(owner, owner_file, prefix, evidence)
            finally:
                subject.update({"backend": "herdr", "pane": mover.current()["pane_id"]})


def require(evidence, checks, prefix):
    from native_forecasts import require as verify
    verify(prefix, evidence, checks)


def nested_transition(subject, owner, owner_file, prefix, evidence):
    before = scratch(subject)
    parent = wait("native parent zsh foreground birth", lambda: shell_birth(terminal(subject)), 15)
    evidence["subject_before"], evidence["parent"] = before, parent
    type_command(subject, ["export", "PFM_HERDR_LONG_COMMAND_SECS=0"])
    wait("parent threshold input completed", lambda: shell_ready(subject), 15)
    literal_input(subject, "zsh -i", evidence)
    nested = wait("native nested zsh has actual parent", lambda: shell_birth(terminal(subject), parent), 15)
    evidence["nested"] = nested
    try:
        literal_input(subject, ":", evidence)
        done = displayed(subject, "native nested command has its own done title",
                         lambda actual: (title_fields(actual) or {}).get("command") == ":")
        require(evidence, title_checks("nested-command", done, ":"), prefix)
        bare = displayed(subject, "Herdr drops bare-shell agent report while retaining its title",
                         lambda actual: actual["agent_status"]["pane"] == "unknown")
        require(evidence, title_checks("bare-shell", bare, ":"), prefix)
        evidence["bare_shell"] = bare
        keys(subject, ["ctrl+d"])
        evidence["inputs"].append({"target": subject, "keys": ["ctrl+d"]})
        returned = wait("Ctrl-D restores the original parent zsh birth", lambda: shell_birth(terminal(subject)) == parent, 15)
        require(evidence, nested_checks(parent, nested, parent if returned else None, process_snapshot()), prefix)
        finished = displayed(subject, "parent's later done survives nested exit release",
                             lambda actual: (title_fields(actual) or {}).get("command") == "zsh -i")
        require(evidence, title_checks("parent-done", finished, "zsh -i"), prefix)
        evidence["subject_during"] = finished
        reader(owner, owner_file, prefix, evidence)
        require(evidence, title_checks("parent-title-through-reader", terminal(subject), "zsh -i"), prefix)
        literal_input(subject, "export PFM_HERDR_SHELL_REPORT=0", evidence)
        cleared = displayed(subject, "parent next command releases its done title",
                            lambda actual: actual["pane"].get("title") is None)
        evidence["subject_released"] = cleared
        require(evidence, [check("next-command-clears-title", None, cleared["pane"].get("title"))], prefix)
    finally:
        if process_snapshot().get(nested["pid"], {}).get("start") == nested["start"]:
            keys(subject, ["ctrl+c", "ctrl+d"])
            wait("owned nested shell cleanup", lambda: shell_birth(terminal(subject)) == parent, 15)


def run(name, chat_file, owner_file, resources, prefix):
    describe(name)
    faults.owned_path(os.path.dirname(str(prefix)))
    owner = read_json(owner_file)
    checked_owner(owner)
    subject = resources["subject"]
    with faults.fresh_target(subject):
        if name in {value[0] for value in native_shell_births.PLANS.values()}:
            return native_shell_births.run(name, chat_file, owner_file, resources, prefix)
        evidence = {"plan": name, "checks": [], "inputs": [], "before": bundle(owner_file)}
        scratch(subject)
        try:
            if name == "moved-replaced-client":
                replaced_client(subject, owner, owner_file, resources, prefix, evidence)
            else:
                nested_transition(subject, owner, owner_file, prefix, evidence)
        finally:
            request(os.environ["HERDR_SOCKET_PATH"], "pane.close", {"pane_id": subject["pane"]})
            evidence["after"] = bundle(owner_file)
            require(evidence, bundle_checks(evidence["after"], carry(), evidence["before"], changed=evidence.get("changed", ())), prefix)
        return evidence
