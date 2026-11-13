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

	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// The binding table and the meta store are two different things, and an
// unreadable one must never read as an empty one.
func TestCodexPaneBindingsDistinguishAnEmptyTableFromAnUnreadableOne(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := manager.CodexPaneBindings(ctx)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("empty table = (%v, %v), want (no bindings, no error)", bindings, err)
	}

	// Closing the store turns every read into a failure. The empty answer and
	// the failed answer must not be the same answer.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CodexPaneBindings(ctx); err == nil {
		t.Fatal("a closed store returned no error — a failed read read as an empty table")
	}

	var stdout bytes.Buffer
	if warnings := doctor.PrintCodexPaneBinding(
		ctx, &stdout, database, commandRuntime{},
	); warnings == 0 {
		t.Fatalf("doctor called an unreadable binding table healthy: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "codex_pane_bindings=unreadable") {
		t.Fatalf("doctor did not name the read failure: %s", stdout.String())
	}
}

// A binding whose pane is long gone is ordinary history, not a contest. This
// host held 78 bindings for 19 live panes; counting the dead ones would have
// reported a fleet-wide emergency made almost entirely of litter, and a check
// that cries wolf fails the same way as one that stays silent.
func TestCodexPaneBindingDoctorCountsDeadPaneBindingsAsStale(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx := context.Background()
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}

	// Two DEAD panes sharing one thread: no tmux server was ever started for
	// either socket.
	const shared = "99999999-9999-4999-8999-999999999999"
	for _, socket := range []string{"cx-1700000090-1-1", "cx-1700000091-1-1"} {
		if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", shared); err != nil {
			t.Fatal(err)
		}
	}

	var stdout bytes.Buffer
	warnings := doctor.PrintCodexPaneBinding(ctx, &stdout, database, commandRuntime{Paths: resolved})
	report := stdout.String()
	if strings.Contains(report, "contested") && !strings.Contains(report, "contested=0") {
		t.Fatalf("dead-pane litter was reported as contested:\n%s", report)
	}
	if !strings.Contains(report, "codex_pane_bindings total=2 live=0 stale=2 contested=0 retired=0") {
		t.Fatalf("the totals do not reconcile:\n%s", report)
	}
	if warnings != 0 {
		t.Fatalf("stale bindings raised %d warnings:\n%s", warnings, report)
	}
}
