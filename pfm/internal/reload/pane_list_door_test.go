package reload

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TestListPanesReportsAnUnresponsiveServer: the worker's pane list is the one
// bounded pane read, so a hung server is ErrServerUnresponsive within gather's
// bound — never a wait on it, never an empty server.
func TestListPanesReportsAnUnresponsiveServer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	ctx := pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(1))
	panes, err := CommandTmux{}.ListPanes(ctx, filepath.Join(t.TempDir(), "cc-hung-1-1"))
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf("ListPanes() over an unresponsive server = %v, %v; want ErrServerUnresponsive", panes, err)
	}
}
