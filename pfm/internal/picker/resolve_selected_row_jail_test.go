package picker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestResolveSelectedRowAttachesALiveChatInsteadOfResumingAStaleCachedRow pins
// the picker's own re-resolve guard (Run's OutcomeSelected case →
// resolveSelectedRow): the picker's first frame comes from scanFleetCached
// (an EMPTY gather, picker.go's scanFleetCached call), so a chat that goes
// live after that frame was painted still renders as a `↻` Resume row when
// Enter lands. Opening THAT stale row straight would synthesize a fresh
// server for a chat that is already running — a second seat of the same
// conversation. Re-resolving against the current fleet before OpenRow acts
// is what turns the outcome into an attach instead.
func TestResolveSelectedRowAttachesALiveChatInsteadOfResumingAStaleCachedRow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
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

	// The picker's cached first frame, before anything is live: a Resume row —
	// exactly what scanFleetCached paints from the empty gather.
	cached, err := scanFleetCached(context.Background(), database, scanRequest{})
	if err != nil {
		t.Fatalf("scanFleetCached() error = %v", err)
	}
	var staleRow compose.Row
	found := false
	for _, row := range cached.Output.Rows {
		if row.ID == id {
			staleRow, found = row, true
		}
	}
	if !found || staleRow.Kind != compose.ResumeClaude {
		t.Fatalf("cached row for %s = %#v (found=%t), want a resumable first frame", id, staleRow, found)
	}

	// The chat goes live AFTER the cached frame was painted: a real tmux pane
	// carrying the transcript's SID crumb. -L addressing (never -S) matches
	// how gather.TmuxProbe itself addresses a socket — see
	// codexRegatherJailFixture's own comment on why both TMUX_TMPDIR and
	// PFM_TMUX_DIR must name the SAME uid-suffixed directory.
	tmuxTmpDir := filepath.Join(root, "tmuxtmp")
	// tmux -L silently falls back to its default /tmp/tmux-<uid> when
	// TMUX_TMPDIR does not already exist as a directory — it never creates
	// the directory itself — so the jail must create it before the first
	// tmux invocation, exactly like startCodexStatusPane does.
	if err := os.MkdirAll(tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", tmuxTmpDir)
	t.Setenv("PFM_TMUX_DIR", filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid())))
	socket := "cc-1800000010-50-1"
	session := exec.Command(
		"tmux", "-f", "/dev/null", "-L", socket,
		"new-session", "-d", "-s", "chat", "sleep 120",
	)
	session.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start jail tmux server %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = session.Env
		_ = kill.Run()
	})
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-F", "#{pane_id}",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("list panes for %q: %v: %s", socket, err, paneOutput)
	}
	paneID := strings.TrimSpace(string(paneOutput))
	if paneID == "" {
		t.Fatalf("list-panes returned no pane id: %q", paneOutput)
	}
	sidDir := filepath.Join(root, "sid")
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(sidDir, socket+"."+paneID)
	if err := os.WriteFile(crumb, []byte(transcriptPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	resolved, err := resolveSelectedRow(
		context.Background(), database, staleRow, &pfmconfig.Runtime{Paths: jailPaths(t)}, &stderr,
	)
	if err != nil {
		t.Fatalf("resolveSelectedRow() error = %v: stderr=%q", err, stderr.String())
	}
	if resolved.Kind != compose.LiveClaude || resolved.Socket != socket || resolved.PaneID != paneID {
		t.Fatalf(
			"resolveSelectedRow(stale ResumeClaude)=%#v, want a LIVE row on socket %q pane %q: stderr=%q",
			resolved, socket, paneID, stderr.String(),
		)
	}
}

// TestResolveSelectedRowNamesAChatGoneFromASuccessfulRescan (L1-F8): the
// rescan itself succeeds — no tmux failure, no store error — and simply
// answers without this id: the chat was killed or resumed elsewhere between
// the picker's paint and this Enter. Opening the stale cached row anyway is
// the exact second-seat hazard resolveSelectedRow's own doc comment names;
// only the id-present branch had a test before this one.
func TestResolveSelectedRowNamesAChatGoneFromASuccessfulRescan(t *testing.T) {
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

	staleRow := compose.Row{
		ID:   "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		Kind: compose.ResumeClaude,
		Name: "ghost-chat",
	}

	var stderr bytes.Buffer
	resolved, err := resolveSelectedRow(
		context.Background(), database, staleRow, &pfmconfig.Runtime{Paths: jailPaths(t)}, &stderr,
	)
	if err == nil {
		t.Fatalf(
			"resolveSelectedRow() opened a row absent from a successful rescan: %#v",
			resolved,
		)
	}
	if !strings.Contains(err.Error(), "ghost-chat") || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("error = %v, want it to name the vanished row", err)
	}
	if !reflect.DeepEqual(resolved, compose.Row{}) {
		t.Fatalf("resolved row = %#v, want the zero value alongside the error", resolved)
	}
}
