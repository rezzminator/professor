package statusline

import (
	"context"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestCommandRunnerOutputRecordsTheCommand proves statusline's production
// command runner is the observed deps.Runner: one comp=runner record per
// Output call with the binary's base name and its exit code.
func TestCommandRunnerOutputRecordsTheCommand(t *testing.T) {
	ctx, recorder := obs.Test(t)
	output, err := commandRunner{}.Output(ctx, "sh", "-c", "echo shape-only; exit 0")
	if err != nil || string(output) != "shape-only\n" {
		t.Fatalf("Output = %q, %v", output, err)
	}
	if _, err := (commandRunner{}).Output(ctx, "sh", "-c", "exit 4"); err == nil {
		t.Fatal("a non-zero exit was not reported")
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	for index, wantExit := range []float64{0, 4} {
		record := records[index]
		if record.Message != "runner.run" {
			t.Fatalf("record %d = %s, want runner.run", index, record.Message)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "runner" {
			t.Fatalf("comp = %v, want runner", comp)
		}
		if argv, _ := record.Field("argv"); argv != "sh" {
			t.Fatalf("argv shape = %v, want sh", argv)
		}
		if exit, _ := record.Field(obs.FieldExit); exit != wantExit {
			t.Fatalf("exit = %v, want %v", exit, wantExit)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	_ = context.Background
}
