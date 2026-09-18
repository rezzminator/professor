package obs

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
)

// killableProcess is the Process shape the sibling's Runner grows into:
// Kill and KillGroup beside Pid/Wait/Release. The wrapper must forward and
// record both once the inner process has them.
type killableProcess struct {
	pid    int
	killed []string
}

func (p *killableProcess) Pid() int         { return p.pid }
func (p *killableProcess) Wait() error      { return nil }
func (p *killableProcess) Release() error   { return nil }
func (p *killableProcess) Kill() error      { p.killed = append(p.killed, "kill"); return nil }
func (p *killableProcess) KillGroup() error { p.killed = append(p.killed, "group"); return nil }

// startRunner is a Runner whose Start hands back a fixed Process, so the
// wrapper's terminal records can be asserted against a known inner.
type startRunner struct {
	deps.FakeRunner
	process deps.Process
}

func (r *startRunner) Start(context.Context, []string, deps.StartOptions) (deps.Process, error) {
	return r.process, nil
}

func requireField(t *testing.T, record Record, key string, want any) {
	t.Helper()
	got, found := record.Field(key)
	if !found || got != want {
		t.Fatalf("%s: %s = %v (found %t), want %v; fields %v", record.Message, key, got, found, want, record.Fields)
	}
}

func requireDur(t *testing.T, record Record) {
	t.Helper()
	if _, found := record.Field(FieldDur); !found {
		t.Fatalf("%s carries no %s: %v", record.Message, FieldDur, record.Fields)
	}
}

// TestRunnerRunRecordsShapeExitAndDuration: one comp=runner record per Run
// with the argv SHAPE (base name + argc), the exit code and dur_ms — and the
// arguments themselves never reach the file, even a planted secret.
func TestRunnerRunRecordsShapeExitAndDuration(t *testing.T) {
	ctx, recorder := Test(t)
	fake := &deps.FakeRunner{}
	fake.Script([]string{"/usr/bin/git"}, deps.RunResult{ExitCode: 3, Stdout: []byte("out")}, nil)
	runner := Runner(fake)

	result, err := runner.Run(ctx, []string{"/usr/bin/git", "push", "--token", "sk-planted-secret"}, deps.RunOptions{})
	if err != nil || result.ExitCode != 3 || string(result.Stdout) != "out" {
		t.Fatalf("wrapped Run changed the result: %+v err=%v", result, err)
	}
	if len(fake.Calls()) != 1 || len(fake.Calls()[0].Argv) != 4 {
		t.Fatalf("inner runner saw %v, want the untouched argv", fake.Calls())
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	record := records[0]
	if record.Message != "runner.run" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s at %s, want runner.run at INFO", record.Message, record.Level)
	}
	requireField(t, record, FieldComp, "runner")
	requireField(t, record, "argv", "git")
	requireField(t, record, "argc", float64(4))
	requireField(t, record, FieldExit, float64(3))
	requireDur(t, record)
	if strings.Contains(recorder.Raw(), "planted") || strings.Contains(recorder.Raw(), "push") {
		t.Fatalf("an argument reached the file: %s", recorder.Raw())
	}
}

// TestRunnerRunFailureToStartIsAnErrorRecord: a Runner error (the binary
// never ran) is an ERROR record carrying err and exit -1, returned unchanged.
func TestRunnerRunFailureToStartIsAnErrorRecord(t *testing.T) {
	ctx, recorder := Test(t)
	runner := Runner(&deps.FakeRunner{})
	_, err := runner.Run(ctx, []string{"missing-binary"}, deps.RunOptions{})
	var unscripted deps.UnscriptedError
	if !errors.As(err, &unscripted) {
		t.Fatalf("wrapped Run changed the error: %v", err)
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != slog.LevelError.String() {
		t.Fatalf("want one ERROR record: %s", recorder.Raw())
	}
	requireField(t, records[0], FieldExit, float64(-1))
	if got, _ := records[0].Field(FieldErr); got == nil || got == "" {
		t.Fatalf("err field missing: %v", records[0].Fields)
	}
}

// TestRunnerLookPathRecordsHitAndMiss: a resolved path is INFO with the
// path; a miss is WARN with err — a missing optional binary is an answer,
// not a failure of the door.
func TestRunnerLookPathRecordsHitAndMiss(t *testing.T) {
	ctx, recorder := Test(t)
	_ = ctx
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("tmux", "/usr/bin/tmux", nil)
	runner := Runner(fake)
	if path, err := runner.LookPath("tmux"); err != nil || path != "/usr/bin/tmux" {
		t.Fatalf("LookPath = %q, %v", path, err)
	}
	if _, err := runner.LookPath("absent"); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("miss error changed: %v", err)
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	if records[0].Message != "runner.lookpath" || records[0].Level != slog.LevelInfo.String() {
		t.Fatalf("hit record = %s %s", records[0].Message, records[0].Level)
	}
	requireField(t, records[0], "argv", "tmux")
	requireField(t, records[0], "path", "/usr/bin/tmux")
	if records[1].Level != slog.LevelWarn.String() {
		t.Fatalf("miss level = %s, want WARN", records[1].Level)
	}
	requireDur(t, records[1])
}

// TestRunnerStartRecordsPidAndEveryTerminal: Start logs the pid; the
// returned Process logs Wait, Release, Kill and KillGroup as their own
// terminals, each once, each with dur_ms since the start.
func TestRunnerStartRecordsPidAndEveryTerminal(t *testing.T) {
	ctx, recorder := Test(t)
	inner := &killableProcess{pid: 777}
	runner := Runner(&startRunner{process: inner})
	process, err := runner.Start(ctx, []string{"/opt/claude", "--resume"}, deps.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if process.Pid() != 777 {
		t.Fatalf("pid = %d, want the inner's 777", process.Pid())
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := process.Release(); err != nil {
		t.Fatal(err)
	}
	killer, ok := process.(interface {
		Kill() error
		KillGroup() error
	})
	if !ok {
		t.Fatal("the wrapped process lost Kill/KillGroup")
	}
	if err := killer.Kill(); err != nil || killer.KillGroup() != nil {
		t.Fatalf("kill forwarding: %v", err)
	}
	if strings.Join(inner.killed, ",") != "kill,group" {
		t.Fatalf("inner saw %v, want kill then group", inner.killed)
	}
	records := recorder.Records()
	want := []string{"runner.start", "runner.exit", "runner.release", "runner.kill", "runner.killgroup"}
	if len(records) != len(want) {
		t.Fatalf("records = %d, want %d: %s", len(records), len(want), recorder.Raw())
	}
	for index, message := range want {
		if records[index].Message != message {
			t.Fatalf("record %d = %s, want %s", index, records[index].Message, message)
		}
		requireField(t, records[index], FieldComp, "runner")
		requireField(t, records[index], FieldPID, float64(777))
		if index > 0 {
			requireDur(t, records[index])
		}
	}
	requireField(t, records[0], "argv", "claude")
	requireField(t, records[0], "argc", float64(2))
	requireField(t, records[1], FieldExit, float64(0))
}

// TestRunnerStartFailureAndWaitExitError: a Start that fails is an ERROR
// record; a Wait that returns the child's non-zero exit is INFO with the
// code (the command answered), any other Wait error is ERROR.
func TestRunnerStartFailureAndWaitExitError(t *testing.T) {
	ctx, recorder := Test(t)
	fake := &deps.FakeRunner{}
	fake.ScriptStart([]string{"boom"}, 0, nil, errors.New("no such binary"))
	fake.ScriptStart([]string{"waiter"}, 12, errors.New("pipe closed"), nil)
	runner := Runner(fake)
	if _, err := runner.Start(ctx, []string{"boom"}, deps.StartOptions{}); err == nil {
		t.Fatal("wrapped Start swallowed the error")
	}
	process, err := runner.Start(ctx, []string{"waiter"}, deps.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err == nil || err.Error() != "pipe closed" {
		t.Fatalf("Wait error changed: %v", err)
	}
	records := recorder.Records()
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3: %s", len(records), recorder.Raw())
	}
	if records[0].Level != slog.LevelError.String() || records[0].Message != "runner.start" {
		t.Fatalf("failed start = %s %s", records[0].Message, records[0].Level)
	}
	if records[2].Level != slog.LevelError.String() {
		t.Fatalf("a non-exit Wait error logged at %s, want ERROR", records[2].Level)
	}
	requireField(t, records[2], FieldExit, float64(-1))
}

// TestRunnerNilNextIsTheRealRunner: Runner(nil) wraps deps.RealRunner so a
// construction site can write obs.Runner(nil) for the default.
func TestRunnerNilNextIsTheRealRunner(t *testing.T) {
	wrapped, ok := Runner(nil).(loggedRunner)
	if !ok {
		t.Fatalf("Runner(nil) = %T", Runner(nil))
	}
	if _, real := wrapped.next.(deps.RealRunner); !real {
		t.Fatalf("Runner(nil).next = %T, want deps.RealRunner", wrapped.next)
	}
}

// TestStartedRecordsADirectDoorLikeAWrappedRunner: the four direct exec doors
// write runner.start with the pid and runner.exit with the exit code — the
// same two records a Runner.Start would have written.
func TestStartedRecordsADirectDoorLikeAWrappedRunner(t *testing.T) {
	ctx, recorder := Test(t)
	finish := Started(ctx, []string{"/usr/local/bin/codex", "app-server"}, 4242)
	finish(nil)
	failing := Started(ctx, []string{"codex"}, 1)
	failing(errors.New("signal: killed"))
	records := recorder.Records()
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4: %s", len(records), recorder.Raw())
	}
	requireField(t, records[0], "argv", "codex")
	requireField(t, records[0], "argc", float64(2))
	requireField(t, records[0], FieldPID, float64(4242))
	if records[1].Message != "runner.exit" {
		t.Fatalf("second record = %s, want runner.exit", records[1].Message)
	}
	requireField(t, records[1], FieldExit, float64(0))
	requireDur(t, records[1])
	if records[3].Level != slog.LevelError.String() {
		t.Fatalf("a killed direct door logged at %s, want ERROR", records[3].Level)
	}
	requireField(t, records[3], FieldExit, float64(-1))
}

// TestStartFailedIsOneErrorRecord pins the direct door's failed-start shape.
func TestStartFailedIsOneErrorRecord(t *testing.T) {
	ctx, recorder := Test(t)
	StartFailed(ctx, []string{"/bin/codex", "app-server"}, errors.New("permission denied"))
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != slog.LevelError.String() || records[0].Message != "runner.start" {
		t.Fatalf("want one runner.start ERROR: %s", recorder.Raw())
	}
	requireField(t, records[0], "argv", "codex")
	requireField(t, records[0], FieldErr, "permission denied")
}
