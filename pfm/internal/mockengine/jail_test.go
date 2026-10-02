package mockengine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/inject"
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
		func(frame string) bool { return strings.Contains(frame, "⏺ ok") && !inject.IsBusy(frame) },
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

func TestJailBindingsAreWhatGatherAndReloadRead(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession})
	socket := filepath.Join(fix.root, "tmux-"+strconv.Itoa(os.Getuid()), "cc-1700000000-4242-7")
	session := fix.startTUI("claude", claudeArgs(), map[string]string{"TMUX": socket + ",4242,0", "TMUX_PANE": "%0"})
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })

	proc := gather.RealProcFS{Root: fix.procRoot}
	pid := os.Getpid()
	cmdline, err := proc.Cmdline(pid)
	if err != nil {
		t.Fatalf("gather could not read the mock's proc entry: %v", err)
	}
	if !gather.IsClaudeCommand(cmdline) || !strings.Contains(strings.Join(cmdline, " "), "--settings") {
		t.Fatalf("cmdline = %q, want a Claude argv gather.IsClaudeCommand accepts", cmdline)
	}
	stat, err := proc.Stat(pid)
	if err != nil || stat.ParentPID != os.Getppid() || stat.StartTime == 0 {
		t.Fatalf("stat = %+v err=%v, want the real parent pid and a start tick", stat, err)
	}
	environment, err := proc.Environ(pid)
	if err != nil || environment["CLAUDE_CONFIG_DIR"] != fix.configDir {
		t.Fatalf("environ = %v err=%v, want CLAUDE_CONFIG_DIR", environment, err)
	}
	id, transcriptPath, err := reload.SessionFromCrumb(fix.sidDir, "cc-1700000000-4242-7", "%0")
	if err != nil || id != fixtureSession || transcriptPath != fix.claudeTranscript(fixtureSession) {
		t.Fatalf("crumb = (%q, %q, %v), want the session and its transcript path", id, transcriptPath, err)
	}
	for _, name := range []string{"cc-1700000000-4242-7", "cc-1700000000-4242-7.%0"} {
		if _, err := os.Stat(filepath.Join(fix.sidDir, name)); err != nil {
			t.Fatalf("crumb %s missing: %v", name, err)
		}
	}

	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(fix.procRoot, strconv.Itoa(pid))); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the proc entry outlived the engine — a dead chat would keep looking alive")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCodexJailEntryLinksTheRolloutAsAnOpenDescriptor(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes})
	session := fix.startTUI("codex", codexArgs(), map[string]string{"TMUX": "/x/cx-1700000000-4242-8,1,0"})
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	proc := gather.RealProcFS{Root: fix.procRoot}
	cmdline, err := proc.Cmdline(os.Getpid())
	if err != nil || !gather.IsCodexCommand(cmdline) {
		t.Fatalf("cmdline = %q err=%v, want a Codex argv", cmdline, err)
	}
	links, err := proc.FDLinks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	rollout := fix.rolloutPath()
	found := false
	for _, link := range links {
		found = found || link.Target == rollout
	}
	if !found {
		t.Fatalf(
			"fd links = %+v, want one open on %s (gather/codexproc.go reads the rollout off the descriptor table)",
			links,
			rollout,
		)
	}
}
