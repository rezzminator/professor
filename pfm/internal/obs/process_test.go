package obs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// fakeTimed returns ctx with the same logger over a fake clock, so a
// duration in a record is exact rather than merely present.
func fakeTimed(ctx context.Context, fake *clock.Fake) context.Context {
	return context.WithValue(ctx, contextKey{}, &scope{logger: Logger(ctx), timing: fake})
}

func TestProcessRecordsTheWholeLifecycleWithKindPidExitAndDuration(t *testing.T) {
	ctx, recorder := Test(t)
	fake := clock.NewFake(time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC))
	process := NewProcess(fakeTimed(ctx, fake), "browser")
	process.Started(4242, nil)
	fake.Advance(30 * time.Millisecond)
	end := process.Request("fetch")
	fake.Advance(1500 * time.Millisecond)
	end(2048, nil)
	failing := process.Request("fetch")
	fake.Advance(7 * time.Millisecond)
	failing(0, errors.New("browser worker read failed: EOF (stderr: Bearer sk-TAILSECRET)"))
	process.Stop("close")
	process.Killed(nil)
	fake.Advance(3 * time.Millisecond)
	process.Exited(nil)

	records := recorder.Records()
	wantOps := []string{"start", "request", "request", "stop", "kill", "exit"}
	if len(records) != len(wantOps) {
		t.Fatalf("records = %d, want %d: %s", len(records), len(wantOps), recorder.Raw())
	}
	for index, op := range wantOps {
		record := records[index]
		if record.Message != "harvestpy."+op {
			t.Fatalf("record %d = %q, want harvestpy.%s", index, record.Message, op)
		}
		wantField(t, record, FieldComp, "harvestpy")
		wantField(t, record, "op", op)
		wantField(t, record, "kind", "browser")
		wantField(t, record, FieldPID, float64(4242))
	}
	if records[0].Level != slog.LevelInfo.String() {
		t.Fatalf("start logged at %s, want INFO", records[0].Level)
	}
	wantField(t, records[1], "subcmd", "fetch")
	wantField(t, records[1], "bytes", float64(2048))
	wantField(t, records[1], FieldDur, float64(1500))
	if records[2].Level != slog.LevelError.String() {
		t.Fatalf("a failed request logged at %s, want ERROR", records[2].Level)
	}
	wantField(t, records[2], FieldDur, float64(7))
	if got, _ := records[2].Field(FieldErr); got != redactedValue {
		t.Fatalf("an error carrying a token was kept: %v", got)
	}
	wantField(t, records[3], "cause", "close")
	wantField(t, records[5], FieldExit, float64(0))
	wantField(t, records[5], FieldDur, float64(1540))
	if strings.Contains(recorder.Raw(), "TAILSECRET") {
		t.Fatalf("a stderr tail inside an error reached the activity log: %s", recorder.Raw())
	}
}

func TestProcessStartFailureIsAnErrorRecordWithoutAPid(t *testing.T) {
	ctx, recorder := Test(t)
	NewProcess(ctx, "converter").Started(0, errors.New("start harvestpy worker: no such file"))
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelError.String() {
		t.Fatalf("a failed start logged at %s, want ERROR", record.Level)
	}
	wantField(t, record, "op", "start")
	wantField(t, record, FieldErr, "start harvestpy worker: no such file")
	if _, found := record.Field(FieldPID); found {
		t.Fatalf("a process that never started has a pid: %v", record.Fields)
	}
}

func TestProcessStderrLogsEachLineAsWarnAndPassesEveryByteThrough(t *testing.T) {
	ctx, recorder := Test(t)
	process := NewProcess(ctx, "converter")
	process.Started(7, nil)
	var captured bytes.Buffer
	stderr := process.Stderr(&captured)
	chunks := []string{
		"DeprecationWarning: x\r\npartial", " line\nAuthorization: Bearer sk-STDERRSECRET\ntail without newline",
	}
	for _, chunk := range chunks {
		if _, err := io.WriteString(stderr, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if captured.String() != strings.Join(chunks, "") {
		t.Fatalf("the tee altered the bytes: %q", captured.String())
	}
	process.Exited(errors.New("signal: killed"))

	records := recorder.Records()
	// start, three complete lines, the flushed tail at exit, exit.
	if len(records) != 6 {
		t.Fatalf("records = %d, want 6: %s", len(records), recorder.Raw())
	}
	for index, want := range []string{"DeprecationWarning: x", "partial line", redactedValue, "tail without newline"} {
		record := records[index+1]
		if record.Message != "harvestpy.stderr" || record.Level != slog.LevelWarn.String() {
			t.Fatalf("record %d = %s %s, want WARN harvestpy.stderr", index+1, record.Level, record.Message)
		}
		wantField(t, record, "op", "stderr")
		wantField(t, record, "line", want)
	}
	if strings.Contains(recorder.Raw(), "STDERRSECRET") {
		t.Fatalf("a stderr token reached the activity log: %s", recorder.Raw())
	}
	if records[5].Level != slog.LevelError.String() {
		t.Fatalf("an exit error that is not an exit status logged at %s, want ERROR", records[5].Level)
	}
	wantField(t, records[5], FieldErr, "signal: killed")
}

// TestProcessStderrCarriesClassAndBytesAlwaysButLineOnlyAtDebug is L2-F27: a
// harvestpy stderr line is the only free-text content this middleware
// carries (converter.py's exception text among it, which can hold a path or
// URL) — at the default level the record carries only a bounded class token
// and the line's size; the full line is added only when comp=harvestpy is
// actually logging at DEBUG.
func TestProcessStderrCarriesClassAndBytesAlwaysButLineOnlyAtDebug(t *testing.T) {
	const line = "Traceback (most recent call last):"
	warnOnly := &Recorder{}
	warnCtx := context.WithValue(context.Background(), contextKey{}, &scope{
		logger: slog.New(levelHandler{
			next:   slog.NewJSONHandler(warnOnly, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: Scrub}),
			levels: Levels{Global: LevelInForce{Level: slog.LevelWarn}},
		}),
		timing: clock.Real,
	})
	process := NewProcess(warnCtx, "converter")
	process.Started(7, nil)
	if _, err := io.WriteString(process.Stderr(io.Discard), line+"\n"); err != nil {
		t.Fatal(err)
	}
	record := onlyRecord(t, warnOnly) // Started's INFO record is filtered by the WARN floor.
	if record.Message != "harvestpy.stderr" {
		t.Fatalf("record = %s, want harvestpy.stderr", record.Message)
	}
	wantField(t, record, "class", "Traceback")
	wantField(t, record, "bytes", float64(len(line)))
	if _, found := record.Field("line"); found {
		t.Fatalf("the full stderr line reached a non-DEBUG record: %v", record.Fields)
	}

	debugRecorder := &Recorder{}
	debugCtx := context.WithValue(context.Background(), contextKey{}, &scope{
		logger: slog.New(levelHandler{
			next: slog.NewJSONHandler(
				debugRecorder,
				&slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: Scrub},
			),
			levels: Levels{Global: LevelInForce{Level: slog.LevelDebug}},
		}),
		timing: clock.Real,
	})
	debugProcess := NewProcess(debugCtx, "converter")
	debugProcess.Started(7, nil)
	if _, err := io.WriteString(debugProcess.Stderr(io.Discard), line+"\n"); err != nil {
		t.Fatal(err)
	}
	records := debugRecorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want start+stderr at DEBUG: %s", len(records), debugRecorder.Raw())
	}
	wantField(t, records[1], "class", "Traceback")
	wantField(t, records[1], "line", line)
}

func TestProcessExitedReadsTheCodeFromAnExitErrorAndWarns(t *testing.T) {
	ctx, recorder := Test(t)
	command := exec.Command("sh", "-c", "exit 3")
	if err := command.Start(); err != nil {
		t.Skipf("sh is not available: %v", err)
	}
	process := NewProcess(ctx, "check")
	process.Started(command.Process.Pid, nil)
	process.Exited(command.Wait())
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want start+exit: %s", len(records), recorder.Raw())
	}
	if records[1].Level != slog.LevelWarn.String() {
		t.Fatalf("a non-zero exit logged at %s, want WARN", records[1].Level)
	}
	wantField(t, records[1], FieldExit, float64(3))
	if _, found := records[1].Field(FieldErr); found {
		t.Fatalf("an exit status is not an error of the wrapper: %v", records[1].Fields)
	}
}

// TestProcessExitedReadsTheCodeFromADuckTypedExitStatusAndWarns is the
// Runner-crossed sibling of TestProcessExitedReadsTheCodeFromAnExitErrorAndWarns:
// a command run through the deps.Runner seam never surfaces a bare
// *exec.ExitError (deps.RealRunner.Run folds one into RunResult.ExitCode),
// so Exited must also read the duck type "any error naming its own exit
// code" — deps.ExitStatus builds exactly that error — or a Runner-crossed
// non-zero exit falls through to the generic ERROR branch instead of the
// WARN-with-code branch a completed command deserves.
func TestProcessExitedReadsTheCodeFromADuckTypedExitStatusAndWarns(t *testing.T) {
	ctx, recorder := Test(t)
	process := NewProcess(ctx, "check")
	process.Started(321, nil)
	process.Exited(deps.ExitStatus(3))
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want start+exit: %s", len(records), recorder.Raw())
	}
	if records[1].Level != slog.LevelWarn.String() {
		t.Fatalf("a non-zero duck-typed exit logged at %s, want WARN", records[1].Level)
	}
	wantField(t, records[1], FieldExit, float64(3))
	if _, found := records[1].Field(FieldErr); found {
		t.Fatalf("an exit status is not an error of the wrapper: %v", records[1].Fields)
	}
}

func TestProcessKilledWithAnErrorIsAnErrorRecord(t *testing.T) {
	ctx, recorder := Test(t)
	process := NewProcess(ctx, "browser")
	process.Started(9, nil)
	process.Killed(errors.New("kill process group: operation not permitted"))
	records := recorder.Records()
	if len(records) != 2 || records[1].Level != slog.LevelError.String() {
		t.Fatalf("a failed kill did not log at ERROR: %s", recorder.Raw())
	}
	wantField(t, records[1], "op", "kill")
	wantField(t, records[1], FieldErr, "kill process group: operation not permitted")
}
