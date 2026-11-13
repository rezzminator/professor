//go:build jail

package kill

// The default runner really starts the child and its observation records are the subject: a fake runner
// would replace the very runner under test.

import (
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestCommandSpawnerDefaultRunnerRecordsTheFinisherLaunch proves the nil
// Runner default is the observed one (obs.Runner over deps.RealRunner): a
// Spawn through it writes comp=runner start and release records naming the
// finisher's base name, never its argument list.
func TestCommandSpawnerDefaultRunnerRecordsTheFinisherLaunch(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	finisher := filepath.Join(root, "pfm")
	if err := testjail.WriteExecutable(finisher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spawner := CommandSpawner{Executable: finisher, LogDir: root}
	err := spawner.Spawn(ctx, ExitArgs{
		Engine: pfmengine.Claude, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", DataPath: filepath.Join(root, "t.jsonl"),
		SocketPath: filepath.Join(root, "cc-2-1-1"), SocketName: "cc-2-1-1", PaneID: "%4",
	})
	if err != nil {
		t.Fatalf("Spawn through the default runner: %v", err)
	}
	records := recorder.Records()
	if len(records) != 2 || records[0].Message != "runner.start" || records[1].Message != "runner.release" {
		t.Fatalf("want runner.start then runner.release from the default runner, got %s", recorder.Raw())
	}
	for _, record := range records {
		if comp, _ := record.Field(obs.FieldComp); comp != "runner" {
			t.Fatalf("%s comp = %v, want runner", record.Message, comp)
		}
	}
	if argv, _ := records[0].Field("argv"); argv != "pfm" {
		t.Fatalf("argv shape = %v, want the finisher's base name", argv)
	}
	if argc, _ := records[0].Field("argc"); argc != float64(15) {
		t.Fatalf("argc = %v, want 15", argc)
	}
	if _, found := records[1].Field(obs.FieldDur); !found {
		t.Fatalf("release record has no dur_ms: %v", records[1].Fields)
	}
}
