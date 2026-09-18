package agentopen

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"hostops/pfm/internal/obs"
)

// fakeAgentopenTmuxBinary plays tmux: list-panes answers a fixed pid.
func fakeAgentopenTmuxBinary(t *testing.T, pid int) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\nprintf '" + strconv.Itoa(pid) + "\\n'\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// TestRealTmuxRecordsEveryInvocation proves RealTmux.command crosses the
// observed tmux door (pfmtmux.Exec, not the bare pfmtmux.Command) — pattern:
// internal/kill/tmux_test.go.
func TestRealTmuxRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cc-1-1-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tmux := RealTmux{Binary: fakeAgentopenTmuxBinary(t, 4242), Dir: dir}
	socket, err := tmux.SocketForPID(ctx, 4242)
	if err != nil {
		t.Fatal(err)
	}
	if socket != "cc-1-1-1" {
		t.Fatalf("socket = %q, want cc-1-1-1", socket)
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	if records[0].Message != "tmux.exec" {
		t.Fatalf("record = %s, want tmux.exec", records[0].Message)
	}
	if comp, _ := records[0].Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if subcmd, _ := records[0].Field("subcmd"); subcmd != "list-panes" {
		t.Fatalf("subcmd = %v, want list-panes", subcmd)
	}
	_ = context.Background
}
