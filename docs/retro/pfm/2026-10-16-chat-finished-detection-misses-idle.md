# 2026-10-16 — no reliable "this chat finished its turn" signal for probe chats

Observed: in an iterative-improvement lab, `pfm chat watch` (glob, `--once`) repeated IDLE lines at about 90 s of quiet and printed socket resolve errors, `pfm ls` showed working chats as idle, and a transcript check for `"stop_reason":"end_turn"` on the last JSONL line never matched because Claude Code appends later records; a finished benchmark sat unreported for 30 minutes.

Amend: pfm chat watch — a turn-complete event per chat (end_turn followed by no new assistant record), usable on a list of names in one blocking call; until then, the iterative-improvement skill names the transcript method (final assistant end_turn with text, then quiet).
