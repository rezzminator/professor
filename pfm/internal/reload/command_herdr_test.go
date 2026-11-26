package reload

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestRunHerdrRejectsMissingTarget(t *testing.T) {
	_, err := runHerdrOnFake(t, context.Background(), Request{}, Options{}, io.Discard)
	if err == nil || err.Error() != "native Herdr reload: socket and pane are required" {
		t.Fatalf("RunHerdr empty target error = %v, want explicit target validation", err)
	}
}

func TestRunHerdrLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want string
		draft, then      bool
	}{
		{name: "model-and-account"},
		{name: "dim-hint", mode: "dim-hint"},
		{name: "same-text-draft", draft: true},
		{name: "pending-cleared-before-new-process", mode: "pending-race"},
		{name: "stashed-draft", draft: true, then: true},
		{name: "same-target-then", then: true},
		{name: "failed-start", mode: "failed-start", want: "permission denied"},
		{name: "old-process-is-not-success", mode: "old-process", want: "new engine"},
		{name: "new-process-wrong-model", mode: "wrong-model", want: "new engine"},
		// The seat host names Remote Control after the seat at every start; a
		// recipe published with a bare --remote-control is still the one running.
		{name: "seat-host-names-remote-control", mode: "seat-host-names-remote-control"},
		{name: "process-error", mode: "process-error", want: "process unavailable"},
		{name: "malformed-process", mode: "malformed-process", want: "shell identity"},
		{name: "wrong-pane", mode: "wrong-pane", want: "shell identity"},
		{
			name: "empty-process", mode: "empty-process",
			want: "native reload: target engine is not running: pane w6:p3 lists no foreground process",
		},
		// A child Claude native runs under argv0 rg is never taken for the engine.
		{
			name: "bundled-rg-is-not-the-engine", mode: "bundled-rg-alone",
			want: `native reload: target engine is not running: pane w6:p3 lists pid 303 name "2.1.330" ` +
				`argv0 "rg" argv ["rg" "--files"]`,
		},
		{name: "bundled-rg-listed-before-the-engine", mode: "bundled-rg-first"},
		{name: "omitted-process", mode: "omitted-process", want: "engine"},
		{name: "null-argv", mode: "null-argv", want: "argv"},
		// Herdr lists a live Claude by its version name with an empty argv now
		// and then: the reload reads again, never calls the chat not running.
		{name: "version-named-row-without-argv-read-again", mode: "version-named-no-argv-once"},
		{
			name: "version-named-row-never-carries-argv", mode: "version-named-no-argv",
			want: "native reload: target engine could not be proven: 3 reads of pane w6:p3 listed it without a valid " +
				`pid/argv; the last listed pid 3300341 name "2.1.330" argv0 "" argv []`,
		},
		// After /exit the old engine dies: a dying process has no argv for a
		// moment, so the wait reads again; one that never clears is the refusal.
		{name: "dying-old-engine-read-again", mode: "argv-gone-once"},
		{
			name: "engine-argv-never-returns", mode: "argv-gone",
			want: "native reload engine process lacks a valid pid/argv; startup cannot be proved",
		},
		{name: "snapshot-error", mode: "read-error", want: "snapshot unavailable"},
		{name: "blocked", mode: "blocked", want: "blocked"},
		// Claude reports working while background shells or sub-agents run behind
		// an idle composer; the screen decides idleness, as on the tmux path.
		{name: "working-behind-idle-composer", mode: "working-behind-idle-composer"},
		{
			name: "working-turn-holds-exit", mode: "working-turn",
			want: "native reload: chat still busy or input box not ready; /exit was not typed",
		},
		{name: "unrecognized-agent-state-waits", mode: "unrecognized-agent-once"},
		{name: "undecodable-agent-state-waits", mode: "undecodable-agent-once"},
		// Herdr holds a Claude it just found in "unknown" for its startup grace
		// (3 s): a reload typed then waits it out.
		{name: "unknown-agent-state-waits", mode: "unknown-agent-once"},
		{
			name: "unknown-agent-state-never-clears", mode: "unknown-agent",
			want: "native reload: Herdr never reported the chat's state (native reload target is unknown); /exit was not typed",
		},
		{
			name: "unrecognized-agent-state-never-clears", mode: "unrecognized-agent",
			want: `native reload: Herdr never reported the chat's state (native reload unknown agent state "thinking"); /exit was not typed`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			f.mode = tc.mode
			if tc.mode == "dim-hint" {
				f.text = "❯ \x1b[2mTry running tests\x1b[0m"
			}
			switch tc.mode {
			case "working-behind-idle-composer":
				f.text = "────────\n❯ \n────────\n  ⏵⏵ bypass permissions on · 2 shells"
			case "working-turn":
				f.text = "✻ Thundering… (1m 50s · ↓ 9.3k tokens)\n────────\n❯ \n────────\n" +
					"  ⏵⏵ bypass permissions on · esc to interrupt"
			}
			if tc.draft {
				f.text = "❯ parked draft"
				if tc.name == "same-text-draft" {
					f.text = "❯ Try running tests"
				}
			}
			if tc.then {
				f.request.Then = "resume the port"
			}
			var exitClock *clock.Fake
			if tc.mode == "old-process" || tc.mode == "version-named-no-argv" {
				// The idle check sleeps one 1 s poll between its two stable reads; the
				// startup give-up then sleeps its poll at each of its 5+3 tries.
				exitClock = clock.NewFake(time.Unix(0, 0))
				f.options.Clock, f.options.Poll = exitClock, time.Second
			}
			var stderr strings.Builder
			result, err := runHerdrOnFake(t, context.Background(), f.request, f.options, &stderr)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("RunHerdr error=%v, want %q", err, tc.want)
				}
				// Once /exit is submitted the seat host starts the new recipe, so
				// its account is recorded; a reload refused before it records none.
				switch tc.mode {
				case "old-process":
					if got := exitClock.Now().Sub(time.Unix(0, 0)); got != 9*time.Second {
						t.Fatalf(
							"startup give-up: fake clock %v past its start, want 9s (1s idle check, 5+3 tries x 1s)",
							got,
						)
					}
				case "version-named-no-argv":
					// Three reads, the clock's 1 s poll between each, nothing typed.
					got := exitClock.Now().Sub(time.Unix(0, 0))
					if err.Error() != tc.want || got != 2*time.Second || len(f.typed) != 0 || f.badReads != 8 {
						t.Fatalf("unprovable engine: error=%q clock=%v typed=%q bad reads=%d, "+
							"want %q, 2s, nothing typed, 8 reads", err, got, f.typed, f.badReads, tc.want)
					}
					f.assertLaunchRecorded(false)
				case "empty-process", "bundled-rg-alone":
					// The refusal names the pane and every row the read listed; nothing is typed.
					if err.Error() != tc.want || len(f.typed) != 0 {
						t.Fatalf("engine not running: error=%q typed=%q, want %q, nothing typed", err, f.typed, tc.want)
					}
					f.assertLaunchRecorded(false)
				case "failed-start":
					f.assertLaunchRecorded(true)
				case "blocked":
					f.assertLaunchRecorded(false)
				case "working-turn":
					f.assertLaunchRecorded(false)
					if err.Error() != tc.want || len(f.typed) != 0 || f.agentReads != f.options.IdleTries {
						t.Fatalf(
							"busy turn: error=%q typed=%q agent reads=%d, want %q, nothing typed, %d reads",
							err, f.typed, f.agentReads, tc.want, f.options.IdleTries,
						)
					}
				}
				if tc.mode == "failed-start" {
					current, found, readErr := f.db.Seat(context.Background(), f.seat.SeatID)
					if readErr != nil || !found || current.State != "failed" || !current.ReloadPending ||
						!strings.Contains(f.text, "permission denied") {
						t.Fatalf("failed row=%+v screen=%q error=%v", current, f.text, readErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("RunHerdr: %v", err)
			}
			if result.Account != 2 {
				t.Fatalf("result=%+v", result)
			}
			f.assertLaunchRecorded(true)
			// Herdr ignores its own hook's "startup" report on a pane it tracks
			// Claude in; the reload hands it the reborn session as a resume.
			wantReport := "source=herdr:claude agent=claude session=" + f.request.SessionID +
				" start=resume seq-clock=0"
			if !slices.Equal(f.sessionReports, []string{wantReport}) {
				t.Fatalf("Herdr session reports=%q, want [%q]", f.sessionReports, wantReport)
			}
			current, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
			if err != nil || !found || current.HerdrPane != f.request.Pane || current.ReloadPending ||
				current.State != "live" {
				t.Fatalf("seat=%+v found=%t err=%v", current, found, err)
			}
			run, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			spawn, err := claudeSpawn(f.request)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := spawn.SeatRun(f.request.CWD)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(run, canonical) {
				t.Fatalf("recipe=%+v, canonical=%+v", run, canonical)
			}
			if tc.mode == "working-behind-idle-composer" && !slices.Equal(f.typed, []string{"/exit"}) {
				t.Fatalf("typed=%q, want [/exit]", f.typed)
			}
			if tc.mode == "dim-hint" && slices.Contains(f.keys, "ctrl+s") {
				t.Fatalf("dim hint stashed as draft: %v", f.keys)
			}
			if tc.draft && !slices.Contains(f.keys, "ctrl+s") {
				t.Fatalf("draft not stashed: %v", f.keys)
			}
			if tc.then && !strings.Contains(f.prompted, f.request.Then) {
				t.Fatalf("then=%q", f.prompted)
			}
			if tc.mode == "argv-gone-once" && f.processes < 4 {
				t.Fatalf("returned after %d process reads, want the dying engine read again", f.processes)
			}
			if tc.mode == "version-named-no-argv-once" && f.badReads != 4 {
				t.Fatalf("served %d version-named rows without argv, want 4", f.badReads)
			}
			if tc.mode == "pending-race" && f.processes < 3 {
				t.Fatalf("returned on old process after %d probes", f.processes)
			}
		})
	}
}

func TestRunHerdrPublicationAndCancellation(t *testing.T) {
	for _, kind := range []string{"missing-recipe", "missing-seat", "publish-failure", "cancel-before", "cancel-after", "pending-write-failure", "already-pending", "failed-retry", "failed-retry-delayed", "failed-retry-failed", "concurrent"} {
		t.Run(kind, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ""
			var lockClock *clock.Fake
			switch kind {
			case "missing-recipe":
				if err := f.db.DeleteSeatRun(f.seat.SeatID); err != nil {
					t.Fatal(err)
				}
				want = "seat run"
			case "missing-seat":
				if err := f.db.DeleteSeat(ctx, f.seat.SeatID); err != nil {
					t.Fatal(err)
				}
				want = "seat"
			case "publish-failure":
				// A seat row retired while reload waited is a chat that was ended.
				f.onRead = func() {
					if err := f.db.DeleteSeat(ctx, f.seat.SeatID); err != nil {
						t.Error(err)
					}
				}
				want = "the chat was ended while reload waited"
			case "cancel-before":
				cancel()
				want = "canceled"
			case "cancel-after":
				f.mode = "pending-kept"
				f.onExit = cancel
				want = "canceled"
			case "pending-write-failure":
				db, err := sqlitedb.OpenStore(ctx, filepath.Join(f.request.Home, "state", "pfm.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				if _, err := db.ExecContext(
					ctx,
					`CREATE TRIGGER deny_reload BEFORE UPDATE OF reload_pending ON seat WHEN new.reload_pending=1 BEGIN SELECT RAISE(FAIL, 'pending publication denied'); END`,
				); err != nil {
					t.Fatal(err)
				}
				want = "pending publication denied"
			case "already-pending":
				f.seat.ReloadPending = true
				if err := f.db.PutSeat(ctx, f.seat); err != nil {
					t.Fatal(err)
				}
				want = "pending"
			case "failed-retry", "failed-retry-delayed", "failed-retry-failed":
				f.mode = kind
				if kind == "failed-retry-failed" {
					want = "permission denied"
				}
				f.seat.State = "failed"
				f.seat.ReloadPending = true
				f.seat.LostNote = "previous failure"
				if err := f.db.PutSeat(ctx, f.seat); err != nil {
					t.Fatal(err)
				}
				f.text = "press Enter to retry, q to close"
			case "concurrent":
				// The public mutex remains observable; keep a real first owner until this request refuses.
				if err := os.MkdirAll(f.options.SIDDir, 0o700); err != nil {
					t.Fatal(err)
				}
				// Filled by the native lock path, not the tmux basename lock.
				key := LockPath(
					f.options.SIDDir,
					fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(f.request.SocketPath+"\x00"+f.request.Pane))),
					"seat",
				)
				lock, err := os.OpenFile(key, os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Close() }()
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				want = "another reload"
				// The give-up sleeps its poll between tries: five tries of 1 s.
				lockClock = clock.NewFake(time.Unix(0, 0))
				f.options.Clock, f.options.Poll = lockClock, time.Second
			}
			_, err := runHerdrOnFake(t, ctx, f.request, f.options, io.Discard)
			if want != "" {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error=%v want=%q", err, want)
				}
				if kind == "concurrent" {
					if got := lockClock.Now().Sub(time.Unix(0, 0)); got != 5*time.Second {
						t.Fatalf("lock give-up: fake clock %v past its start, want 5s (5 tries x 1s poll)", got)
					}
				}
				if strings.HasPrefix(kind, "cancel") && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation identity lost: %v", err)
				}
				if kind == "publish-failure" || kind == "pending-write-failure" {
					run, readErr := f.db.ReadSeatRun(f.seat.SeatID)
					if readErr != nil || !reflect.DeepEqual(run, f.old) {
						t.Fatalf("unpublished recipe=%+v err=%v", run, readErr)
					}
				}
				if kind == "cancel-after" {
					current, found, readErr := f.db.Seat(context.Background(), f.seat.SeatID)
					if readErr != nil || !found || !current.ReloadPending {
						t.Fatalf("canceled reload lost pending: %+v %v", current, readErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			current, found, err := f.db.Seat(ctx, f.seat.SeatID)
			if err != nil || !found || current.State != "live" || current.ReloadPending {
				t.Fatalf("retried row=%+v found=%t err=%v", current, found, err)
			}
		})
	}
}

func TestRunHerdrUnsafePublication(t *testing.T) {
	for _, kind := range []string{"failed-seat-with-live-engine", "binding-changes-during-idle"} {
		t.Run(kind, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			want := ""
			switch kind {
			case "failed-seat-with-live-engine":
				f.seat.State = "failed"
				f.text = "press Enter to retry, q to close"
				if err := f.db.PutSeat(context.Background(), f.seat); err != nil {
					t.Fatal(err)
				}
				want = "engine is still running"
			case "binding-changes-during-idle":
				f.onRead = func() {
					before, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
					if err != nil || !found {
						t.Errorf("read before rebind: %v", err)
						return
					}
					after := before
					after.HerdrPane = "w9:p9"
					after.HerdrTerminalID = "rebound-terminal"
					if err := f.db.UpdateSeat(context.Background(), before, after); err != nil {
						t.Error(err)
					}
				}
				want = "binding changed"
			}
			_, err := runHerdrOnFake(t, context.Background(), f.request, f.options, io.Discard)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v want=%q", err, want)
			}
			run, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil || !reflect.DeepEqual(run, f.old) {
				t.Fatalf("unpublished recipe=%+v want=%+v err=%v", run, f.old, err)
			}
		})
	}
}

func (f *reloadHerdrFixture) composer() string {
	if f.request.Engine == pfmengine.Codex {
		return "› "
	}
	return "❯ "
}

func TestRunHerdrCodex(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want string
		fresh            bool
	}{
		{name: "resume-model-account-effort"},
		{name: "retained-account"},
		{name: "unnamed-seat"},
		{name: "new-session", fresh: true},
		{name: "config-dropped-env"}, // the chat and its full-copy recipe hold a renamed variable
		{name: "failed-start", mode: "failed-start", want: "permission denied"},
		{name: "old-process-is-not-success", mode: "old-process", want: "new engine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			f.request.Engine = pfmengine.Codex
			f.request.Model = "native-model"
			f.request.New = tc.fresh
			f.request.CodexHome = filepath.Join(f.request.Home, "account-two")
			f.request.CodexBinary = filepath.Join(f.request.Home, pfmengine.MustLookup(pfmengine.Codex).Binary)
			f.request.CodexYolo = false
			f.request.PromptChannel = "Keep the seat contract."
			f.request.Machine.Version = pfmconfig.Version
			f.request.Machine.Codex = pfmconfig.CodexPrefs{Binary: f.request.CodexBinary}
			f.request.Machine.CodexAccounts = []pfmconfig.CodexAccount{
				{ID: 1, Home: filepath.Join(f.request.Home, "account-one")},
				{ID: 2, Home: f.request.CodexHome},
			}
			f.seat.Engine = string(pfmengine.Codex)
			if tc.name == "unnamed-seat" {
				f.seat.Name = ""
			}
			f.old.Binary = f.request.CodexBinary
			f.old.Args = []string{"resume", f.request.SessionID}
			wantAccount, wantHome := f.request.Account, f.request.CodexHome
			if tc.name == "retained-account" {
				before := f.request
				before.Account = 1
				var err error
				f.old, err = nativeReloadRun(before, f.seat.SeatID)
				if err != nil {
					t.Fatal(err)
				}
				f.request.AccountGiven = false
				wantAccount, wantHome = 1, f.request.Machine.CodexAccounts[0].Home
			}
			if tc.name == "config-dropped-env" {
				f.configDroppedEnv()
				f.old = f.fullCopy(f.old)
			}
			f.mode = tc.mode
			f.text = f.composer()
			if err := f.db.PutSeat(context.Background(), f.seat); err != nil {
				t.Fatal(err)
			}
			if err := f.db.WriteSeatRun(f.seat.SeatID, f.old); err != nil {
				t.Fatal(err)
			}
			var stderr strings.Builder
			result, err := runHerdrOnFake(t, context.Background(), f.request, f.options, &stderr)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("RunHerdr error=%v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunHerdr: %v", err)
			}
			run, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			for _, word := range []string{"--model", f.request.Model, "model_reasoning_effort=\"high\"", "developer_instructions=\"\"\"\nKeep the seat contract.\"\"\""} {
				if !slices.Contains(run.Args, word) {
					t.Fatalf("argv=%q missing %q", run.Args, word)
				}
			}
			if result.Account != wantAccount || run.Binary != f.request.CodexBinary ||
				!slices.Contains(run.Env, "CODEX_HOME="+wantHome) {
				t.Fatalf("recipe=%+v", run)
			}
			if !tc.fresh && (!slices.Contains(run.Args, "resume") || !slices.Contains(run.Args, f.request.SessionID)) {
				t.Fatalf("resume argv=%q", run.Args)
			}
			if tc.name == "config-dropped-env" {
				assertDroppedEnv(t, run, "AUTO_EFFORT_JUDGE=true", "CODEX_HOME="+wantHome)
			}
			current, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
			if err != nil || !found || current.HerdrPane != f.request.Pane || current.ReloadPending ||
				current.State != "live" {
				t.Fatalf("seat=%+v found=%t err=%v", current, found, err)
			}
			if tc.fresh && (!result.New || current.SessionID != "" || slices.Contains(run.Args, "resume")) {
				t.Fatalf("new result=%+v seat=%+v args=%q", result, current, run.Args)
			}
		})
	}
}

func TestRunHerdrNamesDraftInRefusal(t *testing.T) {
	for _, stage := range []string{"before-exit", "after-resume"} {
		t.Run(stage, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			draft := strings.Repeat("界", 80) + "TAIL_NOT_LOGGED"
			if stage == "before-exit" {
				f.text = "❯ " + draft
				f.mode = "stash-refused"
			} else {
				f.request.Then = "wake up"
				f.onExit = func() { f.text = "❯ " + draft }
			}
			_, err := runHerdrOnFake(t, context.Background(), f.request, f.options, io.Discard)
			if err == nil || !strings.Contains(err.Error(), strings.Repeat("界", 80)) ||
				strings.Contains(err.Error(), "TAIL_NOT_LOGGED") {
				t.Fatalf("RunHerdr error = %v; want bounded draft evidence", err)
			}
			if f.prompted != "" || f.text != "❯ "+draft {
				t.Fatalf("draft changed or wake delivered: text=%q prompted=%q", f.text, f.prompted)
			}
		})
	}
}
