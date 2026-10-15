# 2026-10-11 — an agent's tools allowlist hid the self-compaction tool

Observed: a nudged flights-foreman ran from 143k to 264k without compacting: its `tools:` allowlist left out `mcp__sub-agent-compact__compact`, and its `<compact-now>` markers landed in messages that ended its turn at a wait, where the plugin arms nothing; the plugin's decision log held about 100 skipped asks and no armed row. Every agent but the rr family now lists the tool (`e6865dae`).

Amend: .claude/commands/pcm.md#Special Operations — a new agent with a `tools:` allowlist lists `mcp__sub-agent-compact__compact`, the rr family excepted.

Resolved: 2026-10-11 — .claude/commands/pcm.md and templates/project/commands/pcm.md, New agent
