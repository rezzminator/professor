package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestRunInternalRecordsEveryDispatchUnderTheHooksComponent: the internal
// dispatcher is the hooks door — a dispatched name writes one comp=hooks
// record with the hook, its decision and exit; an unknown name is recorded
// too, as the non-blocking error it exits with.
func TestRunInternalRecordsEveryDispatchUnderTheHooksComponent(t *testing.T) {
	_, recorder := obs.Test(t)
	var stdout, stderr bytes.Buffer
	if code := runInternal([]string{"store-guard"}, &stdout, &stderr, commandRuntime{}); code != 0 {
		t.Fatalf("store-guard exit = %d, stderr=%q", code, stderr.String())
	}
	if code := runInternal([]string{"hook-from-a-newer-pfm"}, &stdout, &stderr, commandRuntime{}); code != 1 {
		t.Fatalf("unknown exit = %d", code)
	}
	var records []obs.Record
	for _, record := range recorder.Records() {
		if record.Message != "action.start" {
			records = append(records, record)
		}
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want one per dispatch: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		if record.Message != "hooks.run" {
			t.Fatalf("record = %s, want hooks.run", record.Message)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "hooks" {
			t.Fatalf("comp = %v, want hooks", comp)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	if hook, _ := records[0].Field("hook"); hook != "store-guard" {
		t.Fatalf("hook = %v", hook)
	}
	// With no payload on stdin the fail-open hook answers nothing and exits
	// 0, which the door reads as allow.
	if decision, _ := records[0].Field("decision"); decision != "allow" {
		t.Fatalf("store-guard decision = %v, want allow", decision)
	}
	if decision, _ := records[1].Field("decision"); decision != "error" {
		t.Fatalf("unknown subcommand decision = %v, want error", decision)
	}
	if exit, _ := records[1].Field(obs.FieldExit); exit != float64(1) {
		t.Fatalf("unknown subcommand exit = %v, want 1", exit)
	}
	directory := t.TempDir()
	t.Setenv(hookentry.EnvHookCaptureDir, directory)
	t.Run("capture read failure", func(t *testing.T) {
		input, err := os.Create(filepath.Join(t.TempDir(), "closed-input"))
		if err != nil {
			t.Fatal(err)
		}
		if err := input.Close(); err != nil {
			t.Fatal(err)
		}
		previous := os.Stdin
		os.Stdin = input
		t.Cleanup(func() { os.Stdin = previous })
		var output, warning bytes.Buffer
		code := runInternal([]string{"store-guard"}, &output, &warning, commandRuntime{})
		want := "capture store-guard hook input: read: read " + input.Name() + ": file already closed\n"
		if code != 1 || output.String() != "" || warning.String() != want {
			t.Fatalf(
				"capture failure = (%d, %q, %q), want (1, empty, %q)",
				code,
				output.String(),
				warning.String(),
				want,
			)
		}
	})
	const payload = " {\"hook_event_name\":\"PreToolUse\",\"session_id\":\"atlas\",\"tool_name\":\"Read\"}\n"
	code, output, warning := runInternalHookPayload(t, "store-guard", payload)
	if code != 0 || output != "" || warning != "" {
		t.Fatalf("captured hook = (%d, %q, %q), want (0, empty, empty)", code, output, warning)
	}
	files, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("%s: captures = %v, %v; want one event", directory, files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || string(raw) != payload {
		t.Fatalf("%s = %q, %v; want %q", files[0], raw, err, payload)
	}
}
