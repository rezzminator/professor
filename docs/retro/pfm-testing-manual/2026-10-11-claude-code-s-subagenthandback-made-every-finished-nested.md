# 2026-10-11 — Claude Code's SubagentHandback made every finished nested agent read "working"

Observed: Claude Code now ends a sub-agent's turn with a SubagentHandback tool_use whose tool_result carries top-level `toolEndsTurn: true`; the statusline nest scan and the picker's rowfacts read any trailing user entry as an open turn, so a foreman row read 14/38 working with all 38 done. Fixed in c19fb949 (both doors), plus a per-role roster and no `solo` segment (3eb1cbbc).

Amend: .claude/commands/pfm-testing-manual.md — any code that infers a Claude transcript's turn state carries a fixture whose last entry is a handback tool_result with toolEndsTurn.
