package tmux

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
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
		// deadline bounds the call: 0 none, negative already past at Start.
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
		{"deadline cut off a waiting client", hangingTmux, time.Millisecond, false, 50 * time.Millisecond, "WARN"},
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
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.deadline)
				defer cancel()
			}
			command := Exec(WithClientRunner(ctx, tc.runner()), Binary, "/sockets/cc-1", "list-panes", "-a")
			if err := command.Start(); err == nil {
				fake.Advance(tc.elapsed)
				_ = command.Wait() // a non-zero exit is the case under test, recorded by Wait
			}
			if got := oneRecord(t, recorder).Level; got != tc.want {
				t.Fatalf("level = %s, want %s: %s", got, tc.want, recorder.Raw())
			}
		})
	}
}

// TestExecRunAndCombinedOutputCarryTheExitCode: a tmux that exits non-zero
// is the command answering — INFO with the exit code, err carried, the
// caller's error untouched.
func TestExecRunAndCombinedOutputCarryTheExitCode(t *testing.T) {
	ctx, recorder := obs.Test(t)
	ctx, binary := WithClientRunner(ctx, fakeTmux(3)), Binary
	killServer := Exec(ctx, binary, "/sockets/cc-1", "kill-server")
	if err := killServer.Run(); err == nil {
		t.Fatal("Run swallowed the exit status")
	}
	if killServer.TimedOut() {
		t.Fatal("TimedOut() = true for a tmux that exited 3 on its own, want false")
	}
	if _, err := Exec(ctx, binary, "/sockets/cc-1", "list-panes", "-a").CombinedOutput(); err == nil {
		t.Fatal("CombinedOutput swallowed the exit status")
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want one per invocation: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		if exit, _ := record.Field(obs.FieldExit); exit != float64(3) {
			t.Fatalf("exit = %v, want 3", exit)
		}
		if _, found := record.Field(obs.FieldErr); !found {
			t.Fatalf("no err on a failed invocation: %v", record.Fields)
		}
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "kill-server" {
		t.Fatalf("subcmd = %v", subcmd)
	}
	if _, found := records[0].Field("target"); found {
		t.Fatalf("kill-server has no -t target, yet one was recorded: %v", records[0].Fields)
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
