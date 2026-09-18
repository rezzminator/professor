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
	"time"

	"hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/hookentry"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/statusline"
	"hostops/pfm/internal/transcript"
)

// installHooks writes the settings.json pfm's installer would converge
// (internal/installer/expected_hooks.go:90-103, settings.go:730-743): one
// recorder script stands in for every `pfm internal …` handler, tees its
// stdin into <record>/<event>.jsonl, and answers the way the real handler
// would for the fixture prompts.
func (fix *fixture) installHooks() {
	fix.t.Helper()
	recorder := filepath.Join(fix.root, "recorder.sh")
	script := `#!/bin/sh
event="$1"
payload="$(cat)"
printf '%s\n' "$payload" >> "` + fix.recordDir + `/$event.jsonl"
case "$event" in
  SessionStart)
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"CTX-FIXTURE"}}' ;;
  UserPromptSubmit)
    if printf '%s' "$payload" | grep -q '"prompt":"/reload'; then
      printf '%s\n' '{"decision":"block","reason":"reload scheduled — this chat reboots when the current turn ends","suppressOriginalPrompt":true}'
    fi ;;
  PreToolUse)
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Explore is disabled"}}' ;;
esac
`
	if err := os.WriteFile(recorder, []byte(script), 0o700); err != nil {
		fix.t.Fatal(err)
	}
	statusScript := filepath.Join(fix.root, "statusline.sh")
	if err := os.WriteFile(statusScript, []byte(`#!/bin/sh
cat >> "`+fix.recordDir+`/statusline.jsonl"
printf '\n' >> "`+fix.recordDir+`/statusline.jsonl"
printf 'SL-FIXTURE\n'
`), 0o700); err != nil {
		fix.t.Fatal(err)
	}
	entry := func(matcher, event string) map[string]any {
		return map[string]any{
			"matcher": matcher,
			"hooks":   []any{map[string]any{"type": "command", "command": recorder + " " + event}},
		}
	}
	document := map[string]any{
		"hooks": map[string]any{
			"SessionStart":     []any{entry("", "SessionStart")},
			"UserPromptSubmit": []any{entry("", "UserPromptSubmit")},
			"PreToolUse":       []any{entry("Agent|Task", "PreToolUse")},
			"SessionEnd":       []any{entry("", "SessionEnd")},
		},
		"statusLine": map[string]any{
			"type":                 "command",
			"command":              statusScript,
			"padding":              0,
			"refreshInterval":      3,
			"hideVimModeIndicator": true,
		},
	}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		fix.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix.configDir, "settings.json"), content, 0o600); err != nil {
		fix.t.Fatal(err)
	}
}

// recorded returns the payload lines one hook event received so far.
func (fix *fixture) recorded(event string) []string {
	fix.t.Helper()
	content, err := os.ReadFile(filepath.Join(fix.recordDir, event+".jsonl"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(content)), "\n")
}

func (fix *fixture) waitRecorded(event string, count int) []string {
	fix.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if lines := fix.recorded(event); len(lines) >= count {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
	fix.t.Fatalf("%s hook fired %d time(s), want %d", event, len(fix.recorded(event)), count)
	return nil
}

// hookEnvelope mirrors the keys every handler table row reads
// (internal/hookentry/compact_nudge.go:16-19, clear_kill.go:30-32,
// epic_inject.go:21-22); the decisions themselves are judged by the handlers.
type hookEnvelope struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	Event          string `json:"hook_event_name"`
	Source         string `json:"source"`
	Reason         string `json:"reason"`
	Prompt         string `json:"prompt"`
	ToolName       string `json:"tool_name"`
	AgentID        string `json:"agent_id"`
}

func decodeEnvelope(t *testing.T, line string) hookEnvelope {
	t.Helper()
	var envelope hookEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode hook payload %q: %v", line, err)
	}
	return envelope
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
		return strings.Contains(frame, "mapped") && !inject.IsBusy(frame)
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
	var denied, denyErr bytes.Buffer
	if code := hookentry.ExploreDeny(strings.NewReader(pre), &denied, &denyErr); code != 0 ||
		!strings.Contains(denied.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("pfm's explore-deny read the payload as exit=%d out=%q: %s", code, denied.String(), pre)
	}
	if frame := session.out.frame(); !strings.Contains(frame, "Explore is disabled") {
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
	front := func(args []string, _, _ io.Writer, _ config.Runtime) int {
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
	kill := func(args []string, _, _ io.Writer, _ ...config.Runtime) int {
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

// installBrokenHook writes a settings.json whose handler for event prints
// malformed JSON stdout, which runHookCommand refuses to decode.
func (fix *fixture) installBrokenHook(event string) {
	fix.t.Helper()
	broken := filepath.Join(fix.root, "broken-hook.sh")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nprintf '{not json'\n"), 0o700); err != nil {
		fix.t.Fatal(err)
	}
	document := map[string]any{"hooks": map[string]any{
		event: []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": broken}}}},
	}}
	content, err := json.Marshal(document)
	if err != nil {
		fix.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix.configDir, "settings.json"), content, 0o600); err != nil {
		fix.t.Fatal(err)
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

func parsedEntries(t *testing.T, path string) []transcript.Entry {
	t.Helper()
	entries := make([]transcript.Entry, 0)
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		if entry, ok := transcript.Parse([]byte(line), string(pfmengine.Claude)); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

// quietCommands is a closed CommandRunner: a statusline render in this test
// never shells out to git.
type quietCommands struct{}

func (quietCommands) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, os.ErrNotExist
}

func TestClaudeStatuslineInputRendersThroughPfmsOwnDecoder(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	// --model on the command line wins over the scenario's model, as on a
	// real launch; the display name is the family word the statusline shows.
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Model: "claude-opus-4-1", Steps: []Step{
		{
			Type:   StepTurn,
			Reply:  "rendered",
			Tokens: &Tokens{Input: 50, Output: 5, CacheRead: 1000, CacheCreation: 10, ContextWindow: 200000},
		},
	}})
	session := fix.startTUI("claude", claudeArgs("--name", "status seat", "--effort", "high"), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("paint me")
	session.waitFrame("the statusline output at the pane bottom", func(frame string) bool {
		return strings.Contains(frame, "rendered") && strings.Contains(frame, "SL-FIXTURE")
	})
	lines := fix.waitRecorded("statusline", 1)
	raw := lines[len(lines)-1]
	sidDir := filepath.Join(fix.root, "sl-sid")
	rendered, err := statusline.Render(context.Background(), []byte(raw), statusline.Runtime{
		Now:          func() time.Time { return time.Unix(1_786_838_400, 0) },
		Home:         fix.home,
		ConfigDir:    fix.configDir,
		CacheDir:     filepath.Join(fix.root, "sl-cache"),
		RateLimitDir: filepath.Join(fix.root, "sl-rates"),
		SIDDir:       sidDir,
		TmuxDir:      filepath.Join(fix.root, "sl-tmux"),
		ProcRoot:     filepath.Join(fix.root, "sl-proc"),
		UID:          1000,
		Engine:       pfmengine.Claude,
		Env:          map[string]string{},
		Command:      quietCommands{},
		Spawn:        func(statusline.RefreshKind) error { return nil },
	})
	if err != nil {
		t.Fatalf("pfm's statusline decoder refused the mock's input: %v\n%s", err, raw)
	}
	for _, want := range []string{"status seat", "repo", "Sonnet"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered statusline %q lacks %q (input %s)", rendered, want, raw)
		}
	}
	var input struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		ContextWindow  struct {
			UsedPercentage float64 `json:"used_percentage"`
			CurrentUsage   struct {
				InputTokens int64 `json:"input_tokens"`
				CacheRead   int64 `json:"cache_read_input_tokens"`
			} `json:"current_usage"`
		} `json:"context_window"`
		Effort struct {
			Level string `json:"level"`
		} `json:"effort"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	if input.SessionID != fixtureSession || input.TranscriptPath != fix.claudeTranscript(fixtureSession) ||
		input.ContextWindow.CurrentUsage.InputTokens != 50 || input.ContextWindow.CurrentUsage.CacheRead != 1000 ||
		input.ContextWindow.UsedPercentage <= 0 || input.Effort.Level != "high" {
		t.Fatalf("statusline input = %s", raw)
	}
}
