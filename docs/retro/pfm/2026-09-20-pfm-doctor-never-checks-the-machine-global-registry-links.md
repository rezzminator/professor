# 2026-09-20 — pfm doctor never checks the machine-global registry links

Observed: `pfm install` links every `templates/global/{agents,commands,skills}` original into `~/.claude/` (and the generated variants, e.g. `super-rr`, from pfm's generated dir), but `pfm doctor` has no check over those links — `grep -n "symlink\|Lstat\|EvalSymlinks" pfm/internal/doctor/*.go` finds only the statusline and overlay checks. A missing link (a new original such as `sub-rr` / `sub-tracer` never linked), a dangling one, a link pointing outside the blueprint clone, or a real file shadowing the original (the hand-symlinked `tracer` case) all read as a healthy install; the first symptom is an `Agent type not found` mid-run.

Amend: `pfm/internal/doctor` — a registry-links check: enumerate the originals plus `variants.json` closed-world, and for each report `OK` / `MISSING` / `DANGLING` / `WRONG-TARGET {where it points}` / `SHADOWED (real file)`; a registry dir that cannot be read reports `UNREAD — {error}`, never a clean pass. Same walk for `~/.codex/agents/` twins.

Resolved: 2026-09-22 — doctor opens each Codex role the way Codex does (installed/SYMLINK/MISMATCH/MISSING/UNREADABLE) and audits role seats' prompt channel (7eb55795, b030e015)
