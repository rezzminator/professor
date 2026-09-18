package codexappendix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestRPCRecordsTheAppServerProcess: the registration helper's `codex
// app-server` child is a direct process door — its start and exit write
// comp=runner records; the RPC method and params never reach the file.
func TestRPCRecordsTheAppServerProcess(t *testing.T) {
	ctx, recorder := obs.Test(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "native")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := rpc(ctx, binary, dir, "hooks/list", map[string]any{"cwds": []string{"PLANTED-CWD"}}); err == nil {
		t.Fatal("a helper that answers nothing must fail")
	}
	records := recorder.Records()
	if len(records) != 2 || records[0].Message != "runner.start" || records[1].Message != "runner.exit" {
		t.Fatalf("want runner.start then runner.exit, got %s", recorder.Raw())
	}
	if comp, _ := records[0].Field(obs.FieldComp); comp != "runner" {
		t.Fatalf("comp = %v, want runner", comp)
	}
	if argv, _ := records[0].Field("argv"); argv != "native" {
		t.Fatalf("argv shape = %v, want the binary's base name", argv)
	}
	if strings.Contains(recorder.Raw(), "hooks/list") || strings.Contains(recorder.Raw(), "PLANTED-CWD") {
		t.Fatalf("an RPC argument reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}
