# Slow-test ledger

## Contents

- [Scope](#scope)
- [Records](#records)
- [Capture outcome](#capture-outcome)

## Scope

The 2026-09-27 fenced `iso test pfm` capture (`-p 4 -parallel 4`, host 1-minute load 12.83 at start) observed 3,956 top-level tests; 82 took at least one second. These durations include host contention and are observations, not accepted timing budgets. Current source bodies and their helpers determine each disposition below. The 63 audited records remain listed when a test now takes less than one second.

Tier A retains real subprocess, tmux, shell, and protocol-boundary tests. Tier U retains local logic with temporary files/databases or injected dependencies. A mock-seam disposition preserves the unit assertion while replacing wall-clock pacing. These are placement decisions for the train, not claims that the tests have already moved.

All 63 source definitions were located on disk. The audit assigns 44 to Tier A, retains 17 in Tier U, and identifies two unit tests whose waits need seams. This replaces classification by keyword alone: subprocess timeouts are not CPU tests, and source fixtures mentioning HTTP do not issue network requests.

## Records

| Disposition | Source audit |
| --- | --- |
| 17 retained U and 2 mock-seam candidates | [Unit records](slow-tests-unit.md) |
| 44 Tier A placements | [Integration records](slow-tests-integration.md) |

## Capture outcome

The unit and tagged e2e suites passed. The capture recorded 3,904 passing and 52 listed skipped top-level tests, with no failures. The timing budgets passed for both suites.
