package action

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// fakeActionTmuxBinary plays tmux: exits 0 and prints nothing.
func fakeActionTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// TestTmuxExecutorRecordsEveryInvocation proves TmuxExecutor.command crosses
// the observed tmux door (pfmtmux.Exec, not the bare pfmtmux.Command) —
// pattern: internal/kill/tmux_test.go.
func TestTmuxExecutorRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmuxDir := t.TempDir()
	tmux := TmuxExecutor{Binary: fakeActionTmuxBinary(t), TmuxDir: tmuxDir}
	if err := tmux.KillPane(ctx, "cc-1", "%3"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.KillServer(ctx, "cc-1"); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		if record.Message != "tmux.exec" {
			t.Fatalf("record = %s, want tmux.exec", record.Message)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "tmux" {
			t.Fatalf("comp = %v, want tmux", comp)
		}
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "kill-pane" {
		t.Fatalf("subcmd = %v, want kill-pane", subcmd)
	}
	if target, _ := records[0].Field("target"); target != "%3" {
		t.Fatalf("target = %v, want %%3", target)
	}
	if subcmd, _ := records[1].Field("subcmd"); subcmd != "kill-server" {
		t.Fatalf("subcmd = %v, want kill-server", subcmd)
	}
}
