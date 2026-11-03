"""Capture a native Codex tmux launch when the product creates no durable seat."""
import json

import chat_carry as carry
import observation
from observation_lifecycle import absent
from chat_registry import save


def capture_codex(branch, origin, metadata, project, workspace, diagnostic_path, diagnostic, replacement=None):
    from chat_capture import tmux_terminal
    socket = metadata["launch_socket"]
    rows = [row for row in observation.fleet_rows()
            if row["kind"] == "live-codex" and row["socket"]
            and carry.socket_path(row["socket"]) == socket]
    diagnostic["native_fleet"] = rows
    carry.write_json(diagnostic_path, diagnostic)
    if not rows or absent(socket):
        return None
    if len(rows) != 1:
        raise ValueError(f"CHAT-CAPTURE-AMBIGUOUS: {[row['id'] for row in rows]!r}")
    row = rows[0]
    if row["project"] != project:
        raise ValueError(f"CHAT-CAPTURE-PROJECT: expected={project!r} actual={row['project']!r}")
    # Resolve the actual socket using the same native pane door as seated launches.
    target = tmux_terminal(socket)
    display = carry.observe_terminal(target)
    births = carry.engine_births({"terminal": display, "process_births": carry.terminal_births(display)}, "codex")
    diagnostic["native_births"] = births
    carry.write_json(diagnostic_path, diagnostic)
    if not births:
        return None
    if len(births) != 1:
        raise ValueError(f"CHAT-CAPTURE-ENGINE-AMBIGUOUS: {births!r}")
    if carry.observed_scenario(target, "codex", display=display)["scenario"] != metadata["scenario"]:
        return None
    status = json.loads(observation.report(["pfm", "chat", "status", row["id"], "--json"]))
    if (status["session_id"] != row["id"] or status["engine"] != "cx"
            or carry.socket_path(status["socket"]) != socket):
        raise ValueError(f"CHAT-CAPTURE-STATUS: {status!r}")
    if status["state"] not in {"idle", "working", "blocked"}:
        return None
    identity = "chat-birth:" + branch + ":" + metadata.get("origin", origin)
    result = {**metadata, "key": metadata.get("key", identity), "branch": branch,
              "origin": metadata.get("origin", origin), "engine": "codex",
              "terminal_kind": "tmux", "place": carry.branch_parts(branch)[2],
              "name": row["name"], "session_id": row["id"], "account": int(row["account"]),
              "cache1h": None, "terminal": target, "project": project, "workspace": workspace,
              "tmux_launch": {"status": status, "terminal": target, "births": births}}
    result["native_rollout"] = observation.read_actual_codex_session(result)
    diagnostic["native_launch"] = result["tmux_launch"]
    diagnostic["native_rollout"] = result["native_rollout"]
    carry.write_json(diagnostic_path, diagnostic)
    return save(result, replacement)
