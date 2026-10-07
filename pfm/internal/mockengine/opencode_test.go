package mockengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/inject"
)

const fixtureOpenCodeSession = "ses_fixture0001"

var openCodeShapes = PaneShapes{
	Busy:      "FIXTURE working · esc interrupt",
	Compacted: "FIXTURE compacted",
	Composer:  "FIXTURE-OPENCODE-PROMPT",
}

func TestOpenCodeTUIWritesTheStorePfmReads(t *testing.T) {
	for _, launch := range []bool{false, true} {
		t.Run(fmt.Sprint(launch), func(t *testing.T) {
			fix := newFixture(t)
			fix.write(
				Scenario{
					Pane: openCodeShapes,
					Steps: []Step{
						{Type: StepTurn, Reply: "first reply", BusyMS: 600, Tokens: &Tokens{Input: 120, Output: 30}},
					},
				},
			)
			args := []string{fix.work, "--session", fixtureOpenCodeSession, "--model", "fixture/model"}
			if launch {
				args = append(args, "--prompt", "prove the bound")
			}
			session := fix.startTUI("opencode", args, nil)
			session.waitFrame(
				"composer",
				func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) },
			)
			if !launch {
				session.typeLine("prove the bound")
			}
			session.waitFrame(
				"pfm busy footer",
				func(frame string) bool { return inject.IsBusyFor(pfmengine.OpenCode, frame) },
			)
			session.waitFrame("idle reply", func(frame string) bool {
				return strings.Contains(frame, "⏺ first reply") && !inject.IsBusyFor(pfmengine.OpenCode, frame)
			})
			root := filepath.Join(fix.home, ".local", "share", "opencode")
			rows, err := index.ReadOpenCodeSessions(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].ID != fixtureOpenCodeSession || rows[0].Directory != fix.work ||
				rows[0].ProjectDir != fix.work ||
				rows[0].FirstPrompt != "prove the bound" ||
				rows[0].PromptCount != 1 ||
				rows[0].AssistantCount != 1 ||
				rows[0].TokensInput != 120 ||
				rows[0].TokensOutput != 30 ||
				rows[0].Model != "fixture/model" {
				t.Fatalf("pfm OpenCode sessions=%+v", rows)
			}
			if !gather.IsOpenCodeCommand(append([]string{"opencode"}, args...)) {
				t.Fatal("pfm detector refuses TUI argv")
			}
			if title := gather.OpenCodePaneName(
				"OC | " + rows[0].Title,
			); title != "prove the bound" ||
				!strings.Contains(session.out.all(), "\x1b]0;OC | "+title+"\a") {
				t.Fatalf("pane title does not bind session %q: %q", title, session.out.all())
			}
			live, err := gather.DetectOpenCode(gather.RealProcFS{Root: fix.procRoot}, []gather.ProbePane{{
				Socket: "ox-1700000000-4242-7", PaneID: "%9", PID: os.Getpid(),
				SessionName: "fixture", CurrentPath: fix.work, PaneTitle: "OC | " + rows[0].Title,
			}}, []gather.OpenCodeSession{{
				ID: rows[0].ID, Title: rows[0].Title,
				Directory: rows[0].Directory, TimeCreatedMS: rows[0].TimeCreatedMS,
			}})
			if err != nil || len(live) != 1 || live[0].SessionID != fixtureOpenCodeSession {
				t.Fatalf("pfm detector seats=%+v err=%v", live, err)
			}
			session.typeLine(`again mock-engine: {"type":"turn","reply":"second reply"}`)
			session.waitFrame(
				"second reply",
				func(frame string) bool { return strings.Contains(frame, "⏺ second reply") },
			)
			rows, err = index.ReadOpenCodeSessions(context.Background(), root)
			if err != nil || len(rows) != 1 || rows[0].PromptCount != 2 || rows[0].AssistantCount != 2 ||
				rows[0].FirstPrompt != "prove the bound" {
				t.Fatalf("continued TUI sessions=%+v err=%v", rows, err)
			}
		})
	}
}

func TestOpenCodeInlineMCPKeepsNamedRefusal(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{Pane: openCodeShapes})
	session := fix.startTUI("opencode", []string{fix.work}, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) })
	session.typeLine(`mock-engine: {"type":"mcp","tool":"chat_find"}`)
	if code := session.waitExit(); code != ExitUnpinned || !strings.Contains(session.stderr.String(), "opencode MCP") {
		t.Fatalf("inline MCP exit=%d stderr=%q", code, session.stderr.String())
	}
}

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
