package main

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startIdlessSession starts one real tmux session named name on its own jailed
// socket, running a bare `sleep` — a live pane that carries no Claude, hence no
// session id — and returns the socket name and the pane id.
func startIdlessSession(t *testing.T, socket, name string) string {
	t.Helper()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	start := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", name, "sleep", "120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start background pane %s on %s: %v: %s", name, socket, err, output)
	}
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-t", name, "-F", "#{pane_id}",
	).Output()
	if err != nil {
		t.Fatalf("list panes of %s on %s: %v", name, socket, err)
	}
	return strings.TrimSpace(string(paneOutput))
}

func paneAlive(socket, pane string) bool {
	output, err := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Fields(string(output)) {
		if line == pane {
			return true
		}
	}
	return false
}

// A chat that resolves to a live tmux address but carries no session id (the
// raw tmux rung finds a bare pane by its session name) is closed by name: the
// pane goes, no tombstone is written, and the outcome line says "closed".
func TestChatKillByNameClosesALivePaneThatCarriesNoSessionID(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	newKillCLIJail(t)
	socket := "probe-killidless-" + strconv.Itoa(os.Getpid())
	pane := startIdlessSession(t, socket, "walk-probe")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", "walk-probe"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat kill walk-probe code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "closed ") {
		t.Fatalf("stdout=%q, want a closed outcome (no kill recorded)", stdout.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for paneAlive(socket, pane) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if paneAlive(socket, pane) {
		t.Fatalf("pane %s on %s still alive after `chat kill walk-probe`", pane, socket)
	}
}

// Two live seats answering to one name: nothing is killed by guess. The refusal
// names both candidates and both panes keep running.
func TestChatKillByAmbiguousNameRefusesAndKillsNothing(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	newKillCLIJail(t)
	first := "probe-killamb-a-" + strconv.Itoa(os.Getpid())
	second := "probe-killamb-b-" + strconv.Itoa(os.Getpid())
	firstPane := startIdlessSession(t, first, "walk-twin")
	secondPane := startIdlessSession(t, second, "walk-twin")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", "walk-twin"}, &stdout, &stderr); code == 0 {
		t.Fatalf("ambiguous kill exited 0: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, socket := range []string{first, second} {
		if !strings.Contains(stderr.String(), socket) {
			t.Fatalf("stderr=%q, want the candidate on socket %s named", stderr.String(), socket)
		}
	}
	if !paneAlive(first, firstPane) || !paneAlive(second, secondPane) {
		t.Fatalf("an ambiguous kill closed a pane: first alive=%v second alive=%v",
			paneAlive(first, firstPane), paneAlive(second, secondPane))
	}
}

// A name that resolves to a chat with no session id has no kill recorded
// against it, so unkill says so instead of lifting a tombstone that cannot
// exist.
func TestChatUnkillByNameOfAnIdlessChatRefuses(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	newKillCLIJail(t)
	socket := "probe-unkillidless-" + strconv.Itoa(os.Getpid())
	pane := startIdlessSession(t, socket, "walk-unkill")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "unkill", "walk-unkill"}, &stdout, &stderr); code != 1 {
		t.Fatalf("unkill code=%d stdout=%q stderr=%q, want 1", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "carries no session id") {
		t.Fatalf("stderr=%q, want the no-session-id refusal", stderr.String())
	}
	if !paneAlive(socket, pane) {
		t.Fatalf("unkill closed pane %s", pane)
	}
}
