#!/usr/bin/env python3
"""Read a genuine moved native seat after its persisted supervisor exits."""
import json

from forecast import action, bounded_check, check
from bootstrap import wait
from chat import refresh, reopen
from chat_carry import carry, temporary_hostless
import faults
from observation import bundle, report, terminal
from projection import (bundle_checks, carried_rows, engine_birth, roster_receipt,
                        selected, supervisor_birth, text_fleet_checks, text_rows)
from runtime import now_ms, read_json, write_json


def peers(chat):
    declared = carry()
    return {**declared, "chats": [row for row in declared["chats"] if row["key"] != chat["key"]]}


def fault_checks(chat, current, receipt, target):
    proof = current["lifecycle"]["hostless"]
    seats = [row for row in current["stored"]["seat"] if row["seat_id"] == chat["seat_id"]]
    diagnostics, table = roster_receipt(receipt["output"])
    rows = carried_rows(text_rows(table), chat)
    return [check("hostless-list-exit", 0, receipt["exit_code"]),
            check("hostless-list-diagnostics", [], diagnostics),
            check("hostless-list-bound", True, bounded_check(receipt["elapsed_ms"], 10000)["actual"] == "bounded"),
            check("hostless-native-supervisor", "dead", proof["supervisor"]["state"]),
            check("hostless-original-pane", "pane_not_found", proof["stored_pane"]),
            check("hostless-public-state", "dead", current["status"]["state"]),
            check("hostless-public-session", chat["session_id"], current["status"].get("session_id")),
            check("hostless-persisted-state", ["lost"], [row["state"] for row in seats]),
            check("hostless-persisted-old-pane", [chat["terminal"]["pane"]], [row["herdr_pane"] for row in seats]),
            check("hostless-actual-moved-pane", target["pane"], current["terminal"]["identity"]["pane"]),
            check("hostless-public-lost-row", [{"name": chat["name"], "account": str(chat["account"]), "kind": "resume-claude", "cwd": ""}],
                  [{key: row[key] for key in ("name", "account", "kind", "cwd")} for row in rows]),
            *text_fleet_checks(table, peers(chat))]


def live_again(chat):
    status = json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3)))
    if status.get("session_id") != chat["session_id"] or status.get("state") != "idle":
        return False
    return refresh(chat["branch"], chat.get("role", chat["origin"])) or False


def restored_checks(chat, before, restored, after, mover, prior_host):
    current, prior = selected(after, restored), selected(before, chat)
    host = supervisor_birth(after, restored)
    engine_birth(after, restored)
    return [check("restored-seat-session-fields", {key: chat[key] for key in ("key", "session_id", "engine", "name", "account", "project")},
                  {key: restored[key] for key in ("key", "session_id", "engine", "name", "account", "project")}),
            check("restored-seat-supervisor-birth", "fresh", "reused" if host == prior_host else "fresh"),
            check("restored-seat-public-state", "idle", current["status"]["state"]),
            check("restored-seat-tab", mover.before["identity"]["tab"], current["terminal"]["identity"]["tab"]),
            check("restored-seat-workspace", mover.before["identity"]["workspace"], current["terminal"]["identity"]["workspace"]),
            check("restored-seat-physical-terminal", mover.terminal_id, current["terminal"]["pane"]["terminal_id"]),
            check("restored-seat-recipe", prior["stored"]["recipe"], current["stored"]["recipe"]),
            *bundle_checks(after, peers(chat), before)]


def run(chat_file, owner_file, resources, prefix, evidence, mover_factory, refreshed, require):
    chat, owner = read_json(chat_file), read_json(owner_file)
    restore_owner = resources["restore_owner"]
    faults.owned_target(restore_owner)
    mover = mover_factory(chat, resources)
    host = supervisor_birth(evidence["before"], chat)
    receipt_path = str(prefix) + ".hostless.json"
    original_workspace = mover.before["identity"]["workspace"]
    killed = False

    def restore():
        nonlocal killed
        killed = faults.birth_exited(host)
        if killed:
            reopen(restore_owner, chat["branch"], chat.get("role", chat["origin"]))
            restored = wait("public reopen restores moved native seat", lambda: live_again(chat), 30)
            mover.rebind(restored)
        target = mover.move(None, original_workspace)
        restored = refreshed(chat, target, original_workspace)
        write_json(chat_file, restored)
        write_json(str(chat_file) + ".target", restored["terminal"])
        evidence["restored_terminal"] = terminal(restored["terminal"])

    try:
        with faults.stopped_for_move(chat["terminal"], host,
                lambda target: mover.move(target, mover.destination), restore, receipt=receipt_path) as moved:
            with temporary_hostless(chat["branch"], chat.get("role", chat["origin"]), evidence["before"], receipt_path):
                started = now_ms()
                receipt = {**action(owner, ["pfm", "ls", "--all", "--tsv"], str(prefix) + ".command"),
                           "prefix": str(prefix) + ".command", "door": "typed"}
                receipt["elapsed_ms"] = now_ms() - started
                evidence["receipt"] = receipt
                evidence["during"] = bundle(owner_file)
                current = selected(evidence["during"], chat)
                require(prefix, evidence, fault_checks(chat, current, receipt, moved)
                        + bundle_checks(evidence["during"], peers(chat), evidence["before"]))
    finally:
        evidence["after"] = bundle(owner_file)
        if killed:
            require(prefix, evidence, restored_checks(chat, evidence["before"], read_json(chat_file),
                                                     evidence["after"], mover, host))
