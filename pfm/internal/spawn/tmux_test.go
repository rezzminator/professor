package spawn

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/obs"
)

// fakeTmuxBinary plays tmux: exits 0 and prints nothing.
func fakeTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// requireTmuxRecord asserts the one comp=tmux record a façade call writes:
// subcmd, target when given, exit 0 and dur_ms.
func requireTmuxRecord(t *testing.T, recorder *obs.Recorder, index int, subcmd, target string) {
	t.Helper()
	records := recorder.Records()
	if len(records) <= index {
		t.Fatalf("records = %d, want at least %d: %s", len(records), index+1, recorder.Raw())
	}
	record := records[index]
	if record.Message != "tmux.exec" {
		t.Fatalf("record %d = %s, want tmux.exec", index, record.Message)
	}
	if comp, _ := record.Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if got, _ := record.Field("subcmd"); got != subcmd {
		t.Fatalf("subcmd = %v, want %s", got, subcmd)
	}
	if got, _ := record.Field("target"); target != "" && got != target {
		t.Fatalf("target = %v, want %s", got, target)
	}
	if exit, _ := record.Field(obs.FieldExit); exit != float64(0) {
		t.Fatalf("exit = %v, want 0", exit)
	}
	if _, found := record.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", record.Fields)
	}
}

// TestTmuxSpawnerRecordsEveryInvocation: the spawn façade terminates
// through the observed tmux command — SendKey and Capture each one record.
func TestTmuxSpawnerRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxSpawner{Binary: fakeTmuxBinary(t), TmuxDir: t.TempDir()}
	if err := tmux.SendKey(ctx, "cc-1-2-3", "%1", "Enter"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.Capture(ctx, "cc-1-2-3", "%1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "send-keys", "%1")
	requireTmuxRecord(t, recorder, 1, "capture-pane", "%1")
	_ = context.Background
}
