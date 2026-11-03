"""Private indicator lifecycle fault; callers use faults.retired_indicator."""
from contextlib import contextmanager
import os
import signal

from bootstrap import wait
from observation import indicator_daemon


@contextmanager
def retired_indicator(birth, socket, restore, *, pane=None):
    from faults import owned_path, checked_birth, signal_birth, birth_exited
    socket = owned_path(socket)
    if str(socket) != os.environ["HERDR_SOCKET_PATH"]:
        raise ValueError("FAULT-INDICATOR-SOCKET: not this run's server")
    if not callable(restore):
        raise ValueError("FAULT-INDICATOR-RESTORE: restart callback required")
    process = checked_birth(birth)
    observed = indicator_daemon(snapshot={birth["pid"]: process}, pane=pane)
    if not isinstance(observed, dict) or any(observed[key] != birth[key] for key in ("pid", "start")):
        raise ValueError(f"FAULT-INDICATOR-BIRTH: expected={birth!r}, actual={observed!r}")
    try:
        signal_birth(birth, signal.SIGTERM)
        wait("retired indicator " + str(birth["pid"]), lambda: birth_exited(birth))
        yield birth
    finally:
        restore()
