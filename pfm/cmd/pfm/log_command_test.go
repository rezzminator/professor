package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
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

// writeJailConfig writes the jail's pfm.config.json body.
func writeJailConfig(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, "home", ".config", "pfm", config.FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version": 2, "log": `+body+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunHandsTheWholeLogPolicyToTheActivityLog pins the process entry: the
// machine's log.components and log.keepDays reach obs.OpenLog, not just the
// level — a component the config lifts to debug is enabled after run(), and
// a rotated file past keepDays is gone.
func TestRunHandsTheWholeLogPolicyToTheActivityLog(t *testing.T) {
	root := jailTest(t)
	writeJailConfig(t, root, `{"level": "error", "keepDays": 30, "components": {"cli": "debug"}}`)
	aged := jailPaths(t).LogFile + ".1"
	if err := os.MkdirAll(filepath.Dir(aged), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aged, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(aged, old, old); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d; stderr = %q", code, stderr.String())
	}
	ctx := context.Background()
	if !obs.Enabled(ctx, "cli", slog.LevelDebug) {
		t.Fatal("log.components.cli=debug did not reach the process logger")
	}
	if obs.Enabled(ctx, "mcp", slog.LevelInfo) {
		t.Fatal("log.level=error did not reach the process logger for an unlisted component")
	}
	if _, err := os.Stat(aged); !os.IsNotExist(err) {
		t.Fatalf("log.keepDays did not reach the rotator: %s still exists (err %v)", aged, err)
	}
}

// TestRunWithLogOffWritesNoFile: `log.level: "off"` in the machine config
// turns the whole log off for every verb — no pfm.jsonl appears.
func TestRunWithLogOffWritesNoFile(t *testing.T) {
	root := jailTest(t)
	writeJailConfig(t, root, `{"level": "off"}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d; stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(jailPaths(t).LogFile); !os.IsNotExist(err) {
		t.Fatalf("log off still produced %s (err %v)", jailPaths(t).LogFile, err)
	}
	stdout.Reset()
	if code := run([]string{"log", "--comp", "cli"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(log --comp cli) = %d; stderr = %q", code, stderr.String())
	}
}
