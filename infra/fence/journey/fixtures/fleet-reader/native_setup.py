#!/usr/bin/env python3
"""Materialize only the real resources required by one supported native plan."""
import json
import os
from pathlib import Path
import sys

from forecast import HERE
from bootstrap import tmux_server, wait
from chat import fresh_shell, scenario, shell_ready, stage_role
from chat_carry import carry, select
from herdr import request
from isolation import environment_d
from observation import bundle, terminal
from projection import selected
from native_tmux import resources as empty_resources, theme_resources
from runtime import command, read_json, write_json, write_text


def account_settings(home=None):
    home = Path(home) if home is not None else Path.home()
    inventory = read_json(HERE / "fixtures/root-inventory.json")
    return [str(home / ".claude/settings.json"),
            *(str(home / ".cc" / str(account) / "settings.json") for account in inventory["claude_accounts"])]


def move_resources(target, receipt):
    actual = terminal(target)
    created = request(os.environ["HERDR_SOCKET_PATH"], "pane.split", {
        "target_pane_id": target["pane"], "direction": "right", "focus": False,
        "cwd": str(Path.home() / "work/atlas")})["result"]
    write_json(str(receipt) + ".split.json", created)
    panes = request(os.environ["HERDR_SOCKET_PATH"], "pane.list", {})["result"]["panes"]
    siblings = [row for row in panes if row["tab_id"] == actual["identity"]["tab"]
                and row["pane_id"] != target["pane"]]
    if len(siblings) != 1:
        raise ValueError(f"NATIVE-PRELUDE-PLACEHOLDER: exactly one actual sibling required, actual={siblings!r}")
    destination = carry()["workspaces"]["beacon"]["id"]
    if destination == actual["identity"]["workspace"]:
        raise ValueError("NATIVE-PRELUDE-DESTINATION: different owned workspace required")
    return {"placeholder": siblings[0]["pane_id"], "destination_workspace": destination}


def recipe_binary(chat, owner_file, preserve_alias=False):
    current = selected(bundle(owner_file), chat)
    declared = Path(current["stored"]["recipe"]["binary"])
    binary = declared if preserve_alias else declared.resolve(strict=True)
    if not declared.is_absolute() or not declared.resolve(strict=True).is_file() or not os.access(declared, os.X_OK):
        raise ValueError("NATIVE-PRELUDE-BINARY: actual recipe executable required")
    return str(binary)


def prepare(plan_file, chat_file, owner_file, resources_file, capability):
    plan, chat, owner = read_json(plan_file), read_json(chat_file), read_json(owner_file)
    values = {"id": capability}
    root = Path(os.environ["JOURNEY_ROOT"])
    name = plan["plan"]
    if plan["entry"] in {"shell-transition", "shell-client", "shell-birth"}:
        values["subject"] = fresh_shell(chat["branch"])
    if plan["entry"] == "shell-birth":
        values.update(birth_branch=chat["branch"], birth_origin="reader-native-" + name,
                      project="atlas", workspace=carry()["workspaces"]["atlas"]["id"],
                      fixture=str(HERE / "fixtures/chat-mesh/seed.json"))
        if name == "stale-tmux-shell":
            values.update(move_resources(values["subject"], resources_file),
                          socket_name="reader-c68-" + os.environ["PFM_RUN"][-8:],
                          session="reader-c68-" + os.environ["PFM_RUN"][-8:])
    if plan["entry"] in {"outside", "denied", "launcher", "launch-reader"}:
        wait("outside native owner shell", lambda: shell_ready(owner), 15)
    if name.startswith(("moved", "stale-seat-")):
        values.update(move_resources(chat["terminal"], resources_file))
        if name.startswith("stale-seat-"):
            values["restore_owner"] = owner
    if name == "outside-stopped":
        values["tmux_socket"] = tmux_server("reader-native-stopped", "reader")["socket"]
    if name == "outside-launcher-version":
        fixture = HERE / "fixtures/fleet-reader/native-version-seed.json"
        staged = stage_role(owner, chat["branch"], "reader-native-launcher-version", str(fixture))
        values.update(tmux_socket=tmux_server("reader-native-launcher-stopped", "reader-launcher")["socket"],
                      native_version=read_json(fixture)["version"], records=staged["records"], scenario=staged["scenario"])
    if name == "launch-unreadable-settings":
        fixture = read_json(HERE / "fixtures/chat-mesh/seed.json")
        declared = read_json(HERE / "fixtures/fleet-reader/launch-inputs.json")
        role = "reader-native-cost-launch"
        gate = root / "reader-native-cost-launch.ready"
        fixture.update(ready_hold=str(gate), ready_timeout_ms=declared["ready_timeout_ms"])
        seed = root / "reader-native-cost-launch.seed.json"
        write_json(seed, fixture)
        staged = stage_role(owner, chat["branch"], role, str(seed))
        values.update(branch=chat["branch"], role=role, launch_name="reader-cost-" + os.environ["PFM_RUN"][-8:],
                      cwd=str(Path.home() / "work/atlas"), scenario=staged["scenario"], records=staged["records"],
                      ready_hold=str(gate), settings=account_settings()[1], user_otlp=declared["user_otlp"],
                      native_binary=recipe_binary(chat, owner_file))
    if name == "empty-server":
        values.update(empty_resources())
    if name == "unset-theme":
        values.update(theme_resources(account_settings()))
    if name == "quiet-picker-dead-server":
        fixture = HERE / "fixtures/fleet-reader/presence-seed.json"
        socket_name = "cc-reader-presence-" + os.environ["PFM_RUN"][-8:]
        session = "reader-presence"
        subject = tmux_server(socket_name, session)
        staged = stage_role(subject, chat["branch"], "reader-native-presence", str(fixture))
        values.update(subject=subject, socket_name=socket_name, session=session, scenario=staged["scenario"],
                      records=staged["records"], session_id=staged["scenario_session_id"], native_binary=recipe_binary(chat, owner_file, True),
                      cwd=str(Path.home() / "work/atlas"), config_dir=str(Path.home() / ".cc/2"),
                      picker=json.loads(command([sys.executable, str(HERE / "terminal.py"), "create"])),
                      name="reader-presence-" + os.environ["PFM_RUN"][-8:])
    if name in {"moved-replaced-client", "user-tab-exec"}:
        if name == "moved-replaced-client":
            values.update(move_resources(values["subject"], resources_file))
        branch = "+".join((chat["branch"].split("+")[0], "tmux", "project", "claude"))
        identities = (("old_chat", "new-from-cli"), ("replacement_chat", "new-from-picker")) if name == "moved-replaced-client" else (("old_chat", "new-from-cli"),)
        for key, origin in identities:
            actor = select(branch, origin)
            if actor is None:
                raise ValueError(f"NATIVE-PRELUDE-CLIENT: required carried actor {branch}:{origin}")
            path = root / ("reader-native-" + key + ".json")
            write_json(path, actor)
            values[key] = str(path)
    if plan["entry"] == "macos-theme":
        settings = Path.home() / ".cc/2/settings.json"
        target = settings.with_name("reader-settings-target.json")
        write_text(target, settings.read_text())
        settings_files = account_settings()
        known = sorted({str(Path(path).resolve(strict=True)) for path in settings_files})
        watches = []
        for path in known:
            watches += [path, str((Path(path).parent / "themes").resolve())]
        watches.append(str(Path.home() / "Library/Preferences/.GlobalPreferences.plist"))
        values.update(settings=str(settings), settings_target=str(target), account_settings=settings_files,
                      expected_watches=watches, account_directory=str(Path.home() / ".cc"),
                      follower_program=[str(Path.home() / ".local/libexec/pfm/pfm-agent"), "theme", "sync"])
    normal = name in {"outside-before-prompt", "outside-usage-hook", "outside-login-default"}
    if plan["entry"] == "denied" or normal:
        subject = json.loads(command([sys.executable, str(HERE / "terminal.py"), "create"])) if normal else owner
        wait("native subject owner shell", lambda: shell_ready(subject), 15)
        seed = read_json(HERE / "fixtures/chat-mesh/seed.json")
        if name in {"outside-before-prompt", "outside-login-default"}:
            values["ready_hold"] = str(root / ("reader-native-" + name + ".ready"))
            seed.update(ready_hold=values["ready_hold"], ready_timeout_ms=120000)
        seed_file = root / ("reader-native-" + name + ".seed.json")
        write_json(seed_file, seed)
        staged = scenario(subject, chat["branch"], "reader-native-" + name, str(seed_file))
        write_text(Path(staged["records"]) / ".reader-input", "Owned native input recording directory.\n")
        cwd = root / ("reader-native-" + name + "-cwd")
        write_text(cwd / "fixture.txt", "Native outside-project caller.\n")
        values.update(scenario=staged["scenario"], records=staged["records"], native_binary=recipe_binary(chat, owner_file, normal),
                      cwd=str(cwd))
        if normal:
            values.update(subject=subject, session_id=staged["scenario_session_id"], config_dir=str(Path.home() / ".cc/2"))
            if name == "outside-login-default":
                from native_actor import generated_environment
                receipt = root / "reader-native-environment-d.json"
                generated = generated_environment(environment_d(receipt))
                values.update(generated_environment=generated, environment_receipt=str(receipt),
                              environment_d=str(Path.home() / ".config/environment.d/pfm-claude-config-dir.conf"),
                              config_dir=generated["CLAUDE_CONFIG_DIR"])
            if name == "outside-usage-hook":
                from native_actor import usage_settings
                folder = root / "reader-native-usage-inputs"
                values.update(broken_config=str(folder / "config.json"), hook_settings=str(folder / "settings.json"),
                              hook_prefix=str(folder / "hook"))
                write_json(values["broken_config"], {})
                write_json(values["hook_settings"], usage_settings(values["broken_config"], values["hook_prefix"]))
        else:
            codex_home = root / "reader-native-codex-home"
            write_text(codex_home / "config.toml", "")
            values["codex_home"] = str(codex_home)
    missing = set(plan["resources"]) - values.keys()
    if missing:
        raise ValueError(f"NATIVE-PRELUDE-MISSING: {sorted(missing)!r}")
    write_json(resources_file, values)


if __name__ == "__main__":
    prepare(*sys.argv[1:])
