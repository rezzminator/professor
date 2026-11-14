package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/transcript"
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
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"FIXTURE-DENY"}}' ;;
esac
`
	if err := testjail.WriteExecutable(recorder, []byte(script), 0o700); err != nil {
		fix.t.Fatal(err)
	}
	statusScript := filepath.Join(fix.root, "statusline.sh")
	if err := testjail.WriteExecutable(statusScript, []byte(`#!/bin/sh
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
			"refreshInterval":      fix.statusRefresh,
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

func writeHookSettings(t *testing.T, path string, hooks map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustJSON(t, map[string]any{"hooks": hooks}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commandHook(command string) []any {
	return []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}
}

func inlineHookSettings(t *testing.T, hooks map[string]any, status string) string {
	t.Helper()
	document := map[string]any{"hooks": hooks}
	if status != "" {
		document["statusLine"] = map[string]any{"type": "command", "command": status, "refreshInterval": 2}
	}
	return string(mustJSON(t, document))
}

func TestClaudeSettingsErrorsNameTheirSource(t *testing.T) {
	for _, test := range []struct{ name, value, want string }{
		{"inline-json", "{", "mock-engine: decode --settings: unexpected end of JSON input\n"},
		{"missing-path", "/missing/claude-settings.json", "mock-engine: read --settings /missing/claude-settings.json: open /missing/claude-settings.json: no such file or directory\n"},
		{"invalid-matcher", `{"hooks":{"PreToolUse":[{"matcher":"[","hooks":[{"type":"command","command":"true"}]}]}}`, "mock-engine: --settings: PreToolUse matcher \"[\": error parsing regexp: missing closing ]: `[`\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			code, _, stderr := runOnce(fix, "claude", []string{"-p", "--settings", test.value}, "hello")
			if code != ExitUsage || stderr != test.want {
				t.Fatalf("exit %d stderr = %q, want %q", code, stderr, test.want)
			}
		})
	}
	t.Run("undecodable-path", func(t *testing.T) {
		fix := newFixture(t)
		t.Chdir(fix.work)
		path := filepath.Join(fix.root, "broken-settings.json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runOnce(fix, "claude", []string{"-p", "--settings", path}, "hello")
		if got, want := strings.ReplaceAll(
			stderr,
			fix.root,
			"{root}",
		), "mock-engine: decode --settings {root}/broken-settings.json: unexpected end of JSON input\n"; code != ExitUsage ||
			got != want {
			t.Fatalf("exit %d stderr = %q, want %q", code, got, want)
		}
	})
}

func TestClaudeQuietSuppressesInlineHooksAndStatusLine(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	marker := filepath.Join(fix.recordDir, "quiet-ran")
	command := `printf 'ran\n' >> "` + marker + `"`
	settings := inlineHookSettings(t, map[string]any{
		hookSessionStart: commandHook(command), hookUserPromptSubmit: commandHook(command),
		hookPreToolUse: commandHook(command), hookStop: commandHook(command), hookSessionEnd: commandHook(command),
	}, command+`; printf 'VISIBLE STATUS\n'`)
	fix.write(
		Scenario{
			SessionID: fixtureSession,
			BusyMS:    intPtr(0),
			Quiet:     true,
			Steps:     []Step{{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"}},
		},
	)
	session := fix.startTUI("claude", []string{"--settings", settings}, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	session.waitFrame("finished", func(frame string) bool { return strings.Contains(frame, "finished") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("quiet hook/status ran: %v", err)
	}
	if strings.Contains(session.out.all(), "VISIBLE STATUS") {
		t.Fatal("quiet status line appeared")
	}
}

func TestClaudeBrokenProjectSettingsFailLaunchNamingTheFile(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	path := filepath.Join(fix.work, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runOnce(fix, "claude", []string{"-p"}, "prompt")
	if got, want := strings.ReplaceAll(
		stderr,
		fix.root,
		"{root}",
	), "mock-engine: decode {root}/work/repo/.claude/settings.json: unexpected end of JSON input\n"; code != ExitUsage ||
		got != want {
		t.Fatalf("exit %d stderr = %q, want %q", code, got, want)
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
// (clear_kill.go:30-32,
// epic_pfmcall.go:21-22); the decisions themselves are judged by the handlers.
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

// installBrokenHook writes a settings.json whose handler for event prints
// malformed JSON stdout, which runHookCommand refuses to decode.
func (fix *fixture) installBrokenHook(event string) {
	fix.t.Helper()
	broken := filepath.Join(fix.root, "broken-hook.sh")
	if err := testjail.WriteExecutable(broken, []byte("#!/bin/sh\nprintf '{not json'\n"), 0o700); err != nil {
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
