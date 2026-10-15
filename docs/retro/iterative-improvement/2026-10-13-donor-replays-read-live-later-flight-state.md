# 2026-10-13 — donor replays read live, later flight state

Observed: forward-handoff probes replaying foreman donors loaded the live flight directory (state written after the cut), producing WRONG grades (fhf-4, fhf-7) until `donor.py --map` reset each flight dir.

Amend: every donor replay maps its flight directory to a reset copy; a mapped copy lacks pre-cut files not written by Write/Edit, which reads as "not found", not as wrong.
