//go:build jail

package gather

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// stoppedProbeTimeout is the probe bound these tests carry on the context of
// every call they point at a stopped server (probeTimeoutKey): production's
// two seconds would only make them wait longer for the same answer.
const stoppedProbeTimeout = 200 * time.Millisecond

// stoppedBound is how long one bounded call on a stopped server may take:
// the probe bound, plus the tmux door's half second for a killed client whose
// stdout the stopped server still holds, plus slack. A scan that probed four
// stopped servers one after another would take near three seconds.
const stoppedBound = stoppedProbeTimeout + time.Second

// stoppedProbe is ctx carrying stoppedProbeTimeout as its probe bound.
func stoppedProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, probeTimeoutKey{}, stoppedProbeTimeout)
}

// within runs every call at once and fails the test when any does not return
// inside bound — the hang the picker showed on stopped servers; it returns
// each call's error by name.
func within(t *testing.T, bound time.Duration, calls map[string]func() error) map[string]error {
	t.Helper()
	type returned struct {
		name string
		err  error
	}
	done := make(chan returned, len(calls))
	for name, call := range calls {
		go func() { done <- returned{name, call()} }()
	}
	timeout := time.After(bound)
	errs := make(map[string]error, len(calls))
	for len(errs) < len(calls) {
		select {
		case call := <-done:
			errs[call.name] = call.err
		case <-timeout:
			var pending []string
			for name := range calls {
				if _, ended := errs[name]; !ended {
					pending = append(pending, name)
				}
			}
			t.Fatalf("%v on a stopped tmux server did not return within %s", pending, bound)
		}
	}
	return errs
}

// TestProbeCallsOnStoppedServersReturnInOneBoundAndLeaveNoClient runs real
// tmux: servers SIGSTOPped after they started never answer. N stopped servers
// cost one probe bound together, not N, each is named unreadable — "could not
// look", never "no chats" — and its warning tells the user how to find a
// stopped server and resume it. Every later call on one — the pid read a reap
// or theme sync makes, the pane capture the label and identity reads make —
// used to run with no deadline, hanging the caller and leaving its tmux client
// blocked forever; each now ends inside the bound with an error. No client
// survives any of them.
func TestProbeCallsOnStoppedServersReturnInOneBoundAndLeaveNoClient(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newTmuxJail(t)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	sockets := []string{"probe-stop-1", "probe-stop-2", "probe-stop-3", "probe-stop-4"}
	for _, socket := range sockets {
		jail.startServer(t, socket, "work", "work", "title")
		jail.stopServer(t, socket)
	}
	client := TmuxProbe{TmuxTmpDir: jail.root}
	ctx := stoppedProbe(context.Background())
	var probe TmuxSnapshot
	errs := within(t, stoppedBound, map[string]func() error{
		"ProbeTmux": func() error {
			var err error
			probe, err = ProbeTmux(ctx, jail.tmuxDir, client, time.Now())
			return err
		},
		"ServerPID": func() error {
			_, err := client.ServerPID(ctx, sockets[0])
			return err
		},
		"CapturePane": func() error {
			_, err := client.CapturePane(ctx, sockets[1], "%0")
			return err
		},
	})
	if err := errs["ProbeTmux"]; err != nil {
		t.Fatalf("ProbeTmux over stopped servers = %v, want each named unreadable", err)
	}
	for _, name := range []string{"ServerPID", "CapturePane"} {
		if err := errs[name]; !errors.Is(err, ErrServerUnresponsive) {
			t.Fatalf("%s on a stopped server = %v, want %v", name, err, ErrServerUnresponsive)
		}
	}
	for _, socket := range sockets {
		want := ErrServerUnresponsive.Error() + " within " + stoppedProbeTimeout.String() + ": " + socket
		if reason, unread := probe.Unreadable[socket]; !unread || reason != want {
			t.Fatalf("stopped server %s: unreadable %v (%q), want %q", socket, unread, reason, want)
		}
	}
	if len(probe.ProbeWarnings) != len(sockets) {
		t.Fatalf("warnings = %q, want one per stopped server", probe.ProbeWarnings)
	}
	for _, warning := range probe.ProbeWarnings {
		if !strings.Contains(warning, "kill -CONT") {
			t.Fatalf("warning %q does not tell how to resume a stopped server", warning)
		}
	}
	if left := jail.clientsLeft(t); len(left) != 0 {
		t.Fatalf("tmux clients left blocked on the stopped servers: %v", left)
	}
}
