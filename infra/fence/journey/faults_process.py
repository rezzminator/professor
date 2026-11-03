"""Private process lifecycle faults; callers use the faults public door."""
from contextlib import contextmanager
from copy import deepcopy
import os
import signal

from observation import process_snapshot, process_state
from runtime import write_json


def validate_target_birth(target, birth):
    """Return the owned terminal and process only when its exact birth is present."""
    from faults import checked_birth, owned_target
    observed = owned_target(target)
    process = checked_birth(birth)
    anchors = {row["pid"] for row in observed["births"]}
    family = process_snapshot()
    ancestor, seen = process["pid"], set()
    while ancestor not in anchors and ancestor > 1 and ancestor not in seen:
        seen.add(ancestor)
        ancestor = family.get(ancestor, {}).get("parent", 0)
    supervisors = {node["pid"] for row in observed["births"] for node in row["chain"]}
    if ancestor not in anchors and birth["pid"] not in supervisors:
        raise ValueError(f"FAULT-PROCESS-NOT-IN-TARGET: {birth['pid']}")
    return observed, process


@contextmanager
def stopped_for_move(target, birth, move, restore, *, receipt=None):
    """Stop one owned birth, move its pane, kill that same birth, then reopen.

    The move callback is the owner’s native pane operation. The returned target
    must still observe the original birth before this door sends SIGKILL.
    """
    from bootstrap import wait
    from faults import birth_exited, checked_birth, owned_path, signal_birth
    if not callable(move):
        raise ValueError("FAULT-MOVE: native move callback required")
    if not callable(restore):
        raise ValueError("FAULT-DEATH-RESTORE: reopen callback required")
    if receipt is not None:
        receipt = owned_path(receipt, preserve=True)
        if receipt.exists() or receipt.is_symlink():
            raise ValueError(f"FAULT-MOVE-RECEIPT-EXISTS: {receipt}")
    source = deepcopy(target)
    before, process = validate_target_birth(target, birth)
    birth = {key: process[key] for key in ("pid", "start")}
    stopped = False
    moved = None
    try:
        signal_birth(birth, signal.SIGSTOP)
        stopped = True
        def paused():
            checked_birth(birth)
            return process_state(birth["pid"])["state"] == "T"
        wait("stopped process " + str(birth["pid"]), paused)
        moved = move(target)
        if moved is None:
            raise ValueError("FAULT-MOVE: callback returned no target")
        prekill, process = validate_target_birth(moved, birth)
        if process["state"] != "T":
            raise ValueError(f"FAULT-MOVE-NOT-STOPPED: {birth}; state={process['state']!r}")
        signal_birth(birth, signal.SIGKILL)
        wait("moved process exit " + str(birth["pid"]), lambda: birth_exited(birth))
        if receipt is not None:
            write_json(receipt, {"run": os.environ["PFM_RUN"], "source": source, "target": moved,
                                "supervisor": birth, "before": before, "prekill": prekill})
        yield moved
    finally:
        try:
            if stopped and not birth_exited(birth):
                signal_birth(birth, signal.SIGCONT)
        finally:
            restore()
