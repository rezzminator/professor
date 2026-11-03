package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/chat"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	opencodeengine "github.com/rezzminator/professor/pfm/internal/engine/opencode"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/statusline"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const fixtureSession = "b1111111-1111-4111-8111-111111111111"

func TestClaudeRemoteControlTitleDoesNotConsumeATurn(t *testing.T) {
	for _, row := range []struct {
		name               string
		args               []string
		remoteName, prompt string
	}{
		{"named", []string{"--remote-control", "carried-chat", "--session-id", fixtureSession, "--name", "carried-chat"}, "carried-chat", ""},
		{"bare", []string{"--remote-control", "--session-id", fixtureSession, "--name", "carried-chat"}, "", ""},
		{"inline", []string{"--remote-control=carried-chat", "--session-id", fixtureSession, "--name", "carried-chat"}, "carried-chat", ""},
		{"prompt", []string{"--remote-control", "carried-chat", "--session-id", fixtureSession, "--name", "carried-chat", "inspect atlas"}, "carried-chat", "inspect atlas"},
	} {
		t.Run(row.name, func(t *testing.T) {
			actual := parseClaudeArgs(row.args)
			if actual.remoteName != row.remoteName || actual.prompt != row.prompt ||
				actual.sessionID != fixtureSession ||
				actual.name != "carried-chat" {
				t.Fatalf(
					"argv=%q: remote=%q prompt=%q session=%q name=%q, want %q %q %q carried-chat",
					row.args,
					actual.remoteName,
					actual.prompt,
					actual.sessionID,
					actual.name,
					row.remoteName,
					row.prompt,
					fixtureSession,
				)
			}
			fix := newFixture(t)
			fix.write(Scenario{Steps: []Step{{Type: StepTurn, Reply: "scripted response"}}})
			session := fix.startTUI(engineClaude, row.args, nil)
			if row.prompt == "" {
				session.waitFrame("idle composer", func(frame string) bool { return strings.Contains(frame, "❯") })
				session.typeLine("inspect atlas")
			}
			session.waitFrame(
				"one scripted response",
				func(frame string) bool { return strings.Contains(frame, "scripted response") },
			)
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("argv=%q: exit=%d stderr=%q, want 0", row.args, code, session.stderr.String())
			}
			if actual := readFile(t, fix.scenario+cursorSuffix); actual != "1" {
				t.Fatalf("%s: cursor=%q, want 1", fix.scenario, actual)
			}
		})
	}
}

const verbsStdioProfessorArg = "mockengine-verbs-stdio-professor"

func init() {
	if len(os.Args) < 2 || os.Args[1] != verbsStdioProfessorArg {
		return
	}
	os.Exit(serveVerbsStdioProfessor())
}

func serveVerbsStdioProfessor() int {
	// This test subprocess is a composition root, like cmd/pfm/engines.go.
	index.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	index.RegisterSource(pfmengine.Codex, codexengine.Source{})
	index.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
	gather.RegisterMatcher(pfmengine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(pfmengine.Codex, codexengine.Matcher{})
	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: resolve paths: %v\n", err)
		return 1
	}
	service, err := mcpserv.NewConfigured("test", os.Stderr, mcpserv.Runtime{
		Paths: resolved, Chat: chat.Verbs{Warnings: os.Stderr}, AllowAmbientIdentity: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: configure chat: %v\n", err)
		return 1
	}
	defer func() {
		if err := service.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "verbs stdio professor: close chat: %v\n", err)
		}
	}()
	professor, err := mcpserv.NewFamilyServers(mcpserv.FamilyServerOptions{Version: "test", Chat: service})
	if err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: %v\n", err)
		return 1
	}
	if err := professor.RunStdio(context.Background(), os.Stdin, os.Stdout, mcpserv.StdioOptions{
		Home: resolved.Home, SIDDir: resolved.SIDDir, Warnings: os.Stderr,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "verbs stdio professor: %v\n", err)
		return 1
	}
	return 0
}

// claudeArgs is the argv pfm's spawn template hands a Claude chat
// (internal/action/claude_spawn.go): the caller's words, then LaunchArgs.
func claudeArgs(extra ...string) []string {
	return append(
		extra,
		"--settings",
		`{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}`,
		"--model",
		"claude-sonnet-4-5-fixture",
	)
}

func (fix *fixture) claudeTranscript(sessionID string) string {
	return filepath.Join(fix.configDir, "projects", claudeProjectSlug(fix.work), sessionID+".jsonl")
}

// indexedTranscripts runs pfm's own Claude indexer over the jail and returns
// what it stored — the judge of every transcript the mock wrote.
func (fix *fixture) indexedTranscripts() []store.Transcript {
	fix.t.Helper()
	database, err := store.Open(store.WithWarningWriter(os.Stderr))
	if err != nil {
		fix.t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			fix.t.Errorf("close store: %v", err)
		}
	}()
	ctx := context.Background()
	counters := index.Counters{}
	if err := index.SyncClaude(
		ctx,
		database,
		[]string{filepath.Join(fix.configDir, "projects")},
		&counters,
	); err != nil {
		fix.t.Fatalf("index Claude transcripts: %v", err)
	}
	if counters.FilesSeen == 0 {
		fix.t.Fatalf("the indexer saw no transcript under %s", filepath.Join(fix.configDir, "projects"))
	}
	rows, err := database.Transcripts(ctx)
	if err != nil {
		fix.t.Fatal(err)
	}
	return rows
}

func readMeta(t *testing.T, path string) transcript.Meta {
	t.Helper()
	meta, err := transcript.ReadMeta(path, string(pfmengine.Claude))
	if err != nil {
		t.Fatalf("ReadMeta %s: %v", path, err)
	}
	return meta
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestClaudeNameFlagAndRenameLandAsCustomTitle(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	session := fix.startTUI("claude", claudeArgs("--name", "worker 7", "audit the firewall"), nil)
	session.waitFrame("the launch prompt answered", func(frame string) bool {
		return strings.Contains(frame, DefaultReply) && !pfmcall.IsBusy(frame)
	})
	rows := fix.indexedTranscripts()
	if len(rows) != 1 || rows[0].CustomTitle != "worker 7" || rows[0].FirstPrompt != "audit the firewall" {
		t.Fatalf("indexed = %+v, want title from --name and the launch prompt recorded", rows)
	}
	session.typeLine("/rename fresh-name")
	waitFile(t, fix.claudeTranscript(fixtureSession), func(content string) bool {
		return strings.Contains(content, "fresh-name")
	})
	rows = fix.indexedTranscripts()
	if len(rows) != 1 || rows[0].CustomTitle != "fresh-name" || rows[0].PromptCount != 1 {
		t.Fatalf("indexed after /rename = %+v, want the new title and no extra prompt", rows)
	}
}

func intPtr(value int) *int { return &value }

func TestClaudeMenuIsAnOpenSelectorUntilAnswered(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{
		{Type: StepMenu, Options: []string{"Yes", "No, and tell Claude what to do differently"}, Selected: 1},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("do the thing")
	frame := session.waitFrame("an open menu pfmcall.SelectorLine reads", func(frame string) bool {
		return pfmcall.SelectorLine(frame) != ""
	})
	if selector := pfmcall.SelectorLine(frame); !strings.Contains(selector, "1. Yes") {
		t.Fatalf("selector line = %q, want the preselected first option", selector)
	}
	if pfmcall.IsBusy(frame) {
		t.Fatalf("an open menu must not read as a busy turn:\n%s", frame)
	}
	session.typeRaw("\r")
	session.waitFrame("the menu closed", func(frame string) bool {
		return pfmcall.SelectorLine(frame) == "" && strings.Contains(frame, "❯")
	})
}

func TestClaudeCrashStepExitsMidTurnWithItsCode(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{{Type: StepCrash, ExitCode: 7}}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	if code := session.waitExit(); code != 7 {
		t.Fatalf("exit code = %d, want the crash step's 7", code)
	}
	if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 1 {
		t.Fatalf("the prompt that crashed the turn was not recorded: %+v", meta)
	}
}

func TestClaudeResumeAppendsToTheSameTranscript(t *testing.T) {
	for _, args := range [][]string{{"--resume", fixtureSession}, {"--continue"}, {"-c"}} {
		t.Run(args[0], func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{BusyMS: intPtr(0), Steps: []Step{{Type: StepTurn}, {Type: StepTurn}}})
			first := fix.startTUI("claude", claudeArgs("--session-id", fixtureSession), nil)
			first.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			first.typeLine("one")
			first.waitFrame("the first reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
			first.typeLine("/exit")
			if code := first.waitExit(); code != 0 {
				t.Fatalf("first exit = %d", code)
			}
			second := fix.startTUI("claude", claudeArgs(args...), nil)
			second.waitFrame("the resumed composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			second.typeLine("two")
			second.waitFrame(
				"the second reply",
				func(frame string) bool { return strings.Contains(frame, DefaultReply) },
			)
			if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 2 {
				t.Fatalf("resumed transcript ReadMeta = %+v, want 2 human prompts in one file", meta)
			}
			if rows := fix.indexedTranscripts(); len(rows) != 1 || rows[0].PromptCount != 2 ||
				rows[0].LastPrompt != "two" {
				t.Fatalf("indexed = %+v, want one row with both prompts", rows)
			}
		})
	}
}

func TestClaudeSessionIDFlagNamesTheTranscript(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	const selected = "c2222222-2222-4222-8222-222222222222"
	session := fix.startTUI("claude", claudeArgs("--session-id", selected), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("chosen id")
	session.waitFrame("the reply", func(frame string) bool { return strings.Contains(frame, DefaultReply) })
	if meta := readMeta(t, fix.claudeTranscript(selected)); meta.HumanPrompts != 1 {
		t.Fatalf("selected transcript meta=%+v", meta)
	}
	if rows := fix.indexedTranscripts(); len(rows) != 1 || rows[0].UUID != selected {
		t.Fatalf("indexed transcripts=%+v, want selected id", rows)
	}
}

func TestClaudeInlineTurnsKeepThePositionalCursor(t *testing.T) {
	for _, row := range []struct {
		name, directive, reply string
		refused                bool
	}{
		{"object", `{"type":"turn","reply":"R1"}`, "R1", false},
		{"compact", `[{"type":"compact"},{"type":"turn","reply":"fallback"}]`, "fallback", false},
		{"unknown type", `{"type":"nope"}`, `step 0: unknown step type "nope"`, true},
		{"broken JSON", `{"type":`, "decode step: unexpected EOF", true},
		{"unknown field", `{"type":"turn","delay":3}`, `decode step: json: unknown field "delay"`, true},
		{"trailing JSON", `{"type":"turn"} {}`, "unexpected JSON after inline steps", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{SessionID: fixtureSession, Reply: "fallback", BusyMS: intPtr(0), Steps: []Step{
				{Type: StepTurn, Reply: "positional"},
			}})
			session := fix.startTUI("claude", claudeArgs(), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			prompt := "hi mock-engine: " + row.directive
			session.typeLine(prompt)
			if row.refused {
				code := session.waitExit()
				expected := "mock-engine: inline steps refused — " + row.reply + "\n"
				if code != ExitUnpinned || session.stderr.String() != expected {
					t.Fatalf("refusal=(%d,%q), want (%d,%q)", code, session.stderr.String(), ExitUnpinned, expected)
				}
				entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
				if len(entries) != 1 || entries[0].Text != prompt {
					t.Fatalf("refused transcript=%+v, want submitted prompt only", entries)
				}
				return
			}
			frame := session.waitFrame("a completed turn", func(frame string) bool {
				return !pfmcall.IsBusy(frame) && strings.Contains(frame, "⏺ ")
			})
			want := "⏺ " + row.reply
			if row.refused {
				want = "⏺ mock-engine: inline steps refused — "
			}
			if !strings.Contains(frame, want) || !strings.Contains(frame, row.reply) {
				t.Fatalf("inline reply lacks %q and %q:\n%s", want, row.reply, frame)
			}
			entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
			if len(entries) != 2 || entries[0].Text != prompt {
				t.Fatalf("inline transcript = %+v, want the whole prompt and a reply", entries)
			}
			if _, err := os.Stat(fix.scenario + cursorSuffix); !os.IsNotExist(err) {
				t.Fatalf("inline turn moved the positional cursor: %v", err)
			}
			if row.name == "compact" &&
				strings.Count(readFile(t, fix.claudeTranscript(fixtureSession)), `"compact_boundary"`) != 1 {
				t.Fatal("the inline compaction did not run")
			}
			session.typeLine("plain prompt")
			session.waitFrame("the pending positional reply", func(frame string) bool {
				return strings.Contains(frame, "⏺ positional")
			})
			if got := readFile(t, fix.scenario+cursorSuffix); got != "1" {
				t.Fatalf("plain turn cursor=%q, want 1", got)
			}
		})
	}
}

func TestClaudeInlineArrayAndQueuedPromptsHaveTheirOwnSteps(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(
		Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{{Type: StepTurn, Reply: "positional"}}},
	)
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	first := `first mock-engine: [{"type":"background_agent","name":"a"},{"type":"turn","reply":"R2","busy_ms":500}]`
	second := `queued mock-engine: {"type":"turn","reply":"queued-reply"}`
	session.typeLine(first)
	session.waitFrame("the busy turn", pfmcall.IsBusy)
	session.typeLine(second)
	session.typeLine("plain queued prompt")
	session.waitFrame("all queued turns", func(frame string) bool { return strings.Contains(frame, "⏺ positional") })
	frame := session.out.frame()
	for _, needle := range []string{"⏺ R2", "⏺ queued-reply", "❯ ● a  running (background)"} {
		if !strings.Contains(frame, needle) {
			t.Fatalf("queued pane lacks %q:\n%s", needle, frame)
		}
	}
	entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
	if len(entries) != 7 || entries[0].Text != first || entries[1].Role != "tool" || entries[1].Tool != "Agent" ||
		entries[3].Text != second || entries[6].Text != "positional" {
		t.Fatalf("queued transcript = %+v", entries)
	}
	sidechains, err := filepath.Glob(
		filepath.Join(strings.TrimSuffix(fix.claudeTranscript(fixtureSession), ".jsonl"), "subagents", "*.jsonl"),
	)
	if err != nil || len(sidechains) != 1 || !strings.Contains(readFile(t, sidechains[0]), `"isSidechain":true`) {
		t.Fatalf("sidechains=%v err=%v", sidechains, err)
	}
	if got := readFile(t, fix.scenario+cursorSuffix); got != "1" {
		t.Fatalf("queued cursor=%q, want only the plain turn consumed", got)
	}
}

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
