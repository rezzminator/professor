# 2026-10-15 — flight plugin-logs: "merged" was reported while four plugins were not live

Observed: Four plugins were not live at the report: one had no dev install, one ran a stale binary, one service lacked the env var.

Amend: templates/global/agents/flights-foreman.md (return) — done means rows flowing on the host, not commits on `develop`; the return names the live check that proved it.
