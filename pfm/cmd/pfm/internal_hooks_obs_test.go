package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestRunInternalRecordsEveryDispatchUnderTheHooksComponent: the internal
// dispatcher is the hooks door — a dispatched name writes one comp=hooks
// record with the hook, its decision and exit; an unknown name is recorded
// too, as the non-blocking error it exits with.
func TestRunInternalRecordsEveryDispatchUnderTheHooksComponent(t *testing.T) {
	_, recorder := obs.Test(t)
	var stdout, stderr bytes.Buffer
	if code := runInternal([]string{"explore-deny"}, &stdout, &stderr, commandRuntime{}); code != 0 {
		t.Fatalf("explore-deny exit = %d, stderr=%q", code, stderr.String())
	}
	if code := runInternal([]string{"hook-from-a-newer-pfm"}, &stdout, &stderr, commandRuntime{}); code != 1 {
		t.Fatalf("unknown exit = %d", code)
	}
	records := recorder.Records()
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
	if hook, _ := records[0].Field("hook"); hook != "explore-deny" {
		t.Fatalf("hook = %v", hook)
	}
	// With no payload on stdin the fail-open hook answers nothing and exits
	// 0, which the door reads as allow.
	if decision, _ := records[0].Field("decision"); decision != "allow" {
		t.Fatalf("explore-deny decision = %v, want allow", decision)
	}
	if decision, _ := records[1].Field("decision"); decision != "error" {
		t.Fatalf("unknown subcommand decision = %v, want error", decision)
	}
	if exit, _ := records[1].Field(obs.FieldExit); exit != float64(1) {
		t.Fatalf("unknown subcommand exit = %v, want 1", exit)
	}
}

// The tracer nudge reads no config: on a broken config it still exits 0 and names its own error to the agent.
func TestInternalTracerNudgeAnswersOnABrokenConfig(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, "{")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("not-json"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = previous
		_ = reader.Close()
	}()
	var stdout, stderr bytes.Buffer
	code := run([]string{"--config", configPath, internalCommand, "tracer-nudge"}, &stdout, &stderr)
	if code != 0 || strings.Contains(stderr.String(), "pfm: config:") ||
		!strings.Contains(stdout.String(), "TRACER-NUDGE-ERROR: decode hook payload") {
		t.Fatalf("broken config tracer-nudge code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
