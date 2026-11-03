#!/usr/bin/env python3
"""Birth assertions read the library's bundle, never launch or query a chat."""
import json
from pathlib import Path
import sys

from observation_process import engine_births
from observation import reload_ready


def chat(bundle, carried):
    identities = {carried["session_id"], carried.get("seat_id", "")} - {""}
    matches = [item for item in bundle["chats"]
               if any(row["id"] in identities for row in item.get("rows", []))]
    if len(matches) != 1:
        raise ValueError(f"BIRTH-IDENTITY: {carried['key']}: matched {len(matches)} observations")
    return matches[0]


def composer(observed, engine):
    prefixes = {"claude": ("─", "❯"), "codex": ("›",),
                "opencode": ("FIXTURE-OPENCODE-PROMPT",)}
    lines = [line.strip() for line in observed["terminal"]["text"].splitlines()]
    return "\n".join(line for line in lines if line.startswith(prefixes[engine])) + "\n"


def births(observed):
    terminal = observed["terminal"]
    rows = list(terminal["births"])
    if isinstance(terminal.get("shield"), dict):
        rows += terminal["shield"]["births"]
    # Keep the real birth and its pane together; a changed PID alone proves nothing.
    return {"terminal": terminal["identity"],
            "processes": sorted({(row["pid"], row["start"]) for row in rows})}


def terminal_label(terminal):
    if terminal["backend"] == "pty":
        return "n/a:outside-terminal"
    return (terminal["tab"]["tab"]["label"] if terminal["backend"] == "herdr"
            else terminal["pane"]["window_name"])


def labels(observed, carried, entry, before, caller, cwd, name):
    rule = "named-label"
    if name:
        expected = name
    elif observed["terminal"]["backend"] == "tmux":
        expected = "chat"
        rule = "unnamed-inner-window"
    elif entry == "new-from-picker":
        expected = {"claude": "New Claude chat", "codex": "New Codex chat",
                    "opencode": "New OpenCode chat"}[carried["engine"]]
        rule = "picker-launch-label"
    elif carried.get("origin") == "new-typed-owner-label":
        expected = "Owner Atlas"
        rule = "typed-owner-label"
    else:
        expected = Path(cwd).name
        if entry == "open-from-foreign-workspace":
            rule = "typed-foreign-workspace-new-tab"
        else:
            rule = ("typed-default-number-to-cwd-leaf" if terminal_label(before) == str(before["tab"]["tab"]["number"])
                    else "typed-fresh-label-to-cwd-leaf")
    result = {"rule": rule, "expected": expected, "actual": terminal_label(observed["terminal"])}
    if not name and entry != "new-from-picker":
        in_place = before["backend"] == "herdr" and entry != "open-from-foreign-workspace"
        result["caller"] = {"expected": expected if in_place else terminal_label(before),
                            "actual": terminal_label(caller)}
    return result


def foreign_workspace(observed, before, caller):
    fields = ("workspace_id", "label", "active_tab_id", "tab_count", "pane_count")
    expected = {"new_tab": True, "caller_identity": before["identity"], "prompt": "journey%",
                "workspace": {key: before["workspace"]["workspace"][key] for key in fields}}
    lines = [line.strip() for line in caller["text"].splitlines() if line.strip()]
    actual = {"new_tab": observed["terminal"]["identity"]["tab"] != before["identity"]["tab"],
              "caller_identity": caller["identity"], "prompt": lines[-1] if lines else "",
              "workspace": {key: caller["workspace"]["workspace"][key] for key in fields}}
    return {"expected": expected, "actual": actual}


def preserved(bundle, keys):
    wanted = set(keys)
    rows = []
    for item in bundle["chats"]:
        identities = {row["id"] for row in item.get("rows", [])}
        selected = identities & wanted
        if not selected:
            continue
        terminal = item["terminal"]
        rows.append({"ids": sorted(selected), "identity": terminal["identity"],
                     "label": terminal_label(terminal), "text": terminal["text"],
                     "state": item["status"]["state"], "births": births(item)})
        wanted -= selected
    if wanted:
        raise ValueError("BIRTH-PRESERVATION-UNREADABLE: " + ",".join(sorted(wanted)))
    return sorted(rows, key=lambda row: row["ids"])


def main(operation, bundle_path, carry_path, *arguments):
    bundle = json.loads(Path(bundle_path).read_text())
    carry = json.loads(Path(carry_path).read_text())
    if operation == "reloaded":
        terminal = json.loads(Path(arguments[0]).read_text())
        print(json.dumps({"engine_replaced": reload_ready(bundle, terminal, carry)}, sort_keys=True))
        return
    if operation == "preserved":
        print(json.dumps(preserved(bundle, carry), sort_keys=True))
        return
    observed = chat(bundle, carry)
    if operation == "chat":
        print(json.dumps(observed, sort_keys=True))
    elif operation == "composer":
        print(composer(observed, carry["engine"]), end="")
    elif operation == "births":
        print(json.dumps(births(observed), sort_keys=True))
    elif operation == "engine-births":
        print(json.dumps(engine_births(observed, carry["engine"]), sort_keys=True))
    elif operation == "labels":
        before, caller = (json.loads(Path(path).read_text()) for path in arguments[1:3])
        print(json.dumps(labels(observed, carry, arguments[0], before, caller, *arguments[3:]), sort_keys=True))
    elif operation == "foreign":
        before, caller = (json.loads(Path(path).read_text()) for path in arguments)
        print(json.dumps(foreign_workspace(observed, before, caller), sort_keys=True))
    else:
        raise ValueError(f"BIRTH-PROJECTION-OPERATION: {operation}")


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"BIRTH-PROJECTION-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
