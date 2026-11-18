# Timing-free tests — Work

## State of work

The epic is open and paused after its first fix. Nothing is in progress.

Live: the watch-ladder tests run on a fake clock (`8e52471d1`, `develop`, 2026-11-18). Everything else in the inventory of `manifest.md` § Discoveries is unclassified and unchanged.

Next steps, in order, when the epic resumes:

1. The first slice: the three Herdr tests below.
2. Classify `internal/mockengine` and `internal/testjail` first. They are test infrastructure, and a fix there can clear sleeps in their callers.
3. One slice per package area, in this order: `cmd/pfm` (20 files), `internal/hookentry`, `internal/mcpserv`, `internal/chat`, `internal/headless`, then the rest.
4. Ask the owner whether the gate's time budget (`pfm/.testtiming.yml`) changes.
5. Decide the older `PFM_TEST_NOW_NS` scan clock in `pfm/internal/fleet/scan.go` and the two wall-clock waits in `pfm/internal/chat/fleetshare_test.go` (see `manifest.md` § Discoveries).

## How to work a slice

- One slice per package area. Each is a flight in one worktree with one foreman and one lander, per `templates/global/rules/flights-life-cycle.md`.
- The brief names the pattern of § Delivered and the files of its slice.
- A slice is proven by watching its tests pass under load, not at rest.
- A change to an OS door's test or fake needs a green macOS run before it lands (`.claude/rules/pfm-os-doors.md`).
- Classify each file before changing it: a sleep that is the interval of a bounded poll on a named condition stays; a fixed sleep that lets time pass is the fault.

## The first slice: three Herdr tests

Three Herdr tests fail on macOS in the full gate's `pfm.unit-proof` step under CPU contention:

- `TestHerdrCallNative` in `pfm/internal/call/herdr_delivery_test.go`.
- `TestHerdrScheduleSurvivesRestart` in `pfm/cmd/pfm/chat_schedule_restart_test.go` (a socket i/o timeout).
- `TestBirthHerdrAwaitsTranscriptAndAttachesNativePane` in `pfm/internal/chat/birth_test.go`.

The macOS failure logs are not on this host.

An unproven patch exists, kept privately at `~/.local/state/pfm/flights/professor/timing-free-tests/herdr-test-contention.patch` (87 lines: three test files plus one production change, `dialTimeout` in `pfm/internal/herdr/wire.go`, 500 ms to 5 s). It was NOT applied. It raises wall-clock bounds instead of removing the dependence on timing, and none of it was proven; its builder was stopped after two hours of stress runs. It shows where the tests wait; it is not the fix.

## Delivered

### The watch ladder on a fake clock (`8e52471d1`)

The watch-ladder tests no longer sleep. The pattern every later slice copies:

- The test owns one `clock.Fake` (`pfm/internal/clock`) and hands it to the code under test.
- It pins every transcript write to fake time with `os.Chtimes`.
- It advances the clock to the armed timer instead of sleeping.
- Idle and quiet seconds are fixed numbers compared as whole lines.

One production change rode along with unchanged output: `ScanShare` (`pfm/internal/chat/fleetshare.go`) takes a poll clock for its stamps and keeps the real clock for its waits; a nil poll clock is `clock.Real`.

Files: `pfm/cmd/pfm/chat_watch.go`, `pfm/cmd/pfm/chat_watch_discovery_jail_test.go`, `pfm/cmd/pfm/chat_watch_ladder_jail_test.go`, `pfm/cmd/pfm/chat_watch_ladder_test.go`, `pfm/internal/chat/fleetshare.go`, `pfm/internal/chat/fleetshare_test.go`, `pfm/.arch/substring-tests.txt`.

Not verified: macOS, and the Herdr native idle path.
