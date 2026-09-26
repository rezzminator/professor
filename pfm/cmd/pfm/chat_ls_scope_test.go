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
