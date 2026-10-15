# 2026-10-11 — a host reboot wiped every tracer run, and the history `tt book` reads

Observed: a Mac crash and reboot cleared `/tmp`, the tracer runs root `/tmp/professor/tracer`. Every earlier run was gone: the 478-hop run open in the user's browser, the A/B seats' runs, the swatch book's history (46 runs before, 1 after) and every Loom page generated under `/tmp`. Only records copied to `~/.local/state/pfm/` survived.

Amend: templates/global/skills/tracetree/SKILL.md (runs root) — decide whether runs that a caller keeps (a bench, a book, a published page) belong under a durable root such as `~/.local/state/pfm/tracer/`, or whether `/tmp` stays and the caller copies what it keeps; or `judgment`.
