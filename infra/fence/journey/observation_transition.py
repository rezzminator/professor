"""Exact carried-session and engine-birth transition projections."""


class ProjectionMismatch(ValueError):
    def __init__(self, message, projection):
        super().__init__(message)
        self.projection = projection


def birth_presence(birth, processes):
    current = processes.get(birth["pid"])
    return "present" if current is not None and current["start"] == birth["start"] else "ended"


def carried(bundle, session_id):
    matches = [item for item in bundle["chats"]
               if isinstance(item.get("status"), dict) and item["status"].get("session_id") == session_id]
    if len(matches) != 1:
        raise ValueError(f"ATTENTION-CHAT: expected one session {session_id!r}, got {len(matches)}")
    return matches[0]


def reload_ready(previous, terminal, carry):
    from observation import terminal_births
    from observation_process import engine_births
    before = carried(previous, carry["session_id"])
    prior = engine_births(before, carry["engine"])
    if len(prior) != 1:
        raise ValueError(f"RELOAD-PRIOR-ENGINE: expected one engine, actual={prior!r}")
    current = engine_births({"terminal": terminal, "stored": before.get("stored", {}),
                            "process_births": terminal_births(terminal)}, carry["engine"])
    return len(current) == 1 and birth_presence(prior[0], {current[0]["pid"]: current[0]}) == "ended"


class ProcessExited(ValueError):
    def __init__(self, process, error, following):
        self.process = process
        self.evidence = {"process": process, "following": following,
                         "error": {"type": type(error).__name__, "errno": error.errno, "message": str(error)}}
        super().__init__(f"OBSERVE-PROCESS-EXIT-DURING-READ: {self.evidence!r}")
