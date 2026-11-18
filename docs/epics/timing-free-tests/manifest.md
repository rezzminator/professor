---
epic: timing-free-tests
status: IN_PROGRESS
created: 2026-11-18
updated: 2026-11-18
pipelines: []
flights: []
---

# Timing-free tests: no pfm test passes or fails by the clock

## Vision & Scope

The owner's ask, 2026-11-18: tests must not depend on timing and timeouts, so a loaded host stops faking red gates ("so we won't be having this headache"). The same day he ruled: land the first fix and park all the rest as this epic, to be worked later with a fresh token limit. The epic is open and paused after its first fix; nothing is in progress.

Done means: no pfm test passes or fails because of how much wall-clock time elapsed.

- A test of time-dependent logic owns a fake clock.
- A test that drives a real process (tmux, the tagged e2e tier, the journey) waits as a bounded poll on a named condition, never a fixed sleep. The law: `templates/global/rules/testing.md` ("Poll a named condition, bounded; never a fixed sleep or wait") and `.claude/rules/pfm-os-doors.md` ("A test waits on a named condition, never on the Linux fence's speed").
- The full gate passes at high host load without a TIMEOUT attributed to contention.

## Key Decisions

Owner rulings:

- 2026-11-18: tests must not depend on timing and timeouts, so a loaded host stops faking red gates.
- 2026-11-18: land the first fix, park the rest as this epic, work it later with a fresh token limit.

## Progress Log

- 2026-11-18 — session: first fix landed, the watch-ladder tests run on a fake clock; the rest parked as this epic (`8e52471d1`)

## Discoveries

- Inventory at develop `4a6faebc2`: 84 `_test.go` files under `pfm/` contain `time.Sleep(`. The count is an upper bound, not a fault list: a sleep that is the interval of a bounded poll on a named condition is allowed, a fixed sleep that lets time pass is the fault, so each file is classified before it is changed. Regenerate with `grep -rcE 'time\.Sleep\(' --include='*_test.go' pfm | grep -v ':0$'`.
- Files per package: `cmd/pfm` 20 · `internal/mockengine` 6 · `internal/hookentry` 6 · `internal/mcpserv` 5 · `internal/chat` 5 · `internal/headless` 4 · `internal/testjail` 3 · `internal/stats` 3 · `internal/deps` 3 · `internal/statusline` 2 · `internal/spawn` 2 · `internal/installer` 2 · `internal/herdr` 2 · `internal/call` 2 · `journey` 2 · `e2e` 2 · one each in `internal/usagehook`, `restart`, `resolve`, `reload`, `picker`, `index`, `gather`, `fleetdb`, `fleet`, `costsink`, `codexappendix`, `clock`, `binwatch`, `archive`, `action`.
- Heaviest files: `pfm/internal/headless/converse_test.go` 13 sleeps, `pfm/internal/resolve/tmux_jail_test.go` 6, `pfm/cmd/pfm/attach_jail_test.go` 6, `pfm/internal/hookentry/herdr_watch_test.go` 5.
- A second, older time override exists beside `pfm/internal/clock`: the scan clock `PFM_TEST_NOW_NS` in `pfm/internal/fleet/scan.go`.
- Two older tests in `pfm/internal/chat/fleetshare_test.go` still wait on the wall clock.
- Not verified for the first fix: macOS, and the Herdr native idle path.

## Open Questions

Awaiting the owner:

1. Whether the gate's time budget (`pfm/.testtiming.yml`) changes. Not yet asked.

## Files

- `update.md` — state of work, the pattern, the first slice, the order and how to work a slice.

Private, outside the repo, in `~/.local/state/pfm/flights/professor/timing-free-tests/`:

- `sleepers.txt` — the per-file sleep snapshot, `{path}:{count}` per line.
- `herdr-test-contention.patch` — an unproven 87-line patch for the first slice; reference only, see `update.md`.
