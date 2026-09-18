package inject

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestCommandThenSpawnerDefaultRunnerRecordsTheWaiterLaunch proves the nil
// Runner default is the observed one: the detached waiter's launch writes
// comp=runner records with the launcher's shape, and the steer text — a
// prompt body — never reaches the file.
func TestCommandThenSpawnerDefaultRunnerRecordsTheWaiterLaunch(t *testing.T) {
	ctx, recorder := obs.Test(t)
	scratch := t.TempDir()
	stub := filepath.Join(scratch, "setsid-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spawner := CommandThenSpawner{Executable: filepath.Join(scratch, "pfm"), Setsid: stub}
	err := spawner.Spawn(ctx, SteerSpawn{
		SocketPath: filepath.Join(scratch, "cx-1-2-3"), Target: "%3",
		Steers: []string{"resume the wave PLANTED-STEER"}, LogPath: filepath.Join(scratch, "steer.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 2 || records[0].Message != "runner.start" || records[1].Message != "runner.exit" {
		t.Fatalf("want runner.start then runner.exit from the default runner, got %s", recorder.Raw())
	}
	if comp, _ := records[0].Field(obs.FieldComp); comp != "runner" {
		t.Fatalf("comp = %v, want runner", comp)
	}
	if argv, _ := records[0].Field("argv"); argv != "setsid-stub" {
		t.Fatalf("argv shape = %v, want the launcher's base name", argv)
	}
	if strings.Contains(recorder.Raw(), "PLANTED-STEER") {
		t.Fatalf("the steer text reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}
