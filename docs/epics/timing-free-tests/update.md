# Timing-free tests — Work

## State of work

The epic is closed (`status: SHIPPED`, 2026-11-23): no step is in flight and none is next.

The sweep is landed: `develop` holds it as commit `4d43dbee4` (one fast-forward commit), and the host's pfm is rebuilt from it (`make -C pfm host-install`). The macOS run, `bash infra/fence/journey/run.sh --ci-mac` (GitHub Actions run 45144839658), passed on that commit on 2026-11-23: `journey (macos)`, `macos (unit)` and `macos (e2e)` all success, so the four macOS-only test files changed by the sweep have run on macOS and the macOS journey step of `.github/workflows/ci.yml` that calls `infra/fence/journey/budget.sh` is proven.

| Count | Before | Now |
| --- | --- | --- |
| Go real-clock sites in test, test-support and fixture files | about 880 in 341 of 1,745 files | 0 in 3,214 files |
| Shell real-clock sites | 108 in 22 test files | 0 in 84 files |
| Unit tests that made production code wait on the real clock | 379 rows in 21 of 103 packages | 0 |

- No test was skipped or renamed. One test left, `TestBudgetFailsOnlyARunOverIt`, because its subject moved to a shell test (`infra/fence/tests/journey-budget_test.sh`).
- The change is 637 paths: 577 modified, 82 new files; 69 production Go files gained a clock seam or one of the fixes under § Delivered.

Landed after it, in `14236aa30` on local `develop` (not pushed): the fleet database brings a schema once per process, and `internal/call` is back under its budget row. `internal/reload` still exceeds its 8 s row (§ Faster after the sweep); no row of `pfm/.testtiming.yml` was edited.

### Rulings that still bind a later change

1. Done means: no pfm test passes or fails because of how much wall-clock time elapsed, on a loaded host as on an idle one.
2. One wait door per language (`manifest.md` § The doors and the checks). A test waits on a NAMED condition; the failure prints its name and the last state observed. No test passes its own duration to the door.
3. The only wall-clock limit in the tests is the hung-only limit, 2 minutes, cut by `t.Deadline()`; it turns a hang into a named failure, never judges speed.
4. Time as the subject runs on a fake clock: one `clock.Fake` (`pfm/internal/clock`) handed to the code under test, file times pinned with `os.Chtimes`, the clock advanced to the armed timer (pattern: `pfm/cmd/pfm/chat_watch_ladder_test.go`).
5. An order between goroutines is a signal (channel, `sync.WaitGroup`, file or FIFO), never a pause or a yield.
6. "Nothing happened" is never proven by waiting; an absence assertion sits beside a presence assertion proving the code reached the point.
7. A fake process acts on a signal, never after a delay, on Linux and macOS alike.
8. Production keeps its real patience: a limit a real user meets is never raised or removed for a test; a production change for a test is a seam with unchanged behaviour.
9. The checks have no allowlist and no exception file; a spelling found beyond them joins the check.
10. A converted test proves what it proved before: same behaviour, same exact-value assertions, same name; none is deleted, skipped, retried or loosened.
11. Out of this sweep: the plugins' own suites, and any production limit.

Test-support packages the clock check also judges: `pfm/internal/mockengine`, `pfm/internal/hostfixture`, `pfm/internal/testjail`, `pfm/cmd/mock-engine`, `pfm/journey/harness`.

## Delivered

### The sweep, held by three checks

Every test wait goes through the doors and every check reads zero; see `manifest.md` for the doors, the checks and the three places real time is still read.

Proof:

- One lander reviewed the whole change, fixed 20 findings in the tree and returned FIXED: full gate `VERDICT PASS`, coverage 87.5 %, journey alone 100 steps in 120.5 s of 500 s, fresh install PASS.
- Under load, the fenced pfm test steps (`.claude/scripts/dev.sh iso test pfm`) pass beside 46 busy loops on a 23-core host at load 60 to 75, every step green including both time-budget steps: unit suite 71.7 s of 100 s, e2e 100.2 s of 150 s. The same run on a quiet host: 64.0 s and 109.9 s.

Speed, fenced:

| Package | Before | Now |
| --- | --- | --- |
| `internal/call` | 40 to 48 s | 24 s |
| `internal/reload` | 18 to 27 s | 10.5 s |
| `internal/hookentry` | 60 to 72 s | 33 s |
| `internal/chat` | 43 to 50 s | 17 s |

### Faster after the sweep (`14236aa30`)

- `initDatabase` (`pfm/internal/fleetdb/open.go`) reads one schema fingerprint per open (`schemaFingerprint`: `user_version` plus every `sqlite_master` row) and skips `bringToSchema` for a database whose fingerprint this process already holds in `schemasBrought`. `retireMovedTmuxSeats` and the newer-version refusal still run at every open, a fingerprint still holding the retired `issues` table is never held, and the connection is not cached.
- `internal/call` no longer exceeds its 16 s row and the gate no longer warns on it: the package alone in the fence 9.0 s (22.9 s before); two full gates on a loaded host (load average 49) 16.4 s and 19.1 s with no warning. No full gate on a quiet host was measured after the change.
- Cause cured in three tests: `TestJailedLongCompactFocusFiresWithFullTranscript` 8.89 s to 0.53 s and `TestJailedBusyCodexQueuesAndLongFileDeliversByPaste` 2.47 s to 0.82 s (their fixture panes open at 200x50; at tmux's 80x24 a long body scrolled the composer row off screen and the render-settle loop ran every try), and `TestHerdrBridgeTmuxGivesUpOnAFrozenServer` 3.01 s to 0.01 s (`bridgeTmux` in `pfm/internal/call/herdr_bridge.go` makes its bound through `gather.ProbeContext`; the test moves a fake clock).
- `internal/reload` still exceeds its 8 s row: 10.5 s on a quiet host before this change, 15.5 s and 18.0 s in the two loaded gates after it.

### Faults found and fixed in the same change

- `pfm chat reload` with no account names the account the reload really runs on; it printed the chat's birth account when an earlier queued reload had handed over another one (`pfm/cmd/pfm/chat_reload_command.go`).
- `pfm log --follow` keeps a half-written record until its newline arrives; it dropped the record (`pfm/internal/obs/logcmd.go`).
- A fleet database another pfm is still creating reads as "no primary account set" instead of an error (`pfm/internal/fleetdb/primary.go`).
- The Herdr indicator hook stands down quietly when Herdr's socket is removed while it runs; a socket absent from the start still errors (`pfm/internal/herdr/indicators_stop.go`).

### The watch ladder on a fake clock (`8e52471d1`)

The watch-ladder tests no longer sleep: one `clock.Fake` handed to the code, every transcript write pinned to fake time with `os.Chtimes`, the clock advanced to the armed timer, idle and quiet seconds fixed numbers compared as whole lines. `ScanShare` (`pfm/internal/chat/fleetshare.go`) takes a poll clock for its stamps and keeps the real clock for its waits; a nil poll clock is `clock.Real`.

Files: `pfm/cmd/pfm/chat_watch.go`, `pfm/cmd/pfm/chat_watch_discovery_jail_test.go`, `pfm/cmd/pfm/chat_watch_ladder_jail_test.go`, `pfm/cmd/pfm/chat_watch_ladder_test.go`, `pfm/internal/chat/fleetshare.go`, `pfm/internal/chat/fleetshare_test.go`, `pfm/.arch/substring-tests.txt`.
