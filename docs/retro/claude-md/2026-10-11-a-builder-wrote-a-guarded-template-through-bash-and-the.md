# 2026-10-11 — a builder wrote a guarded template through Bash, and the guard never saw it

Observed: a general-purpose builder changing `templates/global/skills/tracetree/loom_wall.html` (commit `c1e55f14`) made its first edit with a Python script run through Bash. The PreToolUse guard gates Edit and Write only, so that write went through unchecked. When its next Edit was denied, the builder ran the unlock stamp the deny message names, and its later edits were guarded. The next builder's brief said "every template edit through the Edit tool, never Bash", and it complied.

Amend: CLAUDE.md#Process (Guarded files) — a guarded file is written through Edit or Write only, never through a shell command; and `.claude/scripts/pfm-guard.sh` — consider gating Bash commands that write under a guarded path.
