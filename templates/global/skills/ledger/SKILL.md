---
name: ledger
description: Internal kit of the tracer and mapper agents — ledger.py surveys a repo for a target's spellings, verifies quoted fact rows against the code, widens by outward names, computes absences and lints the report. Not invoked directly; ask tracer or mapper.
---

`ledger.py` is the computed half of `tracer` and `mapper`; the `scribe` agent writes the rows it checks. Run `python3 ledger.py` for the command list. Python 3 standard library only; it reads the repo and writes only under the system temp directory.
