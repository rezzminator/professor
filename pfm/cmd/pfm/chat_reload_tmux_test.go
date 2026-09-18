package main

import (
	"context"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestReloadCommandTmuxRecordsUnderTheTmuxComponent: cmd/pfm's direct tmux
// calls terminate through the observed command. Against a socket nothing
// serves, tmux answers non-zero (or is absent) — either way exactly one
// comp=tmux record names the subcommand; no live server is touched.
func TestReloadCommandTmuxRecordsUnderTheTmuxComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	socket := filepath.Join(t.TempDir(), "no-server")
	if err := (reloadCommandTmux{}).command(ctx, socket, "kill-server").Run(); err == nil {
		t.Fatal("kill-server on a socket nothing serves succeeded")
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Message != "tmux.exec" {
		t.Fatalf("want one tmux.exec record: %s", recorder.Raw())
	}
	if comp, _ := records[0].Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "kill-server" {
		t.Fatalf("subcmd = %v", subcmd)
	}
	if _, found := records[0].Field(obs.FieldErr); !found {
		t.Fatalf("a failed invocation carries no err: %v", records[0].Fields)
	}
	_ = context.Background
}

// TestReloadCommandTmuxSetRemainAgainstNoServer: SetRemain's on/off branches
// both run their tmux invocation through the same command() door — against a
// socket nothing serves, both forms fail the same way the caller expects.
func TestReloadCommandTmuxSetRemainAgainstNoServer(t *testing.T) {
	ctx := context.Background()
	socket := filepath.Join(t.TempDir(), "no-server")
	tmux := reloadCommandTmux{}
	if err := tmux.SetRemain(ctx, socket, "%0", true); err == nil {
		t.Fatal("SetRemain(on) against a socket nothing serves succeeded")
	}
	if err := tmux.SetRemain(ctx, socket, "%0", false); err == nil {
		t.Fatal("SetRemain(off) against a socket nothing serves succeeded")
	}
}
