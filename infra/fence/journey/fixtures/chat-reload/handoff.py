"""Project the native handoff edge written by the engine's old transcript."""
from datetime import datetime
import json


def continuation_records(text, predecessor):
    edges = []
    for line in text.splitlines():
        if not line.strip():
            continue
        record = json.loads(line)
        if not isinstance(record, dict):
            raise ValueError("HANDOFF-RECORD: native JSONL record must be an object")
        if record.get("type") == "continued-in" and record.get("sessionId") == predecessor:
            if datetime.fromisoformat(record["timestamp"].replace("Z", "+00:00")).tzinfo is None:
                raise ValueError("HANDOFF-TIMESTAMP: native record must have a timezone")
            edges.append({key: record[key] for key in ("type", "sessionId", "continuedInSessionId")})
    return edges
