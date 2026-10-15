# 2026-09-16 — live-demo fence (infra/demo)

- `pfm install` harvester sidecar: `nvidia-cusparselt-cu13` is pinned for a platform linux-arm64 cannot install; the whole install fails unless `--skip-harvest`. The pin needs a platform marker or the sidecar a CPU-only fallback.
- `pfm chat new --engine ox` has no door: no headless planner, no spawn launcher, no naming for OpenCode — the picker's "New OpenCode chat" is the only spawn path. Three doors, one wave.
- Seats sharing one transcript store (`~/.cc/N/projects → ~/.claude/projects`, the layout `/reload --account N` needs) make every live row 🥇: compose attributes by transcript path. The pane's process env (`CLAUDE_CONFIG_DIR`) is the seat; gather reads it only for sub-agent rows today. The sub-agent lane now honours it (compose `accountForConfigDir`); the live-chat lane needs the pane env in the snapshot.
- Without an init system the MCP HTTP daemon (`pfm mcp serve`) never starts, and Codex rows boot with "1 MCP startup issue" and no chat tools; `infra/demo/daemon.sh` is the fence's stand-in. `pfm doctor` should name the daemon as DOWN, not leave it to Codex's banner.
- `pfm install` writes nothing for OpenCode: the host's `~/.config/opencode/opencode.jsonc` chat-MCP registration is hand-written with an absolute pfm path.
