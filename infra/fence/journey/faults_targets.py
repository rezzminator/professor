"""Private terminal ownership boundary; callers use faults."""
from contextlib import contextmanager
from contextvars import ContextVar
import os

from observation import terminal
from runtime import read_json


_scopes = ContextVar("fresh_fault_targets", default=())


def _targets(carry):
    return [chat["terminal"] for chat in carry["chats"]] + [
        carry[key] for key in ("owner", "client", "picker") if carry.get(key)]


def _display(target):
    from faults import owned_path
    if target["backend"] == "tmux":
        owned_path(target["socket"])
    elif target["backend"] == "herdr":
        owned_path(os.environ["HERDR_SOCKET_PATH"])
    else:
        raise ValueError(f"FAULT-BACKEND: {target['backend']}")
    return terminal(target)


def _physical(display):
    from faults import owned_path
    identity = display["pane"].get("terminal_id") if display["backend"] == "herdr" else display["identity"]["pane"]
    if not identity:
        raise ValueError("FAULT-TARGET-PHYSICAL: native terminal identity missing")
    return display["backend"], str(owned_path(display["identity"]["socket"])), identity


def owned_target(target):
    carry = read_json(os.environ["JOURNEY_CARRY"])
    if target == carry["foreign"]["terminal"]:
        raise ValueError("FAULT-FOREIGN-SENTINEL")
    if target in _targets(carry):
        return _display(target)
    if _scopes.get():
        observed = _display(target)
        physical = _physical(observed)
        current = {(row["pid"], row["start"]) for row in observed["births"]}
        for scope, births in _scopes.get():
            if physical == scope:
                from faults import checked_birth
                for pid, start in births & current:
                    checked_birth({"pid": pid, "start": start})
                    return observed
                raise ValueError("FAULT-TARGET-BIRTH: fresh terminal birth changed")
    raise ValueError(f"FAULT-TARGET-NOT-CARRIED: {target}")


@contextmanager
def fresh_target(target):
    """Authorize one uncarried native terminal for this lexical fault scope."""
    from faults import checked_birth
    carry = read_json(os.environ["JOURNEY_CARRY"])
    protected = [carry["foreign"]["terminal"], *_targets(carry)]
    if target in protected:
        raise ValueError("FAULT-TARGET-NOT-FRESH: carried or foreign target")
    observed = _display(target)
    physical = _physical(observed)
    for prior in protected:
        if prior["backend"] == target["backend"] and _physical(_display(prior)) == physical:
            raise ValueError("FAULT-TARGET-NOT-FRESH: carried or foreign physical terminal")
    births = frozenset((row["pid"], row["start"]) for row in observed["births"])
    if not births:
        raise ValueError("FAULT-TARGET-BIRTH: no native process")
    for pid, start in births:
        checked_birth({"pid": pid, "start": start})
    token = _scopes.set((*_scopes.get(), (physical, births)))
    try:
        yield target
    finally:
        _scopes.reset(token)


@contextmanager
def restoration_owner(target):
    """Keep a public reopen operator authorized through its restore callback."""
    try:
        owned_target(target)
    except ValueError as error:
        if not str(error).startswith("FAULT-TARGET-NOT-CARRIED:"):
            raise
        with fresh_target(target):
            yield
        return
    yield
