package gather

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
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
