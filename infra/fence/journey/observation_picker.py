"""Inspect existing picker selections without opening chats or changing their query."""
import re


def session(shown, identities):
    tokens = re.findall(r"(?:^|\s{2,})session[^\S\n]+(\S+)", shown, re.MULTILINE)
    if not tokens:
        return None
    if len(tokens) != 1:
        raise ValueError(f"OBSERVE-PICKER-DOSSIER: {tokens!r}")
    token = tokens[0]
    matches = {identity for identity in identities if identity and
               (identity == token or (token.startswith("…") and identity.endswith(token[1:])))}
    if len(matches) != 1:
        raise ValueError(f"OBSERVE-PICKER-SESSION: rendered={token!r} matches={sorted(matches)!r}")
    return matches.pop()


def selected_line(shown):
    lines = [line for line in shown.splitlines()
             if re.match(r"^\s*(?:│\s*)?›\s*[●○◉↻⚙◐✦⬆·]\s", line)]
    if len(lines) != 1:
        raise ValueError(f"OBSERVE-PICKER-SELECTION: {lines!r}")
    return lines[0]


def selection(shown, identities):
    if "◆ pfm " not in shown:
        raise ValueError("OBSERVE-PICKER-NOT-OPEN: existing live picker required")
    identity = session(shown, identities)
    return ("session", identity) if identity else ("row", selected_line(shown).strip())


def _visible_count(shown):
    counts = re.findall(r"\b(\d+)/\d+ (?:visible|match)\b", shown)
    if len(counts) != 1:
        raise ValueError(f"OBSERVE-PICKER-COUNT: {counts!r}")
    return int(counts[0])


def _next(target, previous, identities, read_screen, send_keys):
    from bootstrap import wait
    send_keys(target, ["down"])
    def moved():
        value = read_screen(target)
        return value if selection(value, identities) != previous else None
    return wait("picker next selected session", moved, 3)


def navigate(target, expected, identities, read_screen, send_keys):
    shown = read_screen(target)
    original = selection(shown, identities)
    for _ in range(_visible_count(shown) + 1):
        previous = selection(shown, identities)
        if previous == expected:
            return shown
        shown = _next(target, previous, identities, read_screen, send_keys)
        if selection(shown, identities) == original:
            break
    raise ValueError(f"OBSERVE-PICKER-IDENTITY: expected={expected!r} actual={shown!r}")


def select_session(target, current, identities, read_screen, send_keys):
    return navigate(target, ("session", current["session_id"]), identities, read_screen, send_keys)


def badge(name, shown):
    try:
        line = selected_line(shown)
    except ValueError as error:
        raise ValueError(f"OBSERVE-PICKER-BADGE-ABSENT: {name}") from error
    # The leading live marker pulses for agents too; only the later gauge core means this chat works.
    cores = re.findall(r"(?:^|\s{2})working\s+([●◉◌])", shown, re.MULTILINE)
    if len(cores) > 1:
        raise ValueError(f"OBSERVE-PICKER-WORK-GAUGE: {cores!r}")
    state = "blocked" if "[B]" in line else "working" if cores and cores[0] in ("●", "◉") else "idle"
    return {"state": state, "line": line}


def badges(target, chats, rows, read_screen, send_keys):
    from bootstrap import wait
    available = {row["id"] for row in rows}
    failure = None
    def discovered():
        nonlocal failure
        try:
            return (_badges(target, chats, rows, read_screen, send_keys),)
        except ValueError as error:
            if not str(error).startswith("OBSERVE-PICKER-IDENTITY:") or any(
                    chat["session_id"] not in available for chat in chats
                    if chat.get("expect", {}).get("lifecycle", "live") == "live"):
                raise
            failure = error
            return None
    try:
        return wait("picker discovers publicly listed sessions", discovered, 10)[0]
    except ValueError as error:
        if str(error).startswith("ROOT-TIMEOUT:") and failure is not None:
            raise failure from error
        raise


def _badges(target, chats, rows, read_screen, send_keys):
    applicable = [chat for chat in chats if chat.get("expect", {}).get("lifecycle", "live") == "live"
                  and chat.get("expect", {}).get("visibility", "visible") == "visible"]
    if not applicable:
        return {}
    if not target:
        raise ValueError("OBSERVE-PICKER-NOT-OPEN: carry.picker absent")
    identities = [row["id"] for row in rows]
    shown = read_screen(target)
    original = selection(shown, identities)
    # A dedicated observation picker keeps its query and carousel untouched by owner actions.
    if not re.search(r"find ›\s*(?:type to find · @ to go\s+)?\d+/\d+ visible", shown):
        raise ValueError("OBSERVE-PICKER-FILTERED: dedicated Chats picker with empty query required")
    if re.search(r"◖[^\n]*(?:reboot|kill|unhide|deactive)[^\n]*◗", shown):
        raise ValueError("OBSERVE-PICKER-CAROUSEL: dedicated picker must arm open")
    count = _visible_count(shown)
    names = {chat["session_id"]: chat["name"] for chat in applicable}
    result = {}
    restored = False
    try:
        for step in range(count + 1):
            current = selection(shown, identities)
            if current == original and step:
                restored = True
                break
            if current[0] == "session" and current[1] in names:
                result[current[1]] = badge(names[current[1]], shown)
            shown = _next(target, current, identities, read_screen, send_keys)
        expected = next((chat["session_id"] for chat in applicable if chat["session_id"] not in result), None)
        if expected or not restored:
            expected_identity = ("session", expected) if expected else original
            raise ValueError(f"OBSERVE-PICKER-IDENTITY: expected={expected_identity!r} actual={shown!r}")
    finally:
        if not restored:
            navigate(target, original, identities, read_screen, send_keys)
    return result
