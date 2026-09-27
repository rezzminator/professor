# Concurrency sweep

The unit gate uses `TESTFLAGS ?= -p 4 -parallel 4`. It is the fastest measured setting among the serial, default, and `-p 4 -parallel 4` probes, and three further passing fenced captures at the pinned flags supplied the current unit budgets. The tagged e2e gate keeps its separate `-p 1` setting. See [timing.md](timing.md) for the budget rule and failure semantics.

| Flags | Capture | Go test wall (s) | Result | 1-min load at start | Date (Europe/Berlin) | Role |
| --- | --- | ---: | --- | ---: | --- | --- |
| `-p 1 -parallel 1` | baseline suite 1 | 774.6 | tests pass; gate timing red | 32.63 | 2026-09-26 | serial probe |
| `-p 1 -parallel 1` | baseline suite 2 | 874.0 | pass | 65.63 | 2026-09-26 | serial probe |
| Go defaults | `probe-default` | 280.3 | pass | 22.89 | 2026-09-26 | probe before installer and injection changes |
| Go defaults | `r2-probe-default` | 274.7 | pass | 20.45 | 2026-09-26 | probe before installer and injection changes |
| `-p 4 -parallel 4` | `probe-p4` | 262.3 | pass | 16.43 | 2026-09-26 | probe before installer and injection changes |
| `-p 4 -parallel 4` | `r2-probe-p4` | 264.4 | pass | 26.01 | 2026-09-26 | probe before installer and injection changes |
| `-p 1 -parallel 1` | `speed-3a-serial` | 824.4 | tests pass | 52.00 | 2026-09-27 01:43 | before |
| `-p 4 -parallel 4` | `speed-3a-1` | 251.5 | tests pass; timing red against serial budgets | 19.55 | 2026-09-27 01:57 | derivation |
| `-p 4 -parallel 4` | `speed-3a-1-repeat` | 241.0 | tests pass; timing red against serial budgets | 26.08 | 2026-09-27 02:02 | derivation |
| `-p 4 -parallel 4` | `speed-3a-2` | 278.4 | tests pass; timing red against serial budgets | 18.25 | 2026-09-27 02:15 | derivation and after wall |
| `-p 4 -parallel 4` | `speed-3a-3` | 246.8 | tests and new timing check pass | 13.56 | 2026-09-27 02:22 | independent validation |

The unit block in `pfm/.testtiming.yml` uses twice the rounded-up maximum package and suite event span over the three derivation captures, with a one-second floor before doubling. All three derivation JSONs pass the new timing check for 76 packages; the independent validation also passes. The raw captures, check logs, and per-package before/capture/after table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`.

`-p 2`, `-p 8`, and a quiet host were not measured. A later complete sweep may move the pin if its passing captures and timing checks support the change.
