# 2026-10-18 — investigators hand-wrote census scripts over raw JSONL

Observed: in one heavy investigation chat about 177 calls (~$34) went to hand-written tree, census and cost scripts over raw JSONL, pushing 7 of 9 investigators past their 40-call budget; the brief asked for cross-transcript counts the kit could not give, and `read` printed nothing (exit 0) past the digest.

Amend: templates/global/agents/agent-investigator.md — raw transcript reads forbidden except to diagnose a kit fault, the investigator maintains the kit (3fffb820); `read` fails out loud (09f9c15a); a `census` verb gives cross-transcript counts.

Resolved: 2026-10-18 — landed in 3fffb820 and 09f9c15a; census in flight chatlog-census.
