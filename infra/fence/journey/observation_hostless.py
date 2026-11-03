"""Observe a native moved terminal after its exact seat supervisor died."""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import stat

from herdr import Refused


def hostless_scope(chat):
    encoded = os.environ.get("JOURNEY_HOSTLESS")
    if encoded is None:
        return None
    scope = json.loads(encoded)
    if scope["key"] != chat.get("key", chat["session_id"]):
        return None
    if any(scope[key] != chat.get(key) for key in ("session_id", "seat_id", "engine", "terminal")):
        raise ValueError(f"OBSERVE-HOSTLESS-IDENTITY: {chat['session_id']}")
    return scope


def _birth_state(birth, processes):
    from observation_lifecycle import birth_presence
    return "alive" if birth_presence(birth, processes) == "present" and processes[birth["pid"]].get("state") != "Z" else "dead"


def _physical(display):
    identity = display.get("pane", {}).get("terminal_id")
    if not isinstance(identity, str) or not identity:
        raise ValueError("OBSERVE-HOSTLESS-PHYSICAL: actual Herdr terminal_id required")
    return identity


def _move_evidence(receipt):
    from observation import terminal_births
    evidence = {key: receipt[key] for key in ("run", "source", "target", "supervisor")}
    for key in ("before", "prekill"):
        display = receipt[key]
        evidence[key] = {"identity": display["identity"], "pane": {"terminal_id": _physical(display)},
                         "births": [{"pid": birth["pid"], "start": birth["start"],
                                     "chain": [{key: node[key] for key in ("pid", "start", "state") if key in node}
                                               for node in birth["chain"]]} for birth in terminal_births(display)]}
    return evidence


def _validate(chat, scope, processes, terminal):
    receipt = scope["receipt"]
    if (receipt["run"] != os.environ["PFM_RUN"] or receipt["source"] != chat["terminal"]
            or chat["terminal"]["backend"] != "herdr" or receipt["target"]["backend"] != "herdr"
            or receipt["target"] == receipt["source"]):
        raise ValueError(f"OBSERVE-HOSTLESS-RECEIPT: {chat['session_id']}")
    before, prekill = receipt["before"], receipt["prekill"]
    if (before["identity"]["pane"] != receipt["source"]["pane"]
            or prekill["identity"]["pane"] != receipt["target"]["pane"]
            or before["identity"]["socket"] != os.environ["HERDR_SOCKET_PATH"]
            or prekill["identity"]["socket"] != before["identity"]["socket"]):
        raise ValueError("OBSERVE-HOSTLESS-MOVE: receipt does not name the actual native source and destination")
    supervisor = receipt["supervisor"]
    from observation import terminal_births
    chain = [node for birth in terminal_births(prekill) for node in birth["chain"]]
    if not any(node["pid"] == supervisor["pid"] and node["start"] == supervisor["start"] and node.get("state") == "T" for node in chain):
        raise ValueError("OBSERVE-HOSTLESS-SUPERVISOR: stopped supervisor missing from prekill ancestry")
    if _birth_state(supervisor, processes) != "dead":
        raise ValueError("OBSERVE-HOSTLESS-SUPERVISOR: exact supervisor birth still alive")
    display = terminal(receipt["target"])
    if _physical(before) != _physical(prekill) or _physical(display) != _physical(prekill):
        raise ValueError("OBSERVE-HOSTLESS-PHYSICAL: moved terminal changed")
    if display["identity"]["pane"] != receipt["target"]["pane"] or display["identity"]["socket"] != prekill["identity"]["socket"]:
        raise ValueError("OBSERVE-HOSTLESS-MOVE: current native destination changed")
    return display


def hostless(chat, status, storage, processes, terminal, request):
    scope = hostless_scope(chat)
    if scope is None:
        raise ValueError(f"OBSERVE-HOSTLESS-SCOPE: {chat['session_id']}")
    display = _validate(chat, scope, processes, terminal)
    seats = [seat for seat in storage["seat"] if seat["seat_id"] == chat["seat_id"]]
    if (len(seats) != 1 or seats[0].get("herdr_pane") != chat["terminal"]["pane"]
            or seats[0].get("herdr_socket") != os.environ["HERDR_SOCKET_PATH"]
            or seats[0].get("seat_host_pid") != scope["receipt"]["supervisor"]["pid"]
            or seats[0].get("herdr_terminal_id") != _physical(display)):
        raise ValueError(f"OBSERVE-HOSTLESS-SEAT: {chat['session_id']}: {seats!r}")
    try:
        request(os.environ["HERDR_SOCKET_PATH"], "pane.get", {"pane_id": seats[0]["herdr_pane"]})
    except Refused as error:
        if error.code != "pane_not_found":
            raise
    else:
        raise ValueError("OBSERVE-HOSTLESS-PANE: stored pane still exists")
    engines = [{"prior": birth, "state": _birth_state(birth, processes),
                "current": processes.get(birth["pid"], "n/a:process-absent")} for birth in scope["engines"]]
    return display, {"supervisor": {"prior": scope["receipt"]["supervisor"], "state": "dead"},
                     "engines": engines, "stored_pane": "pane_not_found", "receipt": scope["receipt"]}


@contextmanager
def temporary_hostless(branch, origin, before, receipt_path):
    """Propagate one validated native move window without changing carry or store."""
    import chat_carry
    import observation
    from observation_lifecycle import carried, expectations
    from observation_process import engine_births
    from runtime import read_json
    chat = chat_carry.select(branch, origin)
    if chat is None:
        raise ValueError(f"CHAT-HOSTLESS-ABSENT: {branch}:{origin}")
    if "JOURNEY_HOSTLESS" in os.environ:
        raise ValueError("CHAT-HOSTLESS-SCOPE: nested scope is not allowed")
    if expectations(chat) != {"lifecycle": "live", "terminal": "live", "visibility": "visible"}:
        raise ValueError("CHAT-HOSTLESS-LIFECYCLE: visible live carried actor required")
    path = Path(receipt_path)
    if not path.is_absolute() or path.is_symlink() or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError("CHAT-HOSTLESS-RECEIPT: absolute regular receipt required")
    path.resolve(strict=True).relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True))
    receipt = read_json(path)
    prior = carried(before, chat["session_id"])
    engines = engine_births(prior, chat["engine"])
    prekill = engine_births({"terminal": receipt["prekill"],
                            "process_births": observation.terminal_births(receipt["prekill"])}, chat["engine"])
    identities = lambda births: {(birth["pid"], birth["start"]) for birth in births}
    if not engines or identities(engines) != identities(prekill):
        raise ValueError("CHAT-HOSTLESS-ENGINE: prior and native prekill engine births differ")
    scope = {key: chat[key] for key in ("session_id", "seat_id", "engine", "terminal")}
    scope.update(key=chat.get("key", chat["session_id"]), receipt=_move_evidence(receipt),
                 engines=[{key: birth[key] for key in ("pid", "start")} for birth in engines])
    _validate(chat, scope, observation.process_snapshot(), observation.terminal)
    os.environ["JOURNEY_HOSTLESS"] = json.dumps(scope)
    try:
        yield scope
    finally:
        os.environ.pop("JOURNEY_HOSTLESS", None)
