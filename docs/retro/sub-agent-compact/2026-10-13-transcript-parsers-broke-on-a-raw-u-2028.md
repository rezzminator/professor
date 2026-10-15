# 2026-10-13 — transcript parsers broke on a raw U+2028

Observed: Claude Code writes U+2028/U+2029 unescaped inside transcript JSON lines; sub-agent-compact's `^(\d+):(.*)$` parsers (look_back, priorPrompts, priorCalls) failed or silently skipped those lines. 520 unit tests were green; only a live call against a real transcript caught it.

Amend: any parser of `grep -n` transcript output matches with `[\s\S]*`, never `.`; a tool that reads transcripts gets one live run against a real transcript before it lands.
