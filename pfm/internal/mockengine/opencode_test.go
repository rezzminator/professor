package mockengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/index"
)

const fixtureOpenCodeSession = "ses_fixture0001"

func TestOpenCodeTUIRefusesToRenderByName(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureOpenCodeSession})
	session := fix.startTUI("opencode", nil, nil)
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit = %d, want %d", code, ExitUnpinned)
	}
	for _, want := range []string{"opencode pane shapes are unpinned", "pane.busy", "pane.compacted", "pane.composer"} {
		if !strings.Contains(session.stderr.String(), want) {
			t.Fatalf("stderr = %q, want it to name %q", session.stderr.String(), want)
		}
	}
}

func TestOpenCodeMCPStepIsRefusedByName(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureOpenCodeSession, Steps: []Step{{Type: StepMCP, Server: "chat"}}})
	code, _, stderr := runOnce(fix, "opencode", []string{"run", "hello"}, "")
	if code != ExitUnpinned || !strings.Contains(stderr, "opencode MCP") || !strings.Contains(stderr, "unpinned") {
		t.Fatalf("exit=%d stderr=%q, want %d naming the OpenCode MCP gap", code, stderr, ExitUnpinned)
	}
}

func TestOpenCodeRunWritesTheSessionStorePfmReads(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureOpenCodeSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepTurn, Reply: "bounded", Tokens: &Tokens{Input: 120, Output: 30}},
	}})
	code, stdout, stderr := runOnce(
		fix,
		"opencode",
		[]string{"run", "--model", "anthropic/claude-sonnet-fixture", "prove the bound"},
		"",
	)
	if code != 0 || !strings.Contains(stdout, "bounded") {
		t.Fatalf("opencode run exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	root := filepath.Join(fix.home, ".local", "share", "opencode")
	if _, err := os.Stat(filepath.Join(root, "opencode.db")); err != nil {
		t.Fatalf("no store at %s: %v", root, err)
	}
	sessions, err := index.ReadOpenCodeSessions(context.Background(), root)
	if err != nil {
		t.Fatalf("pfm's OpenCode reader refused the store: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v, want exactly one", sessions)
	}
	got := sessions[0]
	if got.ID != fixtureOpenCodeSession || got.Directory != fix.work || got.ProjectDir != fix.work ||
		got.ParentID != "" || got.FirstPrompt != "prove the bound" || got.PromptCount != 1 || got.AssistantCount != 1 ||
		got.Model != "anthropic/claude-sonnet-fixture" || got.TokensInput != 120 || got.TokensOutput != 30 ||
		got.TimeCreatedMS <= 0 || got.TimeUpdatedMS < got.TimeCreatedMS || got.TimeArchivedMS != 0 {
		t.Fatalf("session = %+v", got)
	}

	code, stdout, stderr = runOnce(
		fix,
		"opencode",
		[]string{"run", "--session", fixtureOpenCodeSession, "now generalize"},
		"",
	)
	if code != 0 || !strings.Contains(stdout, DefaultReply) {
		t.Fatalf("second opencode run exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	sessions, err = index.ReadOpenCodeSessions(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].PromptCount != 2 || sessions[0].AssistantCount != 2 ||
		sessions[0].FirstPrompt != "prove the bound" {
		t.Fatalf("after a continued run sessions = %+v, want one session with two exchanges", sessions)
	}
}
