# 2026-10-18 — the kit's fork-copy scan read every Claude project per prep

Observed: the native kit's ownership scan (chatlogkit/cost/claude.py owners) walked the whole projects root (~3,000 directories, ~10 GB) and regex-tokenized each file, 26-35 s per prep; narrowed to the transcript's own project, gated by its session ids and birth time, it fell to 1-7 s with identical timelines (62c20f3e).

Amend: judgment — a kit step that scans beyond its one transcript is timed on the largest real project before it lands.
