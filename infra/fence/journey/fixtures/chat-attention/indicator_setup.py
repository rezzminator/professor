"""Create a real auxiliary worktree and capture its public Claude launch."""
import hashlib
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from bootstrap import wait
from chat import before, capture, fresh_shell, scenario, select
from projects import worktree
from runtime import write_json
from terminal import type_command


def prepare(branch, label, output):
    token = hashlib.sha256((os.environ["PFM_RUN"] + branch + label).encode()).hexdigest()[:12]
    folder = worktree("atlas", "attention-" + token)
    chat_branch = branch + "+worktree+claude"
    role = "forecast-indicator-" + label
    current = select(chat_branch, role)
    if current is None:
        target = fresh_shell(chat_branch, "atlas", folder)
        scenario(target, chat_branch, role, Path(__file__).resolve().parents[2] / "scenarios/default.json")
        previous = before(target)
        arguments = ["pfm", "chat", "new", "--engine", "cc", "--backend", "herdr",
                     "--account", "1", "--cwd", folder, "--name", "journey-indicator-" + token]
        type_command(target, arguments)
        current = wait("scratch worktree public Claude launch",
                       lambda: capture(target, chat_branch, role, previous), 30)
    owner = fresh_shell(chat_branch, "atlas", os.environ["HOME"])
    write_json(output, {"worktree": folder, "chat": current, "owner": owner})
