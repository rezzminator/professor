"""Retain failed native root births without replacing their original error."""
import os
from pathlib import Path
import activity
import herdr
from runtime import write_json


def failed_composer(progress, actual, missing, bindings, log_path, log_start):
    from chat import seats
    evidence = {"seat": actual, "missing": missing, "bindings": bindings}
    for label, reader in (("seats", lambda: [row for row in seats() if row["herdr_socket"] == os.environ["HERDR_SOCKET_PATH"]]),
                          ("workspaces", lambda: herdr.request(os.environ["HERDR_SOCKET_PATH"], "workspace.list", {})),
                          ("panes", lambda: herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.list", {})),
                          ("pfm_log", lambda: activity.records(log_path, log_start)),
                          ("server_log", lambda: (Path(os.environ["JOURNEY_ROOT"]) / "herdr.log").read_text()),
                          ("native_server_log", lambda: Path(os.environ["HERDR_SOCKET_PATH"]).with_name("herdr-server.log").read_text())):
        try:
            evidence[label] = reader()
        except (OSError, ValueError, RuntimeError) as error:
            evidence[label] = {"error": str(error)}
    write_json(progress, evidence)
