#!/usr/bin/env python3
"""Disjoint NEED fixtures for Herdr placement; reads never reconcile a branch."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys

from bootstrap import wait, workspace
import herdr
from runtime import read_json, write_json

STATES = {"named", "misnamed-exclusive", "mixed"}


def identity(project, branch, state):
    if project not in {"atlas", "beacon", "cobalt"} or not branch or state not in STATES:
        raise ValueError("WORKSPACE-INPUT: expected fixture project, nonempty branch and named|misnamed-exclusive|mixed")
    return project + ":" + branch + ":" + state


def private_root():
    root = Path(os.environ["JOURNEY_ROOT"]).resolve()
    if os.environ.get("PFM_DEV_FENCE") != "1" or root == Path("/") or Path(os.environ["HOME"]).resolve() != root / "home":
        raise ValueError("WORKSPACE-FENCE: requires the private journey HOME inside the fence")
    Path(os.environ["JOURNEY_CARRY"]).resolve().relative_to(root)
    Path(os.environ["HERDR_SOCKET_PATH"]).resolve().relative_to(root)
    return root


def read(project, branch, state):
    private_root()
    key = identity(project, branch, state)
    saved = read_json(os.environ["JOURNEY_CARRY"]).get("workspace_branches", {}).get(key)
    if saved is None:
        return None
    socket = os.environ["HERDR_SOCKET_PATH"]
    listed = herdr.request(socket, "workspace.list", {})["result"]["workspaces"]
    matches = [item for item in listed if item["workspace_id"] == saved["id"]]
    if len(matches) > 1:
        raise ValueError("WORKSPACE-AMBIGUOUS: " + saved["id"])
    panes = herdr.request(socket, "pane.list", {})["result"]["panes"]
    tabs = herdr.request(socket, "tab.list", {"workspace_id": saved["id"]})["result"]["tabs"] if matches else []
    return {**saved, "present": bool(matches), "label": matches[0]["label"] if matches else "n/a:closed",
            "workspace": matches[0] if matches else "n/a:closed", "tabs": tabs,
            "panes": [pane for pane in panes if pane["workspace_id"] == saved["id"]]}


def need(project, branch, state, cwd=None):
    root = private_root()
    key = identity(project, branch, state)
    source = root / "home/work" / project
    requested = str(Path(cwd or source).resolve())
    current = read(project, branch, state)
    if current is not None:
        if current["source_cwd"] != requested:
            raise ValueError("WORKSPACE-CWD-CONFLICT: " + key)
        return current
    if not (source / ".git").is_dir():
        raise ValueError("WORKSPACE-PROJECT-ABSENT: " + project)
    token = hashlib.sha256(key.encode()).hexdigest()[:12]
    physical = project + "-journey-" + token
    folder = source.parent / physical
    if folder.exists():
        raise ValueError("WORKSPACE-PROJECT-OCCUPIED: " + str(folder))
    start = Path(requested)
    if start == root / "home":
        launch = start
    else:
        try:
            launch = folder / start.relative_to(source)
        except ValueError as error:
            raise ValueError("WORKSPACE-CWD: expected HOME or a directory in the source project") from error
        if not start.is_dir():
            raise ValueError("WORKSPACE-CWD-ABSENT: " + str(start))
    # A distinct repository label is required: the carried atlas label otherwise wins before exclusivity.
    shutil.copytree(source, folder, ignore=shutil.ignore_patterns(".worktrees", "plugins", "bench", ".claude", ".codex"))
    label = physical if state == "named" else physical + "-" + state
    created = workspace(label, str(folder))
    target = {"backend": "herdr", "pane": created["pane"]}
    if launch != folder:
        result = herdr.create_tab(created["id"], launch)
        target = {"backend": "herdr", "pane": result["root_pane"]["pane_id"]}
    saved = {**created, "key": key, "source_project": project, "project": physical, "branch": branch, "state": state,
             "cwd": str(folder), "source_cwd": requested, "start_cwd": str(launch), "pane": target["pane"], "target": target,
             "project_pane": created["pane"], "setup_label": label}
    if state == "mixed":
        foreign = "beacon" if project != "beacon" else "cobalt"
        foreign_cwd = source.parent / foreign
        if not (foreign_cwd / ".git").is_dir():
            raise ValueError("WORKSPACE-PROJECT-ABSENT: " + foreign)
        result = herdr.create_tab(created["id"], foreign_cwd)
        saved.update(foreign_pane=result["root_pane"]["pane_id"], foreign_cwd=str(foreign_cwd))
    declared = read_json(os.environ["JOURNEY_CARRY"])
    declared.setdefault("workspace_branches", {})[key] = saved
    write_json(os.environ["JOURNEY_CARRY"], declared)

    def settled():
        observed = read(project, branch, state)
        expected = {saved["project_pane"]: saved["cwd"], saved["pane"]: saved["start_cwd"]}
        if state == "mixed":
            expected[saved["foreign_pane"]] = saved["foreign_cwd"]
        actual = {pane["pane_id"]: pane["foreground_cwd"] for pane in observed["panes"]}
        return observed if all(actual.get(pane) == path for pane, path in expected.items()) else False
    return wait("disjoint workspace " + key, settled, 20)


def main(operation, project, branch, state, *args):
    if operation not in {"need", "read"} or len(args) > (1 if operation == "need" else 0):
        raise ValueError("usage: workspace.py need|read PROJECT BRANCH STATE [CWD]")
    result = need(project, branch, state, *args) if operation == "need" else read(project, branch, state)
    if result is None:
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(*sys.argv[1:]))
    except (OSError, ValueError, RuntimeError, KeyError, TypeError) as error:
        print(f"WORKSPACE-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
