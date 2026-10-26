# 2026-10-26 — gitter's manual counts five plugin submodules; the repo has ten

Observed: three gitter runs in one session each reported that `.gitmodules` registers ten `plugins/*` submodules (`harvester` among them) while the manual says five, and each re-derived the real set before acting.

Amend: .claude/agents/gitter.md#Submodules and line 20 — drop the count ("the `plugins/{name}` submodules `.gitmodules` registers"), so the manual cannot drift as plugins are added.
