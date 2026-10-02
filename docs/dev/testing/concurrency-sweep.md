# Concurrency sweep

The unit gate uses `TESTFLAGS ?= -p 6 -parallel 4`. Among the four points in the 2026-10 hermetic sweep, it is the smallest point within 1.0 s of the fastest wall median whose memory.peak median is at most 2048 MB and whose swap median is zero. The tagged e2e gate keeps its separate `-p 1` setting. See [timing.md](timing.md) for the budget rule and failure semantics; the 2026-09 derivation captures below established the current unit timing budgets, which this sweep did not re-derive.

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

## Hermetic -p sweep (2026-10)

Each unit-alone run used `-parallel 4` and a fresh fence container. The pre-run fence count was zero for all twelve runs; every run passed and used zero swap. Wall, memory.peak, and swap medians select the smallest point within 1.0 s of the fastest wall median, provided memory.peak is at most 2048 MB and swap is zero. The timing-budget check belongs to task 30-w.

| Flags | Capture | Go test wall (s) | Result | 1-min load at start | Date (Europe/Berlin) | Role |
| --- | --- | ---: | --- | ---: | --- | --- |
| `-p 4 -parallel 4` | `/pfm-timing/fix-13-w/p4-1.json` | 34.837 | pass; 1380 MB peak; 0 MB swap | 2.80 | 2026-10-01 05:11 | before |
| `-p 4 -parallel 4` | `/pfm-timing/fix-13-w/p4-2.json` | 33.891 | pass; 1392 MB peak; 0 MB swap | 7.47 | 2026-10-01 05:12 | before |
| `-p 4 -parallel 4` | `/pfm-timing/fix-13-w/p4-3.json` | 33.957 | pass; 1294 MB peak; 0 MB swap | 8.42 | 2026-10-01 05:12 | before |
| `-p 6 -parallel 4` | `/pfm-timing/fix-13-w/p6-1.json` | 29.389 | pass; 1454 MB peak; 0 MB swap | 8.28 | 2026-10-01 05:13 | sweep |
| `-p 6 -parallel 4` | `/pfm-timing/fix-13-w/p6-2.json` | 29.184 | pass; 1424 MB peak; 0 MB swap | 9.98 | 2026-10-01 05:14 | sweep |
| `-p 6 -parallel 4` | `/pfm-timing/fix-13-w/p6-3.json` | 30.261 | pass; 1400 MB peak; 0 MB swap | 9.27 | 2026-10-01 05:14 | sweep |
| `-p 8 -parallel 4` | `/pfm-timing/fix-13-w/p8-1.json` | 30.831 | pass; 1560 MB peak; 0 MB swap | 9.44 | 2026-10-01 05:15 | sweep |
| `-p 8 -parallel 4` | `/pfm-timing/fix-13-w/p8-2.json` | 29.541 | pass; 1513 MB peak; 0 MB swap | 9.89 | 2026-10-01 05:15 | sweep |
| `-p 8 -parallel 4` | `/pfm-timing/fix-13-w/p8-3.json` | 31.432 | pass; 1610 MB peak; 0 MB swap | 10.53 | 2026-10-01 05:16 | sweep |
| `-p 10 -parallel 4` | `/pfm-timing/fix-13-w/p10-1.json` | 32.410 | pass; 1608 MB peak; 0 MB swap | 9.83 | 2026-10-01 05:16 | sweep |
| `-p 10 -parallel 4` | `/pfm-timing/fix-13-w/p10-2.json` | 32.675 | pass; 1629 MB peak; 0 MB swap | 9.22 | 2026-10-01 05:17 | sweep |
| `-p 10 -parallel 4` | `/pfm-timing/fix-13-w/p10-3.json` | 32.908 | pass; 1698 MB peak; 0 MB swap | 10.22 | 2026-10-01 05:18 | sweep |

| `-p` | Median wall (s) | Median memory.peak (MB) | Median swap (MB) |
| ---: | ---: | ---: | ---: |
| 4 | 33.957 | 1380 | 0 |
| 6 | 29.389 | 1424 | 0 |
| 8 | 30.831 | 1560 | 0 |
| 10 | 32.675 | 1629 | 0 |

The `-p 6` sweep median is 4.568 s below the `-p 4` before median. After the Makefile pin, three more `-p 6 -parallel 4` runs (`after-1`, `after-2`, `after-3`) passed with walls of 31.427, 29.475, and 28.791 s, all with zero pre-run fence containers and zero swap. Their 29.475 s median is 4.482 s below the before median. Raw captures are mounted from `/tmp/{project}/timing/fix-13-w/` on the host.

The unit block in `pfm/.testtiming.yml` uses twice the rounded-up maximum package and suite event span over the three derivation captures, with a one-second floor before doubling. All three derivation JSONs pass the new timing check for 76 packages; the independent validation also passes. The raw captures, check logs, and per-package before/capture/after table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`.

`-p 2` and `-parallel` values other than 4 were not measured in this sweep; it did not establish a quiet-host baseline.
