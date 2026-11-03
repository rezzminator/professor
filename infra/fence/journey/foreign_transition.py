#!/usr/bin/env python3
"""Validate and adopt an explicitly proven foreign-sentinel transition.

The normal beat observer compares ``JOURNEY_OUT/foreign.json`` byte-for-byte.
Only an explicitly proven restart, uninstall, or reinstall may move that
baseline, and each transition leaves run-owned evidence before publication.
"""
import json
import os
from pathlib import Path
import sys

import beatstate
from observation import foreign_projection, process_snapshot
from observation_lifecycle import dead_births, engine_id
from observation_process import engine_births
from runtime import read_json, write_json


class TransitionError(ValueError):
    """A transition is not proven by the supplied run-owned artifacts."""


_TRANSITIONS = {"restart", "uninstall", "install"}


def _root():
    try:
        return Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True)
    except (KeyError, OSError) as error:
        raise TransitionError("FOREIGN-TRANSITION-ROOT: JOURNEY_ROOT is not a readable directory") from error


def _owned_path(value, label, *, exists):
    if not isinstance(value, (str, Path)):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-PATH: expected a path")
    path = Path(value)
    if not path.is_absolute() or path != path.resolve(strict=False):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-PATH: expected a canonical absolute path: {path}")
    try:
        path.relative_to(_root())
    except ValueError as error:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-PATH: outside JOURNEY_ROOT: {path}") from error
    if exists:
        if path.is_symlink() or not path.is_file():
            raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-ARTIFACT: expected a regular file: {path}")
    elif path.exists() or path.is_symlink():
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-EXISTS: refusing to replace {path}")
    return path


def _artifact(value, label):
    path = _owned_path(value, label, exists=True)
    try:
        result = read_json(path)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-JSON: {path}: {error}") from error
    if not isinstance(result, dict):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-SHAPE: expected an object: {path}")
    return path, result


def _clean(bundle, label):
    errors = bundle.get("errors")
    if errors != []:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-ERRORS: {errors!r}")
    activity = bundle.get("activity")
    if isinstance(activity, dict) and (activity.get("unexpected") or activity.get("missing_expected")):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-ACTIVITY: {activity!r}")


def _projection(bundle, label):
    value = bundle.get("foreign")
    if not isinstance(value, dict) or "error" in value:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-FOREIGN: invalid projection: {value!r}")
    full = bundle.get("foreign_full")
    if not isinstance(full, dict):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-FOREIGN: full observation is required")
    try:
        expected = foreign_projection(full)
    except (KeyError, TypeError, ValueError) as error:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-FOREIGN: cannot project full observation: {error}") from error
    if value != expected:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-FOREIGN: projection differs from full observation")
    return value


def _full(bundle, label):
    value = bundle.get("foreign_full")
    if not isinstance(value, dict) or "error" in value:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-FULL: invalid observation: {value!r}")
    return value


def _required(value, name):
    if value is None or value == "" or (isinstance(value, str) and value.startswith("n/a:")):
        raise TransitionError(f"FOREIGN-TRANSITION-IDENTITY: missing {name}")
    return value


def _status(full, label):
    status = full.get("status")
    if not isinstance(status, dict):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-STATUS: expected a live status object")
    return status


def _matching_rows(full, session_id):
    rows = full.get("rows", [])
    if not isinstance(rows, list):
        return []
    return [row for row in rows if isinstance(row, dict) and row.get("id") == session_id]


def _value_candidates(values, name):
    candidates = [value for value in values if value is not None and value != "" and not (isinstance(value, str) and value.startswith("n/a:"))]
    if not candidates:
        raise TransitionError(f"FOREIGN-TRANSITION-IDENTITY: missing {name}")
    if any(value != candidates[0] for value in candidates[1:]):
        raise TransitionError(f"FOREIGN-TRANSITION-IDENTITY: conflicting {name}: {candidates!r}")
    return candidates[0]


def _identity(full, label):
    status = _status(full, label)
    session_id = _required(status.get("session_id"), "session_id")
    rows = _matching_rows(full, session_id)
    if not rows:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-IDENTITY: session row absent: {session_id}")
    picker = full.get("picker", [])
    picker_rows = []
    if isinstance(picker, list):
        identified = [row for row in picker if isinstance(row, dict) and row.get("id", row.get("session_id")) == session_id]
        picker_rows = identified if identified else picker if len(picker) == 1 else []
    stored = full.get("stored")
    if not isinstance(stored, dict):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-STORED: expected an object")
    launches = stored.get("launch", []) if isinstance(stored.get("launch", []), list) else []
    launch_rows = [row for row in launches if isinstance(row, dict) and row.get("session_id") == session_id]
    chats = stored.get("chat", []) if isinstance(stored.get("chat", []), list) else []
    chat_rows = [row for row in chats if isinstance(row, dict) and row.get("uuid") == session_id]
    accounts = [status.get("account"), *[row.get("account") for row in rows],
                *[row.get("account") for row in picker_rows], *[row.get("account") for row in launch_rows]]
    normalized_accounts = []
    for account in accounts:
        if isinstance(account, str) and account.isdecimal():
            account = int(account)
        normalized_accounts.append(account)
    account = _value_candidates(normalized_accounts, "account")
    cwd = _value_candidates([status.get("cwd"), *[row.get("cwd") for row in rows], *[row.get("cwd") for row in chat_rows]], "cwd")
    model = _value_candidates([status.get("model"), status.get("model_name")], "model")
    engines = [engine_id(value) if isinstance(value, str) else value for value in
               [status.get("engine"), *[row.get("engine") for row in picker_rows], *[row.get("engine") for row in launch_rows]]]
    engine = _value_candidates(engines, "engine")
    transcript = stored.get("transcript")
    if transcript is None or (isinstance(transcript, str) and transcript.startswith("n/a:")):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-TRANSCRIPT: missing actual transcript projection")
    terminal = full.get("terminal")
    if not isinstance(terminal, dict) or not isinstance(terminal.get("identity"), dict):
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-TERMINAL: missing actual terminal identity")
    labels = terminal.get("labels")
    if not isinstance(labels, dict):
        backend = terminal.get("backend")
        if backend == "herdr":
            try:
                labels = {"workspace": terminal["workspace"]["workspace"]["label"],
                          "tab": terminal["tab"]["tab"]["label"]}
            except (KeyError, TypeError) as error:
                raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-LABELS: unavailable") from error
        elif backend == "tmux":
            try:
                labels = {"workspace": "n/a:tmux", "tab": terminal["pane"]["window_name"]}
            except (KeyError, TypeError) as error:
                raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-LABELS: unavailable") from error
        else:
            raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-LABELS: unavailable")
    invalid_labels = [value for key, value in labels.items()
                      if value is None or value == ""
                      or (isinstance(value, str) and value.startswith("n/a:")
                          and not (terminal.get("backend") == "tmux" and key == "workspace" and value == "n/a:tmux"))]
    if invalid_labels:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-LABELS: incomplete: {labels!r}")
    return {"session_id": session_id, "account": account, "cwd": cwd, "model": model,
            "engine": engine, "transcript": transcript, "labels": labels,
            "terminal": terminal["identity"]}


def _births(full, identity, label):
    try:
        births = engine_births(full, full.get("status", {}).get("engine", identity["engine"]))
    except (KeyError, TypeError, ValueError) as error:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-BIRTHS: unreadable: {error}") from error
    return births


def _pairs(births):
    if not isinstance(births, list):
        raise TransitionError("FOREIGN-TRANSITION-BIRTHS: receipt births must be a list")
    try:
        return sorted((birth["pid"], birth["start"]) for birth in births)
    except (KeyError, TypeError) as error:
        raise TransitionError("FOREIGN-TRANSITION-BIRTHS: malformed receipt birth") from error


def _receipt(value):
    _, receipt = _artifact(value, "receipt")
    return receipt


def _dead_engine_births(births, label):
    if not births:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-BIRTHS: actual prior engine births are required")
    prior = [{"pid": birth.get("pid"), "start": birth.get("start")} for birth in births]
    try:
        return dead_births({"session_id": label, "expected_prior_births": prior}, process_snapshot())
    except (KeyError, TypeError, ValueError, RuntimeError, OSError) as error:
        raise TransitionError(f"FOREIGN-TRANSITION-{label.upper()}-BIRTHS: prior engine birth is not proven dead: {error}") from error


def _check_rebind(receipt, before, after, before_identity, after_identity, old_births, new_births):
    if receipt.get("kind") != "restart":
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: kind is not restart")
    if receipt.get("session_id") != before_identity["session_id"] or receipt.get("session_id") != after_identity["session_id"]:
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: session identity does not match both observations")
    if receipt.get("rebound") is not True or receipt.get("old_births_gone") is not True:
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: explicit rebound and prior-birth death proof are required")
    if receipt.get("before_terminal") != before_identity["terminal"] or receipt.get("after_terminal") != after_identity["terminal"]:
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: terminal rebind does not match observations")
    if _pairs(receipt.get("old_births", [])) != _pairs(old_births):
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: old births do not match the before observation")
    if _pairs(receipt.get("new_births", [])) != _pairs(new_births):
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: new births do not match the after observation")
    if not old_births or not new_births:
        raise TransitionError("FOREIGN-TRANSITION-BIRTHS: restart needs actual prior and successor engine births")
    if set(_pairs(old_births)) & set(_pairs(new_births)):
        raise TransitionError("FOREIGN-TRANSITION-BIRTHS: a prior engine birth is still live or was reused")
    _dead_engine_births(old_births, "restart")
    if before_identity["labels"] != after_identity["labels"]:
        raise TransitionError("FOREIGN-TRANSITION-LABELS: restart changed the foreign sentinel labels")
    if _status(before, "before").get("state") not in {"idle", "working", "blocked"} or _status(after, "after").get("state") not in {"idle", "working", "blocked"}:
        raise TransitionError("FOREIGN-TRANSITION-STATE: restart requires a live foreign sentinel")


def _ended_proof(full, receipt, identity):
    lifecycle = full.get("lifecycle")
    ended = lifecycle.get("ended") if isinstance(lifecycle, dict) else None
    proof = receipt.get("ended_proof")
    if not isinstance(ended, dict) or not isinstance(proof, dict):
        raise TransitionError("FOREIGN-TRANSITION-ENDED: actual ended proof is required in the observation and receipt")
    if proof.get("session_id") != identity["session_id"]:
        raise TransitionError("FOREIGN-TRANSITION-ENDED: receipt session identity does not match")
    if proof.get("state") not in {"dead", "ended", "n/a:uninstalled"}:
        raise TransitionError("FOREIGN-TRANSITION-ENDED: receipt does not prove a terminal state")
    if proof.get("births") != ended.get("births"):
        raise TransitionError("FOREIGN-TRANSITION-ENDED: receipt and observation death proofs differ")
    births = proof.get("births")
    if not isinstance(births, list):
        raise TransitionError("FOREIGN-TRANSITION-ENDED: births proof must be a list")
    for birth in births:
        if not isinstance(birth, dict) or not isinstance(birth.get("prior"), dict):
            raise TransitionError("FOREIGN-TRANSITION-ENDED: malformed prior birth proof")
        current = birth.get("current")
        if current != "n/a:process-absent" and current != "n/a:ended" and current != "n/a:uninstalled":
            if not isinstance(current, dict) or (current.get("pid"), current.get("start")) == (birth["prior"].get("pid"), birth["prior"].get("start")):
                raise TransitionError("FOREIGN-TRANSITION-ENDED: prior birth is not proven dead or replaced")


def _check_inapplicable(full, receipt):
    fields = {key: full.get(key) for key in ("status", "rows", "picker", "terminal")}
    actual = sorted(key for key, value in fields.items() if isinstance(value, str) and value.startswith("n/a:"))
    proof = receipt.get("inapplicable")
    if not isinstance(proof, dict) or sorted(proof) != actual:
        raise TransitionError(f"FOREIGN-TRANSITION-INAPPLICABLE: expected proof for {actual!r}, actual={proof!r}")
    for key in actual:
        reason = proof[key]
        if key in {"status", "rows", "picker"} and reason != "uninstalled":
            raise TransitionError(f"FOREIGN-TRANSITION-INAPPLICABLE: {key} lacks uninstalled proof")
        if key == "terminal" and reason not in {"ended", "uninstalled"}:
            raise TransitionError("FOREIGN-TRANSITION-INAPPLICABLE: terminal lacks ended/uninstalled proof")


def _check_uninstall(receipt, before, after, before_identity):
    if receipt.get("kind") != "uninstall":
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: kind is not uninstall")
    before_machine, after_machine = before.get("machine"), after.get("machine")
    if not isinstance(before_machine, dict) or before_machine.get("phase") != "installed":
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: before is not explicitly installed")
    if not isinstance(after_machine, dict) or after_machine.get("phase") != "uninstalled":
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: after is not explicitly uninstalled")
    if after_machine.get("managed_files") != {} or after_machine.get("units") != []:
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: managed installation is still present")
    binary_absent = after_machine.get("binary") == "absent"
    if receipt.get("session_id") != before_identity["session_id"] or receipt.get("ended") is not True:
        raise TransitionError("FOREIGN-TRANSITION-ENDED: explicit ended proof is required for the sentinel")
    before_full = _full(before, "before")
    state = _status(before_full, "before").get("state")
    if state not in {"idle", "working", "blocked", "dead"}:
        raise TransitionError("FOREIGN-TRANSITION-STATE: uninstall requires an observed live or ended sentinel")
    after_full = _full(after, "after")
    old_births = _births(_full(before, "before"), before_identity, "before")
    if state == "dead":
        _ended_proof(before_full, receipt, before_identity)
        old_births = [row["prior"] for row in before_full["lifecycle"]["ended"]["births"]]
    _dead_engine_births(old_births, "uninstall")
    _ended_proof(after_full, receipt, before_identity)
    if binary_absent:
        _check_inapplicable(after_full, receipt)
        return
    if any(isinstance(after_full.get(key), str) and after_full[key].startswith("n/a:")
           for key in ("status", "rows", "picker", "terminal")):
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: reports may be n/a only after the binary is absent")
    after_identity = _identity(after_full, "after")
    for key in ("session_id", "account", "cwd", "model", "engine", "transcript", "labels"):
        if before_identity[key] != after_identity[key]:
            raise TransitionError(f"FOREIGN-TRANSITION-IDENTITY: {key} changed across uninstall")
    if _status(after_full, "after").get("state") != "dead":
        raise TransitionError("FOREIGN-TRANSITION-ENDED: retained binary must report the foreign sentinel dead")


def _check_install(receipt, before, after):
    if receipt.get("kind") != "install":
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: kind is not install")
    if receipt.get("argv") != ["pfm", "install", "--yes"]:
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: typed public install argv is required")
    if receipt.get("status") not in {0, "0", "0\n"} or not isinstance(receipt.get("out"), str) or not receipt["out"].strip():
        raise TransitionError("FOREIGN-TRANSITION-RECEIPT: successful typed install status/out are required")
    before_machine, after_machine = before.get("machine"), after.get("machine")
    if not isinstance(before_machine, dict) or before_machine.get("phase") != "uninstalled":
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: install requires an uninstalled machine before setup")
    if before_machine.get("managed_files") != {} or before_machine.get("units") != []:
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: install requires retired managed assets before setup")
    if not isinstance(after_machine, dict) or after_machine.get("phase") != "installed":
        raise TransitionError("FOREIGN-TRANSITION-MACHINE: successful install projection is absent")
    before_full, after_full = _full(before, "before"), _full(after, "after")
    session_id = receipt.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        raise TransitionError("FOREIGN-TRANSITION-IDENTITY: install session_id is required")
    ended = {"session_id": session_id}
    if not isinstance(receipt.get("ended_proof"), dict):
        raise TransitionError("FOREIGN-TRANSITION-ENDED: ended foreign proof is required on both sides of install")
    ended_proof = receipt["ended_proof"]
    if before_machine.get("binary") == "absent":
        _check_inapplicable(before_full, receipt)
    elif _status(before_full, "before").get("state") != "dead":
        raise TransitionError("FOREIGN-TRANSITION-ENDED: retained binary must report the sentinel dead")
    _ended_proof(before_full, {"ended_proof": ended_proof}, ended)
    _ended_proof(after_full, {"ended_proof": ended_proof}, ended)
    prior = before_full.get("lifecycle", {}).get("ended", {}).get("births")
    if not isinstance(prior, list):
        raise TransitionError("FOREIGN-TRANSITION-ENDED: before ended engine births are required")
    _dead_engine_births([row["prior"] for row in prior if isinstance(row, dict) and isinstance(row.get("prior"), dict)], "install")
    if before_full.get("stored") != after_full.get("stored"):
        raise TransitionError("FOREIGN-TRANSITION-IDENTITY: foreign stored state changed across install")
    if any(not isinstance(after_full.get(key), expected) for key, expected in
           (("status", dict), ("rows", list), ("picker", list), ("terminal", dict))):
        raise TransitionError("FOREIGN-TRANSITION-FOREIGN: install must restore actual foreign reports")
    after_identity = _identity(after_full, "after")
    if after_identity["session_id"] != session_id or _status(after_full, "after").get("state") != "dead":
        raise TransitionError("FOREIGN-TRANSITION-ENDED: installed foreign sentinel identity/state is not actual")
    return session_id


def _beat_clean(prelude=False):
    try:
        state = beatstate.load(allow_closed=prelude)
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise TransitionError(f"FOREIGN-TRANSITION-BEAT: current beat is unreadable: {error}") from error
    if state.get("closed") and state.get("verdict") != "pass":
        raise TransitionError("FOREIGN-TRANSITION-BEAT: prelude requires a passing prior beat")
    if state.get("errors") != []:
        raise TransitionError(f"FOREIGN-TRANSITION-BEAT: current beat has errors: {state.get('errors')!r}")
    assertions = state.get("assertions")
    if not isinstance(assertions, list) or not assertions or any(
            not isinstance(row, dict) or row.get("pass") is not True for row in assertions):
        raise TransitionError("FOREIGN-TRANSITION-BEAT: current beat has failed, missing, or malformed assertions")


def validate_transition(kind, before_path, after_path, receipt_path, baseline_path=None):
    """Return a validated adoption record without mutating the baseline."""
    if kind not in _TRANSITIONS:
        raise TransitionError(f"FOREIGN-TRANSITION-KIND: expected restart|uninstall|install, got {kind!r}")
    before_file, before = _artifact(before_path, "before")
    after_file, after = _artifact(after_path, "after")
    if before_file == after_file:
        raise TransitionError("FOREIGN-TRANSITION-ARTIFACT: before and after must be distinct files")
    receipt = _receipt(receipt_path)
    _clean(before, "before")
    _clean(after, "after")
    before_projection = _projection(before, "before")
    after_projection = _projection(after, "after")
    baseline = baseline_path or Path(os.environ.get("JOURNEY_OUT", _root())) / "foreign.json"
    baseline_file, old_baseline = _artifact(baseline, "baseline")
    if old_baseline != before_projection:
        raise TransitionError("FOREIGN-TRANSITION-BASELINE: old baseline is not the before foreign projection")
    before_full, after_full = _full(before, "before"), _full(after, "after")
    before_identity = _identity(before_full, "before") if kind != "install" else None
    if kind == "restart":
        after_identity = _identity(after_full, "after")
        for key in ("session_id", "account", "cwd", "model", "engine", "transcript", "labels"):
            if before_identity[key] != after_identity[key]:
                raise TransitionError(f"FOREIGN-TRANSITION-IDENTITY: {key} changed across restart")
        _check_rebind(receipt, before_full, after_full, before_identity, after_identity,
                      _births(before_full, before_identity, "before"), _births(after_full, after_identity, "after"))
        session_id = before_identity["session_id"]
    elif kind == "uninstall":
        _check_uninstall(receipt, before, after, before_identity)
        session_id = before_identity["session_id"]
    else:
        session_id = _check_install(receipt, before, after)
    return {"schema": 1, "kind": kind, "before": str(before_file), "after": str(after_file),
            "baseline": str(baseline_file), "session_id": session_id,
            "old_baseline": old_baseline, "before_foreign": before_projection,
            "after_foreign": after_projection, "receipt": receipt}


def adopt_transition(kind, before_path, after_path, receipt_path, baseline_path=None, evidence_path=None):
    """Validate a transition, preserve its old evidence, then publish its baseline."""
    record = validate_transition(kind, before_path, after_path, receipt_path, baseline_path)
    _beat_clean(prelude=kind == "install")
    evidence = evidence_path or Path(record["after"]).with_name("foreign-transition.json")
    evidence_file = _owned_path(evidence, "evidence", exists=False)
    try:
        write_json(evidence_file, record)
        write_json(record["baseline"], record["after_foreign"])
    except (OSError, ValueError, RuntimeError) as error:
        if evidence_file.exists():
            evidence_file.unlink()
        raise TransitionError(f"FOREIGN-TRANSITION-PUBLISH: refused without changing baseline/evidence: {error}") from error
    return {"kind": kind, "baseline": record["baseline"], "evidence": str(evidence_file),
            "session_id": record["session_id"], "foreign": record["after_foreign"]}


def main(arguments):
    if len(arguments) not in {5, 6, 7} or arguments[0] != "adopt":
        raise TransitionError("usage: foreign_transition.py adopt KIND BEFORE AFTER RECEIPT [BASELINE [EVIDENCE]]")
    result = adopt_transition(*arguments[1:])
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (OSError, ValueError, RuntimeError, KeyError, TypeError) as error:
        print(f"FOREIGN-TRANSITION: {error}", file=sys.stderr)
        sys.exit(2)
