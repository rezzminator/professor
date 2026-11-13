package herdr

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// An attach that cannot start herdr is an error, never a silent return the
// viewer would read as a finished session.
func TestAttachReportsAHerdrThatCannotStart(t *testing.T) {
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("herdr", "", &exec.Error{Name: "herdr", Err: exec.ErrNotFound})
	var stdout, stderr bytes.Buffer
	err := Attach(
		WithAttachRunner(context.Background(), runner),
		"/fixture/herdr.sock",
		"w1:p1",
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	want := `resolve herdr: exec: "herdr": executable file not found in $PATH`
	if err == nil || err.Error() != want {
		t.Fatalf("Attach() error = %v, want %q", err, want)
	}
}
