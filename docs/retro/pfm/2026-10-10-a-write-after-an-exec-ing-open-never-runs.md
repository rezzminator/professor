# 2026-10-10 — a write after an exec-ing open never runs

Observed: the picker's launch memory was written after `chat.OpenRow` returned; in a real terminal OpenRow execs the attach, so the write never ran. Tests passed because the jail's open returns. flights-lander caught it.

Amend: pfm/CLAUDE.md#Code Standards — any state an action must record is written before the call that may exec (OpenRow, action.Dispatch), and rolled back on its error; a test that calls the open through a returning fake proves nothing about the exec path.
