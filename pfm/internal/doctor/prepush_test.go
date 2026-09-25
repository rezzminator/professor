package doctor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// scriptHooksPathRead scripts a repository at the working directory, then
// answers the core.hooksPath read with result and err.
func scriptHooksPathRead(t *testing.T, result deps.RunResult, err error) *deps.FakeRunner {
	t.Helper()
	cwd, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatal(wdErr)
	}
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{"git", "-C", cwd, "rev-parse"},
		deps.RunResult{Stdout: []byte(cwd + "\n")},
		nil,
	)
	runner.Script([]string{"git", "-C", cwd, "config"}, result, err)
	return runner
}

func TestDoctorPrePushNamesHooksPathReadExitAndStderr(t *testing.T) {
	runner := scriptHooksPathRead(t, deps.RunResult{
		Stderr:   []byte("error: key does not contain a section: core\n"),
		ExitCode: 2,
	}, nil)
	gate := inspectPrePushGateWithRunner(context.Background(), runner)
	if gate.State != unreadableState || gate.Error == nil {
		t.Fatalf("gate state=%q error=%v, want %q with an error", gate.State, gate.Error, unreadableState)
	}
	message := gate.Error.Error()
	for _, want := range []string{"exit 2", "key does not contain a section"} {
		if !strings.Contains(message, want) {
			t.Fatalf("gate error %q does not name %q", message, want)
		}
	}
	if strings.Contains(message, "<nil>") {
		t.Fatalf("gate error %q renders a nil error", message)
	}
}

func TestDoctorPrePushWrapsHooksPathRunError(t *testing.T) {
	runErr := errors.New("git killed by signal")
	runner := scriptHooksPathRead(t, deps.RunResult{ExitCode: -1}, runErr)
	gate := inspectPrePushGateWithRunner(context.Background(), runner)
	if gate.State != unreadableState {
		t.Fatalf("gate state=%q error=%v, want %q", gate.State, gate.Error, unreadableState)
	}
	if !errors.Is(gate.Error, runErr) {
		t.Fatalf("gate error %v does not wrap the Run error", gate.Error)
	}
}
