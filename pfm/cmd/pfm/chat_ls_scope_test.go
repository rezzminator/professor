package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestChatLSScopesByRepositoryNotByPathPrefix pins `pfm chat ls`'s repo scope
// to the chat's repository: a seat an orchestrator started in a linked
// worktree lists from the repo root, and from another repository it is only
// counted in the "+N live in other dirs" footer.
func TestChatLSScopesByRepositoryNotByPathPrefix(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	fixture := testjail.GitRepoWithWorktrees(t, "acme")
	jail := newKillCLIJail(t)
	socket := "cc-" + strconv.FormatInt(time.Now().Unix(), 10) + "-" + strconv.Itoa(os.Getpid()) + "-7"
	if output, err := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-c", fixture.Outer, "sleep", "120",
	).CombinedOutput(); err != nil {
		t.Fatalf("start worktree pane %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	paneID := strings.TrimSpace(runTmuxOutput(t, socket, "list-panes", "-F", "#{pane_id}"))
	crumb := filepath.Join(jail.root, "sid", socket+"."+paneID)
	if err := os.WriteFile(crumb, []byte("/nonexistent/"+socket+".jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	list := func(dir string) string {
		t.Helper()
		t.Chdir(dir)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"chat", "ls"}, &stdout, &stderr); code != 0 {
			t.Fatalf("chat ls from %s code=%d stdout=%q stderr=%q", dir, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	if got := list(fixture.Repo); !strings.Contains(got, socket) {
		t.Fatalf("chat ls from the repo root omits the worktree seat %q:\n%s", socket, got)
	}
	if got := list(fixture.Other); strings.Contains(got, socket) ||
		!strings.Contains(got, "(+1 live in other dirs") {
		t.Fatalf("chat ls from another repository = %q, want the seat counted as elsewhere", got)
	}
}

// TestChatLSListsAndReapsAnAbandonedEmptyServer is issue 34's fourth problem
// end to end on real tmux: a pfm chat server left running with no session (an
// abandoned empty server) used to surface as a bare "exit status 1" probe
// warning with no fix. chat ls now names the server and the reap command, and
// `chat ls --reap` ends it — while a server under a name pfm does not own and
// a server that still has a live pane are never touched.
func TestChatLSListsAndReapsAnAbandonedEmptyServer(t *testing.T) {
	newKillCLIJail(t)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	const empty, foreign, live = "probe-abandoned-empty", "work-empty", "probe-live-pane"
	for _, socket := range []string{empty, foreign, live} {
		if output, err := exec.Command(
			"tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "only", "sleep", "120",
		).CombinedOutput(); err != nil {
			t.Fatalf("start tmux server %q: %v: %s", socket, err, output)
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	}
	for _, socket := range []string{empty, foreign} {
		runTmuxOutput(t, socket, "set-option", "-g", "exit-empty", "off")
		runTmuxOutput(t, socket, "kill-session", "-t", "only")
	}
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	listed := stderr.String()
	if !strings.Contains(listed, "pfm: tmux probe warning: "+empty+": abandoned empty tmux server") ||
		!strings.Contains(listed, "pfm chat ls --reap") || strings.Contains(listed, "exit status 1") {
		t.Fatalf("chat ls stderr = %q, want the empty server named with the reap fix", listed)
	}
	if strings.Contains(listed, foreign) || strings.Contains(listed, live) {
		t.Fatalf("chat ls flagged a server it does not own or one with a live pane: %q", listed)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "ls", "--reap"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls --reap code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, empty+" (pid ") || !strings.Contains(got, "ended by SIGTERM") ||
		strings.Contains(got, foreign) || strings.Contains(got, live) {
		t.Fatalf("chat ls --reap stdout = %q, want only %s ended by SIGTERM", got, empty)
	}
	if output, err := exec.Command("tmux", "-L", empty, "list-sessions").CombinedOutput(); err == nil {
		t.Fatalf("the reaped server still answers: %s", output)
	}
	runTmuxOutput(t, foreign, "display-message", "-p", "#{pid}")
	runTmuxOutput(t, live, "list-panes", "-a")
}
