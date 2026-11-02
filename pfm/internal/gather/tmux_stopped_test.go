package gather

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stopServer SIGSTOPs the jail's own server on socket — a chat server frozen
// the way a live one was (state T) — and resumes it at cleanup, before the
// jail's kill-server runs.
func (jail *tmuxJail) stopServer(t *testing.T, socket string) int {
	t.Helper()
	output, err := jail.command("-L", socket, "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("read the pid of jailed server %s: %v", socket, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 0 {
		t.Fatalf("jailed server %s answered pid %q: %v", socket, output, err)
	}
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop jailed server %s (pid %d): %v", socket, pid, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
	return pid
}

// clientsLeft lists every process still addressing the jail's socket
// directory: a tmux client a timed-out call failed to end.
func (jail *tmuxJail) clientsLeft(t *testing.T) []string {
	t.Helper()
	output, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	var left []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, jail.tmuxDir) && !strings.Contains(line, "ps -eo") {
			left = append(left, strings.TrimSpace(line))
		}
	}
	return left
}

// within runs call and fails the test when it does not return inside bound:
// the hang the picker showed on stopped servers.
func within(t *testing.T, name string, bound time.Duration, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("%s on a stopped tmux server did not return within %s", name, bound)
		return nil
	}
}

// TestProbeCallsOnAStoppedServerReturnAndLeaveNoClient runs real tmux: a
// server SIGSTOPped after the scan listed it never answers, and every later
// call on it — the pid read a reap or theme sync makes, the pane capture the
// label and identity reads make — used to run with no deadline, hanging the
// caller and leaving its tmux client blocked forever. Each now ends inside
// the probe's bound with an error, and no client survives it.
func TestProbeCallsOnAStoppedServerReturnAndLeaveNoClient(t *testing.T) {
	jail := newTmuxJail(t)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	const socket = "probe-stopped"
	jail.startServer(t, socket, "work", "work", "title")
	jail.stopServer(t, socket)
	client := TmuxProbe{TmuxTmpDir: jail.root}
	ctx := context.Background()
	bound := ProbeTimeout + 3*time.Second

	if err := within(t, "ServerPID", bound, func() error {
		_, err := client.ServerPID(ctx, socket)
		return err
	}); !errors.Is(err, ErrServerUnresponsive) {
		t.Fatalf("ServerPID on a stopped server = %v, want %v", err, ErrServerUnresponsive)
	}
	if err := within(t, "CapturePane", bound, func() error {
		_, err := client.CapturePane(ctx, socket, "%0")
		return err
	}); !errors.Is(err, ErrServerUnresponsive) {
		t.Fatalf("CapturePane on a stopped server = %v, want %v", err, ErrServerUnresponsive)
	}
	if left := jail.clientsLeft(t); len(left) != 0 {
		t.Fatalf("tmux clients left blocked on the stopped server: %v", left)
	}
}

// TestProbeTmuxNamesEveryStoppedServerInOneBound runs real tmux: N stopped
// servers cost one probe bound together, not N, each is named unreadable —
// "could not look", never "no chats" — and its warning tells the user how to
// find a stopped server and resume it.
func TestProbeTmuxNamesEveryStoppedServerInOneBound(t *testing.T) {
	jail := newTmuxJail(t)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	sockets := []string{"probe-stop-1", "probe-stop-2", "probe-stop-3", "probe-stop-4"}
	for _, socket := range sockets {
		jail.startServer(t, socket, "work", "work", "title")
		jail.stopServer(t, socket)
	}
	client := TmuxProbe{TmuxTmpDir: jail.root}
	var probe TmuxSnapshot
	if err := within(t, "ProbeTmux", ProbeTimeout+3*time.Second, func() error {
		var err error
		probe, err = ProbeTmux(context.Background(), jail.tmuxDir, client, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, socket := range sockets {
		reason, unread := probe.Unreadable[socket]
		if !unread || !strings.Contains(reason, ErrServerUnresponsive.Error()) {
			t.Fatalf("stopped server %s: unreadable %v (%q), want it named unresponsive", socket, unread, reason)
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
