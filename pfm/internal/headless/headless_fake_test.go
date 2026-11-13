package headless

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// writeEngine puts an engine binary named name in directory, which only has to
// resolve: the engine child starts through a fake Runner and nothing ever runs
// it.
func writeEngine(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("make engine directory %s: %v", directory, err)
	}
	if err := testjail.WriteExecutableNeverRun(
		filepath.Join(directory, name),
		[]byte("#!/bin/sh\n"),
		0o700,
	); err != nil {
		t.Fatalf("write engine binary %s: %v", filepath.Join(directory, name), err)
	}
}

// fakeEngine is the Runner of a test: every engine child it starts plays output.
func fakeEngine(output deps.StartOutput) *deps.FakeRunner {
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, output)
	return runner
}

// startedStdin is what the engine child's start was handed on stdin: its prompt.
func startedStdin(t *testing.T, runner *deps.FakeRunner, index int) string {
	t.Helper()
	starts := runner.Starts()
	if index >= len(starts) {
		t.Fatalf("engine starts = %d, want a start %d", len(starts), index)
	}
	raw, err := io.ReadAll(starts[index].Opts.Stdin)
	if err != nil {
		t.Fatalf("read engine stdin: %v", err)
	}
	return string(raw)
}

// removePreparedExchange is the AtStart of a child that consumes the prepared
// exchange file the way a real engine's cleanup race would: exactly one file
// under directory, removed before the run returns.
func removePreparedExchange(t *testing.T, directory string) func([]string, deps.StartOptions) {
	return func([]string, deps.StartOptions) {
		matches, err := filepath.Glob(filepath.Join(directory, "exchange-*.md"))
		if err != nil || len(matches) != 1 {
			t.Errorf("prepared exchange files in %s = %v (%v), want exactly one", directory, matches, err)
			return
		}
		if err := os.Remove(matches[0]); err != nil {
			t.Errorf("remove prepared exchange %s: %v", matches[0], err)
		}
	}
}
