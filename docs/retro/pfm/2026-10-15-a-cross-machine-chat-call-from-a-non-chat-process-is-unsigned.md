# 2026-10-15 — a cross-machine pfm chat call from a non-chat process was refused as unsigned

Observed: `pfm chat call` sent across machines from a process that is not a chat was refused as unsigned, and a display-name target did not resolve.

Amend: templates/global/commands/pfm.md — a call from a non-chat process sets `CHAT_SENDER_SESSION` and `CHAT_SENDER_LABEL` and targets the session id, not the display name.
