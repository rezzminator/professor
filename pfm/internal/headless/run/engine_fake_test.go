package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// engineBinary is a path the engine binary resolves to and nothing ever runs:
// Resolve needs an executable file, and the engine child starts through a fake
// Request.Runner.
func engineBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine")
	if err := testjail.WriteExecutableNeverRun(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write engine binary %s: %v", path, err)
	}
	return path
}

// fakeEngine is the Request.Runner of a test: the engine child it starts
// plays output and starts nowhere.
func fakeEngine(output deps.StartOutput) *deps.FakeRunner {
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, output)
	return runner
}

// onlyStart is the one child a run started.
func onlyStart(t *testing.T, runner *deps.FakeRunner) deps.StartCall {
	t.Helper()
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("engine starts = %d (%v), want exactly 1", len(starts), starts)
	}
	return starts[0]
}

// hostEnv is the parent environment plus the entries a test sets.
func hostEnv(values ...string) []string {
	return append(append([]string(nil), os.Environ()...), values...)
}

func envHas(environment []string, name string) bool {
	for _, entry := range environment {
		if key, _, found := strings.Cut(entry, "="); found && key == name {
			return true
		}
	}
	return false
}
