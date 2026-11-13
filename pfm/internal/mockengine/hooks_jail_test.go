package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestClaudePfmsRenderedSettingsRunPlayedHooks(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	bin := filepath.Join(fix.home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	commands := filepath.Join(fix.recordDir, "pfm-commands")
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$*\" \"$(cat)\" >> \"" + commands + "\"\n"
	if err := testjail.WriteExecutable(filepath.Join(bin, "pfm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(
		filepath.Join(bin, "pfm-statusline"),
		[]byte("#!/bin/sh\nprintf 'rendered status\\n'\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	launch, err := claudelaunch.Render(
		claudelaunch.Request{Purpose: claudelaunch.PurposeInteractive, Home: fix.home, ConfigDir: fix.configDir},
		config.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	fix.write(
		Scenario{
			SessionID: fixtureSession,
			BusyMS:    intPtr(0),
			Steps:     []Step{{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"}},
		},
	)
	session := fix.startTUI("claude", launch.Argv, nil)
	session.waitFrame("rendered status", func(frame string) bool { return strings.Contains(frame, "rendered status") })
	session.typeLine("go")
	session.waitFrame("finished", func(frame string) bool { return strings.Contains(frame, "finished") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	got := readFile(t, commands)
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			t.Fatalf("hook record has no payload: %q", line)
		}
		var payload struct {
			Event string `json:"hook_event_name"`
		}
		if err := json.Unmarshal([]byte(parts[1]), &payload); err != nil {
			t.Fatalf("decode hook record %q: %v", line, err)
		}
		seen[payload.Event+"|"+parts[0]]++
	}
	for _, hook := range claudelaunch.HookTemplates(fix.home) {
		if hook.Event != hookSessionStart && hook.Event != hookUserPromptSubmit &&
			hook.Event != hookPreToolUse && hook.Event != hookPostToolUse &&
			hook.Event != hookStop &&
			hook.Event != hookSessionEnd {
			continue
		}
		if hook.Event == hookPreToolUse && hook.Matcher != "Bash" && hook.Matcher != "*" && hook.Matcher != "" {
			continue
		}
		// A SessionStart matcher selects the start source; this run is a fresh
		// startup, so only unmatched and startup-matched hooks fire.
		if hook.Event == hookSessionStart && hook.Matcher != "" && hook.Matcher != sourceStartup {
			continue
		}
		key := hook.Event + "|" + strings.TrimPrefix(hook.Command, filepath.Join(bin, "pfm")+" ")
		if seen[key] != 1 {
			t.Errorf("%s ran %d times, want 1; records: %q", key, seen[key], got)
		}
	}
}

func TestClaudeFiresTheInstalledHooksAndHonoursTheirAnswers(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepToolCall, Tool: "Agent", Input: json.RawMessage(`{"subagent_type":"Explore","prompt":"look"}`)},
		{Type: StepTurn, Reply: "mapped"},
	}})
	session := fix.startTUI("claude", claudeArgs("--name", "hooked"), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })

	start := decodeEnvelope(t, fix.waitRecorded("SessionStart", 1)[0])
	transcriptPath := fix.claudeTranscript(fixtureSession)
	if start.Event != "SessionStart" || start.Source != "startup" || start.SessionID != fixtureSession ||
		start.TranscriptPath != transcriptPath || start.CWD != fix.work {
		t.Fatalf("SessionStart payload = %+v", start)
	}

	session.typeLine("map it")
	session.waitFrame("the reply after the denied tool", func(frame string) bool {
		return strings.Contains(frame, "mapped") && !pfmcall.IsBusy(frame)
	})
	submit := decodeEnvelope(t, fix.waitRecorded("UserPromptSubmit", 1)[0])
	if submit.Event != "UserPromptSubmit" || submit.Prompt != "map it" || submit.SessionID != fixtureSession ||
		submit.TranscriptPath != transcriptPath || submit.AgentID != "" {
		t.Fatalf("UserPromptSubmit payload = %+v", submit)
	}
	pre := fix.waitRecorded("PreToolUse", 1)[0]
	if envelope := decodeEnvelope(t, pre); envelope.Event != "PreToolUse" || envelope.ToolName != "Agent" {
		t.Fatalf("PreToolUse payload = %+v", envelope)
	}
	if frame := session.out.frame(); !strings.Contains(frame, "FIXTURE-DENY") {
		t.Fatalf("the deny reason never reached the pane:\n%s", frame)
	}
	entries := parsedEntries(t, transcriptPath)
	if len(entries) < 3 || entries[0].Role != "user" || entries[1].Role != transcript.RoleTool ||
		entries[1].Tool != "Agent" || entries[len(entries)-1].Text != "mapped" {
		t.Fatalf("transcript entries = %+v, want user, tool Agent, assistant mapped", entries)
	}
	if meta := readMeta(t, transcriptPath); meta.HumanPrompts != 1 {
		t.Fatalf("the SessionStart additionalContext counted as a human prompt: %+v", meta)
	}

	session.typeLine("/reload --model opus")
	session.waitFrame(
		"the block reason",
		func(frame string) bool { return strings.Contains(frame, "reload scheduled") },
	)
	reload := fix.waitRecorded("UserPromptSubmit", 2)[1]
	var blocked, blockErr bytes.Buffer
	front := func(_ context.Context, args []string, _, _ io.Writer, _ config.Runtime) int {
		if len(args) != 2 || args[0] != "--model" || args[1] != "opus" {
			t.Errorf("reload front got %q", args)
		}
		return 0
	}
	if code := hookentry.ReloadIntercept(
		strings.NewReader(reload),
		&blocked,
		&blockErr,
		config.Runtime{},
		front,
	); code != 0 ||
		!strings.Contains(blocked.String(), `"decision":"block"`) {
		t.Fatalf(
			"pfm's reload-intercept read the payload as exit=%d out=%q err=%q",
			code,
			blocked.String(),
			blockErr.String(),
		)
	}
	if meta := readMeta(t, transcriptPath); meta.HumanPrompts != 1 {
		t.Fatalf("a blocked prompt reached the transcript: %+v", meta)
	}

	session.typeLine("e")
	session.waitFrame("the e prompt answered", func(frame string) bool {
		return strings.Count(session.out.all(), DefaultReply) >= 1 || strings.Contains(frame, DefaultReply)
	})
	exitWord := fix.waitRecorded("UserPromptSubmit", 3)[2]
	killed := false
	kill := func(_ context.Context, args []string, _, _ io.Writer, _ ...config.Runtime) int {
		killed = len(args) == 2 && args[0] == "--self" && args[1] == "--exit"
		return 0
	}
	var quiet, quietErr bytes.Buffer
	if code := hookentry.ExitIntercept(
		strings.NewReader(exitWord),
		&quiet,
		&quietErr,
		config.Runtime{},
		kill,
	); code != 0 ||
		!killed {
		t.Fatalf("pfm's exit-intercept read %q as exit=%d killed=%v", exitWord, code, killed)
	}

	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	end := fix.waitRecorded("SessionEnd", 1)[0]
	if envelope := decodeEnvelope(t, end); envelope.Event != "SessionEnd" || envelope.Reason != "prompt_input_exit" {
		t.Fatalf("SessionEnd payload = %+v", envelope)
	}
	var closeErr bytes.Buffer
	if code := hookentry.ExitClose(
		strings.NewReader(end),
		&closeErr,
	); code != 0 ||
		strings.Contains(closeErr.String(), "decode") {
		t.Fatalf("pfm's exit-close read the payload as exit=%d stderr=%q", code, closeErr.String())
	}
}

// TestClaudePromptHookErrorStopsTheTurnInsteadOfFallingOpen covers F2: a
// UserPromptSubmit handler that cannot be read must not fall through as if it
// had approved — the pane would otherwise be indistinguishable from a hook
// that ran cleanly.
func TestClaudePromptHookErrorStopsTheTurnInsteadOfFallingOpen(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installBrokenHook("UserPromptSubmit")
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit = %d, want %d (a broken prompt hook must not fall open)", code, ExitUnpinned)
	}
	if !strings.Contains(session.stderr.String(), "prompt hooks") {
		t.Fatalf("stderr = %q, want it to name the failed prompt hooks", session.stderr.String())
	}
}

// TestClaudeToolHookErrorStopsTheTurnInsteadOfFallingOpen covers F2's other
// arm: a PreToolUse handler that cannot be read must not paint the tool as
// having run and been approved.
func TestClaudeToolHookErrorStopsTheTurnInsteadOfFallingOpen(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installBrokenHook("PreToolUse")
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepToolCall, Tool: "Agent", Input: json.RawMessage(`{}`)},
		{Type: StepTurn, Reply: "unreachable"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit = %d, want %d (a broken tool hook must not fall open)", code, ExitUnpinned)
	}
	if !strings.Contains(session.stderr.String(), "tool Agent") {
		t.Fatalf("stderr = %q, want it to name the failed tool hook", session.stderr.String())
	}
}
