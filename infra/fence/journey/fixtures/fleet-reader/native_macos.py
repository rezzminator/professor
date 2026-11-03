#!/usr/bin/env python3
"""Darwin follower reads through the real owned launchd and file-fault doors."""
import json
from contextlib import ExitStack
import os
from pathlib import Path
import plistlib
import shlex

from forecast import action, check, checked_owner
from bootstrap import run_managed_unit
import faults
from observation import bundle, managed_unit
from observation_process import engine_births
from isolation import journey_os
from projection import roster_receipt, text_fleet_checks
from runtime import read_json, write_json, write_text

PLANS = {
    "account_discovery=settings_unlistable,os=macos,service_manager=launchd_watch_paths": ("macos-watch-unlistable", "macos-theme"),
    "launchd_watch=reduced_set,account_discovery=read_failed,os=macos": ("macos-watch-failure", "macos-theme"),
    "os=macos,service_manager=launchd_watch_paths,settings_layout=symlinked_settings_json": ("macos-watch-symlink", "macos-theme"),
    "service_manager=launchd,launchd_job=follower_own_job,os=macos": ("macos-own-job", "macos-theme"),
    "job_context=follower_inside_own_job,service_manager=launchd,os=macos": ("macos-own-job", "macos-theme"),
    "recipe_origin=mcp_new_chat,os=macos,launcher=launchd_daemon": ("macos-mcp-recipe", "macos-mcp"),
    "pane_env=herdr_pane_terminal,recipe_env=no_terminal_captured,launcher=launchd_daemon": ("macos-mcp-terminal", "macos-mcp"),
}

SYNC = ["pfm", "theme", "sync", "--json"]
ROSTER = ["pfm", "ls", "--all", "--tsv"]
TERMINAL_KEYS = ("HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "TMUX", "TMUX_PANE")
SELF_RELOAD = "themesync: launch agent plist rewritten; the agent cannot reload itself from inside launchd: run `pfm theme sync` in a shell"


def describe(name):
    if name not in {value[0] for value in PLANS.values()}:
        raise ValueError(f"MACOS-READER-PLAN: {name}")
    if name in {"macos-mcp-recipe", "macos-mcp-terminal"}:
        return {"plan": name, "entry": "macos-mcp", "engine": "claude", "resources": [], "argv": ROSTER}
    resources = ["settings", "settings_target", "account_settings", "expected_watches", "follower_program"]
    if name == "macos-watch-unlistable":
        resources.append("account_directory")
    return {"plan": name, "entry": "macos-theme", "engine": "claude", "resources": resources, "argv": SYNC}


def follower_path():
    label = "com.professor." + os.environ["PFM_UNIT_PREFIX"] + ".theme"
    return label, str(Path.home() / "Library/LaunchAgents" / (label + ".plist"))


def follower_checks(report, verdict, detail):
    _, path = follower_path()
    rows = [{key: row[key] for key in ("surface", "verdict", "path", "detail")}
            for row in report["surfaces"] if row["surface"] == "follower"]
    return [check("native-follower-report", [{"surface": "follower", "verdict": verdict, "path": path, "detail": detail}], rows)]


def watches_checks(actual, inputs, watches):
    label, path = follower_path()
    definition = actual["definition"]
    if not isinstance(definition, dict):
        raise ValueError(f"MACOS-READER-DEFINITION: expected actual installed follower: {actual!r}")
    return [check("native-follower-platform", "darwin", actual["platform"]),
            check("native-follower-label", label, actual["label"]),
            check("native-follower-loaded", "loaded", "absent" if actual["state"] == "absent" else "loaded"),
            check("native-follower-path", path, definition["path"]),
            check("native-follower-program", inputs["follower_program"], definition["ProgramArguments"]),
            check("native-follower-watch-paths", watches, definition["WatchPaths"]),
            check("native-follower-prefix", os.environ["PFM_UNIT_PREFIX"], definition["EnvironmentVariables"].get("PFM_UNIT_PREFIX"))]


def own_job_checks(job, receipt, after, original_definition):
    label, _ = follower_path()
    birth = receipt["birth"]
    chain = birth["chain"]
    words = shlex.split(chain[0]["argv"])
    parent = chain[1] if len(chain) > 1 else {}
    return [check("native-job-public-argv", SYNC, receipt["argv"]),
            check("native-job-public-kernel-argv", ["pfm", *SYNC[1:]], [Path(words[0]).name, *words[1:]] if words else []),
            check("native-job-public-parent", job["pid"], chain[0]["parent"]),
            check("native-job-parent-birth", {"pid": job["pid"], "start": job["start"], "parent": 1},
                  {key: parent.get(key) for key in ("pid", "start", "parent")}),
            check("native-job-xpc-label", label, birth["environment"].get("XPC_SERVICE_NAME")),
            check("native-job-public-status", 1, receipt["status"]),
            check("native-job-public-stderr", "", receipt["stderr"]),
            check("native-job-after-label", label, after["label"]),
            check("native-job-still-loaded", "loaded", "absent" if after["state"] == "absent" else "loaded"),
            check("native-job-rewritten-definition", original_definition, after["definition"])]


def discovery_failure(owner, inputs, prefix, evidence, execute, *, unlistable=False):
    settings = faults.owned_path(inputs["account_directory"] if unlistable else inputs["settings"], preserve=True)
    previous = managed_unit("theme")
    evidence["checks"] += watches_checks(previous, inputs, inputs["expected_watches"])
    cause = (f"themesync: list account settings: open {settings}: too many levels of symbolic links" if unlistable else
             f"themesync: resolve {inputs['settings']}: EvalSymlinks: too many links")
    fault = faults.unlistable_directory(settings) if unlistable else faults.file_symlink(settings, settings)
    with fault as loop:
        evidence["checks"].append(check("native-settings-loop-target", str(settings), str(loop.readlink())))
        for attempt in (1, 2):
            for verb in ("sync", "status"):
                receipt = execute(f"failure-{verb}-{attempt}", ["pfm", "theme", verb, "--json"], 1)
                report = json.loads(receipt["output"])
                source = {"surface": "claude", "verdict": "ERROR", "generation": 0, "path": "", "detail": cause}
                if verb == "sync":
                    evidence["checks"].append(check("failed-discovery-source-only", [source], report["surfaces"]))
                else:
                    evidence["checks"].append(check("failed-discovery-source", [source], [row for row in report["surfaces"] if row["surface"] == "claude"]))
                    evidence["checks"] += follower_checks(report, "ERROR", "themesync: discover follower watches: " + cause)
                observed = managed_unit("theme")
                evidence["checks"] += watches_checks(observed, inputs, inputs["expected_watches"])
                evidence["checks"].append(check("failed-discovery-retains-definition", previous["definition"], observed["definition"]))
                evidence["units"].append(observed)
    restored = execute("outside-sync", SYNC, 0)
    evidence["checks"] += follower_checks(json.loads(restored["output"]), "IN-SYNC", "")


def symlink_watches(owner, inputs, prefix, evidence, execute):
    settings = faults.owned_path(inputs["settings"], preserve=True)
    target = faults.owned_path(inputs["settings_target"])
    if target.parent != settings.parent or target == settings:
        raise ValueError("MACOS-READER-SETTINGS-TARGET: distinct same-directory input required")
    if target.read_bytes() != settings.read_bytes():
        raise ValueError("MACOS-READER-SETTINGS-BYTES: symlink target must retain the original settings input")
    wanted = [str(target) if path == str(settings) else path for path in inputs["expected_watches"]]
    with faults.file_symlink(settings, target) as linked:
        evidence["checks"].append(check("native-settings-canonical-target", str(target), str(linked.resolve(strict=True))))
        receipt = execute("symlink-sync", SYNC, 0)
        evidence["checks"] += follower_checks(json.loads(receipt["output"]), "IN-SYNC", "")
        observed = managed_unit("theme")
        evidence["checks"] += watches_checks(observed, inputs, wanted)
        evidence["units"].append(observed)
    restored = execute("outside-sync", SYNC, 0)
    evidence["checks"] += follower_checks(json.loads(restored["output"]), "IN-SYNC", "")
    evidence["checks"] += watches_checks(managed_unit("theme"), inputs, inputs["expected_watches"])


def own_job(owner, inputs, prefix, evidence, execute):
    original = managed_unit("theme")
    label, path = follower_path()
    evidence["checks"] += watches_checks(original, inputs, inputs["expected_watches"])
    def before(job):
        evidence["checks"] += [check("native-own-job-held-label", label, job["label"]),
                               check("native-own-job-held-state", "running", job["state"])]
        # The native factory owns this disk path and restores it after the callback/child.
        stale = plistlib.dumps({"Label": label, "ProgramArguments": ["/fixture/stale-follower"]}).decode()
        write_text(path, stale)
        evidence["stale_input"] = stale
    def after(job, receipt):
        observed = managed_unit("theme")
        evidence["checks"] += own_job_checks(job, receipt, observed, original["definition"])
        evidence["checks"] += follower_checks(json.loads(receipt["stdout"]), "ERROR", SELF_RELOAD)
        evidence["units"].append(observed)
        evidence["job_receipt"] = receipt
    with faults.unit_state(label, False):
        evidence["native_job"] = run_managed_unit("theme", SYNC, str(prefix) + ".theme-job", before=before, after=after)
    if "job_receipt" not in evidence:
        raise ValueError("MACOS-READER-JOB-OBSERVATION: native after callback did not run")
    receipt = execute("outside-sync", SYNC, 0)
    evidence["checks"] += follower_checks(json.loads(receipt["output"]), "IN-SYNC", "")
    evidence["checks"] += watches_checks(managed_unit("theme"), inputs, inputs["expected_watches"])


def mcp_checks(name, inputs):
    """Bind native producer observations to this beat's independently requested child."""
    if describe(name)["entry"] != "macos-mcp":
        raise ValueError(f"MACOS-MCP-PLAN: {name}")
    producer, context = inputs["producer"], inputs["expected_context"]
    child, observed = inputs["child"], inputs["child_observed"]
    head = producer["chain"][0]
    words = shlex.split(head["argv"])
    records = []
    for row in inputs["action_records"]:
        records.append({key: row.get(key) for key in ("run", "beat", "pid", "action", "door")})
    tools = [{key: row.get(key) for key in ("run", "beat", "pid", "msg", "tool", "err")}
             for row in inputs["tool_records"]]
    expected = {**context, "pid": producer["pid"]}
    births = engine_births(observed, "claude")
    captured = [value for value in observed["stored"]["recipe"]["env"] if value.partition("=")[0] in TERMINAL_KEYS]
    terminal = {"HERDR_PANE_ID": child["terminal"]["pane"], "HERDR_WORKSPACE_ID": child["workspace"],
                "HERDR_SOCKET_PATH": os.environ["HERDR_SOCKET_PATH"]}
    return [check("native-mcp-platform", "macos", journey_os()),
            check("native-mcp-action-producer", [{**expected, "action": "chat.new", "door": "mcp"}], records),
            check("native-mcp-tool-producer", [{**expected, "msg": "mcp.call", "tool": "new", "err": None}], tools),
            check("native-mcp-daemon-argv", ["pfm", "mcp", "serve"], [Path(words[0]).name, *words[1:]] if words else []),
            check("native-mcp-daemon-birth", {"pid": producer["pid"], "start": producer["start"], "parent": 1},
                  {key: head.get(key) for key in ("pid", "start", "parent")}),
            check("native-mcp-daemon-xpc", "com.professor." + os.environ["PFM_UNIT_PREFIX"] + ".mcp", producer["environment"].get("XPC_SERVICE_NAME")),
            check("native-mcp-creation-result", {"status": "ok", "code": 0}, {key: inputs["creation_result"].get(key) for key in ("status", "code")}),
            check("native-mcp-requested-child", inputs["expected_child"], {key: child.get(key) for key in ("engine", "account", "name")}),
            check("native-mcp-child-backend", "herdr", child["terminal"]["backend"]),
            check("native-mcp-child-session", child["session_id"], observed["status"]["session_id"]),
            check("native-mcp-child-pane-environment", [terminal], [{key: row["environment"].get(key) for key in terminal} for row in births]),
            check("native-mcp-recipe-terminal-captures", [], captured)]


def run_mcp(name, owner_file, inputs, prefix):
    owner = read_json(owner_file)
    checked_owner(owner)
    faults.owned_path(Path(prefix).parent)
    evidence = {"checks": mcp_checks(name, inputs), "before": bundle(owner_file)}
    receipt_prefix = str(prefix) + ".native-list"
    receipt = action(owner, ROSTER, receipt_prefix)
    warnings, table = roster_receipt(receipt["output"])
    evidence["checks"] += [check("native-mcp-roster-exit", 0, receipt["exit_code"]),
                           check("native-mcp-roster-warnings", [], warnings)]
    evidence["checks"] += text_fleet_checks(table, read_json(os.environ["JOURNEY_CARRY"]))
    evidence.update(receipts=[{"prefix": receipt_prefix, "argv": ROSTER}], during=bundle(owner_file), after=bundle(owner_file))
    write_json(str(prefix) + ".checks.json", evidence["checks"])
    write_json(str(prefix) + ".evidence.json", evidence)
    return evidence


def run(name, owner_file, inputs, prefix):
    if describe(name)["entry"] == "macos-mcp":
        return run_mcp(name, owner_file, inputs, prefix)
    owner = read_json(owner_file)
    checked_owner(owner)
    faults.owned_path(Path(prefix).parent)
    initial = managed_unit("theme")
    if initial["platform"] != "darwin":
        raise ValueError("MACOS-READER-PLATFORM: actual native Darwin required")
    evidence = {"checks": [], "receipts": [], "units": [initial], "before": bundle(owner_file)}
    def execute(label, argv, expected):
        receipt = action(owner, argv, str(prefix) + "." + label)
        evidence["receipts"].append({"prefix": str(prefix) + "." + label, "argv": list(argv)})
        evidence["checks"].append(check(label + ":exit", expected, receipt["exit_code"]))
        return receipt
    previous = execute("theme-before", ["pfm", "theme", "status", "--json"], 0)
    if json.loads(previous["output"])["enabled"] is not False:
        raise ValueError("MACOS-READER-PRELUDE: previous reader must leave theme off")
    with ExitStack() as preserved:
        for settings in inputs["account_settings"]:
            preserved.enter_context(faults.preserved_file(settings, retain_inode=True))
        try:
            enabled = execute("theme-on", ["pfm", "theme", "on", "--json"], 0)
            evidence["checks"] += follower_checks(json.loads(enabled["output"]), "IN-SYNC", "")
            evidence["checks"] += watches_checks(managed_unit("theme"), inputs, inputs["expected_watches"])
            if name in {"macos-watch-failure", "macos-watch-unlistable"}:
                discovery_failure(owner, inputs, prefix, evidence, execute, unlistable=name == "macos-watch-unlistable")
            else:
                {"macos-watch-symlink": symlink_watches, "macos-own-job": own_job}[name](owner, inputs, prefix, evidence, execute)
            evidence["during"] = bundle(owner_file)
        finally:
            restored = execute("theme-off", ["pfm", "theme", "off", "--json"], 0)
            evidence["checks"].append(check("theme-off-disabled", False, json.loads(restored["output"])["enabled"]))
    evidence["after"] = bundle(owner_file)
    evidence["checks"].append(check("native-follower-unloaded", "absent", managed_unit("theme")["state"]))
    write_json(str(prefix) + ".checks.json", evidence["checks"])
    write_json(str(prefix) + ".evidence.json", evidence)
    return evidence
