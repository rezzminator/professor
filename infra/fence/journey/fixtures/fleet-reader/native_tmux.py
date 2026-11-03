#!/usr/bin/env python3
"""Public reader of a real, owned tmux server with no sessions."""
from contextlib import ExitStack
import json
import os
from pathlib import Path
import stat

from forecast import HERE, action, bounded_check, check, checked_owner
from bootstrap import tmux_server
from chat_carry import carry
import faults
from observation import bundle
from projection import roster_receipt, text_fleet_checks
from runtime import command, now_ms, read_json, write_json

SOCKET_NAME = "cc-reader-empty"
SESSION = "reader-empty"


def resources():
    target = tmux_server(SOCKET_NAME, SESSION)
    return {"tmux_socket": target["socket"], "session": SESSION}


def theme_resources(settings):
    target = tmux_server("cc-reader-unset", "reader-unset")
    return {"tmux_socket": target["socket"], "account_settings": settings}


def option(socket, name):
    faults.owned_path(socket)
    actual = {}
    command(["tmux", "-S", socket, "show", "-gv", name], allowed_codes=(0, 1), capture=actual)
    return {key: actual[key] for key in ("status", "stdout", "stderr")}


def unset_checks(phases):
    unset = {"status": 1, "stdout": "", "stderr": "invalid option: @host_accent\n"}
    return [check("native-host-accent-unset", unset, phases["before"]),
            check("native-host-accent-prior-absent", {"status": 1, "stdout": "", "stderr": "invalid option: @pfm-theme-prior-@host_accent\n"}, phases["prior_before"]),
            check("native-host-accent-captured-unset", {"status": 0, "stdout": "u:\n", "stderr": ""}, phases["prior"]),
            check("native-host-accent-restored-unset", unset, phases["after"])]


def global_options():
    directory = faults.owned_path(os.environ["PFM_TMUX_DIR"])
    result = {}
    for path in sorted(directory.iterdir()):
        if stat.S_ISSOCK(path.lstat().st_mode):
            socket = str(faults.owned_path(path))
            result[socket] = command(["tmux", "-S", socket, "show-options", "-g"])
    return result


def restoration_checks(previous, enabled, disabled, before_options, after_options, required_servers):
    bumps = read_json(HERE / "fixtures/fleet-reader/theme-generation-bumps.json")
    return [check("theme-on-generation", previous["generation"] + bumps["on"], enabled["generation"]),
            check("theme-off-generation", previous["generation"] + bumps["on_off"], disabled["generation"]),
            check("native-theme-server-presence", required_servers, sorted(set(before_options) & set(required_servers))),
            check("all-native-global-options-restored", before_options, after_options)]


def run_unset(owner_file, inputs, prefix):
    owner = read_json(owner_file)
    checked_owner(owner)
    socket = inputs["tmux_socket"]
    if Path(socket).name != "cc-reader-unset":
        raise ValueError("NATIVE-UNSET-RESOURCE: exact fresh prelude server required")
    before_birth = faults.tmux_birth(socket)
    before_options = global_options()
    required = sorted({socket, *(chat["terminal"]["socket"] for chat in carry()["chats"] if chat["terminal"]["backend"] == "tmux")})
    phases = {"before": option(socket, "@host_accent"), "prior_before": option(socket, "@pfm-theme-prior-@host_accent")}
    result = {"receipts": [], "checks": [], "phases": phases, "birth": before_birth}
    with ExitStack() as scopes:
        for path in inputs["account_settings"]:
            scopes.enter_context(faults.preserved_file(path, retain_inode=True))
        def invoke(label, arguments):
            receipt = action(owner, arguments, str(prefix) + "." + label)
            result["receipts"].append({"prefix": str(prefix) + "." + label, "argv": arguments})
            result["checks"].append(check(label + "-exit", 0, receipt["exit_code"]))
            return receipt
        previous = invoke("theme-before", ["pfm", "theme", "status", "--json"])
        previous_state = json.loads(previous["output"])
        if previous_state["enabled"] is not False:
            raise ValueError(f"NATIVE-UNSET-THEME: expected enabled=false, actual={previous_state!r}; artifact={prefix}.theme-before.out")
        try:
            enabled = invoke("theme-on", ["pfm", "theme", "on", "--json"])
            enabled_state = json.loads(enabled["output"])
            result["checks"].append(check("theme-on-enabled", True, enabled_state["enabled"]))
            phases["prior"] = option(socket, "@pfm-theme-prior-@host_accent")
            result["during"] = bundle(owner_file)
        finally:
            disabled = invoke("theme-off", ["pfm", "theme", "off", "--json"])
            disabled_state = json.loads(disabled["output"])
            result["checks"].append(check("theme-off-disabled", False, disabled_state["enabled"]))
        phases["after"] = option(socket, "@host_accent")
    result["checks"] += unset_checks(phases)
    after_options = global_options()
    result["options"] = {"before": before_options, "after": after_options}
    result["checks"] += restoration_checks(previous_state, enabled_state, disabled_state, before_options, after_options, required)
    result["checks"].append(check("unset-server-same-birth", before_birth, faults.tmux_birth(socket)))
    return result


def receipt_checks(output, carried, socket):
    warnings, table = roster_receipt(output)
    golden = (HERE / "goldens/fleet-reader/empty-server-warning.txt").read_text().rstrip("\n")
    wanted = golden.replace("@SOCKET@", Path(socket).name)
    return [check("empty-server-explicit-warning", [wanted], warnings), *text_fleet_checks(table, carried)]


def run(owner_file, inputs, prefix):
    owner = read_json(owner_file)
    checked_owner(owner)
    socket, session = inputs["tmux_socket"], inputs["session"]
    if Path(socket).name != SOCKET_NAME or session != SESSION:
        raise ValueError("NATIVE-EMPTY-RESOURCE: exact prelude auxiliary server required")
    def restore():
        return tmux_server(SOCKET_NAME, SESSION)
    with faults.empty_tmux(socket, session, restore) as actual:
        started = now_ms()
        receipt = {**action(owner, ["pfm", "ls", "--all", "--tsv"], str(prefix) + ".command"),
                   "prefix": str(prefix) + ".command", "door": "typed"}
        elapsed = now_ms() - started
        checks = [check("native-empty-exit-option", "off", actual["exit_empty"]),
                  check("native-empty-sessions", "", actual["list_sessions"]),
                  check("native-empty-list-exit", 0, receipt["exit_code"]), bounded_check(elapsed, 10000)]
        checks += receipt_checks(receipt["output"], carry(), socket)
        during = bundle(owner_file)
        write_json(str(prefix) + ".empty-during.json", during)
    checks.append(check("empty-server-restored-birth", actual["birth"], faults.tmux_birth(socket)))
    return {"receipt": receipt, "checks": checks, "fault": actual, "during": during}
