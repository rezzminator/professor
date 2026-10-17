# 2026-09-23 — a model-copied return is not a verbatim return

In the codeprobe benchmarks the script's return file matched the repo on every line, every run. Final messages that re-emitted it did not: haiku retyping a collector return differed from the file on 31/710, 10/449, 11/453 and 61/431 lines, with shifted indentation and inserted lines; sonnet tracer and mapper rewrote the rendered return in 3 of 6 runs, merging rows, summarising lists and adding hedged or false prose ("only caller", "full detail above"). A prompt rule ("copy whole", a wrong/right example, a banner in the tool output) did not hold; a brief demanding the text inline overrode the agent prompt for haiku.

Amend: judgment — verbatim text travels by file the script wrote, named in a short receipt the model can copy; any return a caller must trust line by line is read from that file.
