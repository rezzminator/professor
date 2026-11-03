#!/usr/bin/env python3
"""Record only the native tool-shell identity and colour-control inputs."""
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from runtime import read_json, write_json

CODEX_NAMES = ("CODEX_TOOL_CALL_ID", "NO_COLOR", "PAGER", "GIT_PAGER", "GH_PAGER", "CODEX_CI",
               "CODEX_SESSION_ID", "CODEX_VERSION", "CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS")
HERDR_NAMES = ("HERDR_ENV", "HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH")
USER_COLOUR = "journey-user-colour"


def checks(caller, producer, birth, child):
    parent_env, child_env = producer["environment"], birth["environment"]
    codex = caller["engine"] == "codex"
    rows = [
        ("observed-keys", sorted(CODEX_NAMES + HERDR_NAMES), sorted(set(birth["observed_environment_keys"]) & set(CODEX_NAMES + HERDR_NAMES))),
        ("parent-tool-marker", ["CODEX_TOOL_CALL_ID"] if codex else [], sorted(set(parent_env) & {"CODEX_TOOL_CALL_ID"})),
        ("parent-colour", "1" if codex else USER_COLOUR, parent_env.get("NO_COLOR")),
        ("child-herdr-environment", [], sorted(set(child_env) & set(HERDR_NAMES))),
        ("child-codex-environment", [], sorted(set(child_env) & (set(CODEX_NAMES) if codex else {"CODEX_TOOL_CALL_ID"}))),
        ("child-own-tmux-pane", child["terminal"]["pane"], child_env.get("TMUX_PANE")),
    ]
    if codex:
        rows.extend((("parent-native-tool-keys", sorted(CODEX_NAMES), sorted(set(parent_env) & set(CODEX_NAMES))),
                     ("parent-native-session", caller["session_id"], parent_env.get("CODEX_SESSION_ID")),
                     ("parent-native-pager", "cat", parent_env.get("PAGER"))))
    else:
        rows.append(("user-colour-preserved", USER_COLOUR, child_env.get("NO_COLOR")))
    if caller["terminal"]["backend"] == "herdr":
        rows.append(("parent-own-herdr-pane", caller["terminal"]["pane"], parent_env.get("HERDR_PANE_ID")))
    return [{"field": field, "expected": expected, "actual": actual} for field, expected, actual in rows]


if __name__ == "__main__":
    if sys.argv[1] == "capture":
        names = CODEX_NAMES + HERDR_NAMES + ("PFM_RUN", "TMUX_PANE")
        write_json(sys.argv[2], {"environment": {name: os.environ[name] for name in names if name in os.environ},
                                 "shell_pid": os.getppid(), "cwd": os.getcwd()})
    elif sys.argv[1] == "checks":
        write_json(sys.argv[6], checks(*(read_json(path) for path in sys.argv[2:6])))
    else:
        raise ValueError(f"unknown tool environment operation: {sys.argv[1]}")
