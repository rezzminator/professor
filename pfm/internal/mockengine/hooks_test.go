package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestRunHookCommandTimeoutDoesNotWaitForOrphan(t *testing.T) {
	start := time.Now()
	_, err := runHookCommand(
		context.Background(),
		"sleep 5; true",
		nil,
		t.TempDir(),
		os.Environ(),
		200*time.Millisecond,
	)
	elapsed := time.Since(start)
	if err == nil || err.Error() != "timed out after 200ms" {
		t.Fatalf("runHookCommand error = %v, want timed out after 200ms", err)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("runHookCommand returned after %s, want under 2s despite the orphan", elapsed)
	}
}

func TestRunHookCommandReadsAnswerWithoutWaitingForBackgroundChild(t *testing.T) {
	command := `printf '%s\n' '{"decision":"block","reason":"orphan"}'; sleep 5 &`
	start := time.Now()
	answer, err := runHookCommand(context.Background(), command, nil, t.TempDir(), os.Environ(), 10*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("runHookCommand: %v", err)
	}
	if answer.Decision != "block" || answer.Reason != "orphan" {
		t.Fatalf("runHookCommand answer = %+v, want block with reason orphan", answer)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("runHookCommand returned after %s, want under 2s despite the background child", elapsed)
	}
}

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
		document["statusLine"] = map[string]any{"type": "command", "command": status}
	}
	return string(mustJSON(t, document))
}

func TestClaudeSettingsLayersFireAllPlayedEventsInOrder(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	order := filepath.Join(fix.recordDir, "settings-order")
	events := []string{
		hookSessionStart, hookUserPromptSubmit, hookPreToolUse, hookPostToolUse, hookStop, hookSessionEnd,
	}
	layer := func(name string) map[string]any {
		hooks := map[string]any{}
		for _, event := range events {
			command := `printf '%s\n' '` + name + `:` + event + `' >> "` + order + `"`
			hooks[event] = commandHook(command)
		}
		return hooks
	}
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), layer("account"))
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), layer("project"))
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"},
	}})
	session := fix.startTUI("claude", []string{"--settings", inlineHookSettings(t, layer("inline"), "")}, nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	session.waitFrame("the reply", func(frame string) bool { return strings.Contains(frame, "finished") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	var want []string
	for _, event := range events {
		for _, name := range []string{"account", "project", "inline"} {
			want = append(want, name+":"+event)
		}
	}
	got := strings.Split(strings.TrimSpace(readFile(t, order)), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%q, want %q", got, want)
	}
}

func TestClaudeInlineStopFiresAtEveryTurnEnd(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	stop := filepath.Join(fix.recordDir, "inline-stop")
	settings := inlineHookSettings(t, map[string]any{
		hookStop: commandHook(`printf 'stop\n' >> "` + stop + `"`),
	}, "")
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	session := fix.startTUI("claude", []string{"--settings", settings}, nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("one")
	session.waitFrame("first reply", func(frame string) bool { return strings.Contains(frame, "⏺ ok") })
	session.typeLine("two")
	session.waitFrame("second reply", func(_ string) bool { return strings.Count(session.out.all(), "⏺ ok") >= 2 })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	if got := readFile(t, stop); got != "stop\nstop\n" {
		t.Fatalf("Stop ran %q", got)
	}
}

func TestClaudeSettingsErrorsNameTheirSource(t *testing.T) {
	for _, test := range []struct{ name, value, source, event string }{
		{"inline-json", "{", "--settings", ""},
		{"missing-path", "/missing/claude-settings.json", "--settings /missing/claude-settings.json", ""},
		{"invalid-matcher", `{"hooks":{"PreToolUse":[{"matcher":"[","hooks":[{"type":"command","command":"true"}]}]}}`, "--settings", "PreToolUse"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			code, _, stderr := runOnce(fix, "claude", []string{"-p", "--settings", test.value}, "hello")
			if code != ExitUsage || !strings.Contains(stderr, test.source) ||
				(test.event != "" && !strings.Contains(stderr, test.event)) {
				t.Fatalf("exit=%d stderr=%q, want source %q event %q", code, stderr, test.source, test.event)
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
		if code != ExitUsage || !strings.Contains(stderr, "--settings") || !strings.Contains(stderr, path) {
			t.Fatalf("exit=%d stderr=%q, want --settings and %s", code, stderr, path)
		}
	})
}

func TestClaudeSettingsPathStatusPrecedenceMatchersAndDedup(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	account := filepath.Join(fix.configDir, "settings.json")
	project := filepath.Join(fix.work, ".claude", "settings.json")
	path := filepath.Join(fix.root, "inline-settings.json")
	order := filepath.Join(fix.recordDir, "matching")
	mark := func(label string) string { return `printf '%s\n' '` + label + `' >> "` + order + `"` }
	entry := func(matcher, command string) map[string]any {
		return map[string]any{"matcher": matcher, "hooks": []any{map[string]any{"type": "command", "command": command}}}
	}
	write := func(filename string, hooks map[string]any, status string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(inlineHookSettings(t, hooks, status)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	shared := mark("once")
	write(
		account,
		map[string]any{hookPreToolUse: []any{entry("*", shared), entry("", mark("all")), entry("Bash", mark("bash"))}},
		mark("status-account"),
	)
	write(project, map[string]any{hookPreToolUse: []any{entry("Bash", mark("project-bash"))}}, mark("status-project"))
	write(
		path,
		map[string]any{
			hookPreToolUse: []any{
				entry("*", shared),
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": mark("omitted")}}},
			},
		},
		mark("status-inline"),
	)
	set, status, err := loadClaudeHooks(fix.configDir, fix.work, path)
	if err != nil {
		t.Fatal(err)
	}
	if status != mark("status-inline") {
		t.Fatalf("status=%q", status)
	}
	for _, tool := range []string{"Bash", "Write"} {
		if _, err := set.fire(
			context.Background(),
			hookPreToolUse,
			tool,
			hookPayload{Event: hookPreToolUse},
			fix.work,
			os.Environ(),
			io.Discard,
		); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"once", "all", "bash", "project-bash", "omitted", "once", "all", "omitted"}
	if got := strings.Split(strings.TrimSpace(readFile(t, order)), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("matching/dedup=%q want %q", got, want)
	}
	if _, status, err := loadClaudeHooks(fix.configDir, fix.work, ""); err != nil || status != mark("status-project") {
		t.Fatalf("project status=%q err=%v", status, err)
	}
	if err := os.Remove(project); err != nil {
		t.Fatal(err)
	}
	if _, status, err := loadClaudeHooks(fix.configDir, fix.work, ""); err != nil || status != mark("status-account") {
		t.Fatalf("account status=%q err=%v", status, err)
	}
}

func TestClaudeInlineAsyncRunsWithoutApplyingItsAnswer(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	marker := filepath.Join(fix.recordDir, "async")
	command := `printf 'ran\n' >> "` + marker + `"; printf '%s\n' '{"decision":"block","reason":"async block","hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"async deny","additionalContext":"async context"}}'`
	settings := inlineHookSettings(t, map[string]any{
		hookUserPromptSubmit: []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "async": true}}},
		},
		hookPreToolUse: []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "async": true}}},
		},
	}, "")
	fix.write(
		Scenario{
			SessionID: fixtureSession,
			BusyMS:    intPtr(0),
			Steps:     []Step{{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "continued"}},
		},
	)
	session := fix.startTUI("claude", []string{"--settings", settings}, nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	session.waitFrame("continued", func(frame string) bool { return strings.Contains(frame, "continued") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	if got := readFile(t, marker); got != "ran\nran\n" {
		t.Fatalf("async runs=%q", got)
	}
	transcriptText := readFile(t, fix.claudeTranscript(fixtureSession))
	if strings.Contains(transcriptText, "async context") || strings.Contains(transcriptText, "async deny") ||
		strings.Contains(transcriptText, "async block") {
		t.Fatalf("async answer applied: %s", transcriptText)
	}
}

func TestClaudeInlinePromptBlockAndToolDenial(t *testing.T) {
	t.Run("prompt exits two", func(t *testing.T) {
		fix := newFixture(t)
		t.Chdir(fix.work)
		settings := inlineHookSettings(t, map[string]any{
			hookUserPromptSubmit: commandHook(`printf 'inline blocked\n' >&2; exit 2`),
		}, "")
		fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
		session := fix.startTUI("claude", []string{"--settings", settings}, nil)
		session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
		session.typeLine("blocked prompt")
		session.waitFrame("block reason", func(frame string) bool { return strings.Contains(frame, "inline blocked") })
		session.typeLine("/exit")
		if code := session.waitExit(); code != 0 {
			t.Fatalf("exit=%d", code)
		}
		if transcriptText := readFile(
			t,
			fix.claudeTranscript(fixtureSession),
		); strings.Contains(
			transcriptText,
			"blocked prompt",
		) {
			t.Fatalf("blocked prompt reached transcript: %s", transcriptText)
		}
	})
	t.Run("first prompt block wins", func(t *testing.T) {
		fix := newFixture(t)
		t.Chdir(fix.work)
		block := func(reason string) []any { return commandHook(`printf '%s\n' '` + reason + `' >&2; exit 2`) }
		writeHookSettings(
			t,
			filepath.Join(fix.configDir, "settings.json"),
			map[string]any{hookUserPromptSubmit: block("account blocked")},
		)
		writeHookSettings(
			t,
			filepath.Join(fix.work, ".claude", "settings.json"),
			map[string]any{hookUserPromptSubmit: block("project blocked")},
		)
		settings := inlineHookSettings(t, map[string]any{hookUserPromptSubmit: block("inline blocked")}, "")
		fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
		session := fix.startTUI("claude", []string{"--settings", settings}, nil)
		session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
		session.typeLine("blocked prompt")
		session.waitFrame(
			"account block",
			func(frame string) bool { return strings.Contains(frame, "account blocked") },
		)
		session.typeLine("/exit")
		if code := session.waitExit(); code != 0 {
			t.Fatalf("exit=%d", code)
		}
		frame := session.out.all()
		if strings.Contains(frame, "Blocked by hook: project blocked") ||
			strings.Contains(frame, "Blocked by hook: inline blocked") {
			t.Fatalf("later block replaced first: %s", frame)
		}
	})
	t.Run("inline tool denial and matching id", func(t *testing.T) {
		fix := newFixture(t)
		t.Chdir(fix.work)
		payloadFile := filepath.Join(fix.recordDir, "pre-payload")
		command := `cat > "` + payloadFile + `"; printf '%s\n' '{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"inline denied"}}'`
		settings := inlineHookSettings(t, map[string]any{hookPreToolUse: commandHook(command)}, "")
		fix.write(
			Scenario{
				SessionID: fixtureSession,
				BusyMS:    intPtr(0),
				Steps:     []Step{{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"}},
			},
		)
		session := fix.startTUI("claude", []string{"--settings", settings}, nil)
		session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
		session.typeLine("go")
		session.waitFrame("denial", func(frame string) bool { return strings.Contains(frame, "inline denied") })
		session.typeLine("/exit")
		if code := session.waitExit(); code != 0 {
			t.Fatalf("exit=%d", code)
		}
		var payload struct {
			ToolUseID string `json:"tool_use_id"`
		}
		if err := json.Unmarshal([]byte(readFile(t, payloadFile)), &payload); err != nil {
			t.Fatal(err)
		}
		transcriptText := readFile(t, fix.claudeTranscript(fixtureSession))
		if payload.ToolUseID == "" || !strings.Contains(transcriptText, `"id":"`+payload.ToolUseID+`"`) ||
			!strings.Contains(transcriptText, `"content":"inline denied"`) {
			t.Fatalf("tool_use_id=%q transcript=%s", payload.ToolUseID, transcriptText)
		}
	})
	t.Run("first denial wins", func(t *testing.T) {
		fix := newFixture(t)
		t.Chdir(fix.work)
		deny := func(reason string) []any {
			return commandHook(
				`printf '%s\n' '{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"` + reason + `"}}'`,
			)
		}
		writeHookSettings(
			t,
			filepath.Join(fix.configDir, "settings.json"),
			map[string]any{hookPreToolUse: deny("account denied")},
		)
		writeHookSettings(
			t,
			filepath.Join(fix.work, ".claude", "settings.json"),
			map[string]any{hookPreToolUse: deny("project denied")},
		)
		settings := inlineHookSettings(t, map[string]any{hookPreToolUse: deny("inline denied")}, "")
		fix.write(
			Scenario{
				SessionID: fixtureSession,
				BusyMS:    intPtr(0),
				Steps:     []Step{{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"}},
			},
		)
		session := fix.startTUI("claude", []string{"--settings", settings}, nil)
		session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
		session.typeLine("go")
		session.waitFrame("denial", func(frame string) bool { return strings.Contains(frame, "account denied") })
		session.typeLine("/exit")
		if code := session.waitExit(); code != 0 {
			t.Fatalf("exit=%d", code)
		}
		transcriptText := readFile(t, fix.claudeTranscript(fixtureSession))
		if !strings.Contains(transcriptText, `"content":"account denied"`) ||
			strings.Contains(transcriptText, `"content":"inline denied"`) {
			t.Fatalf("wrong denial: %s", transcriptText)
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
		key := hook.Event + "|" + strings.TrimPrefix(hook.Command, filepath.Join(bin, "pfm")+" ")
		if seen[key] != 1 {
			t.Errorf("%s ran %d times, want 1; records: %q", key, seen[key], got)
		}
	}
}

func TestClaudeProjectHooksMergeWithSeatHooksAndStopEachTurn(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	order := filepath.Join(fix.recordDir, "hook-order")
	command := func(label, event, answer string) string {
		return `printf '%s:%s\n' '` + label + `' "$CLAUDE_PROJECT_DIR" >> "` + order + `"; cat >> "` + fix.recordDir + `/` + event + `.jsonl"; printf '\n' >> "` + fix.recordDir + `/` + event + `.jsonl"; ` + answer
	}
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
		"PreToolUse": commandHook(
			command("seat-pre", "seat-pre", `printf '%s\n' '{"hookSpecificOutput":{"permissionDecision":"allow"}}'`),
		),
		"Stop": commandHook(
			command(
				"seat-stop",
				"Stop",
				`printf '%s\n' '{"decision":"block","reason":"ignored stop answer"}'; printf 'Stop failure\n' >&2; exit 2`,
			),
		),
	})
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
		"PreToolUse": commandHook(
			command(
				"project-pre",
				"project-pre",
				`printf '%s\n' '{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"project denied"}}'`,
			),
		),
		"Stop": commandHook(command("project-stop", "project-Stop", ":")),
	})
	fix.write(
		Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{{Type: StepToolCall, Tool: "Write"}}},
	)
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("first turn")
	session.waitFrame("the completed reply", func(frame string) bool { return strings.Contains(frame, "⏺ ok") })
	content := readFile(t, fix.claudeTranscript(fixtureSession))
	if !strings.Contains(content, `"is_error":true`) || !strings.Contains(content, "project denied") {
		t.Fatalf("project hook denial did not reach the transcript: %s", content)
	}
	session.typeLine("second turn")
	lines := fix.waitRecorded("Stop", 2)
	fix.waitRecorded("project-Stop", 2)
	for _, line := range lines {
		var payload hookEnvelope
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Event != "Stop" || payload.SessionID != fixtureSession || payload.CWD != fix.work ||
			payload.TranscriptPath != fix.claudeTranscript(fixtureSession) {
			t.Fatalf("Stop payload=%+v", payload)
		}
	}
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("Stop ended the process: %d", code)
	}
	if len(fix.recorded("Stop")) != 2 || len(fix.recorded("project-Stop")) != 2 {
		t.Fatal("Stop did not run once per turn")
	}
	want := ""
	for _, label := range []string{"seat-pre", "project-pre", "seat-stop", "project-stop", "seat-stop", "project-stop"} {
		want += label + ":" + fix.work + "\n"
	}
	if got := readFile(t, order); got != want {
		t.Fatalf("hook order/env=%q, want %q", got, want)
	}
	if got := readFile(
		t,
		filepath.Join(fix.recordDir, "hook-Stop.json"),
	); !strings.Contains(got, "Stop failure") ||
		!strings.Contains(got, `"exit_code":2`) {
		t.Fatalf("Stop failure telemetry=%s", got)
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
	if code != ExitUsage || !strings.Contains(stderr, path) {
		t.Fatalf("exit=%d stderr=%q, want %s", code, stderr, path)
	}
}

func TestClaudeHeadlessStopRunsOnceAfterTheAssistant(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	stop := filepath.Join(fix.recordDir, "Stop.jsonl")
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
		"Stop": commandHook(
			`cat >> "` + stop + `"; printf '\n' >> "` + stop + `"; tail -1 "` + fix.claudeTranscript(
				fixtureSession,
			) + `" > "` + fix.recordDir + `/stop-assistant"`,
		),
	})
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	code, stdout, stderr := runOnce(fix, "claude", []string{"-p", "--output-format", "json"}, "hello")
	if code != 0 || !strings.Contains(stdout, `"result":"ok"`) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(fix.recorded("Stop")) != 1 ||
		!strings.Contains(readFile(t, filepath.Join(fix.recordDir, "stop-assistant")), `"role":"assistant"`) {
		t.Fatal("headless Stop did not follow exactly one assistant record")
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
