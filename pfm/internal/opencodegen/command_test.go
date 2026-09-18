package opencodegen

import (
	"bytes"
	"testing"
)

func TestRunCommandRejectsUnknownAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCommand(
		[]string{"generate"},
		func() (string, error) { return ".", nil },
		".",
		&stdout,
		&stderr,
	); code != 2 {
		t.Fatalf("unknown action code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
