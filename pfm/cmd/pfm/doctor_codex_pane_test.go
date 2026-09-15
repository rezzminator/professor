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

	"hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/store"
)

// A binding table in the exact state a real host was found in — two panes on
// one thread, and that thread already retired by a clear — must say so. Before
// this section existed, both facts sat in the meta table for weeks while
// `pfm doctor` printed "clean".
func TestCodexPaneBindingDoctorNamesContestedAndRetiredBindings(t *testing.T) {
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
	defer database.Close()
	ctx := context.Background()

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	const shared = "01a02dca-c83c-7871-bdf1-461c75441c77"
	for _, socket := range []string{"cx-1787594704-779857-6265", "cx-1787594801-791822-11110"} {
		// Both panes must be LIVE: a binding whose socket is gone is ordinary
		// history, and counting it as contested would report litter as an
		// emergency.
		startCodexStatusPane(t, tmuxTmpDir, socket, `  ENGINE_BUILDER · /work/example · Full Access\n`)
		if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", shared); err != nil {
			t.Fatal(err)
		}
	}
	baseline := int64(7)
	if err := database.Kill(ctx, store.Killed{
		ID: shared, Engine: pfmengine.Codex, KilledAt: 1, BaselinePrompts: &baseline,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	warnings := printCodexPaneBindingDoctor(ctx, &stdout, database, commandRuntime{Paths: resolved})
	report := stdout.String()
	if warnings < 2 {
		t.Fatalf("warnings = %d, want at least 2 (contested + retired): %s", warnings, report)
	}
	for _, want := range []string{
		"codex_pane_binding=contested",
		"codex_pane_binding=retired-thread",
		"codex_pane_bindings total=2 live=2 stale=0 contested=1 retired=1",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
}

// The other half of the same contract: a healthy table must NOT print the same
// words. A check that says "contested" in both states is worth nothing.
func TestCodexPaneBindingDoctorStaysQuietOnAHealthyTable(t *testing.T) {
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
	defer database.Close()
	ctx := context.Background()

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	const socket = "cx-1787757492-3196324-4837"
	const threadID = "01a03ea6-8276-7141-b1ff-1a813901371a"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+threadID+` · /work/example · Full Access\n`)
	codexJailRollout(t, database, root, threadID, 1)
	if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", threadID); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	warnings := printCodexPaneBindingDoctor(ctx, &stdout, database, commandRuntime{Paths: resolved})
	report := stdout.String()
	if warnings != 0 {
		t.Fatalf("healthy table reported %d warnings: %s", warnings, report)
	}
	if strings.Contains(report, "warning") {
		t.Fatalf("healthy table printed a warning: %s", report)
	}
	if !strings.Contains(report, "codex_pane_bindings total=1 live=1 stale=0 contested=0 retired=0") {
		t.Fatalf("healthy table did not report its own count: %s", report)
	}
}

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
	if warnings := printCodexPaneBindingDoctor(
		ctx, &stdout, database, commandRuntime{},
	); warnings == 0 {
		t.Fatalf("doctor called an unreadable binding table healthy: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "codex_pane_bindings=unreadable") {
		t.Fatalf("doctor did not name the read failure: %s", stdout.String())
	}
}

// The pane pfm cannot follow must be named on request, because the reconcile
// pass is deliberately silent about it. Silence on the hot path is only
// defensible while some other surface says it out loud — this is the test that
// keeps that bargain honest.
func TestCodexPaneDoctorNamesAPaneItCannotFollow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000020-1-1"
	startCodexStatusPane(t, tmuxTmpDir, socket, `  FIX_HAND · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()

	var stdout bytes.Buffer
	warnings := printCodexPaneBindingDoctor(ctx, &stdout, database, commandRuntime{Paths: resolved})
	report := stdout.String()
	if warnings == 0 {
		t.Fatalf("doctor called an unfollowable pane healthy:\n%s", report)
	}
	if !strings.Contains(report, "codex_pane=unfollowable") ||
		!strings.Contains(report, fleet.CodexPaneNameUnknown) {
		t.Fatalf("doctor did not name the pane it cannot follow:\n%s", report)
	}
	if !strings.Contains(report, "codex_panes live=1 unfollowable=1") {
		t.Fatalf("doctor did not report its own coverage:\n%s", report)
	}
}

// And the other half: a pane pfm CAN follow must not be reported as one it
// cannot. A check that says "unfollowable" either way says nothing.
func TestCodexPaneDoctorStaysQuietOnAFollowablePane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000021-1-1"
	const threadID = "66666666-6666-4666-8666-666666666666"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+threadID+` · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	codexJailRollout(t, database, root, threadID, 1)

	var stdout bytes.Buffer
	warnings := printCodexPaneBindingDoctor(ctx, &stdout, database, commandRuntime{Paths: resolved})
	report := stdout.String()
	if warnings != 0 || strings.Contains(report, "unfollowable socket") {
		t.Fatalf("a followable pane was reported unfollowable (%d warnings):\n%s", warnings, report)
	}
	if !strings.Contains(report, "codex_panes live=1 unfollowable=0") {
		t.Fatalf("doctor did not report its own coverage:\n%s", report)
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
	defer database.Close()
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
	warnings := printCodexPaneBindingDoctor(ctx, &stdout, database, commandRuntime{Paths: resolved})
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

func TestCodexPaneDoctorUsesHeldRootDespiteModelFirstStatus(t *testing.T) {
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	const socket = "cx-1800000051-1-1"
	const id = "11111111-2222-4333-8444-555555555555"
	startCodexStatusPane(t, tmuxTmpDir, socket, `  gpt-6-astra · NAMED_SEAT · /work/example · Full Access\n`)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	path := codexJailRollout(t, database, root, id, 1)
	if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", id); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{
		Paths:  resolved,
		Config: config.Config{CodexAccounts: []config.CodexAccount{{ID: 1, Home: filepath.Join(root, "codex")}}},
	}
	panes, err := liveCodexPanes(ctx, runtime)
	if err != nil || len(panes) != 1 {
		t.Fatalf("panes=%#v err=%v", panes, err)
	}
	writeFakeProcess(
		t,
		resolved.ProcRoot,
		fakeProcessSpec{pid: 900001, parentPID: panes[0].PID, comm: "codex", cmdline: []string{"codex"}, withFD: true},
	)
	if err := os.Symlink(path, filepath.Join(resolved.ProcRoot, "900001", "fd", "3")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	warnings := printCodexPaneBindingDoctor(ctx, &output, database, runtime)
	if warnings != 0 || strings.Contains(output.String(), "unfollowable socket=") {
		t.Fatalf("held root was dropped: warnings=%d\n%s", warnings, output.String())
	}
	bound, found, err := manager.CodexPaneBinding(ctx, socket, "%0")
	if err != nil || !found || bound != id {
		t.Fatalf("read-only doctor changed binding: %q %v %v", bound, found, err)
	}
}
