package headless

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// startHeadlessTmuxSession mirrors internal/call's newCallTmuxJail
// (tmux_jail_test.go): a real tmux server on a scratch socket rooted at
// the current UID's tmux directory, never t.TempDir() (its test-name-derived path blows past
// the unix socket sun_path limit). TMUX_TMPDIR is still pinned per
// pfm/CLAUDE.md's testing rule even though capturePane below is handed the
// ABSOLUTE socket path and never consults it.
func startHeadlessTmuxSession(t *testing.T) (socketPath, session string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	tmuxJailRoot := paths.TmuxDirUnder(paths.TempRoot())
	if err := os.MkdirAll(tmuxJailRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(tmuxJailRoot, "probe-pfm-headless-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", root)
	socketPath = filepath.Join(root, "sock")
	session = "ask-pane"
	command := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-x", "80", "-y", "10",
		"sh", "-c", "echo live pane content; sleep 60",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start tmux session: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove headless tmux jail: %v", err)
		}
	})
	return socketPath, session
}

// TestAskCapturesLivePaneAndReportsPaneOnlyWithoutExchange pins the third
// distinct wording: a live pane that captured fine but has no recorded human
// exchange yet reads as PANE-ONLY, never as an empty transcript-only answer.
func TestAskCapturesLivePaneAndReportsPaneOnlyWithoutExchange(t *testing.T) {
	socketPath, session := startHeadlessTmuxSession(t)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	writeSummaryStub(t, bin, "claude", "cat >/dev/null\nprintf '%s\\n' '{\"result\":\"quietly idling\"}'")
	// Prepend, not replace: capturePane still needs the REAL tmux binary
	// from the ambient PATH to reach the session started above.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tempDir := filepath.Join(root, "tmp")
	options := AskOptions{Config: summaryMachine(t, "claude"), TempDir: tempDir}
	chat := Chat{
		Name: "seat", Engine: "cc", Path: "", Live: true,
		Socket: socketPath, Session: session,
	}

	result := Ask(context.Background(), chat, options)
	if !strings.HasPrefix(result.Text, "PANE-ONLY (no human exchange recorded yet): ") {
		t.Fatalf("pane-only ask=%+v", result)
	}
	assertNoLeakedTempFiles(t, tempDir)
}

func writeSummaryStub(t *testing.T, directory, name, body string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(
		filepath.Join(directory, name),
		[]byte("#!/bin/sh\n"+body+"\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
}
