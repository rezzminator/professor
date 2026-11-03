"""Bind staged launches only to actual native seats and process scenarios."""
import chat_carry as carry
import re
import observation
from chat_seatless import capture_codex


def capture(target, branch, origin, previous, project=None, workspace=None, replacement=None):
    engine = carry.branch_parts(branch, origin)[3]
    if previous["target"] != target:
        raise ValueError("CHAT-CAPTURE-TARGET: before receipt belongs to another owner entry")
    metadata = carry.read_json(carry.metadata_path(branch, origin))
    if metadata.get("scenario_session_id") and carry.read_json(metadata["scenario"]).get("session_id") != metadata["scenario_session_id"]:
        raise ValueError("CHAT-CAPTURE-SCENARIO: staged UUID no longer matches scenario input")
    project = project or metadata.get("project", "atlas")
    workspace = carry.workspace_id(project, workspace or metadata.get("workspace"))
    if project != metadata.get("project", "atlas") or workspace != metadata.get("workspace", workspace):
        raise ValueError("CHAT-CAPTURE-SCOPE: staged project/workspace differs from expected")
    scope = {"backend": "tmux", "socket": metadata["launch_socket"]} if carry.branch_parts(branch)[1] == "tmux" else {"backend": "herdr"}
    rows = carry.seats()
    scoped = [row for row in rows if carry.owned(row, scope)]
    diagnostic_path = carry.metadata_path(branch, origin).parent / origin / "capture.json"
    diagnostic = {"scope": scope, "scenario": metadata["scenario"], "launch_socket": metadata.get("launch_socket"),
                  "previous_seats": previous["seats"], "scoped_seats": scoped,
                  "run_seats": [row for row in rows if carry.run_owned(row)], "predicates": []}
    carry.write_json(diagnostic_path, diagnostic)
    candidates = [row for row in scoped if row["seat_id"] not in previous["seats"]
                  and row["engine"] == carry.ENGINES[engine] and row["state"] == "live" and row["session_id"]
                  and (scope["backend"] != "herdr" or workspace.startswith("label:") or row["herdr_workspace"] == workspace)]
    pending = [row for row in candidates if not carry.seat_bound(row)]
    if pending:
        carry.write_json(carry.metadata_path(branch, origin).parent / origin / "capture-pending.json", {"pending_terminal_binding": pending})
    diagnostic["candidates"] = [row["seat_id"] for row in candidates]
    matches = []
    try:
        for row in candidates:
            predicate = {"seat_id": row["seat_id"], "terminal_bound": carry.seat_bound(row)}
            diagnostic["predicates"].append(predicate)
            # Fresh CLI Codex/OpenCode mocks register their input UUID after the provisional launch seat.
            if engine in {"codex", "opencode"} and metadata.get("launch_argv", [])[:3] == ["pfm", "chat", "new"]:
                predicate["native_session_matches"] = row["session_id"] == metadata["scenario_session_id"]
                if not predicate["native_session_matches"]:
                    continue
            if not predicate["terminal_bound"]:
                continue
            try:
                terminal = carry.seat_terminal(row)
                actual = carry.observed_scenario(terminal, engine, workspace)
            except (OSError, ValueError, RuntimeError, KeyError) as error:
                predicate["error"] = f"{type(error).__name__}: {error}"
                raise
            predicate["observed_scenario"] = actual
            predicate["scenario_matches"] = actual["scenario"] == metadata["scenario"]
            if predicate["scenario_matches"]:
                matches.append((row, terminal))
    finally:
        carry.write_json(diagnostic_path, diagnostic)
    if not matches:
        if not scoped and scope["backend"] == "tmux" and engine == "codex":
            return capture_codex(branch, origin, metadata, project, workspace, diagnostic_path, diagnostic, replacement)
        return None
    if len(matches) != 1:
        raise ValueError(f"CHAT-CAPTURE-AMBIGUOUS: {[row['seat_id'] for row, _ in matches]!r}")
    row, terminal = matches[0]
    if replacement is not None and terminal == replacement["terminal"]:
        raise ValueError(f"CHAT-CONTINUATION-IDENTITY: terminal was not replaced: {terminal!r}")
    return carry.remember(row, branch, metadata.get("origin", origin),
                    {**metadata, "project": project,
                     "workspace": row["herdr_workspace"] if scope["backend"] == "herdr" else workspace},
                    replacement=replacement, terminal=terminal)


def seat_bound(row):
    return bool(row["herdr_pane"] if row["backend"] == "herdr" else row["tmux_socket"])


def seat_terminal(row):
    if not carry.seat_bound(row):
        raise ValueError(f"CHAT-SEAT-UNBOUND: {row['seat_id']}")
    if row["backend"] == "herdr":
        return {"backend": "herdr", "pane": row["herdr_pane"]}
    return tmux_terminal(carry.socket_path(row["tmux_socket"]), row["tmux_pane"])


def tmux_terminal(socket, pane=""):
    if not pane:
        # Public tmux births may record the socket before an identity hook records its pane.
        panes = observation.report(["tmux", "-S", socket, "list-panes", "-a", "-F", "#{pane_id}"]).splitlines()
        if len(panes) != 1 or not re.fullmatch(r"%[0-9]+", panes[0]):
            raise ValueError(f"CHAT-TMUX-PANE-AMBIGUOUS: {socket}: {panes!r}")
        pane = panes[0]
    return {"backend": "tmux", "socket": socket, "pane": pane}


def workspace_id(project="atlas", workspace=None):
    if not re.fullmatch(r"[a-z][a-z0-9-]*", project):
        raise ValueError(f"CHAT-PROJECT: {project!r}")
    declared = carry.carry()
    workspace = workspace or project
    entry = declared.get("workspaces", {}).get(workspace) or declared.get("workspace_branches", {}).get(workspace)
    return entry["id"] if entry else workspace
