#!/usr/bin/env python3
"""Persistent beat state: assertions, observations and exactly one verdict."""
import csv
import fnmatch
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import sys
import uuid

import activity
from runtime import now_ms, read_json, write_json


def paths(suffix=".state.json"):
    run = Path(os.environ["JOURNEY_OUT"])
    lane = os.environ.get("JOURNEY_STATE_LANE", os.environ["JOURNEY_LANE"])
    if not re.fullmatch(r"[a-z][a-z-]+", lane):
        raise ValueError(f"STATE-LANE: {lane!r}")
    return run, run / (lane + suffix)


def save(state):
    write_json(paths()[1], state)


def load(*, allow_closed=False):
    state = read_json(paths()[1])
    if state.get("closed") and not allow_closed:
        raise ValueError("BEAT-CLOSED: no open beat")
    return state


def start(slug, *ids):
    run, path = paths()
    if path.exists():
        previous = read_json(path)
        if not previous.get("closed"):
            finish("fail", "beat left open")
    lane = os.environ["JOURNEY_LANE"]
    branch = os.environ.get("JOURNEY_BRANCH", "")
    row = lane + "/" + slug + ("@" + branch if branch else "")
    if not re.fullmatch(r"[a-z][a-z-]+/[a-z][a-z-]+(?:@[a-z][a-z0-9+-]+)?", row):
        raise ValueError(f"BEAT-NAME: {row}")
    directory = run / "beats" / row
    directory.mkdir(parents=True, exist_ok=True)
    state = {"row": row, "ids": list(ids), "start": now_ms(), "closed": False,
             "invocation": str(uuid.uuid4()), "run": os.environ["PFM_RUN"],
             "assertions": [], "goldens": [], "expected_logs": [], "errors": [],
             "source": os.environ.get("JOURNEY_SOURCE", "unknown"),
             "artifacts": str(directory), "spends": "none", "observation": None}
    trace = Path(os.environ["PFM_BEAT_FILE"])
    if not trace.is_absolute():
        raise ValueError("TRACE-PATH: PFM_BEAT_FILE must be absolute")
    trace.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
    write_json(trace, {"beat": row, "run": os.environ["PFM_RUN"]})
    try:
        state["cursor"] = activity.cursor(os.environ["JOURNEY_ACTIVITY"])
    except OSError as error:
        state["errors"].append(f"LOG-ABSENT: {error}")
    save(state)
    failed = paths(".failed")[1]
    if failed.exists():
        predecessor = failed.read_text().strip()
        evidence = run / "beats" / predecessor / "result.json"
        evidence.resolve().relative_to(run.resolve())
        retained = read_json(evidence)
        if retained["verdict"] != "fail" or retained["row"] != predecessor:
            raise ValueError(f"BLOCKED-EVIDENCE: {evidence}")
        state["blocked_evidence"] = {"result": str(evidence),
                                     "observation": retained["observation"],
                                     "action": "n/a:predecessor-failed; beat not executed"}
        save(state)
        finish("blocked", "blocked-by " + predecessor)
        return 1
    return 0


def assertion(identity, expected, actual, source="literal"):
    state = load()
    passed = expected == actual
    if source.startswith("owner-input:"):
        proof = owner_receipt(state, source.removeprefix("owner-input:"), actual)
        state.setdefault("owner_input", []).append({**proof, "expected": expected, "actual": actual,
                                                     "pass": passed})
    if source.startswith("engine-input:"):
        path = Path(source.removeprefix("engine-input:"))
        path.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve())
        recorded = path.read_text().rstrip("\n")
        if not recorded or recorded != actual:
            raise ValueError(f"ENGINE-INPUT-MISMATCH: {path}: recorded={recorded!r} actual={actual!r}")
        state.setdefault("engine_input", []).append({"path": str(path), "expected": expected,
                                                     "actual": recorded, "pass": passed})
    state["assertions"].append({"id": identity, "expected": expected,
                                "actual": actual, "source": source,
                                "pass": passed})
    save(state)
    if not passed:
        print(f"ASSERT-FAIL {state['row']} {identity}: expected={expected!r} actual={actual!r} source={source}")
        return 1
    return 0


def receipt_path(prefix, state=None):
    path = Path(prefix)
    root = Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True)
    if not path.is_absolute() or path != path.resolve():
        raise ValueError(f"OWNER-INPUT-PATH: expected canonical absolute receipt: {path}")
    path.parent.relative_to(root)
    if state is not None:
        directory = Path(state["artifacts"])
        directory.resolve(strict=True).relative_to(root)
        path.parent.relative_to(directory)
    for suffix in (".out", ".status", ".dispatch.json"):
        artifact = Path(str(path) + suffix)
        if artifact.is_symlink() or artifact.resolve() != artifact:
            raise ValueError(f"OWNER-INPUT-PATH: receipt symlink: {artifact}")
    return path


def receipt_dispatch(target, arguments, prefix, line, *, sent=False):
    path = receipt_path(prefix)
    if not sent:
        for suffix in (".out", ".status", ".dispatch.json"):
            if Path(str(path) + suffix).exists():
                raise ValueError(f"OWNER-INPUT-EXISTS: {path}{suffix}")
    if "JOURNEY_OUT" not in os.environ or not paths()[1].exists():
        return
    state = load(allow_closed=True)
    # General receipts remain usable outside beats; only beat-local receipts earn credit.
    if state.get("closed") or not path.is_relative_to(Path(state["artifacts"])):
        return
    receipt_path(prefix, state)
    if (os.environ["PFM_BEAT"], os.environ["PFM_RUN"]) != (state["row"], state["run"]):
        raise ValueError("OWNER-INPUT-BEAT: shell context differs from open beat")
    proof = {"run": os.environ["PFM_RUN"], "beat": state["row"], "invocation": state["invocation"],
             "target": target, "argv": list(arguments), "line": line, "path": str(path)}
    if sent:
        state.setdefault("dispatches", {})[str(path)] = proof
        write_json(str(path) + ".dispatch.json", proof)
        Path(str(path) + ".dispatch.json").chmod(0o400)
        save(state)


def owner_receipt(state, prefix, actual, completed_status=None, completed_output=None):
    path = receipt_path(prefix, state)
    if (os.environ["PFM_BEAT"], os.environ["PFM_RUN"]) != (state["row"], state["run"]):
        raise ValueError("OWNER-INPUT-CONTEXT: current shell differs from open beat")
    proof = read_json(str(path) + ".dispatch.json")
    if proof != state.get("dispatches", {}).get(str(path)):
        raise ValueError(f"OWNER-INPUT-DISPATCH: missing or changed dispatch: {path}")
    wanted = {"run": os.environ["PFM_RUN"], "beat": state["row"], "invocation": state["invocation"],
              "target": read_json(os.environ["JOURNEY_TARGET"]), "path": str(path)}
    if any(proof.get(key) != value for key, value in wanted.items()):
        raise ValueError(f"OWNER-INPUT-CONTEXT: expected={wanted!r} actual={proof!r}: {path}")
    argv = proof["argv"]
    if argv != ["claude", "--version"] and (len(argv) < 2 or argv[0] != "pfm" or argv[1] == "internal" or argv[1].startswith("-")):
        raise ValueError(f"OWNER-INPUT-COMMAND: expected public pfm argv or literal claude --version, actual={argv!r}: {path}")
    status = Path(str(path) + ".status").read_text()
    if not re.fullmatch(r"(?:0|[1-9][0-9]{0,2})\n", status) or int(status) > 255 or (completed_status is not None and status != completed_status):
        raise ValueError(f"OWNER-INPUT-STATUS: expected={completed_status or 'completed exit status'!r} actual={status!r}: {path}")
    digest = hashlib.sha256(Path(str(path) + ".out").read_bytes()).hexdigest()
    if completed_output is not None and digest != completed_output:
        raise ValueError(f"OWNER-INPUT-OUTPUT: expected={completed_output!r} actual={digest!r}: {path}")
    recorded = json.dumps(argv, ensure_ascii=False, separators=(",", ":"))
    if recorded != actual:
        raise ValueError(f"OWNER-INPUT-MISMATCH: expected={recorded!r} actual={actual!r}: {path}")
    return {"path": str(path), "dispatch": proof, "status": status, "output_sha256": digest}


def golden(identity, fixture, actual):
    base = Path(__file__).parent / "goldens"
    fixture_path = (base / fixture).resolve()
    fixture_path.relative_to(base.resolve())
    expected_bytes, actual_bytes = fixture_path.read_bytes(), Path(actual).read_bytes()
    state = load()
    state["goldens"].append({"path": str(fixture_path), "sha256": hashlib.sha256(expected_bytes).hexdigest()})
    save(state)
    try:
        expected, observed = expected_bytes.decode(), actual_bytes.decode()
    except UnicodeDecodeError:
        expected, observed = expected_bytes.hex(), actual_bytes.hex()
    return assertion(identity, expected, observed, str(fixture_path))


def observe(target):
    import observation
    state = load()
    try:
        result = observation.bundle(target)
        result["engine_input"] = state.get("engine_input", "n/a:pfm-action")
        result["owner_input"] = state.get("owner_input", "n/a:no-owner-receipt")
        if result.get("errors"):
            state["errors"].extend(result["errors"])
        baseline = paths()[0] / "foreign.json"
        if not baseline.exists():
            raise ValueError("FOREIGN-BASELINE-ABSENT: prelude must capture sentinel before the first action")
        wanted = read_json(baseline)
        result["foreign_unchanged"] = result["foreign"] == wanted
        if not result["foreign_unchanged"]:
            state["errors"].append(f"FOREIGN-CHANGED: expected={wanted!r} actual={result['foreign']!r}")
        if "cursor" not in state:
            raise ValueError("LOG-ABSENT: no starting offset")
        result["activity"] = activity.sweep(os.environ["JOURNEY_ACTIVITY"], state["cursor"],
                                            os.environ["PFM_RUN"], state["row"], state["expected_logs"])
        write_json(Path(state["artifacts"]) / "activity.json", result["activity"])
        state["observation"] = str(Path(state["artifacts"]) / "observation.json")
        write_json(state["observation"], result)
    except (OSError, ValueError, RuntimeError, KeyError, sqlite3.Error) as error:
        state["errors"].append(f"OBSERVE-FAILED: {error}")
    save(state)
    return 0 if state["observation"] and not state["errors"] else 1


def policy(verdict, reason, state):
    directory = Path(os.environ["JOURNEY_DESIGN"])
    if verdict == "skip-because":
        if reason not in (directory / "skip-reasons.txt").read_text().splitlines():
            raise ValueError(f"SKIP-UNLISTED: {reason}")
        return
    with (directory / "gaps.tsv").open() as source:
        rows = list(csv.DictReader(source, delimiter="\t"))
    matched = [gap for gap in rows if fnmatch.fnmatchcase(state["row"], gap.get("beat", gap.get("row", "")))]
    if verdict == "known" and not matched:
        raise ValueError(f"KNOWN-UNLISTED: {state['row']}")
    if verdict == "pass" and matched:
        raise ValueError("KNOWN-NOW-PASSES: remove its gap before accepting it")
    for gap in matched:
        if gap.get("status") == "known" or (gap.get("owner") and gap.get("expires") in {"until-product-flight", "until-owner-decision"}):
            continue  # Owner-accepted product gaps remain red until their explicit ruling changes.
        import datetime
        if not gap.get("owner") or datetime.date.fromisoformat(gap["expires"]) < datetime.date.today():
            raise ValueError("KNOWN-EXPIRED: owner and future expiry required")


def finish(verdict, detail=""):
    state = load()
    run, _ = paths()
    if verdict == "blocked" and not state["observation"] and not any(state.get(key) for key in
            ("assertions", "goldens", "dispatches", "engine_input", "owner_input", "errors", "expected_logs")):
        retained = activity.sweep(os.environ["JOURNEY_ACTIVITY"], state["cursor"],
                                  os.environ["PFM_RUN"], state["row"], [])
        if not retained.get("attributed") and not retained["unexpected"]:
            header = run / "header.json"
            read_json(header)
            state.setdefault("blocked_evidence", {"header": str(header), "reason": detail,
                "action": "n/a:blocked before input; beat not executed"})
            save(state)
    if not state.get("blocked_evidence") and not state["observation"] and os.environ.get("JOURNEY_TARGET"):
        observe(os.environ["JOURNEY_TARGET"])
        state = load()
    logs = {"records": [], "ops": [], "unexpected": [], "missing_expected": []}
    try:
        if "cursor" not in state:
            raise ValueError("LOG-ABSENT: no starting offset")
        logs = activity.sweep(os.environ["JOURNEY_ACTIVITY"], state["cursor"],
                              os.environ["PFM_RUN"], state["row"], state["expected_logs"])
        write_json(Path(state["artifacts"]) / "activity.json", logs)
        if logs["unexpected"] or logs["missing_expected"]:
            state["errors"].append(f"ACTIVITY-FAIL: unexpected={logs['unexpected']!r} missing={logs['missing_expected']!r}")
        if state["observation"]:
            bundle = read_json(state["observation"])
            bundle["activity"] = logs
            bundle["owner_input"] = state.get("owner_input", "n/a:no-owner-receipt")
            write_json(state["observation"], bundle)
    except (OSError, ValueError) as error:
        state["errors"].append(str(error))
    if verdict in {"pass", "known", "skip-because"}:
        try:
            policy(verdict, detail, state)
        except (ValueError, OSError, KeyError) as error:
            state["errors"].append(str(error))
    if verdict == "known" and not any(not row["pass"] for row in state["assertions"]):
        state["errors"].append("KNOWN-NOW-PASSES: no failed assertion proves the listed gap")
    if verdict == "pass":
        for row in state.get("owner_input", []):
            try:
                owner_receipt(state, row["path"], row["actual"], row["status"], row["output_sha256"])
            except (OSError, ValueError, KeyError) as error:
                state["errors"].append(str(error))
        asserted = {row["id"] for row in state["assertions"] if row["pass"]}
        missing = set(state["ids"]) - asserted
        if missing or not state["assertions"]:
            state["errors"].append(f"ASSERTIONS-ABSENT: {sorted(missing)}")
        if any(not row["pass"] for row in state["assertions"]):
            state["errors"].append("ASSERTIONS-FAILED")
        if not state["observation"]:
            state["errors"].append("OBSERVATION-ABSENT")
        if not logs.get("attributed") and not any(row["pass"] for row in state.get("engine_input", []) + state.get("owner_input", [])):
            state["errors"].append("ATTRIBUTION-ABSENT: no action or recorded engine/owner input carries this beat")
    if verdict != "blocked" and state["errors"]:
        verdict = "fail"
    state.update({"closed": True, "verdict": verdict, "detail": detail,
                  "end": now_ms(), "ops": logs["ops"]})
    save(state)
    counts = {identity: sum(row["id"] == identity for row in state["assertions"]) for identity in state["ids"]}
    header = read_json(run / "header.json")
    with (run / "ledger.tsv").open("a") as output:
        csv.writer(output, delimiter="\t", lineterminator="\n").writerow([
            state["row"], {"pass": "PASS", "fail": "FAIL"}.get(verdict, verdict), ",".join(sorted(counts)),
            json.dumps(state["assertions"], sort_keys=True), ",".join(logs["ops"]),
            json.dumps(state["goldens"]), state["end"] - state["start"],
            (state["start"] - header["start_ms"]) / 1000])
    write_json(Path(state["artifacts"]) / "result.json", state)
    glyph = {"pass": "✓", "fail": "✗", "known": "known", "blocked": "blocked-by", "skip-because": "skip-because"}[verdict]
    print(f"{os.environ['JOURNEY_LANE']} {glyph} {state['row']} {detail} ({state['end'] - state['start']}ms)")
    if verdict == "fail":
        marker = paths(".failed")[1]
        if not marker.exists():
            marker.write_text(state["row"] + "\n")
        print(f"FAILURE {state['row']} {state['source']}\n"
              f"expected/actual: {json.dumps(state['assertions'])}\n"
              f"errors: {state['errors']!r}\nfirst error: {logs['unexpected'][:1]!r}\n"
              f"run: {run.resolve()}\nledger: {(run / 'ledger.tsv').resolve()}\n"
              f"log: {os.environ['JOURNEY_ACTIVITY']}")
    return int(verdict in {"fail", "blocked"})


def main(operation, *args):
    if operation == "audit":
        run, path = paths()
        if not path.exists():
            raise ValueError("LANE-EMPTY: no beat was started")
        state = read_json(path)
        if not state.get("closed"):
            return finish("fail", "lane exited with an open beat")
        return int(paths(".failed")[1].exists())
    if operation == "start":
        return start(*args)
    if operation == "assert":
        return assertion(*args)
    if operation == "golden":
        return golden(*args)
    if operation == "observe":
        return observe(*args)
    if operation == "finish":
        return finish(*args)
    state = load()
    if operation == "expect-log":
        re.compile(args[0])
        state["expected_logs"].append(args[0])
    elif operation == "spends":
        state["spends"] = args[0]
    elif operation == "error":
        state["errors"].append(args[0])
    else:
        raise ValueError(f"BEAT-OPERATION: {operation}")
    save(state)
    return 0


def rejected(error):
    message = f"BEAT-ERROR: {error}"
    try:
        run, path = paths()
        if path.exists():
            state = read_json(path)
            if not state.get("closed"):
                state["errors"].append(message)
                save(state)
    except (OSError, ValueError, KeyError) as recording:
        print(f"BEAT-ERROR-RECORDING: {recording}", file=sys.stderr)
    print(message, file=sys.stderr)


if __name__ == "__main__":
    try:
        sys.exit(main(*sys.argv[1:]))
    except (ValueError, OSError, RuntimeError, KeyError) as error:
        rejected(error)
        sys.exit(2)
