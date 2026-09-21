package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestResolveRowTargetReturnsTheComposedRowsLiveAddress pins fleet.ResolveRow
// (renamed from resolveRowEngine) as the single place runKill's plain id path
// gets a row's live socket and pane from: hiding a live chat now also ends it
// (kill.Manager.Kill's `live` gate), and that gate only fires when THIS
// function hands its caller a real socket and pane, not just an engine.
func TestResolveRowTargetReturnsTheComposedRowsLiveAddress(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)

	socket := "probe-resolverowtarget-" + strconv.Itoa(os.Getpid())
	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start background pane: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-F", "#{pane_id} #{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(strings.TrimSpace(string(paneOutput)))
	if len(fields) != 2 {
		t.Fatalf("list-panes output = %q, want \"%%id pid\"", paneOutput)
	}
	paneID := fields[0]
	panePID, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatal(err)
	}

	const agentID = "e6666666-6666-4666-8666-666666666666"
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       90201,
		parentPID: panePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude", "--session-id", agentID},
		environ: map[string]string{
			"CLAUDE_CONFIG_DIR": filepath.Join(jail.home, ".cc", "2"),
		},
	})

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	var stderr bytes.Buffer
	engine, rolloutPath, gotSocket, gotPane := fleet.ResolveRow(
		context.Background(), database, agentID, &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if engine != pfmengine.Claude {
		t.Fatalf("engine = %q, want %q (stderr=%s)", engine, pfmengine.Claude, stderr.String())
	}
	if rolloutPath != "" {
		t.Fatalf("rolloutPath = %q, want empty for a live Claude agent with no transcript yet", rolloutPath)
	}
	if gotSocket != socket {
		t.Fatalf("socket = %q, want %q", gotSocket, socket)
	}
	if gotPane != paneID {
		t.Fatalf("pane = %q, want %q", gotPane, paneID)
	}
}

// TestResolveRowTargetResolvesNoLiveAddressForAResumableID: nothing live
// composes a resumable id, so runKill must not invent a pane for a chat the
// fleet does not see running — the very thing that would wrongly make a hide
// of it end a chat that was never live.
func TestResolveRowTargetResolvesNoLiveAddressForAResumableID(t *testing.T) {
	root := jailTest(t)
	const id = "f7777777-7777-4777-8777-777777777777"
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","cwd":"/work/project","message":{"content":"first"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: id, Path: transcriptPath, CWD: "/work/project", Size: 1, PromptCount: 3,
	}); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	engine, _, gotSocket, gotPane := fleet.ResolveRow(
		context.Background(), database, id, &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if engine != pfmengine.Claude {
		t.Fatalf("engine = %q, want %q (stderr=%s)", engine, pfmengine.Claude, stderr.String())
	}
	if gotSocket != "" || gotPane != "" {
		t.Fatalf("socket=%q pane=%q, want both empty for a resumable id", gotSocket, gotPane)
	}
}
