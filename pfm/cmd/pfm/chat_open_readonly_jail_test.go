package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/spawn"
)

// TestChatOpenScansReadOnly is the regression for chat.OpenID's fleet.Scan call: it
// must pass ReadOnly: true, because a WRITING gather wedges when the caller
// already holds the fleet store open in the same process (this is what made
// `pfm chat open` hang forever under `pfm mcp serve` while the identical call
// over a one-shot CLI returned in under a second). The wedge itself needs a
// live in-process daemon to reproduce, but ReadOnly's own effect on the tmux
// probe is directly observable and jailable: ProbeTmux (write) sweeps a dead
// "corpse" socket file older than an hour, ProbeTmuxReadOnly does not
// (internal/gather/tmuxprobe.go probeTmux's `sweep` flag; see
// TestProbeTmuxReadOnlyLeavesOldCorpse in internal/gather for the same
// contract one layer down). Planting a corpse socket, running `pfm chat
// open`, and asserting the corpse survives is therefore a faithful proxy for
// "chat.OpenID's scan requested ReadOnly".
func TestChatOpenScansReadOnly(t *testing.T) {
	root := jailTest(t)
	t.Setenv(spawn.TestFreshSocketEnv, "cc-1700000000-1-1")

	project := filepath.Join(root, "work", "project")
	transcriptDir := filepath.Join(root, "claude", "project")
	for _, directory := range []string{project, transcriptDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const id = "22222222-2222-4222-8222-222222222222"
	content := `{"type":"user","cwd":` + strconv.Quote(project) +
		`,"message":{"content":"Read-only open prompt"}}` + "\n"
	if err := os.WriteFile(
		filepath.Join(transcriptDir, id+".jsonl"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	corpse := plantCorpseSocket(t, filepath.Join(root, "tmux"), "cc-corpse-"+strconv.Itoa(os.Getpid()))

	var stdout, stderr bytes.Buffer
	if code := run([]string{"index", "--full"}, &stdout, &stderr); code != 0 {
		t.Fatalf("index code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	readServer := holdClaudeOpen(t, root, "cc-1700000000-1-1")
	if code := run([]string{"chat", "open", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("open code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "attach -t 'cc-1700000000-1-1'") {
		t.Fatalf("open stdout=%q", stdout.String())
	}
	if run := readServer("#{pane_start_command}"); !strings.Contains(run, "--resume") || !strings.Contains(run, id) {
		t.Fatalf("opened server runs %q, want the resume of %s", run, id)
	}

	if _, err := os.Stat(corpse); err != nil {
		t.Fatalf(
			"chat open swept the corpse socket %s (%v) — its scanFleet call is "+
				"not read-only, and a writing scan is the wedge this test guards "+
				"against",
			corpse, err,
		)
	}
}

// plantCorpseSocket lays down a real orphaned unix socket file — a listener
// that has been closed without unlinking, so the file carries os.ModeSocket
// and any tmux client connecting to it fails with ErrServerGone, exactly like
// a chat whose server already exited — and backdates it more than an hour so
// probeTmux's sweep threshold applies.
func plantCorpseSocket(t *testing.T, tmuxDir, name string) string {
	t.Helper()
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmuxDir, name)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	// Closing a *net.UnixListener normally unlinks its socket file; disabling
	// that is what leaves a real orphaned socket on disk — the exact shape a
	// dead chat's server leaves behind for probeTmux to find (or not) on the
	// next pass.
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}
