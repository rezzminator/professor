"""The single writer for discovered chat identities and retained lineage."""
import os
import chat_carry as carry


def _retain_lineage(current, updated):
    if current["session_id"] != updated["session_id"]:
        updated["lineage_ids"] = list(dict.fromkeys([*current.get("lineage_ids", []), current["session_id"]]))
    if current.get("seat_id") and current["seat_id"] != updated.get("seat_id"):
        updated["prior_seat_ids"] = list(dict.fromkeys([*current.get("prior_seat_ids", []), current["seat_id"]]))
    return updated


def remember(row, branch, origin, metadata, replacement=None, terminal=None):
    _, terminal_kind, place, _ = carry.branch_parts(branch, origin)
    engine = next((name for name, code in carry.ENGINES.items() if code == row["engine"]), None)
    if engine is None:
        raise ValueError(f"CHAT-ENGINE: {row['engine']!r}")
    target = terminal if terminal is not None else carry.seat_terminal(row)
    identity = "chat-birth:" + branch + ":" + origin
    result = {"key": identity, "branch": branch, "origin": origin, "engine": engine, "place": place,
              "terminal_kind": "tmux" if row["backend"] == "tmux" else terminal_kind, "name": row["name"], "session_id": row["session_id"],
              "seat_id": row["seat_id"], "terminal": target, "project": metadata.get("project", "atlas"),
              "account": row["account"], "cache1h": row["cache1h"]}
    result = {**metadata, **result, "key": metadata.get("key", identity)}
    return save(result, replacement)


def save(result, replacement=None):
    declared = carry.carry()
    if replacement is None:
        if any(chat.get("key") == result["key"] for chat in declared["chats"]):
            raise ValueError(f"CHAT-CARRY-EXISTS: {result['key']}; use refresh after reload")
        if any(chat.get("session_id") == result["session_id"] or (result.get("seat_id") and chat.get("seat_id") == result["seat_id"]) for chat in declared["chats"]):
            raise ValueError(f"CHAT-CARRY-IDENTITY: {result['session_id']}: already carried")
        declared["chats"].append(result)
    else:
        if any(chat.get("session_id") == result["session_id"] or (result.get("seat_id") and chat.get("seat_id") == result["seat_id"])
               for chat in declared["chats"] if chat.get("key") != replacement["key"]):
            raise ValueError(f"CHAT-CARRY-IDENTITY: {result['session_id']}: already carried")
        result = _retain_lineage(replacement, {**replacement, **result,
                                                "key": replacement["key"],
                                                "origin": replacement.get("origin", result["origin"])})
        declared["chats"] = [result if chat.get("key") == replacement["key"] else chat
                              for chat in declared["chats"]]
    carry.write_json(os.environ["JOURNEY_CARRY"], declared)
    return result


