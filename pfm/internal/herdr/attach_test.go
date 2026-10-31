package herdr

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// An attach that cannot start herdr is an error, never a silent return the
// viewer would read as a finished session.
func TestAttachReportsAHerdrThatCannotStart(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := Attach(context.Background(), "/fixture/herdr.sock", "w1:p1", strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("attach without a herdr binary succeeded")
	}
}
