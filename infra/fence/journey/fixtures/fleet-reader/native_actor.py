#!/usr/bin/env python3
"""The one actual native child lifetime used by outside reader actors."""
from contextlib import contextmanager, nullcontext
import os
from pathlib import Path
import shlex
import sys

from forecast import HERE, check, checked_owner
from bootstrap import wait
from chat_carry import stored
import faults
from observation import bundle, process_snapshot, terminal
from projection import carried_rows
from runtime import command, read_json, start, write_json, write_text
from terminal import keys, text, type_command


def arguments(setup, engine):
    if engine == "codex":
        return []
    result = ["--session-id", setup["session_id"]]
    if setup.get("hook_settings"):
        result += ["--settings", setup["hook_settings"]]
    return result


def usage_settings(broken_config, prefix):
    invocation = shlex.join(["pfm", "--config", str(broken_config), "usage-hook"])
    invocation += f" >{shlex.quote(str(prefix) + '.out')} 2>{shlex.quote(str(prefix) + '.err')}"
    invocation += f'; result=$?; printf "%s\\n" "$result" >{shlex.quote(str(prefix) + ".status")}; exit "$result"'
    return {"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": invocation}]}]}}


def composer(target, engine, label="actual native actor composer"):
    glyph = "Ask Codex to do anything" if engine == "codex" else "❯"
    return wait(label, lambda: glyph in terminal(target)["text"], 15)


SELECTORS = ("CLAUDE_CONFIG_DIR", "PFM_CLAUDE_CONFIG_DIR_DEFAULT")


def generated_environment(receipt):
    expected = ["/usr/lib/systemd/user-environment-generators/30-systemd-environment-d-generator"]
    if (receipt.get("argv") != expected or receipt.get("unset") != list(SELECTORS)
            or type(receipt.get("status")) is not int or receipt["status"] != 0 or receipt.get("stderr") != ""):
        raise ValueError("NATIVE-ENVIRONMENT-D-RECEIPT: completed native generator with both selectors unset required")
    selected = {}
    for word in shlex.split(receipt["stdout"]):
        name, separator, value = word.partition("=")
        if name in SELECTORS:
            if not separator or not value or name in selected:
                raise ValueError("NATIVE-ENVIRONMENT-D-ASSIGNMENT: distinct nonempty selectors required")
            selected[name] = value
    if set(selected) != set(SELECTORS) or selected[SELECTORS[0]] != selected[SELECTORS[1]]:
        raise ValueError("NATIVE-ENVIRONMENT-D-DEFAULT: both selectors must name the same actual default")
    return selected


def declared_environment(setup):
    faults.owned_path(setup["environment_receipt"])
    selected = generated_environment(read_json(setup["environment_receipt"]))
    if selected != setup["generated_environment"]:
        raise ValueError("NATIVE-ENVIRONMENT-D-CHANGED: setup differs from native receipt")
    return selected


def login_absence(rows, sid):
    return check("login-default-is-not-agent", [], [row["id"] for row in carried_rows(rows, {"session_id": sid})])


def before_prompt_row(rows, sid):
    return check("before-first-prompt-account", [{"name": "(no prompt)", "account": "2", "kind": "agent"}],
                 [{key: row[key] for key in ("name", "account", "kind")} for row in carried_rows(rows, {"session_id": sid})])


def subject_owner(subject):
    if subject.get("backend") != "pty":
        raise ValueError("NATIVE-ACTOR-TRANSPORT: independent plain PTY required")
    return checked_owner(subject)


def generated_checks(generated, child, parents):
    return [check("native-generated-child-environment", generated, {key: child.get(key) for key in SELECTORS}),
            check("native-generated-parent-environment", [generated], [{key: row.get(key) for key in SELECTORS} for row in parents])]


def preflight(setup, birth, observed, child, recorded_argv, seats, launches):
    family = {row["pid"] for row in observed["births"]}
    expected_argv = "\n".join(["claude", *arguments(setup, "claude")]) + "\n"
    return [check("native-actor-argv", expected_argv, recorded_argv),
            check("native-actor-owned-child", birth["pid"], birth["pid"] if birth["pid"] in family else None),
            check("native-actor-parent", birth["parent_pid"], child["parent"]),
            check("native-actor-no-seat", [], seats), check("native-actor-no-launch", [], launches),
            check("native-actor-multiplexer", [{} for row in observed["births"]],
                [{key: row["environment"][key] for key in ("TMUX", "TMUX_PANE", "HERDR_SOCKET_PATH", "HERDR_PANE_ID") if key in row["environment"]}
                 for row in observed["births"]])]


def held_checks(resources, environment, scenario, transcripts, gate_exists):
    return [check("native-ready-gate", resources["ready_hold"], scenario.get("ready_hold")),
            check("native-account-config-dir", resources["config_dir"], environment.get("CLAUDE_CONFIG_DIR")),
            check("native-transcript-before-first-prompt", [], transcripts),
            check("native-ready-gate-absent", False, gate_exists)]


def wrapper(setup_file, engine, scope=nullcontext):
    setup = read_json(setup_file)
    for key in ("scenario", "records", "cwd"):
        faults.owned_path(setup[key])
    binary = Path(setup["native_binary"]).resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        label = "NATIVE-CODEX-BINARY" if engine == "codex" else "NATIVE-ACTOR-BINARY"
        raise ValueError(label + ": executable required")
    environment = {**os.environ, "MOCK_ENGINE_ENGINE": engine,
                   "MOCK_ENGINE_SCENARIO": setup["scenario"]}
    if engine == "codex":
        faults.owned_path(setup["codex_home"])
        environment["CODEX_HOME"] = setup["codex_home"]
    elif "generated_environment" in setup:
        environment.update(declared_environment(setup))
    elif setup.get("config_dir"):
        faults.owned_path(setup["config_dir"], preserve=True)
        environment["CLAUDE_CONFIG_DIR"] = setup["config_dir"]
    for key in ("PFM_SEAT_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CLAUDE_CODE_SESSION_ID"):
        environment.pop(key, None)
    code = 2
    executable = str(binary) if engine == "codex" else setup["native_binary"]
    try:
        with scope(setup["prefix"]):
            with start([executable, *arguments(setup, engine)], env=environment, cwd=setup["cwd"]) as child:
                code = child.wait()
    finally:
        write_json(setup["prefix"] + ".wrapper-exit.json", {"exit_code": code})


@contextmanager
def scope(name, resources, prefix, evidence, require):
    subject = resources["subject"]
    subject_owner(subject)
    setup = {**resources, "prefix": str(prefix) + ".actor"}
    setup_file, subject_file = str(prefix) + ".actor-setup.json", str(prefix) + ".subject.json"
    write_json(setup_file, setup)
    write_json(subject_file, subject)
    records, sid = Path(resources["records"]), resources["session_id"]
    invocation = [sys.executable, str(Path(__file__).resolve()), "native-wrapper", setup_file]
    if name == "outside-login-default":
        generated = declared_environment(setup)
        invocation = ["env", *[key + "=" + generated[key] for key in SELECTORS], *invocation]
    type_command(subject, invocation)
    try:
        wait("actual outside Claude native birth", lambda: (records / "birth.json").exists(), 15)
        birth = read_json(records / "birth.json")
        observed = checked_owner(subject)
        snapshots = process_snapshot()
        child = snapshots[birth["pid"]]
        require(prefix, evidence, preflight(setup, birth, observed, child, (records / "argv").read_text(),
                stored("SELECT * FROM seat WHERE session_id = ?", (sid,)), stored("SELECT * FROM launch WHERE session_id = ?", (sid,))))
        native = next(row for row in observed["births"] if row["pid"] == birth["pid"])
        evidence["native"] = {"birth": birth, "process": child, "observed": observed}
        if name in {"outside-before-prompt", "outside-login-default"}:
            actual_scenario = read_json(resources["scenario"])
            transcripts = list((Path(resources["config_dir"]) / "projects").rglob(sid + ".jsonl"))
            require(prefix, evidence, held_checks(resources, native["environment"], actual_scenario,
                    [str(path) for path in transcripts], Path(resources["ready_hold"]).exists()))
            if name == "outside-login-default":
                parents = [row["environment"] for row in observed["births"] if row["pid"] == birth["parent_pid"]]
                require(prefix, evidence, generated_checks(generated, native["environment"], parents))
                faults.owned_path(resources["environment_d"])
                evidence["environment_d"] = {"receipt": read_json(resources["environment_receipt"]),
                    "input_path": resources["environment_d"], "input_text": Path(resources["environment_d"]).read_text()}
        else:
            composer(subject, "claude")
        active = {"engine": "claude", "records": resources["records"], "terminal": subject,
                  "session_id": sid, "target_file": subject_file}
        active_file = str(prefix) + ".active.json"
        write_json(active_file, active)
        write_json(active_file + ".target", subject)
        active["metadata_file"] = active_file
        yield active
        evidence["during"] = bundle(subject_file)
    finally:
        if name in {"outside-before-prompt", "outside-login-default"}:
            write_text(resources["ready_hold"], "reader-condition-finished\n")
        birth_file = records / "birth.json"
        if birth_file.exists() and read_json(birth_file)["pid"] in process_snapshot():
            composer(subject, "claude", "actual native actor ready for exit")
            text(subject, "/exit")
            keys(subject, ["enter"])
        try:
            wait("actual native actor exited and was reaped", lambda: Path(setup["prefix"] + ".wrapper-exit.json").exists(), 15)
            require(prefix, evidence, [check("native-actor-exit", {"exit_code": 0}, read_json(setup["prefix"] + ".wrapper-exit.json"))])
        finally:
            command([sys.executable, str(HERE / "terminal.py"), "close", subject_file])


def usage_checks(resources):
    prefix = resources["hook_prefix"]
    wait("actual registered native usage-hook completion", lambda: Path(prefix + ".status").exists(), 15)
    warning = (f"pfm: config: parse config {resources['broken_config']} at byte 1: unexpected EOF; "
               "running on defaults until it is fixed (pfm doctor)")
    stderr = Path(prefix + ".err").read_text()
    return [check("native-usage-hook-exit", "0\n", Path(prefix + ".status").read_text()),
            check("native-usage-hook-config-warning", warning, stderr.splitlines()[0] if stderr else "")]


if __name__ == "__main__":
    try:
        if len(sys.argv) != 3 or sys.argv[1] != "native-wrapper":
            raise ValueError("usage: native_actor.py native-wrapper SETUP_JSON")
        wrapper(sys.argv[2], "claude")
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(f"NATIVE-ACTOR-ERROR: {error}", file=sys.stderr)
        sys.exit(2)

