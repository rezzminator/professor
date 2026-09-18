# Slow-test ledger

## Contents

- [Scope](#scope)
- [Records](#records)
- [Failed baseline tests](#failed-baseline-tests)

## Scope

The serial baseline at `8bfb6cdc` observed 2,332 top-level tests; 64 took at least one second. Those durations include host contention and are historical observations, not current isolated costs or accepted timing budgets. Current source bodies and their helpers determine each disposition below; several reload tests already use fake clocks and differ from the measured version.

Tier A retains real subprocess, tmux, shell, and protocol-boundary tests. Tier U retains local logic with temporary files/databases or injected dependencies. A mock-seam disposition preserves the unit assertion while replacing wall-clock pacing. These are placement decisions for the train, not claims that the tests have already moved.

All 64 source definitions were located on disk. The audit assigns 44 to Tier A, retains 18 in Tier U, and identifies two unit tests whose waits need seams. This replaces classification by keyword alone: subprocess timeouts are not CPU tests, and source fixtures mentioning HTTP do not issue network requests.

## Records

| Disposition | Source audit |
| --- | --- |
| 18 retained U and 2 mock-seam candidates | [Unit records](slow-tests-unit.md) |
| 44 Tier A placements | [Integration records](slow-tests-integration.md) |

## Failed baseline tests

`TestJailedEvalAttachFromPlainAndNestedTmux` failed in both unit baseline captures. The default-concurrency capture failed `eval/bunker/picker` and `raw/inside-tmux/picker`; the serial capture failed `raw/inside-tmux/picker`. `TestKillSelfResolveAndInternalCLI` also failed in the serial capture below the one-second threshold. These are failures to resolve, not successful timing evidence or quarantined passes. The captured failures establish occurrence under contention; they do not prove contention is the sole cause.
