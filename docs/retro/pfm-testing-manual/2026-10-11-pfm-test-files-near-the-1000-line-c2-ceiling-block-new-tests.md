# 2026-10-11 — pfm test files near the 1000-line C2 ceiling block new tests

Observed: claude_store_test.go (992) and main_test.go (998) could not grow; the tvc-store-entry flight found this only at the gate.

Amend: .claude/commands/pfm-testing-manual.md — before adding a test, `wc -l` the target; at or near the ceiling, put the test in a sibling file.
