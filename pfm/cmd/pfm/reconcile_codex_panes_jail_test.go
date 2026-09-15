package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/store"
)

// startCodexStatusPane brings up a real tmux server on a scratch socket
// under tmuxTmpDir — gather.CommandTmux's own addressing (-L socket plus
// TMUX_TMPDIR, landing the socket at tmuxTmpDir/tmux-<uid>/socket) — with
// one pane that paints statusLine and holds. A codex pane's identity lives
// on its own screen and nowhere else, so fleet.ReconcileCodexPanes must read a
// REAL capture, not a mock.
// statusLine is printf's OWN format string (raw, run through a shell), so it
// must escape a literal "%" as "%%" and a newline as "\\n", exactly the way
// TestClaudeWindowNameConvergesOnARealServer (internal/gather) does for the
// claude half of the same mechanism.
func startCodexStatusPane(t *testing.T, tmuxTmpDir, socket, statusLine string) {
	t.Helper()
	if err := os.MkdirAll(tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"tmux", "-f", "/dev/null", "-L", socket,
		"new-session", "-d", "-s", socket, "-n", "codex",
		"printf '"+statusLine+"'; sleep 120",
	)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed codex pane %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
		_ = kill.Run()
	})
	waitForPaneToPaint(t, tmuxTmpDir, socket, statusLine)
}

// waitForPaneToPaint blocks until the pane has actually rendered its status
// line. `new-session -d` returns once the SESSION exists, not once its command
// has run, so under load a capture can arrive before the printf lands — and a
// pane that has not painted yet is byte-for-byte indistinguishable from a pane
// with no identity at all. That is the very confusion these tests exist to
// catch, so a fixture that can produce it silently turns the whole file into a
// coincidence detector: green when the code is right, and green when the code
// is wrong but the pane simply painted in time.
//
// A pane that never paints is a FIXTURE failure and says so in those words —
// it is not evidence about the code under test.
func waitForPaneToPaint(t *testing.T, tmuxTmpDir, socket, statusLine string) {
	t.Helper()
	// The fixture passes the line as a printf FORMAT; the trailing \n is the
	// only escape it uses, so the painted text is the literal before it.
	want := strings.TrimSpace(strings.TrimSuffix(statusLine, `\n`))
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", socket)
		capture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
		output, err := capture.CombinedOutput()
		last = string(output)
		if err == nil && strings.Contains(last, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("FIXTURE: pane %q never painted %q within 5s; last capture=%q", socket, want, last)
}

func codexJailRollout(
	t *testing.T,
	database *store.Store,
	root, id string,
	promptCount int64,
) string {
	t.Helper()
	rolloutPath := filepath.Join(
		root, "codex", "sessions", "2030", "01", "02",
		"rollout-2030-01-02T03-04-05-"+id+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + id + `","thread_source":"user","cwd":"/work/example"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}`,
		"",
	}, "\n")
	if err := os.WriteFile(rolloutPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(context.Background(), store.Rollout{
		ID: id, Path: rolloutPath, CWD: "/work/example", UserThread: true,
		PromptCount: promptCount,
	}); err != nil {
		t.Fatal(err)
	}
	return rolloutPath
}

func codexPane(socket, paneID string) gather.Pane {
	return gather.Pane{
		Socket:         socket,
		SessionName:    socket,
		WindowID:       "@1",
		PaneID:         paneID,
		CurrentCommand: "codex",
	}
}

// The pipeline-level shape of the whole fix: a pane bound to T1 whose status
// line now shows a DIFFERENT bare thread id T2 — a clear happened — is
// killed with a prompt baseline, exactly like Claude's SessionEnd hook gets,
// and the binding advances to T2.
func TestReconcileCodexPanesKillsThePreviousBoundThreadAndAdvancesTheBinding(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000001-1-1"
	const newID = "22222222-2222-4222-8222-222222222222"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+newID+` · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const oldID = "11111111-1111-4111-8111-111111111111"
	codexJailRollout(t, database, root, oldID, 1)

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", oldID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != newID {
		t.Fatalf("binding = (%q, %v, %v), want the new thread: stderr=%q", bound, found, err, stderr.String())
	}
	killed, found, err := database.Killed(context.Background(), oldID)
	if err != nil || !found || killed.Engine != pfmengine.Codex ||
		killed.BaselinePrompts == nil || *killed.BaselinePrompts != 1 {
		t.Fatalf("previous thread killed = %#v found=%v error=%v: stderr=%q", killed, found, err, stderr.String())
	}
}

// A capture that FAILED — the pane's socket carries no server at all — must
// never be read as "this pane runs nothing": nothing is killed, the
// existing binding stands, and the failure is named on stderr in words
// distinct from an ordinary unnamed thread.
func TestReconcileCodexPanesCaptureFailedKillsNothingAndNamesTheFailure(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	if err := os.MkdirAll(tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const socket = "cx-1800000002-1-1"

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const oldID = "33333333-3333-4333-8333-333333333333"
	codexJailRollout(t, database, root, oldID, 1)

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", oldID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	// No server was ever started on this socket: capture-pane fails.
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	if !strings.Contains(stderr.String(), "capture failed") {
		t.Fatalf("stderr = %q, want a capture-failed message", stderr.String())
	}
	if _, found, err := database.Killed(context.Background(), oldID); err != nil || found {
		t.Fatalf("a failed capture killed the bound thread: found=%v error=%v", found, err)
	}
	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != oldID {
		t.Fatalf("binding moved on a failed capture: bound=%q found=%v error=%v", bound, found, err)
	}
}

// The regression the retired process-slot design could not pass: two panes
// share a cwd, only one clears. Only that pane's previous thread is killed;
// the other pane's own binding, and thread, stay untouched.
func TestReconcileCodexPanesOnlyKillsTheClearingPaneInASharedCWD(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const clearingSocket = "cx-1800000003-1-1"
	const steadySocket = "cx-1800000003-2-2"
	const clearedID = "44444444-4444-4444-8444-444444444444"
	startCodexStatusPane(t, tmuxTmpDir, clearingSocket, "  "+clearedID+` · /work/shared · Full Access\n`)
	startCodexStatusPane(t, tmuxTmpDir, steadySocket, `  STEADY_CHAT · /work/shared · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const clearingOldID = "55555555-5555-4555-8555-555555555555"
	const steadyID = "66666666-6666-4666-8666-666666666666"
	codexJailRollout(t, database, root, clearingOldID, 1)
	codexJailRollout(t, database, root, steadyID, 1)
	if err := database.ReplaceCxNames(context.Background(), []store.CxName{
		{ID: steadyID, ThreadName: "STEADY_CHAT"},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), clearingSocket, "%0", clearingOldID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), steadySocket, "%0", steadyID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{
			codexPane(clearingSocket, "%0"),
			codexPane(steadySocket, "%0"),
		}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	if _, found, err := database.Killed(context.Background(), clearingOldID); err != nil || !found {
		t.Fatalf(
			"clearing pane's previous thread was not killed: found=%v error=%v stderr=%q",
			found,
			err,
			stderr.String(),
		)
	}
	if _, found, err := database.Killed(context.Background(), steadyID); err != nil || found {
		t.Fatalf(
			"the steady pane's own thread was killed too: found=%v error=%v stderr=%q",
			found,
			err,
			stderr.String(),
		)
	}
	bound, found, err := manager.CodexPaneBinding(context.Background(), steadySocket, "%0")
	if err != nil || !found || bound != steadyID {
		t.Fatalf("the steady pane's own binding moved: bound=%q found=%v error=%v", bound, found, err)
	}
}

// Duplicate display names are ordinary fleet state, not a broken tmux probe.
// When the pane is already bound to one of the matching threads, that binding
// is the only safe disambiguator: keep it steady without printing a warning
// when the picker releases the terminal.
func TestReconcileCodexPanesUsesExistingBindingForDuplicateName(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000005-1-1"
	startCodexStatusPane(t, tmuxTmpDir, socket, `  FIX_HAND · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const boundID = "99999999-9999-4999-8999-999999999999"
	const duplicateID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	codexJailRollout(t, database, root, boundID, 1)
	codexJailRollout(t, database, root, duplicateID, 1)
	if err := database.ReplaceCxNames(context.Background(), []store.CxName{
		{ID: boundID, ThreadName: "FIX_HAND"},
		{ID: duplicateID, ThreadName: "FIX_HAND"},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", boundID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	if stderr.Len() != 0 {
		t.Fatalf("steady duplicate name printed a shutdown warning: %q", stderr.String())
	}
	got, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || got != boundID {
		t.Fatalf("binding = (%q, %v, %v), want unchanged %q", got, found, err, boundID)
	}
	if _, found, err := database.Killed(context.Background(), boundID); err != nil || found {
		t.Fatalf("steady bound thread was killed: found=%v error=%v", found, err)
	}
}

func TestReconcileCodexPanesSkipsDuplicateNameWithoutUsableBindingQuietly(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, test := range []struct {
		name      string
		boundID   string
		wantBound bool
	}{
		{name: "no incumbent binding", wantBound: false},
		{name: "incumbent is not one of the matches", boundID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", wantBound: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := jailTest(t)
			tmuxTmpDir := filepath.Join(root, "tmuxtmp")
			socket := "cx-1800000006-" + strings.ReplaceAll(test.name, " ", "-")
			startCodexStatusPane(t, tmuxTmpDir, socket, `  FIX_HAND · /work/example · Full Access\n`)

			resolved := jailPaths(t)
			resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()

			const firstID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
			const secondID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			codexJailRollout(t, database, root, firstID, 1)
			codexJailRollout(t, database, root, secondID, 1)
			if err := database.ReplaceCxNames(context.Background(), []store.CxName{
				{ID: firstID, ThreadName: "FIX_HAND"},
				{ID: secondID, ThreadName: "FIX_HAND"},
			}); err != nil {
				t.Fatal(err)
			}

			manager, err := kill.New(database, kill.Dependencies{})
			if err != nil {
				t.Fatal(err)
			}
			if test.boundID != "" {
				codexJailRollout(t, database, root, test.boundID, 1)
				if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", test.boundID); err != nil {
					t.Fatal(err)
				}
			}

			var stderr bytes.Buffer
			fleet.ReconcileCodexPanes(
				context.Background(),
				database,
				gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
				commandRuntime{Paths: resolved},
				fleet.PrintWarn(&stderr),
			)
			if stderr.Len() != 0 {
				t.Fatalf("valid duplicate-name state printed a shutdown warning: %q", stderr.String())
			}
			bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
			if err != nil {
				t.Fatal(err)
			}
			if found != test.wantBound || (found && bound != test.boundID) {
				t.Fatalf("binding = (%q, %v), want incumbent (%q, %v)", bound, found, test.boundID, test.wantBound)
			}
			for _, id := range []string{firstID, secondID} {
				if _, killed, err := database.Killed(context.Background(), id); err != nil || killed {
					t.Fatalf("duplicate-name ambiguity killed %s: killed=%v error=%v", id, killed, err)
				}
			}
		})
	}
}

func TestReconcileCodexPanesKeepsBoundThreadSilentWhenNameIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000007-1-1"
	startCodexStatusPane(t, tmuxTmpDir, socket, `  · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const boundID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	codexJailRollout(t, database, root, boundID, 3)
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", boundID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)
	if stderr.Len() != 0 {
		t.Fatalf("empty Codex name printed a shutdown warning: %q", stderr.String())
	}
	got, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || got != boundID {
		t.Fatalf("empty-name binding = (%q, %v, %v), want unchanged %q", got, found, err, boundID)
	}
	if _, killed, err := database.Killed(context.Background(), boundID); err != nil || killed {
		t.Fatalf("empty Codex name killed incumbent: killed=%v error=%v", killed, err)
	}
}

// codexJailChildRollout is codexJailRollout for a thread that CONTINUES
// another — a resume or a fork, which Codex lands as a child rollout in the
// same pane. Its lineage root is the parent's, and that is exactly what tells
// a resume apart from a clear.
func codexJailChildRollout(
	t *testing.T,
	database *store.Store,
	root, id, parent string,
	promptCount int64,
) {
	t.Helper()
	rolloutPath := filepath.Join(
		root, "codex", "sessions", "2030", "01", "02",
		"rollout-2030-01-02T03-04-06-"+id+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","payload":{"id":"` + id +
		`","thread_source":"user","cwd":"/work/example"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(context.Background(), store.Rollout{
		ID: id, Path: rolloutPath, CWD: "/work/example", UserThread: true,
		PromptCount: promptCount, ParentThread: parent,
	}); err != nil {
		t.Fatal(err)
	}
}

// THE regression, at the pipeline level, over a real tmux server.
//
// A pane has already cleared: pfm retired the old thread, advanced the binding
// to the new one, and re-applied the chat's name to it. cx_names has not
// caught up — Codex's index is refreshed on its own schedule — so the display
// name still resolves to exactly ONE thread, the dead one.
//
// The old pass took that unique match as the pane's identity, moved the
// binding BACKWARD onto the corpse, and clear-killed the live thread that had
// replaced it. Both panes named ENGINE_BUILDER on a real host ended that way:
// bound to one already-killed thread, with `pfm chat resolve` answering the
// corpse and every inject aimed at a thread nobody was running.
//
// A name never moves a binding. Nothing here may change.
func TestReconcileCodexPanesNameNeverMovesTheBindingBackwards(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000010-1-1"
	// The pane shows its NAME again, because the post-clear rename landed.
	startCodexStatusPane(t, tmuxTmpDir, socket, `  W5_TESTER · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const clearedID = "11111111-1111-4111-8111-111111111111"
	const liveID = "22222222-2222-4222-8222-222222222222"
	codexJailRollout(t, database, root, clearedID, 4)
	codexJailRollout(t, database, root, liveID, 1)
	// Only the DEAD thread carries the name: the index lag this test is about.
	if err := database.ReplaceCxNames(context.Background(), []store.CxName{
		{ID: clearedID, ThreadName: "W5_TESTER"},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", liveID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != liveID {
		t.Fatalf(
			"binding = (%q, %v, %v), want it to STAY on the live post-clear thread %q: stderr=%q",
			bound, found, err, liveID, stderr.String(),
		)
	}
	if _, killed, err := database.Killed(context.Background(), liveID); err != nil || killed {
		t.Fatalf(
			"a lagging display name clear-killed the LIVE thread %q: killed=%v error=%v",
			liveID, killed, err,
		)
	}
}

// Two live panes paint the same display name and cx_names knows exactly one
// thread by it. A name may seed at most one pane; the second keeps nothing,
// and the collision is reported rather than left to be discovered months later
// in the meta table.
func TestReconcileCodexPanesNeverBindsTwoPanesToOneThread(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const first = "cx-1800000011-1-1"
	const second = "cx-1800000011-2-2"
	for _, socket := range []string{first, second} {
		startCodexStatusPane(t, tmuxTmpDir, socket, `  ENGINE_BUILDER · /work/example · Full Access\n`)
	}

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const sharedID = "33333333-3333-4333-8333-333333333333"
	codexJailRollout(t, database, root, sharedID, 2)
	if err := database.ReplaceCxNames(context.Background(), []store.CxName{
		{ID: sharedID, ThreadName: "ENGINE_BUILDER"},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(first, "%0"), codexPane(second, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bindings := 0
	for _, socket := range []string{first, second} {
		bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
		if err != nil {
			t.Fatal(err)
		}
		if found && bound == sharedID {
			bindings++
		}
	}
	if bindings != 1 {
		t.Fatalf("%d panes bound to one thread, want exactly 1: stderr=%q", bindings, stderr.String())
	}
	if !strings.Contains(stderr.String(), fleet.CodexPaneNameTaken) {
		t.Fatalf("the refused pane was silent: stderr=%q", stderr.String())
	}
}

// A resume or fork lands a CHILD rollout in the same pane, so the pane's own
// status line shows a bare id it was not bound to — the same shape a clear
// produces. Retiring the previous thread there kills the chat's own lineage
// root and the chat vanishes from the fleet. The lineage is what tells them
// apart, so the binding advances and nothing is retired.
func TestReconcileCodexPanesTreatsASameLineageResumeAsNoClear(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000012-1-1"
	const parentID = "44444444-4444-4444-8444-444444444444"
	const childID = "55555555-5555-4555-8555-555555555555"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+childID+` · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	codexJailRollout(t, database, root, parentID, 6)
	codexJailChildRollout(t, database, root, childID, parentID, 7)

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", parentID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != childID {
		t.Fatalf("binding = (%q, %v, %v), want the child %q", bound, found, err, childID)
	}
	if _, killed, err := database.Killed(context.Background(), parentID); err != nil || killed {
		t.Fatalf("a same-lineage resume retired the chat's own root: killed=%v error=%v", killed, err)
	}
}

// A compact/reset can rotate the thread behind a NAMED pane.
// The status line then carries only the display name, so the status-only
// reconciler cannot move the binding. The live process's open rollout is the
// source-of-truth signal: it must advance the same pane to the new user thread.
// This is NOT a subagent child and must not be folded into the old lineage.
func TestReconcileCodexPanesFollowsTheLiveProcessesCurrentRollout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000013-1-1"
	const parentID = "66666666-6666-4666-8666-666666666666"
	const rotatedID = "77777777-7777-4777-8777-777777777777"
	startCodexStatusPane(t, tmuxTmpDir, socket, `  ROTATED_CHAT · /work/current · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	codexJailRollout(t, database, root, parentID, 6)
	codexJailRollout(t, database, root, rotatedID, 7)
	rotated, found, err := database.Rollout(context.Background(), rotatedID)
	if err != nil || !found {
		t.Fatalf("read rotated rollout: found=%v error=%v", found, err)
	}
	rotated.CWD = "/work/current"
	if err := database.UpsertRollout(context.Background(), rotated); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceCxNames(context.Background(), []store.CxName{
		{ID: parentID, ThreadName: "ROTATED_CHAT"},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", parentID); err != nil {
		t.Fatal(err)
	}
	currentRollout := filepath.Join(
		root, "codex", "sessions", "2030", "01", "02",
		"rollout-2030-01-02T03-04-06-"+rotatedID+".jsonl",
	)

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{
			Panes: []gather.Pane{codexPane(socket, "%0")},
			Codex: []gather.LiveCodex{{
				Socket: socket, PaneID: "%0", RolloutPath: currentRollout,
				RolloutHeld: true,
			}},
		},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != rotatedID {
		t.Fatalf(
			"binding = (%q, %v, %v), want live process thread %q: stderr=%q",
			bound, found, err, rotatedID, stderr.String(),
		)
	}
	if _, killed, err := database.Killed(context.Background(), parentID); err != nil || !killed {
		t.Fatalf("the replaced thread was not retired: killed=%v error=%v", killed, err)
	}
	rollouts, err := database.Rollouts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pane := codexPane(socket, "%0")
	pane.CurrentPath = "/work/current"
	output := compose.Compose(compose.Input{
		Snapshot: gather.Snapshot{
			Panes: []gather.Pane{pane},
			Codex: []gather.LiveCodex{{
				Socket: socket, PaneID: "%0", RolloutPath: currentRollout,
			}},
		},
		Rollouts: rollouts,
		Options:  compose.Options{View: compose.AllView},
	})
	liveRows := 0
	for _, row := range output.Rows {
		if row.Kind != compose.LiveCodex {
			continue
		}
		liveRows++
		if row.ID != rotatedID || row.Socket != socket || row.CWD != "/work/current" {
			t.Fatalf("rotated live row = %#v, want new id/socket/current cwd", row)
		}
	}
	if liveRows != 1 {
		t.Fatalf("live Codex rows = %d, want exactly one: %#v", liveRows, output.Rows)
	}
}

// A pane's TUI process holds no rollout file descriptor at all — the shared
// app-server daemon holds it instead — so the only identity DetectCodex can
// give this process is the state-store resolver's BINDING-derived guess: A,
// the thread the pane was already bound to. That guess is not what the
// process is doing now; the pane's own screen already moved on to a bare,
// unnamed thread id B (the post-/clear shape). RolloutHeld: false is what
// tells fleet.ObserveCodexPanes the guess must never enter processThreads and
// overrule the screen — before the fix this guess overwrote identity.ThreadID
// back onto A, so the binding could never advance and nothing was ever
// killed. Model: TestReconcileCodexPanesKillsThePreviousBoundThreadAndAdvancesTheBinding
// (:130) for the kill/advance shape, TestReconcileCodexPanesFollowsTheLiveProcessesCurrentRollout
// (:716) for the live-process fixture shape.
func TestReconcileCodexPanesFollowsAClearWhenTheProcessHoldsNoRollout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000015-1-1"
	const oldID = "44444444-4444-4444-8444-444444444444"
	const newID = "55555555-5555-4555-8555-555555555555"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+newID+` · /work/example · Full Access
`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	oldRollout := codexJailRollout(t, database, root, oldID, 1)

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", oldID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{
			Panes: []gather.Pane{codexPane(socket, "%0")},
			Codex: []gather.LiveCodex{{
				Socket: socket, PaneID: "%0",
				RolloutPath: oldRollout,
				ThreadID:    oldID,
				RolloutHeld: false,
			}},
		},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != newID {
		t.Fatalf(
			"binding = (%q, %v, %v), want the screen's own thread %q: stderr=%q",
			bound, found, err, newID, stderr.String(),
		)
	}
	killed, found, err := database.Killed(context.Background(), oldID)
	if err != nil || !found || killed.Engine != pfmengine.Codex ||
		killed.BaselinePrompts == nil || *killed.BaselinePrompts != 1 {
		t.Fatalf("previous thread killed = %#v found=%v error=%v: stderr=%q", killed, found, err, stderr.String())
	}
	if strings.Contains(stderr.String(), "disagrees with live process rollout") {
		t.Fatalf("a resolver-derived (RolloutHeld: false) identity overruled the screen: stderr=%q", stderr.String())
	}
}

// codexJailStateTitle writes a scratch Codex state store directly under
// codexRoot as state_1.sqlite — the shape store.CodexStateFiles walks — with
// one thread row carrying id and a title. Only the columns
// store.readCodexState requires (id, cwd, created_at, thread_source) or
// reads (title) are present: the full production schema fixture is
// internal/store/codexstate_test.go's buildCodexState, unexported and in a
// different package, so this is the cmd/pfm-side counterpart.
func codexJailStateTitle(t *testing.T, codexRoot, id, title, cwd string) {
	t.Helper()
	if err := os.MkdirAll(codexRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := sql.Open("sqlite", "file:"+filepath.Join(codexRoot, "state_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		cwd TEXT,
		created_at INTEGER,
		thread_source TEXT,
		title TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Exec(
		"INSERT INTO threads (id, cwd, created_at, thread_source, title) VALUES (?, ?, 0, 'user', ?)",
		id, cwd, title,
	); err != nil {
		t.Fatal(err)
	}
}

// The probe scenario end to end (spec-codex-clear-title.md § Why): a pane
// bound to A clears and keeps typing before pfm's own reconcile pass ever
// sees the bare successor id — Codex titles the new thread from the first
// reply, so the pane's status line shows that TITLE, not a bare id, by the
// time this pass runs. B's rollout is already on disk (pfm's own store, via
// codexJailRollout) and Codex's OWN state store already carries B's title —
// the index T2 (fleet.CodexTitleThreads) reads. B is a real, strictly newer UUIDv7
// than A, so T3's forward-move law lets the title advance the binding.
// Watched RED with T3 neutralized (name branch returning CannotMove
// unconditionally): see TestDecideCodexPaneRulings.
func TestReconcileCodexPanesMovesABindingForwardOnATitleOnlyName(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000020-1-1"
	const title = "Reply with SECOND"
	// Real Codex UUIDv7 ids from the live probe: idB's first 13 characters
	// ("01a05ef0-adc0") sort after idA's ("01a05eef-6a09"), so idB is newer.
	const idA = "01a05eef-6a09-7063-a0f8-43fd0315dcc3"
	const idB = "01a05ef0-adc0-7092-b696-df46f33d5461"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+title+` · /tmp/x · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	aRollout := codexJailRollout(t, database, root, idA, 1)
	codexJailRollout(t, database, root, idB, 1)
	// Codex's own state store, not pfm's: the title index T2 builds.
	codexJailStateTitle(t, resolved.Roots[pfmengine.Codex][0], idB, title, "/tmp/x")

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", idA); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		context.Background(),
		database,
		gather.Snapshot{
			Panes: []gather.Pane{codexPane(socket, "%0")},
			// A live process that holds no rollout file descriptor — the
			// shared app-server shape — whose only guess (A, the stale
			// binding) must never overrule the screen's own title.
			Codex: []gather.LiveCodex{{
				Socket: socket, PaneID: "%0",
				RolloutPath: aRollout,
				ThreadID:    idA,
				RolloutHeld: false,
			}},
		},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != idB {
		t.Fatalf(
			"binding = (%q, %v, %v), want the title's newer thread %q: stderr=%q",
			bound, found, err, idB, stderr.String(),
		)
	}
	killed, found, err := database.Killed(context.Background(), idA)
	if err != nil || !found || killed.Engine != pfmengine.Codex ||
		killed.BaselinePrompts == nil || *killed.BaselinePrompts != 1 {
		t.Fatalf("previous thread killed = %#v found=%v error=%v: stderr=%q", killed, found, err, stderr.String())
	}
}

// The stuck state, replayed against a real tmux server: a pane bound to a
// thread a /clear already retired, showing a NAME that resolves only to that
// same dead thread.
//
// This is where a real fleet was found, and it is a TRAP rather than a
// transient: the pane shows a name from here on, a name may not move a
// binding, so nothing in the ordinary pass can ever move it off the corpse.
// The binding is impossible, so it is dropped — and NOT replaced by a guess.
// The pane's own screen re-seats it on its next unnamed frame.
func TestReconcileCodexPanesDropsABindingOnAClearRetiredThread(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000013-1-1"
	startCodexStatusPane(t, tmuxTmpDir, socket, `  ENGINE_BUILDER · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()

	const deadID = "77777777-7777-4777-8777-777777777777"
	codexJailRollout(t, database, root, deadID, 5)
	if err := database.ReplaceCxNames(ctx, []store.CxName{
		{ID: deadID, ThreadName: "ENGINE_BUILDER"},
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", deadID); err != nil {
		t.Fatal(err)
	}
	// The /clear that retired it: a prompt-baseline kill, exactly what
	// KillClearedCodex records.
	if _, _, err := manager.KillClearedCodex(ctx, deadID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanes(
		ctx,
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(ctx, socket, "%0")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatalf("the impossible binding survived, still on %q: stderr=%q", bound, stderr.String())
	}
	if !strings.Contains(stderr.String(), fleet.CodexPaneBindingRetired) &&
		!strings.Contains(stderr.String(), fleet.CodexPaneNameRetired) {
		t.Fatalf("the repair was silent: stderr=%q", stderr.String())
	}

	// And the next pane to show a bare id is seated on it, without anything
	// having been invented in between.
	const liveID = "88888888-8888-4888-8888-888888888888"
	const reseated = "cx-1800000013-2-2"
	startCodexStatusPane(t, tmuxTmpDir, reseated, "  "+liveID+` · /work/example · Full Access\n`)
	fleet.ReconcileCodexPanes(
		ctx,
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(reseated, "%0")}},
		commandRuntime{Paths: resolved},
		fleet.PrintWarn(&stderr),
	)
	bound, found, err = manager.CodexPaneBinding(ctx, reseated, "%0")
	if err != nil || !found || bound != liveID {
		t.Fatalf("re-seat = (%q, %v, %v), want %q: stderr=%q", bound, found, err, liveID, stderr.String())
	}
}

// fakeCodexRenamer is a Codex composer that agrees to everything. Every marker
// the rename state machine waits for is present in one capture, so each wait
// satisfies immediately and the rename reports CONFIRMED. It exists to reach
// the step AFTER the rename, which had no coverage at all.
type fakeCodexRenamer struct{ name string }

func (renamer fakeCodexRenamer) NewSession(context.Context, spawn.SessionSpec) error { return nil }

func (renamer fakeCodexRenamer) Capture(context.Context, string, string) (string, error) {
	return "› Ask Codex to do anything\n" +
		"gpt-5-codex · /work/example · 12% used\n" +
		"rename the current thread\n" +
		"Type a name and press Enter\n" +
		"• Session renamed to " + renamer.name + ".\n", nil
}

func (renamer fakeCodexRenamer) SendLiteral(context.Context, string, string, string) error {
	return nil
}

func (renamer fakeCodexRenamer) SendKey(context.Context, string, string, string) error { return nil }

// After a clear, pfm re-applies the chat's name to the NEW thread and must
// record that rename in cx_names itself.
//
// Leaving the record to the next index pass set a trap the pass itself sprang.
// Between the rename and that pass the chat's name resolved to exactly ONE
// thread — the retired pre-clear one — so a pane that lost its binding in that
// window could not be re-seated: a name may seed an unbound pane, but every
// thread this name matched was retired. The pane then warned
// "status line name matches only threads a /clear already retired" on every
// single refresh and never recovered on its own.
func TestReconcileCodexPanesRecordsTheNameItReAppliedAfterAClear(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	const socket = "cx-1800000009-9-9"
	const newID = "99999999-9999-4999-8999-999999999999"
	const oldID = "88888888-8888-4888-8888-888888888888"
	const chatName = "ENGINE_BUILDER"
	startCodexStatusPane(t, tmuxTmpDir, socket, "  "+newID+` · /work/example · Full Access\n`)

	resolved := jailPaths(t)
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	codexJailRollout(t, database, root, oldID, 1)

	if err := database.UpsertCxName(context.Background(), store.CxName{
		ID: oldID, ThreadName: chatName,
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.AdvanceCodexPane(context.Background(), socket, "%0", oldID); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	fleet.ReconcileCodexPanesWith(
		context.Background(),
		database,
		gather.Snapshot{Panes: []gather.Pane{codexPane(socket, "%0")}},
		commandRuntime{Paths: resolved},
		fakeCodexRenamer{name: chatName},
		fleet.PrintWarn(&stderr),
	)

	names, err := database.CxNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if names[newID] != chatName {
		t.Fatalf(
			"cx_names[new thread] = %q, want %q — the pane is now unbindable by its own name: stderr=%q",
			names[newID], chatName, stderr.String(),
		)
	}
}

// The recorded name must actually re-seat an unbound pane, which is the whole
// point of recording it. This is the decision the live fleet could not reach:
// the pane shows its NAME (Codex renamed the thread, so the bare id is gone
// from the status line) and carries no binding, and the name must resolve to
// the LIVE thread rather than only to the retired one.
func TestAReAppliedNameReSeatsAnUnboundPane(t *testing.T) {
	const retiredID = "88888888-8888-4888-8888-888888888888"
	const liveID = "99999999-9999-4999-8999-999999999999"
	const chatName = "ENGINE_BUILDER"

	retired := func(id string) (bool, bool) { return id == retiredID, true }
	observations := []fleet.CodexPaneObservation{
		{Socket: "cx-1", PaneID: "%0", Name: chatName},
	}

	stuck := fleet.DecideCodexPanes(
		observations,
		map[string]string{retiredID: chatName},
		nil,
		func(string) string { return "" },
		retired,
	)
	if stuck[0].Bind != "" || stuck[0].Skip != fleet.CodexPaneNameRetired {
		t.Fatalf("without the recorded name = %#v, want the stuck state", stuck[0])
	}

	healed := fleet.DecideCodexPanes(
		[]fleet.CodexPaneObservation{{Socket: "cx-1", PaneID: "%0", Name: chatName}},
		map[string]string{retiredID: chatName, liveID: chatName},
		nil,
		func(string) string { return "" },
		retired,
	)
	if healed[0].Bind != liveID {
		t.Fatalf("with the recorded name = %#v, want a bind onto %s", healed[0], liveID)
	}
}
