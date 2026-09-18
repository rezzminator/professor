package kill

import (
	"os"
	"path/filepath"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/obs"
)

// TestCommandSpawnerDefaultRunnerRecordsTheFinisherLaunch proves the nil
// Runner default is the observed one (obs.Runner over deps.RealRunner): a
// Spawn through it writes comp=runner start and exit records naming the
// launcher's base name, never the finisher's argument list.
func TestCommandSpawnerDefaultRunnerRecordsTheFinisherLaunch(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	setsid := filepath.Join(root, "setsid")
	if err := os.WriteFile(setsid, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spawner := CommandSpawner{Executable: filepath.Join(root, "pfm"), Setsid: setsid}
	err := spawner.Spawn(ctx, ExitArgs{
		Engine: pfmengine.Claude, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", DataPath: filepath.Join(root, "t.jsonl"),
		SocketPath: filepath.Join(root, "cc-2-1-1"), SocketName: "cc-2-1-1", PaneID: "%4",
	})
	if err != nil {
		t.Fatalf("Spawn through the default runner: %v", err)
	}
	records := recorder.Records()
	if len(records) != 2 || records[0].Message != "runner.start" || records[1].Message != "runner.exit" {
		t.Fatalf("want runner.start then runner.exit from the default runner, got %s", recorder.Raw())
	}
	for _, record := range records {
		if comp, _ := record.Field(obs.FieldComp); comp != "runner" {
			t.Fatalf("%s comp = %v, want runner", record.Message, comp)
		}
	}
	if argv, _ := records[0].Field("argv"); argv != "setsid" {
		t.Fatalf("argv shape = %v, want the launcher's base name", argv)
	}
	if exit, _ := records[1].Field(obs.FieldExit); exit != float64(0) {
		t.Fatalf("exit = %v, want 0", exit)
	}
	if _, found := records[1].Field(obs.FieldDur); !found {
		t.Fatalf("exit record has no dur_ms: %v", records[1].Fields)
	}
}
