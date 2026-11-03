#!/usr/bin/env python3
"""Reload and attention compare independent observed surfaces of carried chats."""
import re
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from observation import birth_presence, carried, reload_ready


def account_caches(one, two):
    return {"one": [one["state"], one["regular"], one["symlink"]],
            "two": [two["state"], two["regular"], two["symlink"]],
            "identity": "distinct" if one["identity"] != two["identity"] else "shared"}


def settled_metadata(status, chat):
    return bool(chat["name"] and status.get("model") and not status["model"].startswith("n/a:"))


def metadata_visible(status, chat, screen, name):
    return (status["state"] == "idle" and chat["name"] == name
            and "⏺ reload metadata ready" in screen.splitlines())


def metadata_checks(previous, current, session, engine, name):
    rows = attention_invariants(previous, current, session, engine)
    for row in rows:
        if row["field"] == "name":
            row["expected"] = name
    return [row for row in rows if row["field"] != "model"]


def typed_receipt(screen):
    for paragraph in screen.split("\n\n"):
        text = "".join(paragraph.splitlines())
        if not text.startswith("⎿ Blocked by hook: reload scheduled"):
            continue
        match = re.fullmatch(r"⎿ Blocked by hook: (?P<text>reload scheduled — this chat reboots when the current turn ends)"
                             r" \(pfm chat reload: reload scheduled in place \(log (?P<log>/[^()\s]+\.log)\)\)", text)
        if match is None:
            raise ValueError(f"RELOAD-TYPED-RECEIPT: malformed visible receipt {text!r}")
        return match.groupdict()
    return None


def typed_receipt_log(receipt, bundle, session, directory):
    path = Path(receipt["log"])
    records = carried(bundle, session)["stored"]["reload_records"]
    actual = str(Path(directory) / path.name) if records.get(path.name, "absent") != "absent" else "n/a:missing-reload-log"
    return [{"field": "typed-receipt-log", "expected": str(path), "actual": actual}]


def attention_invariants(previous, current, session_id, engine, transition="preserve"):
    from observation_process import engine_births

    before, after = carried(previous, session_id), carried(current, session_id)
    def identity(chat):
        births = engine_births(chat, engine)
        selected = [row for row in chat["rows"] if row["id"] == session_id]
        if len(births) != 1 or len(selected) != 1:
            raise ValueError(f"ATTENTION-IDENTITY: expected one engine and row: births={births!r}, rows={selected!r}")
        return {"session": chat["status"]["session_id"], "terminal": chat["terminal"]["identity"],
                "name": selected[0]["name"], "project": selected[0]["project"],
                "cwd": chat["status"]["cwd"], "model": chat["status"].get("model", "n/a:unreported-model"),
                "account": selected[0]["account"], "engine": chat["status"]["engine"],
                "birth": {key: births[0][key] for key in ("pid", "start")}}
    expected, actual = identity(before), identity(after)
    if transition == "restore":
        old_birth, new_birth = expected["birth"], actual["birth"]
        expected["birth"] = "replaced"
        actual["birth"] = "preserved" if birth_presence(old_birth, {new_birth["pid"]: new_birth}) == "present" else "replaced"
        del expected["terminal"], actual["terminal"]
    elif transition != "preserve":
        raise ValueError(f"ATTENTION-TRANSITION: {transition!r}")
    expected["foreign"], actual["foreign"] = previous["foreign"], current["foreign"]
    return [{"field": key, "expected": value, "actual": actual[key]} for key, value in expected.items()]


def checks(previous, current, old_carry, new_carry, mode, continuation=None):
    from observation_process import engine_births

    rows = []

    def equal(label, expected, actual):
        rows.append({"field": label, "expected": expected, "actual": actual})

    before, after = carried(previous, old_carry["session_id"]), carried(current, new_carry["session_id"])
    prior_births = engine_births(before, old_carry["engine"])
    next_births = engine_births(after, new_carry["engine"])
    equal("engine-count", 1, len(next_births))
    equal("prior-engine-count", 1, len(prior_births))
    if len(prior_births) == len(next_births) == 1:
        prior, successor = prior_births[0], next_births[0]
        equal("engine-terminal", before["terminal"]["identity"], successor["terminal"])
        equal("engine-birth", "preserved" if mode == "refused" else "replaced",
              "preserved" if birth_presence(prior, {successor["pid"]: successor}) == "present" else "replaced")
        config = "CLAUDE_CONFIG_DIR" if old_carry["engine"] == "claude" else "CODEX_HOME"
        if old_carry["engine"] != "opencode":
            expected_home = prior["environment"][config]
            if mode == "account":
                from pathlib import Path
                expected_home = str(Path(expected_home).parent / "2")
            equal("engine-home", expected_home, successor["environment"][config])
    old_id, new_id = old_carry["session_id"], new_carry["session_id"]
    fresh = mode in {"fresh", "fresh-hidden"}
    equal("terminal", before["terminal"]["identity"], after["terminal"]["identity"])
    equal("cwd", before["status"]["cwd"], after["status"]["cwd"])
    equal("model", before["status"].get("model", "n/a:unreported-model"),
          after["status"].get("model", "n/a:unreported-model"))
    equal("state", "idle", after["status"]["state"])
    equal("current-session", new_id, after["status"]["session_id"])
    if mode == "continuation":
        if continuation is None:
            raise ValueError("RELOAD-CONTINUATION: actual indexed receipt required")
        equal("continuation-launch", old_id, continuation["launch_id"])
        equal("continuation-tail", continuation["tail_id"], new_id)
        equal("lineage-mode", "continued", "continued" if new_id and new_id != old_id else "resume")
    else:
        equal("lineage-mode", "fresh" if fresh else "resume",
              "fresh" if new_id and new_id != old_id else "resume")
    if fresh or mode == "continuation":
        equal("predecessor", old_id, new_carry["lineage_ids"][-1])
    else:
        equal("session", old_id, new_id)
    old_rows = [row for row in before["rows"] if row["id"] == old_id]
    new_rows = [row for row in after["rows"] if row["id"] == new_id]
    if len(old_rows) != 1 or len(new_rows) != 1:
        raise ValueError("RELOAD-ROWS: exactly one current row required before and after reload")
    if len(before["rows"]) != len(before["picker"]) or len(after["rows"]) != len(after["picker"]):
        raise ValueError("RELOAD-PICKER: row projection is incomplete")
    old_picker = next(picker for row, picker in zip(before["rows"], before["picker"]) if row["id"] == old_id)
    new_picker = next(picker for row, picker in zip(after["rows"], after["picker"]) if row["id"] == new_id)
    expected_account = 2 if mode == "account" else old_carry["account"]
    equal("account", expected_account, int(new_rows[0]["account"]))
    equal("name", old_rows[0]["name"], new_rows[0]["name"])
    for field in ("engine", "project", "backend", "address"):
        equal("picker-" + field, old_picker[field], new_picker[field])
    equal("picker-account", expected_account, new_picker["account"])
    equal("picker-badge", "idle", new_picker["badge"]["state"])
    launches = [row for row in after["stored"]["launch"] if row["session_id"] == new_id]
    equal("launch", [{"session_id": new_id, "engine": before["status"]["engine"],
                      "account": expected_account, "cache1h": old_carry["cache1h"]}],
          [{key: row[key] for key in ("session_id", "engine", "account", "cache1h")} for row in launches])
    seats = [row for row in after["stored"]["seat"] if row["state"] == "live"]
    equal("live-seat", [{"session_id": new_id, "reload_pending": 0}],
          [{key: row[key] for key in ("session_id", "reload_pending")} for row in seats])
    if fresh:
        equal("predecessor-hidden", mode == "fresh-hidden",
              any(row["uuid"] == old_id for row in after["stored"]["hidden"]))
    equal("foreign", previous["foreign"], current["foreign"])
    return rows


def attention(bundle, session_id, display=None):
    chat = carried(bundle, session_id)
    matches = [picker for row, picker in zip(chat["rows"], chat["picker"])
               if row["id"] == session_id]
    if len(chat["rows"]) != len(chat["picker"]) or len(matches) != 1:
        raise ValueError(f"ATTENTION-PICKER: expected one row for session {session_id!r}")
    row, terminal = matches[0], chat["terminal"] if display is None else display
    value = {"reported_state": chat["status"]["state"], "picker_state": row["state"],
             "badge_state": row["badge"]["state"]}
    if terminal["backend"] == "herdr":
        tags = re.findall(r"\[[WBE]\]", terminal["tab"]["tab"]["label"])
        tokens = sorted(key for key in terminal["pane"].get("tokens", {}) if key.startswith("pfm_"))
        value.update(herdr_state=terminal["pane"]["agent_status"], tab_tag=" ".join(tags),
                     indicator=",".join(tokens) or "none")
        if value["reported_state"] == "error":
            value["indicator"] = "n/a:error-indicator-unspecified"
    elif terminal["backend"] == "tmux":
        value.update(herdr_state="n/a:tmux", tab_tag="n/a:tmux", indicator="n/a:tmux")
    else:
        raise ValueError(f"ATTENTION-BACKEND: {terminal['backend']!r}")
    return value



if __name__ == "__main__":
    import json
    from runtime import read_json

    try:
        if sys.argv[1] == "attention":
            result = attention(read_json(sys.argv[2]), sys.argv[3],
                               read_json(sys.argv[4]) if len(sys.argv) > 4 else None)
        elif sys.argv[1] == "reload":
            result = checks(*(read_json(path) for path in sys.argv[2:6]), sys.argv[6],
                            read_json(sys.argv[7]) if len(sys.argv) > 7 else None)
        elif sys.argv[1] == "reload-ready":
            result = reload_ready(*(read_json(path) for path in sys.argv[2:5]))
        elif sys.argv[1] == "settled-metadata":
            result = settled_metadata(read_json(sys.argv[2]), read_json(sys.argv[3]))
        elif sys.argv[1] == "metadata-visible":
            result = metadata_visible(read_json(sys.argv[2]), read_json(sys.argv[3]),
                                      Path(sys.argv[4]).read_text(), sys.argv[5])
        elif sys.argv[1] == "metadata-checks":
            result = metadata_checks(read_json(sys.argv[2]), read_json(sys.argv[3]), *sys.argv[4:])
        elif sys.argv[1] == "typed-receipt":
            result = typed_receipt(Path(sys.argv[2]).read_text())
        elif sys.argv[1] == "typed-receipt-log":
            result = typed_receipt_log(read_json(sys.argv[2]), read_json(sys.argv[3]), *sys.argv[4:6])
        elif sys.argv[1] == "attention-invariants":
            result = attention_invariants(read_json(sys.argv[2]), read_json(sys.argv[3]), *sys.argv[4:])
        else:
            raise ValueError("PROJECTION-OPERATION: expected attention or reload")
        print(json.dumps(result, sort_keys=True))
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"CHAT-PROJECTION: {error}", file=sys.stderr)
        sys.exit(2)
