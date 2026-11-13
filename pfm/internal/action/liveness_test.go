package action

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// hungExecutor is an executor over the tmux door; run under hungServer, its
// server never answers (testjail.HungTmuxRunner).
func hungExecutor(t *testing.T) (*Executor, *bytes.Buffer) {
	t.Helper()
	root := jailAction(t)
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      TmuxExecutor{Binary: "tmux", TmuxDir: root},
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor, &stderr
}

// hungServer is a context whose tmux server never answers a pane read.
func hungServer() context.Context {
	return pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(1))
}

func TestOpenRefusesAnUnresponsiveLiveServer(t *testing.T) {
	for _, detached := range []bool{false, true} {
		executor, stderr := hungExecutor(t)
		request := Request{Detached: detached, Row: compose.Row{
			Kind: compose.LiveClaude, ID: "44444444-4444-4444-8444-444444444444",
			Socket: "cc-hung-1-1", SessionName: "live-session", Name: "Live",
		}}
		_, err := executor.Open(hungServer(), request)
		if got, want := rooted(
			t,
			fmt.Sprint(err),
		), map[bool]string{false: "live socket \"cc-hung-1-1\" is unresponsive and may still hold the chat; not resumed: tmux server did not answer within 2s: {root}/001/cc-hung-1-1", true: "open detached: live socket \"cc-hung-1-1\" is unresponsive and may still hold the chat; not resumed: tmux server did not answer within 2s: {root}/003/cc-hung-1-1"}[detached]; got != want ||
			stderr.String() != "" {
			t.Errorf(
				"detached=%t Open() over an unresponsive server stderr %q error = %q, want %q",
				detached,
				stderr,
				got,
				want,
			)
		}
	}
}

// TestWatchLaunchNeverReportsAnUnresponsiveServerDead: a launch pane that will
// not read on a server that does not answer is unknown, never "died at launch".
func TestWatchLaunchNeverReportsAnUnresponsiveServerDead(t *testing.T) {
	executor, _ := hungExecutor(t)
	server := ChatServer{Socket: "cc-hung-1-1", Window: "Claude"}
	warnings, err := executor.watchLaunch(hungServer(), server, "hung probe", "claude")
	if err != nil {
		t.Fatalf("watchLaunch() reported an unresponsive server dead: %v", err)
	}
	if got, want := rooted(
		t,
		fmt.Sprintf("%q", warnings),
	), "[\"pfm could not read hung probe's launch pane (read the launch pane on socket \\\"cc-hung-1-1\\\": exit status 1\\ntmux server did not answer within 2s: {root}/001/cc-hung-1-1); its server is unresponsive: whether it is up is unknown — tmux -L cc-hung-1-1 attach -t cc-hung-1-1\" \"remain-on-exit stayed on for cc-hung-1-1 (exit status 1): when the chat ends its dead pane stays — tmux -L cc-hung-1-1 kill-server clears it\"]"; got != want {
		t.Errorf("watchLaunch() warnings = %q, want %q", got, want)
	}
}

// TestListPanesReportsAnUnresponsiveServer: the executor's pane list is the
// one bounded pane read, so a hung server is ErrServerUnresponsive within
// gather's bound, never an empty server.
func TestListPanesReportsAnUnresponsiveServer(t *testing.T) {
	root := jailAction(t)
	panes, err := TmuxExecutor{
		Binary:  "tmux",
		TmuxDir: root,
	}.ListPanes(
		hungServer(),
		"cc-hung-1-1",
	)
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf("ListPanes() over an unresponsive server = %v, %v; want ErrServerUnresponsive", panes, err)
	}
}
