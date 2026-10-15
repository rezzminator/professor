# 2026-09-20 — headless OpenCode control flags are unverified on a real model (test authorized)

Observed: `--system`, `--json-schema` and `--tools` on `--engine opencode` were never proven end to end: the free model `opencode/big-pickle` refuses a replaced system prompt and the plugin controls ("free tier can only be used from within OpenCode"); pfm surfaces that cleanly (exit 4, no server left behind) but the controls themselves are untested.

Amend: no file — a verification job. The user AUTHORIZED spending paid-model prompts for it without asking again: one run per control on a paid OpenCode model (not the OpenAI quota while it is near its cap), asserting the system prompt is really replaced, the output validates against the schema, and a tool outside the allowlist is refused. A failure becomes its own entry.

Resolved: 2026-09-22 — proven by run in tmp/flights/engine-parity/experiments/opencode.md and tmp/flights/agent-role/experiments/opencode.md
