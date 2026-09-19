package mcpserv

import (
	"context"
	"io"
	"testing"
	"time"
)

// A client holds a stdio server's input open for the whole session, so a read
// on it never returns. Ending the context must still end RunStdio: the
// replaced-executable guard cancels the context and relies on the return to
// exit the process.
func TestRunStdioReturnsOnCancelWhileInputStaysOpen(t *testing.T) {
	service := newIssuesTestService(t)
	input, holdOpen := io.Pipe()
	defer func() { _ = holdOpen.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.RunStdio(ctx, input, io.Discard) }()

	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("RunStdio did not return after its context ended while input stayed open")
	}
}
