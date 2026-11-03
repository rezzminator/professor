#!/usr/bin/env python3
"""Read the installed machine's teardown outputs without consuming them."""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
from runpy import run_path
import sys

from runtime import read_json, write_json
from observation import report
from foreign_transition import adopt_transition


def crumbs(text):
    matches = re.findall(r"^doctor: crumbs entries=(\d+) invalid=(\d+)$", text, re.MULTILINE)
    if len(matches) != 1:
        raise ValueError(f"DOCTOR-CRUMBS: expected one audit row, got {len(matches)}")
    return {"invalid": int(matches[0][1])}


def entries(root):
    if not root.is_dir():
        raise ValueError(f"UNINSTALL-HOME-UNREADABLE: {root}")
    return sorted(str(path.relative_to(root)) for path in root.rglob("*"))


def foreign(root, baseline):
    if baseline["version"] != 1:
        raise ValueError("UNINSTALL-BASELINE-VERSION: expected 1")
    entry = run_path(str(Path(__file__).resolve().parents[2] / "root-baseline.py"))["entry"]
    result = {}
    for group, base in (("files", root), ("projects", root / "work")):
        result[group] = {}
        for relative in baseline[group]:
            path = base / relative
            path.parent.resolve().relative_to(base.resolve())
            if not path.exists() and not path.is_symlink():
                raise ValueError(f"UNINSTALL-FOREIGN-MISSING: {relative}")
            result[group][relative] = entry(path)
    return result


def relocated_baseline(baseline, root):
    old = baseline["root"]
    if not Path(old).is_absolute() or not root.is_absolute():
        raise ValueError("UNINSTALL-BASELINE-ROOT: absolute source and destination required")
    result = {group: {} for group in ("files", "projects")}
    for group in result:
        for name, original in baseline[group].items():
            value = dict(original)
            if value["kind"] == "file":
                raw = base64.b64decode(value["base64"], validate=True)
                if hashlib.sha256(raw).hexdigest() != value["sha256"]:
                    raise ValueError(f"UNINSTALL-BASELINE-HASH: {group}/{name}")
                raw = raw.replace(old.encode(), str(root).encode())
                value.update(base64=base64.b64encode(raw).decode(), sha256=hashlib.sha256(raw).hexdigest())
            elif value["kind"] == "symlink":
                value["target"] = value["target"].replace(old, str(root))
            result[group][name] = value
    return result


def settings(path):
    # The parser proves validity separately from the byte-for-byte root comparison.
    value = json.loads(path.read_bytes())
    if not isinstance(value, dict):
        raise ValueError(f"UNINSTALL-SETTINGS: expected JSON object: {path}")
    return {"type": "object", "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def fleet_ended(carry):
    states = []
    for chat in [*carry["chats"], carry["foreign"]]:
        if chat.get("expect", {}).get("lifecycle") == "archived":
            continue
        status = json.loads(report(["pfm", "chat", "status", chat["session_id"], "--json"], allowed_codes=(0, 3)))
        if status["state"] == "not-found":
            raise ValueError(f"FINAL-CHAT-MISSING: {chat['name']} {chat['session_id']}")
        states.append(status["state"])
    return all(state == "dead" for state in states)


def adopt_lifecycle(kind, before_path, after_path, command_prefix=None):
    before, after = read_json(before_path), read_json(after_path)
    foreign = after["foreign_full"]
    births = foreign["lifecycle"]["ended"]["births"]
    session = (after if kind == "install" else before)["foreign_full"]["status"]["session_id"]
    unavailable = before["foreign_full"] if kind == "install" else foreign
    receipt = str(Path(after_path).with_name(kind + "-transition-receipt.json"))
    value = {"kind": kind, "session_id": session,
        "ended": bool(births) and all(row["current"] != row["prior"] for row in births),
        "ended_proof": {"session_id": session, "state": foreign["status"] if isinstance(foreign["status"], str) else foreign["status"]["state"], "births": births},
        "inapplicable": {key: "ended" if key == "terminal" else "uninstalled" for key in ("status", "rows", "picker", "terminal")
                         if isinstance(unavailable.get(key), str) and unavailable[key].startswith("n/a:")}}
    if kind == "install":
        value.update(argv=read_json(command_prefix + ".dispatch.json")["argv"],
                     status=Path(command_prefix + ".status").read_text(),
                     out=Path(command_prefix + ".out").read_text())
    write_json(receipt, value)
    return adopt_transition(kind, before_path, after_path, receipt,
                            evidence_path=str(Path(after_path).with_name(kind + "-foreign-transition.json")))


if __name__ == "__main__":
    try:
        mode, source, output, *extra = sys.argv[1:]
        if mode == "crumbs":
            value = crumbs(Path(source).read_text())
        elif mode == "entries":
            value = entries(Path(source))
        elif mode == "settings":
            value = settings(Path(source))
        elif mode == "foreign":
            value = foreign(Path(os.environ["HOME"]), read_json(source))
        elif mode == "baseline":
            value = relocated_baseline(read_json(source), Path(os.environ["JOURNEY_ROOT"]))
        elif mode == "ended":
            sys.exit(0 if fleet_ended(read_json(source)) else 1)
        elif mode in {"uninstall-transition", "install-transition"}:
            print(json.dumps(adopt_lifecycle(mode.removesuffix("-transition"), source, output, *extra)))
            sys.exit(0)
        elif mode == "managed":
            base = Path(source)
            if not base.is_dir():
                raise ValueError(f"FINAL-MANAGED-MISSING: {base}")
            value = [str(path) for path in base.rglob("*") if path.is_file() or path.is_symlink()]
        elif mode == "remaining":
            value = [path for path in read_json(source) if Path(path).exists() or Path(path).is_symlink()]
        elif mode in {"installed", "uninstalled"}:
            value = read_json(source)
            value["phase"] = mode
        else:
            raise ValueError(f"UNINSTALL-PROJECTION-MODE: {mode}")
        write_json(output, value)
    except (OSError, ValueError, KeyError) as error:
        print(f"UNINSTALL-PROJECTION: {error}", file=sys.stderr)
        sys.exit(2)
