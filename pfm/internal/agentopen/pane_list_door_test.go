package agentopen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestSocketForPIDReportsAnUnresponsiveServer: a Claude server that does not
// answer may be the one running the agent, so the search reports it within
// gather's bound — never a hang, and never "no socket holds this pid".
func TestSocketForPIDReportsAnUnresponsiveServer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cc-1800000000-1-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socket, err := RealTmux{Binary: testjail.HungTmux(t, 1), Dir: dir}.SocketForPID(context.Background(), 42)
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf("SocketForPID() over an unresponsive server = %q, %v; want ErrServerUnresponsive", socket, err)
	}
}
