//go:build jail

package gather

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// endAtProbeBound runs call, whose probes arm `armed` bounds on fake, and ends
// them by moving fake to ProbeTimeout once every ready condition holds: a
// moment before it every bound still waits, at it call returns its error.
func endAtProbeBound(
	t *testing.T, fake *clock.Fake, armed int, call func() error, ready ...func() (bool, string),
) error {
	t.Helper()
	returned := make(chan error, 1)
	go func() { returned <- call() }()
	testwait.Until(t, fmt.Sprintf("%d probe bounds to be armed", armed), func() (bool, string) {
		return fake.Pending() == armed, fake.String()
	})
	for _, condition := range ready {
		testwait.Until(t, "the probed clients to block on their servers", condition)
	}
	fake.Advance(ProbeTimeout - time.Nanosecond)
	if pending := fake.Pending(); pending != armed {
		t.Fatalf(
			"%d of %d probe bounds fired %s before %s: %s",
			armed-pending, armed, time.Nanosecond, ProbeTimeout, fake,
		)
	}
	fake.Advance(time.Nanosecond)
	err, _ := testwait.Receive(t, "the probes to end at their bound", returned)
	return err
}

// within runs every call at once and fails the test when any never returns —
// the hang the picker showed on stopped servers; it returns each call's error
// by name.
func within(t *testing.T, calls map[string]func() error) map[string]error {
	t.Helper()
	type returned struct {
		name string
		err  error
	}
	done := make(chan returned, len(calls))
	for name, call := range calls {
		go func() { done <- returned{name, call()} }()
	}
	errs := make(map[string]error, len(calls))
	for len(errs) < len(calls) {
		var pending []string
		for name := range calls {
			if _, ended := errs[name]; !ended {
				pending = append(pending, name)
			}
		}
		call, _ := testwait.Receive(t, fmt.Sprintf("%v on a stopped tmux server to return", pending), done)
		errs[call.name] = call.err
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
	// The scan's probes are timed on a fake: one move past the bound ends all
	// four, so N stopped servers cost one bound.
	fake := clock.NewFake(time.Unix(0, 0))
	var probe TmuxSnapshot
	if err := endAtProbeBound(t, fake, len(sockets), func() error {
		var err error
		probe, err = ProbeTmux(WithProbeClock(context.Background(), fake), jail.tmuxDir, client, time.Now())
		return err
	}); err != nil {
		t.Fatalf("ProbeTmux over stopped servers = %v, want each named unreadable", err)
	}
	// The pid read and the pane capture are bounded inside the tmux door, timed
	// on a second fake: both clients are proven blocked on their stopped
	// servers, then one move to the bound ends them together.
	callClock := clock.NewFake(time.Unix(0, 0))
	ctx := pfmtmux.WithBoundClock(context.Background(), callClock)
	var errs map[string]error
	ended := endAtProbeBound(t, callClock, 2, func() error {
		errs = within(t, map[string]func() error{
			"ServerPID": func() error {
				_, err := client.ServerPID(ctx, sockets[0])
				return err
			},
			"CapturePane": func() error {
				_, err := client.CapturePane(ctx, sockets[1], "%0")
				return err
			},
		})
		return nil
	}, func() (bool, string) {
		left := jail.clientsLeft(t)
		return len(left) == 2, fmt.Sprintf("clients blocked: %v", left)
	})
	if ended != nil {
		t.Fatalf("the stopped calls ended with %v, want their errors by name", ended)
	}
	for _, name := range []string{"ServerPID", "CapturePane"} {
		if err := errs[name]; !errors.Is(err, ErrServerUnresponsive) {
			t.Fatalf("%s on a stopped server = %v, want %v", name, err, ErrServerUnresponsive)
		}
	}
	for _, socket := range sockets {
		want := "tmux server did not answer within 2s: " + socket
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
