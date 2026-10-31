package action

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// hungExecutor is an executor over the real tmux door whose binary is a server
// that never answers (testjail.HungTmux).
func hungExecutor(t *testing.T) (*Executor, *bytes.Buffer) {
	t.Helper()
	root := jailAction(t)
	binary := testjail.HungTmux(t, 1)
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      TmuxExecutor{Binary: binary, TmuxDir: root},
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

// TestOpenRefusesAnUnresponsiveLiveServer: a live row whose tmux server does not
// answer may still hold the chat; reading it as dead resumed a second copy of
// the conversation beside it.
func TestOpenRefusesAnUnresponsiveLiveServer(t *testing.T) {
	executor, stderr := hungExecutor(t)
	request := Request{Row: compose.Row{
		Kind: compose.LiveClaude, ID: "44444444-4444-4444-8444-444444444444",
		Socket: "cc-hung-1-1", SessionName: "live-session", Name: "Live",
	}}
	_, err := executor.Open(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "unresponsive") {
		t.Fatalf("Open() over an unresponsive server = %v, want an unresponsive refusal; stderr %q", err, stderr)
	}
	if strings.Contains(stderr.String(), "resuming") {
		t.Fatalf("Open() resumed beside an unresponsive server: %q", stderr)
	}
}

// TestOpenDetachedRefusesAnUnresponsiveLiveServer is the detached caller's half.
func TestOpenDetachedRefusesAnUnresponsiveLiveServer(t *testing.T) {
	executor, _ := hungExecutor(t)
	request := Request{Detached: true, Row: compose.Row{
		Kind: compose.LiveClaude, ID: "44444444-4444-4444-8444-444444444444",
		Socket: "cc-hung-1-1", SessionName: "live-session", Name: "Live",
	}}
	_, err := executor.Open(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "unresponsive") {
		t.Fatalf("detached Open() over an unresponsive server = %v, want an unresponsive refusal", err)
	}
}

// TestWatchLaunchNeverReportsAnUnresponsiveServerDead: a launch pane that will
// not read on a server that does not answer is unknown, never "died at launch".
func TestWatchLaunchNeverReportsAnUnresponsiveServerDead(t *testing.T) {
	executor, _ := hungExecutor(t)
	server := ChatServer{Socket: "cc-hung-1-1", Window: "Claude"}
	warnings, err := executor.watchLaunch(context.Background(), server, "hung probe", "claude")
	if err != nil {
		t.Fatalf("watchLaunch() reported an unresponsive server dead: %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "whether it is up is unknown") {
		t.Fatalf("watchLaunch() warnings %q lack the unknown verdict", warnings)
	}
}

// TestListPanesReportsAnUnresponsiveServer: the executor's pane list is the
// one bounded pane read, so a hung server is ErrServerUnresponsive within
// gather's bound, never an empty server.
func TestListPanesReportsAnUnresponsiveServer(t *testing.T) {
	root := jailAction(t)
	panes, err := TmuxExecutor{
		Binary:  testjail.HungTmux(t, 1),
		TmuxDir: root,
	}.ListPanes(
		context.Background(),
		"cc-hung-1-1",
	)
	if !errors.Is(err, gather.ErrServerUnresponsive) {
		t.Fatalf("ListPanes() over an unresponsive server = %v, %v; want ErrServerUnresponsive", panes, err)
	}
}
