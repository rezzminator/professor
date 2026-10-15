# 2026-09-20 — open defects and parked jobs carried out of the OpenCode close-out

Observed: found during the live tests and the SPEC-C build, none fixed:

- `fleet.ResolveRow` (`fleet/scan.go`) renders its own errors as absence — a failed look reads as "no chat named".
- `pfm chat resolve` answers from a different resolver ladder than every other chat verb.
- `kill/obs.go` `requestShape` cannot tell an id target from a socket target.
- One `call` into an idle OpenCode pane answered "queued, busy"; never reproduced, cause unknown.
- A fresh Claude chat with 0 prompts is missing from `pfm ls`.
- WARN-level log noise (uv stderr) on ordinary runs.
- `scripts/check-opencode-writer.mjs` exists with zero invocation sites — wire it into a gate or delete it.
- `chat new --engine opencode` is refused: no OpenCode launcher is registered (belongs to the engine-parity train).

Parked, unstarted: the chat-ownership train (entry above); the bind-mount lane test; the 24 unmerged old branches (triage: merge, port, or delete each); the untracked `engines/` directory (track it or remove it); the themes eye-check.

Amend: judgment — each bullet is folded into the train that owns it (resolver bullets and the launcher → engine parity; the rest → a small fixes wave) when that train is refined.

Resolved: 2026-09-22 — chat-verb defects (ResolveRow absence, resolver ladders, kill requestShape, idle call) landed in chat-mcp; remaining OpenCode items queued in the parity flights; stale bullets: pfm install DOES write OpenCode MCP (installer/mcp.go mcpOpenCodeRegistration), check-opencode-writer.mjs IS invoked (dev.sh:311), .claude/skills/deep-rr twin no longer exists
