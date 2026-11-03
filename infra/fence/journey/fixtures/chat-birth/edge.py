#!/usr/bin/env python3
"""Drive native birth forecast edges through owner CLI and engine tool entries."""
import copy
import json
import os
from pathlib import Path
import re
import shlex
import sys
import tomllib

from bootstrap import wait
from chat import before, capture, inline, scenario
from faults import owned_path, preserved_file, temporary_text
from observation import report, terminal, terminal_births
from observation_process import engine_births
from runtime import now_ms, read_json, write_json, write_text
from terminal import type_command


def supported(factors):
    matches = [row for row in read_json(Path(__file__).with_name("edge-rows.json"))
               if row["factors"] == factors]
    if len(matches) != 1:
        raise ValueError("BIRTH-EDGE-FACTORS: " + factors)
    return matches[0]


def staging_path(directory):
    path = owned_path(directory, preserve=True)
    if not path.is_dir():
        raise ValueError("BIRTH-EDGE-STAGING: existing run-owned directory required")
    return path


def prepare(factors, directory):
    row = supported(factors)
    directory = staging_path(directory)
    path = directory / "edge-input.json"
    if path.exists():
        raise ValueError("BIRTH-EDGE-REUSED: staging directory already prepared")
    write_json(path, read_json(Path(__file__).with_name("scenario.json")))
    write_json(directory / "edge-state.json", {"factors": factors, "kind": row["kind"]})
    if row["kind"] == "guard":
        write_text(directory / "edge-origin.sh", 'python3 "$(dirname "$0")/edge-note.py"\n')
        write_text(directory / "edge-note.py", "print('edge-allowed')\n")
    return str(path)


def disable_native_text(raw, key):
    document = tomllib.loads(raw)
    if document["hooks"]["state"][key].get("enabled", True) is not True:
        raise ValueError("BIRTH-EDGE-NATIVE-STATE: handler must begin enabled")
    expected = copy.deepcopy(document)
    expected["hooks"]["state"][key]["enabled"] = False
    candidates = []
    # Keep installer comments byte-exact; tomllib validates the single boolean edit.
    for match in re.finditer(r"(?m)^[ \t]*enabled[ \t]*=[ \t]*(true)(?=[ \t]*(?:#.*)?$)", raw):
        start, end = match.span(1)
        candidate = raw[:start] + "false" + raw[end:]
        if tomllib.loads(candidate) == expected:
            candidates.append(candidate)
    if len(candidates) != 1:
        raise ValueError("BIRTH-EDGE-NATIVE-EDIT: exactly one existing enabled=true assignment required")
    return candidates[0]


def native_input():
    account = owned_path(Path(os.environ["HOME"]) / ".codex")
    source = owned_path(account / "hooks.json")
    receipt = owned_path(account / ".professor-hook-trust.json")
    config = owned_path(account / "config.toml")
    command = str(Path(os.environ["HOME"]) / ".local/bin/pfm") + " internal resume-unkill"
    keys = [str(source) + f":session_start:{matcher}:{handler}"
            for matcher, entry in enumerate(read_json(source)["hooks"]["SessionStart"])
            if entry.get("matcher") == "resume"
            for handler, hook in enumerate(entry["hooks"]) if hook.get("command") == command]
    if len(keys) != 1 or not read_json(receipt).get(keys[0]):
        raise ValueError("BIRTH-EDGE-RECEIPT: installed resume-unkill fingerprint required")
    raw = config.read_text()
    state = tomllib.loads(raw)["hooks"]["state"][keys[0]]
    if state.get("trusted_hash") != read_json(receipt)[keys[0]]:
        raise ValueError("BIRTH-EDGE-FINGERPRINT: native state must carry the historical receipt")
    return config, receipt, keys[0], raw


def owner_action(target, directory, name, arguments):
    prefix = str(directory / name)
    if Path(prefix + ".status").exists():
        raise ValueError("BIRTH-EDGE-ACTION-REUSED: " + prefix)
    type_command(target, arguments, prefix)
    wait("birth edge owner receipt " + name, lambda: Path(prefix + ".status").is_file(), 90)
    return {"argv": arguments, "status": Path(prefix + ".status").read_text().strip(),
            "output": Path(prefix + ".out").read_text(), "prefix": prefix}


def trust_projection(evidence):
    doctor = "doctor: hook codex[1] hooks.json SessionStart resume-unkill DISABLED — operator choice preserved; re-enable in Codex only if wanted"
    expected_skip = "skip    resume-unkill hook trust in " + evidence["account"] + ": its operator disabled the handler, left alone"
    projection = {"historical_receipt_present": bool(evidence["receipt_before"]),
                  "native_enabled": tomllib.loads(evidence["config_after"])["hooks"]["state"][evidence["key"]]["enabled"],
                  "doctor_disabled": evidence["doctor"]["status"] == "1" and doctor in evidence["doctor"]["output"].splitlines(),
                  "receipt_preserved": evidence["receipt_before"] == evidence["receipt_after"],
                  "config_preserved": evidence["faulted"] == evidence["config_after"],
                  "fault_restored": evidence["original"] == evidence["restored"]}
    if "install" in evidence:
        projection["installer_preserved_operator_choice"] = evidence["install"]["status"] == "0" and expected_skip in [line.strip() for line in evidence["install"]["output"].splitlines()]
    return projection


def before_entry(factors, directory, owner_file):
    row = supported(factors)
    directory = staging_path(directory)
    if row["kind"] == "guard":
        return {"action": "n/a:guard-runs-after-capture"}
    if row["kind"] == "updater":
        return updater_prepare(factors, directory, owner_file)
    config, receipt, key, original = native_input()
    faulted = disable_native_text(original, key)
    evidence = {"factors": factors, "key": key, "account": str(config.parent),
                "original": original, "faulted": faulted, "receipt_before": receipt.read_text(),
                "started_at_ms": now_ms()}
    try:
        with preserved_file(receipt), temporary_text(config, faulted):
            target = read_json(owner_file)
            evidence["doctor"] = owner_action(target, directory, "edge-doctor", ["pfm", "doctor"])
            if row["kind"] == "trust-install":
                evidence["install"] = owner_action(target, directory, "edge-install", ["pfm", "install", "--yes", "--offline", "--skip-themes"])
            evidence.update(config_after=config.read_text(), receipt_after=receipt.read_text())
    finally:
        evidence.update(restored=config.read_text(), completed_at_ms=now_ms())
        write_json(directory / "edge-evidence.json", evidence)
    projection = trust_projection(evidence)
    write_json(directory / "edge-projection.json", projection)
    return projection


def updater_prepare(factors, directory, owner_file):
    original = owned_path(os.environ["PFM_CONFIG"])
    config = read_json(original)
    accounts = [account for account in config["accounts"] if account["id"] == 2]
    if len(accounts) != 1:
        raise ValueError("BIRTH-EDGE-UPDATER-ACCOUNT: synthetic account2 required")
    private = directory / "edge-config.json"
    write_json(private, {**config, "accounts": accounts})
    configured = [account["id"] for account in read_json(private)["accounts"]]
    if configured != [2]:
        raise ValueError("BIRTH-EDGE-UPDATER-ROSTER: private config must contain only account2")
    owner = read_json(owner_file)
    branch, origin = os.environ["JOURNEY_BRANCH"], "edge-outside-launch"
    if branch.split("+")[1:] != ["tmux", "project", "claude"]:
        raise ValueError("BIRTH-EDGE-UPDATER-BRANCH: actual tmux project Claude required")
    normal = read_json(directory / "scenario.json")
    child_fixture = directory / "edge-child.json"
    write_json(child_fixture, read_json(Path(__file__).with_name("scenario.json")))
    child = scenario(owner, branch, origin, str(child_fixture))
    type_command(owner, ["export", "MOCK_ENGINE_SCENARIO=" + normal["scenario"],
                         "PFM_TEST_FRESH_SOCKET=" + Path(normal["launch_socket"]).name])
    write_json(directory / "edge-state.json", {"factors": factors, "kind": "updater", "owner": owner,
               "child": child, "child_before": before(owner), "branch": branch, "origin": origin,
               "private_config": str(private), "configured_accounts": configured,
               "shared_config": str(original), "shared_config_before": original.read_text()})
    return {"action": "n/a:private-native-launch-input-staged"}


def updater_after(factors, directory, current):
    state = read_json(directory / "edge-state.json")
    account = Path(os.environ["HOME"]) / ".cc/1"
    settings = account / "settings.json"
    if not settings.is_symlink():
        raise ValueError("BIRTH-EDGE-UPDATER-ENTRY: real shared account settings link required")
    original_link, original_bytes = os.readlink(settings), settings.read_bytes()
    owned_path(settings.resolve())
    temporary = account / (".edge-settings-" + read_json(directory / "scenario.json")["scenario_session_id"] + ".tmp")
    receipt = directory / "edge-updater.out"
    command = shlex.join(["cp", str(settings), str(temporary)]) + " && " + shlex.join(["mv", str(temporary), str(settings)]) + " && " + shlex.join(["printf", "edge-updater-complete\\n"]) + " > " + shlex.quote(str(receipt))
    fixture = directory / "edge-tools.json"
    steps = [{"type": "tool_call", "tool": "Bash", "input": {"command": command}},
             {"type": "turn", "reply": "edge-updater-finished", "busy_ms": 0}]
    write_json(fixture, steps)
    evidence = {"factors": factors, "steps": steps, "private_config": state["private_config"],
                "configured_accounts": state["configured_accounts"], "account": str(account), "started_at_ms": now_ms()}
    try:
        with preserved_file(settings), preserved_file(temporary):
            if temporary.exists() or receipt.exists():
                raise ValueError("BIRTH-EDGE-UPDATER-REUSED: native update receipts already exist")
            inline(current["terminal"], str(fixture), "birth-edge-updater")
            def updated():
                display = terminal(current["terminal"])
                evidence["updater_terminal"] = display
                return display if "edge-updater-finished" in display["text"] else None
            evidence["updater_terminal"] = wait("actual native updater tool completed", updated, 20)
            updated = receipt.is_file() and receipt.read_text() == "edge-updater-complete\n" and not settings.is_symlink() and settings.read_bytes() == original_bytes and not temporary.exists()
            if not updated:
                raise ValueError("BIRTH-EDGE-UPDATER-NATIVE-REFUSED: actual native Bash did not temp-write and rename the account settings; see edge-evidence.json")
            child = state["child"]
            canonical = str(Path(os.environ["HOME"]) / ".local/bin/claude")
            arguments = ["env", "PFM_CONFIG=" + state["private_config"], "MOCK_ENGINE_SCENARIO=" + child["scenario"],
                         "PFM_TEST_FRESH_SOCKET=" + Path(child["launch_socket"]).name,
                         "CLAUDE_CONFIG_DIR=" + str(account), "PFM_CLAUDE_CONFIG_DIR_DEFAULT=" + str(account), canonical]
            prefix = str(directory / "edge-outside-launch")
            type_command(state["owner"], arguments, prefix)
            carried = wait("outside-shell native Claude account2 captured", lambda:
                           capture(state["owner"], state["branch"], state["origin"], state["child_before"]), 25)
            display = terminal(carried["terminal"])
            births = engine_births({"terminal": display, "process_births": terminal_births(display)}, "claude")
            if len(births) != 1:
                raise ValueError("BIRTH-EDGE-UPDATER-BIRTH: exactly one actual native child required")
            status = json.loads(report(["pfm", "chat", "status", carried["session_id"], "--json"]))
            evidence.update(child=carried, birth=births[0], child_terminal=display, launch_dispatch=read_json(prefix + ".dispatch.json"),
                            canonical=canonical, updater_temp_and_rename=updated, child_status=status)
    finally:
        evidence.update(settings_restored=settings.is_symlink() and os.readlink(settings) == original_link and settings.read_bytes() == original_bytes,
                        shared_config_preserved=Path(state["shared_config"]).read_text() == state["shared_config_before"], completed_at_ms=now_ms())
        write_json(directory / "edge-evidence.json", evidence)
    projection = updater_projection(evidence)
    evidence["projection"] = projection
    write_json(directory / "edge-evidence.json", evidence)
    write_json(directory / "edge-projection.json", projection)
    return projection


def updater_projection(evidence):
    arguments, account = evidence["launch_dispatch"]["argv"], evidence["account"]
    return {"configured_accounts": evidence["configured_accounts"],
            "outside_shell_launcher": arguments[0] == "env" and arguments[-1] == evidence["canonical"],
            "inherited_sentinel": arguments[-3:-1] == ["CLAUDE_CONFIG_DIR=" + account, "PFM_CLAUDE_CONFIG_DIR_DEFAULT=" + account],
            "updater_temp_and_rename": evidence["updater_temp_and_rename"], "carried_account": evidence["child"]["account"],
            "native_account": 2 if evidence["birth"]["environment"].get("CLAUDE_CONFIG_DIR") == str(Path(account).with_name("2")) else "wrong-account",
            "child_state": evidence["child_status"]["state"], "settings_restored": evidence["settings_restored"],
            "shared_config_preserved": evidence["shared_config_preserved"]}


def guard_reason():
    return 'engine-guard blocked `python3 "$(dirname "$0")/edge-note.py"`: it runs code or applies a patch the guard cannot read (not on disk yet, a pipe, a terminal): pass code inline (-c, -e) or as a heredoc to the interpreter, or write the script or patch file in one command and run it in the next.'


def guard_projection(display, allowed, refused):
    denial = " ".join(display.split()).partition("⏺ Bash — denied by hook: ")[2].partition("⏺ Bash(…)")[0].strip()
    return {"denied_without_origin": denial == guard_reason(), "allowed_with_origin": allowed == "edge-allowed\n",
            "allowed_stdout": allowed, "refused_execution": "executed" if refused else "n/a:hook-denied"}


def after_entry(factors, directory, chat_file):
    row = supported(factors)
    directory = staging_path(directory)
    if row["kind"] not in {"guard", "updater"}:
        return {"action": "n/a:trust-runs-before-birth"}
    current = read_json(chat_file)
    engine = "claude" if row["kind"] == "updater" else "codex"
    if current["engine"] != engine or current not in read_json(os.environ["JOURNEY_CARRY"])["chats"]:
        raise ValueError("BIRTH-EDGE-CAPTURE: actual carried " + engine + " chat required")
    if row["kind"] == "updater":
        return updater_after(factors, directory, current)
    allowed, refused = directory / "edge-allowed.out", directory / "edge-refused.out"
    if allowed.exists() or refused.exists():
        raise ValueError("BIRTH-EDGE-GUARD-REUSED: execution receipts already exist")
    denied = 'python3 "$(dirname "$0")/edge-note.py" > ' + shlex.quote(str(refused))
    accepted = shlex.join(["bash", str(directory / "edge-origin.sh")]) + " > " + shlex.quote(str(allowed))
    steps = [{"type": "tool_call", "tool": "Bash", "input": {"command": command}}
             for command in (denied, accepted)] + [{"type": "turn", "reply": "edge-guard-complete", "busy_ms": 0}]
    fixture = directory / "edge-tools.json"
    write_json(fixture, steps)
    inline(current["terminal"], str(fixture), "birth-edge-guard")
    evidence = {"factors": factors, "chat": current, "steps": steps, "started_at_ms": now_ms()}
    def completed():
        display = terminal(current["terminal"])
        evidence["terminal"] = display
        return display if "edge-guard-complete" in display["text"] else None
    try:
        display = wait("native Codex origin/no-origin tool decisions", completed, 20)
        evidence.update(allowed_stdout=allowed.read_text() if allowed.is_file() else "n/a:not-executed", refused_exists=refused.exists())
        projection = guard_projection(display["text"], evidence["allowed_stdout"], evidence["refused_exists"])
        evidence["projection"] = projection
    finally:
        evidence["completed_at_ms"] = now_ms()
        write_json(directory / "edge-evidence.json", evidence)
    write_json(directory / "edge-projection.json", projection)
    return projection


def assert_edge(factors, directory, chat_file):
    row = supported(factors)
    directory = staging_path(directory)
    evidence = read_json(directory / "edge-evidence.json")
    if evidence["factors"] != factors:
        raise ValueError("BIRTH-EDGE-EVIDENCE: factors changed")
    current = read_json(chat_file)
    if current not in read_json(os.environ["JOURNEY_CARRY"])["chats"]:
        raise ValueError("BIRTH-EDGE-CAPTURE: current chat is not carried")
    projection = (guard_projection(evidence["terminal"]["text"], evidence["allowed_stdout"], evidence["refused_exists"]) if row["kind"] == "guard" else
                  updater_projection(evidence) if row["kind"] == "updater" else trust_projection(evidence))
    if projection != row["expected"]:
        raise ValueError(f"BIRTH-EDGE-PROJECTION: {directory / 'edge-evidence.json'}: expected={row['expected']!r} actual={projection!r}")
    return projection


def self_test():
    import tempfile
    from unittest.mock import patch
    key = "/fixture/hooks.json:session_start:0:0"
    raw = '# keep installer ownership\n[hooks.state."' + key + '"]\nenabled = true # operator choice\ntrusted_hash = "historical"\n[hooks.state.other]\nenabled = true\n'
    changed = disable_native_text(raw, key)
    assert changed == raw.replace("enabled = true # operator choice", "enabled = false # operator choice"), changed
    for bad in (changed, raw.replace("enabled = true # operator choice", "enabled=true")):
        if bad == changed:
            try:
                disable_native_text(bad, key)
            except ValueError as error:
                assert str(error) == "BIRTH-EDGE-NATIVE-STATE: handler must begin enabled", error
            else:
                raise AssertionError("already-disabled handler accepted")
        else:
            assert tomllib.loads(disable_native_text(bad, key))["hooks"]["state"][key]["enabled"] is False
    shown = "⏺ Bash — denied by hook: " + guard_reason() + "\n⏺ Bash(…)\nedge-guard-complete"
    factors = next(row["factors"] for row in read_json(Path(__file__).with_name("edge-rows.json")) if row["kind"] == "guard")
    assert guard_projection(shown, "edge-allowed\n", False) == supported(factors)["expected"]
    for text, output, refused in ((shown.replace("denied by hook", "allowed by hook"), "edge-allowed\n", False),
                                  (shown, "wrong\n", False), (shown, "edge-allowed\n", True)):
        assert guard_projection(text, output, refused) != supported(factors)["expected"]
    evidence = {"account": "/fixture", "key": key, "original": raw, "faulted": changed,
                "config_after": changed, "restored": raw, "receipt_before": '{"fingerprint":"historical"}',
                "receipt_after": '{"fingerprint":"historical"}', "doctor": {"status": "1", "output":
                    "doctor: hook codex[1] hooks.json SessionStart resume-unkill DISABLED — operator choice preserved; re-enable in Codex only if wanted\n"}}
    doctor_factors = next(row["factors"] for row in read_json(Path(__file__).with_name("edge-rows.json")) if row["kind"] == "trust-doctor")
    assert trust_projection(evidence) == supported(doctor_factors)["expected"], trust_projection(evidence)
    for field, value in (("config_after", raw), ("receipt_after", "{}"), ("restored", changed),
                         ("doctor", {"status": "0", "output": "ok"})):
        altered = {**evidence, field: value}
        assert trust_projection(altered) != supported(doctor_factors)["expected"], (field, altered)
    evidence["install"] = {"status": "0", "output": "  skip    resume-unkill hook trust in /fixture: its operator disabled the handler, left alone\n"}
    install_factors = next(row["factors"] for row in read_json(Path(__file__).with_name("edge-rows.json")) if row["kind"] == "trust-install")
    assert trust_projection(evidence) == supported(install_factors)["expected"], trust_projection(evidence)
    evidence["install"]["status"] = "1"
    assert trust_projection(evidence) != supported(install_factors)["expected"]
    updater = {"configured_accounts": [2], "account": "/fixture/.cc/1", "canonical": "/fixture/.local/bin/claude",
               "launch_dispatch": {"argv": ["env", "CLAUDE_CONFIG_DIR=/fixture/.cc/1", "PFM_CLAUDE_CONFIG_DIR_DEFAULT=/fixture/.cc/1", "/fixture/.local/bin/claude"]},
               "updater_temp_and_rename": True, "child": {"account": 2}, "birth": {"environment": {"CLAUDE_CONFIG_DIR": "/fixture/.cc/2"}},
               "child_status": {"state": "idle"}, "settings_restored": True, "shared_config_preserved": True}
    updater_factors = next(row["factors"] for row in read_json(Path(__file__).with_name("edge-rows.json")) if row["kind"] == "updater")
    assert updater_projection(updater) == supported(updater_factors)["expected"], updater_projection(updater)
    for field, value in (("configured_accounts", [1]), ("birth", {"environment": {"CLAUDE_CONFIG_DIR": "/fixture/.cc/1"}}),
                         ("child_status", {"state": "working"}), ("settings_restored", False), ("shared_config_preserved", False),
                         ("launch_dispatch", {"argv": ["env", "CLAUDE_CONFIG_DIR=/fixture/.cc/1", "wrong-sentinel", "/fixture/.local/bin/claude"]})):
        assert updater_projection({**updater, field: value}) != supported(updater_factors)["expected"], field
    with tempfile.TemporaryDirectory(prefix="birth-edge-") as temporary:
        root = Path(temporary)
        stage = root / "stage"
        stage.mkdir()
        config = root / "machine.json"
        write_json(config, {"accounts": [{"id": 1, "configDir": "~/.cc/1"}, {"id": 2, "configDir": "~/.cc/2"}], "claude": {"cache1h": True}})
        original = config.read_text()
        owner_file = root / "owner.json"
        owner = {"backend": "tmux", "socket": str(root / "owner"), "pane": "%1"}
        write_json(owner_file, owner)
        write_json(stage / "scenario.json", {"scenario": str(root / "normal.json"), "launch_socket": str(root / "cc-normal")})
        child = {"scenario": str(root / "child.json"), "launch_socket": str(root / "cc-child")}
        with patch.dict(os.environ, {"JOURNEY_ROOT": temporary, "HOME": str(root / "home"), "PFM_CONFIG": str(config), "JOURNEY_BRANCH": "linux+tmux+project+claude"}), \
                patch(__name__ + ".scenario", return_value=child), \
                patch(__name__ + ".before", return_value={"target": owner, "seats": []}), patch(__name__ + ".type_command") as typed:
            prepare(updater_factors, str(stage))
            updater_prepare(updater_factors, stage, str(owner_file))
            assert read_json(stage / "edge-config.json") == {"accounts": [{"id": 2, "configDir": "~/.cc/2"}], "claude": {"cache1h": True}}
            assert config.read_text() == original
            typed.assert_called_once_with(owner, ["export", "MOCK_ENGINE_SCENARIO=" + str(root / "normal.json"), "PFM_TEST_FRESH_SOCKET=cc-normal"])
            try:
                prepare(updater_factors, str(stage))
            except ValueError as error:
                assert str(error) == "BIRTH-EDGE-REUSED: staging directory already prepared", error
            else:
                raise AssertionError("reused edge input accepted")
    print("BIRTH-EDGE-SELFTEST PASS: native boolean edit, trust/guard/updater projections, private config staging, explicit unavailable seam")


if __name__ == "__main__":
    try:
        operation, *arguments = sys.argv[1:]
        if operation == "self-test":
            self_test()
        else:
            value = {"prepare": prepare, "before-entry": before_entry, "after-entry": after_entry,
                     "assert": assert_edge, "supported": supported,
                     "expected": lambda factors: supported(factors)["expected"]}[operation](*arguments)
            print(value if isinstance(value, str) else json.dumps(value, sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print("BIRTH-EDGE-ERROR: " + str(error), file=sys.stderr)
        sys.exit(2)
