# 2026-10-15 — a run.md watcher anchored at line start missed every unit DONE line

Observed: a run.md watcher matched `^DONE`, but the lines read `{unit} DONE`; it missed every one — a coincidence detector that reported a stall where the flight was progressing.

Amend: templates/global/agents/flights-foreman.md#run.md — a watcher matches the keyword anywhere in the line, and judges a stall by run.md age and live test processes together, never by one signal.
