"""Full independent observation bundles and stable foreign projections."""
from concurrent.futures import ThreadPoolExecutor
import os
import sqlite3

import observation


def foreign_projection(chat):
    terminal = chat["terminal"]
    if isinstance(terminal, str) or not isinstance(chat["status"], dict):
        proof = chat["lifecycle"]
        if "ended" in proof:
            proof = {**proof, "ended": {**proof["ended"], "births": [{"prior": row["prior"]} for row in proof["ended"]["births"]]}}
        return {"rows": chat["rows"], "stored": chat["stored"], "terminal": terminal,
                "state": chat["status"]["state"] if isinstance(chat["status"], dict) else chat["status"], "lifecycle": proof}
    labels = {"workspace": terminal["workspace"]["workspace"]["label"], "tab": terminal["tab"]["tab"]["label"]} if terminal["backend"] == "herdr" else {"workspace": "n/a:tmux", "tab": terminal["pane"]["window_name"]}
    focus = {"workspace": terminal["workspace"]["workspace"]["focused"], "tab": terminal["tab"]["tab"]["focused"], "pane": terminal["pane"]["focused"]} if terminal["backend"] == "herdr" else {"workspace": "n/a:tmux", "tab": "n/a:tmux", "pane": terminal["pane"]["focused"]}
    return {"rows": [{key: row[key] for key in ("kind", "id", "project", "cwd", "name", "account", "killed", "socket")} for row in chat["rows"]],
            "stored": chat["stored"], "terminal": {"identity": terminal["identity"], "labels": labels,
            "focus": focus, "text": terminal["text"], "births": [{"pid": birth["pid"], "start": birth["start"], "argv": birth["chain"][0]["argv"]} for birth in terminal["births"]]}, "state": chat["status"]["state"]}


def bundle(target_file):
    carry = observation.read_json(os.environ["JOURNEY_CARRY"])
    target = observation.read_json(target_file)
    picker_target = carry.get("observer_picker") or carry.get("picker")
    errors = []
    fleet_report = {}

    def collect(label, callback, *arguments):
        try:
            return callback(*arguments), None
        except (OSError, ValueError, RuntimeError, KeyError, sqlite3.Error) as error:
            detail = f"{label}: {type(error).__name__}: {error}"
            return error.projection if isinstance(error, observation.lifecycle.ProjectionMismatch) else {"error": detail}, detail

    def accept(result):
        value, error = result
        if error:
            errors.append(error)
        return value

    # Independent observation reads overlap; owner actions remain serial.
    with ThreadPoolExecutor(max_workers=4) as pool:
        machine_result = accept(collect("machine", observation.lifecycle.machine, carry, observation.report))
        machine, reports_available = machine_result if isinstance(machine_result, tuple) else (machine_result, True)
        rows_future = pool.submit(collect, "picker.rows", observation.fleet_rows, fleet_report) if reports_available else None
        picker_future = pool.submit(collect, "picker.terminal", observation.terminal, picker_target) if picker_target else None
        display_future = pool.submit(collect, "terminal", observation.terminal, target)
        rows = accept(rows_future.result()) if rows_future else "n/a:uninstalled"
        picker_display = accept(picker_future.result()) if picker_future else None
        text = picker_display.get("text", "") if picker_display else ""
        row_values = rows if isinstance(rows, list) else []
        # Preserve the action's rendering before navigating even when it shares this picker pane.
        display = accept(display_future.result())
        from terminal import keys
        badge_future = pool.submit(collect, "picker.badges", observation.observation_picker.badges, picker_target,
                                   [*carry["chats"], carry["foreign"]], row_values, observation.screen, keys) if reports_available and isinstance(rows, list) else None
        def observed_badge(chat):
            values, error = badge_future.result() if badge_future else ({}, None)
            if error:
                return {"state": "n/a:picker-badges-unreadable", "line": "n/a:picker-badges-unreadable"}
            return values.get(chat["session_id"])
        if badge_future and any(chat.get("terminal") == picker_target for chat in [*carry["chats"], carry["foreign"]]):
            badge_future.result()  # A shared pane must be restored before its chat rendering is read.
        # Read independent status/store/terminal evidence while the picker visits its rows.
        chat_futures = [pool.submit(collect, "chat:" + chat["session_id"], observation.chat_projection, chat, row_values,
                                    lambda chat=chat: observed_badge(chat), reports_available) for chat in carry["chats"]]
        foreign_future = pool.submit(collect, "foreign", observation.chat_projection, carry["foreign"], row_values,
                                     lambda: observed_badge(carry["foreign"]), reports_available)
        if badge_future:
            accept(badge_future.result())
        chats = [accept(future.result()) for future in chat_futures]
        foreign = accept(foreign_future.result())
    cache_result = {}
    for index, source in enumerate(carry["chats"]):
        cache_result[source.get("key") or source["session_id"]] = accept(
            collect("account-stats-cache:" + source["session_id"], observation.account_stats_cache, source))
    reporter_result = {}
    for index, source in enumerate(carry["chats"]):
        reporter_result[source.get("key") or source["session_id"]] = accept(
            collect("reporter-activity:" + source["session_id"], observation.reporter_activity, source))
    carried_pane = next((chat.get("terminal", {}).get("pane") for chat in carry["chats"]
                         if chat.get("terminal", {}).get("backend") == "herdr"), None)
    indicator = accept(collect("indicator-daemon", observation.indicator_daemon, None, carried_pane))
    return {"terminal": display, "machine": machine, "fleet_report": fleet_report if reports_available else "n/a:uninstalled", "picker": {"rows": rows,
            "screen": text if picker_display and "error" not in picker_display else picker_display or "n/a:picker-not-open"},
            "chats": chats, "stored": {carry["chats"][index].get("key") or carry["chats"][index]["session_id"]: chat.get("stored", chat) for index, chat in enumerate(chats)},
            "foreign": accept(collect("foreign.projection", observation.foreign_projection, foreign)) if "error" not in foreign else foreign,
            "foreign_full": foreign, "account_stats_cache": cache_result, "reporter_activity": reporter_result,
            "indicator_daemon": indicator,
            "errors": errors, "activity": "pending:pass-refreshes-slice"}
