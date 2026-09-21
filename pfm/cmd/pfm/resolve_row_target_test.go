package main

import (
	"bytes"
	"context"
	"errors"
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
	address, found, err := fleet.ResolveRow(
		context.Background(), database, id, &stderr, &commandRuntime{Paths: jailPaths(t)},
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
	if address.RolloutPath != transcriptPath {
		t.Fatalf("rolloutPath = %q, want %q", address.RolloutPath, transcriptPath)
	}
	if address.Socket != "" || address.PaneID != "" {
		t.Fatalf("socket=%q pane=%q, want both empty for a resumable id", address.Socket, address.PaneID)
	}
}

func TestResolveRowTargetDistinguishesAnAbsentID(t *testing.T) {
	jailTest(t)
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
		context.Background(), database, "no-such-chat", &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if err != nil {
		t.Fatalf("ResolveRow() error = %v", err)
	}
	if found || address != (fleet.RowAddress{}) {
		t.Fatalf("ResolveRow() = %+v, found %t; want zero address, false", address, found)
	}
}

func TestResolveRowTargetReportsResolveEnvFailure(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	t.Setenv(fleet.TestNowNSEnv, "not-a-timestamp")

	const id = "11111111-1111-4111-8111-111111111111"
	var stderr bytes.Buffer
	address, found, err := fleet.ResolveRow(
		context.Background(), database, id, &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if err == nil || !strings.Contains(err.Error(), `resolve row "`+id+`": resolve env:`) {
		t.Fatalf("ResolveRow() error = %v, want resolve env context", err)
	}
	var numberErr *strconv.NumError
	if !errors.As(err, &numberErr) {
		t.Fatalf("ResolveRow() error = %v, want wrapped *strconv.NumError", err)
	}
	if found || address != (fleet.RowAddress{}) {
		t.Fatalf("ResolveRow() = %+v, found %t; want zero address, false", address, found)
	}
}

func TestResolveRowTargetReportsLoadDataFailure(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, loadErr := fleet.LoadData(context.Background(), database)
	if loadErr == nil {
		t.Fatal("setup: LoadData() on a closed database returned nil")
	}
	loadCause := errors.Unwrap(loadErr)
	if loadCause == nil {
		t.Fatalf("setup: LoadData() error %v does not wrap its database cause", loadErr)
	}

	const id = "22222222-2222-4222-8222-222222222222"
	var stderr bytes.Buffer
	address, found, err := fleet.ResolveRow(
		context.Background(), database, id, &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if err == nil || !strings.Contains(err.Error(), `resolve row "`+id+`": load data:`) {
		t.Fatalf("ResolveRow() error = %v, want load data context", err)
	}
	if !errors.Is(err, loadCause) {
		t.Fatalf("ResolveRow() error = %v, want wrapped cause %v", err, loadCause)
	}
	if found || address != (fleet.RowAddress{}) {
		t.Fatalf("ResolveRow() = %+v, found %t; want zero address, false", address, found)
	}
}

func TestResolveRowTargetReportsGatherFailure(t *testing.T) {
	root := jailTest(t)
	invalidTmuxDir := filepath.Join(root, "tmux-is-a-file")
	if err := os.WriteFile(invalidTmuxDir, []byte("not a socket directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_TMUX_DIR", invalidTmuxDir)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	const id = "33333333-3333-4333-8333-333333333333"
	var stderr bytes.Buffer
	address, found, err := fleet.ResolveRow(
		context.Background(), database, id, &stderr, &commandRuntime{Paths: jailPaths(t)},
	)
	if err == nil || !strings.Contains(err.Error(), `resolve row "`+id+`": gather:`) {
		t.Fatalf("ResolveRow() error = %v, want gather context", err)
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("ResolveRow() error = %v, want wrapped *os.PathError", err)
	}
	if found || address != (fleet.RowAddress{}) {
		t.Fatalf("ResolveRow() = %+v, found %t; want zero address, false", address, found)
	}
}

func TestChatKillReportsLookupFailureWithoutKilling(t *testing.T) {
	root := jailTest(t)
	const id = "44444444-4444-4444-8444-444444444444"
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: id, Path: filepath.Join(root, id+".jsonl"), CWD: "/work/project", Size: 1, PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fleet.TestNowNSEnv, "not-a-timestamp")

	var stdout, stderr bytes.Buffer
	code := runKill([]string{id}, &stdout, &stderr, commandRuntime{Paths: jailPaths(t)})
	if code != 1 {
		t.Fatalf("runKill() code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "pfm chat kill: lookup failed:") ||
		!strings.Contains(stderr.String(), fleet.TestNowNSEnv) {
		t.Fatalf("runKill() stdout=%q stderr=%q, want named lookup failure", stdout.String(), stderr.String())
	}

	database, err = store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	_, killed, err := database.Killed(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if killed {
		t.Fatal("runKill() recorded a kill after the lookup failed")
	}
}

func TestChatKillOfAnUnknownIDStillUsesTheManagersRefusal(t *testing.T) {
	jailTest(t)
	const id = "no-such-chat"
	var stdout, stderr bytes.Buffer
	code := runKill([]string{id}, &stdout, &stderr, commandRuntime{Paths: jailPaths(t)})
	if code != 1 {
		t.Fatalf("runKill() code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "lookup failed") || !strings.Contains(stderr.String(), id) {
		t.Fatalf("runKill() stderr=%q, want manager refusal naming the id", stderr.String())
	}
}
