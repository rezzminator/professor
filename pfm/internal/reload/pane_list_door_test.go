package reload

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TestListPanesReportsAnUnresponsiveServer: the worker's pane list is the one
// bounded pane read, so a hung server is ErrServerUnresponsive within gather's
// bound — never a wait on it, never an empty server.
func TestListPanesReportsAnUnresponsiveServer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	// The probe bound is timed on the clock the context carries: the fake fires it.
	fake := clock.NewFake(time.Unix(0, 0))
	ctx := gather.WithProbeClock(pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(1)), fake)
	type answer struct {
		panes []Pane
		err   error
	}
	socket := filepath.Join(t.TempDir(), "cc-hung-1-1")
	answered := make(chan answer, 1)
	go func() {
		panes, err := CommandTmux{}.ListPanes(ctx, socket)
		answered <- answer{panes, err}
	}()
	testwait.Until(t, "the pane read to arm its probe bound", func() (bool, string) {
		step, due := fake.NextDue()
		return due && step == gather.ProbeTimeout, fmt.Sprintf("next due %v (%v); %s", step, due, fake)
	})
	fake.Advance(gather.ProbeTimeout)
	got, _ := testwait.Receive(t, "the pane read to give up at its bound", answered)
	if !errors.Is(got.err, gather.ErrServerUnresponsive) {
		t.Fatalf("ListPanes() over an unresponsive server = %v, %v; want ErrServerUnresponsive", got.panes, got.err)
	}
}
