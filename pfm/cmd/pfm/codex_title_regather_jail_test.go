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

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
)

// codexJailPanePID returns the real pid tmux reports for the sole pane on
// socket — the same "-L socket" / TMUX_TMPDIR addressing startCodexStatusPane
// uses — so a fake /proc process fixture can declare it as its own parent and
// be found by the real process scan's pid-ancestry walk (paneForProcess).
func codexJailPanePID(t *testing.T, tmuxTmpDir, socket string) int {
	t.Helper()
	capture := exec.Command("tmux", "-L", socket, "list-panes", "-F", "#{pane_pid}")
	capture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
	output, err := capture.Output()
	if err != nil {
		t.Fatalf("read pane pid for %q: %v", socket, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", output, err)
	}
	return pid
}

// codexRegatherJailFixture is the T4 probe shape shared by scanFleet's
// one-shot regather and the interactive stream's first-pass regather: a pane
// bound to oldID whose screen has already moved to the bare successor id
// newID, plus a rollout-less live Codex process (the shared app-server
// shape) parented to the pane's real pid, identified ONLY through
// kill.Manager.CodexPaneBound — the pane's own fleet-recorded binding, read
// fresh on every fleet.Gather call. It sets TMUX_TMPDIR/PFM_TMUX_DIR so any
// caller resolving paths.Values fresh from the environment (scanFleet,
// streamFleetRefreshesWith — neither takes a runtime override) finds this
// real tmux server, registers both rollouts in pfm's own store, and advances
// the pane's binding to oldID before returning.
func codexRegatherJailFixture(
	t *testing.T,
) (database *store.Store, manager *kill.Manager, socket, oldID, newID string) {
	t.Helper()
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	// jailTest already pins PFM_TMUX_DIR to its OWN (empty) directory, which
	// outranks TMUX_TMPDIR in paths.Resolve()'s own EnvOr(EnvTmuxDir, ...) —
	// so TMUX_TMPDIR alone is not enough; both must point at the same real
	// server. The reconcile-only tests elsewhere in this package instead pass
	// an explicit commandRuntime and never need this.
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	t.Setenv("PFM_TMUX_DIR", filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid())))
	socket = "cx-1800000030-1-1"
	oldID = "88888888-8888-4888-8888-888888888888"
	newID = "99999999-9999-4999-8999-999999999999"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+newID+` · /work/example · Full Access\n`)
	panePID := codexJailPanePID(t, tmuxTmpDir, socket)

	var err error
	database, err = store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	codexJailRollout(t, database, root, oldID, 1)
	codexJailRollout(t, database, root, newID, 1)

	manager, err = kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", oldID); err != nil {
		t.Fatal(err)
	}

	// A rollout-less live Codex process (empty fd/ dir: FDLinks succeeds,
	// finds nothing) exporting no CODEX_THREAD_ID — its ONLY identity is the
	// pane's own fleet-recorded binding, resolved through
	// store.NewCodexThreadResolverRoots.
	writeFakeProcess(t, filepath.Join(root, "proc"), fakeProcessSpec{
		pid:       90301,
		parentPID: panePID,
		comm:      "codex",
		cmdline:   []string{"/usr/local/bin/codex"},
		withFD:    true,
	})
	return database, manager, socket, oldID, newID
}

// assertNoStaleCodexRow fails the test if the clear-killed predecessor still
// renders LIVE, or the successor does not, and returns the successor row's
// pointer (nil if it never appeared) for a caller that wants to inspect more.
func assertNoStaleCodexRow(t *testing.T, rows []compose.Row, oldID, newID, contextLabel string) *compose.Row {
	t.Helper()
	var successorRow *compose.Row
	for index := range rows {
		row := &rows[index]
		if row.ID == oldID && row.Kind == compose.LiveCodex {
			t.Fatalf(
				"%s: the clear-killed predecessor %s still rendered LIVE from a stale gather: %#v",
				contextLabel, oldID, row,
			)
		}
		if row.ID == newID {
			successorRow = row
		}
	}
	if successorRow == nil || successorRow.Kind != compose.LiveCodex {
		t.Fatalf(
			"%s: successor %s did not render LIVE after the moved binding (rendered %#v instead): rows=%#v",
			contextLabel, newID, successorRow, rows,
		)
	}
	return successorRow
}

// T4 — a reconcile pass that moves a binding must not compose from the
// gather it moved the binding OUT from under: scanFleet's own regather
// (ls_pipeline.go, the block after fleet.ReconcileCodexPanes returns true).
//
// Before the reconcile pass runs, the pane's binding is still the PRE-clear
// thread (oldID); the pane's own screen has already moved to the bare
// successor id (newID), so fleet.ReconcileCodexPanes moves the binding to newID
// and clear-kills oldID. A compose from the FIRST gather renders oldID live
// (stale) and newID merely resumable; only a second gather, taken after the
// binding moved, resolves the same rollout-less process to newID.
func TestScanFleetRegathersAfterAMovedBindingSoTheSuccessorRendersLive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	database, manager, socket, oldID, newID := codexRegatherJailFixture(t)

	var stderr bytes.Buffer
	result, err := scanFleet(context.Background(), database, scanRequest{}, &stderr)
	if err != nil {
		t.Fatalf("scanFleet() error = %v: stderr=%q", err, stderr.String())
	}

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != newID {
		t.Fatalf(
			"binding = (%q, %v, %v), want the screen's own thread %q: stderr=%q",
			bound, found, err, newID, stderr.String(),
		)
	}
	assertNoStaleCodexRow(t, result.Output.Rows, oldID, newID, "scanFleet")
}

// T4, the interactive stream's twin of the test above: streamFleetRefreshesWith
// runs the SAME "gather, reconcile, regather-if-moved, compose" shape for its
// very first pass (ls_pipeline.go, the movedBinding block between the priority
// index run and the first non-Refreshing send) — a materially different code
// path from scanFleet's, not a shared helper, so a fix or regression in one
// does not guarantee the other. The periodic loop's OWN copy of this same
// block (the second of streamFleetRefreshesWith's two sites) is NOT covered
// here: reaching it needs the pane's screen to change on a running stream
// between ticks (a real fleetRefreshInterval, 5s, wait plus a live tmux
// send-keys mid-stream) with no gather-call-counter seam in
// refreshDependencies to shortcut it — named as a gap in the test report
// rather than faked.
func TestInteractiveRefreshFirstPassRegathersAfterAMovedBinding(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	database, manager, socket, oldID, newID := codexRegatherJailFixture(t)

	var stderr bytes.Buffer
	updates := make(chan ui.Snapshot, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) {
				return &immediateIndexRunner{}, nil
			},
		},
	)

	var final ui.Snapshot
	found := false
	for snapshot := range updates {
		if !snapshot.Refreshing {
			final, found = snapshot, true
			break
		}
	}
	cancel()
	for range updates {
	}
	if !found {
		t.Fatalf("interactive refresh stream ended before its first completed pass: stderr=%q", stderr.String())
	}

	bound, boundFound, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !boundFound || bound != newID {
		t.Fatalf(
			"binding = (%q, %v, %v), want the screen's own thread %q: stderr=%q",
			bound, boundFound, err, newID, stderr.String(),
		)
	}
	assertNoStaleCodexRow(t, final.Rows, oldID, newID, "streamFleetRefreshesWith first pass")
}
