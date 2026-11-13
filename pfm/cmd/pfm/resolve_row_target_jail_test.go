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
	address, found, err := fleet.ResolveRow(
		context.Background(), database, agentID, &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if err != nil {
		t.Fatalf("ResolveRow() error = %v", err)
	}
	if !found {
		t.Fatal("ResolveRow() found = false, want true")
	}
	if address.Engine != pfmengine.Claude {
		t.Fatalf("engine = %q, want %q (stderr=%s)", address.Engine, pfmengine.Claude, stderr.String())
	}
	if address.RolloutPath != "" {
		t.Fatalf("rolloutPath = %q, want empty for a live Claude agent with no transcript yet", address.RolloutPath)
	}
	if address.Socket != socket {
		t.Fatalf("socket = %q, want %q", address.Socket, socket)
	}
	if address.PaneID != paneID {
		t.Fatalf("pane = %q, want %q", address.PaneID, paneID)
	}
}
