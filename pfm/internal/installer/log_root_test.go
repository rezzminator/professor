package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestMoveLogsToRootMovesTheStateFolderOnce: with a clone on record, install
// files the activity log and its generations as segments named by their last
// write, appends the cost sink's log to the one the root already holds, drops
// the lock, moves the restart logs, removes both emptied folders and says so
// in one change row each; a second run has nothing left to move. A jailed
// home logs in its state folder and moves nothing.
func TestMoveLogsToRootMovesTheStateFolderOnce(t *testing.T) {
	for _, jailed := range []bool{false, true} {
		home, clone := t.TempDir(), filepath.Join(t.TempDir(), "professor")
		if err := os.Mkdir(clone, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
			t.Fatal(err)
		}
		old := filepath.Join(home, ".local", "state", "pfm", "log")
		restart := filepath.Join(home, ".local", "state", "pfm", "restart")
		root := filepath.Join(clone, "tmp", "logs", "pfm")
		aged := time.Date(2026, 11, 12, 11, 41, 16, 217_000_000, time.UTC)
		for name, body := range map[string]string{
			"pfm.jsonl": "current\n", "pfm.jsonl.1": "older\n", "pfm.jsonl.lock": "",
			"cost-sink.log": "old sink\n",
			// A segmented build that logged in the state folder left a segments folder the root also gets.
			filepath.Join("pfm.segments", "2026-11-01T00-00-00.000Z.jsonl"): "segmented\n",
		} {
			writeFixture(t, filepath.Join(old, name), body)
		}
		if err := os.Chtimes(filepath.Join(old, "pfm.jsonl.1"), aged, aged); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(restart, "restart-20261102T183630Z-1.log"), "restarted\n")
		writeFixture(t, filepath.Join(root, "cost-sink.log"), "new sink\n")
		env := &paths.MapEnv{}
		if jailed {
			env.Values = map[string]string{paths.EnvHome: home}
		}
		var output bytes.Buffer
		installer := &engine{options: Options{Home: home, Env: env, Stdout: &output}, apply: true}
		if err := installer.moveLogsToRoot(); err != nil {
			t.Fatal(err)
		}
		if jailed {
			if output.Len() != 0 || readFixture(t, filepath.Join(old, "pfm.jsonl")) != "current\n" {
				t.Fatalf("a jailed home moved its logs: %q", output.String())
			}
			continue
		}
		want := "  change  move 5 log files from " + old + " to " + root + "\n" +
			"  change  move 1 log files from " + restart + " to " + filepath.Join(root, "restart") + "\n"
		if output.String() != want {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
		for _, gone := range []string{old, restart} {
			if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s still exists (err %v)", gone, err)
			}
		}
		segments, err := os.ReadDir(filepath.Join(root, "pfm.segments"))
		if err != nil || len(segments) != 3 || segments[1].Name() != "2026-11-12T11-41-16.217Z.jsonl" {
			t.Fatalf("pfm.segments = %v (err %v), want the old segment, .1 under its last write, then the current",
				segments, err)
		}
		for path, body := range map[string]string{
			filepath.Join(root, "pfm.segments", segments[0].Name()):          "segmented\n",
			filepath.Join(root, "pfm.segments", segments[1].Name()):          "older\n",
			filepath.Join(root, "pfm.segments", segments[2].Name()):          "current\n",
			filepath.Join(root, "cost-sink.log"):                             "new sink\nold sink\n",
			filepath.Join(root, "restart", "restart-20261102T183630Z-1.log"): "restarted\n",
		} {
			if got := readFixture(t, path); got != body {
				t.Fatalf("%s = %q, want %q", path, got, body)
			}
		}
		if names := strings.Join(dirNames(t, root), ","); names != "cost-sink.log,pfm.segments,restart" {
			t.Fatalf("log root = %s, want the moved files and nothing else", names)
		}
		output.Reset()
		if err := installer.moveLogsToRoot(); err != nil || output.Len() != 0 {
			t.Fatalf("second run: err %v, output %q; want nothing to move", err, output.String())
		}
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
