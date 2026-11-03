#!/usr/bin/env python3
"""Native reader conditions composed only from the journey's public doors.

run FACTORS CHAT OWNER RESOURCES PREFIX -- PUBLIC-ARGV...
Resources: id for engine entry; destination_workspace/placeholder for pane moves;
tmux_socket for stopped-server entry. Every id is an observed run-owned resource.
"""
from contextlib import contextmanager
import csv
import json
import os
from pathlib import Path
import shlex
import sys

from forecast import HERE, action, bounded_check, check, factors, checked_owner, scope as forecast_scope
from bootstrap import wait
from chat import refresh
from chat_carry import carry
import faults
from herdr import request
from observation import bundle, indicator_daemon, report, terminal, terminal_births
from observation_process import engine_births
from projection import identity_checks, selected, supervisor_birth, text_fleet_checks, text_rows
from runtime import command, now_ms, read_json, write_json
from native_denied import run as denied_run, wrapper as denial_wrapper
import native_actor
import native_tmux
import native_macos
import native_seats
import native_shells
import native_launch
import native_presence

PLANS = {
    "herdr_indicators=on,engine=opencode": ("indicators-opencode", "list"),
    "multiplexer=herdr,pane_state=unknown": ("unknown", "list"),
    "herdr_indicators=on,pane_state=idle": ("indicators-idle", "list"),
    "seat_event=pane_moved,door=whoami": ("moved", "identity"),
    "env_pane=birth_id_inherited,herdr_event=pane_moved_workspace,seat_supervisor=ancestor_in_chain": ("moved-inherited", "identity"),
    "process_table=kern_procargs2,caller=resolve_lookup,os=macos": ("darwin-lookup", "identity"),
    "process_state=stopped,tmux_server=job_control_stopped,caller_context=outside_pane": ("outside-stopped", "outside"),
    "seat_lookup=not_in_chat,process_environ_read=permission_denied,engine=codex": ("outside-denied-codex", "denied"),
    "engine=claude_code,account_source=no_launch_record_no_live_seat,transcript_state=absent_before_first_prompt": ("outside-before-prompt", "list"),
    "hook=claude_usage_hook,invoker=claude_code,config_state=invalid": ("outside-usage-hook", "native-list"),
    "exit_empty=off,socket_holder=live_process,tmux_server=running_no_session": ("empty-server", "list"),
    "pane_wrapper=herdr_keep,engine=claude_code,herdr_detection=attach_client_foreground": ("shield-foreground", "list"),
    "herdr_pane=herdr_keep_protected,herdr_keep_listing=per_socket_query,foreground_process=herdr_keep_attach_client": ("shield-socket", "list"),
    "os=linux,login_env_source=environment_d,claude_origin=outside_pfm": ("outside-login-default", "list"),
    "supervisor_pid=dead_or_reused,herdr_pane=recycled_or_moved,seat_row=stale": ("stale-seat-moved", "list"),
    "supervisor_state=absent,scan_mode=writable,herdr_pane=pane_not_found": ("stale-seat-missing-pane", "list"),
    "backend=tmux,theme_prior=failed_read_as_unset,tmux_answer=invalid_option_unset": ("unset-theme", "tmux-theme"),
    "caller_identity=bare_seat_id,seat_name=unnamed,lookup=chat_mcp_transcript_id": ("unnamed-transcript", "unnamed-mcp"),
}
PLANS.update(native_macos.PLANS)
PLANS.update(native_shells.PLANS)
PLANS.update(native_launch.PLANS)
PLANS.update(native_presence.PLANS)


class UnsupportedPlan(ValueError):
    pass


def plan(value):
    actual = factors(value)
    matches = [item for key, item in PLANS.items() if factors(key) == actual]
    if len(matches) != 1:
        raise UnsupportedPlan("NATIVE-PLAN-UNDEFINED: " + json.dumps(actual, sort_keys=True))
    return matches[0]


def describe(value):
    name, entry = plan(value)
    if entry in {"macos-theme", "macos-mcp"}:
        return native_macos.describe(name)
    if entry in {"shell-transition", "shell-client", "shell-birth"}:
        return native_shells.describe(name)
    if entry in {"launcher", "launch-reader"}:
        return native_launch.describe(name)
    if entry == "presence-reader":
        return native_presence.describe(name)
    resources = ["id"] if entry == "identity" or name == "unknown" else []
    if name.startswith("moved"):
        resources += ["placeholder", "destination_workspace"]
    if name == "outside-stopped":
        resources.append("tmux_socket")
    if entry == "denied":
        resources += ["id", "scenario", "records", "native_binary", "cwd", "codex_home"]
    if name in {"outside-before-prompt", "outside-usage-hook", "outside-login-default"}:
        resources += ["id", "subject", "scenario", "records", "native_binary", "cwd", "session_id"]
        resources += ["hook_settings", "broken_config", "hook_prefix"] if name == "outside-usage-hook" else ["config_dir", "ready_hold"]
        if name == "outside-login-default":
            resources += ["generated_environment", "environment_receipt", "environment_d"]
    if name == "empty-server":
        resources += ["tmux_socket", "session"]
    if name.startswith("stale-seat-"):
        resources += ["placeholder", "destination_workspace", "restore_owner"]
    if name == "unset-theme":
        return {"plan": name, "entry": entry, "resources": ["tmux_socket", "account_settings"],
                "engine": "claude", "argv": ["pfm", "theme", "on", "--json"]}
    return {"plan": name, "entry": entry, "resources": resources,
            "engine": "codex" if entry == "denied" else "opencode" if name == "indicators-opencode" else "claude",
            "argv": ["pfm", "ls", "--all", "--tsv"] if entry in {"list", "native-list"} else ["pfm", "whoami", "--json"]}


def owned_chat(chat):
    rows = [row for row in carry()["chats"] if row["key"] == chat["key"]]
    if len(rows) != 1 or any(rows[0][key] != chat[key] for key in ("session_id", "terminal", "engine")):
        raise ValueError("NATIVE-CHAT-IDENTITY: exact current carried actor required")
    return faults.owned_target(chat["terminal"])


def require(prefix, evidence, checks):
    evidence["checks"].extend(checks)
    write_json(str(prefix) + ".checks.json", evidence["checks"])
    write_json(str(prefix) + ".evidence.json", evidence)
    broken = [row for row in checks if row["expected"] != row["actual"]]
    if broken:
        raise ValueError(f"NATIVE-PRECONDITION: expected/actual={broken!r}; artifact={prefix}.evidence.json")


def recorded(evidence, chat_file, owner_file, prefix):
    if "after" not in evidence:
        evidence["after"] = bundle(owner_file)
    write_json(str(prefix) + ".checks.json", evidence["checks"])
    write_json(str(prefix) + ".evidence.json", evidence)
    write_json(str(prefix) + ".chat.json", read_json(chat_file))


def engine_action(chat_file, arguments, prefix, capability, registered=True):
    if not capability:
        raise ValueError("NATIVE-ENGINE-ENTRY: mapped capability id required")
    if arguments not in (["pfm", "whoami", "--json"], ["pfm", "ls", "--all", "--tsv"]):
        raise ValueError(f"NATIVE-ENGINE-ARGV: expected public identity entry, actual={arguments!r}")
    script = '''source "$1/lib.sh"
source "$1/fixtures/chat-mesh/entry.sh"
ID="$2"; chat="$3"; prefix="$4"; registered="$5"; shift 5
mesh_shell "$chat" "$prefix" "$@" || exit 2
poll native-engine-command 20 mesh_command_ready || exit 2
if [ "$registered" = true ]; then poll native-engine-command-idle 20 mesh_state "$chat" idle || exit 2; fi
printf '%s' "$MESH_INPUT_EXPECTED" >"$prefix.expected-input.txt"
mesh_input_finish "$prefix"
'''
    command(["bash", "-c", script, "native-engine-entry", str(HERE), capability,
             str(chat_file), str(prefix), "true" if registered else "false", *arguments], timeout=50)
    return {"argv": arguments, "exit_code": int(Path(str(prefix) + ".status").read_text()),
            "output": Path(str(prefix) + ".out").read_text(), "prefix": str(prefix), "door": "engine"}


def state(chat):
    return json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3)))


def refreshed(chat, target, workspace):
    def durable():
        current = refresh(chat["branch"], chat.get("role", chat["origin"]))
        if (current is not None and current["session_id"] == chat["session_id"]
                and current["terminal"] == target and current.get("workspace") == workspace):
            return current
        return False
    return wait("durable moved pane matches actual terminal and workspace", durable, 20)


def restore_agent(chat_file, prefix, capability):
    receipt = engine_action(chat_file, ["pfm", "whoami", "--json"], str(prefix) + ".restore-agent", capability)
    chat = read_json(chat_file)
    if receipt["exit_code"] != 0 or any(row["expected"] != row["actual"] for row in identity_checks(json.loads(receipt["output"]), chat)):
        raise ValueError(f"NATIVE-AGENT-RESTORE-IDENTITY: {prefix}.restore-agent.out")
    wait("actual native agent restored to idle", lambda: terminal(chat["terminal"])["agent_status"]["pane"] == "idle", 15)


def shield_checks(chat, observed, aliases_equal):
    shield = observed["shield"]
    front = [shlex.split(row["chain"][0]["argv"]) for row in observed["births"]]
    attaching = [argv for argv in front if argv and Path(argv[0]).name in {"herdr-keep", "herdr-shield"}
                 and (len(argv) == 1 or argv[1:2] == ["attach"])]
    return [check("native-wrapper-alias", True, aliases_equal),
            check("native-shield-live", True, isinstance(shield, dict) and shield["holder"]["holder_live"]),
            check("native-foreground-attach-client", "present", "present" if attaching else front),
            check("native-shield-bound-pane", chat["terminal"]["pane"], shield["holder"]["binding"]["pane"] if isinstance(shield, dict) else shield)]


class PaneMove:
    def __init__(self, chat, resources, *, subject=None):
        if (chat is None) == (subject is None):
            raise ValueError("NATIVE-MOVE-SUBJECT: one carried chat or explicit owned terminal required")
        observed = owned_chat(chat) if subject is None else faults.owned_target(subject)
        self.chat, self.before = chat, observed
        self.placeholder = resources["placeholder"]
        self.destination = resources["destination_workspace"]
        workspaces = {value["id"] for value in carry()["workspaces"].values()}
        if self.destination not in workspaces or self.destination == observed["identity"]["workspace"]:
            raise ValueError("NATIVE-MOVE-WORKSPACE: different owned destination required")
        result = request(os.environ["HERDR_SOCKET_PATH"], "pane.get", {"pane_id": self.placeholder})["result"]["pane"]
        if self.placeholder == observed["identity"]["pane"] or result["tab_id"] != observed["identity"]["tab"]:
            raise ValueError("NATIVE-MOVE-PLACEHOLDER: independent actual pane in source tab required")
        self.terminal_id = observed["pane"]["terminal_id"]

    def rebind(self, chat):
        if self.chat is None or any(chat[key] != self.chat[key] for key in ("key", "session_id", "engine")):
            raise ValueError("NATIVE-MOVE-REBIND: same carried session and engine required")
        observed = owned_chat(chat)
        self.chat, self.terminal_id = chat, observed["pane"]["terminal_id"]

    def current(self):
        rows = request(os.environ["HERDR_SOCKET_PATH"], "pane.list", {})["result"]["panes"]
        matches = [row for row in rows if row.get("terminal_id") == self.terminal_id]
        if len(matches) != 1:
            raise ValueError(f"NATIVE-MOVE-TERMINAL: one actual terminal required, actual={matches!r}")
        return matches[0]

    def move(self, unused_target, workspace):
        current = self.current()
        original = self.before["identity"]
        if workspace == original["workspace"]:
            destination = {"type": "tab", "tab_id": original["tab"], "target_pane_id": self.placeholder, "split": "right"}
            already = current["tab_id"] == original["tab"]
        else:
            destination = {"type": "new_tab", "workspace_id": workspace}
            already = current["workspace_id"] == workspace
        if not already:
            request(os.environ["HERDR_SOCKET_PATH"], "pane.move",
                    {"pane_id": current["pane_id"], "destination": destination, "focus": False})
            current = self.current()
        return {"backend": "herdr", "pane": current["pane_id"]}


@contextmanager
def condition(name, values, chat_file, owner, resources, prefix, evidence):
    chat = read_json(chat_file)
    observed = owned_chat(chat)
    if name.startswith("indicators-"):
        daemon = indicator_daemon(pane=chat["terminal"]["pane"])
        expected_engine = "opencode" if name == "indicators-opencode" else "claude"
        require(prefix, evidence, [check("native-herdr-backend", "herdr", observed["backend"]),
                check("native-engine", expected_engine, chat["engine"]),
                check("native-indicator-process", "running", "running" if isinstance(daemon, dict) else daemon)])
        evidence["indicator_daemon"] = daemon
        if name == "indicators-idle":
            require(prefix, evidence, [check("native-pane-idle", "idle", observed["agent_status"]["pane"]),
                                      check("public-chat-idle", "idle", state(chat)["state"])])
        yield chat
    elif name == "unknown":
        with faults.released_agent(chat, lambda: restore_agent(chat_file, prefix, resources["id"])):
            actual = terminal(chat["terminal"])
            require(prefix, evidence, [check("native-agent-unknown", "unknown", actual["agent_status"]["pane"])])
            yield chat
    elif name.startswith("moved"):
        mover = PaneMove(chat, resources)
        previous = selected(evidence["before"], chat)
        prior = engine_births(previous, chat["engine"])
        require(prefix, evidence, [check("one-native-moved-engine", 1, len(prior))])
        expected_births = [{"pid": row["pid"], "start": row["start"]} for row in prior]
        if name == "moved-inherited":
            host = supervisor_birth(evidence["before"], chat)
            chains = [row["chain"] for row in previous["process_births"] if row["pid"] == prior[0]["pid"]]
            require(prefix, evidence, [check("actual-inherited-birth-pane", chat["terminal"]["pane"], prior[0]["environment"].get("HERDR_PANE_ID")),
                    check("actual-supervisor-ancestor", [host["pid"]], sorted({node["pid"] for chain in chains for node in chain if node["pid"] == host["pid"]}))])
        try:
            with faults.moved_pane(chat["terminal"], mover.destination, mover.move) as target:
                changed = refreshed(chat, target, mover.destination)
                actual = terminal(target)
                require(prefix, evidence, [check("moved-session", chat["session_id"], changed["session_id"]),
                        check("moved-workspace", mover.destination, actual["identity"]["workspace"]),
                        check("moved-physical-terminal", mover.terminal_id, actual["pane"]["terminal_id"])])
                current = engine_births({"process_births": terminal_births(actual), "terminal": actual, "stored": previous["stored"]}, chat["engine"])
                require(prefix, evidence, [check("moved-engine-birth", expected_births,
                        [{"pid": row["pid"], "start": row["start"]} for row in current])])
                write_json(chat_file, changed)
                write_json(str(chat_file) + ".target", changed["terminal"])
                evidence["moved"] = actual
                yield changed
        finally:
            pane = mover.current()
            restored = refreshed(chat, {"backend": "herdr", "pane": pane["pane_id"]}, mover.before["identity"]["workspace"])
            write_json(chat_file, restored)
            write_json(str(chat_file) + ".target", restored["terminal"])
            actual = terminal(restored["terminal"])
            require(prefix, evidence, [check("restored-physical-terminal", mover.terminal_id, actual["pane"]["terminal_id"]),
                    check("restored-tab", mover.before["identity"]["tab"], actual["identity"]["tab"]),
                    check("restored-workspace", mover.before["identity"]["workspace"], actual["identity"]["workspace"])])
    elif name == "darwin-lookup":
        require(prefix, evidence, [check("native-kernel-ABI", "darwin", sys.platform)])
        yield chat
    elif name == "outside-stopped":
        with forecast_scope(values, chat, owner, resources) as actual:
            require(prefix, evidence, actual["checks"])
            evidence["fault"] = actual["resources"]
            yield chat
    elif name in {"outside-before-prompt", "outside-usage-hook", "outside-login-default"}:
        if name == "outside-usage-hook":
            with faults.temporary_text(resources["broken_config"], "{"):
                with native_actor.scope(name, resources, prefix, evidence, require) as actual:
                    yield actual
        else:
            with native_actor.scope(name, resources, prefix, evidence, require) as actual:
                yield actual
    elif name.startswith("shield-"):
        alias, current = Path(os.environ["HOME"]) / ".local/bin/herdr-keep", Path(os.environ["HOME"]) / ".local/bin/herdr-shield"
        require(prefix, evidence, shield_checks(chat, observed, alias.samefile(current)))
        evidence["shield"] = observed["shield"]
        yield chat
    else:
        raise ValueError(f"NATIVE-CONDITION-UNDEFINED: {name}")


def run(value, chat_file, owner_file, resources_file, prefix, arguments):
    name, entry = plan(value)
    values = factors(value)
    chat, owner, resources = read_json(chat_file), read_json(owner_file), read_json(resources_file)
    faults.owned_path(Path(prefix).parent)
    checked_owner(owner)
    write_json(str(chat_file) + ".target", chat["terminal"])
    expected = describe(value)["argv"]
    if arguments != expected:
        raise ValueError(f"NATIVE-PUBLIC-ENTRY: expected={expected!r}, actual={arguments!r}")
    evidence = {"plan": name, "factors": values, "checks": [], "before": bundle(owner_file)}
    if entry in {"macos-theme", "macos-mcp"}:
        evidence.update(native_macos.run(name, owner_file, resources, prefix))
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if entry in {"shell-transition", "shell-client", "shell-birth"}:
        evidence.update(native_shells.run(name, chat_file, owner_file, resources, prefix))
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if entry in {"launcher", "launch-reader"}:
        evidence.update(native_launch.run(name, chat_file, owner_file, resources, prefix, require, state))
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if entry == "presence-reader":
        evidence.update(native_presence.run(name, chat_file, owner_file, resources, prefix))
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if name == "empty-server":
        actual = native_tmux.run(owner_file, resources, prefix)
        evidence.update(actual)
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if name == "unset-theme":
        evidence.update(native_tmux.run_unset(owner_file, resources, prefix))
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if name.startswith("stale-seat-"):
        native_seats.run(chat_file, owner_file, resources, prefix, evidence, PaneMove, refreshed, require)
        recorded(evidence, chat_file, owner_file, prefix)
        return
    if entry == "denied":
        denied_run(owner_file, resources, prefix, arguments, evidence, engine_action, require)
        recorded(evidence, chat_file, owner_file, prefix)
        return
    with condition(name, values, chat_file, owner, resources, prefix, evidence) as active:
        started = now_ms()
        if entry in {"identity", "native-list"}:
            receipt = engine_action(active.get("metadata_file", chat_file), arguments, str(prefix) + ".command", resources["id"], registered=entry == "identity")
            if entry == "native-list":
                native_actor.composer(active["terminal"], "claude")
                require(prefix, evidence, native_actor.usage_checks(resources))
        else:
            receipt = {**action(owner, arguments, str(prefix) + ".command"), "prefix": str(prefix) + ".command", "door": "typed"}
        evidence["receipt"] = receipt
        receipt["elapsed_ms"] = now_ms() - started
        evidence["checks"].append(check("public-entry-exit", 1 if entry == "outside" else 0, receipt["exit_code"]))
        if entry == "identity" and receipt["exit_code"] == 0:
            evidence["checks"] += identity_checks(json.loads(receipt["output"]), active)
        elif entry in {"list", "native-list"} and receipt["exit_code"] == 0:
            evidence["checks"] += text_fleet_checks(receipt["output"], carry())
            if name == "outside-before-prompt":
                evidence["checks"].append(native_actor.before_prompt_row(text_rows(receipt["output"]), resources["session_id"]))
            elif name == "outside-login-default":
                evidence["checks"].append(native_actor.login_absence(text_rows(receipt["output"]), resources["session_id"]))
        elif entry == "outside":
            socket = resources["tmux_socket"]
            evidence["checks"] += [check("outside-pane-failed-read", f"look up the caller: read tmux panes on {socket}: tmux server did not answer within 2s: {socket}", receipt["output"].strip()),
                                   check("outside-pane-bound", "within-10s", "within-10s" if bounded_check(receipt["elapsed_ms"], 10000)["actual"] == "bounded" else receipt["elapsed_ms"])]
    evidence["after"] = bundle(owner_file)
    if evidence.get("fault", {}).get("tmux_birth"):
        evidence["checks"].append(check("restored-stopped-server-birth", evidence["fault"]["tmux_birth"], faults.tmux_birth(resources["tmux_socket"])))
    recorded(evidence, chat_file, owner_file, prefix)


if __name__ == "__main__":
    try:
        if len(sys.argv) == 3 and sys.argv[1] == "denial-wrapper":
            denial_wrapper(sys.argv[2])
            sys.exit(0)
        if len(sys.argv) == 3 and sys.argv[1] in {"supports", "plan"}:
            try:
                print(json.dumps(describe(sys.argv[2]), sort_keys=True))
            except UnsupportedPlan as error:
                print(str(error), file=sys.stderr)
                sys.exit(1)
            sys.exit(0)
        if len(sys.argv) < 9 or sys.argv[1] != "run" or sys.argv[7] != "--":
            raise ValueError("usage: native_forecasts.py run FACTORS CHAT OWNER RESOURCES PREFIX -- PUBLIC-ARGV...")
        run(*sys.argv[2:7], sys.argv[8:])
    except (OSError, ValueError, KeyError, RuntimeError, csv.Error) as error:
        print(f"NATIVE-FORECAST-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
