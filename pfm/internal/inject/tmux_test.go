package inject

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

// TestTmuxInjectorRecordsEveryInvocation: the inject façade terminates
// through the observed tmux command — SendPaste is two records (load-buffer,
// paste-buffer) and the pasted text never reaches the file.
func TestTmuxInjectorRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxInjector{Binary: fakeTmuxBinary(t)}
	if err := tmux.SendPaste(ctx, "/sockets/cc-1", "%5", "PLANTED message body"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.SendKey(ctx, "/sockets/cc-1", "%5", "Enter"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "load-buffer", "")
	requireTmuxRecord(t, recorder, 1, "paste-buffer", "%5")
	requireTmuxRecord(t, recorder, 2, "send-keys", "%5")
	if raw := recorder.Raw(); len(recorder.Records()) != 3 || containsPlanted(raw) {
		t.Fatalf("records: %s", raw)
	}
	_ = context.Background
}

func containsPlanted(raw string) bool {
	for index := 0; index+7 <= len(raw); index++ {
		if raw[index:index+7] == "PLANTED" {
			return true
		}
	}
	return false
}
