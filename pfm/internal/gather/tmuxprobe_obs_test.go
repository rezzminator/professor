package gather

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestTmuxProbeCommandsRecordUnderTheTmuxComponent: every TmuxProbe
// invocation — the -L addressed branch outside internal/tmux.Command — writes
// the same one comp=tmux record per call the façades write: subcmd, target,
// exit, dur_ms; never the captured pane text.
func TestTmuxProbeCommandsRecordUnderTheTmuxComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\ncase \"$3\" in capture-pane) echo 'PLANTED pane text';; show) echo on;; esac\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client := TmuxProbe{Binary: binary, TmuxTmpDir: t.TempDir()}
	if _, err := client.CapturePane(ctx, "cc-1-2-3", "%4"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ShowGlobalOption(ctx, "cc-1-2-3", "set-titles"); err != nil {
		t.Fatal(err)
	}
	if err := client.ApplyGlobalOptions(ctx, "cc-1-2-3", [][]string{{"set", "-g", "set-titles", "on"}}); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 3 {
		t.Fatalf("records = %d, want one per invocation: %s", len(records), recorder.Raw())
	}
	for index, want := range []string{"capture-pane", "show", "set"} {
		record := records[index]
		if record.Message != "tmux.exec" {
			t.Fatalf("record %d = %s, want tmux.exec", index, record.Message)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "tmux" {
			t.Fatalf("comp = %v, want tmux", comp)
		}
		if subcmd, _ := record.Field("subcmd"); subcmd != want {
			t.Fatalf("record %d subcmd = %v, want %s", index, subcmd, want)
		}
		if exit, _ := record.Field(obs.FieldExit); exit != float64(0) {
			t.Fatalf("exit = %v", exit)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	if target, _ := records[0].Field("target"); target != "%4" {
		t.Fatalf("capture-pane target = %v, want %%4", target)
	}
	if strings.Contains(recorder.Raw(), "PLANTED") {
		t.Fatalf("pane text reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}
