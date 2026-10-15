# 2026-10-11 — a stat on pfm.db misses WAL commits; PRAGMA data_version is the change gate

Observed: pfm.db runs in WAL mode, and its mtime moves only at checkpoint. The cosmos ledger gate uses `PRAGMA data_version` plus an own-writes counter, because data_version does not move for the committing connection. The pool is one connection, and a second dedicated connection would deadlock it.

Amend: .claude/commands/pfm-testing-manual.md — a cross-process change gate on pfm.db is tested with a second connection and a forced `wal_checkpoint(TRUNCATE)` between inserts.
