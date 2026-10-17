# 2026-09-21 — Codex rejects symlinked role files: every machine-global agent type was unspawnable

Codex reads `agents/<name>.toml` O_NOFOLLOW (exec-server regular_file.rs); pfm linked them; doctor said `linked` over a dead registry. Executor spawns failed with "agent type is currently not available".

Resolved: 2026-09-21 — pfm writes marker-owned regular files into every Codex home, migrates links, retires orphans; doctor opens roles the way Codex does (7eb55795)
