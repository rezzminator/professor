# 2026-10-11 — a flights gate brief leaves out the lander's long run

Observed: the foreman's brief to the lander of a callmeter flight did not name the e2e run docs/testing.md assigns the lander (about 250 s); the lander found it itself, and a foreground call of that length blocks the lander's turn.

Amend: templates/global/agents/flights-foreman.md — the section that spawns the flights-lander: its gate brief names each project's long run the testing manual assigns the lander and says to run it in the background with stdin from /dev/null.
