# 2026-10-11 — per-agent prompt-cache TTL is impossible; GPT-backed agents miss cache 13% of consecutive calls

Observed: CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL is read process-wide: a live probe on Claude Code 2.1.296 with 4 concurrent sub-agents saw a one-agent raise reach 5 of the other agents' 9 requests, so cache-live-control 0.4.0 ships a shared raise (agentRaiseWaiting) instead. A month's sub-agent transcripts (2026-09-09..10-11) showed 321 expired-cache rewrites, $137.74 at API prices (long tool / background shell $72.82, waiting on a child $35.91, resumed via SendMessage $28.48); separately, gpt-6-sol / gpt-6.1-sol agents missed cache on 12.97% of consecutive in-TTL calls vs 0.29% for Claude, $158.44 — more than all expiries.

Amend: open — the GPT-backed agents' cache-miss rate needs its own investigation in the gpt-gateway / gpt-subagent-router path (docs/dev/gpt-subagent-router.md).
