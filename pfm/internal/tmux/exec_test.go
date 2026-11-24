package tmux

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// fakeTmux is a deps.FakeRunner playing tmux: found on the runner's PATH, it
// prints its arguments and exits with the code the test names.
func fakeTmux(exit int) *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath(Binary, "/usr/bin/tmux", nil)
	fake.Handle([]string{Binary}, func(_ context.Context, call deps.RunCall) (deps.RunResult, error) {
		return deps.RunResult{Stdout: []byte(strings.Join(call.Argv[1:], " ") + " "), ExitCode: exit}, nil
	})
	return fake
}

// hangingTmux is a tmux that never answers, as a client of a stopped server
// waits: only its caller's deadline ends it.
func hangingTmux() *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath(Binary, "/usr/bin/tmux", nil)
	fake.Handle([]string{Binary}, func(ctx context.Context, _ deps.RunCall) (deps.RunResult, error) {
		<-ctx.Done()
		return deps.RunResult{ExitCode: -1}, ctx.Err()
	})
	return fake
}

// panes is the output a list-panes client printed before it exited.
const panes = "%0\n%1\n"

// errHeldPipe is how deps.RealRunner returns a tmux client that exited 0 while
// its server kept the client's output pipe open past the wait delay.
var errHeldPipe = fmt.Errorf("run %q: %w", Binary, exec.ErrWaitDelay)

// answeringTmux is a deps.FakeRunner playing tmux, found on the runner's PATH,
// that answers every call as answer says.
func answeringTmux(answer func(ctx context.Context) (deps.RunResult, error)) *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath(Binary, "/usr/bin/tmux", nil)
	fake.Handle([]string{Binary}, func(ctx context.Context, _ deps.RunCall) (deps.RunResult, error) {
		return answer(ctx)
	})
	return fake
}

// heldPipe answers as a client that printed panes and exited 0 while its
// server held the pipe: the output, exit -1 and the error as RealRunner reads them.
func heldPipe(context.Context) (deps.RunResult, error) {
	return deps.RunResult{Stdout: []byte(panes), ExitCode: -1}, errHeldPipe
}

// reached waits until the fake runner has recorded a call: the client is at the runner.
func reached(t *testing.T, fake *deps.FakeRunner) {
	t.Helper()
	testwait.Until(t, "the tmux client to reach the runner", func() (bool, string) {
		calls := len(fake.Calls())
		return calls > 0, fmt.Sprintf("%d runner calls", calls)
	})
}

func oneRecord(t *testing.T, recorder *obs.Recorder) obs.Record {
	t.Helper()
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want exactly one per invocation: %s", len(records), recorder.Raw())
	}
	return records[0]
}

// TestExecOutputRecordsSubcommandTargetExitOnce: Output completes the
// command and writes ONE comp=tmux record — subcmd, target, exit, dur_ms —
// while the arguments (a send-keys body is a prompt) never reach the file.
// The record's clock is pinned with obs.WithTestClock so dur_ms is asserted
// on an EXACT value, not merely checked present: Cmd reads its clock through
// ctx (obs.Clock) at both begin() and finish(), and a fake that never
// advances between the two proves that — the previous "found" check would
// have passed identically whether the wrapper read the fake clock or the
// wall clock underneath it.
func TestExecOutputRecordsSubcommandTargetExitOnce(t *testing.T) {
	ctx, recorder := obs.Test(t, obs.WithTestClock(clock.NewFake(time.Unix(1700000000, 0))))
	output, err := Exec(
		WithClientRunner(ctx, fakeTmux(0)),
		Binary,
		"/sockets/cc-1",
		"send-keys",
		"-t",
		"%7",
		"PLANTED prompt body",
	).Output()
	if err != nil || !strings.Contains(string(output), "PLANTED") {
		t.Fatalf("Output = %q, %v — the wrapper changed the command's result", output, err)
	}
	record := oneRecord(t, recorder)
	if record.Message != "tmux.exec" || record.Level != slog.LevelDebug.String() {
		t.Fatalf("record = %s at %s, want tmux.exec at DEBUG (a routine success)", record.Message, record.Level)
	}
	want := map[string]any{
		obs.FieldComp: "tmux", "subcmd": "send-keys", "target": "%7",
		obs.FieldExit: float64(0), obs.FieldDur: float64(0),
	}
	for key, want := range want {
		if got, found := record.Field(key); !found || got != want {
			t.Fatalf("%s = %v (found %t), want %v", key, got, found, want)
		}
	}
	if strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("a send-keys body reached the file: %s", recorder.Raw())
	}
}

// TestExecRecordsAtTheLevelOfItsOutcome: a clean, fast tmux call is DEBUG (it
// was the bulk of every log line); one that took obs.SlowCall or longer, and
// a tmux that answered with a non-zero exit, are INFO; a tmux that never
// started is ERROR; a call its deadline ended — a stopped or wedged server
// that never answered, whichever way exec reports it — is WARN, the server's
// fault its caller classifies (gather.ErrServerUnresponsive), never pfm's.
func TestExecRecordsAtTheLevelOfItsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runner    func() *deps.FakeRunner
		elapsed   time.Duration
		cancelled bool
		// deadline bounds the call: 0 none, negative already past at Start, positive the length the
		// call's clock moves once the client waits at the runner.
		deadline time.Duration
		want     string
	}{
		{"fast success", func() *deps.FakeRunner { return fakeTmux(0) }, time.Millisecond, false, 0, "DEBUG"},
		{"slow success", func() *deps.FakeRunner { return fakeTmux(0) }, obs.SlowCall, false, 0, "INFO"},
		{"tmux answers non-zero", func() *deps.FakeRunner { return fakeTmux(1) }, time.Millisecond, false, 0, "INFO"},
		// Nothing on the runner's PATH: Start's lookup fails, as exec's does.
		{"never started", func() *deps.FakeRunner { return &deps.FakeRunner{} }, time.Millisecond, false, 0, "ERROR"},
		// The caller ended the call on purpose, as a closing picker cancels its refresh.
		{"caller cancelled", func() *deps.FakeRunner { return fakeTmux(0) }, time.Millisecond, true, 0, "INFO"},
		// A stopped server: the deadline passed before the client could start (exec reports
		// "context deadline exceeded"), or cut off a client left waiting ("signal: killed").
		{"deadline passed before start", func() *deps.FakeRunner { return fakeTmux(0) }, time.Millisecond, false, -time.Second, "WARN"},
		{"deadline cut off a waiting client", hangingTmux, time.Millisecond, false, time.Millisecond, "WARN"},
		// The client exited 0; its server let go of the client's output pipe only past the wait delay.
		{"server held a finished client's pipe", func() *deps.FakeRunner { return answeringTmux(heldPipe) }, time.Millisecond, false, 0, "WARN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake(time.Unix(1700000000, 0))
			ctx, recorder := obs.Test(t, obs.WithTestClock(fake))
			if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if tc.deadline != 0 {
				// The caller's deadline sits on the test's clock: past at once when negative, else
				// fired by the Advance that moves the call's clock by its length.
				var cancel context.CancelFunc
				ctx, cancel = clock.WithTimeout(ctx, fake, tc.deadline)
				defer cancel()
			}
			runner := tc.runner()
			command := Exec(WithClientRunner(ctx, runner), Binary, "/sockets/cc-1", "list-panes", "-a")
			if err := command.Start(); err == nil {
				if tc.deadline > 0 {
					reached(t, runner)
				}
				fake.Advance(tc.elapsed)
				_ = command.Wait() // a non-zero exit is the case under test, recorded by Wait
			}
			if got := oneRecord(t, recorder).Level; got != tc.want {
				t.Fatalf("level = %s, want %s: %s", got, tc.want, recorder.Raw())
			}
		})
	}
}

// TestExecRunAndCombinedOutputCarryTheExitCode: what every completing method
// hands its caller, and the exit and err its one record carries. A tmux that
// exits non-zero is the command answering, its error untouched; a client that
// exited 0 while its server held the output pipe past the wait delay
// succeeded: its output whole, a nil error, exit 0 with the hold named; the
// same hold met by the call's deadline stays the deadline's failed call.
func TestExecRunAndCombinedOutputCarryTheExitCode(t *testing.T) {
	const heldText = `run "tmux": exec: WaitDelay expired before I/O complete`
	completions := map[string]func(*Cmd) ([]byte, error){
		"Run": func(command *Cmd) ([]byte, error) {
			var out bytes.Buffer
			command.Stdout = &out
			err := command.Run()
			return out.Bytes(), err
		},
		"Output":         (*Cmd).Output,
		"CombinedOutput": (*Cmd).CombinedOutput,
		"Wait": func(command *Cmd) ([]byte, error) {
			var out bytes.Buffer
			command.Stdout = &out
			if err := command.Start(); err != nil {
				return nil, err
			}
			err := command.Wait()
			return out.Bytes(), err
		},
	}
	for _, tc := range []struct {
		name         string
		answer       func(ctx context.Context) (deps.RunResult, error)
		deadline     bool
		wantErr      string // "" is a nil error
		wantExit     float64
		wantErrField string
		wantTimedOut bool
	}{
		{"tmux exits 3", func(context.Context) (deps.RunResult, error) {
			return deps.RunResult{Stdout: []byte(panes), ExitCode: 3}, nil
		}, false, "exit status 3", 3, "exit status 3", false},
		{"server held a finished client's pipe", heldPipe, false, "", 0, heldText, false},
		{"deadline fired while the server held the pipe", func(ctx context.Context) (deps.RunResult, error) {
			<-ctx.Done()
			return heldPipe(ctx)
		}, true, heldText, -1, heldText, true},
	} {
		for method, complete := range completions {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				fake := clock.NewFake(time.Unix(1700000000, 0))
				ctx, recorder := obs.Test(t, obs.WithTestClock(fake))
				if tc.deadline {
					var cancel context.CancelFunc
					ctx, cancel = clock.WithTimeout(ctx, fake, time.Millisecond)
					defer cancel()
				}
				runner := answeringTmux(tc.answer)
				command := Exec(WithClientRunner(ctx, runner), Binary, "/sockets/cc-1", "list-panes", "-a")
				type outcome struct {
					output []byte
					err    error
				}
				done := make(chan outcome, 1)
				go func() {
					output, err := complete(command)
					done <- outcome{output, err}
				}()
				if tc.deadline {
					reached(t, runner)
					fake.Advance(time.Millisecond)
				}
				got := <-done
				gotErr := ""
				if got.err != nil {
					gotErr = got.err.Error()
				}
				if string(got.output) != panes || gotErr != tc.wantErr {
					t.Fatalf("%s = %q, %q; want %q, %q", method, got.output, gotErr, panes, tc.wantErr)
				}
				if command.TimedOut() != tc.wantTimedOut {
					t.Fatalf("TimedOut() = %t, want %t", command.TimedOut(), tc.wantTimedOut)
				}
				record := oneRecord(t, recorder)
				exit, _ := record.Field(obs.FieldExit)
				errField, _ := record.Field(obs.FieldErr)
				subcmd, _ := record.Field("subcmd")
				if subcmd != "list-panes" || exit != tc.wantExit || errField != tc.wantErrField {
					t.Fatalf("record subcmd = %v, exit = %v, err = %v; want list-panes, %v, %q: %s",
						subcmd, exit, errField, tc.wantExit, tc.wantErrField, recorder.Raw())
				}
				if target, found := record.Field("target"); found {
					t.Fatalf("list-panes -a has no -t target, yet the record carries %v: %s", target, recorder.Raw())
				}
			})
		}
	}
}

// TestExecStartRecordsAtWait: Start alone writes nothing (the command is
// still running); Wait writes the one record. A Start that cannot run is an
// ERROR record with exit -1.
func TestExecStartRecordsAtWait(t *testing.T) {
	ctx, recorder := obs.Test(t)
	command := Exec(WithClientRunner(ctx, fakeTmux(0)), Binary, "/sockets/cc-1", "wait-for", "pfm-done")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if len(recorder.Records()) != 0 {
		t.Fatalf("Start wrote a record before completion: %s", recorder.Raw())
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	record := oneRecord(t, recorder)
	if subcmd, _ := record.Field("subcmd"); subcmd != "wait-for" {
		t.Fatalf("subcmd = %v", subcmd)
	}
	missing := Exec(WithClientRunner(ctx, fakeTmux(0)), "/missing/no-tmux", "/sockets/cc-1", "list-panes")
	if err := missing.Start(); err == nil {
		t.Fatal("a missing binary started")
	}
	records := recorder.Records()
	if len(records) != 2 || records[1].Level != slog.LevelError.String() {
		t.Fatalf("a failed start is one ERROR record: %s", recorder.Raw())
	}
	if exit, _ := records[1].Field(obs.FieldExit); exit != float64(-1) {
		t.Fatalf("failed start exit = %v, want -1", exit)
	}
}

// TestObserveRecordsALauncherAssembledCommand: a new-session run under
// another launcher (spawn's systemd scope) still writes the tmux record,
// shaped from the tmux arguments, not the launcher's.
func TestObserveRecordsALauncherAssembledCommand(t *testing.T) {
	ctx, recorder := obs.Test(t)
	fake := &deps.FakeRunner{}
	fake.Script([]string{"systemd-run"}, deps.RunResult{}, nil)
	launcher := &exec.Cmd{
		Path: "/usr/bin/systemd-run",
		Args: []string{"systemd-run", "--scope", "--", "tmux", "-S", "/sockets/cc-1", "new-session", "-d"},
	}
	if _, err := Observe(WithClientRunner(ctx, fake), launcher, "new-session", "-d").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	record := oneRecord(t, recorder)
	if subcmd, _ := record.Field("subcmd"); subcmd != "new-session" {
		t.Fatalf("subcmd = %v, want new-session", subcmd)
	}
}
