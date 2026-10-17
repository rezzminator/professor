# 2026-10-15 — flight plugin-logs: review artifacts never reached disk

Observed: Review artifacts for 8 of 10 units never reached disk, and a read-only audit agent could not write its report file.

Amend: templates/global/agents/flights-foreman.md and templates/global/commands/flights/doctor.md — an artifact a gate depends on is checked on disk when it is produced; an agent asked for a file holds a tool that can write it.
