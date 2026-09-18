package hookentry

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestLaunchTmuxCommandRecordsUnderTheTmuxComponent: the hook launcher's
// tmux calls (wait-for, kill-server) terminate through the observed
// command — one comp=tmux record each, Start's record written at Wait.
func TestLaunchTmuxCommandRecordsUnderTheTmuxComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	waiter := launchTmuxCommand(ctx, binary, "/sockets/cc-1", "wait-for", "pfm-done")
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	if err := launchTmuxCommand(ctx, binary, "/sockets/cc-1", "kill-server").Run(); err != nil {
		t.Fatal(err)
	}
	if err := waiter.Wait(); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	for index, want := range []string{"kill-server", "wait-for"} {
		if records[index].Message != "tmux.exec" {
			t.Fatalf("record %d = %s", index, records[index].Message)
		}
		if comp, _ := records[index].Field(obs.FieldComp); comp != "tmux" {
			t.Fatalf("comp = %v", comp)
		}
		if subcmd, _ := records[index].Field("subcmd"); subcmd != want {
			t.Fatalf("record %d subcmd = %v, want %s", index, subcmd, want)
		}
		if _, found := records[index].Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", records[index].Fields)
		}
	}
	_ = context.Background
}
