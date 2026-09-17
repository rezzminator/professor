package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// readJailLog decodes every record the jail's activity file holds.
func readJailLog(t *testing.T) []obs.Record {
	t.Helper()
	path := jailPaths(t).LogFile
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	recorder := &obs.Recorder{}
	if _, err := recorder.Write(content); err != nil {
		t.Fatal(err)
	}
	return recorder.Records()
}

// TestRunWritesCommandStartAndExit pins the one wiring the activity-log
// foundation carries: every pfm process brackets its verb with cmd.start and
// cmd.exit, and cmd.exit reports the code the process really returned.
func TestRunWritesCommandStartAndExit(t *testing.T) {
	jailTest(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"no-such-command"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(no-such-command) = %d, want 2", code)
	}
	records := readJailLog(t)
	if len(records) < 2 {
		t.Fatalf("activity log holds %d records, want cmd.start and cmd.exit", len(records))
	}
	first, last := records[0], records[len(records)-1]
	if first.Message != "cmd.start" || last.Message != "cmd.exit" {
		t.Fatalf("records = %q .. %q, want cmd.start .. cmd.exit", first.Message, last.Message)
	}
	if command, _ := last.Field(obs.FieldCmd); command != "no-such-command" {
		t.Fatalf("cmd.exit cmd = %v, want the verb that ran", command)
	}
	if code, _ := last.Field(obs.FieldExit); code != float64(2) {
		t.Fatalf("cmd.exit exit = %v, want 2", code)
	}
	for _, key := range []string{obs.FieldPID, obs.FieldVersion, obs.FieldTime} {
		if _, found := last.Field(key); !found {
			t.Fatalf("cmd.exit carries no %s: %+v", key, last.Fields)
		}
	}
}

// TestLogVerbReadsWhatTheProcessWrote closes the loop: the verb registered in
// run()'s table reads the file run() itself wrote.
func TestLogVerbReadsWhatTheProcessWrote(t *testing.T) {
	jailTest(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d, want 0; stderr = %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"log", "--cmd", "version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(log) = %d, want 0; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cmd.start") {
		t.Fatalf("pfm log printed no record of the version run:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"cmd":"log"`) {
		t.Fatalf("--cmd version printed another verb's records:\n%s", stdout.String())
	}
}
