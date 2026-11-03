"""One scoped manual painter, launched from the actual removable worktree."""
import os
from pathlib import Path
from subprocess import PIPE
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from bootstrap import wait
from faults import deleted_cwd, interruptible, retired_indicator
from observation import indicator_daemon, process_cwd, process_snapshot
from runtime import command, read_json, start, write_json, write_text


def selected(pid):
    snapshot = process_snapshot()
    return indicator_daemon(snapshot={pid: snapshot[pid]} if pid in snapshot else {})


def run(setup_file):
    setup = read_json(setup_file)
    artifact = Path(os.environ["ARTIFACT"])
    pane = setup["chat"]["terminal"]["pane"]
    environment = {**os.environ, "HERDR_PANE_ID": pane}
    arguments = ["pfm", "internal", "herdr-indicators"]

    def present():
        observed = indicator_daemon()
        return observed if isinstance(observed, dict) else False

    def repaint():
        command(arguments, cwd="/", env=environment, timeout=15)

    original = wait("owned scratch indicator writer", present, 10)
    with interruptible(), retired_indicator(original, os.environ["HERDR_SOCKET_PATH"], repaint):
        child = start(arguments, cwd=setup["worktree"], env=environment, stdout=PIPE, stderr=PIPE, text=True)
        try:
            observed = wait("owned manual indicator writer", lambda: value if isinstance(value := selected(child.pid), dict) else False, 10)
            write_json(artifact / "indicator-startup.json", {"argv": arguments, "cwd": setup["worktree"],
                                                          "birth": observed, "running_cwd": process_cwd(child.pid)})
            invocation = ["bash", "-c", 'source "$1"; _attention_indicator_actions "$2"',
                          "indicator-actions", str(Path(__file__).resolve().parents[2] / "lanes/chat-attention.sh"), setup_file]
            capture = {}
            with deleted_cwd(setup["worktree"]):
                output = command(invocation, env={**os.environ, "INDICATOR_PID": str(child.pid)},
                                 allowed_codes=(0, 1, 2), capture=capture, timeout=90)
            write_text(artifact / "indicator-actions.stdout", output)
            write_text(artifact / "indicator-actions.stderr", capture["stderr"])
            if capture["status"] != 0:
                raise RuntimeError(f"ATTENTION-INDICATOR-ACTION: exit={capture['status']} artifact={artifact}")
        finally:
            if child.poll() is None:
                # The outer scope repaints after this owned temporary writer retires.
                with retired_indicator(selected(child.pid), os.environ["HERDR_SOCKET_PATH"], lambda: None, pane=pane):
                    pass
            stdout, stderr = child.communicate(timeout=15)
            write_json(artifact / "indicator-exit.json", {"status": child.returncode, "stdout": stdout, "stderr": stderr})
