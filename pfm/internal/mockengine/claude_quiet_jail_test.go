package mockengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

func TestClaudeQuietKeepsThePaneAndTranscriptWithoutAnnouncingTheSeat(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
		"Stop":       commandHook(`cat >> "` + fix.recordDir + `/Stop.jsonl"`),
		"PreToolUse": commandHook(`cat >> "` + fix.recordDir + `/project-pre.jsonl"`),
	})
	fix.write(
		Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{{Type: StepToolCall, Tool: "Agent"}}},
	)
	setScenarioField(t, fix, "quiet", true)
	if code, _, stderr := runOnce(fix, "claude", []string{"--version"}, ""); code != 0 {
		t.Fatalf("quiet launch=%d stderr=%q", code, stderr)
	}
	session := fix.startTUI(
		"claude",
		claudeArgs(),
		map[string]string{"TMUX": "/fixture/quiet-seat,42,0", "TMUX_PANE": "%0"},
	)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("quiet prompt")
	session.waitFrame(
		"the quiet reply",
		func(frame string) bool { return strings.Contains(frame, "⏺ ok") && !pfmcall.IsBusy(frame) },
	)
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("quiet exit=%d", code)
	}
	if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 1 {
		t.Fatalf("quiet transcript=%+v", meta)
	}
	if strings.Contains(readFile(t, fix.claudeTranscript(fixtureSession)), `"is_error":true`) {
		t.Fatal("quiet fired the deny hook")
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "SessionEnd", "Stop", "project-pre", "statusline"} {
		if len(fix.recorded(event)) != 0 {
			t.Fatalf("quiet ran %s", event)
		}
	}
	crumbs, err := os.ReadDir(fix.sidDir)
	if err != nil || len(crumbs) != 0 {
		t.Fatalf("quiet crumbs=%v err=%v", crumbs, err)
	}
}

func TestClaudeNoTranscriptAnnouncesItsPathWithoutCreatingIt(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	setScenarioField(t, fix, "no_transcript", true)
	if code, _, stderr := runOnce(fix, "claude", []string{"--version"}, ""); code != 0 {
		t.Fatalf("no_transcript launch=%d stderr=%q", code, stderr)
	}
	session := fix.startTUI(
		"claude",
		claudeArgs(),
		map[string]string{"TMUX": "/fixture/missing-seat,42,0", "TMUX_PANE": "%0"},
	)
	session.waitFrame(
		"the announced composer",
		func(frame string) bool { return strings.Contains(frame, "SL-FIXTURE") },
	)
	path := fix.claudeTranscript(fixtureSession)
	id, announced, err := reload.SessionFromCrumb(fix.sidDir, "missing-seat", "%0")
	if err != nil || id != fixtureSession || announced != path {
		t.Fatalf("crumb=(%q, %q, %v)", id, announced, err)
	}
	for _, event := range []string{"SessionStart", "statusline"} {
		if !strings.Contains(strings.Join(fix.recorded(event), "\n"), path) {
			t.Fatalf("%s did not announce %s", event, path)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("transcript directory exists before a prompt: %v", err)
	}
	session.typeLine(
		`hi mock-engine: [{"type":"background_agent","name":"a"},{"type":"compact"},{"type":"turn","reply":"still live"}]`,
	)
	session.waitFrame("the completed turn", func(frame string) bool { return strings.Contains(frame, "⏺ still live") })
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("transcript directory exists after a prompt: %v", err)
	}
	if len(fix.recorded("UserPromptSubmit")) != 1 {
		t.Fatal("no_transcript suppressed the prompt hook")
	}
	code, stdout, stderr := runOnce(fix, "claude", []string{"-p", "--output-format", "json"}, "headless")
	if code != 0 || !strings.Contains(stdout, `"result":"ok"`) {
		t.Fatalf("headless exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("headless created the transcript directory: %v", err)
	}
}
