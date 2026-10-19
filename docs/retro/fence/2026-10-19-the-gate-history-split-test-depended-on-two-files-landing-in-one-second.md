# 2026-10-19 — the gate-history split test depended on two files landing in one second

Observed: `infra/fence/lanes/tests/gate-history_test.sh` expected run.BADC before run.PFM in the concurrent.tsv sidecar after a split. Ingest orders runs oldest first by gate.tsv mtime with the path as tie-break, so the expectation held only when both files were written in the same second; under load the full gate went red on develop and blocked a host update. Fixed in 4575f730: both mtimes pinned with `TS`, the expectation set to the true oldest-first order, watched failing then passing.

Amend: judgment — a lane test whose assertion depends on ordering by mtime pins every mtime it orders with `TS`, never leaving it to the wall clock.
