package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

// TestMCPListProjectFilterIsASubstringNotAQuery is the F4 regression:
// chat_ls's "project" input used to be handed to scanRequest.Query, which
// only seeds the interactive picker's search box (ui.Snapshot.InitialQuery)
// and never filters anything — every row came back regardless of what the
// caller asked for. The fix applies input.Project itself, case-insensitively,
// as a substring filter over each row's Project OR its CWD.
func TestMCPListProjectFilterIsASubstringNotAQuery(t *testing.T) {
	root := jailTest(t)
	indexResumableChat(t, root, "alpha-project", "11111111-1111-4111-8111-111111111112")
	indexResumableChat(t, root, "beta-project", "22222222-2222-4222-8222-222222222223")

	output := callChatTool[mcpserv.LSOutput](t, "chat_ls", mcpserv.LSInput{All: true, Project: "ALPHA"})
	if output.Matched != 1 || output.Count != 1 {
		t.Fatalf("filtered list = %+v, want exactly the alpha row", output)
	}
	if !strings.Contains(output.Rows[0].Dir, "alpha-project") {
		t.Fatalf("filtered row = %+v, want the alpha-project chat", output.Rows[0])
	}
	if output.Filter != "ALPHA" {
		t.Fatalf("output.Filter = %q, want the echoed caller input %q", output.Filter, "ALPHA")
	}

	// The unfiltered call is the control: both rows come back when no filter
	// is given, proving the narrowing above came from the filter and not
	// from some other difference between the two fixtures.
	unfiltered := callChatTool[mcpserv.LSOutput](t, "chat_ls", mcpserv.LSInput{All: true})
	if unfiltered.Matched != 2 {
		t.Fatalf("unfiltered matched = %d, want 2", unfiltered.Matched)
	}
}

// TestMCPListLimitReportsFullMatchedCountAndTruncated is the F5 regression:
// chat_ls had no Limit input at all, so a large killed history (hundreds of
// rows) always answered in full. The fix caps Rows at Limit (default 200, max
// 1000) while Matched keeps the full count the view and filter selected, and
// Truncated says outright that Count < Matched.
func TestMCPListLimitReportsFullMatchedCountAndTruncated(t *testing.T) {
	root := jailTest(t)
	indexResumableChat(t, root, "one-project", "33333333-3333-4333-8333-333333333334")
	indexResumableChat(t, root, "two-project", "44444444-4444-4444-8444-444444444445")
	indexResumableChat(t, root, "three-project", "55555555-5555-4555-8555-555555555556")

	output := callChatTool[mcpserv.LSOutput](t, "chat_ls", mcpserv.LSInput{All: true, Limit: 1})
	if output.Matched != 3 {
		t.Fatalf("output.Matched = %d, want 3 (the full match count regardless of Limit)", output.Matched)
	}
	if output.Count != 1 || len(output.Rows) != 1 {
		t.Fatalf("output.Count = %d len(Rows)=%d, want 1 (Limit applied)", output.Count, len(output.Rows))
	}
	if !output.Truncated {
		t.Fatal("output.Truncated = false, want true when Count < Matched")
	}

	full := callChatTool[mcpserv.LSOutput](t, "chat_ls", mcpserv.LSInput{All: true, Limit: 3})
	if full.Truncated {
		t.Fatal("output.Truncated = true when Limit covers every matched row")
	}
}

// TestMCPListAdmitsBootingRow is the F6 regression: the chat_ls exclusion used
// to drop compose.Booting alongside the picker's placeholder New* rows, so
// chat_ls reported a chat that had already spawned its process and its tmux
// pane — and already answers chat_inject by name — as flatly absent for its
// first minute of life. The fix lists it with state "booting" instead of
// excluding it.
func TestMCPListAdmitsBootingRow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)
	socket := "cc-1700000000-" + strconv.Itoa(os.Getpid()) + "-9"
	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "sleep", "120",
	)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start booting pane: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-F", "#{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOutput)))
	if err != nil {
		t.Fatal(err)
	}
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       93001,
		parentPID: panePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude"},
	})

	output := callChatTool[mcpserv.LSOutput](t, "chat_ls", mcpserv.LSInput{All: true})
	var found *mcpserv.ChatRow
	for index := range output.Rows {
		if output.Rows[index].Socket == socket {
			found = &output.Rows[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("chat_ls omitted the booting row entirely: rows=%+v", output.Rows)
	}
	if found.State != "booting" {
		t.Fatalf("booting row state = %q, want %q", found.State, "booting")
	}
}

// indexResumableChat lays down one Claude transcript under a distinct
// project directory and indexes it, producing an ordinary resumable row with
// Project/CWD containing projectSlug.
func indexResumableChat(t *testing.T, root, projectSlug, id string) {
	t.Helper()
	project := filepath.Join(root, "work", projectSlug)
	transcriptDir := filepath.Join(root, "claude", projectSlug)
	for _, directory := range []string{project, transcriptDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	content := `{"type":"user","cwd":` + strconv.Quote(project) +
		`,"message":{"content":"prompt for ` + projectSlug + `"}}` + "\n"
	if err := os.WriteFile(
		filepath.Join(transcriptDir, id+".jsonl"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"index", "--full"}, &stdout, &stderr); code != 0 {
		t.Fatalf("index %s code=%d stderr=%q", projectSlug, code, stderr.String())
	}
}
