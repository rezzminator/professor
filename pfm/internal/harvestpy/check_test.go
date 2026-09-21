package harvestpy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestCheckInterpreterFailureIncludesStderr(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.Script(
		[]string{"fake-python", "--version"},
		deps.RunResult{Stdout: []byte("python banner\n"), Stderr: []byte("loader: wrong architecture\n"), ExitCode: -1},
		errors.New("start failed"),
	)

	err := checkInterpreter(context.Background(), fake, "fake-python", "3.11.15+20260610")
	if err == nil || !strings.Contains(err.Error(), "loader: wrong architecture") {
		t.Fatalf("checkInterpreter() error = %v, want stderr diagnostic", err)
	}
}
