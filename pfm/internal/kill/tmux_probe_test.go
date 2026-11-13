package kill

import (
	"context"
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TestPaneExistsReportsAnUnresponsiveServer: the kill grace window's probe of a
// server that does not answer is gather's unresponsive verdict within its bound,
// never a hang and never "gone".
func TestPaneExistsReportsAnUnresponsiveServer(t *testing.T) {
	ctx := pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(1))
	exists, err := TmuxKiller{Binary: "tmux"}.PaneExists(ctx, t.TempDir()+"/cc-hung", "%0")
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf("PaneExists() over an unresponsive server = %v, %v; want ErrServerUnresponsive", exists, err)
	}
}

// TestPanesByTTYReportsAnUnresponsiveServer: the viewport join's read of a
// bunker that does not answer is gather's bounded unresponsive verdict — the
// one pane read — never a wait on the server and never "no viewports".
func TestPanesByTTYReportsAnUnresponsiveServer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	ctx := pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(1))
	panes, err := TmuxKiller{Binary: "tmux"}.PanesByTTY(ctx, t.TempDir()+"/bunker")
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf("PanesByTTY() over an unresponsive server = %v, %v; want ErrServerUnresponsive", panes, err)
	}
}
