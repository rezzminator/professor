package cli

import (
	"bytes"
	"io"
	"testing"
)

func TestHelpOperationsRefusesExtraArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunHelp([]string{"--ops", "extra"}, &stdout, &stderr, nil); code != 2 {
		t.Fatalf("help --ops extra code = %d, want 2", code)
	}
	if got, want := stderr.String(), "usage: pfm help --ops\n"; got != want {
		t.Fatalf("help --ops extra stderr = %q, want %q", got, want)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("help --ops extra stdout = %q, want empty", got)
	}
}

type closedCensusWriter struct{}

func (closedCensusWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestHelpOperationsReportsWriteFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := RunHelp([]string{"--ops"}, closedCensusWriter{}, &stderr, nil); code != 1 {
		t.Fatalf("help --ops code = %d, want 1", code)
	}
	if got, want := stderr.String(), "pfm help --ops: write census: io: read/write on closed pipe\n"; got != want {
		t.Fatalf("help --ops stderr = %q, want %q", got, want)
	}
}
